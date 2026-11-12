# Private R2 Worker release contract

## Scope

The Worker is authoritative only for the private user-media route:

`https://media.testagram.site/v1/users/*`

The Wrangler route must remain narrowly scoped. Do not attach public-media, profile,
video, HLS or other keyspaces to this Worker until each has a separate authorization,
cache and purge contract.

## Required bindings

- `MEDIA_BUCKET`: the production R2 bucket configured in `wrangler.toml`.
- `CDN_MEDIA_ASSERTION_SECRET`: a server-side secret of at least 32 bytes,
  provisioned through GitHub Actions / Cloudflare Worker secrets.
- Backend is the sole issuer of viewer authorization assertions. The Worker verifies
  the assertion and does not make social-visibility decisions.

The secret must never be added to Flutter, Android, browser bundles, Data Centre node
images, source control, logs or public build arguments. Rotate the Backend issuer and
Worker verifier in a coordinated overlap window; verify both sides before revoking the
previous secret.

## Required release sequence

1. Run the Worker unit/contract suite.
2. Deploy the Worker from the pinned main revision.
3. Run `Production Media Smoke` only after the deployment workflow succeeds.
4. Require `/healthz` to identify the expected Worker version.
5. Require `/readyz` to report `ok=true`, proving the assertion secret and R2 binding
   are available.
6. Require a private object request without a viewer assertion to return HTTP 401.

A successful Wrangler deployment is not production verification. If any smoke check
fails, treat private media delivery as unavailable and investigate the route, Worker
deployment, R2 binding, DNS and secret configuration. Do not weaken the expected
version or authorization assertions to turn the check green.

## Current implementation boundary

The current Worker implements Backend-asserted private user-media delivery. It is not
a generic CDN signing service and does not yet issue TV/HLS playback grants. TV
playback requires a separate Backend issuer assertion contract, CDN route and
end-to-end range/manifest/segment tests.
