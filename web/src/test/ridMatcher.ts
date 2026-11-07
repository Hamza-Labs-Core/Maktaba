import { expect } from "vitest";

/**
 * A playlist URL as the player loads it: the given URL plus the session-open request's id as
 * `rid` (Epic 53 story 53.08). Matches `url?rid=<uuid>` or `url&rid=<uuid>`.
 */
export function withRid(url: string): ReturnType<typeof expect.stringMatching> {
  const esc = url.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  return expect.stringMatching(
    new RegExp(
      `^${esc}[?&]rid=[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`
    )
  );
}
