// Epic 45 Phase 2 — the grid's new parts (stories 45.8, 45.11 to 45.13, 45.16):
// the search box, Tag / Speaker / Language from the facets, the Favorites
// toggle, the A to Z jump bar, watched ticks / progress / hearts on the cards,
// Play all and Shuffle into the play queue, and Select with the bulk actions.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Route, useLocation } from "react-router-dom";
import { renderPage, memberUser } from "../test/pageHarness";
import { readQueue } from "../lib/playQueue";
import ar from "../i18n/ar.json";

const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock("../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../lib/api")>("../lib/api");
  return { ...actual, api: { ...actual.api, get, post } };
});

import { LibraryBrowser } from "./LibraryBrowser";

const LIB = "11111111-1111-4111-8111-111111111111";
const TAG = "22222222-2222-4222-8222-222222222222";

const FACETS = {
  languages: [
    { code: "ar", videos: 10 },
    { code: "en", videos: 2 },
  ],
  tags: [{ id: TAG, name: "فقه", videos: 4 }],
  speakers: [{ name: "الشيخ أحمد", videos: 7 }],
  letters: [
    { letter: "أ", videos: 3 },
    { letter: "ب", videos: 2 },
    { letter: "#", videos: 1 },
  ],
};

const VIDEOS = {
  items: [
    {
      id: "v1",
      title: "الدرس الأول",
      filename: "1.mp4",
      state: "ready",
      duration_sec: 1000,
      playback_state: { position_sec: 0, completed: true },
      favorite: true,
    },
    {
      id: "v2",
      title: "الدرس الثاني",
      filename: "2.mp4",
      state: "ready",
      duration_sec: 1000,
      playback_state: { position_sec: 250, completed: false },
    },
    { id: "v3", title: "الدرس الثالث", filename: "3.mp4", state: "ready", duration_sec: 1000 },
  ],
  next: "CUR",
};

function serve(over: { facets?: unknown; videos?: unknown } = {}) {
  get.mockImplementation((url: string) => {
    if (url.startsWith("/api/videos/facets")) return Promise.resolve(over.facets ?? FACETS);
    // the favorites filter is read on the home's /api/me/videos (the same list)
    if (url.startsWith("/api/videos?") || url.startsWith("/api/me/videos?"))
      return Promise.resolve(over.videos ?? VIDEOS);
    return Promise.reject(new Error(`unmocked GET ${url}`));
  });
}

const videoCalls = () =>
  get.mock.calls
    .map((c) => c[0] as string)
    .filter((u) => u.startsWith("/api/videos?") || u.startsWith("/api/me/videos?"));
const lastQuery = () => new URLSearchParams(videoCalls().at(-1)!.split("?")[1]);

function Loc() {
  const { pathname, search } = useLocation();
  return <span data-testid="loc">{pathname + search}</span>;
}

function renderGrid(path = `/library/${LIB}`) {
  return renderPage(
    <>
      <LibraryBrowser />
      <Loc />
    </>,
    {
      path,
      route: "/library/:libraryId",
      user: memberUser,
      extraRoutes: <Route path="/videos/:videoId/watch" element={<p>player</p>} />,
    }
  );
}

describe("LibraryBrowser, Phase 2", () => {
  beforeEach(() => {
    get.mockReset();
    post.mockReset();
    sessionStorage.clear();
    localStorage.clear();
  });

  it("reads the library's facets and offers their tags, speakers and languages", async () => {
    serve();
    renderGrid();
    await screen.findByText("الدرس الأول");
    expect(get).toHaveBeenCalledWith(`/api/videos/facets?library=${LIB}`);
    const user = userEvent.setup();
    await user.selectOptions(await screen.findByRole("combobox", { name: "Tag" }), TAG);
    await waitFor(() => expect(lastQuery().get("tag")).toBe(TAG));
    await user.selectOptions(screen.getByRole("combobox", { name: "Speaker" }), "الشيخ أحمد");
    await waitFor(() => expect(lastQuery().get("speaker")).toBe("الشيخ أحمد"));
    const langs = within(screen.getByRole("group", { name: "Language" }));
    expect(langs.getAllByRole("button").map((b) => b.textContent)).toEqual(["ar", "en"]);
    expect(lastQuery().get("library")).toBe(LIB);
  });

  it("searches by the box and toggles Favorites", async () => {
    serve();
    renderGrid();
    await screen.findByText("الدرس الأول");
    const user = userEvent.setup();
    await user.type(screen.getByRole("searchbox", { name: "Search this library" }), "صلاة");
    await user.click(screen.getByRole("button", { name: "Search" }));
    await waitFor(() => expect(lastQuery().get("q")).toBe("صلاة"));
    const fav = screen.getByRole("button", { name: /Favorites/ });
    expect(fav).toHaveAttribute("aria-pressed", "false");
    await user.click(fav);
    await waitFor(() => expect(lastQuery().get("favorite")).toBe("true"));
    expect(videoCalls().at(-1)).toMatch(/^\/api\/me\/videos\?/);
    expect(screen.getByTestId("loc")).toHaveTextContent("favorite=true");
  });

  it("the jump bar shows under the title sort and filters by a letter", async () => {
    serve();
    renderGrid();
    await screen.findByText("الدرس الأول");
    expect(screen.queryByRole("navigation", { name: "Jump to a letter" })).toBeNull();
    const user = userEvent.setup();
    await user.selectOptions(screen.getByRole("combobox", { name: "Sort" }), "title");
    const bar = await screen.findByRole("navigation", { name: "Jump to a letter" });
    await user.click(within(bar).getByRole("button", { name: "ب: 2 videos" }));
    await waitFor(() => expect(lastQuery().get("letter")).toBe("ب"));
    expect(within(bar).getByRole("button", { name: "ب: 2 videos" })).toHaveAttribute(
      "aria-pressed",
      "true"
    );
    await user.click(within(bar).getByRole("button", { name: "All" }));
    await waitFor(() => expect(lastQuery().get("letter")).toBeNull());
  });

  it("cards show the watched tick, the progress and the heart", async () => {
    serve();
    renderGrid();
    const first = (await screen.findByText("الدرس الأول")).closest("li")!;
    expect(within(first).getByRole("img", { name: "Watched" })).toBeInTheDocument();
    expect(within(first).getByRole("img", { name: "Favorite" })).toBeInTheDocument();
    const second = screen.getByText("الدرس الثاني").closest("li")!;
    expect(within(second).getByRole("progressbar")).toHaveAttribute("aria-valuenow", "25");
    expect(within(second).queryByRole("img", { name: "Watched" })).toBeNull();
  });

  it("Play all queues the grid's list with its cursor and opens the player", async () => {
    serve();
    renderGrid(`/library/${LIB}?sort=title&order=asc`);
    await screen.findByText("الدرس الأول");
    await userEvent.setup().click(screen.getByRole("button", { name: /Play all/ }));
    expect(await screen.findByText("player")).toBeInTheDocument();
    const q = lastQuery();
    expect(q.get("limit")).toBe("200");
    expect(q.get("sort")).toBe("title");
    expect(readQueue()).toMatchObject({ ids: ["v1", "v2", "v3"], index: 0 });
    expect(readQueue()?.source?.cursor).toBe("CUR");
  });

  it("Shuffle asks for a random page", async () => {
    serve();
    renderGrid();
    await screen.findByText("الدرس الأول");
    await userEvent.setup().click(screen.getByRole("button", { name: /Shuffle/ }));
    expect(await screen.findByText("player")).toBeInTheDocument();
    expect(lastQuery().get("sort")).toBe("random");
  });

  it("Select: check cards, mark them watched, favorite them, add them to a playlist", async () => {
    serve();
    post.mockImplementation((path: string, body: { video_ids?: string[] }) => {
      if (path === "/api/me/watched" || path === "/api/me/favorites")
        return Promise.resolve({ updated: body.video_ids?.length ?? 0 });
      if (path === "/api/me/playlists/p1/items") return Promise.resolve({ added: 2 });
      return Promise.reject(new Error(`unmocked POST ${path}`));
    });
    renderGrid();
    await screen.findByText("الدرس الأول");
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Select" }));
    const bar = screen.getByRole("region", { name: "Selected videos" });
    expect(within(bar).getByRole("button", { name: "Mark watched" })).toBeDisabled();
    await user.click(screen.getByRole("checkbox", { name: "Select الدرس الثاني" }));
    await user.click(screen.getByRole("checkbox", { name: "Select الدرس الثالث" }));
    expect(bar).toHaveTextContent("2 selected");

    await user.click(within(bar).getByRole("button", { name: "Mark watched" }));
    expect(post).toHaveBeenCalledWith("/api/me/watched", {
      video_ids: ["v2", "v3"],
      watched: true,
    });
    expect(await screen.findByText("Marked 2 as watched.")).toBeInTheDocument();
    // the cards follow, and the selection is cleared
    const third = screen.getByText("الدرس الثالث").closest("li")!;
    expect(within(third).getByRole("img", { name: "Watched" })).toBeInTheDocument();
    expect(bar).toHaveTextContent("0 selected");

    await user.click(within(bar).getByRole("button", { name: "Select all shown" }));
    expect(bar).toHaveTextContent("3 selected");
    await user.click(within(bar).getByRole("button", { name: "Remove from favorites" }));
    expect(post).toHaveBeenCalledWith("/api/me/favorites", {
      video_ids: ["v1", "v2", "v3"],
      favorite: false,
    });
    await waitFor(() => expect(screen.queryByRole("img", { name: "Favorite" })).toBeNull());

    // Add to playlist: the dialog lists the user's playlists
    get.mockImplementation((url: string) => {
      if (url === "/api/me/playlists")
        return Promise.resolve({ items: [{ id: "p1", name: "Study", videos: 1 }] });
      if (url.startsWith("/api/videos/facets")) return Promise.resolve(FACETS);
      return Promise.resolve(VIDEOS);
    });
    await user.click(screen.getByRole("checkbox", { name: "Select الدرس الأول" }));
    await user.click(screen.getByRole("checkbox", { name: "Select الدرس الثاني" }));
    await user.click(within(bar).getByRole("button", { name: "Add to playlist" }));
    const dialog = await screen.findByRole("dialog");
    await user.click(await within(dialog).findByRole("button", { name: "Add to Study" }));
    expect(post).toHaveBeenCalledWith("/api/me/playlists/p1/items", { video_ids: ["v1", "v2"] });
    expect(await screen.findByText("Added 2 to Study.")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Done" }));
    expect(screen.queryByRole("region", { name: "Selected videos" })).toBeNull();
    expect(screen.getByRole("link", { name: /الدرس الأول/ })).toBeInTheDocument();
  });

  it("a failed bulk change says so and keeps the cards", async () => {
    serve();
    post.mockRejectedValue(new Error("down"));
    renderGrid();
    await screen.findByText("الدرس الأول");
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Select" }));
    await user.click(screen.getByRole("checkbox", { name: "Select الدرس الثالث" }));
    await user.click(screen.getByRole("button", { name: "Mark watched" }));
    expect(await screen.findByText(/Could not save the change/)).toBeInTheDocument();
    const third = screen.getByText("الدرس الثالث").closest("li")!;
    expect(within(third).queryByRole("img", { name: "Watched" })).toBeNull();
  });

  it("an older server without facets falls back to the page's languages", async () => {
    get.mockImplementation((url: string) =>
      url.startsWith("/api/videos/facets")
        ? Promise.reject(new Error("404"))
        : Promise.resolve({
            items: [{ id: "v1", filename: "a.mp4", state: "ready", detected_language: "en" }],
            next: null,
          })
    );
    renderGrid();
    await screen.findByText("a.mp4");
    expect(screen.queryByRole("combobox", { name: "Tag" })).toBeNull();
    expect(
      within(screen.getByRole("group", { name: "Language" })).getByRole("button", { name: "en" })
    ).toBeInTheDocument();
  });

  it("speaks Arabic: the search box, Play all and the jump bar", async () => {
    localStorage.setItem("mkt:locale", "ar");
    serve();
    renderGrid(`/library/${LIB}?sort=title`);
    await screen.findByText("الدرس الأول");
    expect(
      screen.getByRole("searchbox", { name: ar["browse.search.library"] })
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: new RegExp(ar["browse.playAll"]) })
    ).toBeInTheDocument();
    expect(await screen.findByRole("navigation", { name: ar["browse.jump"] })).toBeInTheDocument();
    expect(document.documentElement).toHaveAttribute("dir", "rtl");
  });
});
