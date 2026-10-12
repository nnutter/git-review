import assert from "node:assert/strict";
import { EventEmitter } from "node:events";
import test from "node:test";
import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";
import umpire from "./umpire.ts";
import { MISE_AFTER_ON_STOP_EVENT, MISE_COORDINATES_EVENT, UMPIRE_STATUS_EVENT } from "./umpire/support.ts";

function harness() {
	const events = new EventEmitter();
	const handlers = new Map<string, Function>();
	const messages: string[] = [];
	const published: unknown[] = [];
	let feedback = 1;
	let activity = { code: 0, killed: false, stdout: '{"version":1,"running":true}' };
	let idle = true;
	const ctx = { cwd: "/repo", hasUI: true, isIdle: () => idle } as ExtensionContext;
	const pi = {
		events,
		on: (name: string, handler: Function) => handlers.set(name, handler),
		exec: async (command: string, args: string[]) => {
			if (command === "git") return { code: 0, killed: false, stdout: "true" };
			if (args[0] === "review-running") return activity;
			return { code: 0, killed: false, stdout: JSON.stringify([
				{ state: "needs_review", count: 2 },
				{ state: "reviewing", count: 1 },
				{ state: "feedback", count: feedback },
				{ state: "incomplete", count: 0 },
				{ state: "approved", count: 0 },
			]) };
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
