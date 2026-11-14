// T04 (story 58.07): the /r/ loader with builds 3 to 5. A sender at level 3 gets build 3, one
// at level 6 the newest at or below it (5), one at level 2 the "Update the home" screen. And
// the 12-month window the release keeps builds for.

import { describe, expect, it } from "vitest";
import {
  buildsOf,
  keptBuilds,
  pickBuild,
  senderLevel,
  withBuild,
  type Build,
} from "../src/loader/builds";

const b = (level: number, built: string): Build => ({ level, path: `/r/${level}/`, built });
const builds = [
  b(3, "2026-01-10T00:00:00Z"),
  b(4, "2026-04-01T00:00:00Z"),
  b(5, "2026-09-01T00:00:00Z"),
];

describe("pickBuild (T04)", () => {
  it("level 3 gets build 3", () => expect(pickBuild(builds, 3)?.level).toBe(3));
  it("level 6 gets the newest at or below it", () => expect(pickBuild(builds, 6)?.level).toBe(5));
  it("level 2 gets none: Update the home", () => expect(pickBuild(builds, 2)).toBeNull());
  it("level 4 gets build 4", () => expect(pickBuild(builds, 4)?.level).toBe(4));
  it("no builds: none", () => expect(pickBuild([], 9)).toBeNull());
});

describe("the manifest", () => {
  it("drops bad entries and sorts newest first", () => {
    expect(
      buildsOf({ builds: [b(3, "x"), { level: "4" }, null, b(5, "y")] }).map((x) => x.level)
    ).toEqual([5, 3]);
    expect(buildsOf(null)).toEqual([]);
    expect(buildsOf({ builds: "no" })).toEqual([]);
  });

  it("reads a sender without api_level as level 1", () => {
    expect(senderLevel(undefined)).toBe(1);
    expect(senderLevel(0)).toBe(1);
    expect(senderLevel(2.5)).toBe(1);
    expect(senderLevel(4)).toBe(4);
  });
});

describe("the 12-month window", () => {
  it("keeps a replaced build for 12 months after its replacement", () => {
    expect(keptBuilds(builds, new Date("2026-11-14T00:00:00Z")).map((x) => x.level)).toEqual([
      5, 4, 3,
    ]);
    // build 3 was replaced by 4 on 2026-04-01: gone after 2027-04-01
    expect(keptBuilds(builds, new Date("2027-04-02T00:00:00Z")).map((x) => x.level)).toEqual([
      5, 4,
    ]);
    // the newest build is always kept
    expect(keptBuilds(builds, new Date("2035-01-01T00:00:00Z")).map((x) => x.level)).toEqual([5]);
  });

  it("replaces a level's build with a newer one of the same level", () => {
    const fresh = { ...b(5, "2026-11-14T00:00:00Z"), hash: "new" };
    const out = withBuild(builds, fresh, new Date("2026-11-14T00:00:00Z"));
    expect(out.map((x) => x.level)).toEqual([5, 4, 3]);
    expect(out[0].hash).toBe("new");
  });
});
