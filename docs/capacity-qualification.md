# 100k-viewer capacity qualification

Status: **target and sizing model only; no live throughput qualification has passed.**

## The bandwidth requirement

At 100,000 simultaneous viewers and an average delivered media bitrate of 2 Mbps:

- Viewer egress: **200 Gbit/s** (25 GB/s).
- Payload volume: **90 TB per hour** (decimal units), before protocol/TLS overhead, retransmits, cache misses or operational headroom.
- A 125% burst target is **250 Gbit/s** and **112.5 TB/hour**.

The Go CDN's `TestCapacity1MLogicalViewers` is a useful single-process cache-coalescing regression test. It submits logical requests through `httptest`, warms one cache key and checks that the origin is fetched once. It does **not** open 100,000 real viewer sockets, send 200 Gbit/s, exercise TLS/network interfaces, measure distributed cache hit ratio, or prove multi-region delivery. Do not describe it as a 100k concurrent network capacity test.

## Fleet sizing model

Size against **measured sustained per-node egress**, not a provider's advertised NIC speed:

`active_nodes = ceil(required_viewer_egress / (measured_node_egress * max_utilization))`

For illustration only, if a deployed edge sustains 20 Gbit/s under the real TLS/cache workload and the fleet is capped at 70% utilization:

- Per-node planned egress: 14 Gbit/s.
- Active nodes for 200 Gbit/s: 15.
- N+1 fleet: at least 16 nodes, with capacity to absorb one edge loss while remaining inside the utilization limit.

This is not a deployment recommendation until the 20 Gbit/s figure is measured. If actual sustainable per-node throughput is lower, the required fleet grows. The model is covered by `TestCapacityEnvelope100kViewersAt2Mbps` and `TestCapacityEnvelopeEdgeFleetSizingUsesMeasuredThroughput`.

## Required distributed qualification

Run from independent load generators in at least three regions, against a staging fleet with the same image digest, TLS termination, cache policy, shield topology and origin policy intended for production.

1. Warm-cache ramp: 10k, 25k, 50k, 75k and 100k viewers; hold 100k for 30 minutes.
2. Burst: 125k viewers for 5 minutes, then recover to 100k without manual cache clearing.
3. Mix: live HLS manifests and immutable segments, VOD byte ranges, public objects, private viewer-bound assertions, token expiry and authorization denials.
4. Cache states: cold start, warm steady state, shield hit, shield miss and controlled origin slowdown.
5. Failure: remove one edge at 50% and 100% load; then test shield failover and origin outage. No private stale fallback or authorization fail-open is allowed.
6. Record actual viewer egress per region and node, cache hit ratio, shield/origin egress, request rate, p50/p95/p99 TTFB, segment completion time, 4xx/5xx/timeouts, CPU, RSS, NIC utilization, open sockets, retransmits, packet drops and connection churn.
7. Repeat with the actual video bitrate distribution. A 2 Mbps average must be measured from delivered bytes and playback duration, not assumed from test configuration.

## Release gate

Do not claim 100k-viewer readiness until the test report includes:

- exact source commit and immutable image digest;
- load-generator regions and generator saturation checks;
- measured concurrent viewers and delivered bitrate distribution;
- sustained egress and per-node utilization;
- cache/shield/origin breakdown and latency/error percentiles;
- N+1 edge-loss result and private-media authorization result;
- run duration, raw metrics, alerts and rollback record.

The GitHub Actions logical fan-out test remains a fast regression test. It is not a substitute for this network qualification. No production DNS, edge fleet, Cloudflare Worker route or origin configuration is changed by this document or its unit tests.
