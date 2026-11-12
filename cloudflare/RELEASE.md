# Private R2 Worker release contract

## Runtime ownership

The Go CDN is the sole active media delivery plane for
`https://media.testagram.site/*`. It owns public aliases, cache/shield/origin
routing, byte ranges and the Backend-to-origin private authorization relay.

This Cloudflare Worker is a separately deployable R2 adapter prototype. It is
currently deployed as an unrouted Worker script with `workers_dev = false`.
It has **no custom-domain route** and must not be attached to
`media.testagram.site`. The Worker is not part of the active viewer request
path until a distinct hostname, Backend contract, API service-registry entry,
DNS/TLS ownership and rollout plan are approved.

## Ownership and credentials

- Backend owns identity, media ownership, visibility, moderation and viewer
  authorization.
- Data Centre owns durable placement, replication, repair and deletion.
- Go CDN delivers bytes and forwards private viewer assertions to the trusted
  Backend origin. It does not make product visibility decisions.
- The optional Worker verifies Backend-issued exact-object assertions before
  reading the private R2 binding; it does not issue grants.
- `MEDIA_BUCKET` binds the private `testagram-media` R2 bucket.
- `CDN_MEDIA_ASSERTION_SECRET` is server-side only. It must never be placed in
  Flutter, Android, browser bundles, Data Centre nodes, source control or logs.
- `ORIGIN_AUTH_TOKEN` / Backend `CDN_ORIGIN_AUTH_TOKEN` is a separate
  Go-CDN-to-Backend-origin service credential. Never reuse it as the assertion
  signing key.

The Worker deployment workflow publishes the script and secret only. It does
not create DNS records or custom-domain routes. A successful script deployment
does not mean R2 media is publicly reachable or production delivery is active.

## Assertion contract

The Worker accepts only `Authorization: Bearer v1.<base64url-json>.<base64url-hmac>`
assertions with:
- issuer `testagram-backend` and audience `testagram-cdn`;
- exact object key and canonical owner binding;
- viewer subject, private visibility, issue/expiry timestamps and random JTI;
- maximum five-minute lifetime;
- valid HMAC-SHA256 signature.

Query-string tokens are rejected. Private responses are always
`Cache-Control: private, no-store`. Missing/invalid assertions are rejected
before R2 reads. GET, HEAD and a single byte range are supported.

## Release requirements before activation

1. Agree a distinct hostname that does not overlap the Go CDN hostname.
2. Add the hostname and Worker contract to the shared API service registry and
   Backend configuration; verify DNS, TLS and route ownership.
3. Prove Backend emits the exact assertion format and that its owner/key claims
   match the canonical media row.
4. Test GET, HEAD, 206 ranges, expiry, wrong viewer, wrong owner, wrong key,
   missing object, R2 outage and query-token rejection.
5. Prove private no-store behavior and that no intermediary caches assertions
   or private bytes.
6. Deploy the Worker route only in a reviewed activation change and verify it
   from an external network.
7. Keep the Go CDN route unchanged and retain a tested rollback path.

The current production smoke workflow verifies the canonical Go CDN at
`media.testagram.site`, not this unrouted Worker. TV/HLS playback grants are
not implemented by this Worker and must use a separate Backend-authorized
playback contract.
