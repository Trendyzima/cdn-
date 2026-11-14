import test from "node:test";
import assert from "node:assert/strict";
import worker from "./worker.js";

const apiOrigin = "https://machine-api.instabox.sh";
const cdnOrigin = "https://machine-cdn.instabox.sh";

function env(overrides = {}) {
  return { API_ORIGIN: apiOrigin, CDN_ORIGIN: cdnOrigin, ...overrides };
}

function restoreFetch(t) {
  const original = globalThis.fetch;
  t.after(() => { globalThis.fetch = original; });
}

test("canonical API hostname proxies to the verified Backend origin", async (t) => {
  restoreFetch(t);
  let upstream;
  globalThis.fetch = async (request) => {
    upstream = request;
    return new Response(JSON.stringify({ ok: true, service: "testagram-api" }), {
      status: 200,
      headers: {
        "content-type": "application/json",
        "x-testagram-service": "testagram-api",
        "x-testagram-health-contract": "v1",
      },
    });
  };

  const response = await worker.fetch(
    new Request("https://api.testagram.site/healthz?probe=1"),
    env(),
  );
  assert.equal(response.status, 200);
  assert.equal(new URL(upstream.url).origin, apiOrigin);
  assert.equal(new URL(upstream.url).pathname, "/healthz");
  assert.equal(new URL(upstream.url).search, "?probe=1");
  assert.equal(upstream.headers.get("x-forwarded-host"), "api.testagram.site");
  assert.equal(upstream.headers.get("x-forwarded-proto"), "https");
  assert.equal(response.headers.get("x-testagram-service"), "testagram-api");
  assert.deepEqual(await response.json(), { ok: true, service: "testagram-api" });
});

test("canonical media hostname proxies to the Go CDN and preserves its identity", async (t) => {
  restoreFetch(t);
  let upstream;
  globalThis.fetch = async (request) => {
    upstream = request;
    return new Response(JSON.stringify({ ok: true, service: "testagram-cdn" }), {
      status: 200,
      headers: {
        "content-type": "application/json",
        "x-testagram-cdn": "testagram-edge",
        "x-testagram-cdn-version": "2026-11-13-backend-origin-hardened-v1",
      },
    });
  };

  const response = await worker.fetch(
    new Request("https://media.testagram.site/healthz"),
    env(),
  );
  assert.equal(response.status, 200);
  assert.equal(new URL(upstream.url).origin, cdnOrigin);
  assert.equal(response.headers.get("x-testagram-cdn"), "testagram-edge");
  assert.equal(response.headers.get("x-testagram-cdn-version"), "2026-11-13-backend-origin-hardened-v1");
  assert.deepEqual(await response.json(), { ok: true, service: "testagram-cdn" });
});

test("public API hides readiness and metrics while preserving liveness", async (t) => {
  restoreFetch(t);
  let fetches = 0;
  globalThis.fetch = async () => { fetches += 1; return new Response("unexpected"); };
  const ready = await worker.fetch(new Request("https://api.testagram.site/readyz"), env());
  const metrics = await worker.fetch(new Request("https://api.testagram.site/metrics"), env());
  assert.equal(ready.status, 404);
  assert.equal(metrics.status, 404);
  assert.equal(fetches, 0);
});

test("replaces spoofed forwarding metadata and strips hop-by-hop headers", async (t) => {
  restoreFetch(t);
  let upstream;
  globalThis.fetch = async (request) => {
    upstream = request;
    return new Response("ok");
  };
  const response = await worker.fetch(new Request(
    "https://api.testagram.site/healthz",
    {
      headers: {
        "cf-connecting-ip": "203.0.113.17",
        "x-forwarded-for": "198.51.100.99",
        "x-forwarded-host": "attacker.example",
        "x-forwarded-proto": "http",
        "x-forwarded-port": "8080",
        "x-real-ip": "192.0.2.55",
        forwarded: "for=192.0.2.55;proto=http;host=attacker.example",
        connection: "keep-alive, x-tenant-override",
        "keep-alive": "timeout=5",
        "x-tenant-override": "attacker",
      },
    },
  ), env());
  assert.equal(response.status, 200);
  assert.equal(upstream.headers.get("x-forwarded-for"), "203.0.113.17");
  assert.equal(upstream.headers.get("x-forwarded-host"), "api.testagram.site");
  assert.equal(upstream.headers.get("x-forwarded-proto"), "https");
  for (const name of ["x-forwarded-port", "x-real-ip", "forwarded", "connection", "keep-alive", "x-tenant-override", "cf-connecting-ip"]) {
    assert.equal(upstream.headers.get(name), null, `${name} must not reach the Backend`);
  }
});

test("forwards API methods, authorization, JSON body and query without changing the contract", async (t) => {
  restoreFetch(t);
  let upstream;
  globalThis.fetch = async (request) => {
    upstream = request;
    return new Response("created", { status: 201 });
  };
  const response = await worker.fetch(new Request(
    "https://api.testagram.site/api/posts?source=mobile",
    {
      method: "POST",
      headers: { authorization: "Bearer user-token", "content-type": "application/json" },
      body: JSON.stringify({ text: "hello" }),
    },
  ), env());
  assert.equal(response.status, 201);
  assert.equal(upstream.method, "POST");
  assert.equal(upstream.headers.get("authorization"), "Bearer user-token");
  assert.equal(upstream.headers.get("content-type"), "application/json");
  assert.deepEqual(await upstream.json(), { text: "hello" });
  assert.equal(new URL(upstream.url).search, "?source=mobile");
});

test("preserves range requests and viewer authorization for private media", async (t) => {
  restoreFetch(t);
  let upstream;
  globalThis.fetch = async (request) => {
    upstream = request;
    return new Response("bytes", {
      status: 206,
      headers: { "content-range": "bytes 0-4/10", "cache-control": "private, no-store" },
    });
  };
  const response = await worker.fetch(new Request(
    "https://media.testagram.site/v1/users/u1/file.webp",
    { headers: { authorization: "Bearer viewer-token", range: "bytes=0-4" } },
  ), env());
  assert.equal(response.status, 206);
  assert.equal(upstream.headers.get("authorization"), "Bearer viewer-token");
  assert.equal(upstream.headers.get("range"), "bytes=0-4");
  assert.equal(response.headers.get("cache-control"), "private, no-store");
});

test("fails closed when either origin is missing, insecure, or points back to a canonical host", async (t) => {
  restoreFetch(t);
  let fetches = 0;
  globalThis.fetch = async () => { fetches += 1; return new Response("unexpected"); };
  const cases = [
    [new Request("https://api.testagram.site/healthz"), env({ API_ORIGIN: "" })],
    [new Request("https://api.testagram.site/healthz"), env({ API_ORIGIN: "http://machine.instabox.example" })],
    [new Request("https://media.testagram.site/healthz"), env({ CDN_ORIGIN: "https://media.testagram.site" })],
    [new Request("https://media.testagram.site/healthz"), env({ CDN_ORIGIN: "https://127.0.0.1" })],
    [new Request("https://media.testagram.site/healthz"), env({ CDN_ORIGIN: "https://stale.workers.dev" })],
    [new Request("https://media.testagram.site/healthz"), env({ CDN_ORIGIN: "https://placeholder.example" })],
  ];
  for (const [request, bindings] of cases) {
    const response = await worker.fetch(request, bindings);
    assert.equal(response.status, 503);
  }
  assert.equal(fetches, 0);
});

test("rejects unknown hostnames instead of acting as an open proxy", async (t) => {
  restoreFetch(t);
  let fetches = 0;
  globalThis.fetch = async () => { fetches += 1; return new Response("unexpected"); };
  const response = await worker.fetch(new Request("https://attacker.example/"), env());
  assert.equal(response.status, 421);
  assert.equal(fetches, 0);
});

test("rewrites same-origin redirects back to the canonical hostname", async (t) => {
  restoreFetch(t);
  globalThis.fetch = async () => new Response(null, {
    status: 302,
    headers: { location: `${apiOrigin}/auth/callback?next=%2Ffeed` },
  });
  const response = await worker.fetch(new Request("https://api.testagram.site/start"), env());
  assert.equal(response.status, 302);
  assert.equal(response.headers.get("location"), "https://api.testagram.site/auth/callback?next=%2Ffeed");
});

test("preserves WebSocket upgrade semantics when proxying", async (t) => {
  restoreFetch(t);
  let upstream;
  globalThis.fetch = async (request) => {
    upstream = request;
    return new Response("upgrade forwarded");
  };
  const response = await worker.fetch(new Request("https://api.testagram.site/socket", {
    headers: { connection: "keep-alive, Upgrade", upgrade: "websocket" },
  }), env());
  assert.equal(response.status, 200);
  assert.equal(upstream.headers.get("connection"), "Upgrade");
  assert.equal(upstream.headers.get("upgrade"), "websocket");
});

test("returns 502 on upstream transport failure", async (t) => {
  restoreFetch(t);
  globalThis.fetch = async () => { throw new Error("network failure"); };
  const response = await worker.fetch(new Request("https://api.testagram.site/healthz"), env());
  assert.equal(response.status, 502);
  assert.equal((await response.json()).error, "upstream_unavailable");
});
