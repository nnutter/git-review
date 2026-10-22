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
	const deliveries: unknown[] = [];
	const commands = new Map<string, { handler: (args: string, ctx: ExtensionContext) => Promise<void> }>();
	const published: unknown[] = [];
	let feedback = 1;
	let idle = true;
	let statusGate: Promise<void> | undefined;
	let statusCalls = 0;
	const ctx = { cwd: "/repo", mode, hasUI: true, isIdle: () => idle } as ExtensionContext;
	const pi = {
		events,
		on: (name: string, handler: Function) => handlers.set(name, handler),
		registerCommand: (name: string, command: { handler: (args: string, ctx: ExtensionContext) => Promise<void> }) => commands.set(name, command),
		exec: async (command: string, args: string[]) => {
			if (command === "git") return { code: 0, killed: false, stdout: "true" };
			assert.equal(command, "git-review");
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
		sendUserMessage: (message: string, options?: unknown) => {
			messages.push(message);
			deliveries.push(options);
		},
	} as unknown as ExtensionAPI;
	events.on("git-review:status", data => published.push(data));
	gitReview(pi);
	return {
		events, messages, deliveries, published, ctx,
		command: async (name: string) => { await commands.get(name)!.handler("", ctx); },
		setFeedback: (count: number) => { feedback = count; },
		setIdle: (value: boolean) => { idle = value; },
		setStatusGate: (gate: Promise<void>) => { statusGate = gate; },
		statusCalls: () => statusCalls,
		emitRaw: async (name: string, event = {}) => { await handlers.get(name)?.(event, ctx); },
		emit: async (name: string, event = {}) => {
			await handlers.get(name)?.(event, ctx);
			await setImmediate();
		},
	};
}

test("/feedback asks the model to pull feedback and queues when busy", async () => {
	for (const idle of [true, false]) {
		const h = harness();
		h.setIdle(idle);
		await h.command("feedback");
		assert.deepEqual(h.messages, ["Use the git-review skill to retrieve current feedback with `git-review feedback --json` and address it."]);
		assert.deepEqual(h.deliveries, [idle ? undefined : { deliverAs: "followUp" }]);
		assert.equal(h.statusCalls(), 0, "the command delegates retrieval to the model");
	}
});

test("settle publishes feedback counts without messaging the model", async () => {
	const h = harness();
	await h.emit("agent_settled");
	await h.emit("agent_settled");
	assert.equal(h.published.length, 2);
	assert.equal(h.messages.length, 0, "feedback never starts a model turn on its own");
	await h.emit("session_shutdown");
});

test("settle does not message the model after an aborted run", async () => {
	const h = harness();
	await h.emit("agent_settled", { aborted: true });
	assert.equal(h.published.length, 1);
	assert.equal(h.messages.length, 0);
});

test("mise on-stop hook publishes instead of settle", async () => {
	const h = harness();
	h.events.emit(MISE_COORDINATES_EVENT);
	await h.emit("agent_settled");
	assert.equal(h.published.length, 0, "mise owns publishing after its on-stop hook");
	const hooks: ((ctx: ExtensionContext, aborted: boolean) => Promise<void>)[] = [];
	h.events.emit(MISE_AFTER_ON_STOP_EVENT, hooks);
	for (const hook of hooks) await hook(h.ctx, false);
	await setImmediate();
	assert.equal(h.published.length, 1);
	assert.equal(h.messages.length, 0);
});

for (const mode of ["tui", "rpc"] as const) {
	test(`idle polling publishes only while idle and stops at shutdown (${mode})`, async t => {
		t.mock.timers.enable({ apis: ["setInterval"] });
		const h = harness(mode);
		await h.emit("session_start");
		const startup = h.published.length;
		h.setIdle(false);
		t.mock.timers.tick(5_000);
		await setImmediate();
		assert.equal(h.published.length, startup, "polling does not run during an active run");
		h.setIdle(true);
		t.mock.timers.tick(5_000);
		await setImmediate();
		assert.equal(h.published.length, startup + 1);
		assert.equal(h.messages.length, 0);
		await h.emit("session_shutdown");
		const count = h.published.length;
		t.mock.timers.tick(10_000);
		await setImmediate();
		assert.equal(h.published.length, count, "shutdown removes the timer");
	});
}

test("non-interactive sessions do not start idle polling", async t => {
	t.mock.timers.enable({ apis: ["setInterval"] });
	for (const mode of ["print", "json"] as const) {
		const h = harness(mode);
		await h.emit("session_start");
		const startup = h.published.length;
		t.mock.timers.tick(10_000);
		await setImmediate();
		assert.equal(h.published.length, startup);
		assert.equal(h.messages.length, 0);
		await h.emit("session_shutdown");
	}
});

test("slow idle polls share one read and do not publish after shutdown", async t => {
	t.mock.timers.enable({ apis: ["setInterval"] });
	const h = harness();
	await h.emit("session_start");
	await setImmediate();
	const startup = h.published.length;
	let release!: () => void;
	h.setStatusGate(new Promise<void>(resolve => { release = resolve; }));
	const calls = h.statusCalls();
	t.mock.timers.tick(5_000);
	await setImmediate();
	t.mock.timers.tick(20_000);
	await setImmediate();
	assert.equal(h.statusCalls(), calls + 1, "a slow read prevents overlapping reads");
	await h.emit("session_shutdown");
	release();
	await setImmediate();
	assert.equal(h.published.length, startup, "an in-flight read cannot revive a closed session");
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
