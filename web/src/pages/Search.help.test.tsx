// Story 49.10: the Search page's suggestions, did-you-mean, facets and sort
// (web/test-cases/search.md SRC-44 to SRC-53), on the fake saved-search server.
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
import { memberUser, renderPage } from "../test/pageHarness";
import { axeViolations } from "../test/axe";
import { suggestItems } from "./searchHelp";
import { forgetAnswers, keepAnswer } from "../lib/pageCache";
import {
  type Corpus,
  type FakeServerOptions,
  bucket,
  facetsOf,
  fakeSearchServer,
  manyTitles,
  manyTranscripts,
  passage,
  titleVideo,
  transcriptVideo,
} from "../test/searchServer";

function Loc() {
  const { pathname, search } = useLocation();
  return <span data-testid="loc">{pathname + search}</span>;
}
const loc = () => screen.getByTestId("loc").textContent ?? "";
const locParams = () => new URLSearchParams(loc().split("?")[1] ?? "");

function renderSearchPage(path = "/search") {
  return renderPage(
    <ToastProvider>
      <Search />
      <Loc />
    </ToastProvider>,
    {
      path,
      route: "/search",
      user: memberUser,
      extraRoutes: (
        <>
          <Route path="/videos/:videoId" element={<p>video page</p>} />
          <Route path="/videos/:videoId/watch" element={<p>player page</p>} />
          <Route path="/search/video/:videoId" element={<p>matches page</p>} />
        </>
      ),
    }
  );
}

const serve = (
  corpus: Corpus | ((b: Record<string, unknown>) => Corpus),
  opts: FakeServerOptions = {}
) => fakeSearchServer({ post, get, del }, corpus, opts);
const searchBodies = () =>
  post.mock.calls.filter((c) => c[0] === "/api/search").map((c) => c[1] as Record<string, unknown>);
const lastBody = () => searchBodies().at(-1)!;
const getCalls = (prefix: string) =>
  get.mock.calls.map((c) => String(c[0])).filter((u) => u.startsWith(prefix));
// the search box in either language (its name is the page's word for Search)
const box = () =>
  document.querySelector<HTMLInputElement>('input[type="search"][role="combobox"]')!;
const suggestions = () => within(screen.getByRole("listbox")).getAllByRole("option");
/** A chip's visible text: its name and its count. */
const chipText = (b: HTMLElement) =>
  [
    b.querySelector(".mkt-facet__label")?.textContent,
    b.querySelector(".mkt-facet__count")?.textContent,
  ]
    .filter(Boolean)
    .join(" ");

async function submit(term: string) {
  const user = userEvent.setup();
  await user.clear(box());
  await user.type(box(), term);
  fireEvent.submit(box().closest("form")!);
}

const corpus: Corpus = {
  titles: [titleVideo("v1", "Patience in hardship")],
  transcripts: [transcriptVideo("r1", "Friday talk", [passage(1, 30, "on <mark>patience</mark>")])],
};

const FACETS = facetsOf(
  {
    library: [
      bucket("lib-1", 12, { label: "Lectures" }),
      bucket("lib-2", 3, { label: "Khutbahs" }),
    ],
    language: [bucket("ar", 10), bucket("en", 5)],
    speaker: [bucket("Sheikh Ahmad", 7)],
    year: [
      bucket("2024", 9, {
        filter: { date: { gte: "2024-01-01T00:00:00.000Z", lt: "2025-01-01T00:00:00.000Z" } },
      }),
      bucket("2019", 6, {
        filter: { date: { gte: "2019-01-01T00:00:00.000Z", lt: "2020-01-01T00:00:00.000Z" } },
      }),
    ],
    duration: [
      bucket("under_5m", 2, { filter: { duration_sec: { gte: 0, lt: 300 } } }),
      bucket("over_60m", 13, { filter: { duration_sec: { gte: 3600 } } }),
    ],
  },
  15
);

const SUGGEST = {
  suggestions: ["patience in trials", "patience", "Patience in hardship", "Sheikh Patel"],
  items: [
    { text: "patience in trials", kind: "history" },
    { text: "patience", kind: "term" },
    { text: "Patience in hardship", kind: "title", video_id: "v1" },
    { text: "Sheikh Patel", kind: "speaker" },
  ],
};

/** Types `text` and lets the 200 ms debounce run (fake timers). */
async function typeAndWait(text: string) {
  act(() => box().focus());
  fireEvent.change(box(), { target: { value: text } });
  await act(async () => {
    vi.advanceTimersByTime(200);
  });
  await act(async () => {});
}

const facetGroup = (name: string) => screen.getByRole("group", { name });
const facetButton = (group: string, label: RegExp) =>
  within(facetGroup(group)).getByRole("button", { name: label });

beforeEach(() => {
  post.mockReset();
  get.mockReset();
  del.mockReset();
  sessionStorage.clear();
  localStorage.clear();
  // the libraries list a test read stays in the page cache (memory) for the next test
  forgetAnswers("libraries");
});
afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

describe("Search suggestions (story 49.10)", () => {
  it("SRC-44: each suggestion shows its kind; the ARIA combobox follows up / down; Enter searches the word", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    serve(corpus, { suggest: () => SUGGEST });
    renderSearchPage();
    await typeAndWait("pat");
    expect(getCalls("/api/search/suggest")).toEqual(["/api/search/suggest?q=pat"]);
    const list = screen.getByRole("listbox", { name: "Suggestions" });
    const options = within(list).getAllByRole("option");
    expect(options.map((o) => o.textContent)).toEqual([
      "patience in trialsRecent",
      "patienceWord",
      "Patience in hardshipVideo",
      "Sheikh PatelSpeaker",
    ]);
    expect(box()).toHaveAttribute("aria-expanded", "true");
    expect(box()).toHaveAttribute("aria-controls", list.id);
    expect(box()).not.toHaveAttribute("aria-activedescendant");

    fireEvent.keyDown(box(), { key: "ArrowDown" });
    expect(box()).toHaveAttribute("aria-activedescendant", options[0]!.id);
    expect(options[0]).toHaveAttribute("aria-selected", "true");
    fireEvent.keyDown(box(), { key: "ArrowDown" });
    expect(box()).toHaveAttribute("aria-activedescendant", options[1]!.id);
    // up from the first wraps to the last, down from the last to the first
    fireEvent.keyDown(box(), { key: "ArrowUp" });
    fireEvent.keyDown(box(), { key: "ArrowUp" });
    expect(box()).toHaveAttribute("aria-activedescendant", options[3]!.id);
    fireEvent.keyDown(box(), { key: "ArrowDown" });
    fireEvent.keyDown(box(), { key: "ArrowDown" });
    expect(box()).toHaveAttribute("aria-activedescendant", options[1]!.id);

    fireEvent.keyDown(box(), { key: "Enter" });
    await waitFor(() => expect(searchBodies()).toHaveLength(1));
    expect(lastBody()).toEqual({ q: "patience", mode: "hybrid", view: "list" });
    expect(box()).toHaveValue("patience");
    expect(screen.queryByRole("listbox")).toBeNull();
    expect(box()).toHaveAttribute("aria-expanded", "false");
    await waitFor(() => expect(locParams().get("sid")).toBe("sid-1"));
  });

  it("SRC-44: Escape closes the list and keeps the text; Enter then submits what was typed", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    serve(corpus, { suggest: () => SUGGEST });
    renderSearchPage();
    await typeAndWait("pat");
    fireEvent.keyDown(box(), { key: "ArrowDown" });
    fireEvent.keyDown(box(), { key: "Escape" });
    expect(screen.queryByRole("listbox")).toBeNull();
    expect(box()).toHaveValue("pat");
    expect(box()).not.toHaveAttribute("aria-activedescendant");
    // down opens it again
    fireEvent.keyDown(box(), { key: "ArrowDown" });
    expect(screen.getByRole("listbox")).toBeInTheDocument();
    fireEvent.keyDown(box(), { key: "Escape" });
    fireEvent.submit(box().closest("form")!);
    await waitFor(() => expect(searchBodies()).toHaveLength(1));
    expect(lastBody()).toMatchObject({ q: "pat" });
  });

  it("SRC-45: a title suggestion opens its video (keyboard and pointer); a speaker is searched", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    serve(corpus, { suggest: () => SUGGEST });
    const r = renderSearchPage();
    await typeAndWait("pat");
    fireEvent.keyDown(box(), { key: "ArrowDown" });
    fireEvent.keyDown(box(), { key: "ArrowDown" });
    fireEvent.keyDown(box(), { key: "ArrowDown" });
    fireEvent.keyDown(box(), { key: "Enter" });
    expect(await screen.findByText("video page")).toBeInTheDocument();
    expect(searchBodies()).toHaveLength(0);
    r.unmount();

    renderSearchPage();
    await typeAndWait("she");
    const speaker = screen.getByRole("option", { name: /Sheikh Patel/ });
    // a press keeps the box focused (the list would close on blur first)
    expect(fireEvent.mouseDown(speaker)).toBe(false);
    fireEvent.click(speaker);
    await waitFor(() => expect(searchBodies()).toHaveLength(1));
    expect(lastBody()).toMatchObject({ q: "Sheikh Patel" });

    await typeAndWait("pat");
    fireEvent.click(screen.getByRole("option", { name: /Patience in hardship/ }));
    expect(await screen.findByText("video page")).toBeInTheDocument();
  });

  it("SRC-44: a home before story 49.8 (suggestions only) still lists them, with no kind", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    serve(corpus, { suggest: () => ({ suggestions: ["quick", null, "quiz"] }) });
    renderSearchPage();
    await typeAndWait("qu");
    expect(suggestions().map((o) => o.textContent)).toEqual(["quick", "quiz"]);
    // a title without a video is dropped; the list leaves when the box loses focus
    act(() => box().blur());
    expect(screen.queryByRole("listbox")).toBeNull();
  });
});

describe("Suggestions: review follow-ups (story 49.10)", () => {
  it("SRC-44: Enter with the list open and nothing highlighted submits what was typed", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    serve(corpus, { suggest: () => SUGGEST });
    renderSearchPage();
    await typeAndWait("pat");
    expect(screen.getByRole("listbox")).toBeInTheDocument();
    fireEvent.keyDown(box(), { key: "Enter" });
    fireEvent.submit(box().closest("form")!);
    await waitFor(() => expect(searchBodies()).toHaveLength(1));
    expect(lastBody()).toMatchObject({ q: "pat" });
    expect(screen.queryByRole("listbox")).toBeNull();
  });

  it("SRC-44: leaving the box closes the list until the next keystroke; the highlighted option is scrolled into sight", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const scrolled: string[] = [];
    const original = Element.prototype.scrollIntoView;
    Element.prototype.scrollIntoView = function (this: Element) {
      scrolled.push(this.id);
    };
    try {
      serve(corpus, { suggest: () => SUGGEST });
      renderSearchPage();
      await typeAndWait("pat");
      fireEvent.keyDown(box(), { key: "ArrowUp" });
      const last = suggestions().at(-1)!;
      expect(scrolled.at(-1)).toBe(last.id);
      act(() => box().blur());
      act(() => box().focus());
      expect(screen.queryByRole("listbox")).toBeNull();
      fireEvent.change(box(), { target: { value: "pati" } });
      await act(async () => {
        vi.advanceTimersByTime(200);
      });
      await act(async () => {});
      expect(screen.getByRole("listbox")).toBeInTheDocument();
    } finally {
      Element.prototype.scrollIntoView = original;
    }
  });

  it("SRC-45: a word chosen while filters are on searches the new words without them, the sort kept", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    serve(corpus, { suggest: () => SUGGEST, help: () => ({ facets: FACETS }) });
    renderSearchPage("/search?q=gratitude&mode=hybrid&sort=newest&lib=lib-1");
    await waitFor(() => expect(searchBodies()).toHaveLength(1));
    expect(lastBody()).toMatchObject({ filters: { library_id: ["lib-1"] } });
    await screen.findByTestId("facets");
    await typeAndWait("pat");
    fireEvent.click(suggestions().find((o) => o.textContent === "patienceWord")!);
    await waitFor(() => expect(searchBodies()).toHaveLength(2));
    expect(lastBody()).toEqual({ q: "patience", mode: "hybrid", view: "list", sort: "newest" });
  });

  it("SRC-44: suggestItems keeps what can be shown and chosen", () => {
    expect(
      suggestItems({
        items: [
          { text: "a", kind: "term" },
          { text: "a", kind: "term" },
          { text: "a", kind: "history" },
          { text: "  ", kind: "term" },
          null,
          { text: "Title", kind: "title" },
          { text: "Title", kind: "title", video_id: "v9" },
          { text: "odd", kind: "bogus" },
          { text: 3 },
        ],
      })
    ).toEqual([
      { text: "a", kind: "term" },
      { text: "a", kind: "history" },
      { text: "Title", kind: "title", video_id: "v9" },
      { text: "odd" },
    ]);
    expect(suggestItems({ suggestions: ["x", "", null, "x"] })).toEqual([{ text: "x" }]);
    expect(suggestItems(null)).toEqual([]);
    expect(suggestItems({ items: [], suggestions: ["y"] })).toEqual([{ text: "y" }]);
  });
});

describe("Did you mean (story 49.10)", () => {
  it("SRC-46: the corrected query shows above the results and one tap runs it, filters kept", async () => {
    serve(corpus, {
      help: (b) => ({
        did_you_mean: b.q === "المدرصة" ? "المدرسة" : undefined,
        facets: FACETS,
      }),
    });
    renderSearchPage("/search?q=%D8%A7%D9%84%D9%85%D8%AF%D8%B1%D8%B5%D8%A9&mode=hybrid&lib=lib-1");
    const row = await screen.findByTestId("did-you-mean");
    expect(row).toHaveTextContent("Did you mean المدرسة?");
    const user = userEvent.setup();
    await user.click(within(row).getByRole("button", { name: "المدرسة" }));
    await waitFor(() => expect(searchBodies()).toHaveLength(2));
    expect(lastBody()).toEqual({
      q: "المدرسة",
      mode: "hybrid",
      view: "list",
      filters: { library_id: ["lib-1"] },
    });
    expect(box()).toHaveValue("المدرسة");
    await waitFor(() => expect(screen.queryByTestId("did-you-mean")).toBeNull());
    expect(locParams().getAll("lib")).toEqual(["lib-1"]);
  });

  it("SRC-46: no did_you_mean, or one equal to the query, shows no row", async () => {
    serve(corpus, { help: (b) => ({ did_you_mean: b.q === "same" ? "same" : undefined }) });
    renderSearchPage();
    await submit("same");
    await screen.findByText("Patience in hardship");
    expect(screen.queryByTestId("did-you-mean")).toBeNull();
  });

  it("SRC-46: a correction is shown trimmed, and one that is the query with spaces shows no row (didYouMeanOf, shared with the big screen)", async () => {
    serve(corpus, {
      help: (b) => ({
        did_you_mean: b.q === "same" ? " same " : b.q === "patiense" ? "  patience " : undefined,
      }),
    });
    renderSearchPage();
    await submit("patiense");
    const row = await screen.findByTestId("did-you-mean");
    expect(within(row).getByRole("button", { name: "patience" })).toHaveTextContent(/^patience$/);
    await submit("same");
    await waitFor(() => expect(screen.queryByTestId("did-you-mean")).toBeNull());
  });
});

describe("Facets and sort (story 49.10)", () => {
  it("SRC-47: facets show with their counts and names; a bucket narrows; several combine; remove and Clear all", async () => {
    serve(corpus, { help: () => ({ facets: FACETS }) });
    renderSearchPage();
    await submit("patience");
    const panel = await screen.findByRole("region", { name: "Narrow the results" });
    expect(
      within(panel)
        .getAllByRole("group")
        .map((g) => g.getAttribute("aria-labelledby"))
    ).toHaveLength(5);
    expect(chipText(facetButton("Library", /Lectures/))).toBe("Lectures 12");
    expect(facetButton("Library", /Lectures/)).toHaveAccessibleName("Lectures 12 videos");
    expect(chipText(facetButton("Language", /Arabic/))).toBe("Arabic 10");
    expect(facetButton("Language", /English/)).toBeInTheDocument();
    expect(facetButton("Speaker", /Sheikh Ahmad/)).toHaveAttribute("aria-pressed", "false");
    expect(within(facetGroup("Length")).getAllByRole("button").map(chipText)).toEqual([
      "Under 5 min 2",
      "Over an hour 13",
    ]);
    expect(within(facetGroup("Year")).getAllByRole("button").map(chipText)).toEqual([
      "2024 9",
      "2019 6",
    ]);

    const user = userEvent.setup();
    await user.click(facetButton("Library", /Khutbahs/));
    await waitFor(() => expect(searchBodies()).toHaveLength(2));
    expect(lastBody()).toEqual({
      q: "patience",
      mode: "hybrid",
      view: "list",
      filters: { library_id: ["lib-2"] },
    });
    await waitFor(() => expect(locParams().get("sid")).toBe("sid-2"));
    expect(locParams().getAll("lib")).toEqual(["lib-2"]);
    expect(facetButton("Library", /Khutbahs/)).toHaveAttribute("aria-pressed", "true");

    // a year and a duration take their bucket's own filter; a language and a speaker join
    await user.click(facetButton("Year", /2019/));
    await waitFor(() => expect(searchBodies()).toHaveLength(3));
    await user.click(facetButton("Length", /Over an hour/));
    await waitFor(() => expect(searchBodies()).toHaveLength(4));
    await user.click(facetButton("Language", /Arabic/));
    await waitFor(() => expect(searchBodies()).toHaveLength(5));
    await user.click(facetButton("Speaker", /Sheikh Ahmad/));
    await waitFor(() => expect(searchBodies()).toHaveLength(6));
    expect(lastBody().filters).toEqual({
      library_id: ["lib-2"],
      language: ["ar"],
      speaker: ["Sheikh Ahmad"],
      date: { gte: "2019-01-01T00:00:00.000Z", lt: "2020-01-01T00:00:00.000Z" },
      duration_sec: { gte: 3600 },
    });
    await waitFor(() => expect(locParams().get("sid")).toBe("sid-6"));
    expect(loc()).toBe(
      "/search?q=patience&mode=hybrid&lib=lib-2&lang=ar&spk=Sheikh+Ahmad&year=2019&dur=over_60m&sid=sid-6"
    );
    // another year replaces the first (the request has one date range)
    await user.click(facetButton("Year", /2024/));
    await waitFor(() => expect(searchBodies()).toHaveLength(7));
    expect(lastBody().filters).toMatchObject({
      date: { gte: "2024-01-01T00:00:00.000Z", lt: "2025-01-01T00:00:00.000Z" },
    });

    const active = screen.getByRole("group", { name: "Filters in use" });
    expect(
      within(active)
        .getAllByRole("listitem")
        .map((li) => li.textContent)
    ).toEqual([
      "Library: Khutbahs",
      "Language: Arabic",
      "Speaker: Sheikh Ahmad",
      "Year: 2024",
      "Length: Over an hour",
    ]);
    await user.click(within(active).getByRole("button", { name: "Remove Speaker: Sheikh Ahmad" }));
    await waitFor(() => expect(searchBodies()).toHaveLength(8));
    expect(lastBody().filters).not.toHaveProperty("speaker");
    expect(lastBody().filters).toMatchObject({ library_id: ["lib-2"], language: ["ar"] });

    // pressing a chosen bucket again takes it off
    await user.click(facetButton("Language", /Arabic/));
    await waitFor(() => expect(searchBodies()).toHaveLength(9));
    expect(lastBody().filters).not.toHaveProperty("language");

    await user.click(screen.getByRole("button", { name: "Clear all filters" }));
    await waitFor(() => expect(searchBodies()).toHaveLength(10));
    expect(lastBody()).toEqual({ q: "patience", mode: "hybrid", view: "list" });
    await waitFor(() => expect(screen.queryByRole("group", { name: "Filters in use" })).toBeNull());
    expect(locParams().get("lib")).toBeNull();

    // Back returns to the filtered search by its sid, chips and facets with it
    await user.click(screen.getByRole("button", { name: "go back" }));
    await waitFor(() => expect(locParams().get("sid")).toBe("sid-9"));
    expect(await screen.findByRole("group", { name: "Filters in use" })).toBeInTheDocument();
    expect(facetButton("Library", /Khutbahs/)).toHaveAttribute("aria-pressed", "true");
    expect(searchBodies()).toHaveLength(10);
  });

  it("SRC-48: a chosen bucket the narrowed facets no longer list stays on, pressed, to be taken off", async () => {
    serve(corpus, {
      help: (b) => ({
        facets: (b.filters as Record<string, unknown> | undefined)?.speaker
          ? facetsOf({ library: [bucket("lib-1", 1, { label: "Lectures" })] })
          : FACETS,
      }),
    });
    renderSearchPage();
    await submit("patience");
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: /Sheikh Ahmad/ }));
    await waitFor(() => expect(searchBodies()).toHaveLength(2));
    await waitFor(() => expect(locParams().get("sid")).toBe("sid-2"));
    const kept = facetButton("Speaker", /Sheikh Ahmad/);
    expect(kept).toHaveAttribute("aria-pressed", "true");
    expect(chipText(kept)).toBe("Sheikh Ahmad");
    await user.click(kept);
    await waitFor(() => expect(searchBodies()).toHaveLength(3));
    expect(lastBody()).not.toHaveProperty("filters");
  });

  it("SRC-49: a deep link's filters reach the request, a year and a band by their ranges", async () => {
    serve(corpus, { help: () => ({ facets: FACETS }) });
    renderSearchPage(
      "/search?q=patience&mode=fts&sort=newest&lib=lib-1&lib=lib-2&lang=en&year=2021&dur=5m_20m&year=bad"
    );
    await waitFor(() => expect(searchBodies()).toHaveLength(1));
    expect(lastBody()).toEqual({
      q: "patience",
      mode: "fts",
      view: "list",
      sort: "newest",
      filters: {
        library_id: ["lib-1", "lib-2"],
        language: ["en"],
        date: { gte: "2021-01-01T00:00:00.000Z", lt: "2022-01-01T00:00:00.000Z" },
        duration_sec: { gte: 300, lt: 1200 },
      },
    });
    // the library names come from the facets once they arrive
    const active = await screen.findByRole("group", { name: "Filters in use" });
    await waitFor(() =>
      expect(
        within(active)
          .getAllByRole("listitem")
          .map((li) => li.textContent)
      ).toEqual([
        "Library: Lectures",
        "Library: Khutbahs",
        "Language: English",
        "Year: 2021",
        "Length: 5 to 20 min",
      ])
    );
  });

  it("SRC-50: sort is sent, kept in the URL and on the matches links; the answer's sort is shown", async () => {
    serve(corpus, {
      // a home answers in the order it used (here: newest, whatever was asked)
      help: (b) => ({
        sort: b.sort === "matches" ? "newest" : (b.sort ?? "relevance"),
        facets: FACETS,
      }),
    });
    renderSearchPage();
    await submit("patience");
    const sort = await screen.findByRole("combobox", { name: "Sort by" });
    expect(sort).toHaveValue("relevance");
    expect(
      within(sort)
        .getAllByRole("option")
        .map((o) => o.textContent)
    ).toEqual(["Relevance", "Newest", "Most matches"]);
    const user = userEvent.setup();
    await user.selectOptions(sort, "newest");
    await waitFor(() => expect(searchBodies()).toHaveLength(2));
    expect(lastBody()).toEqual({ q: "patience", mode: "hybrid", view: "list", sort: "newest" });
    await waitFor(() => expect(locParams().get("sid")).toBe("sid-2"));
    expect(locParams().get("sort")).toBe("newest");
    expect(screen.getByRole("combobox", { name: "Sort by" })).toHaveValue("newest");
    expect(screen.getByRole("link", { name: "Friday talk" })).toHaveAttribute(
      "href",
      "/search/video/r1?q=patience&mode=hybrid&sort=newest&sid=sid-2"
    );
    // a filter keeps the order; a new query keeps it too, and drops the filters
    await user.click(facetButton("Library", /Lectures/));
    await waitFor(() => expect(searchBodies()).toHaveLength(3));
    expect(lastBody()).toMatchObject({ sort: "newest", filters: { library_id: ["lib-1"] } });
    await submit("gratitude");
    await waitFor(() => expect(searchBodies()).toHaveLength(4));
    expect(lastBody()).toEqual({ q: "gratitude", mode: "hybrid", view: "list", sort: "newest" });

    await user.selectOptions(screen.getByRole("combobox", { name: "Sort by" }), "matches");
    await waitFor(() => expect(searchBodies()).toHaveLength(5));
    expect(lastBody()).toMatchObject({ sort: "matches" });
    await waitFor(() => expect(locParams().get("sort")).toBe("newest"));
    expect(screen.getByRole("combobox", { name: "Sort by" })).toHaveValue("newest");
  });

  it("SRC-51: the facets stay while the lists page, and a reload by sid shows them again", async () => {
    const big: Corpus = { titles: manyTitles(45), transcripts: manyTranscripts(25) };
    serve(big, { help: () => ({ facets: FACETS, did_you_mean: "patience" }) });
    const r = renderSearchPage();
    await submit("patiense");
    await screen.findByRole("region", { name: "Narrow the results" });
    const user = userEvent.setup();
    const titles = screen.getByRole("region", { name: "In titles" });
    await user.click(
      within(within(titles).getByRole("navigation", { name: "In titles" })).getByRole("button", {
        name: "Page 2",
      })
    );
    await waitFor(() => expect(locParams().get("vp")).toBe("2"));
    await waitFor(() =>
      expect(getCalls("/api/search/results/")).toEqual([
        "/api/search/results/sid-1?list=titles&offset=20&limit=20",
      ])
    );
    expect(screen.getByRole("region", { name: "Narrow the results" })).toBeInTheDocument();
    expect(facetButton("Library", /Lectures/)).toBeInTheDocument();
    expect(screen.getByTestId("did-you-mean")).toBeInTheDocument();
    expect(searchBodies()).toHaveLength(1);
    const url = loc();
    r.unmount();

    // a reload: the pages are read by sid, the facets come from this tab's memory
    renderSearchPage(url);
    await waitFor(() =>
      expect(screen.getByRole("region", { name: "In titles" })).toHaveTextContent("Title 21")
    );
    expect(searchBodies()).toHaveLength(1);
    expect(screen.getByRole("region", { name: "Narrow the results" })).toBeInTheDocument();
    expect(screen.getByTestId("did-you-mean")).toHaveTextContent("Did you mean patience?");
  });

  it("SRC-51: an expired saved search runs again with its sort and filters, keeping the page", async () => {
    const big: Corpus = { titles: manyTitles(45), transcripts: manyTranscripts(3) };
    const state = serve(big, { help: () => ({ facets: FACETS }) });
    renderSearchPage("/search?q=patience&mode=hybrid&sort=newest&spk=Sheikh+Ahmad&year=2024");
    await waitFor(() => expect(locParams().get("sid")).toBe("sid-1"));
    await screen.findByTestId("facets");
    state.expired.add("sid-1");
    const user = userEvent.setup();
    const titles = screen.getByRole("region", { name: "In titles" });
    await user.click(
      within(within(titles).getByRole("navigation", { name: "In titles" })).getByRole("button", {
        name: "Page 2",
      })
    );
    await waitFor(() => expect(searchBodies()).toHaveLength(2));
    expect(lastBody()).toEqual({
      q: "patience",
      mode: "hybrid",
      view: "list",
      sort: "newest",
      filters: {
        speaker: ["Sheikh Ahmad"],
        date: { gte: "2024-01-01T00:00:00.000Z", lt: "2025-01-01T00:00:00.000Z" },
      },
    });
    await waitFor(() => expect(locParams().get("sid")).toBe("sid-2"));
    expect(locParams().get("vp")).toBe("2");
    expect(locParams().get("sort")).toBe("newest");
    expect(locParams().getAll("spk")).toEqual(["Sheikh Ahmad"]);
    expect(screen.getByRole("status")).toHaveTextContent("These results were refreshed");
  });

  it("SRC-52: Arabic: the facet names, bands, counts, sort and did-you-mean read in Arabic", async () => {
    localStorage.setItem("mkt:locale", "ar");
    serve(corpus, { help: () => ({ facets: FACETS, did_you_mean: "الصبر" }) });
    renderSearchPage();
    await waitFor(() => expect(document.documentElement).toHaveAttribute("dir", "rtl"));
    await submit("الصبرر");
    expect(await screen.findByTestId("did-you-mean")).toHaveTextContent("هل تقصد الصبر؟");
    expect(screen.getByRole("region", { name: "تضييق النتائج" })).toBeInTheDocument();
    expect(chipText(facetButton("المدة", /أقل من/))).toBe("أقل من 5 دقائق 2");
    expect(facetButton("المدة", /أكثر من ساعة/)).toHaveAccessibleName(
      "أكثر من ساعة عدد المقاطع: 13"
    );
    expect(chipText(facetButton("اللغة", /العربية/))).toBe("العربية 10");
    expect(chipText(within(facetGroup("السنة")).getAllByRole("button")[0]!)).toBe("2024 9");
    expect(screen.getByRole("region", { name: "تضييق النتائج" }).textContent).not.toMatch(/[٠-٩]/);
    const sort = screen.getByRole("combobox", { name: "الترتيب حسب" });
    expect(
      within(sort)
        .getAllByRole("option")
        .map((o) => o.textContent)
    ).toEqual(["الأقرب صلة", "الأحدث", "الأكثر مطابقات"]);
    const user = userEvent.setup();
    await user.click(facetButton("المكتبة", /Lectures/));
    const active = await screen.findByRole("group", { name: "عوامل التصفية المستخدمة" });
    expect(
      within(active).getByRole("button", { name: "إزالة المكتبة: Lectures" })
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "إزالة كل عوامل التصفية" })).toBeInTheDocument();
  });

  it("SRC-52: Arabic suggestions name their kinds in Arabic", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    localStorage.setItem("mkt:locale", "ar");
    serve(corpus, { suggest: () => SUGGEST });
    renderSearchPage();
    await waitFor(() => expect(document.documentElement).toHaveAttribute("dir", "rtl"));
    await typeAndWait("pat");
    expect(screen.getByRole("listbox", { name: "اقتراحات" })).toBeInTheDocument();
    expect(suggestions().map((o) => o.querySelector(".mkt-suggest__kind")?.textContent)).toEqual([
      "بحثت عنه",
      "كلمة",
      "مقطع",
      "متحدث",
    ]);
  });

  it.each(["en", "ar"])(
    "SRC-53 (%s): axe finds nothing with the suggestions open, did-you-mean and facets shown",
    async (locale) => {
      vi.useFakeTimers({ shouldAdvanceTime: true });
      localStorage.setItem("mkt:locale", locale);
      serve(corpus, {
        suggest: () => SUGGEST,
        help: () => ({ facets: FACETS, did_you_mean: "patience" }),
      });
      renderSearchPage("/search?q=patiense&mode=hybrid&lib=lib-1");
      await screen.findByTestId("did-you-mean");
      await screen.findByTestId("facets");
      await typeAndWait("pat");
      fireEvent.keyDown(box(), { key: "ArrowDown" });
      expect(screen.getByRole("listbox")).toBeInTheDocument();
      vi.useRealTimers();
      expect(await axeViolations(document.body)).toEqual([]);
    },
    30_000
  );
});

// Story 49.10's follow-up: a recent or saved search opens with the order and filters it was
// asked with, and a page opened by its sid alone names its filters and shows its facets.
describe("Search state kept (story 49.10 follow-up)", () => {
  const recentPanel = () => screen.getByRole("complementary", { name: "Recent searches" });
  const savedPanel = () => screen.getByRole("complementary", { name: "Saved searches" });
  const activeChips = () =>
    within(screen.getByRole("group", { name: "Filters in use" }))
      .getAllByRole("listitem")
      .map((li) => li.textContent);
  const row = (sid: string, live: boolean, extra: Record<string, unknown> = {}) => ({
    search_id: sid,
    q: "patience",
    mode: "fts",
    created_at: "2026-11-22T10:00:00Z",
    title_count: 1,
    transcript_count: 1,
    live,
    ...extra,
  });

  it("SRC-54: a recent search opens with its sort and filters: live by its sid, expired run again with them", async () => {
    serve(corpus, {
      existing: { "sid-r": { q: "patience", mode: "fts", sort: "newest", corpus } },
      help: () => ({ facets: FACETS }),
      recent: [
        row("sid-r", true, {
          sort: "newest",
          filters: { library_id: ["lib-1"], duration_sec: { gte: 0, lt: 300 } },
        }),
        row("sid-x", false, {
          q: "sabr",
          sort: "matches",
          filters: {
            speaker: ["Sheikh Ahmad"],
            date: { gte: "2024-01-01T00:00:00.000Z", lt: "2025-01-01T00:00:00.000Z" },
          },
        }),
      ],
    });
    renderSearchPage();
    const user = userEvent.setup();
    await user.click(await within(recentPanel()).findByRole("button", { name: /^patience/ }));
    await waitFor(() =>
      expect(loc()).toBe("/search?q=patience&mode=fts&sort=newest&lib=lib-1&dur=under_5m&sid=sid-r")
    );
    // read by its sid, no new search; its order and filters on screen, its facets with them
    expect(searchBodies()).toHaveLength(0);
    expect(await screen.findByTestId("facets")).toBeInTheDocument();
    expect(screen.getByRole("combobox", { name: "Sort by" })).toHaveValue("newest");
    expect(activeChips()).toEqual(["Library: Lectures", "Length: Under 5 min"]);

    await user.click(within(recentPanel()).getByRole("button", { name: /^sabr/ }));
    await waitFor(() => expect(searchBodies()).toHaveLength(1));
    expect(lastBody()).toEqual({
      q: "sabr",
      mode: "fts",
      view: "list",
      sort: "matches",
      filters: {
        speaker: ["Sheikh Ahmad"],
        date: { gte: "2024-01-01T00:00:00.000Z", lt: "2025-01-01T00:00:00.000Z" },
      },
    });
    await waitFor(() => expect(locParams().get("sid")).toBe("sid-1"));
    expect(locParams().get("sort")).toBe("matches");
    expect(locParams().getAll("spk")).toEqual(["Sheikh Ahmad"]);
    expect(locParams().get("year")).toBe("2024");
  });

  it("SRC-54: a recent search kept before the home kept its sort and filters opens as this tab ran it, else plain", async () => {
    serve(corpus, {
      existing: {
        "sid-a": { q: "patience", mode: "fts", corpus },
        "sid-b": { q: "sabr", mode: "fts", corpus },
      },
      recent: [row("sid-a", true), row("sid-b", true, { q: "sabr" })],
    });
    sessionStorage.setItem(
      "mkt:search:help",
      JSON.stringify({
        "sid-a": { sort: "newest", sel: { library: [], language: ["ar"], speaker: [] } },
      })
    );
    renderSearchPage();
    const user = userEvent.setup();
    await user.click(await within(recentPanel()).findByRole("button", { name: /^patience/ }));
    await waitFor(() =>
      expect(loc()).toBe("/search?q=patience&mode=fts&sort=newest&lang=ar&sid=sid-a")
    );
    await user.click(within(recentPanel()).getByRole("button", { name: /^sabr/ }));
    await waitFor(() => expect(loc()).toBe("/search?q=sabr&mode=fts&sid=sid-b"));
    expect(searchBodies()).toHaveLength(0);
  });

  it("SRC-55: a saved search runs with its sort and filters; one saved without them runs plain", async () => {
    serve(corpus, {
      help: () => ({ facets: FACETS }),
      saved: {
        items: [
          {
            id: "s1",
            name: "patience · Newest · Lectures",
            query: {
              q: "patience",
              mode: "fts",
              sort: "newest",
              filters: { library_id: ["lib-1"], duration_sec: { gte: 3600 } },
            },
            created_at: "2026-11-22T10:00:00Z",
          },
          {
            id: "s2",
            name: "sabr",
            query: { q: "sabr", mode: "hybrid" },
            created_at: "2026-11-01T10:00:00Z",
          },
        ],
      },
    });
    renderSearchPage();
    const user = userEvent.setup();
    await user.click(
      await within(savedPanel()).findByRole("button", { name: "patience · Newest · Lectures" })
    );
    await waitFor(() => expect(searchBodies()).toHaveLength(1));
    expect(lastBody()).toEqual({
      q: "patience",
      mode: "fts",
      view: "list",
      sort: "newest",
      filters: { library_id: ["lib-1"], duration_sec: { gte: 3600 } },
    });
    await waitFor(() =>
      expect(loc()).toBe("/search?q=patience&mode=fts&sort=newest&lib=lib-1&dur=over_60m&sid=sid-1")
    );
    expect(screen.getByRole("combobox", { name: "Sort by" })).toHaveValue("newest");
    expect(activeChips()).toEqual(["Library: Lectures", "Length: Over an hour"]);

    // an older saved search (no sort, no filters): relevance, and the filters on screen go
    await user.click(within(savedPanel()).getByRole("button", { name: "sabr" }));
    await waitFor(() => expect(searchBodies()).toHaveLength(2));
    expect(lastBody()).toEqual({ q: "sabr", mode: "hybrid", view: "list" });
    await waitFor(() => expect(loc()).toBe("/search?q=sabr&mode=hybrid&sid=sid-2"));
    expect(screen.queryByRole("group", { name: "Filters in use" })).toBeNull();
  });

  it("SRC-55: Save keeps the sort and filters on screen, named by them", async () => {
    serve(corpus, { help: () => ({ facets: FACETS }), otherPost: () => ({}) });
    renderSearchPage("/search?q=patience&mode=fts&sort=newest&lib=lib-1&year=2024");
    await screen.findByTestId("facets");
    await userEvent.setup().click(screen.getByRole("button", { name: "Save" }));
    expect(post).toHaveBeenCalledWith("/api/search/save", {
      name: "patience · Newest · Lectures · 2024",
      query: {
        q: "patience",
        mode: "fts",
        sort: "newest",
        filters: {
          library_id: ["lib-1"],
          date: { gte: "2024-01-01T00:00:00.000Z", lt: "2025-01-01T00:00:00.000Z" },
        },
      },
    });
  });

  it("SRC-56: a shared filtered link in a new tab names its library at once, before any facet count", async () => {
    // the page answers no facets yet: the name can only come from the libraries list
    serve(corpus, {
      existing: { "sid-s": { q: "patience", mode: "fts", corpus } },
      libraries: [
        { id: "lib-1", name: "Lectures" },
        { id: "lib-2", name: "Khutbahs" },
      ],
    });
    renderSearchPage("/search?q=patience&mode=fts&lib=lib-1&sid=sid-s");
    await waitFor(() => expect(activeChips()).toEqual(["Library: Lectures"]));
    expect(screen.queryByTestId("facets")).toBeNull();
    expect(getCalls("/api/libraries/summary")).toHaveLength(1);
    expect(searchBodies()).toHaveLength(0);
  });

  it("SRC-56: a libraries list kept from before that lacks a library is read again to name it", async () => {
    // the list this session read earlier knows Lectures only; Khutbahs was added since
    keepAnswer(memberUser.id, "libraries", [
      { id: "lib-1", name: "Lectures", videos: 1, poster_video_ids: [], pinned: false },
    ]);
    serve(corpus, {
      existing: { "sid-s": { q: "patience", mode: "fts", corpus } },
      libraries: [
        { id: "lib-1", name: "Lectures" },
        { id: "lib-2", name: "Khutbahs" },
      ],
    });
    renderSearchPage("/search?q=patience&mode=fts&lib=lib-1&lib=lib-2&sid=sid-s");
    await waitFor(() => expect(activeChips()).toEqual(["Library: Lectures", "Library: Khutbahs"]));
    expect(getCalls("/api/libraries/summary")).toHaveLength(1);
  });

  it("SRC-57: a link naming another order than its search's own follows the search's", async () => {
    serve(corpus, {
      existing: { "sid-s": { q: "patience", mode: "fts", sort: "newest", corpus } },
    });
    renderSearchPage("/search?q=patience&mode=fts&sort=matches&sid=sid-s");
    await waitFor(() => expect(loc()).toBe("/search?q=patience&mode=fts&sort=newest&sid=sid-s"));
    expect(screen.getByRole("combobox", { name: "Sort by" })).toHaveValue("newest");
    expect(searchBodies()).toHaveLength(0);
  });

  it("SRC-57: a page opened by its sid alone shows its facets and did-you-mean, read with its first page", async () => {
    serve(corpus, {
      existing: { "sid-s": { q: "patiense", mode: "fts", sort: "newest", corpus } },
      help: () => ({ facets: FACETS, did_you_mean: "patience" }),
    });
    renderSearchPage("/search?q=patiense&mode=fts&sort=newest&sid=sid-s");
    expect(await screen.findByTestId("facets")).toBeInTheDocument();
    expect(screen.getByTestId("did-you-mean")).toHaveTextContent("Did you mean patience?");
    expect(facetButton("Library", /Lectures/)).toBeInTheDocument();
    expect(getCalls("/api/search/results/").sort()).toEqual([
      "/api/search/results/sid-s?list=titles&offset=0&limit=20&facets=1",
      "/api/search/results/sid-s?list=transcripts&offset=0&limit=20",
    ]);
    expect(searchBodies()).toHaveLength(0);
  });

  it("SRC-57: Arabic: a recent search opened with its filters names them in Arabic", async () => {
    localStorage.setItem("mkt:locale", "ar");
    serve(corpus, {
      existing: { "sid-r": { q: "الصبر", mode: "fts", sort: "newest", corpus } },
      help: () => ({ facets: FACETS }),
      recent: [
        row("sid-r", true, {
          q: "الصبر",
          sort: "newest",
          filters: { language: ["ar"], duration_sec: { gte: 0, lt: 300 } },
        }),
      ],
    });
    renderSearchPage();
    await waitFor(() => expect(document.documentElement).toHaveAttribute("dir", "rtl"));
    const recent = screen.getByRole("complementary", { name: "عمليات البحث الأخيرة" });
    await userEvent.setup().click(await within(recent).findByRole("button", { name: /^الصبر/ }));
    const active = await screen.findByRole("group", { name: "عوامل التصفية المستخدمة" });
    expect(
      within(active)
        .getAllByRole("listitem")
        .map((li) => li.textContent)
    ).toEqual(["اللغة: العربية", "المدة: أقل من 5 دقائق"]);
    expect(screen.getByRole("combobox", { name: "الترتيب حسب" })).toHaveValue("newest");
  });
});
