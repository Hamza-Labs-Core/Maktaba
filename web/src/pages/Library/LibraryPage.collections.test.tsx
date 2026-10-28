// Epic 45 Phase 4 (story 45.24) — the library page's Overview Collections row:
// the library's collections, automatic and curated (its own read: a failure
// only leaves the row out); its title and See all open the Collections tab,
// and the page has no separate Collections button any more.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, within } from "@testing-library/react";
import { renderPage, memberUser, adminUser } from "../../test/pageHarness";
import en from "../../i18n/en.json";

const { get } = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get } };
});

import { LibraryPage } from "./LibraryPage";

const ID = "3f1c2a4e-0000-4000-8000-000000000001";
const HUB = {
  library: { id: ID, name: "Tafsir", videos: 3 },
  rows: {
    continue_watching: [],
    recently_added: [{ id: "a1", title: "Lesson", filename: "a1.mp4" }],
    recently_watched: [],
    most_watched: [],
    series: [{ id: "s1", name: "شرح الأصول", episodes: 30 }],
    years: [],
  },
};
const COLLECTIONS = {
  items: [
    {
      kind: "topic",
      id: "t1",
      name: "الصيام",
      videos: 12,
      poster_video_ids: ["a1"],
      automatic: true,
    },
    {
      kind: "curated",
      id: "c1",
      name: "Ramadan",
      videos: 4,
      poster_video_ids: ["a1"],
      automatic: false,
    },
  ],
  counts: { all: 2, topic: 1, filler: 0, standalone: 0, curated: 1 },
  total: 2,
  next: null,
};

function serve(collections: unknown) {
  get.mockImplementation((url: string) => {
    if (url === `/api/libraries/${ID}/hub`) return Promise.resolve(HUB);
    if (url === `/api/libraries/${ID}/collections`)
      return collections instanceof Error
        ? Promise.reject(collections)
        : Promise.resolve(collections);
    if (url === "/api/me/pinned-libraries") return Promise.resolve({ library_ids: [], items: [] });
    return Promise.reject(new Error(`unmocked GET ${url}`));
  });
}

const render = (user = memberUser) =>
  renderPage(<LibraryPage />, {
    path: `/library/${ID}`,
    route: "/library/:libraryId",
    user,
  });

describe("LibraryPage collections row", () => {
  beforeEach(() => {
    get.mockReset();
  });

  it("shows automatic and curated collections; title and See all open the tab", async () => {
    serve(COLLECTIONS);
    render();
    const row = await screen.findByTestId("row-collections");
    expect(within(row).getByText("الصيام").closest("a")).toHaveAttribute(
      "href",
      `/library/${ID}/collections/topic/t1`
    );
    expect(within(row).getByText(en["collections.automatic"])).toBeInTheDocument();
    expect(within(row).getByText("Ramadan").closest("a")).toHaveAttribute(
      "href",
      "/collections/c1"
    );
    expect(within(row).getByRole("link", { name: en["home.seeAll"] })).toHaveAttribute(
      "href",
      `/library/${ID}?tab=collections`
    );
    expect(within(row).getByRole("link", { name: en["collections.title"] })).toHaveAttribute(
      "href",
      `/library/${ID}?tab=collections`
    );
    // the Series row opens the Series tab
    const series = screen.getByTestId("row-series");
    expect(within(series).getByRole("link", { name: en["home.seeAll"] })).toHaveAttribute(
      "href",
      `/library/${ID}?tab=series`
    );
  });

  it("has no separate Collections button, for an admin either", async () => {
    serve(COLLECTIONS);
    render(adminUser);
    await screen.findByTestId("row-collections");
    const links = screen
      .getAllByRole("link", { name: en["collections.title"] })
      .filter((a) => !a.closest("[data-testid='row-collections']"));
    expect(links).toHaveLength(0);
  });

  it("leaves the row out when there are none or the read fails", async () => {
    serve(new Error("down"));
    render();
    await screen.findByText("Lesson");
    expect(screen.queryByTestId("row-collections")).toBeNull();
  });
});
