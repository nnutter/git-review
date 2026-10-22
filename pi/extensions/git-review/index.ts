/**
 * git-review extension.
 *
 * Reads `git-review status --needs-review --json` for the current Git feature branch and
 * publishes the `commits` counts on `git-review:status`. Outside a Git repository it
 * publishes nothing. Optional lifecycle checks do not delay event dispatch;
 * explicit refresh requests remain awaitable.
 *
 * Recap (and anything else) can ask for a fresh post with
 * `git-review:request-status`. The request object may set `refresh(cwd)`.
 *
 * The extension never messages the model on its own. The user starts feedback
 * handling with `/feedback`.
 *
 * Status publishes on settle. When mise-hooks is loaded, publishing runs from
 * `mise-hooks:after-on-stop` instead, so it follows the mise on-stop hook.
 * Interactive and RPC sessions also refresh every five seconds while idle.
 */

import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";
import {
	MISE_AFTER_ON_STOP_EVENT,
	MISE_COORDINATES_EVENT,
	parseGitReviewStatus,
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

export default function (pi: ExtensionAPI) {
	let miseCoordinates = false;
	const statusReads = new Map<string, Promise<GitReviewStatus | undefined>>();
	let idleTimer: ReturnType<typeof setInterval> | undefined;
	let generation = 0;

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

	function stopMonitoring(): void {
		generation++;
		statusReads.clear();
		if (idleTimer) clearInterval(idleTimer);
		idleTimer = undefined;
	}

	pi.registerCommand("feedback", {
		description: "Ask the model to retrieve and address git-review feedback",
		handler: async (_args, ctx) => {
			const request = "Use the git-review skill to retrieve current feedback with `git-review feedback --json` and address it.";
			if (ctx.isIdle()) pi.sendUserMessage(request);
			else pi.sendUserMessage(request, { deliverAs: "followUp" });
		},
	});

	pi.events.on(MISE_COORDINATES_EVENT, () => {
		miseCoordinates = true;
	});

	pi.events.on(MISE_AFTER_ON_STOP_EVENT, (data) => {
		if (!Array.isArray(data)) return;
		const hooks = data as AfterOnStopHook[];
		hooks.push((ctx) => readAndPublish(ctx.cwd).then(() => undefined, () => undefined));
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
		if (ctx.mode === "tui" || ctx.mode === "rpc") {
			idleTimer = setInterval(() => {
				if (!ctx.isIdle()) return;
				void readAndPublish(ctx.cwd).catch(() => {
					// Status is optional.
				});
			}, IDLE_POLL_MS);
			idleTimer.unref();
		}
		void readAndPublish(ctx.cwd).catch(() => {
			// Status is optional.
		});
	});

	pi.on("session_shutdown", () => {
		stopMonitoring();
	});

	pi.on("agent_settled", (_event, ctx) => {
		if (miseCoordinates) return;
		void readAndPublish(ctx.cwd).catch(() => {
			// Status is optional.
		});
	});
}
