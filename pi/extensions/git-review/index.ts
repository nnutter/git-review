/**
 * git-review extension.
 *
 * Reads `git-review status --needs-review --json` for the current Git feature branch and
 * publishes the `commits` counts on `git-review:status`. Outside a Git repository it
 * publishes nothing. Optional lifecycle checks do not delay event dispatch;
 * explicit refresh requests remain awaitable. `files` reviews use `"command":"files"` and are addressed
 * as new commits, not fixups; they hold the same worktree lock so
 * `status --is-reviewing` defers notices during either review.
 *
 * Recap (and anything else) can ask for a fresh post with
 * `git-review:request-status`. The request object may set `refresh(cwd)`.
 *
 * On settle, when `commits` feedback exists and no `commits` or `files` review
 * is running (`status --is-reviewing`), the extension tells the model to use
 * the git-review skill. The same count is not sent again
 * until it changes, so an unanswered notice does not loop. That notice
 * runs from `mise-hooks:after-on-stop` when mise-hooks is loaded, so it
 * follows the mise on-stop hook. Otherwise it runs on `agent_settled`.
 * Interactive and RPC sessions also check every five seconds while idle.
 * Aborted runs suspend idle notices until a non-aborted run settles.
 */

import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";
import {
	feedbackNotice,
	MISE_AFTER_ON_STOP_EVENT,
	MISE_COORDINATES_EVENT,
	parseGitReviewStatus,
	parseIsReviewing,
	GIT_REVIEW_READY_EVENT,
	GIT_REVIEW_REQUEST_STATUS_EVENT,
	GIT_REVIEW_STATUS_EVENT,
	type GitReviewStatus,
	type GitReviewStatusRequest,
} from "./support.ts";

const GIT_TIMEOUT_MS = 5_000;
const STATUS_TIMEOUT_MS = 15_000;
const IDLE_POLL_MS = 5_000;

type AfterOnStopHook = (ctx: ExtensionContext, aborted: boolean) => Promise<void> | void;
type StopCheck = { ctx: ExtensionContext; aborted: boolean; idleOnly: boolean; epoch: number; revision: number };

export default function (pi: ExtensionAPI) {
	let miseCoordinates = false;
	let lastNudgedFeedback: number | undefined;
	let checkInFlight: Promise<void> | undefined;
	let pendingStop: StopCheck | undefined;
	const statusReads = new Map<string, Promise<GitReviewStatus | undefined>>();
	let noticeRevision = 0;
	let idleTimer: ReturnType<typeof setInterval> | undefined;
	let generation = 0;
	let idleNoticesAllowed = true;

	async function isRepository(cwd: string): Promise<boolean> {
		const result = await pi.exec("git", ["rev-parse", "--is-inside-work-tree"], {
			cwd,
			timeout: GIT_TIMEOUT_MS,
		});
		return !result.killed && result.code === 0 && result.stdout.trim() === "true";
	}

	/** Coalesce readers without making optional checks delay lifecycle handlers. */
	function readAndPublish(cwd: string): Promise<GitReviewStatus | undefined> {
		const epoch = generation;
		const key = JSON.stringify([epoch, cwd]);
		const existing = statusReads.get(key);
		if (existing) return existing;
		const pending = readStatus(cwd, epoch).finally(() => {
			if (statusReads.get(key) === pending) statusReads.delete(key);
		});
		statusReads.set(key, pending);
		return pending;
	}

	async function readStatus(cwd: string, epoch: number): Promise<GitReviewStatus | undefined> {
		if (!(await isRepository(cwd)) || epoch !== generation) return undefined;
		const result = await pi.exec("git-review", ["status", "--needs-review", "--json"], {
			cwd,
			timeout: STATUS_TIMEOUT_MS,
		});
		if (result.killed || result.code !== 0 || epoch !== generation) return undefined;
		const status = parseGitReviewStatus(result.stdout);
		if (!status) return undefined;
		pi.events.emit(GIT_REVIEW_STATUS_EVENT, {
			needsReview: status.needsReview,
			total: status.total,
			feedback: status.feedback,
			line: status.line,
		});
		return status;
	}

	function nudge(ctx: ExtensionContext, count: number): void {
		const notice = feedbackNotice(count);
		if (ctx.isIdle()) pi.sendUserMessage(notice);
		else pi.sendUserMessage(notice, { deliverAs: "followUp" });
		if (!ctx.hasUI) console.error(notice);
	}

	async function checkFeedback({ ctx, aborted, idleOnly, epoch, revision }: StopCheck): Promise<void> {
		const status = await readAndPublish(ctx.cwd);
		if (!status || epoch !== generation || revision !== noticeRevision) return;
		if (status.feedback === 0) {
			lastNudgedFeedback = undefined;
			return;
		}
		if (aborted || status.feedback === lastNudgedFeedback) return;
		const activity = await pi.exec("git-review", ["status", "--is-reviewing", "--json"], {
			cwd: ctx.cwd,
			timeout: STATUS_TIMEOUT_MS,
		});
		if (activity.killed || activity.code !== 0) return;
		const reviewing = parseIsReviewing(activity.stdout);
		if (reviewing !== false || epoch !== generation || revision !== noticeRevision) return;
		if (idleOnly && (!ctx.isIdle() || !idleNoticesAllowed)) return;
		lastNudgedFeedback = status.feedback;
		nudge(ctx, status.feedback);
	}

	function onStop(ctx: ExtensionContext, aborted: boolean, idleOnly = false): void {
		if (!idleOnly) {
			idleNoticesAllowed = false;
			noticeRevision++;
		}
		const request = { ctx, aborted, idleOnly, epoch: generation, revision: noticeRevision };
		if (checkInFlight) {
			if (!idleOnly) pendingStop = request;
			return;
		}
		runStopCheck(request);
	}

	function runStopCheck(request: StopCheck): void {
		const pending = checkFeedback(request).catch(() => {
			// Status is optional. A later request or idle check can retry.
		});
		checkInFlight = pending;
		void pending.finally(() => {
			if (checkInFlight !== pending) return;
			checkInFlight = undefined;
			if (request.epoch !== generation) return;
			if (!request.idleOnly && request.revision === noticeRevision) idleNoticesAllowed = !request.aborted;
			const next = pendingStop;
			pendingStop = undefined;
			if (next && next.epoch === generation && next.revision === noticeRevision) runStopCheck(next);
		});
	}

	function stopMonitoring(): void {
		generation++;
		noticeRevision++;
		pendingStop = undefined;
		checkInFlight = undefined;
		statusReads.clear();
		if (idleTimer) clearInterval(idleTimer);
		idleTimer = undefined;
	}

	pi.events.on(MISE_COORDINATES_EVENT, () => {
		miseCoordinates = true;
	});

	pi.events.on(MISE_AFTER_ON_STOP_EVENT, (data) => {
		if (!Array.isArray(data)) return;
		const hooks = data as AfterOnStopHook[];
		hooks.push((ctx, aborted) => onStop(ctx, aborted));
	});

	pi.events.on(GIT_REVIEW_REQUEST_STATUS_EVENT, (data) => {
		if (data === null || typeof data !== "object" || Array.isArray(data)) return;
		(data as GitReviewStatusRequest).refresh = async (cwd: string) => {
			await readAndPublish(cwd);
		};
	});

	// Announce after the listeners above are registered. Mise may already
	// be loaded, in which case it answers this event. If mise loads later,
	// it emits MISE_COORDINATES_EVENT itself.
	pi.events.emit(GIT_REVIEW_READY_EVENT, undefined);

	pi.on("session_start", (_event, ctx) => {
		stopMonitoring();
		lastNudgedFeedback = undefined;
		idleNoticesAllowed = true;
		if (ctx.mode === "tui" || ctx.mode === "rpc") {
			idleTimer = setInterval(() => {
				if (!ctx.isIdle() || !idleNoticesAllowed || checkInFlight) return;
				onStop(ctx, false, true);
			}, IDLE_POLL_MS);
			idleTimer.unref();
		}
		void readAndPublish(ctx.cwd).catch(() => {
			// Status is optional.
		});
	});

	pi.on("agent_start", () => {
		idleNoticesAllowed = false;
		noticeRevision++;
		pendingStop = undefined;
	});

	pi.on("session_shutdown", () => {
		stopMonitoring();
	});

	pi.on("agent_settled", (event, ctx) => {
		if (miseCoordinates) return;
		onStop(ctx, event.aborted === true);
	});
}
