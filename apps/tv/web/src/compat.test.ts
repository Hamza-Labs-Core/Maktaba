// Story 58.09 T03 (58.01's TypeScript run): the launcher's decision on every shared case
// in shared/compat/vectors.json, the file the Go, Kotlin and Swift copies also run.
import vectors from "../../../../shared/compat/vectors.json";
import { compareVersions, decide, type CompatApp, type CompatHome, type Verdict } from "./compat";

interface Case {
  name: string;
  home: CompatHome;
  app: CompatApp;
  verdict: Verdict;
}

describe("the compatibility decision (shared/compat/vectors.json)", () => {
  const cases = (vectors as { cases: Case[] }).cases;

  it("has the shared cases", () => {
    expect(cases.length).toBeGreaterThanOrEqual(15);
  });

  it.each(cases.map((c) => [c.name, c] as const))("%s", (_, c) => {
    expect(decide(c.home, c.app)).toBe(c.verdict);
  });
});

describe("compareVersions", () => {
  it("compares by number, a missing part as 0, a suffix ending the number", () => {
    expect(compareVersions("1.10.0", "1.9.0")).toBe(1);
    expect(compareVersions("1.4", "1.4.0")).toBe(0);
    expect(compareVersions("1.3.9-beta", "1.4.0")).toBe(-1);
    expect(compareVersions("v1.2.0", "1.3.0")).toBe(-1);
    expect(compareVersions("dev", "1.0.0")).toBeNull();
  });
});
