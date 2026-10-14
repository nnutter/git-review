/**
 * git-review extension.
 *
 * Reads `git-review status --needs-review --json` for the current Git feature branch and
 * publishes the counts on `git-review:status`. Outside a Git repository it
 * publishes nothing.
 *
 * Recap (and anything else) can ask for a fresh post with
 * `git-review:request-status`. The request object may set `refresh(cwd)`.
 *
 * On settle, when feedback exists and no review process is running, the extension tells
 * the model to use the git-review skill. The same count is not sent again
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
	GIT_REVIEW_READY_EVENT,
	GIT_REVIEW_REQUEST_STATUS_EVENT,
	GIT_REVIEW_STATUS_EVENT,
	type GitReviewStatus,
	type GitReviewStatusRequest,
} from "./git-review/support.ts";

const GIT_TIMEOUT_MS = 5_000;
const STATUS_TIMEOUT_MS = 15_000;
const IDLE_POLL_MS = 5_000;

type AfterOnStopHook = (ctx: ExtensionContext, aborted: boolean) => Promise<void> | void;

export default function (pi: ExtensionAPI) {
	let miseCoordinates = false;
	let lastNudgedFeedback: number | undefined;
	let checkInFlight: Promise<void> | undefined;
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

	/** Publish status for a repository. Returns undefined when there is nothing to publish. */
	async function readAndPublish(cwd: string): Promise<GitReviewStatus | undefined> {
		if (!(await isRepository(cwd))) return undefined;
		const result = await pi.exec("git-review", ["status", "--needs-review", "--json"], {
			cwd,
			timeout: STATUS_TIMEOUT_MS,
		});
		if (result.killed || result.code !== 0) return undefined;
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

	async function checkFeedback(ctx: ExtensionContext, aborted: boolean, idleOnly: boolean, epoch: number): Promise<void> {
		const status = await readAndPublish(ctx.cwd);
		if (!status || epoch !== generation) return;
		if (status.feedback === 0) {
			lastNudgedFeedback = undefined;
			return;
		}
		if (aborted || status.feedback === lastNudgedFeedback) return;
		const activity = await pi.exec("git-review", ["running", "--json"], {
			cwd: ctx.cwd,
			timeout: STATUS_TIMEOUT_MS,
		});
		if (activity.killed || activity.code !== 0) return;
		const parsed: unknown = JSON.parse(activity.stdout);
		if (parsed === null || typeof parsed !== "object" || Array.isArray(parsed)) return;
		const { version, running } = parsed as { version?: unknown; running?: unknown };
		if (version !== 1 || running !== false || epoch !== generation) return;
		if (idleOnly && (!ctx.isIdle() || !idleNoticesAllowed)) return;
		lastNudgedFeedback = status.feedback;
		nudge(ctx, status.feedback);
	}

	async function onStop(ctx: ExtensionContext, aborted: boolean, idleOnly = false): Promise<void> {
		if (!idleOnly) idleNoticesAllowed = false;
		const epoch = generation;
		while (checkInFlight) await checkInFlight;
		if (epoch !== generation) return;
		const pending = checkFeedback(ctx, aborted, idleOnly, epoch);
		checkInFlight = pending;
		try {
			await pending;
		} finally {
			checkInFlight = undefined;
			if (!idleOnly && epoch === generation) idleNoticesAllowed = !aborted;
		}
	}

	function stopMonitoring(): void {
		generation++;
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

	pi.on("session_start", async (_event, ctx) => {
		stopMonitoring();
		lastNudgedFeedback = undefined;
		idleNoticesAllowed = true;
		if (ctx.mode === "tui" || ctx.mode === "rpc") {
			idleTimer = setInterval(() => {
				if (!ctx.isIdle() || !idleNoticesAllowed || checkInFlight) return;
				void onStop(ctx, false, true).catch(() => {
					// Status is optional. Retry on the next idle check.
				});
			}, IDLE_POLL_MS);
			idleTimer.unref();
		}
		try {
			await readAndPublish(ctx.cwd);
		} catch {
			// Status is optional.
		}
	});

	pi.on("agent_start", () => {
		idleNoticesAllowed = false;
	});

	pi.on("session_shutdown", () => {
		stopMonitoring();
	});

	pi.on("agent_settled", async (event, ctx) => {
		if (miseCoordinates) return;
		try {
			await onStop(ctx, event.aborted === true);
		} catch {
			// Status is optional.
		}
	});
}
