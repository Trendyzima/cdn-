import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import worker, { RecoveryCoordinator } from "./worker.js";

const apiOrigin = "https://machine-api.instabox.sh";
const cdnOrigin = "https://machine-cdn.instabox.sh";
const apexOrigin = "https://machine-apex.instabox.sh";

function env(overrides = {}) {
  const state = new Map();
  const stub = { async fetch(url, init = {}) {
    const path = new URL(url).pathname;
    if (path === "/key" && init.method === "POST") {
      const { action } = JSON.parse(init.body);
      if (!state.has(action)) state.set(action, "testagram-" + action + "-test-epoch");
      return Response.json({ ok: true, key: state.get(action) });
    }
    if (path === "/complete" && init.method === "POST") { state.clear(); return Response.json({ ok: true }); }
    return new Response("Not Found", { status: 404 });
  } };
  const RECOVERY_COORDINATOR = { idFromName: (name) => name, get: () => stub };
  return { API_ORIGIN: apiOrigin, CDN_ORIGIN: cdnOrigin, APEX_ORIGIN: apexOrigin, EDGE_RECOVERY_READINESS_TOKEN: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", RECOVERY_COORDINATOR, ...overrides };
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


test("does not start a second provider operation when the safe retry transport fails", async (t) => {
  restoreFetch(t);
  let wakeCalls = 0;
  let readinessCalls = 0;
  let originCalls = 0;
  globalThis.fetch = async (request) => {
    const url = typeof request === "string" ? request : request.url;
    if (url === apiOrigin + "/internal/edge/recovery-ready") {
      readinessCalls += 1;
      return Response.json({ ok: true, ready: true, service: "testagram-api" });
    }
    if (url === apiOrigin + "/api/social/feed") {
      originCalls += 1;
      if (originCalls === 1) return new Response("gateway unavailable", { status: 502 });
      throw new Error("retry transport failed");
    }
    if (url === "https://api.instabox.sh/v1/machines/m_test123/wake") {
      wakeCalls += 1;
      return Response.json({ id: "m_test123", state: "running", desiredState: "running", runtime: { ready: true } });
    }
    throw new Error("unexpected fetch: " + url);
  };
  const response = await worker.fetch(
    new Request("https://api.testagram.site/api/social/feed"),
    env({ MACHINE_ID: "m_test123", INSTABOX_API_KEY: "ci-test-key" }),
  );
  assert.equal(response.status, 503);
  assert.equal((await response.json()).error, "machine_waking_or_unavailable");
  assert.equal(originCalls, 2);
  assert.equal(wakeCalls, 1, "one inbound request may initiate only one provider lifecycle operation");
  assert.equal(readinessCalls, 1);
});


test("does not trust a client-forged Backend response marker when the provider origin is unavailable", async (t) => {
  restoreFetch(t);
  let wakeCalls = 0;
  let originCalls = 0;
  globalThis.fetch = async (request) => {
    const url = typeof request === "string" ? request : request.url;
    if (url === apiOrigin + "/api/social/feed") {
      originCalls += 1;
      assert.equal(request.headers.get("x-testagram-service"), null, "Worker must strip client-supplied response identity");
      if (originCalls === 1) return new Response("provider unavailable", { status: 503 });
      return new Response("feed ready", { status: 200 });
    }
    if (url === "https://api.instabox.sh/v1/machines/m_test123/wake") {
      wakeCalls += 1;
      return Response.json({ id: "m_test123", state: "running", desiredState: "running", runtime: { ready: true } });
    }
    if (url === apiOrigin + "/internal/edge/recovery-ready") {
      return Response.json({ ok: true, ready: true, service: "testagram-api" });
    }
    throw new Error("unexpected fetch: " + url);
  };
  const response = await worker.fetch(new Request("https://api.testagram.site/api/social/feed", {
    headers: { "x-testagram-service": "testagram-api" },
  }), env({ MACHINE_ID: "m_test123", INSTABOX_API_KEY: "ci-test-key" }));
  assert.equal(response.status, 200);
  assert.equal(await response.text(), "feed ready");
  assert.equal(wakeCalls, 1);
  assert.equal(originCalls, 2);
});

test("does not wake the provider for an application-generated 503 with a Backend request ID", async (t) => {
  restoreFetch(t);
  let wakeCalls = 0;
  let originCalls = 0;
  globalThis.fetch = async (request) => {
    const url = typeof request === "string" ? request : request.url;
    if (url === apiOrigin + "/api/social/feed") {
      originCalls += 1;
      return new Response(JSON.stringify({ ok: false, error: { code: "DEPENDENCY_UNAVAILABLE" } }), {
        status: 503,
        headers: { "x-request-id": "req_backend_123", "x-testagram-service": "testagram-api", "content-type": "application/json" },
      });
    }
    if (url.includes("/v1/machines/")) {
      wakeCalls += 1;
      throw new Error("application 503 must not initiate a provider lifecycle operation");
    }
    throw new Error("unexpected fetch: " + url);
  };
  const response = await worker.fetch(
    new Request("https://api.testagram.site/api/social/feed"),
    env({ MACHINE_ID: "m_test123", INSTABOX_API_KEY: "ci-test-key" }),
  );
  assert.equal(response.status, 503);
  assert.equal(response.headers.get("x-request-id"), "req_backend_123");
  assert.equal(originCalls, 1);
  assert.equal(wakeCalls, 0);
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
      return new Response(JSON.stringify({ id: "m_test123", state: "running", desiredState: "running", runtime: { ready: true } }), { status: 200 });
    }
    if (url === "https://api.instabox.sh/v1/machines/m_test123/start") {
      throw new Error("wake must never use /start for an idle-suspended machine");
    }
    if (url === apiOrigin + "/internal/edge/recovery-ready") { return Response.json({ ok: true, ready: true, service: "testagram-api" }); }
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


test("polls the exact provider machine after wake acceptance until runtime readiness", async (t) => {
  restoreFetch(t);
  let machineReads = 0;
  let originCalls = 0;
  globalThis.fetch = async (request, init = {}) => {
    const url = typeof request === "string" ? request : request.url;
    if (url === "https://api.instabox.sh/v1/machines/m_test123/wake") {
      return Response.json({ machine: {
        id: "m_test123", state: "starting", desiredState: "running",
        runtime: { ready: false },
      } }, { status: 202 });
    }
    if (url === "https://api.instabox.sh/v1/machines/m_test123" && (init.method || "GET") === "GET") {
      machineReads += 1;
      return Response.json({ machine: machineReads === 1
        ? { id: "m_test123", state: "starting", desiredState: "running", runtime: { ready: false } }
        : { id: "m_test123", state: "running", desiredState: "running", runtime: { ready: true } } });
    }
    if (url === apiOrigin + "/internal/edge/recovery-ready") {
      return Response.json({ ok: true, ready: true, service: "testagram-api" });
    }
    if (url === apiOrigin + "/healthz") {
      originCalls += 1;
      return originCalls === 1
        ? new Response("origin unavailable", { status: 503 })
        : new Response("api ready", { status: 200 });
    }
    throw new Error("unexpected fetch: " + url);
  };

  const response = await worker.fetch(new Request("https://api.testagram.site/healthz"), env({
    MACHINE_ID: "m_test123",
    INSTABOX_API_KEY: "ci-test-key",
  }));
  assert.equal(response.status, 200);
  assert.equal(await response.text(), "api ready");
  assert.equal(machineReads, 2);
  assert.equal(originCalls, 2);
});


test("follows an asynchronous operation returned by the provider wake endpoint", async (t) => {
  restoreFetch(t);
  let operationReads = 0;
  let originCalls = 0;
  globalThis.fetch = async (request) => {
    const url = typeof request === "string" ? request : request.url;
    if (url === "https://api.instabox.sh/v1/machines/m_test123/wake") {
      return Response.json({ operation: { id: "wake-op-456", type: "resume", state: "queued" } }, { status: 202 });
    }
    if (url === "https://api.instabox.sh/v1/operations/wake-op-456") {
      operationReads += 1;
      return Response.json({ operation: operationReads === 1
        ? { id: "wake-op-456", type: "resume", state: "running" }
        : { id: "wake-op-456", type: "resume", state: "succeeded",
            machine: { id: "m_test123", state: "running", desiredState: "running", runtime: { ready: true } } } });
    }
    if (url === apiOrigin + "/internal/edge/recovery-ready") {
      return Response.json({ ok: true, ready: true, service: "testagram-api" });
    }
    if (url === apiOrigin + "/healthz") {
      originCalls += 1;
      return originCalls === 1
        ? new Response("origin unavailable", { status: 503 })
        : new Response("api ready", { status: 200 });
    }
    throw new Error("unexpected fetch: " + url);
  };

  const response = await worker.fetch(new Request("https://api.testagram.site/healthz"), env({
    MACHINE_ID: "m_test123",
    INSTABOX_API_KEY: "ci-test-key",
  }));
  assert.equal(response.status, 200);
  assert.equal(operationReads, 2);
  assert.equal(originCalls, 2);
});


test("independently verifies machine identity when the wake response omits its ID", async (t) => {
  restoreFetch(t);
  let originCalls = 0;
  let machineReads = 0;
  globalThis.fetch = async (request, init = {}) => {
    const url = typeof request === "string" ? request : request.url;
    if (url === "https://api.instabox.sh/v1/machines/m_test123/wake") {
      return Response.json({ state: "running", runtime: { ready: true } });
    }
    if (url === "https://api.instabox.sh/v1/machines/m_test123" && (init.method || "GET") === "GET") {
      machineReads += 1;
      return Response.json({ machine: {
        id: "m_test123", state: "running", desiredState: "running",
        runtime: { ready: true },
      } });
    }
    if (url === apiOrigin + "/internal/edge/recovery-ready") { return Response.json({ ok: true, ready: true, service: "testagram-api" }); }
    if (url.startsWith(apiOrigin)) {
      originCalls += 1;
      if (originCalls === 1) return new Response("machine waking", { status: 503 });
      return new Response("awake", { status: 200 });
    }
    throw new Error("unexpected fetch: " + url);
  };

  const response = await worker.fetch(new Request("https://api.testagram.site/healthz"), env({
    MACHINE_ID: "m_test123",
    INSTABOX_API_KEY: "ci-test-key",
  }));
  assert.equal(response.status, 200);
  assert.equal(machineReads, 1);
  assert.equal(originCalls, 2);
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
            machine: { id: "m_test123", state: "running", desiredState: "running", runtime: { ready: true } },
          };
      return new Response(JSON.stringify(operation), { status: 200 });
    }
    if (url === "https://api.instabox.sh/v1/machines/m_test123/start") {
      throw new Error("must not issue a second start");
    }
    if (url === apiOrigin + "/internal/edge/recovery-ready") { return Response.json({ ok: true, ready: true, service: "testagram-api" }); }
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


test("does not override a manually stopped or suspended machine", async (t) => {
  restoreFetch(t);
  let originCalls = 0;
  let wakeCalls = 0;
  let startCalls = 0;
  let machineReads = 0;
  globalThis.fetch = async (request, init = {}) => {
    const url = typeof request === "string" ? request : request.url;
    if (url === "https://api.instabox.sh/v1/machines/m_test123/wake") {
      wakeCalls += 1;
      return new Response(JSON.stringify({ error: { code: "machine_not_running" } }), { status: 409 });
    }
    if (url === "https://api.instabox.sh/v1/machines/m_test123" && (init.method || "GET") === "GET") {
      machineReads += 1;
      return Response.json({ machine: {
        id: "m_test123", name: "testagramdb", state: "stopped",
        desiredState: "stopped", operationId: null,
      } });
    }
    if (url === "https://api.instabox.sh/v1/machines/m_test123/start") startCalls += 1;
    if (url === apiOrigin + "/internal/edge/recovery-ready") { return Response.json({ ok: true, ready: true, service: "testagram-api" }); }
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
  assert.equal(machineReads, 1);
  assert.equal(startCalls, 0);
});


test("starts an unexpectedly stopped VPS only while desiredState remains running", async (t) => {
  restoreFetch(t);
  let originCalls = 0;
  let wakeCalls = 0;
  let startCalls = 0;
  let operationReads = 0;
  let startKey = "";
  globalThis.fetch = async (request, init = {}) => {
    const url = typeof request === "string" ? request : request.url;
    const method = typeof request === "string" ? (init.method || "GET") : request.method;
    const headers = typeof request === "string" ? new Headers(init.headers) : request.headers;
    if (url === "https://api.instabox.sh/v1/machines/m_test123/wake") {
      wakeCalls += 1;
      return new Response(JSON.stringify({ error: { code: "machine_not_running" } }), { status: 409 });
    }
    if (url === "https://api.instabox.sh/v1/machines/m_test123" && method === "GET") {
      return Response.json({ machine: {
        id: "m_test123", name: "testagramdb", state: "stopped",
        desiredState: "running", operationId: null,
      } });
    }
    if (url === "https://api.instabox.sh/v1/machines/m_test123/start" && method === "POST") {
      startCalls += 1;
      startKey = headers.get("idempotency-key") || "";
      assert.equal(headers.get("x-api-key"), "ci-test-key");
      assert.deepEqual(await new Response(init.body).json(), {});
      return Response.json({ operation: { id: "start-op-123", type: "start", state: "queued" } }, { status: 202 });
    }
    if (url === "https://api.instabox.sh/v1/operations/start-op-123") {
      operationReads += 1;
      const operation = operationReads === 1
        ? { id: "start-op-123", type: "start", state: "running" }
        : {
            id: "start-op-123", type: "start", state: "succeeded",
            machine: { id: "m_test123", state: "running", desiredState: "running", runtime: { ready: true } },
          };
      return Response.json({ operation });
    }
    if (url === apiOrigin + "/internal/edge/recovery-ready") { return Response.json({ ok: true, ready: true, service: "testagram-api" }); }
    if (url.startsWith(apiOrigin)) {
      originCalls += 1;
      if (originCalls === 1) return new Response("machine stopped", { status: 503 });
      return new Response("awake after start", { status: 200 });
    }
    throw new Error("unexpected fetch: " + method + " " + url);
  };

  const response = await worker.fetch(new Request("https://api.testagram.site/healthz"), env({
    MACHINE_ID: "m_test123",
    INSTABOX_API_KEY: "ci-test-key",
  }));
  assert.equal(response.status, 200);
  assert.equal(await response.text(), "awake after start");
  assert.equal(wakeCalls, 1);
  assert.equal(startCalls, 1);
  assert.match(startKey, /^testagram-start-[0-9a-f-]{36}$/i);
  assert.equal(operationReads, 2);
  assert.equal(originCalls, 2);
});


test("wakes a stopped VPS on login but never replays the credential-bearing POST", async (t) => {
  restoreFetch(t);
  let originCalls = 0;
  let startCalls = 0;
  globalThis.fetch = async (request, init = {}) => {
    const url = typeof request === "string" ? request : request.url;
    const method = typeof request === "string" ? (init.method || "GET") : request.method;
    if (url === "https://api.instabox.sh/v1/machines/m_test123/wake") {
      return new Response(JSON.stringify({ error: { code: "machine_not_running" } }), { status: 409 });
    }
    if (url === "https://api.instabox.sh/v1/machines/m_test123" && method === "GET") {
      return Response.json({ machine: {
        id: "m_test123", state: "stopped", desiredState: "running", operationId: null,
      } });
    }
    if (url === "https://api.instabox.sh/v1/machines/m_test123/start" && method === "POST") {
      startCalls += 1;
      return Response.json({ operation: {
        id: "login-start-op", type: "start", state: "queued",
      } }, { status: 202 });
    }
    if (url === "https://api.instabox.sh/v1/operations/login-start-op") {
      return Response.json({ operation: {
        id: "login-start-op", type: "start", state: "succeeded",
        machine: { id: "m_test123", state: "running", desiredState: "running", runtime: { ready: true } },
      } });
    }
    if (url === apiOrigin + "/internal/edge/recovery-ready") { return Response.json({ ok: true, ready: true, service: "testagram-api" }); }
    if (url.startsWith(apiOrigin)) {
      originCalls += 1;
      return new Response("machine stopped", { status: 503 });
    }
    throw new Error("unexpected fetch: " + method + " " + url);
  };

  const response = await worker.fetch(new Request("https://api.testagram.site/api/auth/login", {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ email: "user@example.test", password: "never-log-this" }),
  }), env({ MACHINE_ID: "m_test123", INSTABOX_API_KEY: "ci-test-key" }));

  assert.equal(response.status, 503);
  assert.equal((await response.json()).error, "application_ready_retry_required");
  assert.equal(response.headers.get("x-testagram-recovery"), "ready");
  assert.equal(startCalls, 1);
  assert.equal(originCalls, 1);
});


test("keeps login intent pending until provider and PostgreSQL readiness are independently confirmed", async (t) => {
  restoreFetch(t);
  let originCalls = 0;
  let readinessCalls = 0;
  let wakeCalls = 0;
  globalThis.fetch = async (request, init = {}) => {
    const url = typeof request === "string" ? request : request.url;
    if (url === "https://api.instabox.sh/v1/machines/m_test123/wake") {
      wakeCalls += 1;
      return Response.json({ id: "m_test123", state: "running", desiredState: "running", runtime: { ready: true } });
    }
    if (url === apiOrigin + "/internal/edge/recovery-ready") {
      readinessCalls += 1;
      assert.equal(new Headers(init.headers).get("authorization"), "Bearer 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef");
      if (readinessCalls < 3) return new Response("not ready", { status: 503 });
      return Response.json({ ok: true, ready: true, service: "testagram-api" });
    }
    if (url === apiOrigin + "/api/auth/login") {
      originCalls += 1;
      return new Response("origin gateway timeout", { status: 504 });
    }
    throw new Error("unexpected fetch: " + url);
  };

  const response = await worker.fetch(new Request("https://api.testagram.site/api/auth/login", {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ email: "user@example.test", password: "never-log-this" }),
  }), env({ MACHINE_ID: "m_test123", INSTABOX_API_KEY: "ci-test-key" }));

  assert.equal(response.status, 503);
  assert.equal((await response.json()).error, "application_ready_retry_required");
  assert.equal(response.headers.get("x-testagram-recovery"), "ready");
  assert.equal(wakeCalls, 1);
  assert.equal(readinessCalls, 3);
  assert.equal(originCalls, 1, "gateway must not replay credential-bearing login POST");
});


test("does not expose the edge-only readiness capability through the public API hostname", async () => {
  const response = await worker.fetch(new Request("https://api.testagram.site/internal/edge/recovery-ready"), env({
    MACHINE_ID: "m_test123",
    INSTABOX_API_KEY: "ci-test-key",
  }));
  assert.equal(response.status, 404);
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
      return new Response(JSON.stringify({ id: "m_test123", state: "running", desiredState: "running", runtime: { ready: true } }), { status: 200 });
    }
    if (url === apiOrigin + "/internal/edge/recovery-ready") { return Response.json({ ok: true, ready: true, service: "testagram-api" }); }
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
      return new Response(JSON.stringify({ id: "m_test123", state: "running", desiredState: "running", runtime: { ready: true } }), { status: 200 });
    }
    if (url === apiOrigin + "/internal/edge/recovery-ready") { return Response.json({ ok: true, ready: true, service: "testagram-api" }); }
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
    if (url === apiOrigin + "/internal/edge/recovery-ready") { return Response.json({ ok: true, ready: true, service: "testagram-api" }); }
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
    if (url === apiOrigin + "/internal/edge/recovery-ready") return Response.json({ ok: true, ready: true, service: "testagram-api" });
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



test("API hostname is attached as a Worker Custom Domain for the guarded legacy-owner handoff", () => {
  const config = readFileSync(new URL("./wrangler.toml", import.meta.url), "utf8");
  const lines = config.split("\n");
  const apiCustomDomain = lines.some((line, index) =>
    line.trim() === 'pattern = "api.testagram.site"' &&
    lines[index + 1]?.trim() === "custom_domain = true"
  );
  assert.ok(apiCustomDomain, "api.testagram.site must be a Wrangler Custom Domain");
  assert.ok(!config.includes('pattern = "api.testagram.site/*"'));
  const apiRoute = config.split("[[routes]]").find((route) => route.includes('pattern = "api.testagram.site"'));
  assert.ok(apiRoute?.includes("custom_domain = true"));
});

test("API custom-domain transfer is separately approved, owner-pinned, verified and rolled back", () => {
  const workflow = readFileSync(new URL("../.github/workflows/restore-canonical-host-routing.yml", import.meta.url), "utf8");
  assert.ok(workflow.includes("confirm_legacy_api_domain_transfer"));
  assert.ok(workflow.includes('echo "machine_id=$MACHINE_ID"'));
  assert.ok(workflow.includes('--var "MACHINE_ID:${{ steps.validate_origins.outputs.machine_id }}"'));
  assert.ok(!workflow.includes('--var "MACHINE_ID:${{ inputs.machine_id }}"'));
  assert.ok(workflow.includes("CONFIRM_LEGACY_API_DOMAIN_TRANSFER"));
  assert.ok(workflow.includes("legacy_api_domain_id"));
  assert.ok(workflow.includes("Detach only the explicitly approved legacy API custom domain"));
  assert.ok(workflow.includes("Release only api.testagram.site DNS records for the custom-domain handoff"));
  assert.ok(workflow.includes('.result.name == "api.testagram.site"'));
  assert.ok(workflow.includes("Verify api.testagram.site is owned by the canonical gateway"));
  assert.ok(workflow.includes("Ensure API hostname rolls forward only to the native gateway after a failed cutover"));
  assert.ok(workflow.includes("Do not restore the legacy API Worker"));
  assert.ok(!workflow.includes("legacy_api_domain_payload"));
  assert.ok(workflow.includes('.result.hostname == "api.testagram.site"'));
  assert.ok(workflow.includes('.result.service == "testagram-api"'));
  assert.ok(workflow.includes('.result.service == "testagram-cdn"'));
  assert.ok(workflow.includes('curl --retry 2 --retry-all-errors --retry-delay 2 -fsS -X DELETE "$API/accounts/$CF_ACCOUNT_ID/workers/domains/$domain_id"'));
  assert.ok(workflow.includes('curl --retry 2 --retry-all-errors --retry-delay 2 -fsS -X PUT "$API/accounts/$CF_ACCOUNT_ID/workers/domains"'));
  const preflightIndex = workflow.indexOf("Preflight Cloudflare zone and existing hostname ownership");
  const detachIndex = workflow.indexOf("Detach only the explicitly approved legacy API custom domain");
  const dnsReleaseIndex = workflow.indexOf("Release only api.testagram.site DNS records for the custom-domain handoff");
  const deployIndex = workflow.indexOf("Deploy Worker gateway on canonical API, media, www and apex hostnames");
  const ownerVerifyIndex = workflow.indexOf("Verify api.testagram.site is owned by the canonical gateway");
  const gatewayVerifyIndex = workflow.indexOf("Verify canonical API, Go CDN, Flutter web and apex identities");
  assert.ok(preflightIndex < detachIndex && detachIndex < dnsReleaseIndex && dnsReleaseIndex < deployIndex && deployIndex < ownerVerifyIndex && ownerVerifyIndex < gatewayVerifyIndex);
});


test("detached legacy API Worker cleanup is separately approved and native-gateway guarded", () => {
  const workflow = readFileSync(new URL("../.github/workflows/cleanup-detached-api-worker.yml", import.meta.url), "utf8");
  assert.ok(workflow.includes("confirm_remove_detached_api_worker"));
  assert.ok(workflow.includes("if: inputs.confirm_remove_detached_api_worker == true"));
  assert.ok(workflow.includes('select(.hostname == "api.testagram.site" and .zone_id == $zone and .service == "testagram-cdn"'));
  assert.ok(workflow.includes("https://api.testagram.site/healthz?cleanup_probe="));
  assert.ok(workflow.includes("https://api.testagram.site/readyz?cleanup_probe="));
  assert.ok(workflow.includes("https://api.testagram.site/metrics?cleanup_probe="));
  assert.ok(workflow.includes("https://api.testagram.site/internal/tv/catalog?cleanup_probe="));
  assert.ok(workflow.includes("x-testagram-service"));
  assert.ok(workflow.includes("workers/scripts/testagram-api/schedules"));
  assert.ok(workflow.includes("workers/scripts/testagram-api/settings"));
  assert.ok(workflow.includes('select(.type == "service" or .type == "queue")'));
  assert.ok(workflow.includes("workers/scripts/testagram-api/script-settings"));
  assert.ok(workflow.includes("/pages/projects?per_page=100&page=1"));
  assert.ok(workflow.includes("Pages project inventory is paginated"));
  assert.ok(workflow.includes("Pages project $project still references testagram-api"));
  assert.ok(workflow.includes('select(.script == "testagram-api")'));
  assert.ok(workflow.includes('any(.type == "service" and .service == $target)'));
  assert.ok(workflow.includes('any(.service == $target)'));
  assert.ok(workflow.includes('DELETE'));
  assert.ok(workflow.includes('/workers/scripts/testagram-api'));
  assert.ok(workflow.includes("The legacy API Worker remains after deletion"));
  assert.ok(!workflow.includes("/dns_records"));
  const approvalIndex = workflow.indexOf("confirm_remove_detached_api_worker");
  const ownerIndex = workflow.indexOf("api_domain_count=");
  const healthIndex = workflow.indexOf("cleanup_probe=");
  const routeIndex = workflow.indexOf("Worker route inventory is paginated");
  const deleteIndex = workflow.indexOf('DELETE');
  const verifyIndex = workflow.indexOf("The legacy API Worker remains after deletion");
  assert.ok(approvalIndex < ownerIndex && ownerIndex < healthIndex && healthIndex < routeIndex && routeIndex < deleteIndex && deleteIndex < verifyIndex);
});


test("canonical production cutover verifies real public federation contracts after deployment", () => {
  const workflow = readFileSync(new URL("../.github/workflows/restore-canonical-host-routing.yml", import.meta.url), "utf8");
  assert.match(workflow, /check_public_federation\(\) \{/);
  assert.match(workflow, /acct:crissbeat@testagram\.site/);
  assert.equal([...workflow.matchAll(/acct:testagramprobe2026@testagram\.site/g)].length, 2);
  assert.match("testagramprobe2026", /^[A-Za-z0-9_]{3,30}$/);
  assert.match(workflow, /nodeinfo-discovery\.headers/);
  assert.match(workflow, /nodeinfo\.json/);
  assert.match(workflow, /actor\.json/);
  assert.match(workflow, /account_not_found/);
  assert.match(workflow, /check_public_federation "\$tmp"/);
  assert.doesNotMatch(workflow, /apex_webfinger=.*nonexistent-probe@invalid\.testagram\.site/);
  assert.match(workflow, /legacy_webfinger_route_id/);
  assert.ok(workflow.includes("testagram.site/.well-known/webfinger*"));
  assert.match(workflow, /legacy_script.*testagram-web/);
  assert.match(workflow, /The exact legacy WebFinger route changed after preflight/);
  assert.match(workflow, /Remove the exact stale WebFinger route from Flutter Pages Worker/);
  assert.match(workflow, /conflicting WebFinger route remains after deletion/);
  const secretIndex = workflow.indexOf("Store the InstaBox and application-readiness credentials as Worker secrets");
  const readinessPreflightIndex = workflow.indexOf("Verify the shared application-readiness capability before hostname cutover");
  const r2ReleaseIndex = workflow.indexOf("Release the existing R2 custom-domain binding for Go CDN ownership");
  const apiDNSReleaseIndex = workflow.indexOf("Release only api.testagram.site DNS records for the custom-domain handoff");
  const deployIndex = workflow.indexOf("Deploy Worker gateway on canonical API, media, www and apex hostnames");
  const removeIndex = workflow.indexOf("Remove the exact stale WebFinger route from Flutter Pages Worker");
  const verifyIndex = workflow.indexOf("Verify canonical API, Go CDN, Flutter web and apex identities");
  assert.ok(secretIndex < readinessPreflightIndex && readinessPreflightIndex < r2ReleaseIndex && r2ReleaseIndex < apiDNSReleaseIndex, "verify the shared readiness capability before any destructive hostname/DNS change");
  assert.ok(workflow.includes('"$API_ORIGIN/internal/edge/recovery-ready"'));
  assert.ok(deployIndex < removeIndex && removeIndex < verifyIndex);
});


test("recovers an ambiguous lost wake response by following the provider's existing operation on the next inbound retry", async (t) => {
  restoreFetch(t);
  let acceptedOperations = 0;
  let wakeCalls = 0;
  let originCalls = 0;
  let operationReads = 0;
  let readinessCalls = 0;
  const wakeKeys = [];
  globalThis.fetch = async (request, init = {}) => {
    const url = typeof request === "string" ? request : request.url;
    const method = typeof request === "string" ? (init.method || "GET") : request.method;
    const headers = typeof request === "string" ? new Headers(init.headers) : request.headers;

    if (url === apiOrigin + "/api/social/feed") {
      originCalls += 1;
      if (originCalls <= 2) return new Response("origin is still waking", { status: 503 });
      return new Response("feed restored", { status: 200 });
    }
    if (url === "https://api.instabox.sh/v1/machines/m_test123/wake" && method === "POST") {
      wakeCalls += 1;
      wakeKeys.push(headers.get("idempotency-key"));
      if (wakeCalls === 1) {
        acceptedOperations += 1;
        // The provider accepted the lifecycle operation, but its response
        // was lost. A later inbound request must discover, not duplicate it.
        throw new Error("simulated response loss after provider acceptance");
      }
      return Response.json({
        error: { code: "operation_conflict", operationId: "accepted-wake-op" },
      }, { status: 409 });
    }
    if (url === "https://api.instabox.sh/v1/operations/accepted-wake-op") {
      operationReads += 1;
      return Response.json({
        id: "accepted-wake-op",
        type: "resume",
        state: "succeeded",
        machine: {
          id: "m_test123",
          state: "running",
          desiredState: "running",
          runtime: { ready: true },
        },
      });
    }
    if (url === apiOrigin + "/internal/edge/recovery-ready") {
      readinessCalls += 1;
      return Response.json({ ok: true, ready: true, service: "testagram-api" });
    }
    throw new Error("unexpected fetch: " + method + " " + url);
  };

  const workerEnv = env({ MACHINE_ID: "m_test123", INSTABOX_API_KEY: "ci-test-key" });
  const first = await worker.fetch(new Request("https://api.testagram.site/api/social/feed"), workerEnv);
  assert.equal(first.status, 503);

  const second = await worker.fetch(new Request("https://api.testagram.site/api/social/feed"), workerEnv);
  assert.equal(second.status, 200);
  assert.equal(await second.text(), "feed restored");
  assert.equal(acceptedOperations, 1, "the simulated provider accepted exactly one lifecycle operation");
  assert.equal(wakeCalls, 2, "the retry discovers the existing operation through the provider conflict response");
  assert.equal(wakeKeys[0], wakeKeys[1], "each inbound request currently has a fresh provider idempotency key");
  assert.equal(operationReads, 1, "the retry follows the original provider operation");
  assert.equal(readinessCalls, 1, "the origin retry waits for application readiness");
  assert.equal(originCalls, 3);
});


test("recovers a lost stopped-machine start response by following the operation on the next inbound retry", async (t) => {
  restoreFetch(t);
  let wakeCalls = 0;
  let machineReads = 0;
  let startCalls = 0;
  const startKeys = [];
  let operationReads = 0;
  let originCalls = 0;
  let readinessCalls = 0;
  globalThis.fetch = async (request, init = {}) => {
    const url = typeof request === "string" ? request : request.url;
    const method = typeof request === "string" ? (init.method || "GET") : request.method;

    if (url === apiOrigin + "/api/social/feed") {
      originCalls += 1;
      if (originCalls <= 2) return new Response("origin still unavailable", { status: 503 });
      return new Response("feed restored after start", { status: 200 });
    }
    if (url === "https://api.instabox.sh/v1/machines/m_test123/wake") {
      wakeCalls += 1;
      return Response.json({ error: { code: "machine_not_running" } }, { status: 409 });
    }
    if (url === "https://api.instabox.sh/v1/machines/m_test123" && method === "GET") {
      machineReads += 1;
      return Response.json({ machine: {
        id: "m_test123",
        state: "stopped",
        desiredState: "running",
        operationId: machineReads === 1 ? null : "accepted-start-op",
      } });
    }
    if (url === "https://api.instabox.sh/v1/machines/m_test123/start" && method === "POST") {
      startCalls += 1;
      startKeys.push(new Headers(init.headers).get("idempotency-key"));
      throw new Error("simulated lost start response after provider accepted operation");
    }
    if (url === "https://api.instabox.sh/v1/operations/accepted-start-op") {
      operationReads += 1;
      return Response.json({ operation: {
        id: "accepted-start-op",
        type: "start",
        state: "succeeded",
        machine: {
          id: "m_test123",
          state: "running",
          desiredState: "running",
          runtime: { ready: true },
        },
      } });
    }
    if (url === apiOrigin + "/internal/edge/recovery-ready") {
      readinessCalls += 1;
      return Response.json({ ok: true, ready: true, service: "testagram-api" });
    }
    throw new Error("unexpected fetch: " + method + " " + url);
  };

  const workerEnv = env({ MACHINE_ID: "m_test123", INSTABOX_API_KEY: "ci-test-key" });
  const first = await worker.fetch(new Request("https://api.testagram.site/api/social/feed"), workerEnv);
  assert.equal(first.status, 503);

  const second = await worker.fetch(new Request("https://api.testagram.site/api/social/feed"), workerEnv);
  assert.equal(second.status, 200);
  assert.equal(await second.text(), "feed restored after start");
  assert.equal(wakeCalls, 2);
  assert.equal(machineReads, 2);
  assert.equal(startCalls, 1, "the retry must follow the provider's active operation rather than POST /start again");
  assert.equal(startKeys.length, 1);
  assert.equal(operationReads, 1);
  assert.equal(readinessCalls, 1);
  assert.equal(originCalls, 3);
});

test("Wrangler binds the durable recovery coordinator and declares its SQLite migration", () => {
  const config = readFileSync(new URL("./wrangler.toml", import.meta.url), "utf8");
  assert.match(config, /name = "RECOVERY_COORDINATOR"/);
  assert.match(config, /class_name = "RecoveryCoordinator"/);
  assert.match(config, /new_sqlite_classes = \["RecoveryCoordinator"\]/);
});

// The recovery epoch is a durable provider-idempotency boundary, not a request cache.
test("Durable Object retains lifecycle keys across retries and rotates them only after completion", async () => {
  const values = new Map();
  const storage = {
    async transaction(callback) { return callback({ get: async (key) => values.get(key), put: async (key, value) => values.set(key, structuredClone(value)) }); },
    async delete(key) { values.delete(key); },
  };
  const coordinator = new RecoveryCoordinator({ storage });
  const keyRequest = (action) => new Request("https://recovery-coordinator/key", { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify({ action }) });
  const first = await (await coordinator.fetch(keyRequest("wake"))).json();
  const retry = await (await coordinator.fetch(keyRequest("wake"))).json();
  const start = await (await coordinator.fetch(keyRequest("start"))).json();
  assert.equal(first.key, retry.key);
  assert.notEqual(first.key, start.key);
  await coordinator.fetch(new Request("https://recovery-coordinator/complete", { method: "POST" }));
  const nextEpoch = await (await coordinator.fetch(keyRequest("wake"))).json();
  assert.notEqual(first.key, nextEpoch.key);
});
