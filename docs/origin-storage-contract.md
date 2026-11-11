# Testagram CDN: Instabox + R2 + B2 private origin contract

## Public delivery

The Testagram Go CDN is the only public media byte-delivery plane. Cloudflare is used for authoritative DNS and private R2 object storage, not as the viewer-facing CDN.

media.testagram.site is a Cloudflare DNS record targeting Testagram-owned CDN edge ingress. The DNS record should be DNS-only so traffic reaches Testagram CDN directly. Do not use Cloudflare proxy/cache as an implicit second CDN tier.

## Private origins

The CDN must support three independently configured private origins:

1. Instabox VPS object storage (S3-compatible MinIO/private object service).
2. Cloudflare R2 private bucket.
3. Backblaze B2 private bucket.

The preferred order is configurable and health-aware. A failed origin is temporarily ejected; CDN retries a different healthy origin. An origin is eligible only after its object checksum/metadata has been verified by Backend/Data Centre replica ledger. The CDN must not infer replica integrity from HTTP 200 or provider ETag alone.

Origin credentials are private to CDN nodes and scoped read-only to required buckets/prefixes. Prefer short-lived origin signatures or an authenticated private origin gateway. Never put S3 credentials in viewer URLs or Flutter builds. The current generic bearer ORIGIN_AUTH_TOKEN is not itself an S3 Signature V4 implementation; do not claim direct private S3 support until the adapter exists and is tested.

## Authorization and cache isolation

- Backend authorizes viewer, object, action, visibility and expiry.
- Backend issues a short-lived, object/viewer/scope-bound delivery grant.
- CDN validates the grant before serving from cache or origin.
- Private response: Cache-Control: private, no-store unless a reviewed cache-key partitioning scheme proves per-viewer isolation and revocation semantics.
- Public immutable assets may use shared cache with immutable object-version keys.
- Missing, expired, malformed, wrong-viewer or wrong-object grants fail closed.
- CDN must not mint authorization from caller-controlled private=1, object path or public/private defaults.

## Cache and replication separation

CDN disk/RAM cache and shields are disposable delivery accelerators, never counted as durable media replicas. Cache purge is not object deletion. Backend owns deletion tombstones and Data Centre confirms provider/node deletion receipts.

## Operational metrics

Export per-edge cache hit/miss, bytes served, origin selection/failover, origin latency/error, authorization denials, private cache bypasses, HLS/DASH/range errors, node disk/RAM/network saturation and replication-origin availability. Send operational metrics to Data Centre monitoring; only privacy-minimized aggregates should be copied to product analytics.

## Implementation gates

- Add private S3-compatible origin adapters for MinIO, R2 and B2 or use a private origin gateway with explicit contracts.
- Add per-origin credentials and independent health checks; do not overload one bearer token for provider authentication.
- Test origin failover with provider outage and checksum mismatch fixtures.
- Test private grant validation before cache lookup and viewer cache isolation.
- Verify DNS-only Cloudflare records route to Testagram CDN nodes.
- Prove CDN continues delivering from Instabox/B2 when R2 is unavailable and from Instabox/R2 when B2 is unavailable.

This contract does not authorize a DNS or origin configuration change.
