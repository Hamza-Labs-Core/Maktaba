// System health names two things the queues' own health does not show: the machine is
// overloaded (the host_load item) and workers that do not run their jobs (the Queues
// item). Both read in English and Arabic, and both have a Help entry.
import { describe, expect, it } from "vitest";
import { itemName, itemReason } from "./StatusOverview";
import { lookupMessage, type Locale } from "../../lib/i18n";
import type { Fmt } from "../../components/queue/format";
import type { StatusItem } from "../../lib/systemStatus";
import { SYSTEM_HEALTH_KINDS } from "../../help/registry";
import enHelp from "../../help/content/en/system-health.md?raw";
import arHelp from "../../help/content/ar/system-health.md?raw";

function tFor(locale: Locale) {
  return (key: string, vars?: Record<string, string | number>) => {
    const hit = lookupMessage(locale, key);
    if (hit === undefined) return key;
    return hit.replace(/\{(\w+)\}/g, (m, k) => (vars && k in vars ? String(vars[k]) : m));
  };
}
const fmt = { dur: (s: number) => `${s}s` } as unknown as Fmt;
const n = (v: number) => String(v);
const ARABIC = /[؀-ۿ]/;

const host = (code: string, params: Record<string, unknown>): StatusItem => ({
  id: "host_load",
  group: "services",
  kind: "host_load",
  name: "This machine's load",
  status: code === "host_load_ok" ? "ok" : "degraded",
  code,
  params,
  reason: "server English",
  last_checked: new Date().toISOString(),
  help: "host_load",
});
const queues = (code: string, params: Record<string, unknown>): StatusItem => ({
  ...host(code, params),
  id: "queues",
  group: "queues_tv",
  kind: "queues",
  name: "Queues",
  help: "queues",
});

describe("System health: this machine's load", () => {
  const params = {
    load1: 512.3,
    load5: 498,
    cores: 28,
    bound: 56,
    top: "runaway (795%), Some App (398%)",
  };

  it("is named and worded in English", () => {
    const en = tFor("en");
    expect(itemName(host("host_load_ok", params), en)).toBe("This machine's load");
    expect(itemReason(host("host_load_ok", { ...params, load1: 3.1 }), en, fmt, n)).toBe(
      "Load 3.1 on 28 cores."
    );
    expect(itemReason(host("host_overloaded", params), en, fmt, n)).toBe(
      "This machine is overloaded: load 512.3 on 28 cores. Jobs and the database are slow."
    );
    expect(itemReason(host("host_overloaded_top", params), en, fmt, n)).toBe(
      "This machine is overloaded: load 512.3 on 28 cores. Jobs and the database are slow. Using the most CPU: runaway (795%), Some App (398%)."
    );
  });

  it("is named and worded in Arabic", () => {
    const ar = tFor("ar");
    expect(itemName(host("host_load_ok", params), ar)).toMatch(ARABIC);
    for (const code of ["host_load_ok", "host_overloaded", "host_overloaded_top"]) {
      const s = itemReason(host(code, params), ar, fmt, n);
      expect(s, code).toMatch(ARABIC);
      expect(s, code).toContain("28");
      expect(s, code).not.toMatch(/overloaded|Load|cores|server English/);
    }
    expect(itemReason(host("host_overloaded_top", params), ar, fmt, n)).toContain("runaway (795%)");
  });

  it("has its Help entry in both languages", () => {
    expect(SYSTEM_HEALTH_KINDS).toContain("host_load");
    expect(enHelp).toContain("{#item-host-load}");
    expect(arHelp).toContain("{#item-host-load}");
  });
});

describe("System health: workers that do not run their jobs", () => {
  it("reads in English and Arabic", () => {
    const stalled = queues("queues_workers_stalled", {
      workers: 2,
      waiting: 40,
      kinds: "transcription",
    });
    expect(itemReason(stalled, tFor("en"), fmt, n)).toBe(
      "2 workers are not running their jobs; 40 jobs wait. Restart them on the Queues page."
    );
    const none = queues("queues_no_workers", { workers: 0, waiting: 6, kinds: "" });
    expect(itemReason(none, tFor("en"), fmt, n)).toBe("6 jobs wait and no worker is running.");
    for (const it of [stalled, none]) {
      const s = itemReason(it, tFor("ar"), fmt, n);
      expect(s).toMatch(ARABIC);
      expect(s).not.toMatch(/[A-Za-z]/);
    }
  });
});
