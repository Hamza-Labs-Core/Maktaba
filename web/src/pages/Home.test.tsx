// Epic 45 — Home (`/`): GET /api/home's rows, What's On Now, a row per
// pinned library, empty rows hidden, and the first-run state.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ApiError } from "../lib/api";
import { renderPage, memberUser, adminUser } from "../test/pageHarness";
import en from "../i18n/en.json";

const { get } = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock("../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../lib/api")>("../lib/api");
  return { ...actual, api: { ...actual.api, get } };
});

import { Home } from "./Home";

const card = (id: string, extra: Record<string, unknown> = {}) => ({
  id,
  title: `Video ${id}`,
  filename: `${id}.mp4`,
  library_id: "L1",
  duration_sec: 600,
  state: "ready",
  ...extra,
});

const LIB = "11111111-1111-4111-8111-111111111111";
const EMPTY_LIB = "22222222-2222-4222-8222-222222222222";

// the wire form of GET /api/channels/now (lib/channels reads `channels`)
const NOW = {
  server_time: "2026-10-26T10:00:00Z",
  channels: [
    {
      channel: { id: "c1", number: 1, name: "Quran TV", mode: "shuffle", enabled: true },
      current: {
        channel_id: "c1",
        seq: 0,
        kind: "program",
        title: "Tafsir",
        start: "2026-10-26T09:30:00Z",
        stop: "2026-10-26T10:30:00Z",
      },
    },
  ],
};

function serve(home: unknown, now: unknown = { server_time: "", items: [] }) {
  get.mockImplementation((url: string) => {
    if (url === "/api/home")
      return home instanceof Error ? Promise.reject(home) : Promise.resolve(home);
    if (url.startsWith("/api/channels/now")) return Promise.resolve(now);
    return Promise.reject(new Error(`unmocked GET ${url}`));
  });
}

function renderHome(user = memberUser) {
  return renderPage(<Home />, { path: "/", user });
}

describe("Home", () => {
  beforeEach(() => {
    get.mockReset();
  });

  it("renders the rows, What's On Now and a row per pinned library", async () => {
    serve(
      {
        continue_watching: [card("c1", { position_sec: 300 })],
        recently_added: [card("a1"), card("a2")],
        recently_watched: [card("w1")],
        pinned: [
          {
            library: { id: LIB, name: "دروس الفقه" },
            continue_watching: [card("p1", { position_sec: 60 })],
            recently_added: [card("p2"), card("p1")],
          },
          { library: { id: EMPTY_LIB, name: "Empty" }, continue_watching: [], recently_added: [] },
        ],
      },
      NOW
    );
    renderHome();
    expect(await screen.findByRole("heading", { level: 1, name: en["home.title"] })).toBeVisible();
    const cont = await screen.findByRole("region", { name: en["home.row.continueWatching"] });
    expect(within(cont).getByRole("link", { name: /Video c1/ })).toHaveAttribute(
      "href",
      "/videos/c1"
    );
    expect(within(cont).getByRole("progressbar")).toHaveAttribute("aria-valuenow", "50");
    const added = screen.getByRole("region", { name: en["home.row.recentlyAdded"] });
    expect(within(added).getAllByRole("listitem")).toHaveLength(2);
    expect(within(added).getByRole("link", { name: en["home.seeAll"] })).toHaveAttribute(
      "href",
      "/videos?sort=created_at"
    );
    expect(screen.getByRole("region", { name: en["home.row.recentlyWatched"] })).toBeVisible();
    expect(await screen.findByTestId("whats-on-now-rail")).toBeInTheDocument();

    const pinned = screen.getByRole("region", { name: "دروس الفقه" });
    expect(within(pinned).getByRole("link", { name: "دروس الفقه" })).toHaveAttribute(
      "href",
      `/library/${LIB}`
    );
    // in progress first, then new; a video in both shows once
    const hrefs = within(pinned)
      .getAllByRole("listitem")
      .map((li) => li.querySelector("a")!.getAttribute("href"));
    expect(hrefs).toEqual(["/videos/p1", "/videos/p2"]);
    // a pinned library with nothing in it has no row
    expect(screen.queryByRole("region", { name: "Empty" })).toBeNull();
    expect(screen.queryByText(en["home.empty.title"])).toBeNull();
  });

  it("shows On Deck with each lesson's series, and Favorites with See all", async () => {
    serve({
      on_deck: [card("d1", { series: { id: "s1", name: "شرح الأربعين" } })],
      favorites: [card("f1"), card("f2", { completed: true })],
      continue_watching: [],
      recently_added: [],
      recently_watched: [],
      pinned: [],
    });
    renderHome();
    const deck = await screen.findByRole("region", { name: en["home.row.onDeck"] });
    const lesson = within(deck).getByRole("link", { name: /Video d1/ });
    expect(lesson).toHaveAttribute("href", "/videos/d1");
    expect(lesson).toHaveTextContent("شرح الأربعين");
    const favs = screen.getByRole("region", { name: en["home.row.favorites"] });
    expect(within(favs).getAllByRole("listitem")).toHaveLength(2);
    expect(within(favs).getByRole("link", { name: en["home.seeAll"] })).toHaveAttribute(
      "href",
      "/videos?favorite=true"
    );
    // a finished one has the watched tick
    expect(within(favs).getByRole("img", { name: en["browse.watchedMark"] })).toBeInTheDocument();
    expect(screen.queryByText(en["home.empty.title"])).toBeNull();
  });

  it("hides empty rows", async () => {
    serve({
      continue_watching: [],
      recently_added: [card("a1")],
      recently_watched: null,
      pinned: [],
    });
    renderHome();
    expect(
      await screen.findByRole("region", { name: en["home.row.recentlyAdded"] })
    ).toBeInTheDocument();
    expect(screen.queryByRole("region", { name: en["home.row.continueWatching"] })).toBeNull();
    expect(screen.queryByRole("region", { name: en["home.row.recentlyWatched"] })).toBeNull();
    expect(screen.queryByText(en["home.empty.title"])).toBeNull();
  });

  it("a first-run home points a viewer to Libraries", async () => {
    serve({ continue_watching: [], recently_added: [], recently_watched: [], pinned: [] });
    renderHome();
    expect(await screen.findByText(en["home.empty.title"])).toBeInTheDocument();
    expect(screen.getByText(en["home.empty.desc"])).toBeInTheDocument();
    expect(screen.getByRole("link", { name: en["home.empty.libraries"] })).toHaveAttribute(
      "href",
      "/library"
    );
    expect(screen.queryByRole("link", { name: en["home.empty.manage"] })).toBeNull();
  });

  it("a first-run home also points an admin to Manage libraries", async () => {
    serve({});
    renderHome(adminUser);
    expect(await screen.findByText(en["home.empty.descAdmin"])).toBeInTheDocument();
    expect(screen.getByRole("link", { name: en["home.empty.manage"] })).toHaveAttribute(
      "href",
      "/admin/libraries"
    );
  });

  it("an error shows Try again, which reads the home again", async () => {
    let calls = 0;
    get.mockImplementation((url: string) => {
      if (url !== "/api/home") return Promise.resolve({ items: [] });
      calls += 1;
      return calls === 1
        ? Promise.reject(new ApiError({ type: "about:blank", title: "home failed", status: 500 }))
        : Promise.resolve({ recently_added: [card("a1")] });
    });
    renderHome();
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent(en["error.kind.server"]);
    expect(alert).not.toHaveTextContent("home failed");
    await userEvent.setup().click(within(alert).getByRole("button", { name: en["common.retry"] }));
    expect(
      await screen.findByRole("region", { name: en["home.row.recentlyAdded"] })
    ).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByRole("alert")).toBeNull());
  });
});
