import test from "node:test";
import assert from "node:assert/strict";
import worker from "./worker.js";

function makeObject(key, body, type = "image/webp") {
  const bytes = new TextEncoder().encode(body);
  return {
    body: new ReadableStream({ start(c){ c.enqueue(bytes); c.close(); } }),
    size: bytes.byteLength,
    httpEtag: '"test-etag"',
    range: undefined,
    writeHttpMetadata(h){ h.set("content-type", type); },
  };
}

test("serves native /v1 media from R2", async () => {
  const env = { MEDIA_BUCKET: { get: async (key) => {
    assert.equal(key, "users/u1/avatar.webp");
    return makeObject(key, "test-image");
  }}};
  const res = await worker.fetch(new Request("https://media.testagram.site/v1/users/u1/avatar.webp"), env);
  assert.equal(res.status, 200);
  assert.equal(res.headers.get("content-type"), "image/webp");
  assert.equal(res.headers.get("x-testagram-cdn"), "r2-edge");
  assert.equal(await res.text(), "test-image");
});

test("legacy aliases converge on the same R2 key", async () => {
  const env = { MEDIA_BUCKET: { get: async (key) => {
    assert.equal(key, "users/u1/avatar.webp");
    return makeObject(key, "legacy-image");
  }}};
  const res = await worker.fetch(new Request("https://media.testagram.site/users/u1/avatar.webp"), env);
  assert.equal(res.status, 200);
  assert.equal(await res.text(), "legacy-image");
});

test("missing objects remain a real 404", async () => {
  const env = { MEDIA_BUCKET: { get: async () => null }};
  const res = await worker.fetch(new Request("https://media.testagram.site/v1/users/missing.webp"), env);
  assert.equal(res.status, 404);
});
test("health and readiness expose the deployed Worker identity", async () => {
  const env = { MEDIA_BUCKET: { list: async () => ({ objects: [] }) } };
  const health = await worker.fetch(new Request("https://media.testagram.site/healthz"), env);
  const ready = await worker.fetch(new Request("https://media.testagram.site/readyz"), env);
  assert.equal(health.status, 200);
  assert.equal(ready.status, 200);
  assert.equal(health.headers.get("x-testagram-cdn-version"), "2026-10-08-r2-cdn-lockdown-1");
  assert.equal(ready.headers.get("x-testagram-cdn-version"), "2026-10-08-r2-cdn-lockdown-1");
});

test("range requests return HTTP 206", async () => {
  const env = { MEDIA_BUCKET: { get: async (_key, options) => {
    assert.deepEqual(options.range, { offset: 0, length: 4 });
    return { ...makeObject("users/u1/video.mp4", "test-video", "video/mp4"), range: { offset: 0, length: 4 } };
  }}};
  const res = await worker.fetch(new Request("https://media.testagram.site/v1/users/u1/video.mp4", { headers: { Range: "bytes=0-3" }}), env);
  assert.equal(res.status, 206);
  assert.equal(res.headers.get("content-range"), "bytes 0-3/10");
  assert.equal(res.headers.get("accept-ranges"), "bytes");
});

test("path traversal never reaches R2", async () => {
  let called = false;
  const env = { MEDIA_BUCKET: { get: async () => { called = true; return null; } } };
  const res = await worker.fetch(new Request("https://media.testagram.site/v1/users/u1/../secret.webp"), env);
  assert.equal(res.status, 404);
  assert.equal(called, false);
});
