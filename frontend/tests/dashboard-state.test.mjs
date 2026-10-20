import assert from "node:assert/strict";
import test from "node:test";
import { createPendingRun } from "../app/pending-run.ts";
import { applyRuntimeEvent } from "../app/runtime-events.ts";

function harness() {
  let sending = false;
  let failed = false;
  const tracker = createPendingRun((active, failure = false) => { sending = active; failed = failure; });
  const request = tracker.begin();
  return { tracker, request, get sending() { return sending; }, get failed() { return failed; } };
}

for (const status of ["completed", "failed"]) {
  test(`reconnect unlocks a run that ${status} while disconnected`, async () => {
    const h = harness();
    h.tracker.accept(h.request, { id: "ours", status: "queued" });
    await h.tracker.restore([{ id: "ours", status: "running" }], () => assert.fail("unexpected lookup"));
    assert.equal(h.sending, true);
    await h.tracker.restore([{ id: "ours", status }], () => assert.fail("unexpected lookup"));
    assert.equal(h.sending, false);
    assert.equal(h.failed, status === "failed");
  });
}

test("another tab's completion cannot unlock the current request", () => {
  const h = harness();
  h.tracker.observe({ id: "other", status: "failed" });
  h.tracker.accept(h.request, { id: "ours", status: "queued" });
  h.tracker.observe({ id: "other", status: "completed" });
  assert.equal(h.sending, true);
  assert.equal(h.failed, false);
  h.tracker.observe({ id: "ours", status: "completed" });
  assert.equal(h.sending, false);
});

test("completion arriving before the POST response is retained", async () => {
  for (const source of ["event", "bootstrap"]) {
    const h = harness();
    const run = { id: "ours", status: "completed" };
    if (source === "event") h.tracker.observe(run);
    else await h.tracker.restore([run], () => assert.fail("no run ID yet"));
    assert.equal(h.sending, true);
    h.tracker.accept(h.request, { ...run, status: "queued" });
    assert.equal(h.sending, false);
  }
});

test("a run outside the recent snapshot is looked up before unlocking", async () => {
  const h = harness();
  h.tracker.accept(h.request, { id: "ours", status: "queued" });
  const recent = Array.from({ length: 8 }, (_, i) => ({ id: `other-${i}`, status: "completed" }));
  await h.tracker.restore(recent, async id => {
    assert.equal(id, "ours");
    return { id, status: "running" };
  });
  assert.equal(h.sending, true);
  await h.tracker.restore(recent, async id => ({ id, status: "completed" }));
  assert.equal(h.sending, false);
});

test("expired runs unlock, but lookup failures preserve the pending run for retry", async () => {
  const h = harness();
  h.tracker.accept(h.request, { id: "ours", status: "queued" });
  await assert.rejects(h.tracker.restore([], async () => { throw new Error("offline"); }), /offline/);
  assert.equal(h.sending, true);
  await h.tracker.restore([], async () => null);
  assert.equal(h.sending, false);
});

test("late responses from a reset session cannot affect a new request", async () => {
  const h = harness();
  h.tracker.accept(h.request, { id: "old", status: "queued" });
  let resolve;
  const lookup = h.tracker.restore([], () => new Promise(done => { resolve = done; }));
  h.tracker.reset();
  assert.equal(h.sending, false);
  const next = h.tracker.begin();
  h.tracker.accept(next, { id: "new", status: "running" });
  assert.equal(h.tracker.accept(h.request, { id: "old", status: "completed" }), false);
  assert.equal(h.tracker.reset(h.request), false);
  resolve(null);
  await lookup;
  assert.equal(h.sending, true);
  h.tracker.observe({ id: "new", status: "completed" });
  assert.equal(h.sending, false);
});

test("new memory appears immediately and replay does not duplicate it", () => {
  const message = { id: "u1", role: "user", content: "remember this", createdAt: "2026-10-11T00:00:00Z" };
  let state = { messages: [message], tasks: [], memories: [], events: [] };
  const memory = { id: "m1", content: "a useful preference", createdAt: message.createdAt, expiresAt: "2026-10-14T00:00:00Z" };
  const event = { id: "e1", type: "memory.created", timestamp: message.createdAt, payload: { memory } };
  state = applyRuntimeEvent(state, event);
  assert.deepEqual(state.memories, [memory]);
  state = applyRuntimeEvent(state, event);
  assert.deepEqual(state.memories, [memory]);
  assert.equal(state.events.length, 1);
  assert.deepEqual(state.messages, [message]);
  state = applyRuntimeEvent(state, { ...event, id: "e2", type: "tool.completed", payload: {} });
  assert.deepEqual(state.memories, [memory]);
});
