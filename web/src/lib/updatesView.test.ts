// The derived values both update screens show (the Updates page and About): a machine's
// last update that completed (a rolled-back run never is), what happens next with a time
// that has its units, and one status word for each machine.
import { describe, it, expect } from "vitest";
import en from "../i18n/en.json";
import {
  dayText,
  homeNext,
  inText,
  lastUpdateHow,
  lastUpdateOf,
  machineNext,
  machineStatus,
  releaseMigrates,
  safeReleaseUrl,
  spanText,
  windowEnds,
} from "./updatesView";
import type { UpdateAttempt, UpdateMachine, UpdatesOverview } from "./updatesApi";

/** en.json's t(), as the app's (values in braces). */
const t = (key: string, vars: Record<string, string | number> = {}) =>
  ((en as Record<string, string>)[key] ?? key).replace(/\{(\w+)\}/g, (_, k: string) =>
    String(vars[k] ?? `{${k}}`)
  );
const say = (w: { key: string; vars: Record<string, string | number> } | null) =>
  w ? t(w.key, w.vars) : "";

const NOW = Date.parse("2026-10-28T04:00:00Z");

function attempt(over: Partial<UpdateAttempt>): UpdateAttempt {
  return {
    id: "a",
    at: "2026-10-28T01:30:27Z",
    source: "server",
    to: "0.3.37",
    outcome: "installed",
    ...over,
  };
}

/** The owner's cluster on 2026-10-28: a run to 0.3.36 rolled back, then 0.3.37 installed by
 * hand on the home; the PC on 0.3.33, behind, caught up in the next quiet window. */
function owners(over: Partial<UpdatesOverview> = {}): UpdatesOverview {
  const home: UpdateMachine = {
    id: "home",
    name: "home",
    role: "home",
    version: "0.3.37",
    state: "up_to_date",
    attempts: [
      attempt({ id: "start-0.3.37", from: "0.3.35", to: "0.3.37", trigger: "start" }),
      attempt({ id: "start-0.3.36", at: "2026-10-27T21:09:10Z", to: "0.3.36", outcome: "failed" }),
      attempt({
        id: "run-b3cc",
        at: "2026-10-27T20:59:10Z",
        source: "run",
        trigger: "manual",
        from: "0.3.35",
        to: "0.3.36",
        outcome: "rolled_back",
        code: "failed_tv",
        run_id: "b3cc0bef-82e4-4d6e-bdda-6b3849570889",
      }),
    ],
  };
  const pc: UpdateMachine = {
    id: "1e943eda-0064-42e9-8692-851ebab36f54",
    name: "windows-pc",
    role: "node",
    version: "0.3.33",
    state: "behind",
    platform: "windows-amd64",
    kind: "appbundle",
    attempts: [
      attempt({
        id: "run-39cd",
        at: "2026-10-25T20:39:07Z",
        source: "run",
        from: "0.3.13",
        to: "0.3.16",
        outcome: "cancelled",
      }),
    ],
    next: { version: "0.3.37", reason: "catch_up_window", at: "2026-10-28T07:00:00Z" },
  };
  return {
    current_version: "0.3.37",
    watermark: "0.3.37",
    policy: {
      auto_install: true,
      channel: "stable",
      window: "03:00-05:00",
      window_overrun_minutes: 120,
      min_release_age_hours: 24,
      pinned_version: "",
      deferred_until: null,
      skipped_versions: [],
      node_failure_policy: "hold_platform",
      keep_versions: 3,
    },
    auto_install_available: true,
    plan: {
      held: false,
      stepping_stone: false,
      auto_install: true,
      due: false,
      waiting: "up_to_date",
    },
    last_run: {
      id: "b3cc0bef-82e4-4d6e-bdda-6b3849570889",
      version: "0.3.36",
      from_version: "0.3.35",
      trigger: "manual",
      state: "rolled_back",
      step: "tv",
      started_at: "2026-10-27T20:59:10Z",
      finished_at: "2026-10-28T00:02:22Z",
    },
    catalog: [],
    check_stale: false,
    time_zone: "Local",
    machines: [home, pc],
    ...over,
  };
}

describe("last update", () => {
  it("is the newest installed attempt: a rolled-back run's target never is", () => {
    const [home, pc] = owners().machines!;
    const last = lastUpdateOf(home!);
    expect(last?.to).toBe("0.3.37");
    // installed by hand, not "the last update, to 0.3.36"
    expect(lastUpdateHow(last!, home!, owners())).toBe("server");
    // a machine whose only attempt was cancelled has none
    expect(lastUpdateOf(pc!)).toBeNull();
    expect(
      lastUpdateOf({
        attempts: [attempt({ outcome: "rolled_back" }), attempt({ outcome: "put_back" })],
      })
    ).toBeNull();
  });

  it("takes the server's last_update first, and never a failed one it might send", () => {
    const run = attempt({ source: "run", to: "0.3.30", at: "2026-10-01T00:00:00Z" });
    expect(lastUpdateOf({ attempts: [], last_update: run })?.to).toBe("0.3.30");
    expect(
      lastUpdateOf({ attempts: [], last_update: { ...run, outcome: "rolled_back" } })
    ).toBeNull();
  });

  it("a release's first start during, or after, a run that brought it is the run's", () => {
    const start = attempt({ to: "0.3.38" });
    const ov = owners({
      run: {
        ...owners().last_run!,
        version: "0.3.38",
        state: "home_applying",
        finished_at: undefined,
      },
    });
    expect(lastUpdateHow(start, { attempts: [] }, ov)).toBe("run");
    const ran = attempt({ source: "run", to: "0.3.38", outcome: "installed" });
    expect(lastUpdateHow(start, { attempts: [ran] }, owners())).toBe("run");
    expect(lastUpdateHow(attempt({ source: "app" }), { attempts: [] }, owners())).toBe("app");
  });
});

describe("what happens next", () => {
  it("the home: up to date, or the release's day, window and time left, with units", () => {
    expect(say(homeNext(owners(), NOW, "en", t))).toBe("Nothing to install: up to date.");
    const ov = owners({
      plan: {
        target: { version: "0.3.38", tag: "v0.3.38", channel: "stable", verify_state: "verified" },
        held: false,
        stepping_stone: false,
        auto_install: true,
        due: false,
        next_run_at: "2026-10-28T07:00:00Z",
      },
    });
    const text = say(homeNext(ov, NOW, "en", t));
    expect(text).toMatch(/^0\.3\.38 installs .+ between 03:00 and 05:00 \(in 3 hours\)\.$/);
    // never a bare number ("in about 33")
    expect(text).not.toMatch(/about \d+\b(?! (minutes?|hours?|days?))/);
  });

  it("the home during a run: updating to the run's release", () => {
    const ov = owners({
      run: {
        ...owners().last_run!,
        version: "0.3.38",
        state: "home_applying",
        finished_at: undefined,
      },
    });
    expect(say(homeNext(ov, NOW, "en", t))).toBe("Updating to 0.3.38 now.");
  });

  it("the PC behind: when it catches up to the home's release, as the server decided it", () => {
    const ov = owners();
    const pc = ov.machines![1]!;
    expect(machineStatus(pc, ov)).toBe("behind");
    expect(say(machineNext(pc, ov, NOW, "en", t))).toMatch(
      /^0\.3\.37 installs .+ between 03:00 and 05:00 \(in 3 hours\)\.$/
    );
    expect(
      say(
        machineNext({ ...pc, next: { version: "0.3.37", reason: "held_back" } }, ov, NOW, "en", t)
      )
    ).toBe("0.3.37 failed on this machine: it waits for a newer release, or Retry.");
    expect(
      say(
        machineNext(
          {
            ...pc,
            next: { version: "0.3.37", reason: "catch_up_retry", at: "2026-10-28T09:30:00Z" },
          },
          ov,
          NOW,
          "en",
          t
        )
      )
    ).toBe("Tries 0.3.37 again in 5 hours 30 minutes.");
    expect(
      say(machineNext({ ...pc, next: { version: "0.3.38", reason: "in_run" } }, ov, NOW, "en", t))
    ).toBe("Updates to 0.3.38 after the home, in this update.");
    // an older server sends no decision: still a sentence with the release, never a bare number
    expect(say(machineNext({ ...pc, next: undefined }, ov, NOW, "en", t))).toBe(
      "Catches up to 0.3.37 automatically."
    );
    // up to date: nothing
    expect(
      machineNext(
        { ...pc, version: "0.3.37", state: "up_to_date", next: undefined },
        ov,
        NOW,
        "en",
        t
      )
    ).toBeNull();
  });

  it("the home's status word: up to date even though a machine is behind; the machine's is behind", () => {
    const ov = owners();
    expect(machineStatus(ov.machines![0]!, ov)).toBe("up_to_date");
    expect(machineStatus({ ...ov.machines![1]!, state: "offline" }, ov)).toBe("offline");
  });
});

describe("times and windows", () => {
  it("spans always carry their units", () => {
    expect(spanText(33 * 60_000, "en", t)).toBe("33 minutes");
    expect(spanText(60 * 60_000, "en", t)).toBe("1 hour");
    expect(spanText(135 * 60_000, "en", t)).toBe("2 hours 15 minutes");
    expect(spanText(26 * 3600_000, "en", t)).toBe("1 day 2 hours");
    expect(spanText(10_000, "en", t)).toBe("less than a minute");
    expect(inText("2026-10-28T04:33:00Z", NOW, "en", t)).toBe("in 33 minutes");
    expect(inText("2026-10-28T03:00:00Z", NOW, "en", t)).toBe("now");
    expect(spanText(33 * 60_000, "ar", t)).toMatch(/دقيقة/);
  });

  it("days and windows", () => {
    expect(dayText(new Date(NOW).toISOString(), NOW, "en", t)).toBe("today");
    expect(dayText(new Date(NOW + 86_400_000).toISOString(), NOW, "en", t)).toBe("tomorrow");
    expect(windowEnds("3:00-05:00")).toEqual(["03:00", "05:00"]);
    expect(windowEnds("bad")).toBeNull();
  });
});

describe("the release", () => {
  it("backs the database up first only when its schema is newer than the home's", () => {
    const cat = [
      {
        version: "0.3.37",
        tag: "v0.3.37",
        channel: "stable",
        verify_state: "verified" as const,
        manifest: { schema_rev: 164 },
      },
    ];
    const ov = owners({ catalog: cat });
    const target = (rev: number) => ({
      ...cat[0]!,
      version: "0.3.38",
      manifest: { schema_rev: rev },
    });
    expect(releaseMigrates(target(165), ov)).toBe(true);
    expect(releaseMigrates(target(164), ov)).toBe(false);
    expect(releaseMigrates(undefined, ov)).toBe(false);
  });

  it("links only http(s) release pages", () => {
    expect(safeReleaseUrl("https://github.com/x/releases/tag/v1")).toBe(
      "https://github.com/x/releases/tag/v1"
    );
    expect(safeReleaseUrl("javascript:alert(1)")).toBeNull();
    expect(safeReleaseUrl(42)).toBeNull();
  });
});
