// Epic 53 story 53.12 T06: the boards draw a panel per metric with a sentence for screen
// readers, the shared range asks again, Use lists the top videos, an empty board says so,
// and in Arabic the words are Arabic while the chart's time still runs left to right.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { renderPage, memberUser } from "../../test/pageHarness";
import en from "../../i18n/en.json";
import ar from "../../i18n/ar.json";

const { get } = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get } };
});

import { AdminDashboards } from "./Dashboards";

const OVERVIEW = {
  board: "overview",
  range: "24h",
  step: 60,
  series: [
    {
      name: "maktaba_component_up",
      labels: "id=core.api,machine=home",
      kind: "gauge",
      points: [
        [1, 1],
        [61, 0],
        [121, 1],
      ],
    },
    {
      name: "maktaba_component_up",
      labels: "id=core.streaming,machine=home",
      kind: "gauge",
      points: [
        [1, 1],
        [61, 1],
      ],
    },
    {
      name: "maktaba_errors_per_hour",
      labels: "component=core.api",
      kind: "gauge",
      points: [[3600, 4]],
    },
  ],
};

describe("Dashboards", () => {
  beforeEach(() => {
    get.mockImplementation((path: string) => {
      if (path.startsWith("/api/admin/dashboards/use"))
        return Promise.resolve({
          board: "use",
          range: "24h",
          step: 86400,
          series: [],
          top: [{ title: "Tafsir 1", plays: 3 }],
        });
      if (path.startsWith("/api/admin/dashboards/storage"))
        return Promise.resolve({ board: "storage", range: "24h", step: 60, series: [] });
      return Promise.resolve(OVERVIEW);
    });
  });
  afterEach(() => {
    get.mockReset();
    localStorage.clear();
    window.history.replaceState({}, "", "/");
  });

  it("draws a panel per metric, a line per label set, with its summary and Open in Logs", async () => {
    renderPage(<AdminDashboards />, { path: "/admin/dashboards" });
    const panels = await screen.findAllByTestId("dash-panel");
    expect(panels).toHaveLength(2);
    expect(String(get.mock.calls[0][0])).toBe("/api/admin/dashboards/overview?range=24h");
    const up = panels[0];
    expect(within(up).getByRole("heading")).toHaveTextContent(
      en["dash.series.maktaba_component_up"]
    );
    expect(up.querySelectorAll("polyline")).toHaveLength(2);
    expect(up.querySelector("figcaption")?.textContent).toContain("core.api · home 1");
    expect(
      within(up).getByRole("link", { name: en["dash.openLogs"] }).getAttribute("href")
    ).toContain("components=core.api");
    fireEvent.click(screen.getByRole("button", { name: en["dash.range.7d"] }));
    await waitFor(() =>
      expect(String(get.mock.calls.at(-1)?.[0])).toBe("/api/admin/dashboards/overview?range=7d")
    );
  });

  it("Use lists the most played videos; an empty board says there is no data yet", async () => {
    renderPage(<AdminDashboards />, { path: "/admin/dashboards" });
    await screen.findAllByTestId("dash-panel");
    fireEvent.click(screen.getByRole("tab", { name: en["dash.use"] }));
    expect(await screen.findByTestId("dash-top")).toHaveTextContent("Tafsir 1");
    fireEvent.click(screen.getByRole("tab", { name: en["dash.storage"] }));
    expect(await screen.findByTestId("dash-empty")).toHaveTextContent(en["dash.noData"]);
  });

  it("in Arabic the words are Arabic and the chart's time runs left to right", async () => {
    localStorage.setItem("mkt:locale", "ar");
    renderPage(<AdminDashboards />, { path: "/admin/dashboards" });
    const panels = await screen.findAllByTestId("dash-panel");
    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent(ar["dash.title"]);
    expect(within(panels[1]).getByRole("heading")).toHaveTextContent(
      ar["dash.series.maktaba_errors_per_hour"]
    );
    expect(panels[0].querySelector("figure")?.getAttribute("dir")).toBe("ltr");
  });

  it("a viewer sees admins only, and no board is asked for", async () => {
    renderPage(<AdminDashboards />, { path: "/admin/dashboards", user: memberUser });
    expect(await screen.findByText(en["common.forbidden.title"])).toBeInTheDocument();
    expect(get).not.toHaveBeenCalled();
  });
});
