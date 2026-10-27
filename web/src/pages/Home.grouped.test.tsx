// Epic 45 Phase 3 (story 45.18) — Recently Added grouped by series on Home:
// a series' new lessons are one card that opens the series; other entries
// stay video cards; an older server's plain row still shows.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, within } from "@testing-library/react";
import { renderPage, memberUser } from "../test/pageHarness";
import en from "../i18n/en.json";

const { get } = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock("../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../lib/api")>("../lib/api");
  return { ...actual, api: { ...actual.api, get } };
});

import { Home } from "./Home";

const card = (id: string) => ({ id, title: `Video ${id}`, filename: `${id}.mp4`, state: "ready" });
const BASE = {
  continue_watching: [],
  recently_watched: [],
  on_deck: [],
  favorites: [],
  pinned: [],
  recently_added: [card("a1"), card("a2"), card("a3")],
};

function serve(home: unknown) {
  get.mockImplementation((url: string) => {
    if (url === "/api/home") return Promise.resolve(home);
    if (url.startsWith("/api/channels/now")) return Promise.resolve({ server_time: "", items: [] });
    return Promise.reject(new Error(`unmocked GET ${url}`));
  });
}

describe("Home, Recently Added by series", () => {
  beforeEach(() => {
    get.mockReset();
  });

  it("groups a series' new lessons into one card", async () => {
    serve({
      ...BASE,
      recently_added_grouped: [
        { kind: "video", video: card("a1") },
        { kind: "series", video: card("a2"), series: { id: "s1", name: "تفسير" }, count: 12 },
        { kind: "video", video: card("a9"), series: { id: "s2", name: "Fiqh" } },
      ],
    });
    renderPage(<Home />, { path: "/", user: memberUser });
    const row = await screen.findByTestId("row-added");
    const group = within(row).getByText("تفسير").closest("a")!;
    expect(group).toHaveAttribute("href", "/series/s1");
    expect(within(group).getByText(en["home.newLessons"].replace("{n}", "12"))).toBeInTheDocument();
    expect(within(row).getByText("Video a1").closest("a")).toHaveAttribute("href", "/videos/a1");
    // a lone new lesson stays a video card that names its series
    expect(within(row).getByText("Video a9").closest("a")).toHaveAttribute("href", "/videos/a9");
    expect(within(row).getByText("Fiqh")).toBeInTheDocument();
    expect(within(row).queryByText("Video a2")).toBeNull();
  });

  it("shows the plain row from a server without groups", async () => {
    serve(BASE);
    renderPage(<Home />, { path: "/", user: memberUser });
    const row = await screen.findByTestId("row-added");
    expect(within(row).getAllByRole("link", { name: /Video a/ })).toHaveLength(3);
  });
});
