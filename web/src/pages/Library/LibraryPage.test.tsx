// Epic 45 — a library's page (`/library/:libraryId`): the hub's Overview
// rows, the Browse tab's grid with its filters in the URL and the request,
// Pin, the admin's Scan now / Manage, and the not-found state.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useLocation } from "react-router-dom";
import { ApiError } from "../../lib/api";
import { renderPage, memberUser, adminUser } from "../../test/pageHarness";
import { ToastProvider } from "@ds/components/Toast/Toast";
import en from "../../i18n/en.json";

const { get, put, post } = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), post: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, put, post } };
});

import { LibraryPage } from "./LibraryPage";
import { resetPinsStore } from "../../lib/pins";

const ID = "3f1c2a4e-0000-4000-8000-000000000001";
const card = (id: string, extra: Record<string, unknown> = {}) => ({
  id,
  title: `Video ${id}`,
  filename: `${id}.mp4`,
  duration_sec: 120,
  ...extra,
});

const HUB = {
  library: { id: ID, name: "Dr. Othman Alkamees", videos: 47273 },
  rows: {
    continue_watching: [card("c1", { position_sec: 30 })],
    recently_added: [card("a1"), card("a2")],
    recently_watched: [],
    most_watched: [card("m1", { plays: 12 })],
    series: [
      { id: "s1", name: "شرح الأصول", episodes: 30, poster_video_id: "v-first" },
      { id: "s2", name: "بلا صورة", episodes: 2 },
    ],
    years: [
      { year: 2021, videos: 300 },
      { year: 2019, videos: 12 },
    ],
  },
};

function serve(hub: unknown = HUB, videos: unknown = { items: [], next: null }) {
  get.mockImplementation((url: string) => {
    if (url === `/api/libraries/${ID}/hub`)
      return hub instanceof Error ? Promise.reject(hub) : Promise.resolve(hub);
    if (url.startsWith("/api/videos?")) return Promise.resolve(videos);
    if (url === "/api/me/pinned-libraries") return Promise.resolve({ library_ids: [], items: [] });
    return Promise.reject(new Error(`unmocked GET ${url}`));
  });
  put.mockImplementation((_u: string, body: { library_ids: string[] }) =>
    Promise.resolve({
      library_ids: body.library_ids,
      items: body.library_ids.map((id, position) => ({ id, name: HUB.library.name, position })),
    })
  );
}

function Loc() {
  const { pathname, search } = useLocation();
  return <span data-testid="loc">{pathname + search}</span>;
}

function renderLib(path = `/library/${ID}`, user = memberUser) {
  return renderPage(
    <ToastProvider>
      <LibraryPage />
      <Loc />
    </ToastProvider>,
    { path, route: "/library/:libraryId", user }
  );
}

const videoCalls = () =>
  get.mock.calls.map((c) => c[0] as string).filter((u) => u.startsWith("/api/videos?"));

describe("LibraryPage", () => {
  beforeEach(() => {
    get.mockReset();
    put.mockReset();
    post.mockReset();
    resetPinsStore();
  });

  it("Overview shows the hub's rows; empty ones are hidden", async () => {
    serve();
    renderLib();
    expect(
      await screen.findByRole("heading", { level: 1, name: "Dr. Othman Alkamees" })
    ).toBeInTheDocument();
    expect(screen.getByText("47,273 videos")).toBeInTheDocument();
    const tabs = screen.getByRole("tablist", { name: en["libraryPage.tabs"] });
    expect(within(tabs).getByRole("tab", { name: en["libraryPage.tab.overview"] })).toHaveAttribute(
      "aria-selected",
      "true"
    );
    for (const k of [
      "home.row.continueWatching",
      "home.row.recentlyAdded",
      "libraryPage.row.mostWatched",
      "libraryPage.row.series",
      "libraryPage.row.byYear",
    ] as const) {
      expect(screen.getByRole("region", { name: en[k] })).toBeInTheDocument();
    }
    expect(screen.queryByRole("region", { name: en["home.row.recentlyWatched"] })).toBeNull();
    const series = screen.getByRole("region", { name: en["libraryPage.row.series"] });
    expect(within(series).getByRole("link", { name: /شرح الأصول/ })).toHaveAttribute(
      "href",
      "/series/s1"
    );
    const years = screen.getByRole("region", { name: en["libraryPage.row.byYear"] });
    expect(within(years).getByRole("link", { name: "2021: 300 videos" })).toHaveAttribute(
      "href",
      `/library/${ID}?tab=browse&year=2021`
    );
    // Overview reads no grid
    expect(videoCalls()).toHaveLength(0);
  });

  it("a series tile shows its first episode's poster; one without has none", async () => {
    serve();
    renderLib();
    const series = await screen.findByRole("region", { name: en["libraryPage.row.series"] });
    const withPoster = within(series).getByRole("link", { name: /شرح الأصول/ });
    expect(withPoster.querySelector("img")).toHaveAttribute("src", "/api/videos/v-first/poster");
    expect(
      within(series)
        .getByRole("link", { name: /بلا صورة/ })
        .querySelector("img")
    ).toBeNull();
  });

  it("Overview shows On Deck, the next lesson of each series under way", async () => {
    serve({
      ...HUB,
      rows: { ...HUB.rows, on_deck: [card("d1", { series: { id: "s1", name: "شرح الأصول" } })] },
    });
    renderLib();
    const deck = await screen.findByRole("region", { name: en["home.row.onDeck"] });
    expect(within(deck).getByRole("link", { name: /Video d1/ })).toHaveTextContent("شرح الأصول");
  });

  it("a year chip opens Browse filtered by that year", async () => {
    serve(HUB, { items: [{ id: "v1", filename: "2019.mp4", state: "ready" }], next: null });
    renderLib();
    const user = userEvent.setup();
    const years = await screen.findByRole("region", { name: en["libraryPage.row.byYear"] });
    await user.click(within(years).getByRole("link", { name: "2019: 12 videos" }));
    expect(await screen.findByText("2019.mp4")).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: en["libraryPage.tab.browse"] })).toHaveAttribute(
      "aria-selected",
      "true"
    );
    const q = new URLSearchParams(videoCalls().at(-1)!.split("?")[1]);
    expect(q.get("library")).toBe(ID);
    expect(q.get("year")).toBe("2019");
    // the year select offers the hub's years
    const select = screen.getByRole("combobox", { name: en["videosAll.filter.year"] });
    expect(select).toHaveValue("2019");
    expect(
      within(select)
        .getAllByRole("option")
        .map((o) => o.textContent)
    ).toEqual([en["library.filter.all"], "2021", "2019"]);
  });

  it("Browse keeps sort, order and filters in the URL and the request", async () => {
    serve();
    renderLib(`/library/${ID}?tab=browse&sort=plays&watched=unwatched`);
    const user = userEvent.setup();
    await waitFor(() => expect(videoCalls()).toHaveLength(1));
    let q = new URLSearchParams(videoCalls()[0].split("?")[1]);
    expect(q.get("library")).toBe(ID);
    expect(q.get("sort")).toBe("plays");
    expect(q.get("order")).toBe("desc");
    expect(q.get("watched")).toBe("unwatched");
    // embedded: the page has one h1, the library's
    expect(screen.getAllByRole("heading", { level: 1 })).toHaveLength(1);
    await user.selectOptions(
      screen.getByRole("combobox", { name: en["library.sort"] }),
      "published_at"
    );
    expect(screen.getByTestId("loc")).toHaveTextContent(
      `/library/${ID}?tab=browse&sort=published_at&watched=unwatched`
    );
    await waitFor(() => expect(videoCalls()).toHaveLength(2));
    q = new URLSearchParams(videoCalls()[1].split("?")[1]);
    expect(q.get("sort")).toBe("published_at");
    expect(q.get("library")).toBe(ID);
    // back to Overview drops the tab, keeps the rest
    await user.click(screen.getByRole("tab", { name: en["libraryPage.tab.overview"] }));
    expect(screen.getByTestId("loc")).not.toHaveTextContent("tab=");
    expect(screen.getByRole("region", { name: en["home.row.recentlyAdded"] })).toBeVisible();
  });

  it("Pin pins this library", async () => {
    serve();
    renderLib();
    const user = userEvent.setup();
    const pin = await screen.findByRole("button", {
      name: "Pin Dr. Othman Alkamees to the sidebar",
    });
    // by the title, not among the header's actions
    expect(pin.closest(".mkt-titlerow")).not.toBeNull();
    await waitFor(() => expect(pin).toBeEnabled());
    await user.click(pin);
    await waitFor(() =>
      expect(put).toHaveBeenCalledWith("/api/me/pinned-libraries", { library_ids: [ID] })
    );
    expect(
      await screen.findByRole("button", {
        name: "Dr. Othman Alkamees is pinned to the sidebar; click to unpin",
      })
    ).toHaveAttribute("aria-pressed", "true");
  });

  it("an admin sees Scan now and Manage; a viewer does not", async () => {
    serve();
    const first = renderLib(`/library/${ID}`, adminUser);
    expect(await screen.findByRole("button", { name: en["library.scanNow"] })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: en["libraryPage.manage"] })).toHaveAttribute(
      "href",
      "/admin/libraries"
    );
    first.unmount();
    resetPinsStore();
    renderLib();
    await screen.findByRole("heading", { level: 1, name: "Dr. Othman Alkamees" });
    expect(screen.queryByRole("button", { name: en["library.scanNow"] })).toBeNull();
    expect(screen.queryByRole("link", { name: en["libraryPage.manage"] })).toBeNull();
  });

  it("a 404 (missing, or not yours to see) shows Library not found", async () => {
    serve(new ApiError({ type: "about:blank", title: "Not Found", status: 404 }));
    renderLib();
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent(en["libraryPage.notFound"]);
    expect(screen.getByRole("link", { name: en["libraryPage.backToLibraries"] })).toHaveAttribute(
      "href",
      "/library"
    );
    expect(screen.queryByRole("tablist")).toBeNull();
    expect(videoCalls()).toHaveLength(0);
  });

  it("a malformed id is not found without asking the server", async () => {
    serve();
    renderLib("/library/not-a-uuid");
    expect(await screen.findByRole("alert")).toHaveTextContent(en["libraryPage.notFound"]);
    expect(get.mock.calls.filter(([u]) => String(u).includes("/hub"))).toHaveLength(0);
  });

  it("a library with nothing in it says so", async () => {
    serve({
      library: { id: ID, name: "Empty", videos: 0 },
      rows: {},
    });
    renderLib();
    expect(await screen.findByText(en["library.empty.title"])).toBeInTheDocument();
    expect(screen.queryByRole("region")).toBeNull();
  });
});
