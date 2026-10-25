import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import vm from "node:vm";
import ts from "typescript";
import { createPendingRun } from "../app/pending-run.ts";

// Execute the real submit handler, keeping its request asynchronous so edits
// made while the request is pending are visible to React-style state updates.
const source = readFileSync(new URL("../app/dashboard.tsx", import.meta.url), "utf8");
const ast = ts.createSourceFile("dashboard.tsx", source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
let handler;
function findHandler(node) {
  if (ts.isFunctionDeclaration(node) && node.name?.text === "sendMessage") handler = node;
  ts.forEachChild(node, findHandler);
}
findHandler(ast);
assert.ok(handler, "Dashboard submit handler must exist");
const script = new vm.Script(ts.transpileModule(handler.getText(ast), {
  compilerOptions: { target: ts.ScriptTarget.ES2022 },
}).outputText);

function harness() {
  let draft = "전송한 첫 메시지";
  let sending = false;
  let notice = "";
  let resolveRequest;
  let rejectRequest;
  const pendingRun = createPendingRun(active => { sending = active; });
  const context = vm.createContext({
    draft, runtimeReady: true, sending: false, pendingRun, apiBase: "http://runtime.test",
    setDraft: update => { draft = typeof update === "function" ? update(draft) : update; },
    setNotice: value => { notice = value; },
    fetch: () => new Promise((resolve, reject) => { resolveRequest = resolve; rejectRequest = reject; }),
    setCollections: () => assert.fail("failed request must not add a message"),
  });
  script.runInContext(context);
  const request = context.sendMessage({ preventDefault() {} });
  assert.equal(draft, "");
  assert.equal(sending, true);
  return {
    request, pendingRun,
    edit(value) { draft = value; },
    fail(network) { if (network) rejectRequest(new Error("offline")); else resolveRequest({ ok: false }); },
    get draft() { return draft; },
    get sending() { return sending; },
    get notice() { return notice; },
  };
}

for (const network of [false, true]) {
  const failure = network ? "network rejection" : "HTTP failure";
  for (const draft of ["", "새로 작성한 두 번째 초안\n계속 작성 중", " \n "]) {
    test(`${failure} preserves pending edits or restores an empty composer: ${JSON.stringify(draft)}`, async () => {
      const h = harness();
      h.edit(draft);
      h.fail(network);
      await h.request;
      assert.equal(h.draft, draft || "전송한 첫 메시지");
      assert.equal(h.sending, false);
      assert.match(h.notice, /다시 시도/);
    });
  }
}

test("a stale request failure cannot restore a draft or unlock a newer request", async () => {
  const h = harness();
  h.pendingRun.reset();
  h.pendingRun.begin();
  h.edit("새 세션의 초안");
  h.fail(true);
  await h.request;
  assert.equal(h.draft, "새 세션의 초안");
  assert.equal(h.sending, true);
  assert.equal(h.notice, "");
});
