import assert from "node:assert/strict";
import test from "node:test";

async function render() {
  const workerUrl = new URL("../dist/server/index.js", import.meta.url);
  workerUrl.searchParams.set("test", `${process.pid}-${Date.now()}`);
  const { default: worker } = await import(workerUrl.href);

  return worker.fetch(
    new Request("http://quanyejun.local/", { headers: { accept: "text/html", host: "quanyejun.local" } }),
    { ASSETS: { fetch: async () => new Response("Not found", { status: 404 }) } },
    { waitUntil() {}, passThroughOnException() {} },
  );
}

test("server-renders the QuanYejun identity surface", async () => {
  const response = await render();
  assert.equal(response.status, 200);
  assert.match(response.headers.get("content-type") ?? "", /^text\/html\b/i);

  const html = await response.text();
  assert.match(html, /<title>QuanYejun — Persistent Identity Runtime<\/title>/i);
  assert.match(html, /PERSISTENT IDENTITY/);
  assert.match(html, /하나의 이어지는 대화/);
  assert.match(html, /WORLD STATE/);
  assert.match(html, /data-x-product="quanyejun"/);
  assert.match(html, /data-x-theme="light"/);
  for (const destination of ["conversation", "activity", "memory", "tasks"]) {
    assert.match(html, new RegExp(`href="#${destination}"`));
    assert.equal([...html.matchAll(new RegExp(` id="${destination}"`, "g"))].length, 1);
    assert.match(html, new RegExp(`id="${destination}"[^>]*tabindex="-1"`, "i"));
  }
  assert.match(html, /아직 작업이 없습니다/);
  assert.match(html, /aria-describedby="composer-help"/);
  assert.match(html, /id="alias-feedback"[^>]*role="status"/);
  assert.match(html, /https:\/\/quanyejun\.local\/og\.png/);
  assert.doesNotMatch(html, /codex-preview|SkeletonPreview|react-loading-skeleton/);
});
