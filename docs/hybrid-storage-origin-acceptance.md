# Hybrid storage origin acceptance: InstaBox, R2 and B2

The Testagram Go CDN is the only public media byte-delivery service. Cloudflare DNS points the media hostname to Go CDN ingress. Cloudflare R2 and Backblaze B2 are private object stores; neither is a public authorization bypass. VPS MinIO, R2 and B2 are durable replicas. CDN RAM/disk/shield caches are disposable and never count toward durability.

## Trust boundary

- Backend authorizes viewer, owner, visibility, moderation and asset readiness using canonical PostgreSQL records.
- Data Centre owns storage provider credentials, replication, checksum receipts, repair and restore.
- CDN validates the Backend-origin service credential and relays the viewer-bound Backend assertion.
- CDN must never accept provider credentials or decide that a key is public from its prefix.
- Public cache fill requires explicit Backend classification of visibility=public and status=ready.
- Private media is always private/no-store and bypasses shared cache, hot cache, disk cache and shields.

## Origin selection and failover

A provider is eligible only when the Data Centre replica ledger records an independently verified SHA-256 and byte length matching the canonical asset record. HTTP 200, ETag equality, provider availability or a cached response are not proof of replica integrity.

For a safe retry/failover:
1. Revalidate Backend authorization for the exact viewer, object and action.
2. Obtain an eligible replica receipt for the same canonical asset and digest.
3. Fetch through a private origin adapter with bounded timeout, response size and redirect policy.
4. Verify response size/content type and, where the transport contract requires, streamed SHA-256 before marking the origin healthy.
5. Preserve authorization denials; never turn 401/403/404 into a fallback public read.
6. Never expose signed provider URLs, bucket names, credentials or origin errors to clients.

The current CDN origin client speaks the Backend private HTTP origin contract, not S3 SigV4. Direct MinIO/R2/B2 access requires a separately reviewed private SigV4 adapter and must not be improvised by adding a bearer header.

## Required acceptance tests

- Private media: correct viewer succeeds; wrong viewer, expired assertion, wrong object and missing assertion fail closed.
- Private responses never enter shared cache; cache keys cannot mix viewers.
- Public media: only Backend-confirmed ready/public assets are cacheable.
- Origin outage: no direct provider bypass; safe replica failover only with a matching receipt.
- Range, HEAD, HLS/DASH and large-object streaming work across each approved origin adapter.
- Checksum mismatch quarantines the replica and emits an operational alert.
- Cache purge and provider deletion remain separate operations; tombstones and provider deletion receipts govern deletion.
- media.testagram.site remains on Go CDN; do not add a competing Cloudflare Worker route.

No production origin, DNS, cache or provider configuration is changed by this document.
