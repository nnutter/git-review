import assert from "node:assert/strict";
import test from "node:test";
import { parseGitReviewStatus } from "../support.ts";

const summary = [
	{ state: "needs_review", count: 0 },
	{ state: "reviewing", count: 0 },
	{ state: "feedback", count: 0 },
	{ state: "incomplete", count: 0 },
	{ state: "approved", count: 3 },
];

test("status parser reads idle summary and includes additional states in total", () => {
	const result = { version: 1, command: "status", status: "idle", entries: [], summary: [...summary, { state: "future", count: 2 }] };
	assert.deepEqual(parseGitReviewStatus(JSON.stringify(result)), {
		needsReview: 0, total: 5, feedback: 0, line: "Needs Review: 0/5",
	});
});

test("status parser rejects unsupported envelopes and invalid summaries", () => {
	const result = { version: 1, command: "status", status: "pending", summary };
	for (const invalid of [
		null,
		summary,
		{ ...result, version: 2 },
		{ ...result, command: "needs-review" },
		{ ...result, status: "error" },
		{ ...result, summary: undefined },
		{ ...result, summary: summary.slice(1) },
		{ ...result, summary: [...summary, summary[0]] },
		...[-1, 0.5, "1"].map(count => ({ ...result, summary: [...summary.slice(0, -1), { state: "approved", count }] })),
	]) {
		assert.equal(parseGitReviewStatus(JSON.stringify(invalid)), undefined);
	}
	assert.equal(parseGitReviewStatus("not json"), undefined);
});
