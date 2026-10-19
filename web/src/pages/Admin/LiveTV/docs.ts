// The operator doc of Epic 32 (story 32.12): docs/tv-cluster-and-remote.md,
// one heading per section so the Live TV page links straight to it.
export const DOC_URL =
  "https://github.com/Hamza-Labs-Core/Maktaba/blob/main/docs/tv-cluster-and-remote.md";

/** The doc's anchors the Live TV page links to (plan 32.12 §1). */
export const DOC_SECTIONS = {
  streamingHosts: "streaming-hosts",
  placement: "placement",
  failover: "failover",
  cloudflare: "cloudflare",
  costs: "costs",
  remoteViewers: "remote-viewers",
  failures: "failures",
  plex: "plex-remote",
} as const;

export const docLink = (section: keyof typeof DOC_SECTIONS) =>
  `${DOC_URL}#${DOC_SECTIONS[section]}`;
