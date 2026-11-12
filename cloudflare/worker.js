const WORKER_VERSION = "2026-11-12-r2-backend-assertion-v1";
const LEGACY_PREFIXES = ["/media/","/users/","/profiles/","/uploads/","/avatars/","/covers/","/photos/","/videos/"];

function keyFromPath(p) {
  let raw = null;
  if (p.startsWith("/v1/")) raw = p.slice(4);
  else for (const x of LEGACY_PREFIXES) if (p.startsWith(x)) { raw = p.slice(1); break; }
  if (!raw || raw.length > 512) return null;
  const parts = raw.split("/");
  if (parts.length < 3) return null;
  const decoded = [];
  for (const part of parts) {
    let value;
    try { value = decodeURIComponent(part); } catch { return null; }
    if (!value || value === "." || value === ".." || value.includes("/") || value.includes("\\") || value.includes("%")) return null;
    if (!/^[A-Za-z0-9._-]+$/.test(value)) return null;
    decoded.push(value);
  }
  return decoded.join("/");
}
async function authorizedPrivate(key, authorization, secret) {
  if (typeof secret !== "string" || new TextEncoder().encode(secret).length < 32) return {ok:false,private:true};
  if (typeof authorization !== "string" || !authorization.startsWith("Bearer ") || authorization.length > 8192) return {ok:false,private:true};
  const token = authorization.slice(7).trim();
  const parts = token.split(".");
  if (parts.length !== 3 || parts[0] !== "v1") return {ok:false,private:true};
  let signature, payload;
  try {
    const decode = value => {
      const raw = atob(value.replace(/-/g,"+").replace(/_/g,"/") + "=".repeat((4-value.length%4)%4));
      return Uint8Array.from(raw, ch => ch.charCodeAt(0));
    };
    signature = decode(parts[2]);
    payload = JSON.parse(new TextDecoder().decode(decode(parts[1])));
  } catch { return {ok:false,private:true}; }
  if (signature.length !== 32) return {ok:false,private:true};
  const keyData = await crypto.subtle.importKey("raw",new TextEncoder().encode(secret),{name:"HMAC",hash:"SHA-256"},false,["verify"]);
  let valid = false;
  try { valid = await crypto.subtle.verify("HMAC",keyData,signature,new TextEncoder().encode("v1."+parts[1])); } catch {}
  if (!valid) return {ok:false,private:true};
  const now = Math.floor(Date.now()/1000);
  const ownerMatch = /^users\/([A-Za-z0-9._-]{1,128})\/[a-f0-9]{32}$/.exec(key);
  const claimsValid = payload && payload.v === "v1" && payload.iss === "testagram-backend" &&
    payload.aud === "testagram-cdn" && payload.visibility === "private" &&
    typeof payload.sub === "string" && ownerMatch && ownerMatch[1] === payload.sub &&
    payload.key === key && Number.isSafeInteger(payload.iat) && Number.isSafeInteger(payload.exp) &&
    payload.iat > 0 && payload.iat <= now + 30 && payload.exp > now &&
    payload.exp > payload.iat && payload.exp - payload.iat <= 300 &&
    typeof payload.jti === "string" && /^[a-f0-9]{32}$/.test(payload.jti);
  return {ok:!!claimsValid,private:true};
}
function type(k) {
  const e = k.toLowerCase().split(".").pop();
  return ({webp:"image/webp",jpg:"image/jpeg",jpeg:"image/jpeg",png:"image/png",gif:"image/gif",avif:"image/avif",svg:"image/svg+xml",mp4:"video/mp4",webm:"video/webm",m3u8:"application/vnd.apple.mpegurl",ts:"video/mp2t",m4s:"video/iso.segment",mpd:"application/dash+xml"})[e] || "application/octet-stream";
}
function cache(k) {
  const l = k.toLowerCase();
  if (l.endsWith(".m3u8") || l.endsWith(".mpd")) return "public, max-age=2, s-maxage=2, stale-while-revalidate=5";
  if (/\.(jpg|jpeg|png|webp|avif|gif|svg)$/.test(l)) return "public, max-age=86400, s-maxage=31536000, immutable";
  if (/\.(mp4|webm|ts|m4s)$/.test(l)) return "public, max-age=86400, s-maxage=86400, stale-while-revalidate=60";
  return "public, max-age=300, s-maxage=300, stale-while-revalidate=60";
}
function range(v) {
  if (!v) return { kind: "none" };
  const raw = v.trim();
  if (!/^bytes=/.test(raw)) return { kind: "invalid" };
  const spec = raw.slice(6).trim();
  if (!spec || spec.includes(",")) return { kind: "invalid" };
  const m = /^(\d*)-(\d*)$/.exec(spec);
  if (!m || (m[1] === "" && m[2] === "")) return { kind: "invalid" };
  const a = m[1] === "" ? undefined : Number(m[1]);
  const b = m[2] === "" ? undefined : Number(m[2]);
  if ((a !== undefined && !Number.isSafeInteger(a)) || (b !== undefined && !Number.isSafeInteger(b))) return { kind: "invalid" };
  if (a === undefined) {
    if (b === 0) return { kind: "invalid" };
    return { kind: "range", value: { suffix: b } };
  }
  if (b !== undefined && b < a) return { kind: "invalid" };
  return { kind: "range", value: { offset: a, length: b === undefined ? undefined : b - a + 1 } };
}
function headers(extra = {}) {
  return {"x-testagram-cdn":"r2-edge","x-testagram-cdn-version":WORKER_VERSION,...extra};
}
function json(body, status = 200, extra = {}) {
  return new Response(JSON.stringify(body), {status, headers: headers({"content-type":"application/json; charset=utf-8","cache-control":"no-store",...extra})});
}
async function objectResponse(req, env, key) {
  if (!key || key.length > 512 || key.includes("\\") || key.includes("..") || key.startsWith("/")) return json({error:"Not found"},404);
  const segments = key.split("/");
  if (segments.length < 3 || !["users","profiles","uploads","avatars","covers","photos","videos","media"].includes(segments[0])) return json({error:"Not found"},404);
  const url = new URL(req.url);
  if (url.searchParams.has("token")) return json({error:"Query-string media tokens are not supported"},400);
  const auth = await authorizedPrivate(key, req.headers.get("Authorization") || "", env.CDN_MEDIA_ASSERTION_SECRET || "");
  if (!auth.ok) return json({error:"Unauthorized"},401,{"cache-control":"no-store"});
  const parsedRange = range(req.headers.get("Range"));
  if (parsedRange.kind === "invalid") return json({error:"Range not satisfiable"},416,{"content-range":"bytes */0"});
  const r = parsedRange.kind === "range" ? parsedRange.value : undefined;
  let o;
  try {
    o = await env.MEDIA_BUCKET.get(key, r ? {range:r} : undefined);
  } catch {
    return json({error:"Media storage unavailable"},503);
  }
  if (!o) return json({error:"Not found"},404);
  const h = new Headers();
  o.writeHttpMetadata(h);
  h.set("content-type", h.get("content-type") || type(key));
  h.set("cache-control", auth.private ? "private, no-store" : (h.get("cache-control") || cache(key)));
  h.set("etag", o.httpEtag);
  h.set("x-testagram-cdn","r2-edge");
  h.set("x-testagram-cdn-version",WORKER_VERSION);
  h.set("x-cache","MISS");
  h.set("accept-ranges","bytes");
  let status = 200;
  if (o.range) {
    const off = o.range.offset ?? 0, len = o.range.length ?? o.size;
    h.set("content-range", `bytes ${off}-${off+len-1}/${o.size}`);
    h.set("content-length", String(len));
    status = 206;
  } else h.set("content-length", String(o.size));
  return new Response(req.method === "HEAD" ? null : o.body, {status,headers:h});
}
export default {
  async fetch(req, env) {
    const u = new URL(req.url);
    if (req.method !== "GET" && req.method !== "HEAD") return new Response("Method Not Allowed",{status:405,headers:{allow:"GET, HEAD"}});
    if (u.pathname === "/healthz") return json({ok:true,service:"testagram-cdn",storage:"r2",version:WORKER_VERSION});
    if (u.pathname === "/readyz") {
      try {
        await env.MEDIA_BUCKET.list({limit:1});
        return json({ok:true,service:"testagram-cdn",storage:"r2",version:WORKER_VERSION});
      } catch {
        return json({ok:false,service:"testagram-cdn",storage:"r2",version:WORKER_VERSION},503);
      }
    }
    const key = keyFromPath(u.pathname);
    if (!key) return json({error:"Not found"},404);
    return objectResponse(req,env,key);
  }
};