# Testagram Edge CDN

A self-hosted, Bunny/Cloudflare-style data-plane CDN for live HLS/DASH, built as a normal Go HTTP service. GitHub is the source/build/automation layer; it is never the video transport layer.

## Architecture

Viewer -> regional edge -> ephemeral cache -> origin

Optional:

Viewer <-> P2P peers
        \-> HTTP edge fallback

The design deliberately does not require edge/serverless functions for video requests. Every manifest/segment request is handled by the long-running Go edge process.

## Implemented

- HLS/DASH HTTP delivery
- GET and HEAD
- byte-range support through standard HTTP serving
- ephemeral LRU cache with byte capacity
- atomic cache writes
- short live-stream TTLs
- request coalescing for simultaneous cache misses
- multi-origin failover with temporary origin quarantine
- authenticated edge-to-origin requests
- optional HMAC playback authorization
- protected cache purge endpoint
- per-client rate limiting
- CORS and security headers
- ETag and Cache-Status/X-Cache headers
- Prometheus-style metrics
- health/readiness endpoints
- non-root Docker image
- race-tested CI
- immutable GHCR image tags by commit SHA

Bunny documents request coalescing, customizable caching, token authentication, automatic healing and monitoring as CDN capabilities. This repository implements the parts useful for Testagram without putting a metered edge-function invocation in the media path.

## Live HLS cache policy

Manifests are intentionally short-lived:

- .m3u8: default cache TTL 2 seconds
- media segments: default cache TTL 20 seconds
- default object limit: 16 MiB
- default edge cache capacity: 2 GiB

This is a rolling cache, not permanent video storage. Old live segments disappear through TTL/LRU eviction.

## Request coalescing

If 500 viewers request the same uncached segment at nearly the same time:

1. first request becomes the upstream fetch;
2. other requests join the same in-flight fetch;
3. one origin request supplies the object;
4. the object is cached;
5. all waiting viewers receive the same result.

This is the critical mechanism for fan-out without multiplying origin traffic.

## Origins

Use either:

ORIGIN_URL=https://origin.example

or:

ORIGIN_URLS=https://origin-a.example,https://origin-b.example

The edge temporarily quarantines failing origins and tries the next healthy origin.

Set ORIGIN_AUTH_TOKEN when the origin should only accept authenticated edge traffic.

## Playback authentication

Set PLAYBACK_SECRET to require:

?token=<expiry>.<hmac>

The signature covers the exact asset path and expiry.

Important: for browser/native HLS, do not enable this blindly on an existing playlist format. HLS playlists reference the segment URLs that players subsequently request. Authentication must be designed so those child requests carry valid credentials as well. Apple documents cookie/header authentication and URL-based segment authorization patterns for HLS.

The Testagram integration phase should therefore use either:
- a session-bound authorization mechanism supported by the chosen player, or
- a stable per-session token propagated into playlist resource URLs.

## Purging

Set PURGE_TOKEN.

Purge everything:

POST /api/cache/purge
Authorization: Bearer <token>

Purge one asset:

POST /api/cache/purge?path=live/channel/index.m3u8
Authorization: Bearer <token>

Purge is disabled unless a token is configured.

## Rate limiting

Defaults:

- 1,200 requests/minute/IP
- burst: 300

Tune these for the actual player population. The limiter is a protective layer, not the primary DDoS solution.

## Metrics

/metrics exposes:

- cache hits/misses
- upstream requests
- in-flight fetches
- bytes served
- rejected requests
- cache items
- cache bytes/capacity

## Environment

| Variable | Default | Purpose |
|---|---:|---|
| LISTEN_ADDR | :8080 | HTTP listener |
| ORIGIN_URL | empty | primary origin |
| ORIGIN_URLS | empty | ordered origin list |
| ORIGIN_AUTH_TOKEN | empty | edge-to-origin bearer token |
| PLAYBACK_SECRET | empty | optional HMAC playback auth |
| PURGE_TOKEN | empty | purge API authorization |
| CACHE_DIR | ./cache | cache directory |
| MAX_CACHE_BYTES | 2 GiB | cache capacity |
| SEGMENT_TTL | 20s | segment cache TTL |
| MANIFEST_TTL | 2s | playlist cache TTL |
| MAX_SEGMENT_BYTES | 16 MiB | object size limit |
| ORIGIN_TIMEOUT | 10s | upstream timeout |
| RATE_LIMIT_PER_MIN | 1200 | per-IP rate |
| RATE_LIMIT_BURST | 300 | per-IP burst |
| ALLOWED_ORIGINS | empty | optional CORS allow-list |
| NODE_ID | local | edge identity |

## Local verification

    go test ./...
    go test -race ./...
    go vet ./...
    go build ./cmd/edge

Docker:

    docker compose up --build

## Scaling path

1. standalone edge data plane
2. origin authentication
3. signed/session playback
4. regional edge registry
5. latency/health-aware routing
6. cache tier/shield
7. graceful draining
8. P2P HLS integration
9. private tracker/signalling
10. load/chaos testing at 1k, 5k and 10k+ viewers
11. only then connect Testagram

Do not claim million-viewer capacity until measured with realistic segment bitrate, segment duration, cache-hit ratio, concurrent connections, origin request rate and node bandwidth.

## Cost boundary

The software can remain open-source and free. Public bandwidth, compute, IP transit and always-on machines are physical resources and cannot honestly be promised as unlimited/free forever.

GitHub documents that standard GitHub-hosted runners are free for public repositories, but those runners are temporary CI machines; they are not suitable as the persistent video-serving layer.

The target is:

no mandatory CDN vendor fee
no per-video-request edge-function dependency
aggressive cache reuse
optional P2P
independently deployable edge nodes

## Production rule

Do not expose the origin directly to public viewers once the CDN is integrated.

Public path:

viewer -> edge -> cache -> authenticated origin

Never:

viewer -> origin
