# XClone media integration and 500k scaling

## Canonical data path

1. XClone uploads the object directly to the Cloudflare R2 `testagram-media` bucket using its presigned S3 URL.
2. XClone stores only the stable object key (`users/<user-id>/<uuid>.<ext>`) and the CDN URL in `media_assets`/`post_media`.
3. The CDN's `GET /v1/media/url?path=<object-key>` contract returns the canonical public URL.
4. Public media requests use `/v1/<object-key>` and are served by this Go CDN.
5. The Go CDN reads the object from the configured R2 public/custom-domain origin, caches it locally, coalesces concurrent misses, and serves stale data during short origin failures.

Cloudflare remains the public DNS/WAF/cache boundary. If the R2 bucket is exposed through a Cloudflare custom domain, set `R2_PUBLIC_BASE_URL` to that bucket domain and keep `PUBLIC_BASE_URL=https://media.testagram.site`. Cloudflare documents that R2 custom domains provide cached public access and that Smart Tiered Cache can reduce repeated R2 origin fetches. See the Cloudflare R2 public bucket and cache documentation.

## Host routing

`media.testagram.site/*` must terminate at the deployed Go CDN service (directly or through a Cloudflare Worker/route proxy). Do not leave the old R2-only Worker as the authoritative `/v1/*` origin, because that bypasses this repository's cache plane.

`cdn.testagram.site` may remain the control/health hostname. The application-facing URL contract is `media.testagram.site/v1/...`.

## 500k target

The repository now includes an opt-in 500,000 logical-viewer fan-out test. It proves request coalescing and cache fan-out for a single hot object; it is not a claim that one VM can transmit 500,000 real Internet streams.

Real 500k delivery requires Cloudflare/global edge caching plus multiple CDN nodes, adequate aggregate egress, origin shielding and measured SLOs. A 2 Mbps average bitrate at 500,000 viewers is approximately 1 Tbps of aggregate viewer egress, so capacity must be distributed across the edge network.

Promote capacity in stages: 1k -> 5k -> 10k -> 25k -> 50k -> 100k -> 250k -> 500k. Record p50/p95/p99 latency, error rate, cache hit ratio, origin requests, egress, CPU, memory, open connections and rebuffer rate at every stage.

## Required production configuration

- `PUBLIC_BASE_URL=https://media.testagram.site`
- `MEDIA_URL_PREFIX=/v1`
- `R2_PUBLIC_BASE_URL=<private-or-custom R2 delivery origin reachable by the CDN>`
- `ORIGIN_URLS=<one or more authenticated application/origin fallbacks>`
- `SHIELD_URLS=<shield CDN nodes>`
- `EDGE_URLS=<regional CDN nodes>`
- `TRUST_CLOUDFLARE=1` only when the service is reachable exclusively through trusted Cloudflare proxying
- `RATE_LIMIT_PER_MIN` and `RATE_LIMIT_BURST` sized for the deployed node and protected at the Cloudflare layer

Do not put R2 access keys in XClone client code or in media URLs.