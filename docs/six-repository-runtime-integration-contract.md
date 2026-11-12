# Testagram seven-repository runtime integration contract

Status: implementation and staging contract; not production cutover authorization.
Reviewed: 2026-11-13.

Each repository owns its domain. No repository may bypass Backend product
authorization or become a second source of truth. The shared `testagram-api`
repository owns versioned contracts, schemas, SDKs and immutable source pins;
it is not a runtime gateway.

## Service ownership

| Repository | Runtime responsibility | Allowed communication |
|---|---|---|
| `testagram-flutter-client-` | Flutter UI, navigation, local presentation and Appwrite session acquisition | Public authenticated Backend API and canonical public media URLs |
| `Testagram-backend-` | Canonical identity bridge, authorization, social records, media lifecycle, orchestration and outboxes | Appwrite Auth; private Algorithm, Data Centre and Federation APIs; CDN media URLs |
| `the-algorithm` | Stateless ranking of already-authorized bounded candidates | Private Backend ranking API only |
| `federated-content` | ActivityPub actors, keys, inbox/outbox, signature verification and remote delivery | Private Backend actor/outbox APIs; remote ActivityPub peers |
| `Testagram-data-centre-` | Storage placement, video processing, replication, repair, restore, VPS/runtime deployment and operations | Private Backend/platform network; storage providers and node agents |
| `cdn-` | Go media data plane: public caching, private assertion relay, HLS/DASH byte delivery, ranges, shields and origins | Public media requests; trusted Backend origin; configured public origins |
| `testagram-api` | Shared API contracts, schemas, SDKs, route inventory, immutable source pins and cross-repo CI | Build/contract-time integration only; no runtime API gateway |

## Canonical request paths

### Flutter -> Backend
- Flutter obtains an Appwrite session JWT and sends it as `Authorization: Bearer <JWT>`.
- Backend validates the session and derives canonical user identity server-side.
- Flutter must not call Algorithm, Federation, Data Centre platform APIs, PostgreSQL or storage-provider credentials directly.
- Client API base is configured through `TESTAGRAM_API_URL`; no private service URL or secret belongs in a Flutter build.

### Backend -> Algorithm
- Internal endpoint: `POST /v1/feed/rank`; readiness: `GET /readyz`.
- Backend sends `Authorization: Bearer <ALGORITHM_INTERNAL_TOKEN>`, API version and request ID.
- Algorithm may return only IDs from the submitted bounded candidate set. Backend rechecks current visibility, blocks, follows and moderation before hydrating posts.
- Current `cmd/api` uses `ALGORITHM_URL`; do not substitute `ALGORITHM_ENDPOINT` unless configuring a different runtime.

### Backend <-> Federation
- Backend owns local identity, actor eligibility and remote-follow authority.
- Use distinct credentials per direction: `FEDERATION_INTERNAL_TOKEN` for Backend -> Federation and `FEDERATION_BACKEND_INTERNAL_TOKEN` for Federation -> Backend.
- Publish activities through the private Backend/Federation contract with durable idempotency and request IDs.
- Federation must not create or mutate local follows without Backend authorization.
- Delivery/inbox/outbox activation requires signature, consent, replay, durability, idempotency and external-peer interoperability tests.

### Backend <-> Data Centre
- Data Centre is infrastructure/storage authority, not identity, social graph, visibility, media ownership, quota or retention authority.
- Backend media lifecycle calls must match implemented Data Centre endpoints and typed request/response schemas.
- Upload completion requires provider-side object verification and independently calculated SHA-256 read-back.
- Replica status must be backed by durable per-provider receipts; deletion and repair must be idempotent and auditable.
- Do not count CDN cache or a successful HTTP PUT as a durable replica.

### Backend -> Go CDN -> trusted Backend origin -> storage
- Backend owns upload intent, canonical object key, authorization, visibility, quota, retention and deletion.
- Backend returns stable media URLs under `https://media.testagram.site/v1/` only when the Go CDN hostname and origin are deployed.
- Public shared-cache requests use only the explicit `public/` namespace. The trusted origin must explicitly return `X-Testagram-Visibility: public`.
- Private Flutter requests send a short-lived Backend assertion in `Authorization: Bearer <assertion>`.
- Go CDN relays it to the trusted origin as `X-Testagram-Viewer-Authorization` and sends a separate service credential as `X-Testagram-Origin-Auth`. The Backend origin verifies both and checks the canonical asset row.
- Private requests bypass CDN cache, shield and public-origin fallback. Responses are `private, no-store`.
- Go CDN does not mint user-media or TV grants and does not make visibility decisions.
- Cloudflare R2 Worker source is currently deployed without a custom-domain route. It must not overlap `media.testagram.site`; activation requires a distinct hostname and reviewed API/Backend contract.

### TV/HLS
- Data Centre ingests and normalizes approved catalogue sources; it does not publish upstream credentials or raw private stream URLs to clients.
- Backend owns public catalogue DTOs and viewer-bound playback grants.
- CDN validates/relays only the approved Backend grant contract and preserves manifest, segment, range and expiry semantics.
- Flutter calls Backend only and plays the returned CDN URL.
- The current Go CDN `/v1/tv/*` legacy proxy is disabled (503) until all four layers are implemented and staging-tested.

## Shared reliability and security rules
- HTTPS for public endpoints; private-network HTTP only for explicitly private service hostnames.
- Distinct high-entropy service credentials per direction and audience; server-side secret stores only.
- Propagate request IDs; never log JWTs, service tokens, signed URLs, private content or authorization headers.
- Strict schemas, body limits, request deadlines, bounded concurrency and bounded retries.
- Retry only idempotent operations or those protected by idempotency keys.
- Preserve privacy, blocks, moderation and deletion tombstones across every projection and delivery path.
- Readiness must include required dependencies, not merely process liveness.
- Update producer, consumer, shared contract and fixtures together before changing a contract.
- API source pins must reference immutable commits and match the tested source checkout.

## Known integration gates
- Backend -> Data Centre media lifecycle endpoints and real provider operations require end-to-end proof.
- Backend private media origin must validate viewer assertions and CDN service authentication.
- CDN production hostname, trusted origin, secret names and private authorization require external smoke tests.
- TV catalogue, playback grant and manifest/segment path are not complete.
- Full seven-repository contract tests and deployment smoke tests must pass against the same immutable revisions before production promotion.

## Release acceptance
1. Verify each repository CI on the proposed integration revision.
2. Run producer/consumer contract tests for Flutter/Backend, Backend/Algorithm, Backend/Federation, Backend/Data Centre and Backend/CDN.
3. Verify DNS, TLS, private service networking, secrets, readiness and timeouts in staging.
4. Exercise authorization failures, malformed/oversized payloads, duplicate requests, upstream outage, retry and recovery.
5. Verify upload -> independent checksum read-back -> replica -> private/public CDN delivery -> deletion, including 401/403 denial and byte-range behavior.
6. Keep changes in reviewable PRs. Merge and deploy only after the release owner approves the verified set.
