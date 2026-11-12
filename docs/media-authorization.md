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

Only objects in the explicit canonical `public/` namespace may use the
unauthenticated shared-cache path. The trusted origin must explicitly return
`X-Testagram-Visibility: public`; absence of this header or any other value
must fail closed. Private namespaces such as `users/`, `profiles/` and
`uploads/` require viewer authorization and bypass all CDN caches.

Direct Cloudinary/R2 public-origin fallback is allowed only for explicit
`public/` keys and configured public delivery origins. Never point
`R2_PUBLIC_BASE_URL` at a private bucket or use it to expose user-private
objects.

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
