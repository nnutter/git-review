/**
 * Umpire extension.
 *
 * Reads `umpire status --json` for the current Git feature branch and
 * publishes the counts on `umpire:status`. Outside a Git repository it
 * publishes nothing.
 *
 * Recap (and anything else) can ask for a fresh post with
 * `umpire:request-status`. The request object may set `refresh(cwd)`.
 *
 * On settle, when the feedback count is non-zero, the extension tells
 * the model to use the umpire skill. The same count is not sent again
 * until it changes, so an unanswered notice does not loop. That notice
 * runs from `mise-hooks:after-on-stop` when mise-hooks is loaded, so it
 * follows the mise on-stop hook. Otherwise it runs on `agent_settled`.
 */

import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";
import {
	feedbackNotice,
	MISE_AFTER_ON_STOP_EVENT,
	MISE_COORDINATES_EVENT,
	parseUmpireStatus,
	UMPIRE_READY_EVENT,
	UMPIRE_REQUEST_STATUS_EVENT,
	UMPIRE_STATUS_EVENT,
	type UmpireStatus,
	type UmpireStatusRequest,
} from "./umpire/support.ts";

const GIT_TIMEOUT_MS = 5_000;
const STATUS_TIMEOUT_MS = 15_000;

type AfterOnStopHook = (ctx: ExtensionContext, aborted: boolean) => Promise<void> | void;

export default function (pi: ExtensionAPI) {
	let miseCoordinates = false;
	let lastNudgedFeedback: number | undefined;

	async function isRepository(cwd: string): Promise<boolean> {
		const result = await pi.exec("git", ["rev-parse", "--is-inside-work-tree"], {
			cwd,
			timeout: GIT_TIMEOUT_MS,
		});
		return !result.killed && result.code === 0 && result.stdout.trim() === "true";
	}

	/** Publish status for a repository. Returns undefined when there is nothing to publish. */
	async function readAndPublish(cwd: string): Promise<UmpireStatus | undefined> {
		if (!(await isRepository(cwd))) return undefined;
		const result = await pi.exec("umpire", ["status", "--json"], {
			cwd,
			timeout: STATUS_TIMEOUT_MS,
		});
		if (result.killed || result.code !== 0) return undefined;
		const status = parseUmpireStatus(result.stdout);
		if (!status) return undefined;
		pi.events.emit(UMPIRE_STATUS_EVENT, {
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

	async function onStop(ctx: ExtensionContext, aborted: boolean): Promise<void> {
		const status = await readAndPublish(ctx.cwd);
		if (!status) return;
		if (status.feedback === 0) {
			lastNudgedFeedback = undefined;
			return;
		}
		if (aborted || status.feedback === lastNudgedFeedback) return;
		lastNudgedFeedback = status.feedback;
		nudge(ctx, status.feedback);
	}

	pi.events.on(MISE_COORDINATES_EVENT, () => {
		miseCoordinates = true;
	});

	pi.events.on(MISE_AFTER_ON_STOP_EVENT, (data) => {
		if (!Array.isArray(data)) return;
		const hooks = data as AfterOnStopHook[];
		hooks.push((ctx, aborted) => onStop(ctx, aborted));
	});

	pi.events.on(UMPIRE_REQUEST_STATUS_EVENT, (data) => {
		if (data === null || typeof data !== "object" || Array.isArray(data)) return;
		(data as UmpireStatusRequest).refresh = async (cwd: string) => {
			await readAndPublish(cwd);
		};
	});

	// Announce after the listeners above are registered. Mise may already
	// be loaded, in which case it answers this event. If mise loads later,
	// it emits MISE_COORDINATES_EVENT itself.
	pi.events.emit(UMPIRE_READY_EVENT, undefined);

	pi.on("session_start", async (_event, ctx) => {
		lastNudgedFeedback = undefined;
		try {
			await readAndPublish(ctx.cwd);
		} catch {
			// Status is optional.
		}
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
