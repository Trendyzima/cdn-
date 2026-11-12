# CDN media authorization contract

The Backend is the only authority for media ownership, visibility, moderation and viewer access. A private object is never public because a signing secret or token is absent.

## Private requests
1. The client obtains a short-lived assertion from authenticated Backend `POST /api/media/cdn-download-url`.
2. Backend checks the exact canonical asset row is owned by the viewer, private and ready.
3. Backend returns a stable CDN URL and a separate `Authorization: Bearer <assertion>` value.
4. The assertion uses `v1.<base64url-json>.<base64url-hmac-sha256>`, signed with dedicated `CDN_MEDIA_ASSERTION_SECRET`.
5. Claims are `v=v1`, `iss=testagram-backend`, `aud=testagram-cdn`, `sub` viewer ID, `key` exact canonical object key, `visibility=private`, `iat`, `exp`, and random `jti`.
6. Worker validates HMAC, issuer, audience, viewer/key binding, private visibility and expiry before reading R2. Private responses are always `private, no-store`.

Do not send assertions in query strings, referrers or analytics. Legacy `?token=` URLs are rejected. Never reuse Appwrite, storage or origin-service credentials as the assertion secret.

## Public media
Public delivery remains a separate path. A missing assertion is not proof that an object is public. Until a distinct public namespace and authoritative visibility metadata are wired, this Worker serves private objects only and rejects requests without a valid Backend assertion.

## Go CDN
The Go CDN forwards the bearer value to the trusted origin as viewer authorization and bypasses cache for authenticated requests. The origin must validate the same assertion and exact asset row; it must not treat the assertion as an Appwrite session. Origin service authentication remains a separate credential.

## Rollout
Configure `CDN_MEDIA_ASSERTION_SECRET` in Backend and Worker only after both sides are deployed with the same secret. Keep legacy Backend direct-storage downloads available until Flutter clients send assertion headers for image, video, HLS manifest, segment and key requests. Do not merge production enforcement before the Backend trusted-origin verifier, Go CDN integration, Flutter propagation and physical end-to-end tests pass.
