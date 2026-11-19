import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import worker from "./worker.js";

const apiOrigin = "https://machine-api.instabox.sh";
const cdnOrigin = "https://machine-cdn.instabox.sh";
const apexOrigin = "https://machine-apex.instabox.sh";

function env(overrides = {}) {
  return { API_ORIGIN: apiOrigin, CDN_ORIGIN: cdnOrigin, APEX_ORIGIN: apexOrigin, ...overrides };
}

function restoreFetch(t) {
  const original = globalThis.fetch;
  t.after(() => { globalThis.fetch = original; });
}

test("answers approved browser preflights at the edge without waking the machine", async (t) => {
  restoreFetch(t);
  let fetches = 0;
  globalThis.fetch = async () => { fetches += 1; return new Response("unexpected"); };

  const response = await worker.fetch(new Request("https://api.testagram.site/v1/auth/login", {
    method: "OPTIONS",
    headers: {
      origin: "https://testagram.site",
      "access-control-request-method": "POST",
      "access-control-request-headers": "authorization,content-type,idempotency-key",
    },
  }), env());

  assert.equal(response.status, 204);
  assert.equal(response.headers.get("access-control-allow-origin"), "https://testagram.site");
  assert.match(response.headers.get("access-control-allow-methods"), /POST/);
  const allowedHeaders = response.headers.get("access-control-allow-headers").toLowerCase();
  assert.match(allowedHeaders, /authorization/);
  assert.match(allowedHeaders, /content-type/);
  assert.match(allowedHeaders, /idempotency-key/);
  assert.equal(response.headers.get("access-control-max-age"), "600");
  assert.equal(fetches, 0);
});

test("rejects unapproved or unsupported browser preflights without contacting an origin", async (t) => {
  restoreFetch(t);
  let fetches = 0;
  globalThis.fetch = async () => { fetches += 1; return new Response("unexpected"); };

  const unapprovedOrigin = await worker.fetch(new Request("https://api.testagram.site/v1/auth/login", {
    method: "OPTIONS",
    headers: {
      origin: "https://evil.example",
      "access-control-request-method": "POST",
      "access-control-request-headers": "authorization",
    },
  }), env());
  assert.equal(unapprovedOrigin.status, 403);
  assert.equal(unapprovedOrigin.headers.get("access-control-allow-origin"), null);

  const unapprovedHeader = await worker.fetch(new Request("https://api.testagram.site/v1/auth/login", {
    method: "OPTIONS",
    headers: {
      origin: "https://testagram.site",
      "access-control-request-method": "POST",
      "access-control-request-headers": "x-evil-header",
    },
  }), env());
  assert.equal(unapprovedHeader.status, 403);
  assert.equal(unapprovedHeader.headers.get("access-control-allow-origin"), null);

  const unsupportedMethod = await worker.fetch(new Request("https://api.testagram.site/v1/auth/login", {
    method: "OPTIONS",
    headers: {
      origin: "https://testagram.site",
      "access-control-request-method": "PATCH",
    },
  }), env());
  assert.equal(unsupportedMethod.status, 405);
  assert.equal(fetches, 0);
});

test("leaves media-host preflights on the media origin's own CORS policy", async (t) => {
  restoreFetch(t);
  let fetches = 0;
  globalThis.fetch = async () => {
    fetches += 1;
    return new Response(null, { status: 204, headers: { "access-control-allow-origin": "https://testagram.site" } });
  };

  const response = await worker.fetch(new Request("https://media.testagram.site/upload", {
    method: "OPTIONS",
    headers: {
      origin: "https://testagram.site",
      "access-control-request-method": "PUT",
      "access-control-request-headers": "authorization,x-amz-date",
    },
  }), env());

  assert.equal(response.status, 204);
  assert.equal(fetches, 1);
});

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
        "x-testagram-cdn-version": "2026-11-15-private-health-listener-v1",
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
  assert.equal(response.headers.get("x-testagram-cdn-version"), "2026-11-15-private-health-listener-v1");
  assert.deepEqual(await response.json(), { ok: true, service: "testagram-cdn" });
});

test("apex WebFinger and ActivityPub paths proxy to the dedicated Caddy origin", async (t) => {
  restoreFetch(t);
  let upstream;
  globalThis.fetch = async (request) => {
    upstream = request;
    return new Response(JSON.stringify({ subject: "acct:alice@testagram.site" }), {
      status: 200,
      headers: { "content-type": "application/jrd+json" },
    });
  };
  const response = await worker.fetch(
    new Request("https://testagram.site/.well-known/webfinger?resource=acct%3Aalice%40testagram.site"),
    env(),
  );
  assert.equal(response.status, 200);
  assert.equal(new URL(upstream.url).origin, apexOrigin);
  assert.equal(new URL(upstream.url).pathname, "/.well-known/webfinger");
  assert.equal(new URL(upstream.url).search, "?resource=acct%3Aalice%40testagram.site");
  assert.equal(upstream.headers.get("x-forwarded-host"), "testagram.site");
  assert.equal(upstream.headers.get("x-forwarded-proto"), "https");
  assert.equal(response.headers.get("content-type"), "application/jrd+json");
});

test("www frontend hostname proxies to the self-hosted Flutter origin", async (t) => {
  restoreFetch(t);
  let upstream;
  globalThis.fetch = async (request) => {
    upstream = request;
    return new Response('<html><script src="flutter_bootstrap.js"></script></html>', {
      status: 200,
      headers: { "content-type": "text/html; charset=utf-8" },
    });
  };
  const response = await worker.fetch(
    new Request("https://www.testagram.site/"),
    env(),
  );
  assert.equal(response.status, 200);
  assert.equal(new URL(upstream.url).origin, apexOrigin);
  assert.equal(new URL(upstream.url).pathname, "/");
  assert.equal(upstream.headers.get("x-forwarded-host"), "www.testagram.site");
  assert.equal(upstream.headers.get("x-forwarded-proto"), "https");
  assert.ok((await response.text()).includes("flutter_bootstrap.js"));
});

test("apex ordinary web paths preserve Caddy's canonical redirect to www", async (t) => {
  restoreFetch(t);
  let upstream;
  globalThis.fetch = async (request) => {
    upstream = request;
    return new Response(null, {
      status: 308,
      headers: { location: "https://www.testagram.site/about?from=apex" },
    });
  };
  const response = await worker.fetch(
    new Request("https://testagram.site/about?from=apex"),
    env(),
  );
  assert.equal(response.status, 308);
  assert.equal(new URL(upstream.url).origin, apexOrigin);
  assert.equal(response.headers.get("location"), "https://www.testagram.site/about?from=apex");
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

test("media gateway hides readiness and metrics while preserving public liveness", async (t) => {
  restoreFetch(t);
  let fetches = 0;
  globalThis.fetch = async () => { fetches += 1; return new Response("unexpected"); };
  const ready = await worker.fetch(new Request("https://media.testagram.site/readyz"), env());
  const metrics = await worker.fetch(new Request("https://media.testagram.site/metrics"), env());
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
  assert.equal(upstream.headers.get("x-real-ip"), "203.0.113.17");
  for (const name of ["x-forwarded-port", "forwarded", "connection", "keep-alive", "x-tenant-override", "cf-connecting-ip"]) {
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

test("protocol-relative paths cannot escape the configured origin", async (t) => {
  restoreFetch(t);
  let upstream;
  globalThis.fetch = async (request) => {
    upstream = request;
    return new Response("ok");
  };
  const response = await worker.fetch(
    new Request("https://media.testagram.site//attacker.example/asset?x=1"),
    env(),
  );
  assert.equal(response.status, 200);
  assert.equal(new URL(upstream.url).origin, cdnOrigin);
  assert.equal(new URL(upstream.url).pathname, "//attacker.example/asset");
  assert.equal(new URL(upstream.url).search, "?x=1");
});

test("uses the documented InstaBox idle-wake endpoint and replays an idempotent request", async (t) => {
  restoreFetch(t);
  let originCalls = 0;
  let wakeCalls = 0;
  let originalBody;
  globalThis.fetch = async (request, init = {}) => {
    const url = typeof request === "string" ? request : request.url;
    const method = typeof request === "string" ? (init.method || "GET") : request.method;
    const headers = typeof request === "string" ? new Headers(init.headers) : request.headers;
    if (url === "https://api.instabox.sh/v1/machines/m_test123/wake") {
      wakeCalls += 1;
      assert.equal(method, "POST");
      assert.match(headers.get("idempotency-key") || "", /^testagram-wake-[0-9a-f-]{36}$/i);
      assert.deepEqual(await new Response(init.body).json(), {});
      return new Response(JSON.stringify({ state: "running", runtime: { ready: true } }), { status: 200 });
    }
    if (url === "https://api.instabox.sh/v1/machines/m_test123/start") {
      throw new Error("wake must never use /start for an idle-suspended machine");
    }
    if (url.startsWith(apiOrigin)) {
      originCalls += 1;
      if (originCalls === 1) return new Response("machine waking", { status: 503 });
      originalBody = await request.clone().text();
      return new Response("created after wake", { status: 201 });
    }
    throw new Error("unexpected fetch: " + url);
  };

  const payload = JSON.stringify({ text: "preserve this post body" });
  const response = await worker.fetch(new Request("https://api.testagram.site/api/social/posts", {
    method: "POST",
    headers: { "content-type": "application/json", "idempotency-key": "post-create-123" },
    body: payload,
  }), env({ MACHINE_ID: "m_test123", INSTABOX_API_KEY: "ci-test-key" }));

  assert.equal(response.status, 201);
  assert.equal(await response.text(), "created after wake");
  assert.equal(originCalls, 2);
  assert.equal(wakeCalls, 1);
  assert.equal(originalBody, payload);
});

test("follows the exact concurrent InstaBox resume operation without issuing another start", async (t) => {
  restoreFetch(t);
  let originCalls = 0;
  let operationReads = 0;
  globalThis.fetch = async (request, init = {}) => {
    const url = typeof request === "string" ? request : request.url;
    if (url === "https://api.instabox.sh/v1/machines/m_test123/wake") {
      return new Response(JSON.stringify({
        error: { code: "operation_conflict", operationId: "existing-operation" },
      }), { status: 409 });
    }
    if (url === "https://api.instabox.sh/v1/operations/existing-operation") {
      operationReads += 1;
      const operation = operationReads === 1
        ? { type: "resume", state: "running" }
        : {
            type: "resume",
            state: "succeeded",
            machine: { state: "running", runtime: { ready: true } },
          };
      return new Response(JSON.stringify(operation), { status: 200 });
    }
    if (url === "https://api.instabox.sh/v1/machines/m_test123/start") {
      throw new Error("must not issue a second start");
    }
    if (url.startsWith(apiOrigin)) {
      originCalls += 1;
      if (originCalls === 1) return new Response("machine resuming", { status: 503 });
      return new Response("awake", { status: 200 });
    }
    throw new Error("unexpected fetch: " + url);
  };
  const response = await worker.fetch(new Request("https://api.testagram.site/healthz"), env({
    MACHINE_ID: "m_test123",
    INSTABOX_API_KEY: "ci-test-key",
  }));
  assert.equal(response.status, 200);
  assert.equal(await response.text(), "awake");
  assert.equal(originCalls, 2);
  assert.equal(operationReads, 2);
});

test("does not override a manually suspended or stopped machine", async (t) => {
  restoreFetch(t);
  let originCalls = 0;
  let wakeCalls = 0;
  let startCalls = 0;
  globalThis.fetch = async (request, init = {}) => {
    const url = typeof request === "string" ? request : request.url;
    if (url === "https://api.instabox.sh/v1/machines/m_test123/wake") {
      wakeCalls += 1;
      return new Response(JSON.stringify({ error: { code: "machine_not_running" } }), { status: 409 });
    }
    if (url === "https://api.instabox.sh/v1/machines/m_test123/start") startCalls += 1;
    if (url.startsWith(apiOrigin)) {
      originCalls += 1;
      return new Response("machine asleep", { status: 503 });
    }
    throw new Error("unexpected fetch: " + url);
  };
  const response = await worker.fetch(new Request("https://api.testagram.site/healthz"), env({
    MACHINE_ID: "m_test123",
    INSTABOX_API_KEY: "ci-test-key",
  }));
  assert.equal(response.status, 503);
  assert.equal((await response.json()).error, "machine_wake_failed");
  assert.equal(originCalls, 1);
  assert.equal(wakeCalls, 1);
  assert.equal(startCalls, 0);
});

test("does not replay a non-idempotent POST after waking the machine", async (t) => {
  restoreFetch(t);
  let originCalls = 0;
  let wakeCalls = 0;
  let wakeIdempotencyKey = "";
  globalThis.fetch = async (request, init) => {
    const url = typeof request === "string" ? request : request.url;
    if (url === "https://api.instabox.sh/v1/machines/m_test123/wake") {
      wakeCalls += 1;
      wakeIdempotencyKey = new Headers(init?.headers).get("idempotency-key") ?? "";
      return new Response(JSON.stringify({ state: "running", runtime: { ready: true } }), { status: 200 });
    }
    if (url.startsWith(apiOrigin)) {
      originCalls += 1;
      return new Response("temporary gateway failure", { status: 503 });
    }
    throw new Error("unexpected fetch: " + url);
  };
  const response = await worker.fetch(new Request("https://api.testagram.site/api/posts", {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ text: "do not duplicate" }),
  }), env({ MACHINE_ID: "m_test123", INSTABOX_API_KEY: "ci-test-key" }));
  assert.equal(response.status, 503);
  assert.equal((await response.json()).error, "machine_awake_retry_required");
  assert.equal(originCalls, 1);
  assert.equal(wakeCalls, 1);
});

test("does not replay unrelated mutations even when they carry an Idempotency-Key", async (t) => {
  restoreFetch(t);
  let originCalls = 0;
  let wakeCalls = 0;
  globalThis.fetch = async (request, init = {}) => {
    const url = typeof request === "string" ? request : request.url;
    if (url === "https://api.instabox.sh/v1/machines/m_test123/wake") {
      wakeCalls += 1;
      return new Response(JSON.stringify({ state: "running", runtime: { ready: true } }), { status: 200 });
    }
    if (url.startsWith(apiOrigin)) {
      originCalls += 1;
      return new Response("temporary gateway failure", { status: 503 });
    }
    throw new Error("unexpected fetch: " + url);
  };

  const response = await worker.fetch(new Request("https://api.testagram.site/api/social/posts/post-1/replies", {
    method: "POST",
    headers: {
      "content-type": "application/json",
      "idempotency-key": "reply-create-123",
    },
    body: JSON.stringify({ content: "do not duplicate" }),
  }), env({ MACHINE_ID: "m_test123", INSTABOX_API_KEY: "ci-test-key" }));

  assert.equal(response.status, 503);
  assert.equal((await response.json()).error, "machine_awake_retry_required");
  assert.equal(originCalls, 1);
  assert.equal(wakeCalls, 1);
});

test("preserves InstaBox max-run cooldown Retry-After for clients", async (t) => {
  restoreFetch(t);
  let originCalls = 0;
  let wakeCalls = 0;
  globalThis.fetch = async (request) => {
    const url = typeof request === "string" ? request : request.url;
    if (url === "https://api.instabox.sh/v1/machines/m_test123/wake") {
      wakeCalls += 1;
      return new Response(JSON.stringify({
        error: {
          code: "machine_cooldown",
          message: "The machine is cooling down after its maximum run.",
          retryAfterSeconds: 120,
        },
      }), {
        status: 409,
        headers: { "retry-after": "120", "content-type": "application/json" },
      });
    }
    if (url.startsWith(apiOrigin)) {
      originCalls += 1;
      return new Response("machine paused", { status: 503 });
    }
    throw new Error("unexpected fetch: " + url);
  };

  const response = await worker.fetch(new Request("https://api.testagram.site/healthz", {
    headers: { origin: "https://testagram.site" },
  }), env({
    MACHINE_ID: "m_test123",
    INSTABOX_API_KEY: "ci-test-key",
  }));

  assert.equal(response.status, 503);
  assert.equal((await response.json()).error, "machine_wake_failed");
  assert.equal(response.headers.get("retry-after"), "120");
  assert.equal(response.headers.get("x-testagram-recovery"), "attempted");
  assert.equal(response.headers.get("access-control-allow-origin"), "https://testagram.site");
  const exposedHeaders = response.headers.get("access-control-expose-headers").toLowerCase();
  assert.match(exposedHeaders, /retry-after/);
  assert.match(exposedHeaders, /x-testagram-recovery/);
  assert.equal(originCalls, 1);
  assert.equal(wakeCalls, 1);
});

test("does not reflect an unapproved origin on machine recovery responses", async (t) => {
  restoreFetch(t);
  globalThis.fetch = async (request) => {
    const url = typeof request === "string" ? request : request.url;
    if (url === "https://api.instabox.sh/v1/machines/m_test123/wake") {
      return new Response(JSON.stringify({
        error: { code: "machine_cooldown", retryAfterSeconds: 30 },
      }), {
        status: 409,
        headers: { "retry-after": "30", "content-type": "application/json" },
      });
    }
    if (url.startsWith(apiOrigin)) return new Response("machine paused", { status: 503 });
    throw new Error("unexpected fetch: " + url);
  };

  const response = await worker.fetch(new Request("https://api.testagram.site/healthz", {
    headers: { origin: "https://evil.example" },
  }), env({ MACHINE_ID: "m_test123", INSTABOX_API_KEY: "ci-test-key" }));

  assert.equal(response.status, 503);
  assert.equal(response.headers.get("access-control-allow-origin"), null);
  assert.equal(response.headers.get("retry-after"), "30");
});

test("returns a retryable 503 when the origin fails and wake credentials are absent", async (t) => {
  restoreFetch(t);
  globalThis.fetch = async () => { throw new Error("network failure"); };
  const response = await worker.fetch(new Request("https://api.testagram.site/healthz"), env());
  assert.equal(response.status, 503);
  assert.equal((await response.json()).error, "machine_wake_failed");
  assert.equal(response.headers.get("retry-after"), "5");
});


test("canonical production cutover verifies real public federation contracts after deployment", () => {
  const workflow = readFileSync(new URL("../.github/workflows/restore-canonical-host-routing.yml", import.meta.url), "utf8");
  assert.match(workflow, /function check_public_federation/);
  assert.match(workflow, /acct:crissbeat@testagram\.site/);
  assert.match(workflow, /nodeinfo-discovery\.headers/);
  assert.match(workflow, /nodeinfo\.json/);
  assert.match(workflow, /actor\.json/);
  assert.match(workflow, /account_not_found/);
  assert.match(workflow, /check_public_federation "\$tmp"/);
  assert.doesNotMatch(workflow, /apex_webfinger=.*nonexistent-probe@invalid\.testagram\.site/);
});
