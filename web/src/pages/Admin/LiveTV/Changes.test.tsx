// Story 33.11 TC1 / TC4: the Changes page - the list (state chips, kinds, source,
// author, checks summary), the filters, the slot bar, the in-flight banner, the staging
// settings, staging off (EC2), the release history, live updates in place, en + ar.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { I18nProvider } from "../../../lib/i18n";

const { get, post, patch } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), patch: vi.fn() }));
vi.mock("../../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../../lib/api")>("../../../lib/api");
  return { ...actual, api: { ...actual.api, get, post, patch } };
});
vi.mock("../../../lib/auth", () => ({
  useAuth: () => ({
    user: { id: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", username: "mahmoud", is_admin: true },
  }),
}));

import { AdminTvChanges, matches } from "./Changes";
import { answer, changeList } from "./changeFixtures";
import { reconcileList } from "../../../lib/tvChanges";
import en from "../../../i18n/en.json";
import ar from "../../../i18n/ar.json";

function renderPage(locale: "en" | "ar" = "en") {
  window.localStorage.setItem("mkt:locale", locale);
  return render(
    <I18nProvider>
      <MemoryRouter>
        <AdminTvChanges />
      </MemoryRouter>
    </I18nProvider>
  );
}

beforeEach(() => {
  get.mockReset();
  get.mockImplementation(async (path: string) => answer(path));
});
afterEach(() => {
  window.localStorage.removeItem("mkt:locale");
  vi.useRealTimers();
});

describe("Changes page", () => {
  it("lists the changes with state, kinds, source, author and checks, newest first", async () => {
    renderPage();
    const row = await screen.findByTestId("change-42");
    expect(within(row).getByText("Ramadan grid and the Seerah channel")).toBeInTheDocument();
    expect(within(row).getByText("Ready")).toBeInTheDocument();
    expect(within(row).getByText("Lineup")).toBeInTheDocument();
    expect(within(row).getByText("Channel editor")).toBeInTheDocument();
    expect(within(row).getByText(/mahmoud/)).toBeInTheDocument();
    expect(within(row).getByText("3 pass")).toBeInTheDocument();
    expect(within(row).getByText("1 warn")).toBeInTheDocument();
    const rows = screen.getAllByTestId(/^change-\d+$/).map((r) => r.dataset.testid);
    expect(rows).toEqual(["change-43", "change-42", "change-41", "change-40"]);
    // the source of a pipeline change, a failed one
    expect(within(screen.getByTestId("change-41")).getByText("Channel plan")).toBeInTheDocument();
    expect(within(screen.getByTestId("change-40")).getByText("Failed")).toBeInTheDocument();
  });

  it("filters by state group, the counts beside each filter", async () => {
    renderPage();
    await screen.findByTestId("change-42");
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: /Drafts/ }));
    expect(screen.getAllByTestId(/^change-\d+$/).map((r) => r.dataset.testid)).toEqual([
      "change-43",
    ]);
    await user.click(screen.getByRole("button", { name: /In flight/ }));
    expect(screen.getAllByTestId(/^change-\d+$/).map((r) => r.dataset.testid)).toEqual([
      "change-42",
    ]);
    await user.click(screen.getByRole("button", { name: /Ended/ }));
    expect(screen.getAllByTestId(/^change-\d+$/).map((r) => r.dataset.testid)).toEqual([
      "change-40",
    ]);
    expect(screen.getByRole("button", { name: /Ended/ })).toHaveAttribute("aria-pressed", "true");
    expect(matches("live", "live")).toBe(true);
    expect(matches("inflight", "draft")).toBe(false);
  });

  it("shows the slot bar, the change in flight and the staging settings", async () => {
    renderPage();
    const banner = await screen.findByTestId("in-flight");
    expect(banner).toHaveTextContent("Change #42 is Ready");
    expect(banner).toHaveAttribute("href", `/admin/live-tv/changes/${changeList.in_flight}`);
    expect(screen.getByTestId("slot-home-blue")).toHaveTextContent("LIVE");
    expect(screen.getByTestId("slot-staging")).toHaveTextContent("STAGING");
    const settings = screen.getByTestId("staging-settings");
    expect(settings).toHaveTextContent("03:00-05:00");
    expect(settings).toHaveTextContent("an admin approves");
    // the release history
    expect(screen.getByText("generation 6")).toBeInTheDocument();
  });

  it("explains staging off and keeps the release history only (EC2)", async () => {
    get.mockImplementation(async (path: string) =>
      path.startsWith("/api/tv/changes")
        ? { ...changeList, config: { ...changeList.config, enabled: false } }
        : answer(path)
    );
    renderPage();
    expect(await screen.findByTestId("staging-off")).toHaveTextContent("TV staging is off.");
    expect(screen.queryByTestId("change-42")).not.toBeInTheDocument();
    expect(screen.getByText("generation 6")).toBeInTheDocument();
  });

  it("re-reads every 2 s and keeps unchanged rows in place", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    renderPage();
    await screen.findByTestId("change-42");
    const before = screen.getByTestId("change-41");
    const calls = get.mock.calls.length;
    get.mockImplementation(async (path: string) =>
      path.startsWith("/api/tv/changes")
        ? {
            ...changeList,
            items: changeList.items.map((c) =>
              c.number === 42
                ? { ...c, state: "approved", allowed_actions: ["promote", "cancel", "schedule"] }
                : c
            ),
          }
        : answer(path)
    );
    await act(async () => {
      await vi.advanceTimersByTimeAsync(2100);
    });
    expect(get.mock.calls.length).toBeGreaterThan(calls);
    expect(within(screen.getByTestId("change-42")).getByText("Approved")).toBeInTheDocument();
    // the same DOM node: the unchanged row was not re-created
    expect(screen.getByTestId("change-41")).toBe(before);
  });

  it("reconcileList keeps the objects of unchanged items", () => {
    const a = changeList.items;
    const b = JSON.parse(JSON.stringify(a)) as typeof a;
    expect(reconcileList(a, b)).toBe(a);
    b[1] = { ...b[1]!, state: "approved" };
    const out = reconcileList(a, b);
    expect(out).not.toBe(a);
    expect(out[0]).toBe(a[0]);
    expect(out[1]).not.toBe(a[1]);
  });

  it("renders in Arabic, right to left", async () => {
    renderPage("ar");
    expect(
      await screen.findByRole("heading", { level: 1, name: "تغييرات البث" })
    ).toBeInTheDocument();
    expect(document.documentElement.getAttribute("dir")).toBe("rtl");
    expect(within(screen.getByTestId("change-42")).getByText("جاهز")).toBeInTheDocument();
  });
});

describe("i18n (TC4)", () => {
  it("every Changes page and notification key exists in English and Arabic", () => {
    const e = en as Record<string, string>;
    const a = ar as Record<string, string>;
    const ours = Object.keys(e).filter(
      (k) =>
        k.startsWith("tvchanges.") || k.startsWith("notify.") || k.startsWith("livetv.staging.")
    );
    expect(ours.length).toBeGreaterThan(200);
    for (const k of ours) expect(a[k], k).toBeTruthy();
    // the placeholders match
    const vars = (s: string) => (s.match(/\{[a-z]+\}/gi) ?? []).sort().join(",");
    for (const k of ours) expect(vars(a[k]!), k).toBe(vars(e[k]!));
  });
});
