const HOSTS = Object.freeze({
  "api.testagram.site": "API_ORIGIN",
  "media.testagram.site": "CDN_ORIGIN",
});
const PRIVATE_API_PATHS = new Set(["/readyz", "/metrics"]);

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

    if (incoming.hostname === "api.testagram.site" && PRIVATE_API_PATHS.has(incoming.pathname)) {
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

    const target = new URL(incoming.pathname + incoming.search, origin);
    const headers = new Headers(request.headers);
    headers.delete("host");
    headers.set("x-forwarded-host", incoming.hostname);
    headers.set("x-forwarded-proto", "https");
    const clientIP = request.headers.get("cf-connecting-ip");
    if (clientIP) headers.set("x-forwarded-for", clientIP);

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
      return await fetch(upstreamRequest);
    } catch {
      return json({ ok: false, error: "upstream_unavailable" }, 502);
    }
  },
};
