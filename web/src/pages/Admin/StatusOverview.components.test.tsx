// Epic 51 story 51.14: System health's Components line names the component by its title
// and its reason in short words, in both languages.
import { describe, expect, it } from "vitest";
import { itemReason } from "./StatusOverview";
import { lookupMessage, type Locale } from "../../lib/i18n";
import type { Fmt } from "../../components/queue/format";
import type { StatusItem } from "../../lib/systemStatus";

function tFor(locale: Locale) {
  return (key: string, vars?: Record<string, string | number>) => {
    const hit = lookupMessage(locale, key);
    if (hit === undefined) return key;
    return hit.replace(/\{(\w+)\}/g, (m, k) => (vars && k in vars ? String(vars[k]) : m));
  };
}

const failed: StatusItem = {
  id: "components",
  group: "services",
  kind: "components",
  name: "Components",
  status: "degraded",
  code: "components_failed",
  params: { failed: 1, component: "llm", why: "crash_loop", machine: "home" },
  reason: "1 failed: llm (crash loop).",
  attention: true,
  last_checked: new Date().toISOString(),
  help: "components",
};

const fmt = { dur: (s: number) => `${s}s` } as unknown as Fmt;
const n = (v: number) => String(v);

describe("the Components line", () => {
  it("reads in English", () => {
    const t = tFor("en");
    expect(itemReason(failed, t, fmt, n)).toBe(
      `1 failed: ${t("components.llm.title")} (crash loop).`
    );
    expect(itemReason({ ...failed, code: "components_ok", params: undefined }, t, fmt, n)).toBe(
      "All parts running."
    );
  });

  it("reads in Arabic", () => {
    const t = tFor("ar");
    const s = itemReason(failed, t, fmt, n);
    expect(s).toContain(t("components.llm.title"));
    expect(s).toContain("تعطّل متكرر");
    expect(s).not.toContain("crash");
  });

  it("keeps an unknown reason's code readable", () => {
    const t = tFor("en");
    expect(
      itemReason({ ...failed, params: { ...failed.params, why: "from_the_future" } }, t, fmt, n)
    ).toContain("(from the future)");
  });
});
