// Series detail cases from web/test-cases/series-detail.md.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Route } from "react-router-dom";
import { ToastProvider } from "@ds/components/Toast/Toast";
import { ApiError } from "../../lib/api";
import { renderPage, memberUser, adminUser, quietCrash } from "../../test/pageHarness";

const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post } };
});

import { SeriesDetail } from "./SeriesDetail";

type Answer = unknown | ((url: string) => unknown);

const problem = (status: number, title: string) =>
  new ApiError({ type: "about:blank", title, status });

const HEADER = {
  id: "s1",
  name: "Breaking Bad",
  year: 2008,
  numbering: "season",
  poster_path: "https://img.example/bb.jpg",
};
const EPISODES = {
  numbering: "season",
  seasons: [
    {
      season: 1,
      episodes: [
        {
          video_id: "e1",
          season: 1,
          episode: 1,
          title: "Pilot",
          thumbnail: "/api/videos/e1/poster",
          progress_pct: 0,
          watched: true,
        },
        {
          video_id: "e2",
          season: 1,
          episode: 2,
          title: "Cat's in the Bag",
          progress_pct: 0,
          watched: false,
        },
      ],
    },
    {
      season: 2,
      episodes: [
        { video_id: "e3", season: 2, episode: 1, title: "737", progress_pct: 0, watched: false },
      ],
    },
  ],
};

/** Serves the three series GETs for any id; values may be functions that throw or return promises. */
function serve(over: { header?: Answer; episodes?: Answer; missing?: Answer } = {}, id = "s1") {
  const table: Record<string, Answer> = {
    [`/api/series/${id}`]: over.header ?? HEADER,
    [`/api/series/${id}/episodes`]: over.episodes ?? EPISODES,
    [`/api/series/${id}/missing`]: over.missing ?? { gaps: [], numbering: "season" },
  };
  const prev = get.getMockImplementation();
  get.mockImplementation((url: string) => {
    if (url in table) {
      const v = table[url];
      try {
        return Promise.resolve(typeof v === "function" ? (v as (u: string) => unknown)(url) : v);
      } catch (e) {
        return Promise.reject(e);
      }
    }
    return prev ? prev(url) : Promise.reject(new Error(`unmocked GET ${url}`));
  });
}

const reject = (e: unknown) => () => Promise.reject(e);

function renderDetail(
  path = "/series/s1",
  opts: { user?: typeof adminUser | null; history?: string[] } = {}
) {
  return renderPage(
    <ToastProvider>
      <SeriesDetail />
    </ToastProvider>,
    {
      path,
      route: "/series/:id",
      user: opts.user === undefined ? memberUser : opts.user,
      history: opts.history,
      extraRoutes: (
        <>
          <Route path="/series" element={<p>series grid page</p>} />
          <Route path="/videos/:videoId" element={<p>video detail page</p>} />
          <Route path="/videos/:videoId/watch" element={<p>player page</p>} />
        </>
      ),
    }
  );
}

const seasonCells = (heading: string) => {
  const h = screen.getByRole("heading", { level: 2, name: heading });
  return within(h.closest("section")!).queryAllByRole("listitem");
};

describe("SeriesDetail (test-cases/series-detail.md)", () => {
  beforeEach(() => {
    get.mockReset();
    post.mockReset();
  });
  afterEach(() => vi.restoreAllMocks());

  it("SED-01: header, seasons and episode grid render", async () => {
    serve();
    renderDetail("/series/s1", { user: adminUser });
    const h1 = await screen.findByRole("heading", { level: 1, name: "Breaking Bad" });
    expect(h1).toHaveAttribute("dir", "auto");
    expect(screen.getByText("2008")).toBeInTheDocument();
    expect(document.querySelector("img.mkt-series-detail__poster")).toHaveAttribute(
      "src",
      "https://img.example/bb.jpg"
    );
    expect(seasonCells("Season 1")).toHaveLength(2);
    expect(seasonCells("Season 2")).toHaveLength(1);
    expect(screen.getByRole("link", { name: /Pilot/ })).toHaveAttribute("href", "/videos/e1");
    expect(screen.getByRole("link", { name: /Cat's in the Bag/ })).toHaveAttribute(
      "href",
      "/videos/e2"
    );
    expect(
      Array.from(document.querySelectorAll(".mkt-episode__no")).map((e) => e.textContent)
    ).toEqual(["E01", "E02", "E01"]);
    expect(screen.getByRole("button", { name: "Accept all episode matches" })).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: /Continue watching/ })).toBeNull();
  });

  it("SED-02: Continue watching links to the next episode's player", async () => {
    serve({
      episodes: {
        ...EPISODES,
        next_episode: {
          video_id: "e2",
          title: "Cat's in the Bag",
          progress_pct: 40,
          watched: false,
        },
      },
    });
    renderDetail();
    const link = await screen.findByRole("link", { name: "▶ Continue watching" });
    expect(link).toHaveAttribute("href", "/videos/e2/watch");
    await userEvent.setup().click(link);
    expect(screen.getByText("player page")).toBeInTheDocument();
  });

  it.todo("SED-02: Continue watching resumes at the saved position (needs VPL-09)");

  it.each([
    ["seasons: null", { seasons: null, numbering: "season" }],
    ["seasons missing", { numbering: "season" }],
    ["seasons: []", { seasons: [] }],
  ])("SED-03: %s renders the No episodes empty state", async (_label, episodes) => {
    serve({ episodes });
    renderDetail();
    expect(await screen.findByText("No episodes in this series yet.")).toBeInTheDocument();
    expect(screen.getByRole("heading", { level: 1, name: "Breaking Bad" })).toBeInTheDocument();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("SED-04: a season with episodes null / missing does not crash", async () => {
    serve({
      episodes: {
        numbering: "season",
        seasons: [
          { season: 1, episodes: null },
          { season: 2 },
          {
            season: 3,
            episodes: [
              { video_id: "e9", episode: 1, title: "Ok", progress_pct: 0, watched: false },
            ],
          },
        ],
      },
      missing: { gaps: [{ season: 1, episode: 4 }] },
    });
    renderDetail();
    expect(await screen.findByText("Ok")).toBeInTheDocument();
    await waitFor(() => expect(seasonCells("Season 1")).toHaveLength(1));
    expect(seasonCells("Season 1")[0]).toHaveTextContent("E04Missing");
    expect(seasonCells("Season 2")).toHaveLength(0);
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it.each([
    ["gaps: null", { gaps: null }],
    ["gaps missing", {}],
    ["500", reject(problem(500, "boom"))],
    ["a null entry", { gaps: [null] }],
  ])("SED-05: %s renders no ghost cells", async (_label, missing) => {
    serve({ missing });
    renderDetail();
    expect(await screen.findByText("Pilot")).toBeInTheDocument();
    await new Promise((r) => setTimeout(r, 0));
    expect(document.querySelector(".mkt-episode--missing")).toBeNull();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("SED-06: render crash shows route error fallback; Back still works", async () => {
    quietCrash();
    let bad = true;
    serve({
      // an object where the title string is rendered
      episodes: () =>
        bad
          ? {
              numbering: "season",
              seasons: [
                {
                  season: 1,
                  episodes: [{ video_id: "e1", episode: 1, title: {}, progress_pct: 0 }],
                },
              ],
            }
          : EPISODES,
    });
    renderDetail();
    expect(
      await screen.findByText("This page hit an error and could not be shown.")
    ).toBeInTheDocument();
    const user = userEvent.setup();
    await user.click(screen.getByRole("link", { name: "go elsewhere" }));
    expect(screen.getByText("elsewhere")).toBeInTheDocument();
    bad = false;
    await user.click(screen.getByRole("button", { name: "go back" }));
    expect(await screen.findByText("Pilot")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it.each([
    [404, "not_found"],
    [403, "permission"],
  ])(
    "SED-07/SED-10: a %s answer leaks nothing and shows a not-found error with a link back",
    async (status, kind) => {
      const e = problem(status, "series not found");
      serve({ header: reject(e), episodes: reject(e), missing: reject(e) }, "s-hidden");
      renderDetail("/series/s-hidden");
      const alert = await screen.findByRole("alert");
      expect(alert).toHaveAttribute("data-kind", kind);
      expect(alert).toHaveTextContent("series not found");
      expect(document.querySelector(".mkt-episode")).toBeNull();
      await userEvent.setup().click(within(alert).getByRole("link", { name: "Series" }));
      expect(screen.getByText("series grid page")).toBeInTheDocument();
    }
  );

  it("SED-08: non-admin does not see Accept all; admin does", async () => {
    serve();
    const first = renderDetail();
    await screen.findByText("Pilot");
    expect(screen.queryByRole("button", { name: "Accept all episode matches" })).toBeNull();
    first.unmount();
    renderDetail("/series/s1", { user: adminUser });
    expect(
      await screen.findByRole("button", { name: "Accept all episode matches" })
    ).toBeInTheDocument();
  });

  it("SED-09: signed-out user is sent to /login and no series requests are sent", () => {
    serve();
    renderDetail("/series/s1", { user: null });
    expect(screen.getByText("login page (from /series/s1)")).toBeInTheDocument();
    expect(get).not.toHaveBeenCalled();
  });

  it.each([
    ["500", problem(500, "boom"), "boom"],
    ["network failure", new TypeError("Failed to fetch"), "Something went wrong."],
    ["malformed JSON", new SyntaxError("Unexpected token b"), "Something went wrong."],
  ])("SED-11: episodes %s shows the ErrorState with Try again", async (_l, e, desc) => {
    let fail = true;
    serve({ episodes: () => (fail ? Promise.reject(e) : EPISODES) });
    renderDetail();
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Something went wrong.");
    expect(alert).toHaveTextContent(desc);
    fail = false;
    await userEvent.setup().click(within(alert).getByRole("button", { name: "Try again" }));
    expect(await screen.findByText("Pilot")).toBeInTheDocument();
  });

  it("SED-12: header fails but episodes load: fallback title", async () => {
    serve({ header: reject(problem(500, "boom")) });
    renderDetail();
    expect(await screen.findByText("Pilot")).toBeInTheDocument();
    expect(screen.getByRole("heading", { level: 1, name: "Series" })).toBeInTheDocument();
    expect(document.querySelector("img.mkt-series-detail__poster")).toBeNull();
    expect(screen.queryByText("2008")).toBeNull();
    expect(screen.getByRole("heading", { level: 2, name: "Season 2" })).toBeInTheDocument();
  });

  it("SED-13: loading text until episodes arrive", async () => {
    serve({ episodes: () => new Promise(() => {}) });
    renderDetail();
    expect(screen.getByText("Loading…")).toBeInTheDocument();
    await new Promise((r) => setTimeout(r, 0));
    expect(screen.getByText("Loading…")).toBeInTheDocument();
    expect(screen.queryByRole("heading", { level: 1 })).toBeNull();
    expect(screen.queryByText("No episodes in this series yet.")).toBeNull();
  });

  it("SED-14: missing episodes show as ghost cells in order, incl. a gap-only season", async () => {
    serve({
      episodes: {
        numbering: "season",
        seasons: [
          {
            season: 1,
            episodes: [1, 2, 4].map((e) => ({
              video_id: `e${e}`,
              episode: e,
              title: `Ep ${e}`,
              progress_pct: 0,
              watched: false,
            })),
          },
        ],
      },
      missing: {
        gaps: [
          { season: 1, episode: 3 },
          { season: 1, episode: 5 },
          { season: 4, episode: 1 },
        ],
      },
    });
    renderDetail();
    await screen.findByText("Ep 1");
    await waitFor(() => expect(seasonCells("Season 1")).toHaveLength(5));
    expect(seasonCells("Season 1").map((li) => li.textContent)).toEqual([
      "▤E01Ep 1",
      "▤E02Ep 2",
      "?E03Missing",
      "▤E04Ep 4",
      "?E05Missing",
    ]);
    const ghost = seasonCells("Season 1")[2];
    expect(ghost.querySelector('[aria-disabled="true"]')).not.toBeNull();
    expect(within(ghost).queryByRole("link")).toBeNull();
    expect(seasonCells("Season 4").map((li) => li.textContent)).toEqual(["?E01Missing"]);
  });

  it("SED-15: season 0 shows as Specials; absolute numbering badge", async () => {
    const ep = (id: string) => ({ video_id: id, episode: 1, title: id, progress_pct: 0 });
    serve({
      episodes: {
        numbering: "absolute",
        seasons: [
          { season: 0, episodes: [ep("sp")] },
          { season: 1, episodes: [ep("one")] },
        ],
      },
    });
    renderDetail();
    expect(await screen.findByRole("heading", { level: 2, name: "Specials" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { level: 2, name: "Season 1" })).toBeInTheDocument();
    expect(screen.getByText("Absolute numbering")).toBeInTheDocument();
  });

  it("SED-16: episode number label: E01 / E12 / #12 / E00 / none", async () => {
    const eps = [
      { episode: 1 },
      { episode: 12 },
      { episode: null, absolute_number: 12 },
      { episode: 0 },
      {},
    ].map((x, i) => ({ video_id: `e${i}`, title: `T${i}`, progress_pct: 0, ...x }));
    serve({ episodes: { numbering: "season", seasons: [{ season: 1, episodes: eps }] } });
    renderDetail();
    await screen.findByText("T0");
    const labels = Array.from(document.querySelectorAll(".mkt-episode__no")).map(
      (e) => e.textContent
    );
    // ghost-free: the label without a number sorts last
    expect(labels).toEqual(["E00", "E01", "E12", "#12", ""]);
    expect(document.body.textContent).not.toMatch(/undefined|null/);
  });

  it("SED-17: watched and in-progress markers", async () => {
    const eps = [
      { watched: true, progress_pct: 100 },
      { watched: false, progress_pct: 42.6 },
      { watched: false, progress_pct: 0 },
      { watched: false, progress_pct: null },
    ].map((x, i) => ({ video_id: `e${i}`, episode: i + 1, title: `T${i}`, ...x }));
    serve({ episodes: { numbering: "season", seasons: [{ season: 1, episodes: eps }] } });
    renderDetail();
    await screen.findByText("T0");
    const cell = (i: number) => screen.getByRole("link", { name: new RegExp(`T${i}`) });
    expect(cell(0).querySelector(".mkt-episode__watched")).toHaveTextContent("✓");
    expect(within(cell(0)).queryByRole("progressbar")).toBeNull();
    expect(within(cell(1)).getByRole("progressbar", { name: "43% watched" })).toBeInTheDocument();
    for (const i of [2, 3]) {
      expect(within(cell(i)).queryByRole("progressbar")).toBeNull();
      expect(cell(i).querySelector(".mkt-episode__watched")).toBeNull();
    }
  });

  it.todo("SED-17: the watched state is exposed to screen readers (needs an i18n key)");

  it("SED-18: accept-all success toast; double click sends one POST; titles reload", async () => {
    let resolvePost: (v: unknown) => void = () => {};
    post.mockImplementation(() => new Promise((r) => (resolvePost = r)));
    serve();
    renderDetail("/series/s1", { user: adminUser });
    const user = userEvent.setup();
    const btn = await screen.findByRole("button", { name: "Accept all episode matches" });
    await user.click(btn);
    await user.click(btn);
    expect(post).toHaveBeenCalledTimes(1);
    expect(post).toHaveBeenCalledWith("/api/series/s1/enrichment/accept-all");
    expect(btn).toHaveAttribute("aria-busy", "true");
    const before = get.mock.calls.filter((c) => String(c[0]).endsWith("/episodes")).length;
    resolvePost(undefined);
    expect(await screen.findByText("Episode matches applied.")).toBeInTheDocument();
    await waitFor(() =>
      expect(get.mock.calls.filter((c) => String(c[0]).endsWith("/episodes")).length).toBe(
        before + 1
      )
    );
  });

  it("SED-18: accept-all error shows an error toast", async () => {
    post.mockRejectedValue(problem(500, "accept failed"));
    serve();
    renderDetail("/series/s1", { user: adminUser });
    await userEvent
      .setup()
      .click(await screen.findByRole("button", { name: "Accept all episode matches" }));
    expect(await screen.findByText("accept failed")).toBeInTheDocument();
  });

  it.todo("SED-18: accept-all asks for confirmation first");

  it("SED-19: navigating to another series never shows the old name over new episodes", async () => {
    serve();
    let resolveHeader: (v: unknown) => void = () => {};
    serve(
      {
        header: () => new Promise((r) => (resolveHeader = r)),
        episodes: {
          numbering: "season",
          seasons: [
            {
              season: 1,
              episodes: [{ video_id: "o1", episode: 1, title: "Other ep", progress_pct: 0 }],
            },
          ],
        },
      },
      "s2"
    );
    // start on s1 with s2 as the next history entry: Back from s2 is not needed,
    // we open s1 first and then go "back" to s2.
    renderDetail("/series/s1", { history: ["/series/s2"] });
    await screen.findByRole("heading", { level: 1, name: "Breaking Bad" });
    await userEvent.setup().click(screen.getByRole("button", { name: "go back" }));
    expect(await screen.findByText("Other ep")).toBeInTheDocument();
    expect(screen.queryByText("Breaking Bad")).toBeNull();
    expect(screen.queryByText("Pilot")).toBeNull();
    resolveHeader({ id: "s2", name: "Other", numbering: "season" });
    expect(await screen.findByRole("heading", { level: 1, name: "Other" })).toBeInTheDocument();
  });

  it("SED-20: episode click opens video detail; Back returns", async () => {
    serve();
    renderDetail();
    const user = userEvent.setup();
    await user.click(await screen.findByRole("link", { name: /Pilot/ }));
    expect(screen.getByText("video detail page")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "go back" }));
    expect(await screen.findByText("Pilot")).toBeInTheDocument();
    expect(screen.getByRole("heading", { level: 2, name: "Season 2" })).toBeInTheDocument();
  });

  it("SED-22: Arabic UI and Arabic titles", async () => {
    localStorage.setItem("mkt:locale", "ar");
    serve({
      header: { id: "s1", name: "باب الحارة", numbering: "season" },
      episodes: {
        numbering: "season",
        next_episode: { video_id: "a1", title: "x", progress_pct: 0, watched: false },
        seasons: [
          {
            season: 0,
            episodes: [{ video_id: "a0", episode: 1, title: "حلقة خاصة", progress_pct: 0 }],
          },
          {
            season: 2,
            episodes: [{ video_id: "a1", episode: 1, title: "الحلقة الأولى", progress_pct: 0 }],
          },
        ],
      },
      missing: { gaps: [{ season: 2, episode: 2 }] },
    });
    renderDetail();
    const h1 = await screen.findByRole("heading", { level: 1, name: "باب الحارة" });
    expect(h1).toHaveAttribute("dir", "auto");
    await waitFor(() => expect(document.documentElement).toHaveAttribute("dir", "rtl"));
    expect(screen.getByText("الحلقة الأولى")).toHaveAttribute("dir", "auto");
    expect(screen.getByRole("heading", { level: 2, name: "الموسم 2" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { level: 2, name: "حلقات خاصة" })).toBeInTheDocument();
    expect(await screen.findByText("مفقودة")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "▶ متابعة المشاهدة" })).toBeInTheDocument();
  });
});
