# Canonical Testagram API and Go CDN gateway

## Production request ownership

The Cloudflare Worker `testagram-cdn` is the public hostname gateway for:

- `api.testagram.site/*` → the published HTTPS origin of the Testagram Backend on InstaBox port 8080.
- `media.testagram.site/*` → the published HTTPS origin of the canonical Go CDN on InstaBox port 8081.

The Worker forwards requests and responses. It does not read R2 objects, authorize viewers, or make media visibility decisions.

The Go CDN is the only public media delivery plane. Backend owns media identity, visibility, moderation and viewer authorization. The CDN forwards private viewer assertions to the trusted Backend origin and delivers bytes only after Backend authorization. The R2 buckets remain storage origins and must not own the public `media.testagram.site` custom domain after cutover.

## Hostname ownership and cutover

The canonical-host workflow must:

1. Validate both separate HTTPS upstream origins and their service identities before changing Cloudflare.
2. Inspect the active `testagram.site` zone, exact Worker routes and custom-domain ownership.
3. Discover whether `media.testagram.site` is currently attached directly to an R2 bucket or already attached to the `testagram-cdn` Worker.
4. If R2 owns the hostname, capture the exact bucket and custom-domain settings, then detach only that hostname. It must not delete, copy, empty, or modify any bucket or object.
5. Deploy the gateway Worker and verify the public API and Go CDN identities, private readiness/metrics isolation, and anonymous private-media denial.
6. If public verification fails, restore the previous Worker deployment and, when the previous owner was R2, detach the failed Worker custom domain and reattach the captured R2 custom-domain settings. Verify the restored owner.

The Cloudflare API token needs Zone read, Worker Scripts read/write, Worker Routes read/write, and Workers R2 Storage read/write permissions for the controlled R2-to-Worker transfer. If any ownership cannot be enumerated or the token lacks permission, the workflow must stop before changing the hostname.

The R2 domain transfer affects routing only. It does not change bucket contents, object keys, credentials, lifecycle rules, or private-media authorization.

## Security boundaries

- Public `/healthz` identifies the expected service.
- `/readyz` and `/metrics` remain private and must return 404 through public hostnames.
- Anonymous private-media requests must be denied (401, 403 or 404).
- The Worker must reject unknown hostnames and unsafe upstream origins.
- The Worker must not expose InstaBox control-plane URLs or internal listener ports in redirects.
- The Go CDN origin credential, Backend viewer-assertion signing secret and CDN purge credential are separate server-side secrets. Never put them in Flutter, browser bundles, URLs, source control or logs.
- Private media responses remain `Cache-Control: private, no-store`; the gateway must not cache viewer assertions or private bytes.

## Release and rollback acceptance

A successful Worker script upload alone is not a production release. Release acceptance requires external probes against both canonical hostnames and the exact service/version headers, plus verification that the media hostname is owned by the Go CDN gateway rather than an R2 bucket. The rollback path must be tested against the captured pre-cutover owner before the release is declared complete.
