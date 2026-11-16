const HOSTS = Object.freeze({
  "api.testagram.site": "API_ORIGIN",
  "media.testagram.site": "CDN_ORIGIN",
  "testagram.site": "APEX_ORIGIN",
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

async function wakeMachine(env) {
  const machineId = typeof env.MACHINE_ID === "string" ? env.MACHINE_ID.trim() : "";
  const apiKey = typeof env.INSTABOX_API_KEY === "string" ? env.INSTABOX_API_KEY : "";
  if (!machineId || !apiKey) return false;

  const api = "https://api.instabox.sh";
  const auth = { "X-API-Key": apiKey };
  const machineUrl = api + "/v1/machines/" + encodeURIComponent(machineId);
  const readMachine = async () => {
    const response = await fetch(machineUrl, { headers: auth });
    if (!response.ok) return null;
    return response.json();
  };

  let machine;
  try {
    machine = await readMachine();
    if (!machine || machine.state === "error") return false;
    if (machine.state === "running" && machine.runtime?.ready === true) return true;

    if (machine.state !== "running") {
      const start = await fetch(machineUrl + "/start", {
        method: "POST",
        headers: {
          ...auth,
          "Content-Type": "application/json",
          // Concurrent real user requests in the same minute share one start
          // operation instead of creating a start storm.
          "Idempotency-Key": "testagram-edge-wake-" + machineId + "-" + Math.floor(Date.now() / 60000),
        },
        body: "{}",
      });
      if (!start.ok) return false;
      const operation = await start.json();
      const operationId = operation.id;
      if (operationId) {
        let operationSucceeded = false;
        for (let attempt = 0; attempt < 8; attempt += 1) {
          const status = await fetch(api + "/v1/operations/" + encodeURIComponent(operationId), {
            headers: auth,
          });
          if (!status.ok) return false;
          const current = await status.json();
          if (current.state === "succeeded") {
            operationSucceeded = true;
            break;
          }
          if (current.state === "failed") return false;
          await new Promise((resolve) => setTimeout(resolve, 1000));
        }
        if (!operationSucceeded) return false;
      }
    }

    for (let attempt = 0; attempt < 8; attempt += 1) {
      machine = await readMachine();
      if (machine?.state === "running" && machine?.runtime?.ready === true) return true;
      if (machine?.state === "error") return false;
      await new Promise((resolve) => setTimeout(resolve, 1000));
    }
  } catch {
    return false;
  }
  return false;
}

function recoveryUnavailable(error) {
  const response = json({ ok: false, error }, 503);
  const headers = new Headers(response.headers);
  headers.set("retry-after", "5");
  headers.set("x-testagram-recovery", "attempted");
  return new Response(response.body, { status: 503, headers });
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
    const replaySafe =
      (["GET", "HEAD", "OPTIONS"].includes(request.method) && !request.headers.get("upgrade")) ||
      Boolean(request.headers.get("idempotency-key"));
    let upstreamResponse;
    try {
      upstreamResponse = await fetch(makeUpstreamRequest(request, target, incoming));
      if ([502, 503, 504].includes(upstreamResponse.status)) {
        try { await upstreamResponse.body?.cancel(); } catch {}
        if (!await wakeMachine(env)) return recoveryUnavailable("machine_wake_failed");
        if (!replaySafe) return recoveryUnavailable("machine_awake_retry_required");
        upstreamResponse = await fetch(makeUpstreamRequest(retryRequest, target, incoming));
        if ([502, 503, 504].includes(upstreamResponse.status)) {
          try { await upstreamResponse.body?.cancel(); } catch {}
          return recoveryUnavailable("machine_waking_or_unavailable");
        }
      }
    } catch {
      if (!await wakeMachine(env)) return recoveryUnavailable("machine_wake_failed");
      if (!replaySafe) return recoveryUnavailable("machine_awake_retry_required");
      try {
        upstreamResponse = await fetch(makeUpstreamRequest(retryRequest, target, incoming));
      } catch {
        return recoveryUnavailable("machine_waking_or_unavailable");
      }
      if ([502, 503, 504].includes(upstreamResponse.status)) {
        try { await upstreamResponse.body?.cancel(); } catch {}
        return recoveryUnavailable("machine_waking_or_unavailable");
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
          return new Response(upstreamResponse.body, {
            status: upstreamResponse.status,
            statusText: upstreamResponse.statusText,
            headers: responseHeaders,
          });
        }
      } catch {
        // Preserve the upstream response if its Location is not a valid URL.
      }
    }
    return upstreamResponse;
  },
};
