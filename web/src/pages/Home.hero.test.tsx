// Epic 45 Phase 3 (story 45.21) — Home's featured hero (the newest series or
// an admin's pick) and the admin's Feature on Home on a video's page.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderPage, memberUser, adminUser } from "../test/pageHarness";
import en from "../i18n/en.json";

const { get, put } = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn() }));
vi.mock("../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../lib/api")>("../lib/api");
  return { ...actual, api: { ...actual.api, get, put } };
});

import { Home } from "./Home";
import { FeatureOnHome } from "../components/FeatureOnHome";

const card = (id: string) => ({
  id,
  title: `Lesson ${id}`,
  filename: `${id}.mp4`,
  duration_sec: 900,
});
const BASE = {
  continue_watching: [],
  recently_watched: [],
  on_deck: [],
  favorites: [],
  pinned: [],
  recently_added: [card("a1")],
};

function serve(home: unknown, pick: string | null = null) {
  get.mockImplementation((url: string) => {
    if (url === "/api/home") return Promise.resolve(home);
    if (url === "/api/home/featured") return Promise.resolve({ video_id: pick });
    if (url.startsWith("/api/channels/now")) return Promise.resolve({ server_time: "", items: [] });
    return Promise.reject(new Error(`unmocked GET ${url}`));
  });
}

describe("Featured hero", () => {
  beforeEach(() => {
    get.mockReset();
    put.mockReset();
  });

  it("features the newest series from its first lesson", async () => {
    serve({
      ...BASE,
      featured: {
        kind: "series",
        video: card("a1"),
        series: { id: "s1", name: "شرح الأصول", episodes: 30 },
        picked: false,
      },
    });
    renderPage(<Home />, { path: "/", user: adminUser });
    const hero = await screen.findByTestId("featured-hero");
    expect(within(hero).getByRole("heading", { name: "شرح الأصول" })).toBeInTheDocument();
    expect(within(hero).getByText(en["home.hero.newSeries"])).toBeInTheDocument();
    expect(within(hero).getByRole("link", { name: /Play the first lesson/ })).toHaveAttribute(
      "href",
      "/videos/a1/watch"
    );
    expect(within(hero).getByRole("link", { name: en["home.hero.openSeries"] })).toHaveAttribute(
      "href",
      "/series/s1"
    );
    // nothing picked: no reset
    expect(within(hero).queryByRole("button", { name: en["home.hero.reset"] })).toBeNull();
  });

  it("shows a picked lesson; an admin can go back to the newest series", async () => {
    const picked = {
      ...BASE,
      featured: {
        kind: "video",
        video: card("v7"),
        series: { id: "s1", name: "Tafsir" },
        picked: true,
      },
    };
    serve(picked);
    put.mockResolvedValue({ video_id: null });
    renderPage(<Home />, { path: "/", user: adminUser });
    const hero = await screen.findByTestId("featured-hero");
    expect(within(hero).getByRole("heading", { name: "Lesson v7" })).toBeInTheDocument();
    expect(within(hero).getByRole("link", { name: "Tafsir" })).toHaveAttribute(
      "href",
      "/series/s1"
    );
    await userEvent.click(within(hero).getByRole("button", { name: en["home.hero.reset"] }));
    await waitFor(() => expect(put).toHaveBeenCalledWith("/api/home/featured", { video_id: null }));
    // Home reads itself again
    await waitFor(() => expect(get.mock.calls.filter((c) => c[0] === "/api/home")).toHaveLength(2));
  });

  it("gives a viewer no reset, and no hero when there is nothing to feature", async () => {
    serve({ ...BASE, featured: { kind: "video", video: card("v7"), picked: true } });
    const { unmount } = renderPage(<Home />, { path: "/", user: memberUser });
    const hero = await screen.findByTestId("featured-hero");
    expect(within(hero).queryByRole("button")).toBeNull();
    unmount();
    serve({ ...BASE, featured: null });
    renderPage(<Home />, { path: "/", user: memberUser });
    await screen.findByTestId("row-added");
    expect(screen.queryByTestId("featured-hero")).toBeNull();
  });
});

describe("Feature on Home", () => {
  beforeEach(() => {
    get.mockReset();
    put.mockReset();
  });

  it("lets an admin feature a lesson and take it back", async () => {
    serve(BASE, null);
    put.mockResolvedValueOnce({ video_id: "v1" }).mockResolvedValueOnce({ video_id: null });
    renderPage(<FeatureOnHome videoId="v1" />, { path: "/videos/v1", user: adminUser });
    const btn = await screen.findByRole("button", { name: /Feature on Home/ });
    expect(btn).toHaveAttribute("aria-pressed", "false");
    await userEvent.click(btn);
    await waitFor(() => expect(put).toHaveBeenCalledWith("/api/home/featured", { video_id: "v1" }));
    const on = await screen.findByRole("button", { name: /Featured on Home/ });
    expect(on).toHaveAttribute("aria-pressed", "true");
    await userEvent.click(on);
    await waitFor(() =>
      expect(put).toHaveBeenLastCalledWith("/api/home/featured", { video_id: null })
    );
  });

  it("shows nothing to a viewer and never reads the pick", async () => {
    serve(BASE);
    renderPage(<FeatureOnHome videoId="v1" />, { path: "/videos/v1", user: memberUser });
    await new Promise((r) => setTimeout(r, 20));
    expect(screen.queryByRole("button", { name: /Feature/ })).toBeNull();
    expect(get).not.toHaveBeenCalledWith("/api/home/featured");
  });
});
