// Search must speak the real server contract (web/test-cases/search.md):
//   POST /api/search { q, mode, view }
//   -> { search_id, page_size, videos (In titles), videos_total,
//        transcript_videos (In transcripts, one per video), transcript_total }
//   GET  /api/search/results/{sid}?list=&offset=&limit=   (later pages)
//   GET  /api/search/passages?sid=&video_id=&order=score   (show all)
//   GET/DELETE /api/search/recent[/{sid}]
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Route, useLocation } from "react-router-dom";

const { post, get, del } = vi.hoisted(() => ({
  post: vi.fn(),
  get: vi.fn(),
  del: vi.fn(),
}));
vi.mock("../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../lib/api")>("../lib/api");
  return { ...actual, api: { ...actual.api, post, get, delete: del } };
});

import { Search } from "./Search";
import { ToastProvider } from "@ds/components/Toast/Toast";
import { ApiError } from "../lib/api";
import en from "../i18n/en.json";
import { adminUser, memberUser, quietCrash, renderPage } from "../test/pageHarness";
import {
  type Corpus,
  fakeSearchServer,
  manyTitles,
  manyTranscripts,
  passage,
  titleVideo,
  transcriptVideo,
} from "../test/searchServer";

type Answer = unknown | ((body: Record<string, unknown>) => unknown);

/** A one-video, one-passage transcript answer (raw, without a sid). */
const oneHit = (video: string, snippet: string | null, start = 72.5, end = 78) => ({
  transcript_videos: [
    {
      ...titleVideo(video, `Video ${video}`),
      matches: 1,
      passages: [{ segment_id: 1, start_sec: start, end_sec: end, snippet, score: 1 }],
    },
  ],
  transcript_total: 1,
});

/**
 * Routes api.post / api.get by path with a raw POST /api/search answer (no
 * saved search unless the answer has a search_id). `search` may be a
 * function of the body.
 */
function serveSearch(
  search: Answer,
  opts: { saved?: Answer; suggest?: Answer; other?: Answer; ytAvailable?: boolean } = {}
) {
  const pick = (v: Answer, arg: Record<string, unknown>) => {
    try {
      return Promise.resolve(
        typeof v === "function" ? (v as (b: Record<string, unknown>) => unknown)(arg) : v
      );
    } catch (e) {
      return Promise.reject(e);
    }
  };
  post.mockImplementation((url: string, body: Record<string, unknown>) =>
    url === "/api/search" ? pick(search, body) : pick(opts.other ?? undefined, { url })
  );
  // Story 26.8: the YouTube block comes from its own GET after the local
  // search, and only when the server reports yt-dlp available.
  const ytBlock =
    search && typeof search === "object" && "youtube" in (search as Record<string, unknown>)
      ? (search as { youtube: unknown }).youtube
      : { items: [] };
  get.mockImplementation((url: string) => {
    if (url === "/api/search/youtube/status")
      return Promise.resolve({ available: opts.ytAvailable !== false });
    if (url.startsWith("/api/search/youtube?")) return pick(ytBlock, { url });
    if (url.startsWith("/api/search/saved")) return pick(opts.saved ?? { items: [] }, { url });
    if (url.startsWith("/api/search/recent")) return Promise.resolve({ items: [] });
    if (url.startsWith("/api/search/suggest"))
      return pick(opts.suggest ?? { suggestions: [] }, { url });
    return Promise.reject(new Error(`unmocked GET ${url}`));
  });
}

const serve = (corpus: Corpus | ((b: Record<string, unknown>) => Corpus), opts = {}) =>
  fakeSearchServer({ post, get, del }, corpus, opts);

const searchCalls = () => post.mock.calls.filter((c) => c[0] === "/api/search");
const getCalls = (prefix: string) =>
  get.mock.calls.map((c) => String(c[0])).filter((u) => u.startsWith(prefix));
const problem = (status: number, title: string) =>
  new ApiError({ type: "about:blank", title, status });

function Loc() {
  const { pathname, search } = useLocation();
  return <span data-testid="loc">{pathname + search}</span>;
}
const loc = () => screen.getByTestId("loc").textContent ?? "";
const locParams = () => new URLSearchParams(loc().split("?")[1] ?? "");

function renderSearchPage(
  path = "/search",
  opts: { history?: string[]; user?: typeof memberUser | null } = {}
) {
  return renderPage(
    <ToastProvider>
      <Search />
      <Loc />
    </ToastProvider>,
    {
      path,
      route: "/search",
      user: opts.user === undefined ? memberUser : opts.user,
      history: opts.history,
      extraRoutes: (
        <>
          <Route path="/videos/:videoId/watch" element={<p>player page</p>} />
          <Route path="/search/video/:videoId" element={<p>matches page</p>} />
        </>
      ),
    }
  );
}

async function submit(term: string) {
  const user = userEvent.setup();
  const box = screen.getByRole("combobox", { name: "Search" });
  await user.clear(box);
  if (term) await user.type(box, term);
  await user.click(screen.getByRole("button", { name: /^search$/i }));
}

const titlesSection = () => screen.getByRole("region", { name: "In titles" });
const transcriptsSection = () => screen.getByRole("region", { name: "In transcripts" });
const rowTitles = (section: HTMLElement) =>
  Array.from(section.querySelectorAll(".mkt-result__title")).map((e) => e.textContent);

beforeEach(() => {
  post.mockReset();
  get.mockReset();
  del.mockReset();
  sessionStorage.clear();
});
afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

describe("Search (test-cases/search.md)", () => {
  it("SRC-01: submit POSTs { q, mode, view }, keeps the sid in the URL and links passages to the player", async () => {
    serve({
      titles: [],
      transcripts: [
        transcriptVideo("vid-1", "Lesson", [
          passage(42, 72.5, "the <mark>quick</mark> brown fox", 78),
        ]),
      ],
    });
    renderSearchPage();
    await submit("quick");
    expect(searchCalls()[0][1]).toEqual({ q: "quick", mode: "hybrid", view: "list" });
    const link = await screen.findByRole("link", { name: /brown fox/ });
    expect(link).toHaveAttribute("href", "/videos/vid-1/watch?t=72");
    expect(link).toHaveTextContent("[1:12 → 1:18]");
    expect(within(link).getByText("quick").tagName).toBe("MARK");
    expect(loc()).toBe("/search?q=quick&mode=hybrid&sid=sid-1");
    expect(screen.getByRole("heading", { name: "In transcripts" })).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "In titles" })).toBeNull();
  });

  it("SRC-02: In titles and In transcripts show under their headings; a title opens the matches page", async () => {
    serve({
      titles: [
        titleVideo("v9", "Quick start"),
        titleVideo("v8", "No length", { duration_sec: undefined }),
      ],
      transcripts: [transcriptVideo("vid-1", "Talk", [passage(1, 3, "a <mark>quick</mark> one")])],
    });
    renderSearchPage();
    await submit("quick");
    expect(await screen.findByRole("heading", { name: "In titles" })).toBeInTheDocument();
    const titles = titlesSection();
    const link = within(titles).getByRole("link", { name: "Quick start" });
    expect(link).toHaveAttribute("href", "/search/video/v9?q=quick&mode=hybrid&sid=sid-1");
    const row = link.closest("li")!;
    expect(row).toHaveTextContent("Lectures");
    expect(row).toHaveTextContent("1:02:05");
    expect(
      within(titles).getByRole("link", { name: "No length" }).closest("li")
    ).not.toHaveTextContent(/\d:\d\d/);
    expect(screen.getByRole("heading", { name: "In transcripts" })).toBeInTheDocument();
    expect(screen.queryByText(/No results/)).toBeNull();
    await userEvent.setup().click(link);
    expect(screen.getByText("matches page")).toBeInTheDocument();
  });

  it("SRC-03: a deep link /search?q=... runs the search once on load", async () => {
    localStorage.setItem("mkt:search:mode", "fts");
    serve({
      titles: [],
      transcripts: [transcriptVideo("v1", "T", [passage(1, 1, "<mark>السيرة</mark>")])],
    });
    renderSearchPage("/search?q=%D8%A7%D9%84%D8%B3%D9%8A%D8%B1%D8%A9");
    expect(screen.getByRole("combobox", { name: "Search" })).toHaveValue("السيرة");
    expect(await screen.findByRole("link", { name: /السيرة/ })).toBeInTheDocument();
    expect(searchCalls()).toHaveLength(1);
    expect(searchCalls()[0][1]).toEqual({ q: "السيرة", mode: "fts", view: "list" });
  });

  it("SRC-04/SRC-05: a new ?q= while on /search (header search, Back/Forward) runs that query", async () => {
    serve((b) => ({ titles: [titleVideo("v1", `result for ${b.q}`)], transcripts: [] }));
    renderSearchPage("/search?q=one", { history: ["/search?q=two"] });
    expect(await screen.findByText("result for one")).toBeInTheDocument();
    await userEvent.setup().click(screen.getByRole("button", { name: "go back" }));
    expect(await screen.findByText("result for two")).toBeInTheDocument();
    expect(screen.getByRole("combobox", { name: "Search" })).toHaveValue("two");
    expect(screen.queryByText("result for one")).toBeNull();
  });

  it("SRC-05: Back to an earlier search opens it by its sid (no new POST, same order)", async () => {
    serve((b) => ({
      titles: [titleVideo("v1", `${b.q} first`), titleVideo("v2", `${b.q} second`)],
      transcripts: [transcriptVideo("r1", "Talk", [passage(1, 30, `said ${b.q}`)])],
    }));
    renderSearchPage();
    await submit("one");
    await screen.findByText("one first");
    await submit("two");
    await screen.findByText("two first");
    expect(searchCalls()).toHaveLength(2);
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "go back" }));
    expect(loc()).toBe("/search?q=one&mode=hybrid&sid=sid-1");
    expect(await screen.findByText("one first")).toBeInTheDocument();
    expect(rowTitles(titlesSection())).toEqual(["one first", "one second"]);
    expect(screen.getByRole("combobox", { name: "Search" })).toHaveValue("one");
    expect(searchCalls()).toHaveLength(2);
    expect(getCalls("/api/search/results/sid-1")).toHaveLength(2);
    // a passage click then Back opens the same saved search again
    await user.click(screen.getByRole("link", { name: /said one/ }));
    expect(screen.getByText("player page")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "go back" }));
    expect(await screen.findByText("one first")).toBeInTheDocument();
    expect(searchCalls()).toHaveLength(2);
  });

  it.each([
    ["lists null", { videos: null, transcript_videos: null, total: 0 }],
    ["lists missing", { total: 0 }],
    ["empty object", {}],
  ])("SRC-06: %s renders the empty state", async (_l, answer) => {
    serveSearch(answer);
    renderSearchPage();
    await submit("x");
    expect(
      await screen.findByText("No results. Check spelling or try a different mode.")
    ).toBeInTheDocument();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("SRC-06: a transcript video with passages null shows its row without passages", async () => {
    serveSearch({
      search_id: "",
      transcript_videos: [{ ...titleVideo("v1", "Bare"), matches: 2, passages: null }],
      transcript_total: 1,
    });
    renderSearchPage();
    await submit("x");
    expect(await screen.findByRole("link", { name: "Bare" })).toBeInTheDocument();
    expect(screen.getByText("2 matches")).toBeInTheDocument();
    // no sid: nothing to page and no Show all
    expect(screen.queryByRole("button", { name: /Show all/ })).toBeNull();
    expect(screen.queryByRole("navigation", { name: "In transcripts" })).toBeNull();
  });

  it("SRC-07: YouTube reason disabled (yt-dlp went away) shows no section", async () => {
    serveSearch({ youtube: { reason: "disabled" } });
    renderSearchPage();
    await userEvent
      .setup()
      .click(await screen.findByRole("checkbox", { name: "Also search YouTube" }));
    await submit("x");
    await screen.findByText(/No results/);
    await waitFor(() =>
      expect(get.mock.calls.some(([u]) => String(u).startsWith("/api/search/youtube?"))).toBe(true)
    );
    expect(screen.queryByRole("region", { name: "From YouTube" })).toBeNull();
  });

  it("SRC-07: without yt-dlp there is no toggle and no YouTube search", async () => {
    serveSearch({ youtube: { items: [] } }, { ytAvailable: false });
    renderSearchPage();
    await waitFor(() => expect(get).toHaveBeenCalledWith("/api/search/youtube/status"));
    expect(screen.queryByRole("checkbox", { name: "Also search YouTube" })).toBeNull();
    await submit("x");
    await screen.findByText(/No results/);
    expect(get.mock.calls.some(([u]) => String(u).startsWith("/api/search/youtube?"))).toBe(false);
  });

  it("SRC-07: YouTube items null shows the note", async () => {
    serveSearch({ youtube: { items: null } });
    renderSearchPage();
    await userEvent
      .setup()
      .click(await screen.findByRole("checkbox", { name: "Also search YouTube" }));
    await submit("x");
    const section = await screen.findByRole("region", { name: "From YouTube" });
    expect(section).toHaveTextContent("No YouTube results.");
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("SRC-07: a YouTube item with match null renders with the import button (admin)", async () => {
    serveSearch({
      youtube: {
        items: [{ youtube_id: "y1", title: "T", channel: "C", thumbnail: "", match: null }],
      },
    });
    renderSearchPage("/search", { user: adminUser });
    await userEvent
      .setup()
      .click(await screen.findByRole("checkbox", { name: "Also search YouTube" }));
    await submit("x");
    expect(await screen.findByText("T")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Import" })).toBeInTheDocument();
  });

  it("SRC-07: a passage with snippet null renders with an empty snippet", async () => {
    serveSearch(oneHit("v1", null, 0, 1));
    renderSearchPage();
    await submit("x");
    const link = await screen.findByRole("link", { name: /0:00/ });
    expect(link.querySelector(".mkt-result__snippet")).toBeEmptyDOMElement();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it.each([
    ["null lists", { items: null }, { suggestions: null }],
    ["empty objects", {}, {}],
    [
      "500s",
      () => Promise.reject(problem(500, "boom")),
      () => Promise.reject(problem(500, "boom")),
    ],
  ])("SRC-08: saved / suggest %s show nothing and do not crash", async (_l, saved, suggest) => {
    serveSearch({}, { saved, suggest });
    renderSearchPage();
    const aside = screen.getByRole("complementary", { name: "Saved searches" });
    await waitFor(() => expect(get).toHaveBeenCalledWith("/api/search/saved"));
    expect(within(aside).getByText("Nothing here yet.")).toBeInTheDocument();
    await userEvent.setup().type(screen.getByRole("combobox", { name: "Search" }), "qu");
    await waitFor(() =>
      expect(get.mock.calls.some((c) => String(c[0]).startsWith("/api/search/suggest"))).toBe(true)
    );
    await new Promise((r) => setTimeout(r, 0));
    expect(document.querySelectorAll("#mkt-suggest option")).toHaveLength(0);
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("SRC-09: render crash shows route error fallback; Back still works", async () => {
    quietCrash();
    let bad = true;
    serveSearch(() =>
      bad
        ? { videos: [{ video_id: "v1", title: {} }], videos_total: 1 }
        : { videos: [{ video_id: "v1", title: "Fine" }], videos_total: 1 }
    );
    renderSearchPage("/search?q=x");
    expect(
      await screen.findByText("This page hit an error and could not be shown.")
    ).toBeInTheDocument();
    const user = userEvent.setup();
    await user.click(screen.getByRole("link", { name: "go elsewhere" }));
    expect(screen.getByText("elsewhere")).toBeInTheDocument();
    bad = false;
    await user.click(screen.getByRole("button", { name: "go back" }));
    expect(await screen.findByRole("link", { name: "Fine" })).toBeInTheDocument();
  });

  it("SRC-10: signed-out user is sent to /login (keeping ?q) and no search runs", () => {
    serveSearch({});
    renderSearchPage("/search?q=fox", { user: null });
    expect(screen.getByText("login page (from /search?q=fox)")).toBeInTheDocument();
    expect(post).not.toHaveBeenCalled();
  });

  it("SRC-12: snippet markup is escaped; only <mark> is highlighted", async () => {
    const alert = vi.spyOn(window, "alert").mockImplementation(() => {});
    serveSearch(
      oneHit("v1", "<img src=x onerror=alert(1)> <mark>fox</mark> <b>bold</b> <mark>unclosed")
    );
    renderSearchPage();
    await submit("fox");
    const snippet = (await screen.findByRole("link", { name: /bold/ })).querySelector(
      ".mkt-result__snippet"
    )!;
    expect(snippet.querySelector("img")).toBeNull();
    expect(snippet.querySelector("b")).toBeNull();
    expect(snippet.textContent).toContain("<img src=x onerror=alert(1)>");
    expect(snippet.textContent).toContain("<b>bold</b>");
    expect(Array.from(snippet.querySelectorAll("mark")).map((m) => m.textContent)).toEqual([
      "fox",
      "unclosed",
    ]);
    for (const span of snippet.querySelectorAll("span"))
      expect(span).toHaveAttribute("dir", "auto");
    expect(alert).not.toHaveBeenCalled();
  });

  it("SRC-12: a title snippet highlights only <mark>", async () => {
    serve({
      titles: [titleVideo("v1", "x", { title_snippet: "<b>no</b> <mark>Fox</mark> talk" })],
      transcripts: [],
    });
    renderSearchPage();
    await submit("fox");
    const link = await within(await screen.findByRole("region", { name: "In titles" })).findByRole(
      "link",
      { name: /Fox talk/ }
    );
    expect(link.querySelector("b")).toBeNull();
    expect(link.querySelector("mark")).toHaveTextContent("Fox");
  });

  it("SRC-14: a blank query does nothing; clearing removes ?q and the old results", async () => {
    serveSearch({ ...oneHit("v1", "fox result") });
    renderSearchPage("/search?q=fox");
    await screen.findByText("fox result");
    post.mockClear();
    await submit("   ");
    await userEvent.setup().click(screen.getByRole("button", { name: "Save" }));
    expect(post).not.toHaveBeenCalled();
    expect(loc()).toBe("/search");
    expect(screen.queryByText("fox result")).toBeNull();
  });

  it("SRC-14: spaces are trimmed in the URL and the request body", async () => {
    serveSearch({});
    renderSearchPage();
    await submit("  fox  ");
    expect(searchCalls()[0][1]).toMatchObject({ q: "fox" });
    expect(loc()).toBe("/search?q=fox&mode=hybrid");
  });

  it("SRC-15: pending shows loading; the latest query wins", async () => {
    const resolvers: Record<string, (v: unknown) => void> = {};
    serveSearch((b: Record<string, unknown>) => new Promise((r) => (resolvers[String(b.q)] = r)));
    renderSearchPage();
    await submit("a");
    expect(screen.getByRole("button", { name: /^search$/i })).toHaveAttribute("aria-busy", "true");
    const box = screen.getByRole("combobox", { name: "Search" });
    fireEvent.change(box, { target: { value: "b" } });
    fireEvent.submit(box.closest("form")!);
    await waitFor(() => expect(resolvers.b).toBeDefined());
    resolvers.b(oneHit("v2", "b result"));
    expect(await screen.findByText("b result")).toBeInTheDocument();
    resolvers.a(oneHit("v1", "a result"));
    await new Promise((r) => setTimeout(r, 0));
    expect(screen.queryByText("a result")).toBeNull();
    expect(screen.getByText("b result")).toBeInTheDocument();
  });

  it.each([
    ["500", problem(500, "search backend down"), en["error.kind.server"]],
    // a 400 with only a title: the title is the server's words, not shown
    ["400", problem(400, "query too long"), en["error.kind.server"]],
    ["network failure", new TypeError("Failed to fetch"), en["error.kind.unreachable"]],
    ["malformed JSON", new SyntaxError("Unexpected token b"), en["error.kind.server"]],
  ])("SRC-16: search %s shows the ErrorState and clears results", async (_l, e, desc) => {
    let fail = false;
    serveSearch(() => (fail ? Promise.reject(e) : oneHit("v1", "old result")));
    renderSearchPage();
    await submit("old");
    await screen.findByText("old result");
    fail = true;
    await submit("fox");
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Something went wrong.");
    expect(alert).toHaveTextContent(desc);
    expect(screen.queryByText("old result")).toBeNull();
    expect(screen.queryByText(/No results/)).toBeNull();
    expect(screen.getByRole("combobox", { name: "Search" })).toBeEnabled();
  });

  it("SRC-17: the error clears on the next successful search", async () => {
    serveSearch((b: Record<string, unknown>) =>
      b.q === "a" ? Promise.reject(problem(500, "boom")) : oneHit("v1", "b hit")
    );
    renderSearchPage();
    await submit("a");
    await screen.findByRole("alert");
    await submit("b");
    expect(await screen.findByText("b hit")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("SRC-18: mode select persists and is sent", async () => {
    serveSearch({});
    const first = renderSearchPage();
    const select = screen.getByRole("combobox", { name: "Mode" });
    expect(Array.from((select as HTMLSelectElement).options).map((o) => o.textContent)).toEqual([
      "Hybrid",
      "Keyword",
      "Semantic",
    ]);
    await userEvent.setup().selectOptions(select, "semantic");
    await submit("x");
    expect(searchCalls()[0][1]).toMatchObject({ mode: "semantic" });
    expect(loc()).toBe("/search?q=x&mode=semantic");
    expect(localStorage.getItem("mkt:search:mode")).toBe("semantic");
    first.unmount();
    renderSearchPage();
    expect(screen.getByRole("combobox", { name: "Mode" })).toHaveValue("semantic");
  });

  it("SRC-18: the mode in the URL wins over the stored one", async () => {
    localStorage.setItem("mkt:search:mode", "semantic");
    serveSearch({});
    renderSearchPage("/search?q=x&mode=fts");
    await waitFor(() => expect(searchCalls()).toHaveLength(1));
    expect(searchCalls()[0][1]).toMatchObject({ q: "x", mode: "fts" });
    expect(screen.getByRole("combobox", { name: "Mode" })).toHaveValue("fts");
  });

  it("SRC-18: a bogus or throwing stored mode falls back to Hybrid", () => {
    serveSearch({});
    localStorage.setItem("mkt:search:mode", "bogus");
    const first = renderSearchPage();
    expect(screen.getByRole("combobox", { name: "Mode" })).toHaveValue("hybrid");
    first.unmount();
    vi.spyOn(localStorage, "getItem").mockImplementation(() => {
      throw new Error("denied");
    });
    renderSearchPage();
    expect(screen.getByRole("combobox", { name: "Mode" })).toHaveValue("hybrid");
  });

  it("SRC-19: suggestions debounce, encode, and drop stale answers", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const pending: Record<string, (v: unknown) => void> = {};
    serveSearch(
      {},
      {
        suggest: ({ url }: Record<string, unknown>) =>
          new Promise((r) => (pending[String(url)] = r)),
      }
    );
    renderSearchPage();
    const box = screen.getByRole("combobox", { name: "Search" });
    const suggestCalls = () => getCalls("/api/search/suggest");
    fireEvent.change(box, { target: { value: "q" } });
    fireEvent.change(box, { target: { value: "qu" } });
    act(() => {
      vi.advanceTimersByTime(199);
    });
    expect(suggestCalls()).toHaveLength(0);
    act(() => {
      vi.advanceTimersByTime(1);
    });
    expect(suggestCalls()).toEqual(["/api/search/suggest?q=qu"]);
    fireEvent.change(box, { target: { value: "qui" } });
    act(() => {
      vi.advanceTimersByTime(250);
    });
    await act(async () => pending["/api/search/suggest?q=qui"]({ suggestions: ["quick", "quiz"] }));
    await act(async () => pending["/api/search/suggest?q=qu"]({ suggestions: ["quiet"] }));
    const options = () =>
      Array.from(document.querySelectorAll("#mkt-suggest option")).map((o) =>
        o.getAttribute("value")
      );
    expect(options()).toEqual(["quick", "quiz"]);
    fireEvent.change(box, { target: { value: "سي ر" } });
    act(() => {
      vi.advanceTimersByTime(200);
    });
    expect(suggestCalls().at(-1)).toBe("/api/search/suggest?q=%D8%B3%D9%8A%20%D8%B1");
    fireEvent.submit(box.closest("form")!);
    expect(options()).toEqual([]);
  });

  it("SRC-19: unmounting during the debounce sends no request", () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    serveSearch({});
    const r = renderSearchPage();
    fireEvent.change(screen.getByRole("combobox", { name: "Search" }), {
      target: { value: "qu" },
    });
    r.unmount();
    act(() => {
      vi.advanceTimersByTime(300);
    });
    expect(getCalls("/api/search/suggest")).toHaveLength(0);
  });

  it("SRC-20: save, run and delete saved searches", async () => {
    let items = [{ id: "s1", q: "fox", mode: "fts" }];
    serveSearch({}, { saved: () => ({ items }) });
    del.mockResolvedValue(undefined);
    renderSearchPage();
    const user = userEvent.setup();
    const aside = screen.getByRole("complementary", { name: "Saved searches" });
    await within(aside).findByRole("button", { name: "fox" });
    post.mockImplementation((url: string) => {
      if (url === "/api/search/save") items = [...items, { id: "s2", q: "dog", mode: "hybrid" }];
      return Promise.resolve({});
    });
    await user.type(screen.getByRole("combobox", { name: "Search" }), " dog ");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(post).toHaveBeenCalledWith("/api/search/save", { q: "dog", mode: "hybrid" });
    expect(await within(aside).findByRole("button", { name: "dog" })).toBeInTheDocument();
    await user.click(within(aside).getByRole("button", { name: "fox" }));
    expect(screen.getByRole("combobox", { name: "Search" })).toHaveValue("fox");
    expect(screen.getByRole("combobox", { name: "Mode" })).toHaveValue("fts");
    expect(post).toHaveBeenLastCalledWith("/api/search", { q: "fox", mode: "fts", view: "list" });
    expect(loc()).toBe("/search?q=fox&mode=fts");
    items = items.filter((i) => i.id !== "s1");
    await user.click(within(aside).getByRole("button", { name: "Delete fox" }));
    expect(del).toHaveBeenCalledWith("/api/search/saved/s1");
    await waitFor(() => expect(within(aside).queryByRole("button", { name: "fox" })).toBeNull());
  });

  it("SRC-20: a failed save shows an error toast; an unknown saved mode falls back", async () => {
    serveSearch(
      {},
      {
        saved: { items: [{ id: "s9", q: "odd", mode: "weird" }] },
        other: () => Promise.reject(problem(500, "save failed")),
      }
    );
    renderSearchPage();
    const user = userEvent.setup();
    await user.type(screen.getByRole("combobox", { name: "Search" }), "dog");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByText(en["error.kind.server"])).toBeInTheDocument();
    expect(screen.queryByText("save failed")).toBeNull();
    await user.click(await screen.findByRole("button", { name: "odd" }));
    expect(screen.getByRole("combobox", { name: "Mode" })).toHaveValue("hybrid");
  });

  it.todo("SRC-20: a successful save shows a confirmation (needs an i18n key)");

  it("SRC-21: YouTube block: results, in-library link, reason text", async () => {
    serveSearch({
      youtube: {
        items: [
          {
            youtube_id: "abc&x",
            title: "عنوان",
            channel: "Chan",
            description_snippet: "",
            thumbnail: "https://i.ytimg.com/vi/abc/hq.jpg",
            view_count: 12345,
            match: { state: "in_library", video_id: "v5" },
          },
          {
            youtube_id: "def",
            title: "Other",
            channel: "C2",
            thumbnail: "",
            match: { state: "none" },
          },
        ],
      },
    });
    renderSearchPage("/search", { user: adminUser });
    const user = userEvent.setup();
    await user.click(await screen.findByRole("checkbox", { name: "Also search YouTube" }));
    await submit("x");
    expect(searchCalls()[0][1]).not.toHaveProperty("include");
    expect(get).toHaveBeenCalledWith("/api/search/youtube?q=x");
    const section = await screen.findByRole("region", { name: "From YouTube" });
    const first = within(section).getByRole("link", { name: /عنوان/ });
    expect(first).toHaveAttribute("href", "https://www.youtube.com/watch?v=abc%26x");
    expect(first).toHaveAttribute("target", "_blank");
    expect(first).toHaveAttribute("rel", "noopener noreferrer");
    expect(first).toHaveTextContent("Chan · 12,345");
    expect(within(section).getByRole("link", { name: "In your library →" })).toHaveAttribute(
      "href",
      "/videos/v5"
    );
    const second = within(section).getByRole("link", { name: /Other/ });
    expect(second.querySelector("img")).toBeNull();
    expect(within(section).getAllByRole("button", { name: "Import" })).toHaveLength(1);
  });

  it.each([
    ["error", "YouTube is unavailable right now."],
    ["quota", "No YouTube results."],
  ])("SRC-21: reason %s shows readable text", async (reason, text) => {
    serveSearch({ youtube: { items: [], reason } });
    renderSearchPage();
    await userEvent
      .setup()
      .click(await screen.findByRole("checkbox", { name: "Also search YouTube" }));
    await submit("x");
    expect(await screen.findByText(text)).toBeInTheDocument();
    expect(document.body.textContent).not.toContain("search.youtube.reason");
  });

  it("SRC-21: with the toggle off there is no YouTube section", async () => {
    serveSearch({ youtube: { items: [] } });
    renderSearchPage();
    await submit("x");
    await screen.findByText(/No results/);
    expect(screen.queryByRole("region", { name: "From YouTube" })).toBeNull();
  });

  it("SRC-22: YouTube import dialog: cancel, success, error; hidden for non-admins", async () => {
    const yt = {
      videos: [{ video_id: "v7", title: "Lesson" }],
      videos_total: 1,
      youtube: {
        items: [
          {
            youtube_id: "defdefdefde",
            title: "Other",
            channel: "C2",
            description_snippet: "d",
            thumbnail: "",
            match: { state: "none" },
          },
        ],
      },
    };
    const importPost = vi.fn();
    serveSearch(yt, { other: ({ url }: Record<string, unknown>) => importPost(url) });
    const prompt = vi.spyOn(window, "prompt");
    const r = renderSearchPage("/search", { user: adminUser });
    const user = userEvent.setup();
    await user.click(await screen.findByRole("checkbox", { name: "Also search YouTube" }));
    await submit("x");
    const btn = await screen.findByRole("button", { name: "Import" });

    await user.click(btn);
    let dialog = await screen.findByRole("dialog");
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    expect(importPost).not.toHaveBeenCalled();

    importPost.mockResolvedValueOnce({ staged: true });
    await user.click(btn);
    dialog = await screen.findByRole("dialog");
    await user.click(within(dialog).getByLabelText(/Lesson/));
    await user.click(within(dialog).getByRole("button", { name: "Import" }));
    expect(post).toHaveBeenCalledWith(
      "/api/videos/v7/import-youtube",
      expect.objectContaining({ youtube_id: "defdefdefde", title: "Other", channel: "C2" })
    );
    expect(await screen.findByText(/Metadata staged for “Lesson”/)).toBeInTheDocument();

    importPost.mockRejectedValueOnce(problem(404, "video not found"));
    await user.click(await screen.findByRole("button", { name: "Import" }));
    dialog = await screen.findByRole("dialog");
    await user.click(within(dialog).getByLabelText(/Lesson/));
    await user.click(within(dialog).getByRole("button", { name: "Import" }));
    expect(await screen.findByText(en["error.kind.notFound"])).toBeInTheDocument();
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(prompt).not.toHaveBeenCalled();

    r.unmount();
    renderSearchPage();
    await userEvent
      .setup()
      .click(await screen.findByRole("checkbox", { name: "Also search YouTube" }));
    await submit("x");
    await screen.findByText("Other");
    expect(screen.queryByRole("button", { name: "Import" })).toBeNull();
  });

  it("SRC-23: Arabic query, results and UI", async () => {
    localStorage.setItem("mkt:locale", "ar");
    serve({
      titles: [titleVideo("v2", "السيرة النبوية ( 18 )")],
      transcripts: [
        transcriptVideo("v1", "درس", [passage(1, 5, "من <mark>السيرة</mark> النبوية")]),
      ],
    });
    renderSearchPage();
    await waitFor(() => expect(document.documentElement).toHaveAttribute("dir", "rtl"));
    const user = userEvent.setup();
    const box = document.querySelector<HTMLInputElement>('input[type="search"]')!;
    await user.type(box, "السيرة النبوية");
    fireEvent.submit(box.closest("form")!);
    expect(searchCalls()[0][1]).toMatchObject({ q: "السيرة النبوية" });
    expect(await screen.findByText("السيرة النبوية ( 18 )")).toHaveAttribute("dir", "auto");
    expect(locParams().get("q")).toBe("السيرة النبوية");
    expect(screen.getByRole("heading", { name: "في العناوين" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "في التفريغات" })).toBeInTheDocument();
    expect(screen.getByText("مطابقة واحدة")).toBeInTheDocument();
    expect(screen.getAllByText(/الصفحة ١ من ١/)).toHaveLength(2);
    expect(screen.queryByText("Saved searches")).toBeNull();
    expect(screen.getByRole("complementary", { name: "عمليات البحث الأخيرة" })).toBeInTheDocument();
    const time = document.querySelector(".mkt-result__time")!;
    expect(time.textContent).toContain("←");
  });
});

describe("Search results redesign (test-cases/search.md SRC-28..)", () => {
  it("SRC-28: In transcripts has one row per video with its library, matches, top passages and the also-in-title badge", async () => {
    const many = Array.from({ length: 7 }, (_, i) =>
      passage(100 + i, 10 * (i + 1), `hit <mark>fox</mark> ${i + 1}`)
    );
    serve({
      titles: [titleVideo("v1", "Fox one")],
      transcripts: [
        transcriptVideo("v1", "Fox one", many, { in_title: true }),
        transcriptVideo("v2", "Other talk", [passage(200, 90, "a <mark>fox</mark> here")]),
      ],
    });
    renderSearchPage();
    await submit("fox");
    const section = await screen.findByRole("region", { name: "In transcripts" });
    const rows = within(section)
      .getAllByRole("listitem")
      .filter((li) => li.matches(".mkt-svideo"));
    expect(rows).toHaveLength(2);
    expect(rowTitles(section)).toEqual(["Fox one", "Other talk"]);
    const first = rows[0]!;
    expect(within(first).getByText("7 matches")).toBeInTheDocument();
    expect(within(first).getByText("Also in title")).toBeInTheDocument();
    expect(within(first).getByText("Lectures")).toBeInTheDocument();
    expect(within(first).getByText("1:02:05")).toBeInTheDocument();
    const links = within(first).getAllByRole("link", { name: /hit fox/ });
    expect(links.map((a) => a.getAttribute("href"))).toEqual([
      "/videos/v1/watch?t=10",
      "/videos/v1/watch?t=20",
      "/videos/v1/watch?t=30",
    ]);
    expect(within(rows[1]!).getByText("1 match")).toBeInTheDocument();
    expect(within(rows[1]!).queryByText("Also in title")).toBeNull();
    expect(within(rows[1]!).queryByRole("button", { name: /Show all/ })).toBeNull();
    // poster and title both open the matches page
    const toMatches = within(first)
      .getAllByRole("link", { hidden: true })
      .filter((a) => a.getAttribute("href")?.startsWith("/search/video/v1?"));
    expect(toMatches).toHaveLength(2);
    expect(first.querySelector("img")).toHaveAttribute("src", "/api/videos/v1/poster");
  });

  it("SRC-29: Show all loads every passage of the video (best first, paged), then Show fewer", async () => {
    const all = Array.from({ length: 5 }, (_, i) =>
      passage(300 + i, 100 - i * 10, `passage <mark>${i + 1}</mark>`)
    );
    serve(
      { titles: [], transcripts: [transcriptVideo("v1", "Long talk", all)] },
      { passagesPageSize: 2 }
    );
    renderSearchPage();
    await submit("fox");
    const section = await screen.findByRole("region", { name: "In transcripts" });
    expect(within(section).getAllByRole("link", { name: /passage/ })).toHaveLength(3);
    const user = userEvent.setup();
    await user.click(within(section).getByRole("button", { name: "Show all 5" }));
    await waitFor(() =>
      expect(within(section).queryByRole("button", { name: "Show all 5" })).toBeNull()
    );
    expect(getCalls("/api/search/passages")).toEqual([
      "/api/search/passages?sid=sid-1&video_id=v1&order=score&offset=0&limit=2",
    ]);
    expect(within(section).getAllByRole("link", { name: /passage/ })).toHaveLength(2);
    await user.click(within(section).getByRole("button", { name: "Load more" }));
    await waitFor(() =>
      expect(within(section).getAllByRole("link", { name: /passage/ })).toHaveLength(4)
    );
    await user.click(within(section).getByRole("button", { name: "Load more" }));
    await waitFor(() =>
      expect(within(section).getAllByRole("link", { name: /passage/ })).toHaveLength(5)
    );
    expect(within(section).queryByRole("button", { name: "Load more" })).toBeNull();
    expect(
      within(section)
        .getAllByRole("link", { name: /passage/ })
        .map((a) => a.textContent?.replace(/\[.*\]/, "").trim())
    ).toEqual(["passage 1", "passage 2", "passage 3", "passage 4", "passage 5"]);
    await user.click(within(section).getByRole("button", { name: "Show fewer" }));
    expect(within(section).getAllByRole("link", { name: /passage/ })).toHaveLength(3);
  });

  it("SRC-30: the List / Grid toggle switches both lists, is sent as view and is remembered", async () => {
    serve({
      titles: [titleVideo("v1", "Fox title")],
      transcripts: [transcriptVideo("v2", "Fox talk", [passage(1, 61, "the <mark>fox</mark>")])],
    });
    const first = renderSearchPage();
    await submit("fox");
    const group = await screen.findByRole("radiogroup", { name: "View" });
    expect(within(group).getByRole("radio", { name: "List" })).toHaveAttribute(
      "aria-checked",
      "true"
    );
    // list view: library name and passages are on the row
    expect(within(titlesSection()).getByText("Lectures")).toBeInTheDocument();
    expect(within(transcriptsSection()).getByRole("link", { name: /the fox/ })).toBeInTheDocument();

    const user = userEvent.setup();
    await user.click(within(group).getByRole("radio", { name: "Grid" }));
    expect(localStorage.getItem("mkt:search:view")).toBe("grid");
    expect(titlesSection().querySelector(".mkt-sresults--grid")).not.toBeNull();
    expect(transcriptsSection().querySelector(".mkt-sresults--grid")).not.toBeNull();
    const card = transcriptsSection().querySelector(".mkt-scard") as HTMLElement;
    expect(within(card).getByText("1 match")).toBeInTheDocument();
    expect(within(card).getByText("1:02:05")).toBeInTheDocument();
    // passages open on demand
    expect(within(card).queryByRole("link", { name: /the fox/ })).toBeNull();
    const toggle = within(card).getByRole("button", { name: "Show passages" });
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    await user.click(toggle);
    expect(within(card).getByRole("link", { name: /the fox/ })).toHaveAttribute(
      "href",
      "/videos/v2/watch?t=61"
    );
    expect(within(card).getByRole("button", { name: "Hide passages" })).toHaveAttribute(
      "aria-expanded",
      "true"
    );

    first.unmount();
    renderSearchPage();
    await submit("fox");
    expect(await screen.findByRole("radio", { name: "Grid" })).toHaveAttribute(
      "aria-checked",
      "true"
    );
    expect(searchCalls().at(-1)![1]).toEqual({ q: "fox", mode: "hybrid", view: "grid" });
  });

  it("SRC-30: a blocked localStorage still shows the list view", async () => {
    vi.spyOn(localStorage, "getItem").mockImplementation(() => {
      throw new Error("denied");
    });
    vi.spyOn(localStorage, "setItem").mockImplementation(() => {
      throw new Error("denied");
    });
    serve({ titles: [titleVideo("v1", "Fox")], transcripts: [] });
    renderSearchPage();
    await submit("fox");
    expect(await screen.findByRole("radio", { name: "List" })).toHaveAttribute(
      "aria-checked",
      "true"
    );
    await userEvent.setup().click(screen.getByRole("radio", { name: "Grid" }));
    expect(screen.getByRole("radio", { name: "Grid" })).toHaveAttribute("aria-checked", "true");
  });

  it("SRC-31: each list pages on its own: totals, page X of Y, stable order, URL vp / tp", async () => {
    serve({ titles: manyTitles(45), transcripts: manyTranscripts(25) }, { pageSize: 20 });
    renderSearchPage();
    await submit("fox");
    await screen.findByRole("heading", { name: "In titles" });
    expect(within(titlesSection()).getByTestId("pageinfo-titles")).toHaveTextContent(
      "Page 1 of 3 · 45 videos"
    );
    expect(within(transcriptsSection()).getByTestId("pageinfo-transcripts")).toHaveTextContent(
      "Page 1 of 2 · 25 videos"
    );
    expect(rowTitles(titlesSection())).toHaveLength(20);
    expect(rowTitles(titlesSection())[0]).toBe("Title 1");

    const user = userEvent.setup();
    const titlesNav = within(titlesSection()).getByRole("navigation", { name: "In titles" });
    await user.click(within(titlesNav).getByRole("button", { name: "Page 2" }));
    await waitFor(() => expect(rowTitles(titlesSection())[0]).toBe("Title 21"));
    expect(rowTitles(titlesSection())).toEqual(
      manyTitles(40)
        .slice(20)
        .map((v) => v.title)
    );
    expect(locParams().get("vp")).toBe("2");
    expect(locParams().get("sid")).toBe("sid-1");
    expect(getCalls("/api/search/results/")).toEqual([
      "/api/search/results/sid-1?list=titles&offset=20&limit=20",
    ]);
    expect(within(titlesSection()).getByTestId("pageinfo-titles")).toHaveTextContent(
      "Page 2 of 3 · 45 videos"
    );
    // the other list keeps its page
    expect(rowTitles(transcriptsSection())[0]).toBe("Talk 1");

    const trNav = within(transcriptsSection()).getByRole("navigation", { name: "In transcripts" });
    await user.click(within(trNav).getByRole("button", { name: "Next page" }));
    await waitFor(() =>
      expect(rowTitles(transcriptsSection())).toEqual([
        "Talk 21",
        "Talk 22",
        "Talk 23",
        "Talk 24",
        "Talk 25",
      ])
    );
    expect(locParams().get("tp")).toBe("2");
    expect(locParams().get("vp")).toBe("2");
    // matches-page links carry both pages
    expect(within(transcriptsSection()).getByRole("link", { name: "Talk 21" })).toHaveAttribute(
      "href",
      "/search/video/r21?q=fox&mode=hybrid&sid=sid-1&vp=2&tp=2"
    );
    // last page of titles
    await user.click(within(titlesNav).getByRole("button", { name: "Page 3" }));
    await waitFor(() => expect(rowTitles(titlesSection())).toHaveLength(5));
    expect(within(titlesNav).getByRole("button", { name: "Next page" })).toBeDisabled();

    // Back steps through the page changes (pushed history entries)
    await user.click(screen.getByRole("button", { name: "go back" }));
    await waitFor(() => expect(rowTitles(titlesSection())[0]).toBe("Title 21"));
    expect(locParams().get("vp")).toBe("2");
    expect(searchCalls()).toHaveLength(1);
  });

  it("SRC-32: a reload with sid and pages reads those pages by sid (no POST)", async () => {
    const corpus = { titles: manyTitles(45), transcripts: manyTranscripts(3) };
    sessionStorage.setItem(
      "mkt:search:sizes",
      JSON.stringify({ "sid-old": { pageSize: 20, passagesPageSize: 20 } })
    );
    serve(corpus, { existing: { "sid-old": { q: "fox", mode: "fts", corpus } } });
    renderSearchPage("/search?q=fox&mode=fts&sid=sid-old&vp=3");
    await waitFor(() => expect(rowTitles(titlesSection())).toHaveLength(5));
    expect(rowTitles(titlesSection())[0]).toBe("Title 41");
    expect(searchCalls()).toHaveLength(0);
    expect(getCalls("/api/search/results/").sort()).toEqual([
      "/api/search/results/sid-old?list=titles&offset=40&limit=20",
      "/api/search/results/sid-old?list=transcripts&offset=0&limit=20",
    ]);
    expect(screen.getByRole("combobox", { name: "Mode" })).toHaveValue("fts");
    expect(screen.queryByRole("status")).toBeNull();
  });

  it("SRC-33: an expired sid runs the search again, keeps the pages (clamped) and never mixes sids", async () => {
    // the old snapshot had 65 titles; the new run finds 30 (2 pages)
    let runs = 0;
    serve(() => {
      runs += 1;
      return { titles: manyTitles(30, "New"), transcripts: manyTranscripts(45, 1, "Talk") };
    });
    renderSearchPage("/search?q=fox&mode=hybrid&sid=gone&vp=4&tp=2");
    await waitFor(() => expect(locParams().get("sid")).toBe("sid-1"));
    expect(runs).toBe(1);
    expect(searchCalls()[0][1]).toEqual({ q: "fox", mode: "hybrid", view: "list" });
    expect(locParams().get("vp")).toBe("2");
    expect(locParams().get("tp")).toBe("2");
    await waitFor(() => expect(rowTitles(titlesSection())[0]).toBe("New 21"));
    expect(rowTitles(transcriptsSection())[0]).toBe("Talk 21");
    // every read after the 410s is from the new sid
    const reads = getCalls("/api/search/results/");
    const after = reads.slice(reads.findIndex((u) => u.includes("sid-1")));
    expect(after.every((u) => u.includes("/sid-1?"))).toBe(true);
    const status = screen.getByRole("status");
    expect(status).toHaveTextContent("These results were refreshed; the order may have changed.");
    const user = userEvent.setup();
    await user.click(within(status).getByRole("button", { name: "Back to page 1" }));
    await waitFor(() => expect(rowTitles(titlesSection())[0]).toBe("New 1"));
    expect(loc()).toBe("/search?q=fox&mode=hybrid&sid=sid-1");
    expect(screen.queryByRole("status")).toBeNull();
    expect(runs).toBe(1);
  });

  it("SRC-34: a page change on an expired search refreshes, shows the asked page of the new sid and the notice", async () => {
    const state = serve({ titles: manyTitles(45), transcripts: manyTranscripts(2) });
    renderSearchPage();
    await submit("fox");
    await screen.findByRole("heading", { name: "In titles" });
    state.expired.add("sid-1");
    const user = userEvent.setup();
    await user.click(
      within(within(titlesSection()).getByRole("navigation", { name: "In titles" })).getByRole(
        "button",
        { name: "Page 2" }
      )
    );
    await waitFor(() => expect(locParams().get("sid")).toBe("sid-2"));
    expect(locParams().get("vp")).toBe("2");
    await waitFor(() => expect(rowTitles(titlesSection())[0]).toBe("Title 21"));
    expect(screen.getByRole("status")).toHaveTextContent("These results were refreshed");
    await user.click(screen.getByRole("button", { name: "Dismiss" }));
    expect(screen.queryByRole("status")).toBeNull();
    expect(searchCalls()).toHaveLength(2);
  });

  it("SRC-34: Show all on an expired search refreshes the results", async () => {
    const all = Array.from({ length: 5 }, (_, i) => passage(i + 1, i * 10, `p ${i}`));
    const state = serve({ titles: [], transcripts: [transcriptVideo("v1", "Talk", all)] });
    renderSearchPage();
    await submit("fox");
    const section = await screen.findByRole("region", { name: "In transcripts" });
    state.expired.add("sid-1");
    await userEvent.setup().click(within(section).getByRole("button", { name: "Show all 5" }));
    await waitFor(() => expect(locParams().get("sid")).toBe("sid-2"));
    expect(await screen.findByRole("status")).toHaveTextContent("These results were refreshed");
    expect(
      within(transcriptsSection()).getByRole("button", { name: "Show all 5" })
    ).toBeInTheDocument();
  });

  it("SRC-35: Recent searches: shown with mode, time, counts and state; live opens by sid, expired runs again", async () => {
    const corpus = {
      titles: [titleVideo("a1", "Old A"), titleVideo("a2", "Old B")],
      transcripts: [],
    };
    const fiveMinAgo = new Date(Date.now() - 5 * 60_000).toISOString();
    const state = serve(
      { titles: [titleVideo("n1", "Fresh")], transcripts: [] },
      {
        existing: { "sid-live": { q: "kahf", mode: "fts", corpus } },
        recent: [
          {
            search_id: "sid-live",
            q: "kahf",
            mode: "fts",
            created_at: fiveMinAgo,
            title_count: 2,
            transcript_count: 0,
            live: true,
          },
          {
            search_id: "sid-dead",
            q: "maryam",
            mode: "semantic",
            created_at: "2026-01-01T10:00:00Z",
            title_count: 4,
            transcript_count: 7,
            live: false,
          },
        ],
      }
    );
    renderSearchPage();
    const recent = screen.getByRole("complementary", { name: "Recent searches" });
    const live = await within(recent).findByRole("button", { name: /^kahf/ });
    expect(live).toHaveTextContent("Keyword");
    expect(live).toHaveTextContent("5 minutes ago");
    expect(live).toHaveTextContent("2 in titles · 0 in transcripts");
    expect(live).toHaveTextContent("Results kept");
    const dead = within(recent).getByRole("button", { name: /^maryam/ });
    expect(dead).toHaveTextContent("Semantic");
    expect(dead).toHaveTextContent("4 in titles · 7 in transcripts");
    expect(dead).toHaveTextContent("Expired, runs again");
    // Saved searches stay a separate panel
    expect(screen.getByRole("complementary", { name: "Saved searches" })).not.toContainElement(
      live
    );

    const user = userEvent.setup();
    await user.click(live);
    await waitFor(() => expect(rowTitles(titlesSection())).toEqual(["Old A", "Old B"]));
    expect(loc()).toBe("/search?q=kahf&mode=fts&sid=sid-live");
    expect(searchCalls()).toHaveLength(0);
    expect(screen.getByRole("combobox", { name: "Search" })).toHaveValue("kahf");

    await user.click(within(recent).getByRole("button", { name: /^maryam/ }));
    await waitFor(() => expect(searchCalls()).toHaveLength(1));
    expect(searchCalls()[0][1]).toEqual({ q: "maryam", mode: "semantic", view: "list" });
    await waitFor(() => expect(loc()).toBe("/search?q=maryam&mode=semantic&sid=sid-1"));
    // the list is read again after each search
    expect(getCalls("/api/search/recent").length).toBeGreaterThanOrEqual(3);

    await user.click(within(recent).getByRole("button", { name: "Remove kahf" }));
    expect(del).toHaveBeenCalledWith("/api/search/recent/sid-live");
    await waitFor(() => expect(within(recent).queryByRole("button", { name: /^kahf/ })).toBeNull());
    expect(state.recent).toHaveLength(1);
    await user.click(within(recent).getByRole("button", { name: "Clear all" }));
    expect(del).toHaveBeenCalledWith("/api/search/recent");
    expect(await within(recent).findByText("Nothing here yet.")).toBeInTheDocument();
  });

  it("SRC-35: a live recent search that expired meanwhile runs again with the notice", async () => {
    serve(
      { titles: [titleVideo("n1", "Fresh")], transcripts: [] },
      {
        recent: [
          {
            search_id: "sid-stale",
            q: "kahf",
            mode: "hybrid",
            created_at: "2026-10-22T09:00:00Z",
            title_count: 1,
            transcript_count: 0,
            live: true,
          },
        ],
      }
    );
    renderSearchPage();
    const recent = screen.getByRole("complementary", { name: "Recent searches" });
    await userEvent.setup().click(await within(recent).findByRole("button", { name: /^kahf/ }));
    await waitFor(() => expect(loc()).toBe("/search?q=kahf&mode=hybrid&sid=sid-1"));
    expect(await screen.findByText("Fresh")).toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent("These results were refreshed");
  });
});
