# Cloudflare R2 private-media Worker deployment

## Purpose and boundary

This Worker is the protected delivery interface for the private `testagram-media` R2 bucket. It is not the Testagram public product API, DNS authority, general-purpose public CDN, or source of media ownership decisions. The Backend is the authority for media ownership, visibility and viewer access. It issues a short-lived v1 assertion for an exact private object and viewer.

The R2 bucket must remain private. Bind it to the Worker as `MEDIA_BUCKET`; do not enable public bucket access to make a route work.

## Hostname ownership — mandatory before deployment

The Go CDN repository reserves `media.testagram.site` for the Go CDN data plane. Do not attach this Worker to that hostname or add a competing DNS/Worker route.

Choose and approve a distinct hostname for the private R2 Worker (for example, `r2-private.testagram.site`) and record it in the API service registry, Backend environment and Cloudflare zone runbook. Do not create DNS records until the current live routes, target origin, TLS and owner are checked.

`wrangler.toml` intentionally has `workers_dev = false` and no custom-domain route. This prevents accidental public activation. Add the approved custom domain/route only in a reviewed deployment change after DNS ownership and origin separation are confirmed.

## Required secret and binding

- R2 binding: `MEDIA_BUCKET` -> private `testagram-media` bucket.
- Worker secret: `CDN_MEDIA_ASSERTION_SECRET`, generated with at least 32 random bytes. Set it with Wrangler secret management, not in `wrangler.toml`, source files, or client apps.
- Backend secret: `CDN_MEDIA_ASSERTION_SECRET` must contain the exact same secret bytes. It is separate from S3 credentials, `CDN_ORIGIN_AUTH_TOKEN`, Go CDN `ORIGIN_AUTH_TOKEN`, `PURGE_TOKEN`, Appwrite keys and Federation tokens.
- Go CDN origin service credential: configure the same random value as Go CDN `ORIGIN_AUTH_TOKEN` and Backend `CDN_ORIGIN_AUTH_TOKEN`. This is not the assertion signing key.
- Rotate by deploying an overlap-capable versioned key scheme in a coordinated change; do not rotate only one side or invalidate all active playback unexpectedly.

This Worker fails closed when the secret is missing or shorter than 32 characters. All media requests to this private Worker require a valid token. Public media must use a separately designed public delivery path and must not be made public by omitting the secret.

## Signed request contract

Token format is `<unix-expiry>.<hex-hmac-sha256>`. HMAC input is the exact normalized R2 object key, a literal pipe, and the decimal expiry: `object-key|expiry`.

The Backend must:
1. authorize the current viewer against canonical media ownership and post visibility;
2. sign only the exact normalized object key;
3. use a short expiry (2 minutes by default, with a hard maximum of 5 minutes in the Backend signer);
4. never accept a caller-provided bucket, endpoint, host or object key without canonical record lookup;
5. return no signed URL when authorization or signing configuration is unavailable.

Worker acceptance tests must prove missing/weak secret, missing/expired/malformed/wrong-key/wrong-viewer/wrong-audience assertion denial occurs before R2 reads; valid tokens permit GET/HEAD and byte ranges; private responses use `Cache-Control: private, no-store`; path traversal never reaches R2.

## Deployment and verification

1. Confirm the private R2 bucket exists in the intended account and is not publicly accessible.
2. Configure the R2 binding and Worker secret in the correct Cloudflare environment.
3. Configure the distinct hostname only after the route ownership review.
4. Confirm HTTPS, TLS policy, no cache rule can cache token-bearing/private media, and origin/Worker logs do not record secrets.
5. Run `npm test` and the Backend signer contract tests.
6. Verify from an external network: no-token -> 401; expired/wrong-key -> 401; valid Backend assertion GET -> 200; valid assertion range -> 206; missing object -> 404; R2 failure -> 503; readiness fails when R2 is unavailable.
7. Confirm the Go CDN hostname and routes are unchanged.
8. Record Worker deployment version, route, R2 bucket identifier (not credentials), secret rotation reference, smoke-test evidence and rollback procedure.

No production deployment, DNS mutation, bucket ACL change or secret operation is performed by this source repository PR.
