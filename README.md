# Testagram Edge CDN

A self-hosted, Bunny/Cloudflare-style data-plane CDN for live HLS/DASH, built as a normal Go HTTP service. GitHub is the source/build/automation layer; it is never the video transport layer.

## 100k-viewer capacity target

The repository now has the core capacity architecture needed to pursue a 100,000 logical-viewer target without putting video requests through serverless/edge functions:

Viewer -> deterministic edge selection -> regional edge -> hot cache -> shield -> origin

Optional:

Viewer <-> P2P peers
        -> HTTP edge fallback

This is an engineering target, not an unmeasured capacity claim. Actual capacity is constrained by edge NIC bandwidth, upstream bandwidth, concurrent sockets, cache-hit ratio, segment bitrate/duration, CPU, memory and network path quality.

For example, 100,000 viewers at an average 2 Mbps media bitrate represents about 200 Gbit/s of aggregate viewer egress. That cannot be supplied by one ordinary server. The target therefore requires multiple edge nodes/regions and measured bandwidth capacity.

## Capacity work implemented

- high-concurrency Go HTTP transport with configurable idle/host connection pools
- request coalescing so one cache miss does not fan out to the origin
- multi-origin failover
- cache-shield tier via SHIELD_URLS
- stale-if-error cache retention so a recent segment/playlist can survive a short upstream outage
- deterministic /route?key=... edge selection over configured EDGE_URLS
- graceful shutdown/draining
- bounded rate-limiter state
- atomic cache writes and byte-bounded LRU eviction
- HLS-aware TTL and HTTP cache-control
- opt-in 100,000 logical-viewer fan-out test (CAPACITY_TEST=1)
- Prometheus-style counters for cache, upstream, inflight, stale and rejection behavior
- IPTV prefetch observability: cache hits/misses, warmed segment count, warmed seconds, failures, and counters on `/healthz` and `/metrics`

Live IPTV delivery is tuned for low CPU and low buffering: HLS playlists use short cache lifetimes, playlists are gzip-compressed only when large enough to benefit, compression uses the fastest gzip level, and live responses advertise `X-Accel-Buffering: no` to avoid reverse-proxy buffering.

The HTTP stale controls follow the model defined by RFC 5861: stale content can be used during controlled revalidation or when an upstream error occurs. RFC 8216 also requires live media segments referenced by a playlist to remain available long enough to avoid interrupting in-progress playback, so cache retention must not be shorter than the playback safety window.

## Architecture

### Data plane

1. Viewer obtains an edge URL from /route or an external DNS/LB layer.
2. Edge checks its local ephemeral cache.
3. Concurrent misses for the same object are coalesced.
4. Edge asks SHIELD_URLS first when configured.
5. Shield asks its cache/origins.
6. Origin is contacted only after cache/shield misses.
7. A bounded stale object can be served when upstream is unavailable.

The shield tier is important at large scale: regional edges should not all independently hammer the origin for the same live segment.

### Multi-edge routing

Set EDGE_URLS=https://edge-ke.example,https://edge-eu.example,https://edge-us.example

Then call GET /route?key=<stable-user-or-session-key>.

The response is deterministic for the same key. This keeps the data plane stateless. For production, place this behind authoritative DNS, a load balancer, or an application-level bootstrap endpoint that can remove unhealthy edges. The /route endpoint is a routing primitive, not a substitute for global anycast/DNS health management.

### Shield

Set SHIELD_URLS=https://shield-1.example,https://shield-2.example.

Edges try shields before origins. A shield is another instance of this same service with a larger cache and direct access to the origin. Do not point a shield back at an edge in a cycle.

### HLS cache policy

The CDN uses a two-tier local cache: a larger disk-backed cache for breadth and a bounded RAM hot tier for the hottest IPTV/HLS objects. RAM is never allowed to grow with traffic; once the hot tier reaches its configured ceiling it evicts least-recently-used objects. This keeps repeated live segment requests off disk while preserving a large persistent edge cache.

Defaults:

- .m3u8: internal TTL 2 seconds
- media segments: internal TTL 30 seconds
- stale-if-error: 60 seconds
- object limit: 16 MiB
- cache capacity: 64 GiB by default (disk-backed, configurable)
- hot memory cache: 48 GiB by default for hottest live objects, configurable; it is bounded and allocated only as objects are admitted

Manifests are intentionally short-lived because live playlists change frequently. Segments are retained longer because their URLs normally identify immutable media once published. The exact TTL must be tuned to the stream's target duration and playlist window.

### P2P

P2P remains an acceleration layer, never the source of truth. HTTP CDN delivery is authoritative fallback. A production P2P deployment still needs controlled tracker/signalling and must be load-tested separately.

## Environment

| Variable | Default | Purpose |
|---|---:|---|
| LISTEN_ADDR | :8080 | HTTP listener |
| ORIGIN_URL | empty | primary origin |
| ORIGIN_URLS | empty | ordered origin list |
| SHIELD_URLS | empty | upstream cache-shield URLs |
| EDGE_URLS | empty | deterministic edge-routing pool |
| ORIGIN_AUTH_TOKEN | empty | upstream bearer token |
| PURGE_TOKEN | empty | purge API authorization |
| CACHE_DIR | ./cache | cache directory |
| MAX_CACHE_BYTES | 64 GiB | disk-backed cache capacity |
| HOT_CACHE_BYTES | 48 GiB | bounded RAM hot-cache tier; raise only when node RAM can safely support it |
| SEGMENT_TTL | 30s | segment cache TTL |
| MANIFEST_TTL | 2s | playlist cache TTL |
| STALE_IF_ERROR | 60s | stale retention after expiry |
| MAX_SEGMENT_BYTES | 32 MiB | object size limit |
| ORIGIN_TIMEOUT | 10s | upstream timeout |
| MAX_IDLE_CONNS | 2048 | global upstream idle pool |
| MAX_IDLE_CONNS_PER_HOST | 256 | per-upstream idle pool |
| MAX_CONNS_PER_HOST | 512 | per-upstream connection cap |
| RATE_LIMIT_PER_MIN | 1200 | per-IP rate |
| RATE_LIMIT_BURST | 300 | per-IP burst |
| ALLOWED_ORIGINS | empty | optional CORS allow-list |
| NODE_ID | local | edge identity |
| TV_PREFETCH_SECONDS | 30s | advertised live media window to warm before playback; reduce upstream work and startup delay |
| TV_PREFETCH_CONCURRENCY | 4 | bounded concurrent IPTV segment warmers |
| TV_PREFETCH_TIMEOUT | 12s | hard deadline for each rolling warmup pass |
| PUBLIC_BASE_URL | empty | canonical public media origin, e.g. https://media.testagram.site |
| MEDIA_URL_PREFIX | /v1 | stable public media path prefix |
| CLOUDINARY_CLOUD_NAME | empty | Cloudinary cloud used for media-origin fallback |
| CLOUDINARY_IMAGE_BASE_URL | empty | explicit Cloudinary image delivery origin |
| CLOUDINARY_VIDEO_BASE_URL | empty | explicit Cloudinary video/HLS delivery origin |
- `R2_PUBLIC_BASE_URL` — optional Cloudflare R2 public media origin; used after Cloudinary and before legacy Testagram origins.
| UPSTASH_REDIS_REST_URL | empty | optional Upstash Redis REST endpoint for control-plane coordination |
| UPSTASH_REDIS_REST_TOKEN | empty | server-only Upstash Redis token |
| UPSTASH_LOCK_TTL | 8s | distributed coordination lease |
| TRUST_CLOUDFLARE | 0 | use CF-Connecting-IP for rate limiting when behind Cloudflare |

## Testagram provider compatibility

This repository is the canonical Go media-delivery plane for Testagram.
`media.testagram.site` routes to the Go CDN, which handles public aliases,
private viewer-authorized streaming, HLS/DASH byte delivery, range requests,
cache, shields and configured trusted origins.

Cloudflare provides authoritative DNS and private R2 object storage. The
Cloudflare R2 Worker under `cloudflare/` is currently deployed only as an
unrouted script (`workers_dev = false`); it has no custom-domain route and
must not intercept `media.testagram.site`. A separate hostname and reviewed
Backend/API contract are required before Worker activation.

Backend is the authority for identity, media ownership, visibility and
viewer authorization. Appwrite is used for identity/session verification;
there is no Supabase runtime identity dependency. Data Centre owns storage
placement, replication, repair and deletion. The CDN is not a product API or
authorization authority.

Cloudinary and explicitly public R2 delivery origins may be used only for
objects under the canonical `public/` namespace. Private MinIO/R2/B2 objects
must be reached through the authenticated Backend origin gateway; the current
Go service is not an S3 Signature V4 client. Upstash Redis is optional
control-plane coordination only, never a binary media store.

Recommended active flow:

Viewer -> Go Testagram CDN -> public cache or authenticated Backend origin
Backend -> Data Centre -> private object providers (Instabox/MinIO, R2, B2)

The Go CDN's private path forwards a Backend-issued viewer assertion and a
separate CDN-to-origin service credential. Private responses bypass cache and
are always `private, no-store`.

## XClone / Testagram media compatibility

The Go CDN exposes the canonical media delivery contract. Backend owns the
stable URL and object key; the CDN does not mint media URLs from caller-supplied
paths or visibility flags. The legacy `GET /v1/media/url` helper intentionally
returns HTTP 410.

Supported compatibility aliases include `/media/*`, `/users/*`,
`/profiles/*`, `/uploads/*`, `/avatars/*`, `/covers/*`, `/photos/*`,
and `/videos/*`. The canonical API path is `/v1/<object-key>`.

Public shared-cache delivery is restricted to the explicit `public/`
namespace and requires the trusted origin to return
`X-Testagram-Visibility: public`. Unclassified, private or denied origin
responses are never cached or served stale. A 401/403/404 origin response
invalidates a stale public cache entry.

Private media requests carry a Backend-issued assertion in
`Authorization: Bearer <assertion>`. The Go CDN forwards it to the trusted
Backend origin as `X-Testagram-Viewer-Authorization` together with the
separate service credential `X-Testagram-Origin-Auth`. Private bytes bypass
hot/disk caches and are returned with `Cache-Control: private, no-store`.
The Backend origin validates the viewer assertion and canonical asset record.

The Go CDN supports GET/HEAD, single byte ranges, ETags, content types and
HLS/DASH byte streaming. The legacy CDN-side `/v1/tv/*` proxy is deliberately
disabled (HTTP 503) until the Backend TV catalogue/playback-grant contract,
Data Centre ingestion and Flutter manifest/segment integration are complete.
The CDN must not mint playback grants or proxy arbitrary caller-provided
`src` URLs.

## Capacity verification

Normal CI runs the full correctness suite, race detector and vet. The one-million logical-viewer cache-fanout test is run in CI, but it is an in-process correctness stress test, not proof of one million real Internet viewers:

CAPACITY_TEST=1 go test ./internal/edge -run TestCapacity1MLogicalViewers -count=1 -timeout 10m

It verifies that 100,000 concurrent logical viewers can request the same uncached segment through the in-process data plane while the origin sees only one fetch. This is a fan-out correctness test, not proof of 100,000 real Internet viewers.

For real capacity certification, run staged external tests against deployed nodes:

- 1,000 viewers
- 5,000 viewers
- 10,000 viewers
- 25,000 viewers
- 50,000 viewers
- 100,000 viewers

Each stage must record p50/p95/p99 latency, HTTP error rate, rebuffer/stall rate, edge egress, origin egress, origin request rate, cache hit ratio, CPU, memory, open connections and network saturation. Do not promote a stage if any SLO regresses.

## Cost boundary

The software can remain open-source and free. Public bandwidth, compute, IP transit and always-on machines are physical resources and cannot honestly be promised as unlimited/free forever.

GitHub documents that standard GitHub-hosted runners are free for public repositories, but they are temporary CI machines rather than persistent video-serving nodes. GitHub also imposes workflow/job limits, so CI must remain build/test automation rather than the CDN transport layer.

The real target is:

- no mandatory CDN vendor fee
- no per-video-request edge-function dependency
- aggressive cache reuse
- shielded origins
- optional P2P offload
- independently deployable edge nodes

## Production rule

Do not expose the origin directly to public viewers once the CDN is integrated.

Public path:

viewer -> edge -> cache -> shield -> authenticated origin

Never:

viewer -> origin

<!-- CI verification marker: current main is continuously verified by GitHub Actions. -->


## IPTV continuity profile

The default production profile is deliberately light enough for small edge nodes while protecting live playback:

- 8 GiB disk cache and 512 MiB hot RAM cache by default; both are bounded.
- 2,048 global idle upstream connections, 256 idle connections per host and a 512 per-host connection ceiling.
- Live playlists are short-lived so viewers discover new segments quickly.
- Media segments receive a longer stale recovery window so a brief origin/R2 outage does not immediately interrupt playback.
- The edge coalesces concurrent requests for the same live object.
- After a live playlist is fetched, the edge warms the earliest advertised media until at least 45 seconds is covered (or the playlist ends), using bounded concurrency. Extensionless HLS media URIs are supported; playlists are never prefetched as media. Each warmup pass has a hard timeout and records success/failure metrics.
- Generic HLS/DASH byte delivery is supported by the Go data plane when it is backed by an approved origin.
- The product TV route `/v1/tv/*` remains disabled until Backend-issued playback grants and Flutter subrequest authorization are complete.

HLS requires segments referenced by a live playlist to remain available long enough to avoid interrupting playback, and clients are expected to load segments ahead of their presentation time. The CDN therefore treats the playlist as control-plane metadata and segments as durable short-lived data-plane objects.


**Security note:** legacy CDN-issued TV playback tokens are not an approved product authorization path; the `/v1/tv/*` route is disabled. Private media uses Backend-issued viewer assertions relayed to the trusted origin. See [docs/media-authorization.md](docs/media-authorization.md).
