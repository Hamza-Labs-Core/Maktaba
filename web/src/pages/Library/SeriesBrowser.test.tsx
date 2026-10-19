// Story 26.10 — SeriesBrowser renders the cross-library grid and, per
// AC test_rtl_render, an Arabic series name carries dir="auto" so it
// renders right-to-left.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, waitFor, within, fireEvent } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, useLocation } from "react-router-dom";
import { ApiError } from "../../lib/api";
import { renderPage, memberUser, quietCrash } from "../../test/pageHarness";

const { get } = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get } };
});

import { SeriesBrowser } from "./SeriesBrowser";
import { I18nProvider } from "../../lib/i18n";

function renderBrowser() {
  return render(
    <MemoryRouter>
      <I18nProvider>
        <SeriesBrowser />
      </I18nProvider>
    </MemoryRouter>
  );
}

describe("SeriesBrowser", () => {
  beforeEach(() => get.mockReset());

  it("renders series cards from the cross-library list", async () => {
    get.mockResolvedValue({
      items: [
        {
          id: "s1",
          name: "Breaking Bad",
          numbering: "season",
          season_count: 5,
          episode_count: 62,
          watched_count: 10,
          in_progress: 1,
          year: 2008,
        },
      ],
    });
    renderBrowser();
    await waitFor(() => expect(screen.getByText("Breaking Bad")).toBeInTheDocument());
  });

  it("marks an Arabic series name dir=auto for RTL", async () => {
    get.mockResolvedValue({
      items: [
        {
          id: "s2",
          name: "باب الحارة",
          numbering: "season",
          season_count: 11,
          episode_count: 400,
          watched_count: 0,
          in_progress: 0,
        },
      ],
    });
    renderBrowser();
    const name = await screen.findByText("باب الحارة");
    expect(name).toHaveAttribute("dir", "auto");
  });

  it("shows an empty state when there are no series", async () => {
    get.mockResolvedValue({ items: [] });
    renderBrowser();
    await waitFor(() => expect(screen.getByText(/No series detected yet/)).toBeInTheDocument());
  });
});

const series = (id: string, name: string, over: Record<string, unknown> = {}) => ({
  id,
  name,
  numbering: "season",
  season_count: 1,
  episode_count: 2,
  watched_count: 0,
  in_progress: 0,
  ...over,
});

function Loc() {
  const { pathname, search } = useLocation();
  return <span data-testid="loc">{pathname + search}</span>;
}

function renderSeries(opts: { user?: typeof memberUser | null; history?: string[] } = {}) {
  return renderPage(
    <>
      <SeriesBrowser />
      <Loc />
    </>,
    {
      path: "/series",
      user: opts.user === undefined ? memberUser : opts.user,
      history: opts.history,
      extraRoutes: <Route path="/series/:id" element={<p>series detail page</p>} />,
    }
  );
}

const problem = (status: number, title: string) =>
  new ApiError({ type: "about:blank", title, status });

const names = () =>
  Array.from(document.querySelectorAll(".mkt-series-card__name")).map((e) => e.textContent);

describe("SeriesBrowser (test-cases/series-browser.md)", () => {
  beforeEach(() => {
    get.mockReset();
  });
  afterEach(() => vi.restoreAllMocks());

  it("SER-01: grid shows a card per series with name, counts, progress", async () => {
    get.mockResolvedValue({
      items: [
        series("s1", "Breaking Bad", {
          season_count: 5,
          episode_count: 62,
          watched_count: 31,
          in_progress: 1,
          year: 2008,
          poster_video_id: "v9",
        }),
      ],
    });
    renderSeries();
    expect(screen.getByRole("heading", { name: "Series" })).toBeInTheDocument();
    const link = await screen.findByRole("link", { name: /Breaking Bad/ });
    expect(get).toHaveBeenCalledWith("/api/series?sort=name");
    expect(link).toHaveAttribute("href", "/series/s1");
    const list = screen.getByRole("list");
    expect(within(list).getAllByRole("listitem")).toHaveLength(1);
    expect(within(link).getByText("2008")).toBeInTheDocument();
    expect(within(link).getByText("5 · 62")).toBeInTheDocument();
    const bar = within(link).getByRole("progressbar", { name: "31 of 62 watched" });
    expect(bar).toHaveAttribute("aria-valuenow", "50");
  });

  it.each([
    ["items: null", { items: null }],
    ["items missing", {}],
    ["items not an array", { items: "oops" }],
  ])("SER-02: %s renders the empty state", async (_label, payload) => {
    get.mockResolvedValue(payload);
    renderSeries();
    expect(await screen.findByText("No series detected yet")).toBeInTheDocument();
    expect(
      screen.getByText("Series appear here once episodes are grouped across your libraries.")
    ).toBeInTheDocument();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("SER-03: null / missing numeric and optional fields do not crash", async () => {
    get.mockResolvedValue({
      items: [
        {
          id: "s1",
          name: "A",
          numbering: "season",
          season_count: null,
          episode_count: 0,
          watched_count: 0,
          in_progress: 0,
        },
        { id: "s2", name: "", numbering: "absolute", episode_count: null, watched_count: null },
        { id: "s3", name: null, poster_path: null, poster_video_id: null },
      ],
    });
    renderSeries();
    await screen.findByText("A");
    expect(screen.getAllByRole("link", { name: /./ }).filter((l) => l.closest("li"))).toHaveLength(
      3
    );
    for (const bar of screen.getAllByRole("progressbar")) {
      expect(bar).toHaveAttribute("aria-valuenow", "0");
    }
    expect(document.body.textContent).not.toMatch(/NaN|undefined|null/);
    expect(document.querySelectorAll(".mkt-series-card__poster--ph")).toHaveLength(3);
    // An empty or null name falls back to the series id so the card has a label.
    expect(screen.getByRole("link", { name: /s2/ })).toHaveAttribute("href", "/series/s2");
    expect(screen.getByRole("link", { name: /s3/ })).toHaveAttribute("href", "/series/s3");
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("SER-05: render crash shows route error fallback; Back still works", async () => {
    quietCrash();
    let bad = true;
    get.mockImplementation(() =>
      Promise.resolve({ items: [series("s1", bad ? ({} as unknown as string) : "Fixed")] })
    );
    renderSeries();
    expect(
      await screen.findByText("This page hit an error and could not be shown.")
    ).toBeInTheDocument();
    const user = userEvent.setup();
    await user.click(screen.getByRole("link", { name: "go elsewhere" }));
    expect(screen.getByText("elsewhere")).toBeInTheDocument();
    bad = false;
    await user.click(screen.getByRole("button", { name: "go back" }));
    expect(await screen.findByText("Fixed")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("SER-06: signed-out user is sent to /login and no series are requested", () => {
    renderSeries({ user: null });
    expect(screen.getByText("login page (from /series)")).toBeInTheDocument();
    expect(get).not.toHaveBeenCalled();
  });

  it("SER-08: loading text in a status region while pending", () => {
    get.mockReturnValue(new Promise(() => {}));
    renderSeries();
    expect(screen.getByRole("status")).toHaveTextContent("Loading…");
    expect(screen.queryByText("No series detected yet")).toBeNull();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it.each([
    ["500", problem(500, "list series failed"), "list series failed"],
    ["403", problem(403, "forbidden"), "forbidden"],
    ["network failure", new TypeError("Failed to fetch"), "Something went wrong."],
    ["malformed JSON", new SyntaxError("Unexpected token <"), "Something went wrong."],
  ])("SER-09/SER-10: %s shows the ErrorState", async (_label, err, desc) => {
    get.mockRejectedValue(err);
    renderSeries();
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Something went wrong.");
    expect(alert).toHaveTextContent(desc);
    expect(screen.queryByText("Loading…")).toBeNull();
    expect(screen.queryByText("No series detected yet")).toBeNull();
  });

  it("SER-11: Try again re-fetches and shows the series", async () => {
    get.mockRejectedValueOnce(problem(500, "boom")).mockResolvedValue({
      items: [series("s1", "Back again")],
    });
    renderSeries();
    const alert = await screen.findByRole("alert");
    await userEvent.setup().click(within(alert).getByRole("button", { name: "Try again" }));
    expect(await screen.findByText("Back again")).toBeInTheDocument();
    expect(get).toHaveBeenCalledTimes(2);
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("SER-12: sort change re-fetches with sort=progress and keeps it in the URL", async () => {
    get.mockImplementation((url: string) => {
      return Promise.resolve({
        items: url.endsWith("progress")
          ? [series("b", "B"), series("a", "A")]
          : [series("a", "A"), series("b", "B")],
      });
    });
    renderSeries();
    await screen.findByText("A");
    await userEvent
      .setup()
      .selectOptions(screen.getByRole("combobox", { name: "Sort" }), "progress");
    expect(get).toHaveBeenLastCalledWith("/api/series?sort=progress");
    expect(screen.getByTestId("loc")).toHaveTextContent("/series?sort=progress");
    await waitFor(() => expect(names()).toEqual(["B", "A"]));
  });

  it("SER-12: a deep link with ?sort=progress requests that sort", async () => {
    get.mockResolvedValue({ items: [] });
    renderPage(<SeriesBrowser />, { path: "/series?sort=progress", user: memberUser });
    await waitFor(() => expect(get).toHaveBeenCalledWith("/api/series?sort=progress"));
    expect(screen.getByRole("combobox", { name: "Sort" })).toHaveValue("progress");
  });

  it("SER-13: fast sort changes: last response wins", async () => {
    let resolveProgress: (v: unknown) => void = () => {};
    get.mockImplementation((url: string) => {
      if (url.endsWith("progress")) return new Promise((r) => (resolveProgress = r));
      return Promise.resolve({ items: [series("n", "N")] });
    });
    renderSeries();
    await screen.findByText("N");
    const select = screen.getByRole("combobox", { name: "Sort" });
    fireEvent.change(select, { target: { value: "progress" } });
    fireEvent.change(select, { target: { value: "name" } });
    await screen.findByText("N");
    resolveProgress({ items: [series("p", "P")] });
    await new Promise((r) => setTimeout(r, 0));
    expect(names()).toEqual(["N"]);
    expect(select).toHaveValue("name");
  });

  it("SER-14: poster source rules and broken-poster fallback", async () => {
    get.mockResolvedValue({
      items: [
        series("s1", "A", { poster_path: "https://img.example/p.jpg" }),
        series("s2", "B", { poster_path: "/api/series/s2/poster" }),
        series("s3", "C", { poster_path: "/mnt/media/poster.jpg", poster_video_id: "v3" }),
        series("s4", "D"),
      ],
    });
    renderSeries();
    await screen.findByText("A");
    const img = (name: string) =>
      screen.getByRole("link", { name: new RegExp(name) }).querySelector("img");
    expect(img("A")).toHaveAttribute("src", "https://img.example/p.jpg");
    expect(img("B")).toHaveAttribute("src", "/api/series/s2/poster");
    expect(img("C")).toHaveAttribute("src", "/api/videos/v3/poster");
    expect(img("D")).toBeNull();
    for (const i of document.querySelectorAll("img")) expect(i).toHaveAttribute("alt", "");
    fireEvent.error(img("A")!);
    expect(img("A")).toBeNull();
    expect(
      screen.getByRole("link", { name: /A/ }).querySelector(".mkt-series-card__poster--ph")
    ).not.toBeNull();
  });

  it("SER-15: card links to /series/:id; Back returns to the grid", async () => {
    get.mockResolvedValue({ items: [series("s1", "Show")] });
    renderSeries();
    const user = userEvent.setup();
    await user.click(await screen.findByRole("link", { name: /Show/ }));
    expect(screen.getByText("series detail page")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "go back" }));
    expect(await screen.findByText("Show")).toBeInTheDocument();
  });

  it("SER-17: Arabic UI uses ar strings and Arabic digits consistently", async () => {
    localStorage.setItem("mkt:locale", "ar");
    get.mockResolvedValue({
      items: [
        series("s1", "باب الحارة", { season_count: 11, episode_count: 400, watched_count: 12 }),
      ],
    });
    renderSeries();
    const name = await screen.findByText("باب الحارة");
    expect(name).toHaveAttribute("dir", "auto");
    await waitFor(() => expect(document.documentElement).toHaveAttribute("dir", "rtl"));
    expect(screen.getByRole("heading", { name: "المسلسلات" })).toBeInTheDocument();
    expect(screen.getByText("١١ · ٤٠٠")).toBeInTheDocument();
    expect(screen.getByRole("progressbar")).toHaveAttribute("aria-label", "تمت مشاهدة ١٢ من ٤٠٠");
  });
});
