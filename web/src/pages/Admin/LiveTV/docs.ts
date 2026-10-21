// The Live TV page's guide links (story 32.12), now into the in-app help
// (Epic 41) instead of the operator doc on GitHub. help.coverage.test.ts
// checks each of these entries exists.
import { hrefOf, type HelpRef } from "../../../help/registry";

/** The help entries the Live TV page links to. */
export const DOC_SECTIONS = {
  streamingHosts: { topic: "live-tv-admin", anchor: "streaming-hosts" },
  placement: { topic: "live-tv-admin", anchor: "placement" },
  failover: { topic: "live-tv-admin", anchor: "failover" },
  cloudflare: { topic: "remote-tv", anchor: "set-up" },
  costs: { topic: "remote-tv", anchor: "budget" },
  remoteViewers: { topic: "remote-tv", anchor: "limits" },
  failures: { topic: "remote-tv", anchor: "troubleshooting" },
  plex: { topic: "plex", anchor: "live-tv" },
} as const satisfies Record<string, HelpRef>;

export const docLink = (section: keyof typeof DOC_SECTIONS) => hrefOf(DOC_SECTIONS[section]);
