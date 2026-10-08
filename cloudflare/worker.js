const WORKER_VERSION = "2026-10-08-r2-cdn-lockdown-1";
const LEGACY_PREFIXES = ["/media/","/users/","/profiles/","/uploads/","/avatars/","/covers/","/photos/","/videos/"];

function keyFromPath(p) {
  if (p.startsWith("/v1/")) return p.slice(4);
  for (const x of LEGACY_PREFIXES) if (p.startsWith(x)) return p.slice(1);
  return null;
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
  if (!v) return;
  const m = /^bytes=(\d*)-(\d*)$/.exec(v.trim());
  if (!m) return;
  const a = m[1] === "" ? undefined : Number(m[1]);
  const b = m[2] === "" ? undefined : Number(m[2]);
  if ((a !== undefined && !Number.isSafeInteger(a)) || (b !== undefined && !Number.isSafeInteger(b))) return;
  if (a === undefined) return { suffix: b };
  if (b !== undefined && b < a) return;
  return { offset: a, length: b === undefined ? undefined : b - a + 1 };
}
function headers(extra = {}) {
  return {"x-testagram-cdn":"r2-edge","x-testagram-cdn-version":WORKER_VERSION,...extra};
}
function json(body, status = 200, extra = {}) {
  return new Response(JSON.stringify(body), {status, headers: headers({"content-type":"application/json; charset=utf-8","cache-control":"no-store",...extra})});
}
async function objectResponse(req, env, key) {
  if (!key || key.length > 512 || key.includes("\\") || key.includes("..") || key.startsWith("/")) return json({error:"Not found"},404);
  const r = range(req.headers.get("Range"));
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
  h.set("cache-control", h.get("cache-control") || cache(key));
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