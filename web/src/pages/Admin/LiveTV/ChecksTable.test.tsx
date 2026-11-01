// A TV change check's value is said from its code and params (tvchanges.value.<code>),
// in the page's language; a code this web does not know, or a home before the codes,
// shows the home's English.
import { afterEach, describe, expect, it } from "vitest";
import { render, screen, within } from "@testing-library/react";
import { I18nProvider } from "../../../lib/i18n";
import type { VerifyReport } from "../../../lib/tvChanges";
import { ChecksTable } from "./ChecksTable";
import { checkValue } from "./changeBits";
import en from "../../../i18n/en.json";
import ar from "../../../i18n/ar.json";

const report: VerifyReport & { reasons?: string[] } = {
  passed: false,
  at: "2026-10-31T18:00:00Z",
  checks: [
    {
      check: "slot.health",
      result: "fail",
      code: "slot.release_mismatch",
      value: "the staging slot runs another release (release_mismatch)",
    },
    {
      check: "guide.coverage",
      channel_id: "c1",
      result: "warn",
      code: "guide.gaps",
      params: { gaps: "3" },
      value: "3 gaps in the next 24 h",
    },
    { check: "moon.phase", result: "pass", code: "moon.waxing", value: "the moon is waxing" },
    { check: "ts.conformity", result: "pass", value: "ok" },
  ],
  reasons: ["slot.health: the staging slot runs another release (release_mismatch)"],
};

function show() {
  return render(
    <I18nProvider>
      <ChecksTable report={report} names={{ c1: "Quran" }} />
    </I18nProvider>
  );
}

function valueOf(check: string): string {
  const row = screen.getAllByRole("row").find((r) => within(r).queryAllByText(check).length > 0);
  if (!row) throw new Error(`no row ${check}`);
  return within(row).getAllByRole("cell")[3].textContent ?? "";
}

describe("a TV check's value by its code", () => {
  afterEach(() => localStorage.clear());

  it("reads in English from its code", () => {
    show();
    expect(valueOf("slot.health")).toBe("The staging slot runs another release");
    expect(valueOf("guide.coverage")).toBe("3 gaps in the next 24 hours");
    expect(screen.getByTestId("checks-reasons")).toHaveTextContent(
      "Staging process: The staging slot runs another release"
    );
  });

  it("reads in Arabic from its code, and an unknown code shows the home's English", () => {
    localStorage.setItem("mkt:locale", "ar");
    show();
    expect(valueOf("slot.health")).toBe("خانة التجهيز تشغّل إصدارًا آخر");
    expect(valueOf("guide.coverage")).toBe("3 فجوات في الساعات الـ24 القادمة");
    expect(valueOf("moon.phase")).toBe("the moon is waxing");
    expect(valueOf("ts.conformity")).toBe("ok");
    expect(screen.getByTestId("checks-reasons")).toHaveTextContent(
      "عملية التجهيز: خانة التجهيز تشغّل إصدارًا آخر"
    );
  });

  it("falls back to the English value without a code or with an unknown one", () => {
    const t = (k: string) => k;
    expect(checkValue("ar", t, { value: "as sent" })).toBe("as sent");
    expect(checkValue("ar", t, { code: "brand.new", value: "as sent" })).toBe("as sent");
    expect(checkValue("ar", t, {})).toBe("");
  });

  it("every value code has its Arabic, with the same {params}", () => {
    const params = (s: string) => [...s.matchAll(/\{(\w+)\}/g)].map((m) => m[1]).sort();
    const enMsgs = en as Record<string, string>;
    const arMsgs = ar as Record<string, string>;
    const keys = Object.keys(enMsgs).filter((k) => k.startsWith("tvchanges.value."));
    expect(keys.length).toBeGreaterThan(40);
    for (const k of keys) {
      expect(arMsgs[k], k).toBeTruthy();
      expect(params(arMsgs[k]), k).toEqual(params(enMsgs[k]));
    }
  });
});
