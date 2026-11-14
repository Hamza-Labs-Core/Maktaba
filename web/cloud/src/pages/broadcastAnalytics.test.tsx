// /homes/:id/viewers (Epic 50 story 50.28 §3, T08): a publisher's folded viewer analytics, in
// English and Arabic, with the cloud's 403 for anyone but the home's owner shown as such.
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { setAccessToken } from "../shared/api";
import { renderAt, session, stubFetch, user } from "../test/harness";
import en from "../i18n/en.json";
import ar from "../i18n/ar.json";
import { analyticsPath, type AnalyticsReport } from "./BroadcastAnalytics";

const HOME = "0a1b2c3d-0000-4000-8000-000000000000";

const FULL: AnalyticsReport = {
  level: "full",
  from: "2026-11-14T00:00:00Z",
  to: "2026-11-15T00:00:00Z",
  time_zone: "UTC",
  k: 5,
  live_viewers: 7,
  hours: { today: 6.7, week: 20, month: 120, allowance: 5000 },
  period_hours: 6.67,
  peak: 9,
  sessions: 14,
  avg_watch_minutes: 28.6,
  rebuffer_ratio: 0.0125,
  countries: [
    { key: "EG", value: 12 },
    { key: "other", value: 2 },
  ],
  devices: [{ key: "web", value: 14 }],
  quality: [{ key: "720p", value: 1 }],
  errors: [{ key: "storage_unavailable", value: 2 }],
  blocked: [{ key: "full", value: 1 }],
};

beforeEach(() => setAccessToken(null));
afterEach(() => {
  localStorage.clear();
});

describe("a home's viewers", () => {
  it("shows the folded counts: Egypt 12 and Other 2, no Germany", async () => {
    const f = stubFetch({
      "POST /v1/auth/refresh": session(user()),
      "GET /v1/broadcast/analytics": [200, FULL],
    });
    renderAt(`/homes/${HOME}/viewers`);
    expect(await screen.findByRole("heading", { name: "Viewers", level: 1 })).toBeInTheDocument();
    expect(await screen.findByText("Watching now")).toBeInTheDocument();
    const table = within(screen.getAllByRole("table")[0]);
    expect(table.getByText("Egypt")).toBeInTheDocument();
    expect(table.getByText("Other")).toBeInTheDocument();
    expect(screen.queryByText("Germany")).toBeNull();
    expect(screen.getByText(en["broadcast.analytics.privacy"])).toBeInTheDocument();
    expect(screen.getByText("Channel full")).toBeInTheDocument();
    const call = f.find("GET", "/v1/broadcast/analytics")[0];
    expect(call.path).toContain(`server=${HOME}`);
    expect(call.path).toContain("period=today");
    expect(call.path).toContain("tz=");
  });

  it("asks for another period", async () => {
    const f = stubFetch({
      "POST /v1/auth/refresh": session(user()),
      "GET /v1/broadcast/analytics": [200, FULL],
    });
    renderAt(`/homes/${HOME}/viewers`);
    await screen.findByText("Watching now");
    await userEvent.setup().selectOptions(screen.getByLabelText("Period"), "7d");
    await screen.findByText("Watching now");
    expect(f.find("GET", "/v1/broadcast/analytics").some((c) => c.path.includes("period=7d"))).toBe(
      true
    );
  });

  it("Pro's guest sharing sees hours, and what the Broadcaster add-on adds", async () => {
    stubFetch({
      "POST /v1/auth/refresh": session(user()),
      "GET /v1/broadcast/analytics": [
        200,
        {
          ...FULL,
          level: "hours",
          peak: undefined,
          countries: undefined,
          hours: { ...FULL.hours, allowance: 50 },
        },
      ],
    });
    renderAt(`/homes/${HOME}/viewers`);
    expect(await screen.findByText(en["broadcast.analytics.upgrade"])).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Countries" })).toBeNull();
    expect(screen.queryByText("Most at once")).toBeNull();
  });

  it("someone else's home: the cloud's 403 is shown, nothing else", async () => {
    stubFetch({
      "POST /v1/auth/refresh": session(user()),
      "GET /v1/broadcast/analytics": [403, { error: "broadcast_analytics_forbidden" }],
    });
    renderAt(`/homes/${HOME}/viewers`);
    expect(await screen.findByRole("alert")).toBeInTheDocument();
    expect(screen.queryByText("Watching now")).toBeNull();
  });

  it("reads in Arabic", async () => {
    localStorage.setItem("mkt:cloud:locale", "ar");
    stubFetch({
      "POST /v1/auth/refresh": session(user()),
      "GET /v1/broadcast/analytics": [200, FULL],
    });
    renderAt(`/homes/${HOME}/viewers`);
    expect(await screen.findByRole("heading", { name: "المشاهدون", level: 1 })).toBeInTheDocument();
    expect(screen.getByText("يشاهدون الآن")).toBeInTheDocument();
    expect(screen.getByText("أخرى")).toBeInTheDocument();
    expect(screen.getByText(ar["broadcast.analytics.privacy"])).toBeInTheDocument();
  });
});

describe("T08: the story's strings", () => {
  it("has every key the story names, in English and Arabic, with the story's text", () => {
    const story: Record<string, [string, string]> = {
      "broadcast.analytics.title": ["Viewers", "المشاهدون"],
      "broadcast.analytics.live": ["Watching now", "يشاهدون الآن"],
      "broadcast.analytics.peak": ["Most at once", "الأعلى في وقت واحد"],
      "broadcast.analytics.hours": ["Viewer-hours", "ساعات المشاهدة"],
      "broadcast.analytics.avgWatch": ["Average watch time", "متوسط مدة المشاهدة"],
      "broadcast.analytics.countries": ["Countries", "البلدان"],
      "broadcast.analytics.other": ["Other", "أخرى"],
      "broadcast.analytics.quality": ["Quality watched", "الجودة المشاهَدة"],
    };
    const e = en as Record<string, string>;
    const a = ar as Record<string, string>;
    for (const [k, [want, wantAr]] of Object.entries(story)) {
      expect([k, e[k]]).toEqual([k, want]);
      expect([k, a[k]]).toEqual([k, wantAr]);
    }
    expect(e["broadcast.analytics.privacy"]).toContain("fewer than 5");
    expect(a["broadcast.analytics.privacy"]).toContain("5");
  });

  it("builds the API path with the zone and the CSV format", () => {
    expect(analyticsPath(HOME, "month", "Africa/Cairo", "csv")).toBe(
      `/v1/broadcast/analytics?server=${HOME}&period=month&tz=Africa%2FCairo&format=csv`
    );
  });
});
