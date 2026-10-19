// R4.1 — Search must speak the real server contract:
//   POST /api/search  { q, mode, limit }
//   -> { hits: [{ segment_id, video_id, start_sec, end_sec, snippet, score }], total, ... }
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, useLocation } from "react-router-dom";

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
import { I18nProvider } from "../lib/i18n";
import { ToastProvider } from "@ds/components/Toast/Toast";
import { ApiError } from "../lib/api";
import { TestAuth, adminUser, memberUser, quietCrash, renderPage } from "../test/pageHarness";

function renderSearch() {
  return render(
    <MemoryRouter>
      <I18nProvider>
        <TestAuth user={memberUser}>
          <ToastProvider>
            <Search />
          </ToastProvider>
        </TestAuth>
      </I18nProvider>
    </MemoryRouter>
  );
}

describe("Search", () => {
  beforeEach(() => {
    post.mockReset();
    get.mockReset();
    del.mockReset();
    // Saved-searches sidebar fetch on mount.
    get.mockResolvedValue({ items: [], suggestions: [] });
  });

  it("POSTs /api/search with { q, mode, limit } and renders server hits", async () => {
    post.mockResolvedValue({
      hits: [
        {
          segment_id: 42,
          video_id: "vid-1",
          start_sec: 12.5,
          end_sec: 18.0,
          snippet: "the <mark>quick</mark> brown fox",
          score: 0.9,
        },
      ],
      total: 1,
      took_ms: { fts: 3, semantic: 0, fusion: 1 },
      mode: "hybrid",
      filters: {},
    });

    renderSearch();
    const user = userEvent.setup();
    await user.type(screen.getByRole("combobox", { name: /search/i }), "quick");
    await user.click(screen.getByRole("button", { name: /^search$/i }));

    await waitFor(() => {
      expect(post).toHaveBeenCalledTimes(1);
    });
    const [path, body] = post.mock.calls[0];
    expect(path).toBe("/api/search");
    expect(body).toMatchObject({ q: "quick" });
    expect(body).toHaveProperty("mode");
    expect(body).toHaveProperty("limit");

    // Hit content from the server `snippet` is rendered.
    await waitFor(() => {
      expect(screen.getByText(/brown fox/)).toBeInTheDocument();
    });
  });

  it("renders the empty state when the server returns no hits", async () => {
    post.mockResolvedValue({
      hits: [],
      total: 0,
      took_ms: { fts: 1, semantic: 0, fusion: 0 },
      mode: "hybrid",
      filters: {},
    });

    renderSearch();
    const user = userEvent.setup();
    await user.type(screen.getByRole("combobox", { name: /search/i }), "nothing");
    await user.click(screen.getByRole("button", { name: /^search$/i }));

    await waitFor(() => {
      expect(screen.getByText(/no results/i)).toBeInTheDocument();
    });
  });

  it("lists title matches, linked to the video, when no transcript matches", async () => {
    post.mockResolvedValue({
      hits: [],
      videos: [{ video_id: "vid-9", title: "السيرة النبوية ( 18 )", duration_sec: 3600 }],
      total: 0,
      took_ms: { fts: 1, semantic: 0, fusion: 0 },
      mode: "hybrid",
      filters: {},
    });

    renderSearch();
    const user = userEvent.setup();
    await user.type(screen.getByRole("combobox", { name: /search/i }), "السيرة");
    await user.click(screen.getByRole("button", { name: /^search$/i }));

    const link = await screen.findByRole("link", { name: /السيرة النبوية/ });
    expect(link).toHaveAttribute("href", "/videos/vid-9");
    expect(screen.queryByText(/No results/)).not.toBeInTheDocument();
  });
});

// ─── Cases from web/test-cases/search.md ───────────────────────────────

type Answer = unknown | ((body: Record<string, unknown>) => unknown);

const hit = (id: number, video: string, snippet: string | null, start = 72.5, end = 78) => ({
  segment_id: id,
  video_id: video,
  start_sec: start,
  end_sec: end,
  snippet,
  score: 1,
});

/** Routes api.post / api.get by path. `search` may be a function of the body. */
function serveSearch(
  search: Answer,
  opts: { saved?: Answer; suggest?: Answer; other?: Answer } = {}
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
  get.mockImplementation((url: string) => {
    if (url.startsWith("/api/search/saved")) return pick(opts.saved ?? { items: [] }, { url });
    if (url.startsWith("/api/search/suggest"))
      return pick(opts.suggest ?? { suggestions: [] }, { url });
    return Promise.reject(new Error(`unmocked GET ${url}`));
  });
}

const searchCalls = () => post.mock.calls.filter((c) => c[0] === "/api/search");
const problem = (status: number, title: string) =>
  new ApiError({ type: "about:blank", title, status });

function Loc() {
  const { pathname, search } = useLocation();
  return <span data-testid="loc">{pathname + search}</span>;
}

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
      extraRoutes: <Route path="/videos/:videoId/watch" element={<p>player page</p>} />,
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

describe("Search (test-cases/search.md)", () => {
  beforeEach(() => {
    post.mockReset();
    get.mockReset();
    del.mockReset();
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("SRC-01: submit runs the search and renders passage hits with time links", async () => {
    serveSearch({ hits: [hit(42, "vid-1", "the <mark>quick</mark> brown fox")], total: 1 });
    renderSearchPage();
    await submit("quick");
    expect(searchCalls()[0][1]).toEqual({ q: "quick", mode: "hybrid", limit: 50 });
    const link = await screen.findByRole("link", { name: /brown fox/ });
    expect(link).toHaveAttribute("href", "/videos/vid-1/watch?t=72");
    expect(link).toHaveTextContent("[1:12 → 1:18]");
    expect(within(link).getByText("quick").tagName).toBe("MARK");
    expect(screen.getByTestId("loc")).toHaveTextContent("/search?q=quick");
    expect(screen.queryByRole("heading", { name: "Videos" })).toBeNull();
    expect(screen.queryByRole("heading", { name: "In transcripts" })).toBeNull();
  });

  it("SRC-02: title matches and passages show under their headings", async () => {
    serveSearch({
      hits: [hit(1, "vid-1", "a <mark>quick</mark> one")],
      videos: [
        { video_id: "v9", title: "Quick start", duration_sec: 3725 },
        { video_id: "v8", title: "No length" },
      ],
      total: 1,
    });
    renderSearchPage();
    await submit("quick");
    expect(await screen.findByRole("heading", { name: "Videos" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /Quick start/ })).toHaveAttribute("href", "/videos/v9");
    expect(screen.getByRole("link", { name: /Quick start/ })).toHaveTextContent(
      "Quick start · 62:05"
    );
    expect(screen.getByRole("link", { name: "No length" })).toHaveAttribute("href", "/videos/v8");
    expect(screen.getByRole("heading", { name: "In transcripts" })).toBeInTheDocument();
    expect(screen.queryByText(/No results/)).toBeNull();
  });

  it("SRC-03: a deep link /search?q=... runs the search once on load", async () => {
    localStorage.setItem("mkt:search:mode", "fts");
    serveSearch({ hits: [hit(1, "v1", "<mark>السيرة</mark>")], total: 1 });
    renderSearchPage("/search?q=%D8%A7%D9%84%D8%B3%D9%8A%D8%B1%D8%A9");
    expect(screen.getByRole("combobox", { name: "Search" })).toHaveValue("السيرة");
    expect(await screen.findByRole("link", { name: /السيرة/ })).toBeInTheDocument();
    expect(searchCalls()).toHaveLength(1);
    expect(searchCalls()[0][1]).toEqual({ q: "السيرة", mode: "fts", limit: 50 });
  });

  it("SRC-04/SRC-05: a new ?q= while on /search (header search, Back/Forward) runs that query", async () => {
    serveSearch((b: Record<string, unknown>) => ({
      hits: [hit(1, "v1", `result for ${b.q}`)],
      total: 1,
    }));
    renderSearchPage("/search?q=one", { history: ["/search?q=two"] });
    expect(await screen.findByText("result for one")).toBeInTheDocument();
    await userEvent.setup().click(screen.getByRole("button", { name: "go back" }));
    expect(await screen.findByText("result for two")).toBeInTheDocument();
    expect(screen.getByRole("combobox", { name: "Search" })).toHaveValue("two");
    expect(screen.queryByText("result for one")).toBeNull();
  });

  it("SRC-05: in-page searches push history, so Back steps through queries", async () => {
    serveSearch((b: Record<string, unknown>) => ({
      hits: [hit(1, "v1", `result for ${b.q}`)],
      total: 1,
    }));
    renderSearchPage();
    await submit("one");
    await screen.findByText("result for one");
    await submit("two");
    await screen.findByText("result for two");
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "go back" }));
    expect(screen.getByTestId("loc")).toHaveTextContent("/search?q=one");
    expect(await screen.findByText("result for one")).toBeInTheDocument();
    expect(screen.getByRole("combobox", { name: "Search" })).toHaveValue("one");
    // a result click then Back re-runs the query on mount
    await user.click(screen.getByRole("link", { name: /result for one/ }));
    expect(screen.getByText("player page")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "go back" }));
    expect(await screen.findByText("result for one")).toBeInTheDocument();
  });

  it.each([
    ["hits and videos null", { hits: null, videos: null, total: 0 }],
    ["hits missing", { total: 0 }],
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

  it.each([
    ["items null", { hits: [], youtube: { items: null } }, "No YouTube results."],
    [
      "items missing, reason disabled",
      { hits: [], youtube: { reason: "disabled" } },
      "YouTube search is off.",
    ],
  ])("SRC-07: YouTube %s shows the note", async (_l, answer, note) => {
    serveSearch(answer);
    renderSearchPage();
    await userEvent.setup().click(screen.getByRole("checkbox", { name: "Also search YouTube" }));
    await submit("x");
    const section = await screen.findByRole("region", { name: "From YouTube" });
    expect(section).toHaveTextContent(note);
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("SRC-07: a YouTube item with match null renders with the import button (admin)", async () => {
    serveSearch({
      hits: [],
      youtube: {
        items: [{ youtube_id: "y1", title: "T", channel: "C", thumbnail: "", match: null }],
      },
    });
    renderSearchPage("/search", { user: adminUser });
    await userEvent.setup().click(screen.getByRole("checkbox", { name: "Also search YouTube" }));
    await submit("x");
    expect(await screen.findByText("T")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Import metadata to…" })).toBeInTheDocument();
  });

  it("SRC-07: a hit with snippet null renders with an empty snippet", async () => {
    serveSearch({ hits: [hit(1, "v1", null, 0, 1)], total: 1 });
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
    serveSearch({ hits: [] }, { saved, suggest });
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
        ? { hits: [], videos: [{ video_id: "v1", title: {} }] }
        : { hits: [], videos: [{ video_id: "v1", title: "Fine" }] }
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

  it("SRC-10: signed-out user is sent to /login and no search runs", () => {
    serveSearch({ hits: [] });
    renderSearchPage("/search?q=fox", { user: null });
    expect(screen.getByText(/^login page \(from \/search/)).toBeInTheDocument();
    expect(post).not.toHaveBeenCalled();
  });

  it("SRC-10: the /login redirect keeps ?q=fox", () => {
    serveSearch({ hits: [] });
    renderSearchPage("/search?q=fox", { user: null });
    expect(screen.getByText("login page (from /search?q=fox)")).toBeInTheDocument();
  });

  it("SRC-12: snippet markup is escaped; only <mark> is highlighted", async () => {
    const alert = vi.spyOn(window, "alert").mockImplementation(() => {});
    serveSearch({
      hits: [
        hit(1, "v1", "<img src=x onerror=alert(1)> <mark>fox</mark> <b>bold</b> <mark>unclosed"),
      ],
      total: 1,
    });
    renderSearchPage();
    await submit("fox");
    const snippet = (await screen.findByRole("link", { name: /fox/ })).querySelector(
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

  it("SRC-14: a blank query does nothing; clearing removes ?q and the old results", async () => {
    serveSearch({ hits: [hit(1, "v1", "fox result")], total: 1 });
    renderSearchPage("/search?q=fox");
    await screen.findByText("fox result");
    post.mockClear();
    await submit("   ");
    await userEvent.setup().click(screen.getByRole("button", { name: "Save" }));
    expect(post).not.toHaveBeenCalled();
    expect(screen.getByTestId("loc")).toHaveTextContent(/^\/search$/);
    expect(screen.queryByText("fox result")).toBeNull();
  });

  it("SRC-14: spaces are trimmed in the URL and the request body", async () => {
    serveSearch({ hits: [] });
    renderSearchPage();
    await submit("  fox  ");
    expect(searchCalls()[0][1]).toMatchObject({ q: "fox" });
    expect(screen.getByTestId("loc")).toHaveTextContent("/search?q=fox");
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
    resolvers.b({ hits: [hit(2, "v2", "b result")], total: 1 });
    expect(await screen.findByText("b result")).toBeInTheDocument();
    resolvers.a({ hits: [hit(1, "v1", "a result")], total: 1 });
    await new Promise((r) => setTimeout(r, 0));
    expect(screen.queryByText("a result")).toBeNull();
    expect(screen.getByText("b result")).toBeInTheDocument();
  });

  it.each([
    ["500", problem(500, "search backend down"), "search backend down"],
    ["400", problem(400, "query too long"), "query too long"],
    ["network failure", new TypeError("Failed to fetch"), "Something went wrong."],
    ["malformed JSON", new SyntaxError("Unexpected token b"), "Something went wrong."],
  ])("SRC-16: search %s shows the ErrorState and clears results", async (_l, e, desc) => {
    let fail = false;
    serveSearch(() => (fail ? Promise.reject(e) : { hits: [hit(1, "v1", "old result")] }));
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
      b.q === "a" ? Promise.reject(problem(500, "boom")) : { hits: [hit(1, "v1", "b hit")] }
    );
    renderSearchPage();
    await submit("a");
    await screen.findByRole("alert");
    await submit("b");
    expect(await screen.findByText("b hit")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("SRC-18: mode select persists and is sent", async () => {
    serveSearch({ hits: [] });
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
    expect(localStorage.getItem("mkt:search:mode")).toBe("semantic");
    first.unmount();
    renderSearchPage();
    expect(screen.getByRole("combobox", { name: "Mode" })).toHaveValue("semantic");
  });

  it("SRC-18: a bogus or throwing stored mode falls back to Hybrid", () => {
    serveSearch({ hits: [] });
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
      { hits: [] },
      {
        suggest: ({ url }: Record<string, unknown>) =>
          new Promise((r) => (pending[String(url)] = r)),
      }
    );
    renderSearchPage();
    const box = screen.getByRole("combobox", { name: "Search" });
    const suggestCalls = () =>
      get.mock.calls.map((c) => String(c[0])).filter((u) => u.startsWith("/api/search/suggest"));
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
    serveSearch({ hits: [] });
    const r = renderSearchPage();
    fireEvent.change(screen.getByRole("combobox", { name: "Search" }), {
      target: { value: "qu" },
    });
    r.unmount();
    act(() => {
      vi.advanceTimersByTime(300);
    });
    expect(get.mock.calls.some((c) => String(c[0]).startsWith("/api/search/suggest"))).toBe(false);
  });

  it("SRC-20: save, run and delete saved searches", async () => {
    let items = [{ id: "s1", q: "fox", mode: "fts" }];
    serveSearch({ hits: [] }, { saved: () => ({ items }) });
    del.mockResolvedValue(undefined);
    renderSearchPage();
    const user = userEvent.setup();
    const aside = screen.getByRole("complementary", { name: "Saved searches" });
    await within(aside).findByRole("button", { name: "fox" });
    post.mockImplementation((url: string) => {
      if (url === "/api/search/save") items = [...items, { id: "s2", q: "dog", mode: "hybrid" }];
      return Promise.resolve({ hits: [] });
    });
    await user.type(screen.getByRole("combobox", { name: "Search" }), " dog ");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(post).toHaveBeenCalledWith("/api/search/save", { q: "dog", mode: "hybrid" });
    expect(await within(aside).findByRole("button", { name: "dog" })).toBeInTheDocument();
    await user.click(within(aside).getByRole("button", { name: "fox" }));
    expect(screen.getByRole("combobox", { name: "Search" })).toHaveValue("fox");
    expect(screen.getByRole("combobox", { name: "Mode" })).toHaveValue("fts");
    expect(post).toHaveBeenLastCalledWith("/api/search", { q: "fox", mode: "fts", limit: 50 });
    expect(screen.getByTestId("loc")).toHaveTextContent("/search?q=fox");
    items = items.filter((i) => i.id !== "s1");
    await user.click(within(aside).getByRole("button", { name: "Delete fox" }));
    expect(del).toHaveBeenCalledWith("/api/search/saved/s1");
    await waitFor(() => expect(within(aside).queryByRole("button", { name: "fox" })).toBeNull());
  });

  it("SRC-20: a failed save shows an error toast; an unknown saved mode falls back", async () => {
    serveSearch(
      { hits: [] },
      {
        saved: { items: [{ id: "s9", q: "odd", mode: "weird" }] },
        other: () => Promise.reject(problem(500, "save failed")),
      }
    );
    renderSearchPage();
    const user = userEvent.setup();
    await user.type(screen.getByRole("combobox", { name: "Search" }), "dog");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByText("save failed")).toBeInTheDocument();
    await user.click(await screen.findByRole("button", { name: "odd" }));
    expect(screen.getByRole("combobox", { name: "Mode" })).toHaveValue("hybrid");
  });

  it.todo("SRC-20: a successful save shows a confirmation (needs an i18n key)");

  it("SRC-21: YouTube block: results, in-library link, reason text", async () => {
    serveSearch({
      hits: [],
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
    await user.click(screen.getByRole("checkbox", { name: "Also search YouTube" }));
    await submit("x");
    expect(searchCalls()[0][1]).toMatchObject({ include: ["youtube"] });
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
    expect(within(section).getAllByRole("button", { name: "Import metadata to…" })).toHaveLength(1);
  });

  it.each([
    ["error", "YouTube is unavailable right now."],
    ["quota", "No YouTube results."],
  ])("SRC-21: reason %s shows readable text", async (reason, text) => {
    serveSearch({ hits: [], youtube: { items: [], reason } });
    renderSearchPage();
    await userEvent.setup().click(screen.getByRole("checkbox", { name: "Also search YouTube" }));
    await submit("x");
    expect(await screen.findByText(text)).toBeInTheDocument();
    expect(document.body.textContent).not.toContain("search.youtube.reason");
  });

  it("SRC-21: with the toggle off there is no YouTube section", async () => {
    serveSearch({ hits: [], youtube: { items: [] } });
    renderSearchPage();
    await submit("x");
    await screen.findByText(/No results/);
    expect(screen.queryByRole("region", { name: "From YouTube" })).toBeNull();
  });

  it("SRC-22: YouTube import prompt: cancel, success, error; hidden for non-admins", async () => {
    const yt = {
      hits: [],
      youtube: {
        items: [
          {
            youtube_id: "def",
            title: "Other",
            channel: "C2",
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
    await user.click(screen.getByRole("checkbox", { name: "Also search YouTube" }));
    await submit("x");
    const btn = await screen.findByRole("button", { name: "Import metadata to…" });
    prompt.mockReturnValueOnce(null);
    await user.click(btn);
    expect(importPost).not.toHaveBeenCalled();
    prompt.mockReturnValueOnce(" v7 ");
    importPost.mockResolvedValueOnce(undefined);
    await user.click(btn);
    expect(post).toHaveBeenCalledWith("/api/videos/v7/import-youtube", { youtube_id: "def" });
    expect(await screen.findByText("Metadata imported.")).toBeInTheDocument();
    prompt.mockReturnValueOnce("v7");
    importPost.mockRejectedValueOnce(problem(404, "video not found"));
    await user.click(btn);
    expect(await screen.findByText("video not found")).toBeInTheDocument();
    r.unmount();
    renderSearchPage();
    await userEvent.setup().click(screen.getByRole("checkbox", { name: "Also search YouTube" }));
    await submit("x");
    await screen.findByText("Other");
    expect(screen.queryByRole("button", { name: "Import metadata to…" })).toBeNull();
  });

  it("SRC-23: Arabic query, results and UI", async () => {
    localStorage.setItem("mkt:locale", "ar");
    serveSearch({
      hits: [hit(1, "v1", "من <mark>السيرة</mark> النبوية")],
      videos: [{ video_id: "v2", title: "السيرة النبوية ( 18 )" }],
      total: 1,
    });
    renderSearchPage();
    await waitFor(() => expect(document.documentElement).toHaveAttribute("dir", "rtl"));
    const user = userEvent.setup();
    const box = document.querySelector<HTMLInputElement>('input[type="search"]')!;
    await user.type(box, "السيرة النبوية");
    fireEvent.submit(box.closest("form")!);
    expect(searchCalls()[0][1]).toMatchObject({ q: "السيرة النبوية" });
    expect(screen.getByTestId("loc")).toHaveTextContent(
      "/search?q=%D8%A7%D9%84%D8%B3%D9%8A%D8%B1%D8%A9+%D8%A7%D9%84%D9%86%D8%A8%D9%88%D9%8A%D8%A9"
    );
    expect(await screen.findByText("السيرة النبوية ( 18 )")).toHaveAttribute("dir", "auto");
    expect(screen.queryByRole("heading", { name: "Videos" })).toBeNull();
    expect(screen.queryByText("Saved searches")).toBeNull();
    const time = document.querySelector(".mkt-result__time")!;
    expect(time.textContent).toContain("←");
  });
});
