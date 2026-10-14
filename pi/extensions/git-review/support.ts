/**
 * Pure helpers for the git-review extension.
 *
 * No Pi imports: node:test can load this file outside the Pi process.
 * `git-review status --needs-review --json` prints a result with a summary of { state, count }.
 * Failures print a versioned object and exit non-zero. Callers check
 * the exit code before parsing.
 */

export const GIT_REVIEW_STATUS_EVENT = "git-review:status";
export const GIT_REVIEW_REQUEST_STATUS_EVENT = "git-review:request-status";

/** Mise emits this with a mutable hook list at the end of on-stop. */
export const MISE_AFTER_ON_STOP_EVENT = "mise-hooks:after-on-stop";

/** git-review emits this at load. Mise answers with MISE_COORDINATES_EVENT. */
export const GIT_REVIEW_READY_EVENT = "git-review:ready";
export const MISE_COORDINATES_EVENT = "mise-hooks:coordinates-on-stop";

const REQUIRED_STATES = ["needs_review", "reviewing", "feedback", "incomplete", "approved"] as const;

export type GitReviewStatus = {
	needsReview: number;
	total: number;
	feedback: number;
	line: string;
};

export type GitReviewStatusRequest = {
	refresh?: (cwd: string) => Promise<void>;
};

export function reviewStatusLine(needsReview: number, total: number): string {
	return `Needs Review: ${needsReview}/${total}`;
}

/**
 * Parse successful `git-review status --needs-review --json` stdout.
 * Returns undefined for unsupported results, malformed rows, or a missing required state.
 * `total` is the sum of every state in summary, including states this parser does not name.
 */
export function parseGitReviewStatus(stdout: string): GitReviewStatus | undefined {
	let parsed: unknown;
	try {
		parsed = JSON.parse(stdout);
	} catch {
		return undefined;
	}
	if (parsed === null || typeof parsed !== "object" || Array.isArray(parsed)) return undefined;
	const result = parsed as { version?: unknown; command?: unknown; status?: unknown; summary?: unknown };
	if (result.version !== 1 || result.command !== "status") return undefined;
	if (result.status !== "pending" && result.status !== "idle") return undefined;
	if (!Array.isArray(result.summary)) return undefined;

	const counts = new Map<string, number>();
	for (const entry of result.summary) {
		if (entry === null || typeof entry !== "object") return undefined;
		const state = (entry as { state?: unknown }).state;
		const count = (entry as { count?: unknown }).count;
		if (typeof state !== "string" || state.length === 0) return undefined;
		if (typeof count !== "number" || !Number.isInteger(count) || count < 0) return undefined;
		if (counts.has(state)) return undefined;
		counts.set(state, count);
	}
	for (const state of REQUIRED_STATES) {
		if (!counts.has(state)) return undefined;
	}

	let total = 0;
	for (const count of counts.values()) total += count;
	const needsReview = counts.get("needs_review") ?? 0;
	if (needsReview > total) return undefined;
	const feedback = counts.get("feedback") ?? 0;
	return {
		needsReview,
		total,
		feedback,
		line: reviewStatusLine(needsReview, total),
	};
}

/** Accept only the status object git-review publishes. */
export function reviewLineFromEvent(data: unknown): string | undefined {
	if (data === null || typeof data !== "object") return undefined;
	const record = data as { needsReview?: unknown; total?: unknown; line?: unknown; feedback?: unknown };
	const { needsReview, total, line, feedback } = record;
	if (typeof needsReview !== "number" || !Number.isInteger(needsReview) || needsReview < 0) return undefined;
	if (typeof total !== "number" || !Number.isInteger(total) || total < 0) return undefined;
	if (needsReview > total) return undefined;
	if (typeof feedback !== "number" || !Number.isInteger(feedback) || feedback < 0) return undefined;
	const expected = reviewStatusLine(needsReview, total);
	if (line !== expected) return undefined;
	return expected;
}

export function feedbackNotice(count: number): string {
	if (count === 1) {
		return "git-review has 1 commit in the feedback state. Use the git-review skill to address it.";
	}
	return `git-review has ${count} commits in the feedback state. Use the git-review skill to address them.`;
}
