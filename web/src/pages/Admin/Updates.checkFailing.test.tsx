// A failed update check is not log-only (the incident of 2026-11-24: every home's check
// failed for over a day and nothing on screen said so). The Updates page says since when and
// why from two failures in a row, quietly after one; System health's Update check item says
// the same; both in English and Arabic.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";

const { get } = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post: vi.fn(), put: vi.fn() } };
});

import { I18nProvider, lookupMessage, type Locale } from "../../lib/i18n";
import { ToastProvider } from "@ds/components/Toast/Toast";
import { AdminUpdates } from "./Updates";
import { itemName, itemReason } from "./StatusOverview";
import type { Fmt } from "../../components/queue/format";
import type { StatusItem } from "../../lib/systemStatus";
import { SYSTEM_HEALTH_KINDS } from "../../help/registry";

const since = "2026-11-23T11:02:00Z";
const tooLarge =
  "GET https://api.github.com/repos/Hamza-Labs-Core/Maktaba/releases?per_page=30: larger than 8388608 bytes";

function overview(over: Record<string, unknown> = {}) {
  return {
    current_version: "0.3.157",
    watermark: "0.3.157",
    policy: { auto_install: true, channel: "stable", window: "03:00-05:00" },
    auto_install_available: true,
    plan: { held: false, stepping_stone: false, auto_install: true, due: false },
    catalog: [],
    check_stale: false,
    time_zone: "UTC",
    machines: [{ id: "home", name: "home", role: "home", version: "0.3.157", state: "up_to_date" }],
    ...over,
  };
}

function renderPage(ov: unknown, lang?: string) {
  if (lang) localStorage.setItem("mkt:locale", lang);
  get.mockImplementation((path: string) => {
    if (path === "/api/cluster/updates") return Promise.resolve(ov);
    if (path.startsWith("/api/cluster/updates/runs")) return Promise.resolve({ items: [] });
    return Promise.reject(new Error("unexpected " + path));
  });
  return render(
    <MemoryRouter>
      <I18nProvider>
        <ToastProvider>
          <AdminUpdates />
        </ToastProvider>
      </I18nProvider>
    </MemoryRouter>
  );
}

beforeEach(() => {
  get.mockReset();
  localStorage.clear();
});

describe("Updates: a check that keeps failing", () => {
  it("says since when and why from two failures in a row, with what GitHub answered", async () => {
    renderPage(
      overview({
        check_failures: 4,
        check_failing_since: since,
        last_check_code: "too_large",
        last_check_error: tooLarge,
      })
    );
    const box = await screen.findByTestId("updates-check-failing");
    expect(box).toHaveAttribute("role", "alert");
    expect(box.textContent).toContain("Can't check for updates since");
    expect(box.textContent).toContain(new Date(since).toLocaleString());
    expect(box.textContent).toContain(
      "4 checks in a row failed: GitHub's list of releases is larger than this version of Maktaba reads."
    );
    expect(box.textContent).toContain(tooLarge);
  });

  it("is a quiet line after one failure", async () => {
    renderPage(
      overview({ check_failures: 1, check_failing_since: since, last_check_code: "unreachable" })
    );
    const line = await screen.findByTestId("updates-check-failed-once");
    expect(line.textContent).toBe(
      "The last update check failed: GitHub could not be reached. It is tried again in 6 hours."
    );
    expect(screen.queryByTestId("updates-check-failing")).toBeNull();
  });

  it("says nothing while the checks work", async () => {
    renderPage(overview({ last_check_ok: since }));
    await screen.findByRole("heading", { name: "Updates" });
    expect(screen.queryByTestId("updates-check-failing")).toBeNull();
    expect(screen.queryByTestId("updates-check-failed-once")).toBeNull();
  });

  it("reads in Arabic", async () => {
    renderPage(
      overview({ check_failures: 2, check_failing_since: since, last_check_code: "rate_limited" }),
      "ar"
    );
    const box = await screen.findByTestId("updates-check-failing");
    expect(box.textContent).toContain("تعذّر التحقق من التحديثات منذ");
    expect(box.textContent).toContain("طلب GitHub من مكتبة أن تنتظر");
  });
});

function tFor(locale: Locale) {
  return (key: string, vars?: Record<string, string | number>) => {
    const hit = lookupMessage(locale, key);
    if (hit === undefined) return key;
    return hit.replace(/\{(\w+)\}/g, (m, k) => (vars && k in vars ? String(vars[k]) : m));
  };
}
const fmt = { dur: (s: number) => `${s}s` } as unknown as Fmt;
const n = (v: number) => String(v);
const date = (iso: string) => `<${iso}>`;

const item = (code: string, params?: Record<string, unknown>): StatusItem => ({
  id: "update_check",
  group: "integrations",
  kind: "update_check",
  name: "Update check",
  status: code === "update_check_failing" ? "degraded" : "ok",
  code,
  params,
  reason: "server English",
  last_checked: since,
  help: "update_check",
  link: "/admin/updates",
});

describe("System health: the update check", () => {
  const failing = item("update_check_failing", {
    since,
    failing_seconds: 93600,
    failures: 5,
    code: "too_large",
    detail: tooLarge,
  });

  it("is named, dated and worded in English", () => {
    const en = tFor("en");
    expect(itemName(failing, en)).toBe("Update check");
    expect(itemReason(failing, en, fmt, n, date)).toBe(
      `Can't check for updates since <${since}> (5 checks in a row failed): GitHub's list of releases is larger than this version of Maktaba reads.`
    );
    expect(itemReason(item("update_check_ok"), en, fmt, n, date)).toBe(
      "Updates are checked every 6 hours."
    );
    expect(
      itemReason(item("update_check_failed_once", { code: "http_error" }), en, fmt, n, date)
    ).toBe("The last update check failed: GitHub answered with an error. It is tried again in 6 hours.");
  });

  it("is named and worded in Arabic", () => {
    const ar = tFor("ar");
    expect(itemName(failing, ar)).toBe("التحقق من التحديثات");
    const s = itemReason(failing, ar, fmt, n, date);
    expect(s).toContain("تعذّر التحقق من التحديثات منذ");
    expect(s).toContain("قائمة الإصدارات على GitHub أكبر");
    expect(s).not.toContain("{");
  });

  it("has its help entry", () => {
    expect(SYSTEM_HEALTH_KINDS).toContain("update_check");
  });
});
