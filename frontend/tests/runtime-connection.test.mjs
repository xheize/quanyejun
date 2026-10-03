import assert from "node:assert/strict";
import test from "node:test";
import { connectRuntime } from "../app/runtime-connection.ts";

const flush = () => new Promise(resolve => setImmediate(resolve));

function harness(t, apiBase = "https://runtime.example.test", fetchResponse = async () => new Response("{}")) {
  t.mock.timers.enable({ apis: ["setTimeout"] });
  const sockets = [];
  class Socket {
    constructor(url) { this.url = url; sockets.push(this); }
    close() { this.onclose?.(); }
  }
  const originalSocket = globalThis.WebSocket;
  globalThis.WebSocket = Socket;
  t.after(() => { globalThis.WebSocket = originalSocket; });
  const fetchMock = t.mock.method(globalThis, "fetch", fetchResponse);
  const states = [];
  let exhausted = 0;
  let bootstraps = 0;
  const stop = connectRuntime({
    apiBase,
    onBootstrap: async response => {
      if (!response.ok) throw new Error("unavailable");
      await response.json();
      bootstraps++;
    },
    cursor: () => "event/1",
    onMessage: () => {},
    onState: state => states.push(state),
    onExhausted: () => exhausted++,
  });
  t.after(stop);
  return { sockets, fetchMock, states, stop, get exhausted() { return exhausted; }, get bootstraps() { return bootstraps; } };
}

test("preview makes no HTTP or WebSocket requests", async t => {
  const h = harness(t, "");
  t.mock.timers.tick(60_000);
  await flush();
  assert.equal(h.fetchMock.mock.callCount(), 0);
  assert.equal(h.sockets.length, 0);
});

test("HTTP failures stop after the initial attempt plus ten retries", async t => {
  const h = harness(t, "https://runtime.example.test", async () => new Response("", { status: 503 }));
  await flush();
  for (let i = 0; i < 10; i++) { t.mock.timers.tick(2500); await flush(); }
  assert.equal(h.fetchMock.mock.callCount(), 11);
  assert.equal(h.exhausted, 1);
  assert.equal(h.states.at(-1), "offline");
  t.mock.timers.tick(60_000);
  await flush();
  assert.equal(h.fetchMock.mock.callCount(), 11);
});

test("successful socket opens do not reset the shared retry budget", async t => {
  const h = harness(t);
  await flush();
  for (let i = 0; i <= 10; i++) {
    assert.equal(h.sockets[i].url, "wss://runtime.example.test/ws?lastEventId=event%2F1");
    h.sockets[i].onopen();
    h.sockets[i].close();
    t.mock.timers.tick(2500);
    await flush();
  }
  assert.equal(h.bootstraps, 11);
  assert.equal(h.sockets.length, 11);
  assert.equal(h.exhausted, 1);
});

test("cleanup cancels scheduled retries and aborts requests", async t => {
  const h = harness(t);
  await flush();
  h.sockets[0].close();
  h.stop();
  t.mock.timers.tick(60_000);
  await flush();
  assert.equal(h.fetchMock.mock.callCount(), 1);
  assert.equal(h.fetchMock.mock.calls[0].arguments[1].signal.aborted, true);
});

test("network failures and socket errors consume the same budget", async t => {
  let attempts = 0;
  const h = harness(t, "https://runtime.example.test", async () => {
    if (++attempts <= 5) throw new TypeError("Failed to fetch");
    return new Response("{}");
  });
  await flush();
  for (let i = 0; i < 5; i++) { t.mock.timers.tick(2500); await flush(); }
  for (let i = 0; i < 6; i++) {
    h.sockets[i].onerror();
    t.mock.timers.tick(2500);
    await flush();
  }
  assert.equal(attempts, 11);
  assert.equal(h.exhausted, 1);
});

test("cleanup ignores a late bootstrap response", async t => {
  let resolve;
  const pending = new Promise(done => { resolve = done; });
  const h = harness(t, "https://runtime.example.test", () => pending);
  h.stop();
  resolve(new Response("{}"));
  await flush();
  assert.equal(h.bootstraps, 0);
  assert.equal(h.sockets.length, 0);
});
