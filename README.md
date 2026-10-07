# Testagram Edge CDN

Self-hosted, ephemeral HLS edge cache for Testagram. GitHub is the source of truth and GitHub Actions builds, tests and packages the edge. GitHub is not used as the video delivery plane.

## Current status

This repository is the CDN foundation only. It is not wired to testagram.site yet.

Implemented:
- HLS/DASH-compatible HTTP asset delivery
- short TTL cache for live manifests and segments
- LRU byte-capacity eviction
- request coalescing for concurrent cache misses
- path traversal protection
- origin size limits
- CORS/security headers
- health/readiness endpoints
- Prometheus-style metrics
- non-root Docker image
- GitHub Actions CI and GHCR image publishing

## Traffic model

Viewer -> nearest edge -> ephemeral cache -> origin

A cache miss is fetched once from the origin. Other concurrent viewers for that exact object wait for the same upstream request. This is important for live segment fan-out.

The cache is intentionally disposable. It is not a VOD store.

## API

GET /healthz
GET /readyz
GET /metrics
GET /v1/<channel>/<asset>

The origin must expose the same path.

## Environment

ORIGIN_URL is required.
LISTEN_ADDR defaults to :8080.
CACHE_DIR defaults to ./cache.
SEGMENT_TTL defaults to 20s.
MANIFEST_TTL defaults to 2s.
MAX_CACHE_BYTES defaults to 2 GiB.
MAX_SEGMENT_BYTES defaults to 16 MiB.
NODE_ID identifies an edge.
ALLOWED_ORIGINS optionally restricts browser origins.

## Scale design

The next layers are separate from this edge:
1. multi-origin health and failover
2. edge registration and regional routing
3. signed playback authorization
4. adaptive-bitrate-aware cache keys
5. P2P HLS delivery in the player
6. automated edge deployment and draining
7. load and chaos testing
8. observability and capacity controls

Million-viewer capacity must be demonstrated by load testing; it is not claimed by the current code.

## Security

Do not expose an origin directly to public viewers after integration. The origin should accept authenticated edge traffic. Playback authorization must be added before production.

## Local run

go test ./...
go run ./cmd/edge

or:

docker compose up --build

## Integration rule

No Testagram application changes belong in this repository until the edge passes independent functional, failure, load and security tests.
