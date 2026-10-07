// P2P acceleration adapter. HTTP edge/origin remains authoritative fallback.
// Pin a tested p2p-media-loader release during player integration.
export function p2pConfig(streamId) {
  return { streamId, enabled: true, fallback: "http", maxUploadPeers: 4, maxDownloadPeers: 8 };
}
