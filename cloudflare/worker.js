const HOSTS = Object.freeze({
  "api.testagram.site": "API_ORIGIN",
  "media.testagram.site": "CDN_ORIGIN",
  "testagram.site": "APEX_ORIGIN",
  "www.testagram.site": "APEX_ORIGIN",
});
const PRIVATE_API_PATHS = new Set(["/readyz", "/metrics"]);
const PRIVATE_MEDIA_PATHS = new Set(["/readyz", "/metrics"]);

function json(body, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: {
      "content-type": "application/json; charset=utf-8",
      "cache-control": "no-store",
      "x-content-type-options": "nosniff",
    },
  });
}

const RECOVERY_CORS_ORIGINS = new Set([
  "https://testagram.site",
  "https://www.testagram.site",
  "http://localhost:5173",
  "http://127.0.0.1:5173",
]);
const CORS_METHODS = ["GET", "HEAD", "POST", "PUT", "DELETE", "OPTIONS"];
const CORS_HEADERS = [
  "authorization",
  "content-type",
  "idempotency-key",
  "x-application-token",
  "x-verification-session-token",
  "x-identity-approval-token",
  "x-identity-approval-jti",
  "x-request-id",
  "accept",
];

function corsPreflight(request) {
  const origin = request.headers.get("origin")?.trim() || "";
  const method = request.headers.get("access-control-request-method")?.trim().toUpperCase() || "";
  if (!RECOVERY_CORS_ORIGINS.has(origin)) {
    return new Response(null, { status: 403, headers: { "cache-control": "no-store" } });
  }
  if (!CORS_METHODS.includes(method)) {
    return new Response(null, { status: 405, headers: { "cache-control": "no-store" } });
  }

  const requestedHeaders = (request.headers.get("access-control-request-headers") || "")
    .split(",")
    .map((value) => value.trim().toLowerCase())
    .filter(Boolean);
  if (requestedHeaders.some((header) => !CORS_HEADERS.includes(header))) {
    return new Response(null, { status: 403, headers: { "cache-control": "no-store" } });
  }

  return new Response(null, {
    status: 204,
    headers: {
      "access-control-allow-origin": origin,
      "access-control-allow-methods": CORS_METHODS.join(", "),
      "access-control-allow-headers": CORS_HEADERS.join(", "),
      "access-control-max-age": "600",
      "access-control-expose-headers": "Retry-After, X-Testagram-Recovery",
      "cache-control": "no-store",
      "vary": "Origin, Access-Control-Request-Method, Access-Control-Request-Headers",
      "x-content-type-options": "nosniff",
    },
  });
}

function exposeRecoveryHeaders(response, request) {
  // Do not reconstruct WebSocket upgrade responses.
  if (response.status === 101) return response;

  const headers = new Headers(response.headers);
  const requestOrigin = request?.headers.get("origin")?.trim() || "";
  if (requestOrigin && RECOVERY_CORS_ORIGINS.has(requestOrigin)) {
    headers.set("access-control-allow-origin", requestOrigin);
    const vary = new Set(
      (headers.get("vary") || "")
        .split(",")
        .map((value) => value.trim())
        .filter(Boolean),
    );
    if (![...vary].some((value) => value === "*")) vary.add("Origin");
    headers.set("vary", [...vary].join(", "));
  }

  const exposed = new Map(
    (headers.get("access-control-expose-headers") || "")
      .split(",")
      .map((value) => value.trim())
      .filter(Boolean)
      .map((value) => [value.toLowerCase(), value]),
  );
  exposed.set("retry-after", "Retry-After");
  exposed.set("x-testagram-recovery", "X-Testagram-Recovery");
  headers.set("access-control-expose-headers", [...exposed.values()].join(", "));

  return new Response(response.body, {
    status: response.status,
    statusText: response.statusText,
    headers,
  });
}

function configuredOrigin(raw, publicHost) {
  if (typeof raw !== "string" || raw.trim() === "") {
    throw new Error("origin_not_configured");
  }
  let origin;
  try {
    origin = new URL(raw);
  } catch {
    throw new Error("origin_invalid");
  }
  if (
    origin.protocol !== "https:" ||
    origin.username !== "" ||
    origin.password !== "" ||
    origin.pathname !== "/" ||
    origin.search !== "" ||
    origin.hash !== "" ||
    origin.port !== "" ||
    origin.hostname === publicHost ||
    origin.hostname === "testagram.site" ||
    origin.hostname.endsWith(".testagram.site") ||
    origin.hostname === "localhost" ||
    origin.hostname.includes(":") ||
    /^\d{1,3}(?:\.\d{1,3}){3}$/.test(origin.hostname) ||
    origin.hostname.endsWith(".local") ||
    origin.hostname.endsWith(".internal") ||
    origin.hostname.endsWith(".example") ||
    origin.hostname.endsWith(".invalid") ||
    origin.hostname.endsWith(".test") ||
    origin.hostname.endsWith(".onion") ||
    origin.hostname.endsWith(".workers.dev") ||
    origin.hostname.endsWith(".pages.dev") ||
    origin.hostname.endsWith(".cloudflare.com") ||
    origin.hostname.endsWith(".cloudflare.net") ||
    origin.hostname === "api.instabox.sh" ||
    !origin.hostname.includes(".")
  ) {
    throw new Error("origin_unsafe");
  }
  return origin;
}

function makeUpstreamRequest(source, target, incoming) {
  const headers = new Headers(source.headers);
  const isWebSocket = source.headers.get("upgrade")?.toLowerCase() === "websocket";

  // Never trust client-supplied forwarding metadata or hop-by-hop headers.
  // Cloudflare's connecting IP is the only source used for X-Forwarded-For.
  const connectionTokens = (headers.get("connection") || "")
    .split(",")
    .map((value) => value.trim().toLowerCase())
    .filter(Boolean);
  for (const name of connectionTokens) headers.delete(name);
  for (const name of [
    "host",
    "connection",
    "keep-alive",
    "proxy-authenticate",
    "proxy-authorization",
    "proxy-connection",
    "te",
    "trailer",
    "transfer-encoding",
    "forwarded",
    "x-forwarded-for",
    "x-forwarded-host",
    "x-forwarded-proto",
    "x-forwarded-port",
    "x-real-ip",
    "cf-connecting-ip",
  ]) {
    headers.delete(name);
  }
  if (isWebSocket) {
    headers.set("connection", "Upgrade");
    headers.set("upgrade", "websocket");
  } else {
    headers.delete("upgrade");
  }
  headers.set("x-forwarded-host", incoming.hostname);
  headers.set("x-forwarded-proto", "https");
  const clientIP = source.headers.get("cf-connecting-ip");
  if (clientIP) {
    headers.set("x-forwarded-for", clientIP);
    headers.set("x-real-ip", clientIP);
  }

  const init = {
    method: source.method,
    headers,
    redirect: "manual",
  };
  if (source.method !== "GET" && source.method !== "HEAD") {
    init.body = source.body;
    init.duplex = "half";
  }
  return new Request(target.toString(), init);
}

function retryAfterSeconds(headers) {
  const raw = headers?.get("retry-after");
  if (!raw || !/^\d+$/.test(raw.trim())) return null;
  const seconds = Number(raw);
  return Number.isSafeInteger(seconds) && seconds > 0 ? Math.min(seconds, 86400) : null;
}

async function providerJson(response) {
  const text = await response.text();
  if (!text) return {};
  try {
    return JSON.parse(text);
  } catch {
    throw new Error("instabox_invalid_json");
  }
}

function providerObject(payload, key) {
  if (key && payload?.[key] && typeof payload[key] === "object") return payload[key];
  if (payload?.data && typeof payload.data === "object") {
    if (key && payload.data[key] && typeof payload.data[key] === "object") return payload.data[key];
    return payload.data;
  }
  return payload;
}

function readyMachine(machine, machineId) {
  return String(machine?.id || "") === machineId &&
    machine?.state === "running" &&
    machine?.runtime?.ready === true &&
    (machine?.desiredState === undefined || machine.desiredState === "running");
}

async function waitForLifecycleOperation(operationId, machineUrl, auth, machineId, retryAfter) {
  if (typeof operationId !== "string" || !operationId) {
    return { ready: false, retryAfter };
  }
  const operationUrl = "https://api.instabox.sh/v1/operations/" + encodeURIComponent(operationId);
  for (let attempt = 0; attempt < 40; attempt += 1) {
    const response = await fetch(operationUrl, { headers: auth });
    if (!response.ok) {
      return { ready: false, retryAfter: retryAfterSeconds(response.headers) ?? retryAfter };
    }
    const operation = providerObject(await providerJson(response), "operation");
    if (!["start", "resume"].includes(operation?.type)) {
      return { ready: false, retryAfter };
    }
    if (operation.state === "failed") {
      const operationRetry = Number(operation?.error?.retryAfterSeconds);
      return {
        ready: false,
        retryAfter: Number.isSafeInteger(operationRetry) && operationRetry > 0
          ? Math.min(operationRetry, 86400)
          : retryAfter,
      };
    }
    if (operation.state === "succeeded") {
      let machine = operation.machine;
      if (!machine || typeof machine !== "object") {
        const current = await fetch(machineUrl, { headers: auth });
        if (!current.ok) {
          return { ready: false, retryAfter: retryAfterSeconds(current.headers) ?? retryAfter };
        }
        machine = providerObject(await providerJson(current), "machine");
      }
      return { ready: readyMachine(machine, machineId), retryAfter: null };
    }
    if (!["queued", "running"].includes(operation.state)) {
      return { ready: false, retryAfter };
    }
    await new Promise((resolve) => setTimeout(resolve, 1000));
  }
  return { ready: false, retryAfter };
}

async function wakeStoppedMachine(env, machineUrl, auth, machineId, retryAfter) {
  const snapshotResponse = await fetch(machineUrl, { headers: auth });
  if (!snapshotResponse.ok) {
    return { ready: false, retryAfter: retryAfterSeconds(snapshotResponse.headers) ?? retryAfter };
  }
  const machine = providerObject(await providerJson(snapshotResponse), "machine");
  if (String(machine?.id || "") !== machineId) return { ready: false, retryAfter };
  if (machine.state === "running" && readyMachine(machine, machineId)) {
    return { ready: true, retryAfter: null };
  }

  // A stopped machine is started only when the provider still records the
  // desired state as running. Manual stop/suspend is operator intent and must
  // never be undone by an ordinary user request.
  if (machine.state !== "stopped" || machine.desiredState !== "running") {
    return { ready: false, retryAfter };
  }
  if (machine.operationId) {
    return waitForLifecycleOperation(machine.operationId, machineUrl, auth, machineId, retryAfter);
  }

  const start = await fetch(machineUrl + "/start", {
    method: "POST",
    headers: {
      ...auth,
      "Content-Type": "application/json",
      "Idempotency-Key": `testagram-start-${crypto.randomUUID()}`,
    },
    body: "{}",
  });
  if (!start.ok) {
    const startRetryAfter = retryAfterSeconds(start.headers) ?? retryAfter;
    if (start.status === 409) {
      const conflict = await providerJson(start).catch(() => ({}));
      const operationId = conflict?.error?.code === "operation_conflict"
        ? conflict.error.operationId
        : null;
      if (typeof operationId === "string" && operationId) {
        return waitForLifecycleOperation(operationId, machineUrl, auth, machineId, startRetryAfter);
      }
    }
    return { ready: false, retryAfter: startRetryAfter };
  }

  const started = providerObject(await providerJson(start), "operation");
  if (started?.id) {
    return waitForLifecycleOperation(String(started.id), machineUrl, auth, machineId, startRetryAfter);
  }

  // A synchronous provider response is accepted only after an independent
  // read confirms the exact machine is running and runtime-ready.
  const finalResponse = await fetch(machineUrl, { headers: auth });
  if (!finalResponse.ok) {
    return { ready: false, retryAfter: retryAfterSeconds(finalResponse.headers) ?? retryAfter };
  }
  const finalMachine = providerObject(await providerJson(finalResponse), "machine");
  return { ready: readyMachine(finalMachine, machineId), retryAfter: null };
}

async function wakeMachine(env) {
  const machineId = typeof env.MACHINE_ID === "string" ? env.MACHINE_ID.trim() : "";
  const apiKey = typeof env.INSTABOX_API_KEY === "string" ? env.INSTABOX_API_KEY : "";
  if (!machineId || !apiKey) return { ready: false, retryAfter: null };

  const api = "https://api.instabox.sh";
  const auth = { "X-API-Key": apiKey };
  const machineUrl = api + "/v1/machines/" + encodeURIComponent(machineId);

  try {
    // The dedicated wake endpoint handles an idle-suspended machine and
    // coalesces concurrent callers. It deliberately does not override a
    // manual stop. Unexpectedly stopped machines use the guarded /start path
    // below only when desiredState still says running.
    const wake = await fetch(machineUrl + "/wake", {
      method: "POST",
      headers: {
        ...auth,
        "Content-Type": "application/json",
        "Idempotency-Key": `testagram-wake-${crypto.randomUUID()}`,
      },
      body: "{}",
    });
    if (wake.ok) {
      const machine = providerObject(await providerJson(wake), "machine");
      return { ready: readyMachine(machine, machineId), retryAfter: null };
    }

    const wakeRetryAfter = retryAfterSeconds(wake.headers);
    const failure = await providerJson(wake).catch(() => ({}));
    if (wake.status === 409 && failure?.error?.code === "operation_conflict") {
      return waitForLifecycleOperation(
        failure.error.operationId, machineUrl, auth, machineId, wakeRetryAfter,
      );
    }
    if (wake.status === 409 && failure?.error?.code === "machine_not_running") {
      return wakeStoppedMachine(env, machineUrl, auth, machineId, wakeRetryAfter);
    }
    return { ready: false, retryAfter: wakeRetryAfter };
  } catch {
    return { ready: false, retryAfter: null };
  }
}
function recoveryUnavailable(error, retryAfter = null, request = null) {
  const response = json({ ok: false, error }, 503);
  const headers = new Headers(response.headers);
  const seconds = Number.isSafeInteger(retryAfter) && retryAfter > 0
    ? Math.min(retryAfter, 86400)
    : 5;
  headers.set("retry-after", String(seconds));
  headers.set("x-testagram-recovery", "attempted");
  return exposeRecoveryHeaders(new Response(response.body, { status: 503, headers }), request);
}

export default {
  async fetch(request, env) {
    if (!["GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"].includes(request.method)) {
      return new Response("Method Not Allowed", {
        status: 405,
        headers: { allow: "GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS" },
      });
    }

    const incoming = new URL(request.url);
    const variable = HOSTS[incoming.hostname.toLowerCase()];
    if (!variable) return json({ ok: false, error: "unknown_public_host" }, 421);

    const privatePath =
      (incoming.hostname === "api.testagram.site" && PRIVATE_API_PATHS.has(incoming.pathname)) ||
      (incoming.hostname === "media.testagram.site" && PRIVATE_MEDIA_PATHS.has(incoming.pathname));
    if (privatePath) {
      return new Response("Not Found", {
        status: 404,
        headers: { "cache-control": "no-store", "x-content-type-options": "nosniff" },
      });
    }

    // Browser preflights are answered at the edge. They must not wake the
    // paused machine; the real API request below is what triggers recovery.
    if (incoming.hostname.toLowerCase() === "api.testagram.site" && request.method === "OPTIONS" && request.headers.has("access-control-request-method")) {
      return corsPreflight(request);
    }

    let origin;
    try {
      origin = configuredOrigin(env[variable], incoming.hostname);
    } catch (error) {
      return json({ ok: false, error: error.message }, 503);
    }

    const target = new URL(origin.toString());
    target.pathname = incoming.pathname;
    target.search = incoming.search;
    const retryRequest = request.clone();
    const hasIdempotencyKey = Boolean(request.headers.get("idempotency-key")?.trim());
    // Match the Backend's only currently replay-safe mutation contract.
    // An arbitrary Idempotency-Key must never make another mutation replayable.
    const idempotentPostCreate =
      request.method === "POST" &&
      incoming.pathname === "/api/social/posts" &&
      hasIdempotencyKey;
    const replaySafe =
      (["GET", "HEAD", "OPTIONS"].includes(request.method) && !request.headers.get("upgrade")) ||
      idempotentPostCreate;
    let upstreamResponse;
    try {
      upstreamResponse = await fetch(makeUpstreamRequest(request, target, incoming));
      if ([502, 503, 504].includes(upstreamResponse.status)) {
        try { await upstreamResponse.body?.cancel(); } catch {}
        const wake = await wakeMachine(env);
      if (!wake.ready) return recoveryUnavailable("machine_wake_failed", wake.retryAfter, request);
      if (!replaySafe) return recoveryUnavailable("machine_awake_retry_required", null, request);
        upstreamResponse = await fetch(makeUpstreamRequest(retryRequest, target, incoming));
        if ([502, 503, 504].includes(upstreamResponse.status)) {
          try { await upstreamResponse.body?.cancel(); } catch {}
          return recoveryUnavailable("machine_waking_or_unavailable", null, request);
        }
      }
    } catch {
      const wake = await wakeMachine(env);
      if (!wake.ready) return recoveryUnavailable("machine_wake_failed", wake.retryAfter, request);
      if (!replaySafe) return recoveryUnavailable("machine_awake_retry_required", null, request);
      try {
        upstreamResponse = await fetch(makeUpstreamRequest(retryRequest, target, incoming));
      } catch {
        return recoveryUnavailable("machine_waking_or_unavailable", null, request);
      }
      if ([502, 503, 504].includes(upstreamResponse.status)) {
        try { await upstreamResponse.body?.cancel(); } catch {}
        return recoveryUnavailable("machine_waking_or_unavailable", null, request);
      }
    }

    const location = upstreamResponse.headers.get("location");
    if (location && upstreamResponse.status >= 300 && upstreamResponse.status < 400) {
      try {
        const redirect = new URL(location, origin);
        if (redirect.origin === origin.origin) {
          // Keep same-origin redirects on the canonical public hostname;
          // never send clients to an InstaBox port URL.
          redirect.protocol = incoming.protocol;
          redirect.host = incoming.host;
          const responseHeaders = new Headers(upstreamResponse.headers);
          responseHeaders.set("location", redirect.toString());
          return exposeRecoveryHeaders(new Response(upstreamResponse.body, {
            status: upstreamResponse.status,
            statusText: upstreamResponse.statusText,
            headers: responseHeaders,
          }), request);
        }
      } catch {
        // Preserve the upstream response if its Location is not a valid URL.
      }
    }
    return exposeRecoveryHeaders(upstreamResponse, request);
  },
};
