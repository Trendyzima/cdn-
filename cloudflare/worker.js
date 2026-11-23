const HOSTS = Object.freeze({
  "api.testagram.site": "API_ORIGIN",
  "media.testagram.site": "CDN_ORIGIN",
  "testagram.site": "APEX_ORIGIN",
  "www.testagram.site": "APEX_ORIGIN",
});
const PRIVATE_API_PATHS = new Set(["/readyz", "/metrics", "/internal/edge/recovery-ready"]);
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
    // This response-identity header is server-owned; never accept a client
    // copy that could suppress recovery when the provider origin is down.
    "x-testagram-service",
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

async function waitForMachineReady(machineUrl, auth, machineId, retryAfter) {
  for (let attempt = 0; attempt < 20; attempt += 1) {
    const response = await fetch(machineUrl, { headers: auth });
    if (!response.ok) {
      return { ready: false, retryAfter: retryAfterSeconds(response.headers) ?? retryAfter };
    }
    const machine = providerObject(await providerJson(response), "machine");
    if (String(machine?.id || "") !== machineId) return { ready: false, retryAfter };
    if (readyMachine(machine, machineId)) return { ready: true, retryAfter: null };
    // A provider-side manual stop is operator intent. Do not reverse it.
    if (machine?.desiredState === "stopped") return { ready: false, retryAfter };
    if (attempt < 19) await new Promise((resolve) => setTimeout(resolve, 1000));
  }
  return { ready: false, retryAfter: retryAfter ?? 5 };
}

async function verifyReadyMachine(candidate, machineUrl, auth, machineId, retryAfter) {
  if (candidate?.id && String(candidate.id) !== machineId) {
    return { ready: false, retryAfter };
  }
  if (readyMachine(candidate, machineId)) return { ready: true, retryAfter: null };
  // An accepted wake or completed lifecycle operation can precede the final
  // runtime-ready snapshot. Re-read the exact machine instead of treating
  // provider acceptance as readiness.
  return waitForMachineReady(machineUrl, auth, machineId, retryAfter);
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
        retireEpoch: true,
      };
    }
    if (operation.state === "succeeded") {
      return verifyReadyMachine(operation.machine, machineUrl, auth, machineId, retryAfter);
    }
    if (!["queued", "running"].includes(operation.state)) {
      return { ready: false, retryAfter };
    }
    await new Promise((resolve) => setTimeout(resolve, 1000));
  }
  return { ready: false, retryAfter };
}

// Each inbound recovery attempt holds a durable lease. Successful completion
// marks the epoch ready, but keys are not rotated while another request still
// holds a lease. Expired leases are pruned after five minutes.
async function acquireLifecycleRecovery(env, machineId) {
  const namespace = env.RECOVERY_COORDINATOR;
  if (!namespace || typeof namespace.idFromName !== "function" || typeof namespace.get !== "function") {
    throw new Error("recovery_coordinator_unavailable");
  }
  const stub = namespace.get(namespace.idFromName(machineId));
  const response = await stub.fetch("https://recovery-coordinator/acquire", { method: "POST" });
  if (!response.ok) throw new Error("recovery_coordinator_acquire_failed");
  const body = await response.json();
  if (typeof body?.leaseId !== "string" || !body.leaseId) throw new Error("recovery_coordinator_invalid_lease");
  return body.leaseId;
}

async function lifecycleIdempotencyKey(env, machineId, action, leaseId) {
  const namespace = env.RECOVERY_COORDINATOR;
  if (!namespace || typeof namespace.idFromName !== "function" || typeof namespace.get !== "function") {
    throw new Error("recovery_coordinator_unavailable");
  }
  const stub = namespace.get(namespace.idFromName(machineId));
  const response = await stub.fetch("https://recovery-coordinator/key", {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ action, leaseId }),
  });
  if (!response.ok) throw new Error("recovery_coordinator_key_failed");
  const body = await response.json();
  if (typeof body?.key !== "string" || !body.key) throw new Error("recovery_coordinator_invalid_key");
  return body.key;
}

async function completeLifecycleRecovery(env, machineId, leaseId, retireEpoch) {
  const namespace = env.RECOVERY_COORDINATOR;
  if (!namespace || typeof namespace.idFromName !== "function" || typeof namespace.get !== "function") {
    throw new Error("recovery_coordinator_unavailable");
  }
  const stub = namespace.get(namespace.idFromName(machineId));
  const response = await stub.fetch("https://recovery-coordinator/release", {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ leaseId, retireEpoch: retireEpoch === true }),
  });
  if (!response.ok) throw new Error("recovery_coordinator_release_failed");
}

async function wakeStoppedMachine(env, machineUrl, auth, machineId, retryAfter, leaseId) {
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

  const startKey = await lifecycleIdempotencyKey(env, machineId, "start", leaseId);
  const start = await fetch(machineUrl + "/start", {
    method: "POST",
    headers: {
      ...auth,
      "Content-Type": "application/json",
      "Idempotency-Key": startKey,
    },
    body: "{}",
  });
  const startRetryAfter = retryAfterSeconds(start.headers) ?? retryAfter;
  if (!start.ok) {
    if (start.status === 409) {
      const conflict = await providerJson(start).catch(() => ({}));
      const operationId = conflict?.error?.code === "operation_conflict"
        ? conflict.error.operationId
        : null;
      if (typeof operationId === "string" && operationId) {
        return waitForLifecycleOperation(operationId, machineUrl, auth, machineId, startRetryAfter);
      }
    }
    return {
      ready: false,
      retryAfter: startRetryAfter,
      retireEpoch: [400, 401, 403, 404, 422].includes(start.status),
    };
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

async function waitForApplicationReadiness(env, retryAfter = null) {
  const rawOrigin = typeof env.API_ORIGIN === "string" ? env.API_ORIGIN : "";
  const token = typeof env.EDGE_RECOVERY_READINESS_TOKEN === "string"
    ? env.EDGE_RECOVERY_READINESS_TOKEN
    : "";
  if (!rawOrigin || !/^[A-Fa-f0-9]{64}$/.test(token)) return { ready: false, retryAfter };

  let readinessUrl;
  try {
    const origin = configuredOrigin(rawOrigin, "api.testagram.site");
    readinessUrl = new URL("/internal/edge/recovery-ready", origin);
  } catch {
    return { ready: false, retryAfter };
  }

  for (let attempt = 0; attempt < 20; attempt += 1) {
    try {
      const response = await fetch(readinessUrl.toString(), {
        method: "GET",
        headers: {
          "Authorization": `Bearer ${token}`,
          "Accept": "application/json",
          "Cache-Control": "no-store",
        },
      });
      if (response.status === 200) {
        const body = await providerJson(response);
        if (body?.ok === true && body?.ready === true && body?.service === "testagram-api") {
          return { ready: true, retryAfter: null };
        }
        return { ready: false, retryAfter };
      }
      // A missing/invalid edge capability is a configuration fault, not a
      // reason to keep polling or to replay the user's original request.
      if (response.status === 404 || response.status === 401 || response.status === 403) {
        return { ready: false, retryAfter };
      }
    } catch {
      // The API process may still be starting; keep the bounded probe alive.
    }
    if (attempt < 19) await new Promise((resolve) => setTimeout(resolve, 1000));
  }
  return { ready: false, retryAfter: retryAfter ?? 5 };
}

function loginRetryReady(request) {
  const response = json({ ok: false, error: "application_ready_retry_required" }, 503);
  const headers = new Headers(response.headers);
  headers.set("retry-after", "1");
  headers.set("x-testagram-recovery", "ready");
  return exposeRecoveryHeaders(new Response(response.body, { status: 503, headers }), request);
}

async function wakeMachine(env) {
  const machineId = typeof env.MACHINE_ID === "string" ? env.MACHINE_ID.trim() : "";
  const apiKey = typeof env.INSTABOX_API_KEY === "string" ? env.INSTABOX_API_KEY : "";
  if (!machineId || !apiKey) return { ready: false, retryAfter: null };

  const api = "https://api.instabox.sh";
  const auth = { "X-API-Key": apiKey };
  const machineUrl = api + "/v1/machines/" + encodeURIComponent(machineId);
  let leaseId = null;

  try {
    leaseId = await acquireLifecycleRecovery(env, machineId);
    // The dedicated wake endpoint handles idle suspension without overriding
    // a manual stop. Unexpected stopped state uses the guarded /start path.
    const wakeKey = await lifecycleIdempotencyKey(env, machineId, "wake", leaseId);
    const wake = await fetch(machineUrl + "/wake", {
      method: "POST",
      headers: { ...auth, "Content-Type": "application/json", "Idempotency-Key": wakeKey },
      body: "{}",
    });
    if (wake.ok) {
      const payload = await providerJson(wake);
      const operation = providerObject(payload, "operation");
      if (typeof operation?.id === "string" &&
          ["start", "resume"].includes(operation?.type) &&
          ["queued", "running", "succeeded"].includes(operation?.state)) {
        return { ...(await waitForLifecycleOperation(operation.id, machineUrl, auth, machineId, null)), leaseId };
      }
      const machine = providerObject(payload, "machine");
      return { ...(await verifyReadyMachine(machine, machineUrl, auth, machineId, null)), leaseId };
    }

    const wakeRetryAfter = retryAfterSeconds(wake.headers);
    const failure = await providerJson(wake).catch(() => ({}));
    if (wake.status === 409 && failure?.error?.code === "operation_conflict") {
      return {
        ...(await waitForLifecycleOperation(failure.error.operationId, machineUrl, auth, machineId, wakeRetryAfter)),
        leaseId,
      };
    }
    if (wake.status === 409 && failure?.error?.code === "machine_not_running") {
      return { ...(await wakeStoppedMachine(env, machineUrl, auth, machineId, wakeRetryAfter, leaseId)), leaseId };
    }
    return {
      ready: false,
      retryAfter: wakeRetryAfter,
      leaseId,
      retireEpoch: wake.status >= 400 && wake.status < 500 && ![408, 409, 425, 429].includes(wake.status),
    };
  } catch {
    if (leaseId) await completeLifecycleRecovery(env, machineId, leaseId, false).catch(() => {});
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
    // Clone only requests whose explicit replay contract permits retry. In
    // particular, never create a second body stream for login credentials.
    const retryRequest = replaySafe ? request.clone() : null;
    let upstreamResponse;
    let originTransportFailed = false;
    try {
      upstreamResponse = await fetch(makeUpstreamRequest(request, target, incoming));
    } catch {
      // A transport exception is ambiguous: the origin may have processed a
      // mutation before the connection failed. Recovery may wake the provider,
      // but only the explicit replay-safe contract permits replay.
      originTransportFailed = true;
    }

    const originGatewayFailure =
      !originTransportFailed &&
      [502, 503, 504].includes(upstreamResponse.status) &&
      upstreamResponse.headers.get("x-testagram-service") !== "testagram-api";

    if (originTransportFailed || originGatewayFailure) {
      if (upstreamResponse) {
        try { await upstreamResponse.body?.cancel(); } catch {}
      }
      const provider = await wakeMachine(env);
      if (!provider.ready) {
        if (provider.leaseId) {
          await completeLifecycleRecovery(env, String(env.MACHINE_ID || "").trim(), provider.leaseId, provider.retireEpoch === true).catch(() => {});
        }
        return recoveryUnavailable("machine_wake_failed", provider.retryAfter, request);
      }

      // Provider runtime readiness is not application readiness. Wait for the
      // separately authenticated Backend capability, which checks PostgreSQL.
      const application = await waitForApplicationReadiness(env, provider.retryAfter);
      if (!application.ready) {
        if (provider.leaseId) {
          await completeLifecycleRecovery(env, String(env.MACHINE_ID || "").trim(), provider.leaseId, false).catch(() => {});
        }
        return recoveryUnavailable("application_not_ready", application.retryAfter, request);
      }

      try {
        await completeLifecycleRecovery(env, String(env.MACHINE_ID || "").trim(), provider.leaseId, true);
      } catch {
        return recoveryUnavailable("recovery_coordination_unavailable", null, request);
      }

      if (!replaySafe) {
        if (request.method === "POST" && incoming.pathname === "/api/auth/login") {
          return loginRetryReady(request);
        }
        return recoveryUnavailable("machine_awake_retry_required", null, request);
      }

      try {
        upstreamResponse = await fetch(makeUpstreamRequest(retryRequest, target, incoming));
      } catch {
        // Do not re-enter the lifecycle recovery branch: one request may start
        // or follow only one provider operation.
        return recoveryUnavailable("machine_waking_or_unavailable", null, request);
      }

      const retryIsGatewayFailure =
        [502, 503, 504].includes(upstreamResponse.status) &&
        upstreamResponse.headers.get("x-testagram-service") !== "testagram-api";
      if (retryIsGatewayFailure) {
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


// One Durable Object per exact provider machine serializes recovery epochs and
// persists lifecycle idempotency keys across independent Worker invocations.
// Keys are retired only after provider runtime and private application readiness
// have both been independently confirmed.
export class RecoveryCoordinator {
  constructor(ctx) {
    this.ctx = ctx;
  }

  async fetch(request) {
    const url = new URL(request.url);
    const now = Date.now();
    const leaseTtlMs = 5 * 60 * 1000;

    if (request.method === "POST" && url.pathname === "/acquire") {
      const leaseId = crypto.randomUUID();
      const epoch = await this.ctx.storage.transaction(async (txn) => {
        let state = await txn.get("recovery-epoch");
        if (!state) state = { epoch: crypto.randomUUID(), keys: {}, leases: {}, retireWhenIdle: false };
        state.leases ||= {};
        for (const [id, expiry] of Object.entries(state.leases)) {
          if (!Number.isFinite(expiry) || expiry <= now) delete state.leases[id];
        }
        if (state.retireWhenIdle && Object.keys(state.leases).length === 0) {
          state = { epoch: crypto.randomUUID(), keys: {}, leases: {}, retireWhenIdle: false };
        }
        state.leases[leaseId] = now + leaseTtlMs;
        await txn.put("recovery-epoch", state);
        return state.epoch;
      });
      return json({ ok: true, leaseId, epoch });
    }

    if (request.method === "POST" && url.pathname === "/key") {
      let body;
      try { body = await request.json(); } catch { return json({ ok: false }, 400); }
      const action = body?.action;
      if ((action !== "wake" && action !== "start") || typeof body?.leaseId !== "string") {
        return json({ ok: false }, 400);
      }
      const key = await this.ctx.storage.transaction(async (txn) => {
        const state = await txn.get("recovery-epoch");
        if (!state?.leases?.[body.leaseId]) return null;
        state.leases[body.leaseId] = now + leaseTtlMs;
        if (!state.keys[action]) {
          state.keys[action] = `testagram-${action}-${state.epoch}-${crypto.randomUUID()}`;
        }
        await txn.put("recovery-epoch", state);
        return state.keys[action];
      });
      if (!key) return json({ ok: false }, 409);
      return json({ ok: true, key });
    }

    if (request.method === "POST" && url.pathname === "/release") {
      let body;
      try { body = await request.json(); } catch { return json({ ok: false }, 400); }
      if (typeof body?.leaseId !== "string") return json({ ok: false }, 400);
      const released = await this.ctx.storage.transaction(async (txn) => {
        const state = await txn.get("recovery-epoch");
        if (!state?.leases?.[body.leaseId]) return false;
        delete state.leases[body.leaseId];
        if (body.retireEpoch === true) state.retireWhenIdle = true;
        await txn.put("recovery-epoch", state);
        return true;
      });
      if (!released) return json({ ok: false }, 409);
      return json({ ok: true });
    }

    return new Response("Not Found", { status: 404, headers: { "cache-control": "no-store" } });
  }
}
