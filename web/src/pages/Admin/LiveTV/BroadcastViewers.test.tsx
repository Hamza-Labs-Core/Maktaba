// The Broadcast panel's viewers (Epic 50 story 50.28): hidden while the home isn't claimed; an
// empty period says no one has watched yet, with the privacy note; the numbers show viewers now
// and viewer-hours today, over 7 days and this month against the allowance; countries and devices
// come as the cloud folded them, Other kept and nothing unfolded; Pro's guest sharing shows hours
// only; a cloud that can't be reached says so; the period asks again; Arabic renders.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "../../../lib/i18n";

const { get } = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock("../../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../../lib/api")>("../../../lib/api");
  return { ...actual, api: { ...actual.api, get } };
});

import { BroadcastViewers } from "./BroadcastViewers";
import type { ViewerReport, ViewersView } from "../../../lib/tvBroadcastApi";

const report = (over: Partial<ViewerReport> = {}): ViewerReport => ({
  level: "full",
  time_zone: "Africa/Cairo",
  k: 5,
  live_viewers: 3,
  hours: { today: 10, week: 40.5, month: 90, allowance: 5000 },
  period_hours: 10,
  peak: 11,
  sessions: 14,
  avg_watch_minutes: 42.6,
  countries: [
    { key: "EG", value: 12 },
    { key: "other", value: 2 },
  ],
  devices: [
    { key: "web", value: 9 },
    { key: "tvos", value: 5 },
    { key: "other", value: 1 },
  ],
  ...over,
});

const ok = (r: ViewerReport): ViewersView => ({ cloud: "ok", report: r });

function renderViewers() {
  return render(
    <I18nProvider>
      <BroadcastViewers />
    </I18nProvider>
  );
}

beforeEach(() => {
  get.mockReset();
  localStorage.clear();
  document.documentElement.lang = "en";
});

describe("BroadcastViewers", () => {
  it("shows nothing while the home isn't claimed", async () => {
    get.mockResolvedValue({ cloud: "unclaimed" });
    const { container } = renderViewers();
    await waitFor(() => expect(get).toHaveBeenCalled());
    expect(container).toBeEmptyDOMElement();
  });

  it("says so when no one has watched, with the privacy note", async () => {
    get.mockResolvedValue(
      ok(
        report({
          live_viewers: 0,
          period_hours: 0,
          hours: { today: 0, week: 0, month: 0, allowance: 50 },
          peak: 0,
          sessions: 0,
          avg_watch_minutes: 0,
          countries: [],
          devices: [],
        })
      )
    );
    renderViewers();
    expect(await screen.findByTestId("broadcast-viewers-empty")).toHaveTextContent(
      "No one has watched your channels in this period yet."
    );
    expect(screen.getAllByText("Nothing yet.")).toHaveLength(2);
    expect(screen.getByText(/grouped as "Other" for that hour/)).toBeInTheDocument();
  });

  it("shows viewers now and viewer-hours against the allowance", async () => {
    get.mockResolvedValue(ok(report()));
    renderViewers();
    const section = await screen.findByTestId("broadcast-viewers");
    expect(get).toHaveBeenCalledWith(
      expect.stringMatching(/^\/api\/tv\/broadcast\/analytics\?period=today&tz=/)
    );
    const live = within(section).getByText("Watching now").nextElementSibling;
    expect(live).toHaveTextContent("3");
    expect(
      within(section).getByText("Last 7 days", { selector: "dt" }).nextElementSibling
    ).toHaveTextContent("40.5");
    expect(within(section).getByText("90 of 5,000")).toBeInTheDocument();
    expect(
      within(section).getByRole("progressbar", { name: "This month's viewer-hours" })
    ).toHaveAttribute("aria-valuenow", "2");
    expect(within(section).getByText("Most at once").nextElementSibling).toHaveTextContent("11");
    expect(within(section).getByText("43 min")).toBeInTheDocument();
    expect(screen.queryByTestId("broadcast-viewers-empty")).not.toBeInTheDocument();
  });

  it("keeps the cloud's Other and names nothing it folded", async () => {
    get.mockResolvedValue(ok(report()));
    renderViewers();
    const section = await screen.findByTestId("broadcast-viewers");
    const rows = within(section)
      .getAllByRole("row")
      .map((r) => r.textContent);
    expect(rows).toEqual(["Egypt12", "Other2", "Web9", "Apple TV5", "Other1"]);
    expect(within(section).queryByText(/Germany/)).not.toBeInTheDocument();
    expect(within(section).getByText(/fewer than 5 viewers in an hour/)).toBeInTheDocument();
  });

  it("shows hours only on Pro's guest sharing", async () => {
    get.mockResolvedValue(
      ok({
        level: "hours",
        time_zone: "UTC",
        k: 5,
        live_viewers: 1,
        hours: { today: 2, week: 3, month: 4, allowance: 50 },
        period_hours: 2,
      })
    );
    renderViewers();
    expect(
      await screen.findByText("Countries and devices come with the Broadcaster add-on.")
    ).toBeInTheDocument();
    expect(screen.queryByText("Countries")).not.toBeInTheDocument();
    expect(screen.queryByText("Most at once")).not.toBeInTheDocument();
  });

  it("says when the cloud can't be reached", async () => {
    get.mockResolvedValue({ cloud: "unreachable" });
    renderViewers();
    expect(await screen.findByRole("status")).toHaveTextContent("Maktaba Cloud can't be reached");
  });

  it("asks again for another period", async () => {
    get.mockResolvedValue(ok(report()));
    renderViewers();
    await userEvent.selectOptions(await screen.findByLabelText("Period"), "7d");
    await waitFor(() => expect(get).toHaveBeenLastCalledWith(expect.stringContaining("period=7d")));
  });

  it("renders in Arabic", async () => {
    localStorage.setItem("mkt:locale", "ar");
    get.mockResolvedValue(ok(report()));
    renderViewers();
    expect(await screen.findByRole("heading", { name: "المشاهدون" })).toBeInTheDocument();
    expect(screen.getByText("يشاهدون الآن")).toBeInTheDocument();
    expect(screen.getAllByText("أخرى")).toHaveLength(2);
    expect(screen.getByText(/تحت "أخرى" في تلك الساعة/)).toBeInTheDocument();
    expect(screen.getByText("90 من 5,000")).toBeInTheDocument();
  });
});
