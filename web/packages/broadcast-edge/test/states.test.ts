// Story 50.27 §3: the guest states. The edge's viewerState and the apps' mappings (MaktabaKit's
// GuestViewerState, Android's) all read shared/broadcast/viewer-states.json, so a page and an
// app can never show different states for one answer.
import { describe, expect, it } from "vitest";
import { viewerState } from "../src/site/states";
import { STRINGS } from "../src/strings";
import { E } from "./helpers";

interface Case {
  status: number;
  body: { error?: string; channel_state?: string } | null;
  state: string;
  key: string;
  plays: boolean;
  retry: boolean;
}

const cases = (
  JSON.parse((E as unknown as { TEST_VIEWER_STATES: string }).TEST_VIEWER_STATES) as {
    cases: Case[];
  }
).cases;

describe("shared viewer-state vectors", () => {
  it("has the story's states", () => {
    const states = new Set(cases.map((c) => c.state));
    for (const s of [
      "live",
      "tuning",
      "offline",
      "off_air",
      "full",
      "geoBlocked",
      "ended",
      "inviteUsed",
      "inviteExpired",
      "revoked",
    ])
      expect(states.has(s), s).toBe(true);
  });

  it.each(cases.map((c) => [`${c.status} ${JSON.stringify(c.body)}`, c] as const))(
    "%s",
    (_name, c) => {
      expect(viewerState(c.status, c.body)).toEqual({
        state: c.state,
        key: c.key,
        plays: c.plays,
        retry: c.retry,
      });
      if (c.key) expect(STRINGS[c.key], c.key).toBeDefined();
    }
  );
});
