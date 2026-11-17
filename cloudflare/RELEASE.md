# Canonical Testagram API, web, Go CDN and federation gateway

## Production request ownership

The Cloudflare Worker `testagram-cdn` is the public hostname gateway for:

- `api.testagram.site/*` → Testagram Backend on the InstaBox public origin for port 8080.
- `media.testagram.site/*` → the canonical Go CDN on the InstaBox public origin for port 8081.
- `testagram.site/*` → the dedicated Caddy edge on the InstaBox public origin for port 8443. Caddy routes WebFinger/ActivityPub to Federation and redirects ordinary web traffic to www.
- `www.testagram.site/*` → the same Caddy edge, which routes the trusted forwarded www hostname to the private Flutter web container.

The Worker forwards requests and responses. It does not read R2 objects, authorize viewers or make media visibility decisions.

The Go CDN is the only public media delivery plane. Backend owns media identity, visibility, moderation and viewer authorization. The CDN delivers bytes only after Backend authorization. R2 buckets remain storage origins and must not own the public `media.testagram.site` hostname after cutover.

## Hostname ownership and cutover

The guarded workflow must:

1. Validate separate Backend and Go CDN origins, plus the Caddy edge origin for both apex and www, before changing Cloudflare.
2. Prove that the Caddy origin serves apex WebFinger/redirect behavior and that the www path returns the actual Flutter web shell and service health identity.
3. Inspect the active `testagram.site` zone, exact Worker routes, Worker custom-domain ownership and the exact Pages associations for apex and www.
4. Verify existing proxied DNS records. DNS records are not changed by this workflow.
5. Discover whether `media.testagram.site` is attached directly to an R2 bucket or already owned by the canonical Worker. If R2 owns it, capture the exact bucket/domain settings and detach only that hostname; never delete, copy, empty or modify any bucket or object.
6. Detach only the exact `testagram.site` and `www.testagram.site` associations from the expected Flutter Pages project after origin and ownership checks pass.
7. Deploy the gateway and verify API, CDN, Flutter web and apex Federation identities, private readiness/metrics isolation, and anonymous private-media denial.

If the www cutover fails and Pages previously owned www, the recovery step removes only the exact www Worker route before restoring and verifying the previous Pages association. The previous Worker deployment split is captured for an intentional manual rollback of API/apex routing. The workflow does not automatically roll back the Worker to an older version that may not recognize the apex route.

The Cloudflare API token needs Zone read, DNS read, Worker Scripts read/write, Worker Routes read/write, Pages read/write, Worker custom-domain read/write and Workers R2 Storage read/write permissions. If any ownership cannot be enumerated or the token lacks permission, the workflow must stop before changing the hostname.

The R2 domain transfer affects routing only. It does not change bucket contents, object keys, credentials, lifecycle rules or private-media authorization.

## Security boundaries

- Public `/healthz` identifies the expected service.
- `/readyz` and `/metrics` remain private and must return 404 through public API/media hostnames.
- Anonymous private-media requests must be denied (401, 403 or 404).
- The Worker rejects unknown hostnames and unsafe upstream origins.
- The Worker must not expose InstaBox control-plane URLs or internal listener ports in redirects.
- The Go CDN origin credential, Backend viewer-assertion signing secret and CDN purge credential are separate server-side secrets. Never put them in Flutter, browser bundles, URLs, source control or logs.
- Private media responses remain `Cache-Control: private, no-store`; the gateway must not cache viewer assertions or private bytes.

## Release acceptance

A successful Worker script upload alone is not a production release. Acceptance requires external probes against all four canonical hostnames, the exact service identities, private-path isolation and confirmation that the media hostname is owned by the Go CDN gateway rather than an R2 bucket. The web fallback must also be ready to restore Pages ownership if www verification fails.
