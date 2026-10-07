# Scaling model

Viewer -> regional edge -> ephemeral HLS cache -> origin.

Optional WebRTC P2P can share HLS segments between viewers. P2P is never required for playback.

## Cost boundary

The repository software can remain open-source and cost-free. GitHub Actions can build/test a public repository without runner charges, but GitHub is not the video transport layer. Public Internet egress, compute and always-on nodes are not guaranteed free forever.

## Thousand-viewer strategy

The same live segment is fetched once per edge/origin path and then served to many viewers. P2P can reduce HTTP delivery further when peers are available.

Before claiming a viewer capacity, load-test bitrate, segment duration, cache-hit ratio, edge bandwidth, CPU, concurrent connections, origin request rate and P2P offload.

For production at large events use multiple origins and edge nodes, signed playback authorization, and an authenticated origin path.
