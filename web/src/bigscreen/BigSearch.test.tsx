// Story 49.11 — big screen's Search with story 49.8's help, as the desk's page has it
// (49.10): suggestions under the keyboard with their kinds, a title opening its video;
// did-you-mean re-running the search with the same order and filters; the order chips;
// the Filters panel (a focus layer) whose facet chips combine, each filter in use a chip
// that removes it, and Clear all; the request bodies of each; an older home that sends
// none of it; Arabic; and axe in English and Arabic with all of it on screen.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { I18nProvider } from "../lib/i18n";
import { TestAuth, memberUser } from "../test/pageHarness";
import { axeViolations } from "../test/axe";
import en from "../i18n/en.json";
import ar from "../i18n/ar.json";

const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock("../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../lib/api")>("../lib/api");
  return { ...actual, api: { ...actual.api, get, post } };
});

import BigScreen from "./BigScreen";
import { resetFocusMemory } from "./focus/FocusProvider";

const E = en as Record<string, string>;
const A = ar as Record<string, string>;

const SUGGEST = {
  suggestions: ["patience", "patience in islam", "Patience and prayer", "Patrick"],
  items: [
    { text: "patience in islam", kind: "history" },
    { text: "patience", kind: "term" },
    { text: "Patience and prayer", kind: "title", video_id: "v9" },
    { text: "Patrick", kind: "speaker" },
  ],
};

const FACETS = {
  library: [{ value: "L1", label: "Tafsir", count: 3 }],
  language: [{ value: "ar", count: 2 }],
  speaker: [{ value: "Ahmad", count: 2 }],
  year: [
    {
      value: "2024",
      count: 2,
      filter: { date: { gte: "2024-01-01T00:00:00.000Z", lt: "2025-01-01T00:00:00.000Z" } },
    },
  ],
  duration: [{ value: "5m_20m", count: 1 }],
  videos: 3,
};

interface Body {
  q: string;
  sort?: string;
  filters?: Record<string, unknown>;
}

/** The answer to POST /api/search: one title named after the query, and `extra`. */
function answer(body: Body, extra: Record<string, unknown> = {}) {
  return {
    search_id: "s1",
    videos: [{ video_id: "v1", title: `Title ${body.q}`, duration_sec: 60 }],
    videos_total: 1,
    transcript_videos: [],
    transcript_total: 0,
    ...extra,
  };
}

function serve(
  extra: (b: Body) => Record<string, unknown> = () => ({}),
  suggest: unknown = SUGGEST
) {
  get.mockImplementation((url: string) => {
    if (url.startsWith("/api/search/suggest?")) return Promise.resolve(suggest);
    if (/\/chapters$/.test(url)) return Promise.resolve({ items: [] });
    if (/\/segments\?/.test(url)) return Promise.resolve({ items: [] });
    if (/^\/api\/videos\/[^/?]+$/.test(url))
      return Promise.resolve({ id: url.split("/").pop(), title: "A video", filename: "a.mp4" });
    return Promise.reject(new Error(`unmocked GET ${url}`));
  });
  post.mockImplementation((url: string, body: Body) =>
    url === "/api/search"
      ? Promise.resolve(answer(body, extra(body)))
      : Promise.reject(new Error(`unmocked POST ${url}`))
  );
}

let where = "";
function Where() {
  const l = useLocation();
  where = l.pathname + l.search;
  return null;
}

function renderBig(path: string) {
  return render(
    <I18nProvider>
      <TestAuth user={memberUser}>
        <MemoryRouter initialEntries={[path]}>
          <Where />
          <Routes>
            <Route path="/big/*" element={<BigScreen mode="desktop" />} />
          </Routes>
        </MemoryRouter>
      </TestAuth>
    </I18nProvider>
  );
}

const suggestName = () =>
  (localStorage.getItem("mkt:locale") === "ar" ? A : E)["search.suggest.label"]!;

/**
 * axe's findings that are this screen's. A video tile, a button with role listitem in a
 * role list row, is how every big-screen row is built (ui.tsx VideoTile) and is left to its
 * own change.
 */
async function axeOwn() {
  return (await axeViolations(document.body)).filter(
    (v) => !(v.id === "aria-allowed-role" && v.nodes.every((n) => n.includes('class="bs-tile"')))
  );
}

const lastBody = () => post.mock.calls[post.mock.calls.length - 1]![1] as Body;
const key = (k: string) => fireEvent.keyDown(window, { key: k });

beforeEach(() => {
  get.mockReset();
  post.mockReset();
  resetFocusMemory();
  localStorage.clear();
  sessionStorage.clear();
  localStorage.setItem("mkt:locale", "en");
  serve();
});

afterEach(() => {
  vi.useRealTimers();
});

describe("Suggestions (story 49.11)", () => {
  it("shows each suggestion's kind under the keyboard; a word is searched and the list hides", async () => {
    renderBig("/big/search");
    const s = await screen.findByTestId("big-search");
    fireEvent.click(within(s).getByRole("button", { name: "p" }));
    // from the first letter: on a remote every key saved counts
    const list = await within(s).findByRole("list", { name: suggestName() }, { timeout: 3000 });
    expect(get).toHaveBeenCalledWith("/api/search/suggest?q=p");
    const items = within(list).getAllByRole("button");
    expect(items.map((b) => b.getAttribute("data-kind"))).toEqual([
      "history",
      "term",
      "title",
      "speaker",
    ]);
    expect(items.map((b) => b.querySelector(".bs-suggest__kind")?.textContent)).toEqual([
      E["search.suggest.kind.history"],
      E["search.suggest.kind.term"],
      E["search.suggest.kind.title"],
      E["search.suggest.kind.speaker"],
    ]);
    // each is a D-pad stop with a stable focus id, in one group
    expect(items.map((b) => b.getAttribute("data-bs-focus"))).toEqual([
      "sg:0",
      "sg:1",
      "sg:2",
      "sg:3",
    ]);
    fireEvent.click(items[1]!);
    expect(within(s).getByRole("searchbox")).toHaveValue("patience");
    expect(within(s).queryByRole("list", { name: suggestName() })).toBeNull();
    expect(await within(s).findByText("Title patience", {}, { timeout: 3000 })).toBeInTheDocument();
    expect(post).toHaveBeenLastCalledWith("/api/search", {
      q: "patience",
      mode: "hybrid",
      view: "grid",
    });
    expect(where).toBe("/big/search?q=patience");
    // the list stays hidden for the chosen words, and comes back with the next key
    expect(within(s).queryByRole("list", { name: suggestName() })).toBeNull();
    fireEvent.click(within(s).getByRole("button", { name: E["big.keyboard.space"] }));
    fireEvent.click(within(s).getByRole("button", { name: "i" }));
    expect(
      await within(s).findByRole("list", { name: suggestName() }, { timeout: 3000 })
    ).toBeInTheDocument();
  });

  it("a title opens its video", async () => {
    renderBig("/big/search");
    const s = await screen.findByTestId("big-search");
    fireEvent.click(within(s).getByRole("button", { name: "p" }));
    const list = await within(s).findByRole("list", { name: suggestName() }, { timeout: 3000 });
    fireEvent.click(within(list).getByRole("button", { name: /Patience and prayer/ }));
    await waitFor(() => expect(where).toBe("/big/video/v9"));
  });

  it("an older home's plain texts are listed without a kind", async () => {
    serve(undefined, { suggestions: ["abc", "abd"] });
    renderBig("/big/search");
    const s = await screen.findByTestId("big-search");
    fireEvent.click(within(s).getByRole("button", { name: "a" }));
    const list = await within(s).findByRole("list", { name: suggestName() }, { timeout: 3000 });
    const items = within(list).getAllByRole("button");
    expect(items.map((b) => b.textContent)).toEqual(["abc", "abd"]);
    expect(items.every((b) => b.getAttribute("data-kind") === "plain")).toBe(true);
  });
});

describe("Did you mean, the order and the filters (story 49.11)", () => {
  it("did-you-mean searches the corrected words with the same order and filters", async () => {
    serve((b) => (b.q === "patiense" ? { did_you_mean: "patience", facets: FACETS } : {}));
    renderBig("/big/search?q=patiense&sort=newest&lib=L1");
    const row = await screen.findByTestId("big-did-you-mean", {}, { timeout: 3000 });
    expect(lastBody()).toEqual({
      q: "patiense",
      mode: "hybrid",
      view: "grid",
      sort: "newest",
      filters: { library_id: ["L1"] },
    });
    expect(row).toHaveTextContent("Did you mean patience?");
    fireEvent.click(within(row).getByRole("button", { name: "patience" }));
    await waitFor(() => expect(lastBody().q).toBe("patience"));
    expect(lastBody()).toEqual({
      q: "patience",
      mode: "hybrid",
      view: "grid",
      sort: "newest",
      filters: { library_id: ["L1"] },
    });
    await waitFor(() => expect(where).toBe("/big/search?q=patience&sort=newest&lib=L1"));
    // the answer for the corrected words has none: the row goes
    await waitFor(() => expect(screen.queryByTestId("big-did-you-mean")).toBeNull());
  });

  it("the order chips and the Filters panel: each request body, several combined, removal, Clear all", async () => {
    serve(() => ({ facets: FACETS }));
    renderBig("/big/search?q=ab");
    const s = await screen.findByTestId("big-search");
    await within(s).findByText("Title ab", {}, { timeout: 3000 });
    expect(lastBody()).toEqual({ q: "ab", mode: "hybrid", view: "grid" });

    // the order: relevance is chosen and not sent; another one is, and rides in the URL
    const sortBar = within(s).getByRole("group", { name: E["search.sort"] });
    expect(
      within(sortBar).getByRole("button", { name: E["search.sort.relevance"] })
    ).toHaveAttribute("aria-pressed", "true");
    fireEvent.click(within(sortBar).getByRole("button", { name: E["search.sort.newest"] }));
    await waitFor(() => expect(lastBody().sort).toBe("newest"));
    expect(where).toBe("/big/search?q=ab&sort=newest");

    // Filters opens a panel of the facets with their counts
    fireEvent.click(within(sortBar).getByRole("button", { name: E["big.search.filters"] }));
    const panel = await screen.findByTestId("big-filters");
    const group = (facet: string) =>
      within(panel).getByRole("group", { name: E[`search.facet.${facet}`] });
    const lib = within(group("library")).getByRole("button", { name: /Tafsir/ });
    expect(lib.querySelector(".bs-facet__count")).toHaveTextContent("3");
    expect(lib).toHaveAttribute("aria-pressed", "false");
    expect(within(group("language")).getByRole("button", { name: /Arabic/ })).toBeInTheDocument();
    expect(
      within(group("duration")).getByRole("button", { name: /5 to 20 min/ })
    ).toBeInTheDocument();

    // a library, then a year (its bucket's own filter) and a band (the server's range): they combine
    fireEvent.click(lib);
    await waitFor(() => expect(lastBody().filters).toEqual({ library_id: ["L1"] }));
    fireEvent.click(within(group("year")).getByRole("button", { name: /2024/ }));
    fireEvent.click(within(group("duration")).getByRole("button", { name: /5 to 20 min/ }));
    await waitFor(() =>
      expect(lastBody()).toEqual({
        q: "ab",
        mode: "hybrid",
        view: "grid",
        sort: "newest",
        filters: {
          library_id: ["L1"],
          date: { gte: "2024-01-01T00:00:00.000Z", lt: "2025-01-01T00:00:00.000Z" },
          duration_sec: { gte: 300, lt: 1200 },
        },
      })
    );
    expect(where).toBe("/big/search?q=ab&sort=newest&lib=L1&year=2024&dur=5m_20m");
    expect(within(group("library")).getByRole("button", { name: /Tafsir/ })).toHaveAttribute(
      "aria-pressed",
      "true"
    );

    // Back closes the panel; the filters in use are chips that remove them
    key("Escape");
    await waitFor(() => expect(screen.queryByTestId("big-filters")).toBeNull());
    const active = within(s).getByRole("group", { name: E["search.filters.active"] });
    const chips = within(active).getAllByRole("button");
    expect(chips.map((b) => b.getAttribute("aria-label") ?? b.textContent)).toEqual([
      "Remove Library: Tafsir",
      "Remove Year: 2024",
      "Remove Length: 5 to 20 min",
      E["search.filters.clear"],
    ]);
    fireEvent.click(chips[1]!);
    await waitFor(() =>
      expect(lastBody().filters).toEqual({
        library_id: ["L1"],
        duration_sec: { gte: 300, lt: 1200 },
      })
    );
    expect(where).toBe("/big/search?q=ab&sort=newest&lib=L1&dur=5m_20m");
    fireEvent.click(
      within(within(s).getByRole("group", { name: E["search.filters.active"] })).getByRole(
        "button",
        { name: E["search.filters.clear"] }
      )
    );
    await waitFor(() => expect(lastBody().filters).toBeUndefined());
    expect(lastBody().sort).toBe("newest");
    expect(within(s).queryByRole("group", { name: E["search.filters.active"] })).toBeNull();
  });

  it("new words drop the filters and keep the order", async () => {
    serve(() => ({ facets: FACETS }));
    renderBig("/big/search?q=ab&sort=matches&lang=ar");
    const s = await screen.findByTestId("big-search");
    await within(s).findByText("Title ab", {}, { timeout: 3000 });
    expect(lastBody().filters).toEqual({ language: ["ar"] });
    fireEvent.click(within(s).getByRole("button", { name: "c" }));
    await within(s).findByText("Title abc", {}, { timeout: 3000 });
    expect(lastBody()).toEqual({ q: "abc", mode: "hybrid", view: "grid", sort: "matches" });
    expect(where).toBe("/big/search?q=abc&sort=matches");
  });

  it("an older home that sends no facets, sort or did-you-mean: the results, relevance, no Filters", async () => {
    renderBig("/big/search?q=ab");
    const s = await screen.findByTestId("big-search");
    await within(s).findByText("Title ab", {}, { timeout: 3000 });
    const sortBar = within(s).getByRole("group", { name: E["search.sort"] });
    expect(
      within(sortBar).getByRole("button", { name: E["search.sort.relevance"] })
    ).toHaveAttribute("aria-pressed", "true");
    expect(within(s).queryByRole("button", { name: E["big.search.filters"] })).toBeNull();
    expect(screen.queryByTestId("big-did-you-mean")).toBeNull();
  });
});

describe("Arabic and axe (story 49.11)", () => {
  it("reads in Arabic: the kinds, did-you-mean, the order and the facets", async () => {
    localStorage.setItem("mkt:locale", "ar");
    serve(() => ({ facets: FACETS, did_you_mean: "الصبر" }));
    renderBig("/big/search?q=الصبڕ");
    const row = await screen.findByTestId("big-did-you-mean", {}, { timeout: 3000 });
    expect(row).toHaveTextContent("هل تقصد الصبر؟");
    expect(document.documentElement.dir).toBe("rtl");
    const s = screen.getByTestId("big-search");
    const sortBar = within(s).getByRole("group", { name: A["search.sort"] });
    expect(
      within(sortBar).getByRole("button", { name: A["search.sort.matches"] })
    ).toBeInTheDocument();
    fireEvent.click(within(sortBar).getByRole("button", { name: A["big.search.filters"] }));
    const panel = await screen.findByTestId("big-filters");
    expect(within(panel).getByRole("heading", { name: A["search.facets"] })).toBeInTheDocument();
    expect(
      within(within(panel).getByRole("group", { name: A["search.facet.language"] })).getByRole(
        "button",
        { name: /العربية/ }
      )
    ).toBeInTheDocument();
    expect(
      within(panel).getByRole("group", { name: A["search.facet.duration"] })
    ).toHaveTextContent(/دقيقة/);
  });

  it.each(["en", "ar"])(
    "(%s): axe finds nothing with did-you-mean, the order, filters in use, the panel open, then suggestions",
    async (locale) => {
      localStorage.setItem("mkt:locale", locale);
      const T = locale === "ar" ? A : E;
      serve(() => ({ facets: FACETS, did_you_mean: "patience" }));
      renderBig("/big/search?q=pat&lib=L1");
      await screen.findByTestId("big-did-you-mean", {}, { timeout: 3000 });
      const s = screen.getByTestId("big-search");
      expect(
        within(s).getByRole("group", { name: T["search.filters.active"] })
      ).toBeInTheDocument();
      fireEvent.click(
        within(s).getByRole("button", { name: new RegExp(`^${T["big.search.filters"]!}`) })
      );
      await screen.findByTestId("big-filters");
      expect(await axeOwn()).toEqual([]);
      key("Escape");
      await waitFor(() => expect(screen.queryByTestId("big-filters")).toBeNull());
      fireEvent.click(within(s).getByRole("button", { name: locale === "ar" ? "ا" : "a" }));
      await within(s).findByRole("list", { name: suggestName() }, { timeout: 3000 });
      expect(await axeOwn()).toEqual([]);
    },
    30_000
  );
});
