# CDN media authorization contract

## No playback signing secret

The CDN does not mint, sign, parse, or validate playback tokens. The `PLAYBACK_SECRET` configuration field and `?token=` URL mechanism are removed. A private media URL is a stable URL, not a bearer capability.

## Private media request

1. The client obtains/holds its Appwrite session.
2. The client requests the stable CDN media URL with `Authorization: Bearer <Appwrite-session>`.
3. The CDN requires `ORIGIN_AUTH_TOKEN` and sends two separate headers to the authoritative origin:
   - `X-Testagram-Origin-Auth: <service credential>` authenticates the CDN service.
   - `X-Testagram-Viewer-Authorization: Bearer <Appwrite-session>` carries the viewer identity.
4. The origin validates the service credential, validates the viewer session with Appwrite, and checks the requested object's ACL.
5. The CDN streams the authorized response directly. It does not cache authenticated requests, use a shield hop, or fall back to Cloudinary/R2. It sends `Cache-Control: private, no-store`.

The origin must not trust `X-Testagram-Viewer-Authorization` from arbitrary public clients. It must be reachable only through the CDN or independently require the CDN service credential. The origin must never return a private object with a successful response unless the viewer ACL check has passed.

## Public media request

Requests without a viewer Authorization header are public candidates only. The authoritative origin must return `X-Testagram-Visibility: public` on a successful response before the CDN caches it. Missing, unknown, or private visibility is rejected. Cloudinary/R2 fallback is limited to paths beginning with `public/`; those configured vendor origins must contain public objects only.

If a public object's visibility is changed to private, purge all CDN/shield copies as part of that state transition. Do not rely on TTL expiry for revocation.

## Deployment

- Remove `PLAYBACK_SECRET` from CDN deployment secrets and environment configuration.
- Keep `ORIGIN_AUTH_TOKEN`; it is a separate service-to-service credential and is not a playback signing secret.
- Configure the same origin service credential on the CDN and authoritative origin.
- Implement origin validation for `X-Testagram-Origin-Auth`, viewer-session validation for `X-Testagram-Viewer-Authorization`, and the explicit public visibility response header.
- Do not expose the origin publicly in a way that permits bypassing the CDN's service credential.
- Update Flutter media playback to attach the current Appwrite session to private media requests. HLS players must support authenticated request headers for manifests and every segment/key request.

Until the origin and Flutter client implement this contract, private media playback is intentionally fail-closed. This PR does not claim cross-repository deployment completion.
