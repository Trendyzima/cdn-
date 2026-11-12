# Testagram media integration

## Canonical data path

1. Flutter requests upload intent and completion through the authenticated Backend API.
2. Backend creates the canonical object key and metadata row and coordinates storage with Data Centre.
3. Data Centre owns placement, replication, repair and deletion across configured providers. Provider credentials remain server-side.
4. Backend returns canonical CDN URLs only after the object is ready and its visibility is known.
5. Requests without viewer assertions may enter the Go CDN shared cache only after Backend checks the canonical asset row and confirms `visibility=public` and `status=ready` with `X-Testagram-Visibility: public`; object-key prefixes are not visibility authority.
6. Private objects require a short-lived Backend viewer assertion on each media request. The Go CDN relays it to Backend's private `/internal/cdn/v1/{key...}` origin route and never caches the private bytes.

The CDN does not mint media URLs, decide visibility, own media records or count cached bytes as durable replicas.

## Host routing

`media.testagram.site/*` belongs to the Go CDN data plane in this repository.
The Cloudflare R2 Worker under `cloudflare/` is deployed only as an unrouted
script with `workers_dev = false`. It must not attach routes to this hostname,
including `/healthz`, `/readyz` or `/v1/users/*`.

Cloudflare provides DNS and private R2 storage. It is not a second viewer-facing
media CDN for this hostname. The Go CDN provides the public health/readiness
endpoints and canonical media delivery.

## Authorization and origin contract

For private media:
- Backend issues `v1.<base64url-json>.<base64url-hmac-sha256>` with exact key,
  owner, viewer, issuer, audience, visibility, issue/expiry and JTI claims.
- Flutter sends it as `Authorization: Bearer <assertion>`.
- Go CDN forwards it as `X-Testagram-Viewer-Authorization`.
- Go CDN separately authenticates itself with `X-Testagram-Origin-Auth`,
  using the CDN-side `ORIGIN_AUTH_TOKEN` and Backend
  `CDN_ORIGIN_AUTH_TOKEN`.
- Backend verifies both credentials, validates the canonical asset row and
  streams the object with `private, no-store`.
- Private requests bypass Go CDN cache, shields and all public-origin fallback;
  responses are no-store. Mutable user media is never served stale after an
  origin outage.

For public media, a request without a viewer assertion is cache eligible only
after Backend checks the canonical asset row and explicitly returns
`X-Testagram-Visibility: public`. Object keys commonly use
`users/<owner>/<id>` for both public and private assets; the path itself is not
a visibility signal. Current Backend origin is private-only; Backend issue
#205 tracks the missing public branch. Until it lands, public CDN cache misses
fail closed. Direct Cloudinary/R2 fallback is disabled to avoid bypassing
canonical visibility. Authorization, visibility and deletion denials are
authoritative. The CDN invalidates its local copy on explicit denials; Backend
must also publish authenticated purge events for visibility/deletion changes.
That purge integration is part of Backend issue #205.

## TV and HLS

The old CDN-side `PLAYBACK_SECRET` proxy is disabled. The Go CDN returns
HTTP 503 for `/v1/tv/*` until Backend catalogue and playback-grant handlers,
Data Centre catalogue ingestion, per-manifest/segment authorization and
Flutter player refresh/range behavior are complete and staging-tested. Do not
proxy arbitrary `src` URLs or let Flutter call Data Centre directly.

## Scaling and capacity

The repository contains local cache, request coalescing, origin failover,
shield routing and bounded hot/disk cache tiers. These are software
capabilities, not a measured Internet capacity guarantee.

A 100,000-viewer target at 2 Mbps average media bitrate requires about
200 Gbit/s aggregate egress. Real capacity must be proven across deployed
regional nodes with staged external load tests. Record p50/p95/p99 latency,
error rate, rebuffer rate, cache hit ratio, origin request rate, edge egress,
CPU, memory, open connections and network saturation. Never infer capacity
from an in-process logical-viewer fan-out test.
