# CDN media authorization contract

## Authority and active delivery path

Backend is the sole authority for identity, media ownership, visibility,
moderation and viewer access. Data Centre owns durable storage operations.
The Go CDN is a byte-delivery service and never decides whether a viewer may
see a post or attachment.

For private media:

1. Flutter obtains a short-lived, object/viewer-bound assertion from the
   authenticated Backend media-download endpoint.
2. Backend checks the canonical asset record, owner, ready/deletion state,
   moderation and the viewer's current post/media visibility.
3. Flutter sends the assertion as `Authorization: Bearer <assertion>` to the
   canonical Go CDN URL.
4. The Go CDN does not cache private bytes. It forwards the assertion in
   `X-Testagram-Viewer-Authorization` and its separate service credential in
   `X-Testagram-Origin-Auth` to the trusted Backend origin route
   `/internal/cdn/v1/{key...}`.
5. The Backend origin validates the service credential, assertion signature,
   issuer/audience, exact object key, viewer, owner and expiry, then resolves
   the canonical object and streams it. A CDN request without a valid viewer
   assertion cannot be upgraded to public access.

The Go CDN preserves origin authorization denials and streams private responses
with `Cache-Control: private, no-store`, `CDN-Cache-Control: no-store` and
cache bypass. It does not put private content in its hot/disk cache.

## Assertion format

The Backend assertion contract is
`v1.<base64url-json>.<base64url-hmac-sha256>`, signed with the dedicated
`CDN_MEDIA_ASSERTION_SECRET`. Claims include:
- `v=v1`, `iss=testagram-backend`, `aud=testagram-cdn`;
- `sub` as the authorized viewer ID;
- `owner` as the canonical media owner ID (legacy owner-only assertions may
  omit it, in which case the subject must equal the owner);
- `key` as the exact canonical object key and `visibility=private`;
- `iat`, `exp` and a random `jti`.

The Go CDN relays this assertion to the trusted Backend origin; the origin is
the cryptographic verifier and authorization decision point. Never confuse the
viewer assertion with the separate Go CDN service credential
(`ORIGIN_AUTH_TOKEN` / Backend `CDN_ORIGIN_AUTH_TOKEN`).

Do not send assertions in query strings, referrers, analytics or logs. Legacy
`?token=` media URLs are rejected. Never reuse Appwrite, storage-provider or
origin-service credentials as the assertion signing secret.

## Public media

Unauthenticated requests may use the shared-cache path only when Backend
checks the canonical asset row and explicitly returns
`X-Testagram-Visibility: public` for a ready public object. Testagram object
keys commonly use `users/<owner>/<id>` for both public and private media; the
path prefix is not a visibility signal. Missing or non-public classification
must fail closed.

**Integration blocker:** current Backend `/internal/cdn/v1/{key...}` is
private-only and requires a viewer assertion for every object. Backend issue
#205 tracks the missing service-authenticated public-origin branch. Until it is
merged and tested, public cache misses fail closed and direct Cloudinary/R2
provider fallback is disabled.

## Cloudflare R2 Worker boundary

The optional Worker source verifies the same Backend assertion before reading
its R2 binding, but it is currently deployed without a custom-domain route.
It is not in the active `media.testagram.site` request path. Do not add a route
to that hostname: the Go CDN owns it. Worker activation requires a distinct
approved hostname and a reviewed API/Backend contract.

## TV/HLS

The legacy CDN-side `PLAYBACK_SECRET` proxy is not an approved product
authorization path. The public `/v1/tv/*` route is disabled until Backend
catalogue/playback-grant handlers, Data Centre catalogue ingestion, CDN
verification semantics and Flutter manifest/segment/range/expiry tests are
implemented together. The CDN must not mint TV authorization from an arbitrary
`src` URL or caller-controlled channel identifier.


## Edge network boundary

The Go CDN rate limiter normally keys clients from the TCP peer address.
`TRUST_CLOUDFLARE=1` switches that input to `CF-Connecting-IP`. Enable it
only when the origin listener is protected by a firewall/security group or
authenticated tunnel that admits traffic exclusively from Cloudflare's
trusted proxy path. If the Go origin can be reached directly from the
Internet, a caller can forge `CF-Connecting-IP` and evade per-client rate
limits. DNS proxying alone is not an origin access control. Keep this setting
disabled until the network boundary has been proven.

The CDN service credential is sent only to configured trusted origin URLs.
Origin redirects are not followed, preventing that credential and a viewer
assertion from being forwarded to a redirect target. Keep origin URLs private,
use TLS for public-network origin hops, and do not put credentials in URL
user-info, query strings or paths.
