# Testagram CDN: Instabox + R2 + B2 private origin contract

## Canonical public delivery

The Go CDN is the only active public media byte-delivery plane. The hostname
`media.testagram.site` terminates at Testagram-owned Go CDN ingress. Cloudflare
provides authoritative DNS and private R2 storage; it must not attach the
unrouted R2 Worker or a competing cache route to the Go CDN hostname.

## Private origins

The CDN supports configured private origin gateways:
1. Backend's authenticated media origin (canonical authorization and object
   resolution);
2. approved Instabox VPS / MinIO origin gateway;
3. approved R2/B2 origin gateways only after explicit adapter and credential
   contracts are implemented.

The preferred origin order is configurable and health-aware. A failed origin
may be retried only when the operation is safe and the next origin has the same
verified object identity. The CDN must not infer replica integrity from HTTP
200 or provider ETag alone. Data Centre's replica ledger and independent
SHA-256 read-back are the source of replica-integrity evidence.

Direct S3-compatible private-origin support is not implied by a generic bearer
header. The current Go CDN origin client speaks the Backend internal HTTP
gateway contract; it is not an S3 Signature V4 client. Do not point it directly
at MinIO, R2 or B2 S3 endpoints until an adapter is implemented and tested.

**Public origin blocker:** current Backend `/internal/cdn/v1/{key...}` only
serves private assets with a viewer assertion. Backend issue #205 tracks a
service-authenticated public branch that checks canonical `visibility=public`
and `status=ready` before returning bytes. The CDN deliberately fails closed
rather than bypassing Backend with Cloudinary/R2 direct URLs.

## Authorization and cache isolation

- Backend authorizes viewer, object, action, visibility and expiry.
- Go CDN forwards the viewer assertion and a separate origin-service
  credential to the trusted Backend origin.
- The Backend origin validates both credentials and the canonical asset
  record before streaming.
- Private media bypasses disk cache, hot cache, shields and public-origin
  fallbacks; it is always `private, no-store`.
- Unauthenticated requests may enter shared cache only after Backend checks
  the canonical asset row and explicitly classifies a ready public object with
  `X-Testagram-Visibility: public`; key prefixes are not visibility authority.
- Missing, expired, malformed, wrong-viewer or wrong-object assertions fail
  closed at the Backend origin.
- CDN cache purge is not object deletion. Backend owns deletion tombstones and
  Data Centre confirms provider/node deletion receipts.

## Cache and replica separation

CDN disk/RAM cache and shields are disposable delivery accelerators, never
counted as durable media replicas. Mutable user media is not served stale after
expiry or origin failure. Browser cache lifetimes are zero and shared cache
lifetimes are bounded. Cache purge is not object deletion.
Backend owns deletion tombstones; Data Centre confirms provider/node deletion
receipts and performs replica repair/reconciliation.

## Operational metrics

Export per-edge cache hit/miss, bytes served, origin selection/failover, origin
latency/error, authorization denials, private cache bypasses, HLS/DASH/range
errors, node disk/RAM/network saturation and replication-origin availability.
Send operational metrics to Data Centre monitoring; only privacy-minimized
aggregates should be copied to product analytics.

## Implementation gates

- Implement private origin adapters for Instabox/MinIO, R2 and B2 or use the
  Backend private origin gateway; do not claim direct private S3 support until
  signed-request and checksum contracts exist.
- Add per-origin credentials and independent health checks; do not overload
  one bearer token for provider authentication.
- Test origin failover with provider outage and checksum mismatch fixtures.
- Test private grant denial before bytes are read and prove private cache
  bypass at every tier.
- Verify DNS routes to Go CDN nodes and that the Cloudflare Worker has no
  overlapping route.
- Prove delivery continuity when each configured origin is unavailable in
  turn, without bypassing Backend authorization for private objects.

This contract does not authorize DNS, origin, bucket ACL or secret changes.
