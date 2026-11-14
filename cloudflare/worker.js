const HOSTS = Object.freeze({
  "api.testagram.site": "API_ORIGIN",
  "media.testagram.site": "CDN_ORIGIN",
});
const PRIVATE_API_PATHS = new Set(["/readyz", "/metrics"]);
const PRIVATE_MEDIA_PATHS = new Set(["/metrics"]);

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
    const headers = new Headers(request.headers);
    const isWebSocket = request.headers.get("upgrade")?.toLowerCase() === "websocket";

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
    const clientIP = request.headers.get("cf-connecting-ip");
    if (clientIP) {
      headers.set("x-forwarded-for", clientIP);
      headers.set("x-real-ip", clientIP);
    }

    const init = {
      method: request.method,
      headers,
      redirect: "manual",
    };
    if (request.method !== "GET" && request.method !== "HEAD") {
      init.body = request.body;
      init.duplex = "half";
    }

    try {
      const upstreamRequest = new Request(target.toString(), init);
      const upstreamResponse = await fetch(upstreamRequest);
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
    } catch {
      return json({ ok: false, error: "upstream_unavailable" }, 502);
    }
  },
};
