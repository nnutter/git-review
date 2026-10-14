import assert from "node:assert/strict";
import { EventEmitter } from "node:events";
import test from "node:test";
import { setImmediate } from "node:timers/promises";
import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";
import umpire from "./umpire.ts";
import { MISE_AFTER_ON_STOP_EVENT, MISE_COORDINATES_EVENT, UMPIRE_STATUS_EVENT } from "./umpire/support.ts";

function harness(mode: ExtensionContext["mode"] = "tui") {
	const events = new EventEmitter();
	const handlers = new Map<string, Function>();
	const messages: string[] = [];
	const published: unknown[] = [];
	let feedback = 1;
	let activity = { code: 0, killed: false, stdout: '{"version":1,"running":true}' };
	let idle = true;
	let activityGate: Promise<void> | undefined;
	let activityCalls = 0;
	const ctx = { cwd: "/repo", mode, hasUI: true, isIdle: () => idle } as ExtensionContext;
	const pi = {
		events,
		on: (name: string, handler: Function) => handlers.set(name, handler),
		exec: async (command: string, args: string[]) => {
			if (command === "git") return { code: 0, killed: false, stdout: "true" };
			if (args[0] === "review-running") {
				activityCalls++;
				await activityGate;
				return activity;
			}
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
	events.on(UMPIRE_STATUS_EVENT, data => published.push(data));
	umpire(pi);
	return {
		events, messages, published, ctx,
		setActivity: (result: typeof activity) => { activity = result; },
		setFeedback: (count: number) => { feedback = count; },
		setIdle: (value: boolean) => { idle = value; },
		setActivityGate: (gate: Promise<void>) => { activityGate = gate; },
		activityCalls: () => activityCalls,
		emit: async (name: string, event = {}) => { await handlers.get(name)?.(event, ctx); },
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
			settle = async () => { for (const hook of hooks) await hook(h.ctx, false); };
		}
		await settle();
		assert.equal(h.messages.length, 0);
		assert.equal(h.published.length, 1, "UI still receives review counts");
		h.setActivity({ code: 0, killed: false, stdout: '{"version":1,"running":false}' });
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
		h.setActivity({ code: 0, killed: false, stdout: '{"version":1,"running":false}' });
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
	h.setActivity({ code: 0, killed: false, stdout: '{"version":1,"running":false}' });
	// Pi can report idle while another extension is still finishing on-stop.
	await h.emit("agent_settled");
	t.mock.timers.tick(5_000);
	await setImmediate();
	assert.equal(h.messages.length, 0);
	const hooks: ((ctx: ExtensionContext, aborted: boolean) => Promise<void>)[] = [];
	h.events.emit(MISE_AFTER_ON_STOP_EVENT, hooks);
	for (const hook of hooks) await hook(h.ctx, false);
	assert.equal(h.messages.length, 1);
});

test("non-interactive sessions do not start idle polling", async t => {
	t.mock.timers.enable({ apis: ["setInterval"] });
	for (const mode of ["print", "json"] as const) {
		const h = harness(mode);
		await h.emit("session_start");
		h.setActivity({ code: 0, killed: false, stdout: '{"version":1,"running":false}' });
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
	h.setActivity({ code: 0, killed: false, stdout: '{"version":1,"running":false}' });
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
	h.setActivity({ code: 0, killed: false, stdout: '{"version":1,"running":false}' });
	t.mock.timers.tick(5_000);
	await setImmediate();
	assert.equal(h.messages.length, 0, "abort must not restart the agent through polling");
	h.setActivity({ code: 0, killed: false, stdout: '{"version":1,"running":true}' });
	await h.emit("agent_settled");
	h.setActivity({ code: 0, killed: false, stdout: '{"version":1,"running":false}' });
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

test("activity query failures do not authorize a feedback notice", async () => {
	const h = harness();
	for (const result of [
		{ code: 1, killed: false, stdout: '{"version":1,"status":"error"}' },
		{ code: 0, killed: true, stdout: '{"version":1,"running":false}' },
		{ code: 0, killed: false, stdout: 'not json' },
		{ code: 0, killed: false, stdout: '{"version":2,"running":false}' },
		{ code: 0, killed: false, stdout: '{"version":1}' },
	]) {
		h.setActivity(result);
		await h.emit("agent_settled");
		assert.equal(h.messages.length, 0);
	}
	h.setActivity({ code: 0, killed: false, stdout: '{"version":1,"running":false}' });
	await h.emit("agent_settled", { aborted: true });
	assert.equal(h.messages.length, 0);
	await h.emit("agent_settled");
	assert.equal(h.messages.length, 1);
});
