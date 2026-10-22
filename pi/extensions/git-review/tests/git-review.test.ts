import assert from "node:assert/strict";
import { EventEmitter } from "node:events";
import test from "node:test";
import { setImmediate } from "node:timers/promises";
import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";
import gitReview from "../index.ts";
import { GIT_REVIEW_REQUEST_STATUS_EVENT, MISE_AFTER_ON_STOP_EVENT, MISE_COORDINATES_EVENT, type GitReviewStatusRequest } from "../support.ts";

function harness(mode: ExtensionContext["mode"] = "tui") {
	const events = new EventEmitter();
	const handlers = new Map<string, Function>();
	const messages: string[] = [];
	const published: unknown[] = [];
	let feedback = 1;
	let activity = { code: 0, killed: false, stdout: '{"version":1,"command":"status","is_reviewing":true}' };
	let idle = true;
	let activityGate: Promise<void> | undefined;
	let statusGate: Promise<void> | undefined;
	let statusCalls = 0;
	let activityCalls = 0;
	const ctx = { cwd: "/repo", mode, hasUI: true, isIdle: () => idle } as ExtensionContext;
	const pi = {
		events,
		on: (name: string, handler: Function) => handlers.set(name, handler),
		exec: async (command: string, args: string[]) => {
			if (command === "git") return { code: 0, killed: false, stdout: "true" };
			assert.equal(command, "git-review");
			if (args[0] === "status" && args[1] === "--is-reviewing") {
				activityCalls++;
				await activityGate;
				assert.deepEqual(args, ["status", "--is-reviewing", "--json"]);
				return activity;
			}
			assert.deepEqual(args, ["status", "--needs-review", "--json"]);
			statusCalls++;
			await statusGate;
			return { code: 0, killed: false, stdout: JSON.stringify({ version: 1, command: "status", status: "pending", summary: [
				{ state: "needs_review", count: 2 },
				{ state: "reviewing", count: 1 },
				{ state: "feedback", count: feedback },
				{ state: "incomplete", count: 0 },
				{ state: "approved", count: 0 },
			] }) };
		},
		sendUserMessage: (message: string) => messages.push(message),
	} as unknown as ExtensionAPI;
	events.on("git-review:status", data => published.push(data));
	gitReview(pi);
	return {
		events, messages, published, ctx,
		setActivity: (result: typeof activity) => { activity = result; },
		setFeedback: (count: number) => { feedback = count; },
		setIdle: (value: boolean) => { idle = value; },
		setActivityGate: (gate: Promise<void>) => { activityGate = gate; },
		setStatusGate: (gate: Promise<void>) => { statusGate = gate; },
		statusCalls: () => statusCalls,
		activityCalls: () => activityCalls,
		emitRaw: async (name: string, event = {}) => { await handlers.get(name)?.(event, ctx); },
		emit: async (name: string, event = {}) => {
			await handlers.get(name)?.(event, ctx);
			await setImmediate();
		},
	};
}

for (const coordinated of [false, true]) {
	test(`defer feedback until review ends (${coordinated ? "mise" : "settle"})`, async () => {
		const h = harness();
		let settle = () => h.emit("agent_settled");
		if (coordinated) {
			h.events.emit(MISE_COORDINATES_EVENT);
			const hooks: ((ctx: ExtensionContext, aborted: boolean) => Promise<void>)[] = [];
			h.events.emit(MISE_AFTER_ON_STOP_EVENT, hooks);
			settle = async () => {
				for (const hook of hooks) await hook(h.ctx, false);
				await setImmediate();
			};
		}
		await settle();
		assert.equal(h.messages.length, 0);
		assert.equal(h.published.length, 1, "UI still receives review counts");
		h.setActivity({ code: 0, killed: false, stdout: '{"version":1,"command":"status","is_reviewing":false}' });
		await settle();
		assert.equal(h.messages.length, 1, "suppression does not consume the notice");
		await settle();
		assert.equal(h.messages.length, 1, "unchanged feedback does not loop");
		h.setFeedback(0);
		await settle();
		h.setFeedback(1);
		await settle();
		assert.equal(h.messages.length, 2, "feedback can recur after it clears");
	});
}

for (const mode of ["tui", "rpc"] as const) {
	test(`idle monitoring delivers deferred feedback once and stops at shutdown (${mode})`, async t => {
		t.mock.timers.enable({ apis: ["setInterval"] });
		const h = harness(mode);
		t.after(() => h.emit("session_shutdown"));
		await h.emit("session_start");
		// Mise owns settle, but idle monitoring must still deliver after review ends.
		h.events.emit(MISE_COORDINATES_EVENT);
		t.mock.timers.tick(5_000);
		await setImmediate();
		assert.equal(h.messages.length, 0);
		h.setActivity({ code: 0, killed: false, stdout: '{"version":1,"command":"status","is_reviewing":false}' });
		h.setIdle(false);
		t.mock.timers.tick(5_000);
		await setImmediate();
		assert.equal(h.messages.length, 0, "polling does not interrupt an active run");
		h.setIdle(true);
		t.mock.timers.tick(5_000);
		await setImmediate();
		assert.equal(h.messages.length, 1);
		t.mock.timers.tick(5_000);
		await setImmediate();
		assert.equal(h.messages.length, 1);
		await h.emit("session_shutdown");
		const count = h.published.length;
		t.mock.timers.tick(10_000);
		await setImmediate();
		assert.equal(h.published.length, count, "shutdown removes the timer");
	});
}

test("idle checks wait for the coordinated on-stop hook", async t => {
	t.mock.timers.enable({ apis: ["setInterval"] });
	const h = harness();
	t.after(() => h.emit("session_shutdown"));
	await h.emit("session_start");
	h.events.emit(MISE_COORDINATES_EVENT);
	await h.emit("agent_start");
	h.setActivity({ code: 0, killed: false, stdout: '{"version":1,"command":"status","is_reviewing":false}' });
	// Pi can report idle while another extension is still finishing on-stop.
	await h.emit("agent_settled");
	t.mock.timers.tick(5_000);
	await setImmediate();
	assert.equal(h.messages.length, 0);
	const hooks: ((ctx: ExtensionContext, aborted: boolean) => Promise<void>)[] = [];
	h.events.emit(MISE_AFTER_ON_STOP_EVENT, hooks);
	for (const hook of hooks) await hook(h.ctx, false);
	await setImmediate();
	assert.equal(h.messages.length, 1);
});

test("non-interactive sessions do not start idle polling", async t => {
	t.mock.timers.enable({ apis: ["setInterval"] });
	for (const mode of ["print", "json"] as const) {
		const h = harness(mode);
		await h.emit("session_start");
		h.setActivity({ code: 0, killed: false, stdout: '{"version":1,"command":"status","is_reviewing":false}' });
		t.mock.timers.tick(10_000);
		await setImmediate();
		assert.equal(h.activityCalls(), 0);
		assert.equal(h.messages.length, 0);
		await h.emit("session_shutdown");
	}
});

test("slow idle checks do not overlap or notify after shutdown", async t => {
	t.mock.timers.enable({ apis: ["setInterval"] });
	const h = harness();
	await h.emit("session_start");
	h.setActivity({ code: 0, killed: false, stdout: '{"version":1,"command":"status","is_reviewing":false}' });
	let release!: () => void;
	h.setActivityGate(new Promise<void>(resolve => { release = resolve; }));
	t.mock.timers.tick(5_000);
	await setImmediate();
	assert.equal(h.activityCalls(), 1);
	t.mock.timers.tick(20_000);
	await setImmediate();
	assert.equal(h.activityCalls(), 1, "a slow check prevents further idle checks");
	await h.emit("session_shutdown");
	release();
	await setImmediate();
	assert.equal(h.messages.length, 0, "an in-flight query cannot revive a closed session");
});

test("idle notices respect abort and recheck whether the agent is idle", async t => {
	t.mock.timers.enable({ apis: ["setInterval"] });
	const h = harness();
	t.after(() => h.emit("session_shutdown"));
	await h.emit("session_start");
	await h.emit("agent_settled", { aborted: true });
	h.setActivity({ code: 0, killed: false, stdout: '{"version":1,"command":"status","is_reviewing":false}' });
	t.mock.timers.tick(5_000);
	await setImmediate();
	assert.equal(h.messages.length, 0, "abort must not restart the agent through polling");
	h.setActivity({ code: 0, killed: false, stdout: '{"version":1,"command":"status","is_reviewing":true}' });
	await h.emit("agent_settled");
	h.setActivity({ code: 0, killed: false, stdout: '{"version":1,"command":"status","is_reviewing":false}' });
	let release!: () => void;
	h.setActivityGate(new Promise<void>(resolve => { release = resolve; }));
	t.mock.timers.tick(5_000);
	await setImmediate();
	h.setIdle(false);
	release();
	await setImmediate();
	assert.equal(h.messages.length, 0, "agent started while the idle query was pending");
	h.setIdle(true);
	t.mock.timers.tick(5_000);
	await setImmediate();
	assert.equal(h.messages.length, 1);
});

test("slow status reads do not delay lifecycle handlers and explicit refreshes share the read", async () => {
	const h = harness();
	let release!: () => void;
	h.setStatusGate(new Promise<void>(resolve => { release = resolve; }));
	try {
		let started = false;
		const startup = h.emitRaw("session_start").then(() => { started = true; });
		await setImmediate();
		assert.equal(started, true, "session startup does not await Git");
		assert.equal(h.statusCalls(), 1);
		const request: GitReviewStatusRequest = {};
		h.events.emit(GIT_REVIEW_REQUEST_STATUS_EVENT, request);
		let refreshed = false;
		const refresh = request.refresh!(h.ctx.cwd).then(() => { refreshed = true; });
		let settled = false;
		const settling = h.emitRaw("agent_settled").then(() => { settled = true; });
		await setImmediate();
		assert.equal(settled, true, "settle dispatch does not await Git");
		assert.equal(refreshed, false, "explicit refresh still waits for fresh data");
		assert.equal(h.statusCalls(), 1, "startup, refresh, and settle share one read");
		release();
		await Promise.all([startup, refresh, settling]);
		await setImmediate();
		assert.equal(h.published.length, 1);
	} finally {
		release();
		await h.emit("session_shutdown");
	}
});

test("the latest queued settle controls abort suppression", async () => {
	const h = harness();
	let release!: () => void;
	h.setStatusGate(new Promise<void>(resolve => { release = resolve; }));
	h.setActivity({ code: 0, killed: false, stdout: '{"version":1,"command":"status","is_reviewing":false}' });
	await h.emit("agent_settled");
	await h.emit("agent_settled", { aborted: true });
	release();
	await setImmediate();
	assert.equal(h.messages.length, 0, "a queued abort supersedes an older check");
	await h.emit("agent_settled");
	assert.equal(h.messages.length, 1, "a later successful settle can notify");
});

test("optional background query failures do not prevent later refreshes", async () => {
	const h = harness();
	let reject!: (error: Error) => void;
	h.setStatusGate(new Promise<void>((_resolve, fail) => { reject = fail; }));
	await h.emit("session_start");
	reject(new Error("git-review unavailable"));
	await setImmediate();
	assert.equal(h.published.length, 0);
	h.setStatusGate(Promise.resolve());
	await h.emit("agent_settled");
	assert.equal(h.published.length, 1);
	await h.emit("session_shutdown");
});

test("a stale status read cannot publish into a replacement session", async () => {
	const h = harness();
	let releaseOld!: () => void;
	h.setStatusGate(new Promise<void>(resolve => { releaseOld = resolve; }));
	await h.emit("session_start");
	await h.emit("agent_settled");
	await h.emit("session_shutdown");
	h.setStatusGate(Promise.resolve());
	await h.emit("session_start");
	assert.equal(h.published.length, 1);
	releaseOld();
	await setImmediate();
	assert.equal(h.published.length, 1, "ignore results from the previous generation");
	assert.equal(h.messages.length, 0);
	await h.emit("session_shutdown");
});

test("activity query failures do not authorize a feedback notice", async () => {
	const h = harness();
	for (const result of [
		{ code: 1, killed: false, stdout: '{"version":1,"status":"error"}' },
		{ code: 0, killed: true, stdout: '{"version":1,"command":"status","is_reviewing":false}' },
		{ code: 0, killed: false, stdout: 'not json' },
		{ code: 0, killed: false, stdout: '{"version":2,"command":"status","is_reviewing":false}' },
		{ code: 0, killed: false, stdout: '{"version":1}' },
		{ code: 0, killed: false, stdout: '{"version":1,"command":"status"}' },
		{ code: 0, killed: false, stdout: '{"version":1,"command":"status","is_reviewing":"false"}' },
		{ code: 0, killed: false, stdout: '{"version":1,"running":false}' },
	]) {
		h.setActivity(result);
		await h.emit("agent_settled");
		assert.equal(h.messages.length, 0);
	}
	h.setActivity({ code: 0, killed: false, stdout: '{"version":1,"command":"status","is_reviewing":false}' });
	await h.emit("agent_settled", { aborted: true });
	assert.equal(h.messages.length, 0);
	await h.emit("agent_settled");
	assert.equal(h.messages.length, 1);
});
