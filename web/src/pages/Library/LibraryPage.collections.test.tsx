// Epic 45 Phase 3 (story 45.17) — the library page's Collections row (its own
// read: a failure only leaves the row out) and its header link.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, within } from "@testing-library/react";
import { renderPage, memberUser } from "../../test/pageHarness";
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
    series: [],
    years: [],
  },
};

function serve(collections: unknown) {
  get.mockImplementation((url: string) => {
    if (url === `/api/libraries/${ID}/hub`) return Promise.resolve(HUB);
    if (url === `/api/collections?library=${ID}`)
      return collections instanceof Error
        ? Promise.reject(collections)
        : Promise.resolve(collections);
    if (url === "/api/me/pinned-libraries") return Promise.resolve({ library_ids: [], items: [] });
    return Promise.reject(new Error(`unmocked GET ${url}`));
  });
}

const render = () =>
  renderPage(<LibraryPage />, {
    path: `/library/${ID}`,
    route: "/library/:libraryId",
    user: memberUser,
  });

describe("LibraryPage collections", () => {
  beforeEach(() => {
    get.mockReset();
  });

  it("shows the library's collections in a row that links to its Collections page", async () => {
    serve({
      items: [
        {
          id: "c1",
          library_id: ID,
          name: "Ramadan",
          is_smart: false,
          videos: 4,
          poster_video_ids: ["a1"],
        },
      ],
    });
    render();
    const row = await screen.findByTestId("row-collections");
    expect(within(row).getByText("Ramadan").closest("a")).toHaveAttribute(
      "href",
      "/collections/c1"
    );
    expect(within(row).getByRole("link", { name: en["home.seeAll"] })).toHaveAttribute(
      "href",
      `/library/${ID}/collections`
    );
    expect(screen.getAllByRole("link", { name: en["collections.title"] }).length).toBeGreaterThan(
      0
    );
  });

  it("leaves the row (and a viewer's header link) out when there are none or the read fails", async () => {
    serve(new Error("down"));
    render();
    await screen.findByText("Lesson");
    expect(screen.queryByTestId("row-collections")).toBeNull();
    expect(screen.queryByRole("link", { name: en["collections.title"] })).toBeNull();
  });
});
