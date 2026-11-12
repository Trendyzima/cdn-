# Cloudflare R2 private-media Worker deployment

## Purpose and current state

This Worker is an optional R2 delivery adapter prototype for Backend-authorized
private user media. It is **not the active Testagram media delivery plane**.
The canonical public hostname `media.testagram.site` belongs to the Go CDN in
this repository. The Worker is deployed as an unrouted script with
`workers_dev = false`; it has no public custom-domain route.

Do not attach this Worker to `media.testagram.site`, even for health probes.
The production smoke workflow checks the canonical Go CDN at that hostname.
A distinct Worker hostname requires a reviewed API service-registry entry,
Backend configuration, DNS/TLS ownership check and separate activation PR.

The R2 bucket must remain private. Bind it as `MEDIA_BUCKET`; do not enable
public bucket access to make a route work.

## Credentials and ownership

- Backend owns media authorization and signs the exact private object assertion.
- Worker verifies `CDN_MEDIA_ASSERTION_SECRET` (minimum 32 random bytes).
- Go CDN uses a separate `ORIGIN_AUTH_TOKEN` matching Backend
  `CDN_ORIGIN_AUTH_TOKEN` for its trusted origin hop.
- Never reuse Appwrite, S3, origin-service or Federation credentials as the
  assertion signing secret.
- Never put any server secret in Flutter, Android, browser bundles, Data Centre
  nodes, source control or logs.

GitHub Actions deployment requires `CLOUDFLARE_API_TOKEN`,
`CLOUDFLARE_ACCOUNT_ID` and `CDN_MEDIA_ASSERTION_SECRET`. It deploys the
script and Worker secret only; no DNS, route, bucket ACL or public activation
is performed.

## Assertion format

The Worker accepts
`v1.<base64url-json>.<base64url-hmac-sha256>`. Claims include issuer
`testagram-backend`, audience `testagram-cdn`, viewer subject, canonical
owner, exact object key, `visibility=private`, issue/expiry times and random
JTI. Maximum assertion lifetime is five minutes. HMAC-SHA256 verification is
performed before R2 reads. Query-string tokens are rejected.

## Tests before any activation

Run `cd cloudflare && npm test`. Verify missing/weak secret, missing/expired,
tampered, wrong-viewer, wrong-owner, wrong-key and wrong-audience assertions
are denied before R2 access. Verify GET, HEAD and single byte ranges, 206
headers, missing object 404, R2 outage 503 and `private, no-store` behavior.

Before adding a route:
1. approve a distinct hostname that does not overlap the Go CDN;
2. record the service in the shared API registry and Backend configuration;
3. prove Backend signer parity and external GET/HEAD/range tests;
4. verify no intermediary caches private bytes or assertion-bearing requests;
5. document rollback and keep the Go CDN route unchanged.

A successful script deployment is not proof of production delivery. TV/HLS
playback grants are not implemented by this Worker.
