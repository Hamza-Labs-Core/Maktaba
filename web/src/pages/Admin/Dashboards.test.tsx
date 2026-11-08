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

  it("with many machines it says how many it shows, and picks one machine on its own", async () => {
    const all = Array.from({ length: 37 }, (_, i) => `n${String(i).padStart(2, "0")}`);
    get.mockImplementation((path: string) =>
      Promise.resolve({
        board: "overview",
        range: "24h",
        step: 60,
        machine: path.includes("machine=") ? "n07" : undefined,
        machines: {
          total: 37,
          shown: path.includes("machine=") ? ["n07"] : ["n07", "n33", "n12", "n41", "n20"],
          all,
        },
        omitted: path.includes("machine=") ? 3 : 0,
        series: [
          { name: "maktaba_machines_components_down", kind: "gauge", points: [[1, 4]] },
          {
            name: "maktaba_component_down",
            labels: "machine=n07",
            kind: "gauge",
            points: [[1, 3]],
          },
        ],
      })
    );
    renderPage(<AdminDashboards />, { path: "/admin/dashboards" });
    const panels = await screen.findAllByTestId("dash-panel");
    expect(within(panels[0]).getByRole("heading")).toHaveTextContent(
      en["dash.series.maktaba_machines_components_down"]
    );
    expect(within(panels[1]).getByRole("heading")).toHaveTextContent(
      en["dash.series.maktaba_component_down"]
    );
    expect(screen.getByTestId("dash-machines-shown")).toHaveTextContent("Showing 5 of 37 machines");
    const pick = screen.getByLabelText(en["dash.machine"]) as HTMLSelectElement;
    expect(pick.options).toHaveLength(38);
    expect(pick.options[0].textContent).toBe(en["dash.machine.all"]);
    expect(screen.queryByTestId("dash-omitted")).toBeNull();
    fireEvent.change(pick, { target: { value: "n07" } });
    await waitFor(() =>
      expect(String(get.mock.calls.at(-1)?.[0])).toBe(
        "/api/admin/dashboards/overview?range=24h&machine=n07"
      )
    );
    expect(await screen.findByTestId("dash-omitted")).toHaveTextContent("3 more lines did not fit");
    expect(screen.queryByTestId("dash-machines-shown")).toBeNull();
    // the machine is the Overview's: another board is asked without it
    fireEvent.click(screen.getByRole("tab", { name: en["dash.storage"] }));
    await waitFor(() =>
      expect(String(get.mock.calls.at(-1)?.[0])).toBe("/api/admin/dashboards/storage?range=24h")
    );
    expect(screen.queryByTestId("dash-machine")).toBeNull();
  });

  it("in Arabic the machines line is Arabic", async () => {
    localStorage.setItem("mkt:locale", "ar");
    get.mockResolvedValue({
      board: "overview",
      range: "24h",
      step: 60,
      machines: {
        total: 9,
        shown: ["a", "b", "c", "d", "e"],
        all: ["a", "b", "c", "d", "e", "f", "g", "h", "i"],
      },
      series: [{ name: "maktaba_machines_load1_max", kind: "gauge", points: [[1, 2]] }],
    });
    renderPage(<AdminDashboards />, { path: "/admin/dashboards" });
    const panels = await screen.findAllByTestId("dash-panel");
    expect(within(panels[0]).getByRole("heading")).toHaveTextContent(
      ar["dash.series.maktaba_machines_load1_max"]
    );
    expect(screen.getByTestId("dash-machines-shown").textContent).toContain("عرض");
    expect(screen.getByLabelText(ar["dash.machine"])).toBeInTheDocument();
  });

  it("names the offline machines, marks them in the picker, and says so for one", async () => {
    get.mockImplementation((path: string) =>
      Promise.resolve({
        board: "overview",
        range: "24h",
        step: 60,
        machine: path.includes("machine=") ? "n45" : undefined,
        machines: {
          total: 9,
          shown: path.includes("machine=") ? ["n45"] : ["n45", "n70", "n07", "n33", "n12"],
          all: ["n07", "n12", "n20", "n33", "n41", "n45", "n50", "n60", "n70"],
          offline: ["n45", "n70"],
        },
        series: [{ name: "maktaba_machines_components_down", kind: "gauge", points: [[1, 4]] }],
      })
    );
    renderPage(<AdminDashboards />, { path: "/admin/dashboards" });
    expect(await screen.findByTestId("dash-offline")).toHaveTextContent("Offline: n45, n70");
    const pick = screen.getByLabelText(en["dash.machine"]) as HTMLSelectElement;
    const labels = Array.from(pick.options).map((o) => o.textContent);
    expect(labels).toContain("n45 (offline)");
    expect(labels).toContain("n07");
    fireEvent.change(pick, { target: { value: "n45" } });
    await waitFor(() =>
      expect(screen.getByTestId("dash-offline")).toHaveTextContent(en["dash.machine.offline"])
    );
    // a machine that is online, on its own: no offline line
    fireEvent.change(pick, { target: { value: "n07" } });
    await waitFor(() => expect(String(get.mock.calls.at(-1)?.[0])).toContain("machine=n07"));
    await screen.findAllByTestId("dash-panel");
    expect(screen.queryByTestId("dash-offline")).toBeNull();
  });

  it("with none offline there is no offline line, and an older home without the list reads as none", async () => {
    get.mockResolvedValue({
      board: "overview",
      range: "24h",
      step: 60,
      machines: { total: 2, shown: ["a", "b"], all: ["a", "b"] },
      series: [{ name: "maktaba_machines_load1_max", kind: "gauge", points: [[1, 2]] }],
    });
    renderPage(<AdminDashboards />, { path: "/admin/dashboards" });
    await screen.findAllByTestId("dash-panel");
    expect(screen.queryByTestId("dash-offline")).toBeNull();
  });

  it("in Arabic the offline line is Arabic", async () => {
    localStorage.setItem("mkt:locale", "ar");
    get.mockResolvedValue({
      board: "overview",
      range: "24h",
      step: 60,
      machines: { total: 2, shown: ["b", "a"], all: ["a", "b"], offline: ["b"] },
      series: [{ name: "maktaba_machines_load1_max", kind: "gauge", points: [[1, 2]] }],
    });
    renderPage(<AdminDashboards />, { path: "/admin/dashboards" });
    expect((await screen.findByTestId("dash-offline")).textContent).toContain("غير متصلة: b");
    const pick = screen.getByLabelText(ar["dash.machine"]) as HTMLSelectElement;
    expect(Array.from(pick.options).map((o) => o.textContent)).toContain("b (غير متصل)");
  });

  it("a viewer sees admins only, and no board is asked for", async () => {
    renderPage(<AdminDashboards />, { path: "/admin/dashboards", user: memberUser });
    expect(await screen.findByText(en["common.forbidden.title"])).toBeInTheDocument();
    expect(get).not.toHaveBeenCalled();
  });
});
