# Canonical Testagram Cloudflare edge gateway

## Ownership

The Worker `testagram-cdn` in this repository is the public hostname gateway.
It forwards requests to Testagram-owned origins; it is not the media byte server,
does not authorize viewers and does not read R2 objects.

| Public hostname | Origin | Responsibility |
| --- | --- | --- |
| `api.testagram.site` | Backend, InstaBox port 8080 | Public Testagram API |
| `media.testagram.site` | Go CDN, InstaBox port 8081 | Authorized media byte delivery |
| `testagram.site` | Caddy edge, InstaBox port 8443 | WebFinger, ActivityPub and apex-to-www redirect |
| `www.testagram.site` | Caddy edge, InstaBox port 8443 | Flutter web container, selected by the Worker's trusted forwarded-host header |

The Caddy edge forwards `www.testagram.site` to the private `flutter-web`
container on the existing Docker edge network. The apex keeps its separate
Federation paths and redirects ordinary web requests to www. The API and media
origins remain separate and are not merged into the frontend container.

## Media and storage boundaries

- Backend owns media identity, upload authorization, visibility and deletion.
- The Go CDN is the only public media delivery plane.
- Private media requires Backend-issued, short-lived object-bound authorization;
  private responses are not shared-cacheable and must not be served stale.
- R2 and other object stores remain storage origins, not public media hostname
  owners after the Go CDN cutover.
- The Worker forwards requests and responses only; it does not read buckets or
  make viewer authorization decisions.

## Deployment

The guarded workflow `.github/workflows/restore-canonical-host-routing.yml`
is manually dispatched. It requires explicit confirmation and verified
InstaBox origins for Backend, Go CDN and the Caddy web/apex edge.

Before changing Cloudflare it verifies:
1. API and CDN service identities and that private readiness/metrics endpoints
   remain hidden from public hostnames;
2. apex Caddy identity, redirect and Federation WebFinger reachability;
3. the self-hosted Flutter web health endpoint and actual bootstrap document
   when routed as `www.testagram.site`;
4. exact Pages-domain ownership, Worker custom-domain/route ownership and
   existing proxied DNS records for all affected hostnames;
5. the current R2 custom-domain owner for `media.testagram.site`, without
   changing any bucket or object data.

Only after these checks does the workflow detach the exact apex and www
hostname associations from the expected Pages project, deploy the Worker
routes, and verify all four public hostnames. It does not delete the Pages
project or change DNS records.

If the www cutover fails and Pages previously owned www, the workflow removes
only the exact www Worker route and restores the previous Pages association.
API/apex Worker rollback remains a separately reviewed operator action; the
workflow captures the active deployment version split for that purpose.

## Secrets and permissions

GitHub Actions uses protected repository secrets:
- `CLOUDFLARE_API_TOKEN` and `CLOUDFLARE_ACCOUNT_ID`;
- `INSTABOX_API_KEY`, stored as the Worker secret used by request-triggered
  machine recovery.

The Cloudflare token needs zone read, DNS read, Worker scripts read/write,
Worker routes read/write, Pages read/write, Worker custom-domain read/write
and R2 custom-domain read/write permissions for the operations it performs.
The workflow fails closed when ownership or permissions cannot be verified.
Never put Cloudflare or InstaBox credentials in Flutter, browser bundles,
source control, public environment variables or logs.

## Validation

Run `cd cloudflare && npm test`. Tests cover hostname allowlisting, upstream
origin validation, canonical forwarding headers, private-path isolation,
safe redirects, recovery behavior and the www-to-Flutter origin mapping.

A successful script upload is not a production release. Release acceptance
requires the guarded workflow's external checks against all four canonical
hostnames and the exact service identities.
