// TV Guide cases from web/test-cases/guide.md (GDE-*). api.get is mocked at
// the wrapper layer, so channels.ts (guideFrom / fromHeader / fromBlock) runs
// for real, as in Guide.test.tsx; the page is mounted with the shared page
// harness (RequireAuth + RouteErrorBoundary + a signed-in user).
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, cleanup, fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Route } from "react-router-dom";

const { get, actualApi } = vi.hoisted(() => ({
  get: vi.fn(),
  actualApi: { current: null as null | { get: (p: string) => Promise<unknown> } },
}));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  actualApi.current = actual.api as unknown as { get: (p: string) => Promise<unknown> };
  return { ...actual, api: { ...actual.api, get } };
});

import { Guide } from "./Guide";
import { ApiError } from "../../lib/api";
import en from "../../i18n/en.json";
import { memberUser, quietCrash, renderPage } from "../../test/pageHarness";

let NOW = Date.now();
const iso = (msFromNow: number) => new Date(NOW + msFromNow).toISOString();
const MIN = 60_000;

type WireBlock = Record<string, unknown>;
function header(id: string, number: number, name: unknown, extra: Record<string, unknown> = {}) {
  return { id, number, name, ...extra };
}
function block(
  channel_id: string,
  title: string,
  fromMin: number,
  toMin: number,
  extra: WireBlock = {}
) {
  return {
    channel_id,
    kind: "program",
    title,
    start: iso(fromMin * MIN),
    stop: iso(toMin * MIN),
    ...extra,
  };
}

/** GDE-01 data: "Movies" with "Now Playing" (−10 → +20) and "Later Show" (+20 → +140). */
function basePayload(laterExtra: WireBlock = {}) {
  return {
    server_time: iso(0),
    channels: [
      {
        channel: header("c1", 1, "Movies"),
        blocks: [
          block("c1", "Now Playing", -10, 20),
          block("c1", "Later Show", 20, 140, laterExtra),
        ],
      },
    ],
  };
}

const extraRoutes = (
  <>
    <Route path="/live/:number" element={<p>LIVE PLAYER</p>} />
    <Route path="/admin/channels" element={<p>ADMIN CHANNELS</p>} />
  </>
);

function openGuide(
  opts: { path?: string; user?: typeof memberUser | null; history?: string[] } = {}
) {
  return renderPage(<Guide />, {
    path: opts.path ?? "/guide",
    route: "/guide",
    user: opts.user,
    history: opts.history,
    extraRoutes,
  });
}

const guideCalls = () =>
  get.mock.calls.filter((c) => String(c[0]).startsWith("/api/channels/guide"));
const cellNamed = (name: string) =>
  screen.getAllByTestId("guide-cell").find((c) => c.textContent?.includes(name))!;
/** An element's inset-inline-start, in % of the window (jsdom drops it from `style`). */
const inlineStart = (el: HTMLElement) => {
  const m = /inset-inline-start:\s*(-?[\d.]+)%/.exec(el.getAttribute("style") ?? "");
  if (!m) throw new Error(`no inset-inline-start in % on ${el.outerHTML}`);
  return parseFloat(m[1]);
};

const realMatchMedia = window.matchMedia;

/** No route error fallback (the guide's own ErrorState is also role="alert"). */
function expectNoCrash() {
  expect(screen.queryByText("This page hit an error and could not be shown.")).toBeNull();
}

describe("Guide (test-cases/guide.md)", () => {
  beforeEach(() => {
    NOW = Date.now();
    get.mockReset();
    try {
      localStorage.removeItem("mkt:locale");
    } catch {
      /* storage blocked */
    }
  });
  afterEach(() => {
    vi.useRealTimers();
    window.matchMedia = realMatchMedia;
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  // ── P1 ──────────────────────────────────────────────────────────────

  it("GDE-07: What's on now lists current and next per channel, tapping a card tunes", async () => {
    const payload = basePayload();
    payload.channels.push({
      channel: header("c2", 2, "Kids"),
      blocks: [block("c2", "Tonight", 60, 120)],
    });
    get.mockResolvedValue(payload);
    const user = userEvent.setup();
    openGuide();
    const toggle = await screen.findByRole("button", { name: "What's on now" });
    expect(toggle).toHaveAttribute("aria-pressed", "false");
    await user.click(toggle);

    const list = screen.getByTestId("whats-on-now");
    const cards = within(list).getAllByRole("button");
    expect(cards).toHaveLength(2);
    expect(cards[0]).toHaveTextContent("Now Playing");
    expect(within(cards[0]).getByRole("progressbar", { name: "Duration" })).toBeInTheDocument();
    expect(cards[0]).toHaveTextContent("Next: Later Show");
    expect(cards[1]).toHaveTextContent("No content");
    expect(within(cards[1]).queryByRole("progressbar")).toBeNull();
    expect(cards[1]).toHaveTextContent("Next: Tonight");

    const back = screen.getByRole("button", { name: "Grid view" });
    expect(back).toHaveAttribute("aria-pressed", "true");

    await user.click(cards[0]);
    expect(await screen.findByText("LIVE PLAYER")).toBeInTheDocument();
  });

  it("GDE-07: channel 2's card tunes too (the whole card is the button)", async () => {
    const payload = basePayload();
    payload.channels.push({
      channel: header("c2", 2, "Kids"),
      blocks: [block("c2", "Tonight", 60, 120)],
    });
    get.mockResolvedValue(payload);
    const user = userEvent.setup();
    openGuide();
    await user.click(await screen.findByRole("button", { name: "What's on now" }));
    const cards = within(screen.getByTestId("whats-on-now")).getAllByRole("button");
    await user.click(cards[1]);
    expect(await screen.findByText("LIVE PLAYER")).toBeInTheDocument();
  });

  it.each([
    ["channels null", { server_time: iso(0), channels: null }],
    ["channels missing", { server_time: iso(0) }],
    ["empty object", {}],
  ])("GDE-10: %s shows the No channels yet empty state", async (_label, payload) => {
    get.mockResolvedValue(payload);
    openGuide();
    expect(await screen.findByText("No channels yet")).toBeInTheDocument();
    expect(
      screen.getByText("There are no live channels to show in the guide.")
    ).toBeInTheDocument();
    expectNoCrash();
  });

  it.each([
    ["blocks null", { channel: header("c1", 1, "Movies"), blocks: null }],
    ["blocks missing", { channel: header("c1", 1, "Movies") }],
  ])("GDE-10: %s renders the row with one No content cell", async (_label, entry) => {
    get.mockResolvedValue({ channels: [entry] });
    const user = userEvent.setup();
    openGuide();
    const grid = await screen.findByTestId("guide-grid");
    expect(within(grid).getByText("Movies")).toBeInTheDocument();
    expect(within(grid).getByText("1")).toBeInTheDocument();
    expect(within(grid).queryAllByTestId("guide-cell")).toHaveLength(0);
    const empty = grid.querySelector(".mkt-guide__cell--empty") as HTMLElement;
    expect(empty).toHaveTextContent("No content");
    expect(empty.style.width).toBe("100%"); // the whole two-hour window

    await user.click(screen.getByRole("button", { name: "What's on now" }));
    const card = within(screen.getByTestId("whats-on-now")).getByRole("button");
    expect(card).toHaveTextContent("No content");
    expect(card).not.toHaveTextContent("Next:");
    expectNoCrash();
  });

  it.each([
    ["a null entry", { server_time: iso(0), channels: [null] }],
    ["a null channel header", { channels: [{ channel: null, blocks: [] }] }],
  ])("GDE-11: %s shows the guide error state, not a blank page", async (_label, payload) => {
    get.mockResolvedValue(payload);
    openGuide();
    expect(await screen.findByText("Could not load the guide.")).toBeInTheDocument();
    expect(screen.getByRole("alert")).toBeInTheDocument();
    expectNoCrash(); // no render crash
  });

  // Bug: fromHeader(null) throws inside guideFrom (src/lib/channels.ts), so
  // one bad entry hides every channel. The fix (skip bad entries) belongs in
  // channels.ts guideFrom.
  it.fails("GDE-11: a bad entry is skipped and the other channels still render", async () => {
    const payload = basePayload() as { server_time: string; channels: unknown[] };
    payload.channels.push(null);
    get.mockResolvedValue(payload);
    openGuide();
    expect(await screen.findByTestId("guide-grid")).toBeInTheDocument();
    expect(screen.getByText("Movies")).toBeInTheDocument();
  });

  it.each([
    [500, en["error.kind.server"], "button", en["common.retry"]],
    [401, en["error.kind.signedOut"], "link", en["error.signIn"]],
    [403, en["error.kind.noAccess"], "button", en["error.back"]],
  ] as const)(
    "GDE-13: first load failing with %i shows the error state; a later refresh recovers",
    async (status, desc, role, action) => {
      vi.useFakeTimers({ shouldAdvanceTime: true });
      get.mockRejectedValueOnce(new ApiError({ type: "about:blank", title: "x", status }));
      get.mockResolvedValue(basePayload());
      openGuide();
      expect(await screen.findByText("Could not load the guide.")).toBeInTheDocument();
      const alert = screen.getByRole("alert");
      expect(alert).toHaveTextContent(desc);
      expect(within(alert).getByRole(role, { name: action })).toBeInTheDocument();
      expect(screen.queryByRole("toolbar")).toBeNull();
      expect(screen.queryByTestId("guide-grid")).toBeNull();
      await act(async () => {
        await vi.advanceTimersByTimeAsync(30_000);
      });
      expect(await screen.findByTestId("guide-grid")).toBeInTheDocument();
      expect(screen.queryByText("Could not load the guide.")).toBeNull();
    }
  );

  it("GDE-20: signed out goes to /login with from=/guide and makes no guide request", async () => {
    openGuide({ user: null });
    expect(await screen.findByText("login page (from /guide)")).toBeInTheDocument();
    expect(get).not.toHaveBeenCalled();
  });

  it("GDE-20: a normal (non-admin) user sees the guide", async () => {
    get.mockResolvedValue(basePayload());
    openGuide({ user: memberUser });
    expect(await screen.findByTestId("guide-grid")).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "TV Guide" })).toBeInTheDocument();
    expect(screen.queryByText(/admins only/i)).toBeNull();
  });

  it("GDE-21: a render crash shows the route error fallback; leaving and Back work", async () => {
    const spy = quietCrash();
    const bad = basePayload() as { channels: Array<{ channel: Record<string, unknown> }> };
    bad.channels[0].channel.name = { not: "a string" }; // objects are not valid React children
    get.mockResolvedValue(bad);
    const user = userEvent.setup();
    openGuide();
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "This page hit an error and could not be shown."
    );
    expect(screen.getByRole("navigation", { name: "test shell" })).toBeInTheDocument();

    await user.click(screen.getByRole("link", { name: "go elsewhere" }));
    expect(await screen.findByText("elsewhere")).toBeInTheDocument();
    expectNoCrash();

    get.mockResolvedValue(basePayload());
    await user.click(screen.getByRole("button", { name: "go back" }));
    expect(await screen.findByTestId("guide-grid")).toBeInTheDocument();
    expectNoCrash();
    spy.mockRestore();
  });

  it("GDE-21: Back from a crashed guide returns to the previous page", async () => {
    const spy = quietCrash();
    const bad = basePayload() as { channels: Array<{ channel: Record<string, unknown> }> };
    bad.channels[0].channel.name = { not: "a string" };
    get.mockResolvedValue(bad);
    const user = userEvent.setup();
    openGuide({ history: ["/elsewhere"] });
    expect(await screen.findByRole("alert")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "go back" }));
    expect(await screen.findByText("elsewhere")).toBeInTheDocument();
    expectNoCrash();
    spy.mockRestore();
  });

  // ── P2 ──────────────────────────────────────────────────────────────

  it("GDE-04: a future cell opens a full details dialog; Escape closes it and restores focus", async () => {
    get.mockResolvedValue(
      basePayload({
        desc: "A long story",
        poster: "/p.jpg",
        series: "Seerah",
        season: 1,
        episode: 3,
        sub_title: "Badr",
      })
    );
    const user = userEvent.setup();
    openGuide();
    await screen.findByTestId("guide-grid");
    const later = cellNamed("Later Show");
    await user.click(later);
    const dialog = await screen.findByRole("dialog", { name: "Later Show" });
    expect(dialog).toHaveTextContent("Starts at");
    expect(dialog).toHaveTextContent("Series");
    expect(dialog).toHaveTextContent("Seerah · S1E3 · Badr");
    expect(dialog).toHaveTextContent("Duration");
    expect(dialog).toHaveTextContent("120 min");
    expect(dialog).toHaveTextContent("A long story");
    expect(dialog.querySelector("img")).toHaveAttribute("src", "/p.jpg");
    expect(dialog.querySelector("img")).toHaveAttribute("alt", "");
    expect(within(dialog).getByText("program")).toBeInTheDocument();
    expect(screen.queryByText("LIVE PLAYER")).toBeNull();

    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    await waitFor(() => expect(later).toHaveFocus());
  });

  it("GDE-05: filler runs collapse into one cell titled as the API says (else Up next) that opens details, also when airing", async () => {
    get.mockResolvedValue({
      server_time: iso(0),
      channels: [
        {
          channel: header("c1", 1, "Movies"),
          blocks: [
            block("c1", "Now Playing", -10, 20),
            block("c1", "فواصل", 20, 25, { kind: "filler" }),
            block("c1", "Bumper", 25, 27, { kind: "bumper" }),
            block("c1", "Station", 27, 30, { kind: "station_id" }),
            block("c1", "Feature", 30, 90),
          ],
        },
        {
          channel: header("c2", 2, "Kids"),
          blocks: [block("c2", "", -5, 10, { kind: "filler" })],
        },
      ],
    });
    const user = userEvent.setup();
    openGuide();
    const grid = await screen.findByTestId("guide-grid");
    const rows = grid.querySelectorAll(".mkt-guide__row");
    const row1 = within(rows[0] as HTMLElement).getAllByTestId("guide-cell");
    expect(row1).toHaveLength(3);
    expect(row1[1]).toHaveTextContent("فواصل"); // the API's filler title
    expect(row1[1]).toHaveClass("mkt-guide__cell--filler");
    expect(parseFloat(row1[1].style.width)).toBeCloseTo((10 / 120) * 100, 5); // 10 of 120 min

    await user.click(row1[1]);
    expect(await screen.findByRole("dialog")).toBeInTheDocument();
    expect(screen.queryByText("LIVE PLAYER")).toBeNull();
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());

    const airingFiller = within(rows[1] as HTMLElement).getByTestId("guide-cell");
    expect(airingFiller).toHaveClass("mkt-guide__cell--airing");
    await user.click(airingFiller);
    expect(await screen.findByRole("dialog")).toBeInTheDocument();
    expect(screen.queryByText("LIVE PLAYER")).toBeNull();
  });

  it("GDE-08: the compact view is the default on a phone", async () => {
    window.matchMedia = vi.fn((q: string) => ({
      matches: q === "(max-width: 640px)",
      media: q,
      onchange: null,
      addListener: vi.fn(),
      removeListener: vi.fn(),
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      dispatchEvent: vi.fn(),
    })) as unknown as typeof window.matchMedia;
    get.mockResolvedValue(basePayload());
    openGuide();
    expect(await screen.findByTestId("whats-on-now")).toBeInTheDocument();
    expect(screen.queryByTestId("guide-grid")).toBeNull();
    expect(screen.getByRole("button", { name: "Grid view" })).toHaveAttribute(
      "aria-pressed",
      "true"
    );
  });

  it("GDE-08: without matchMedia the grid renders and nothing throws", async () => {
    (window as unknown as { matchMedia: unknown }).matchMedia = undefined;
    get.mockResolvedValue(basePayload());
    openGuide();
    expect(await screen.findByTestId("guide-grid")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "What's on now" })).toHaveAttribute(
      "aria-pressed",
      "false"
    );
  });

  it("GDE-09: no channels shows the empty state in both modes; the CTA goes to /admin/channels", async () => {
    get.mockResolvedValue({ server_time: iso(0), channels: [] });
    const user = userEvent.setup();
    openGuide();
    expect(await screen.findByText("No channels yet")).toBeInTheDocument();
    const select = screen.getByRole("combobox", { name: "Category" });
    expect(
      within(select)
        .getAllByRole("option")
        .map((o) => o.textContent)
    ).toEqual(["All categories"]);
    await user.click(screen.getByRole("button", { name: "What's on now" }));
    expect(screen.getByText("No channels yet")).toBeInTheDocument();
    expect(
      screen.getByText("There are no live channels to show in the guide.")
    ).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Create a channel" }));
    expect(await screen.findByText("ADMIN CHANNELS")).toBeInTheDocument();
  });

  it("GDE-09: a non-admin does not get the Create a channel CTA", async () => {
    get.mockResolvedValue({ server_time: iso(0), channels: [] });
    openGuide({ user: memberUser });
    expect(await screen.findByText("No channels yet")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Create a channel" })).toBeNull();
  });

  it("GDE-14: a network failure shows the error state", async () => {
    const err = vi.spyOn(console, "error").mockImplementation(() => {});
    get.mockImplementation((p: string) => actualApi.current!.get(p));
    vi.stubGlobal(
      "fetch",
      vi.fn(() => Promise.reject(new TypeError("Failed to fetch")))
    );
    openGuide();
    expect(await screen.findByText("Could not load the guide.")).toBeInTheDocument();
    expectNoCrash();
    // the path is logged without its query
    expect(
      err.mock.calls.some((c) => String(c[0]).includes("GET /api/channels/guide failed"))
    ).toBe(true);
  });

  it("GDE-14: a malformed JSON body shows the error state", async () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    get.mockImplementation((p: string) => actualApi.current!.get(p));
    vi.stubGlobal(
      "fetch",
      vi.fn(() =>
        Promise.resolve(
          new Response('{"channels": [', {
            status: 200,
            headers: { "Content-Type": "application/json" },
          })
        )
      )
    );
    openGuide();
    expect(await screen.findByText("Could not load the guide.")).toBeInTheDocument();
    expectNoCrash();
  });

  it("GDE-15: a failed refresh keeps the last grid", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    get.mockResolvedValueOnce(basePayload());
    get.mockRejectedValue(new ApiError({ type: "about:blank", title: "x", status: 500 }));
    openGuide();
    await screen.findByTestId("guide-grid");
    await act(async () => {
      await vi.advanceTimersByTimeAsync(30_000);
    });
    expect(guideCalls()).toHaveLength(2);
    expect(screen.getByTestId("guide-grid")).toBeInTheDocument();
    expect(screen.getByText("Now Playing")).toBeInTheDocument();
    expect(screen.queryByText("Could not load the guide.")).toBeNull();
  });

  it("GDE-16: a 1 s tick moves airing without a refetch; a refresh every 30 s; none after unmount", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    NOW = Date.now();
    get.mockResolvedValue({
      server_time: iso(0),
      channels: [
        {
          channel: header("c1", 1, "Movies"),
          blocks: [
            {
              channel_id: "c1",
              kind: "program",
              title: "Ending",
              start: iso(-10 * MIN),
              stop: iso(2000),
            },
            block("c1", "After", 0, 60, { start: iso(2000) }),
          ],
        },
      ],
    });
    const err = vi.spyOn(console, "error");
    const { unmount } = openGuide();
    await screen.findByTestId("guide-grid");
    expect(guideCalls()).toHaveLength(1);
    expect(cellNamed("Ending")).toHaveClass("mkt-guide__cell--airing");

    await act(async () => {
      await vi.advanceTimersByTimeAsync(3000);
    });
    expect(cellNamed("Ending")).not.toHaveClass("mkt-guide__cell--airing");
    expect(cellNamed("After")).toHaveClass("mkt-guide__cell--airing");
    expect(guideCalls()).toHaveLength(1);

    const firstUrl = String(guideCalls()[0][0]);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(30_000);
    });
    expect(guideCalls()).toHaveLength(2);
    const secondUrl = String(guideCalls()[1][0]);
    expect(secondUrl).toMatch(/start=.*&end=/);
    expect(secondUrl).not.toBe(firstUrl); // fresh window

    unmount();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(60_000);
    });
    expect(guideCalls()).toHaveLength(2);
    expect(err.mock.calls.some((c) => /unmounted|state update/i.test(String(c[0])))).toBe(false);
  });

  it("GDE-17: the now-line follows server_time, not the device clock", async () => {
    get.mockResolvedValue({
      server_time: iso(60 * MIN),
      channels: [
        {
          channel: header("c1", 1, "Movies"),
          blocks: [block("c1", "A", 0, 40), block("c1", "B", 50, 90)],
        },
      ],
    });
    const user = userEvent.setup();
    openGuide();
    await screen.findByTestId("guide-grid");
    expect(cellNamed("B")).toHaveClass("mkt-guide__cell--airing");
    expect(cellNamed("A")).not.toHaveClass("mkt-guide__cell--airing");
    await user.click(cellNamed("B"));
    expect(await screen.findByText("LIVE PLAYER")).toBeInTheDocument();
  });

  it("GDE-17: an unparseable server_time falls back to the device clock", async () => {
    get.mockResolvedValue({
      server_time: "not-a-date",
      channels: [
        {
          channel: header("c1", 1, "Movies"),
          blocks: [block("c1", "A", 0, 30), block("c1", "B", 50, 90)],
        },
      ],
    });
    openGuide();
    await screen.findByTestId("guide-grid");
    expect(cellNamed("A")).toHaveClass("mkt-guide__cell--airing");
    expect(cellNamed("B")).not.toHaveClass("mkt-guide__cell--airing");
    expectNoCrash();
  });

  // Bug: fromHeader() in src/lib/channels.ts does not copy `category`
  // (WireHeader has no such field), so the select only ever has "All
  // categories". The fix belongs in channels.ts.
  it.fails("GDE-18: the category filter lists the channels' categories and refetches", async () => {
    get.mockResolvedValue({
      server_time: iso(0),
      channels: [
        { channel: header("c1", 1, "Movies", { category: "movies" }), blocks: [] },
        { channel: header("c2", 2, "Kids", { category: "kids" }), blocks: [] },
      ],
    });
    const user = userEvent.setup();
    openGuide();
    const select = await screen.findByRole("combobox", { name: "Category" });
    await waitFor(() =>
      expect(
        within(select)
          .getAllByRole("option")
          .map((o) => o.textContent)
      ).toEqual(["All categories", "kids", "movies"])
    );
    await user.selectOptions(select, "kids");
    await waitFor(() =>
      expect(String(guideCalls().at(-1)?.[0])).toMatch(/start=.*&end=.*&category=kids/)
    );
  });

  it("GDE-18: choosing a category refetches with &category= (option injected)", async () => {
    get.mockResolvedValue(basePayload());
    openGuide();
    const select = (await screen.findByRole("combobox", { name: "Category" })) as HTMLSelectElement;
    // the select has no options today (GDE-18 bug); add one to drive the refetch path
    const opt = document.createElement("option");
    opt.value = "kids";
    opt.textContent = "kids";
    select.appendChild(opt);
    fireEvent.change(select, { target: { value: "kids" } });
    await waitFor(() =>
      expect(String(guideCalls().at(-1)?.[0])).toMatch(/start=.*&end=.*&category=kids$/)
    );
  });

  it("GDE-19: bad timestamps never give a NaN width or a progress bar", async () => {
    get.mockResolvedValue({
      server_time: iso(0),
      channels: [
        {
          channel: header("c1", 1, "Movies"),
          blocks: [
            { channel_id: "c1", kind: "program", title: "Bad", start: "bad", stop: "bad" },
            block("c1", "Backwards", 30, 10),
            block("c1", "Fine", 40, 60),
          ],
        },
      ],
    });
    openGuide();
    await screen.findByTestId("guide-grid");
    for (const c of screen.getAllByTestId("guide-cell")) {
      expect(c.style.width).not.toBe("");
      expect(Number.isNaN(parseFloat(c.style.width))).toBe(false);
      expect(c.querySelector(".mkt-guide__cellprogress")).toBeNull();
    }
  });

  it("GDE-22: Back from the live player returns to the guide with a fresh fetch", async () => {
    get.mockResolvedValue(basePayload());
    const user = userEvent.setup();
    openGuide({ path: "/guide?foo=1" });
    await screen.findByTestId("guide-grid");
    expect(guideCalls()).toHaveLength(1);
    await user.click(cellNamed("Now Playing"));
    expect(await screen.findByText("LIVE PLAYER")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "go back" }));
    expect(await screen.findByTestId("guide-grid")).toBeInTheDocument();
    expect(guideCalls()).toHaveLength(2);
  });

  it("GDE-24: Arabic renders RTL with Arabic strings", async () => {
    localStorage.setItem("mkt:locale", "ar");
    const payload = basePayload();
    payload.channels[0].channel.name = "قناة القرآن";
    payload.channels[0].blocks[0].title = "تلاوة";
    get.mockResolvedValue(payload);
    const user = userEvent.setup();
    openGuide();
    expect(await screen.findByRole("heading", { name: "دليل القنوات" })).toBeInTheDocument();
    await waitFor(() => expect(document.documentElement.dir).toBe("rtl"));
    expect(document.documentElement.lang).toBe("ar");
    expect(screen.getByText("قناة القرآن")).toBeInTheDocument();
    // cells are placed with a logical (direction-aware) property
    expect(cellNamed("تلاوة").getAttribute("style")).toMatch(/inset-inline-start/);
    expect(screen.getByTestId("now-line").getAttribute("style")).toMatch(/inset-inline-start: 25%/);
    await user.click(screen.getByRole("button", { name: "ما يُعرض الآن" }));
    expect(within(screen.getByTestId("whats-on-now")).getByText("تلاوة")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "عرض الشبكة" })).toBeInTheDocument();
  });

  it("GDE-27: programmes are cut at both window edges, nothing spills out", async () => {
    // a frozen clock: the window is exactly now − 30 … now + 90 min
    vi.useFakeTimers({ toFake: ["Date"], now: NOW });
    get.mockResolvedValue({
      server_time: iso(0),
      channels: [
        {
          channel: header("c1", 1, "Movies"),
          blocks: [
            block("c1", "Gone", -100, -40), // over before the window
            block("c1", "Early", -60, 10), // began 30 min before the window
            block("c1", "Middle", 10, 60),
            block("c1", "Late", 60, 150), // runs 60 min past the window
            block("c1", "Far", 100, 130), // starts after the window
          ],
        },
      ],
    });
    openGuide();
    await screen.findByTestId("guide-grid");
    const cells = screen.getAllByTestId("guide-cell");
    expect(cells.map((c) => c.textContent)).toEqual(["Early", "Middle", "Late"]);

    const early = cellNamed("Early");
    expect(inlineStart(early)).toBe(0);
    expect(parseFloat(early.style.width)).toBeCloseTo((40 / 120) * 100, 5);
    expect(early).toHaveClass("mkt-guide__cell--cut-start");
    expect(early).not.toHaveClass("mkt-guide__cell--cut-end");
    expect(within(early).getByTestId("cell-cut-start")).toHaveAttribute("aria-hidden", "true");
    expect(within(early).queryByTestId("cell-cut-end")).toBeNull();
    // the title still reads, and the name is the title alone
    expect(screen.getByRole("button", { name: "Early" })).toBe(early);
    // the progress bar spans the visible part up to now: 30 of its 40 min
    const bar = early.querySelector(".mkt-guide__cellprogress") as HTMLElement;
    expect(parseFloat(bar.style.inlineSize)).toBeCloseTo(75, 5);

    const middle = cellNamed("Middle");
    expect(middle.className).not.toMatch(/cut-/);
    expect(inlineStart(middle)).toBeCloseTo((40 / 120) * 100, 5);

    const late = cellNamed("Late");
    expect(inlineStart(late)).toBeCloseTo(75, 5);
    expect(parseFloat(late.style.width)).toBeCloseTo(25, 5);
    expect(late).toHaveClass("mkt-guide__cell--cut-end");
    expect(late).not.toHaveClass("mkt-guide__cell--cut-start");
    expect(within(late).getByTestId("cell-cut-end")).toHaveAttribute("aria-hidden", "true");
    expect(screen.getByRole("button", { name: "Late" })).toBe(late);

    // every cell sits inside the window: start ≥ 0 %, end ≤ 100 %
    for (const c of cells) {
      expect(inlineStart(c)).toBeGreaterThanOrEqual(0);
      expect(inlineStart(c) + parseFloat(c.style.width)).toBeLessThanOrEqual(100 + 1e-9);
    }
    expect(inlineStart(screen.getByTestId("now-line"))).toBe(25);
  });

  it("GDE-28: the two hours fill the guide's width, with half-hour ticks", async () => {
    // 10:10 local: the window is 09:40 … 11:40
    NOW = new Date(2026, 9, 28, 10, 10).getTime();
    vi.useFakeTimers({ toFake: ["Date"], now: NOW });
    get.mockResolvedValue({
      server_time: iso(0),
      channels: [
        { channel: header("c1", 1, "Movies"), blocks: [block("c1", "Whole", -30, 90)] },
        { channel: header("c2", 2, "Empty"), blocks: [] },
      ],
    });
    openGuide();
    const grid = await screen.findByTestId("guide-grid");
    // no fixed pixel width anywhere: the track and axis take what is there
    for (const el of grid.querySelectorAll<HTMLElement>(
      ".mkt-guide__track, .mkt-guide__timeaxis"
    )) {
      expect(el.style.width).toBe("");
    }
    for (const el of grid.querySelectorAll<HTMLElement>("[style]")) {
      expect(el.getAttribute("style")).not.toMatch(/\d+px/);
    }
    // a programme the length of the window is the full width, uncut
    const whole = cellNamed("Whole");
    expect(inlineStart(whole)).toBe(0);
    expect(parseFloat(whole.style.width)).toBeCloseTo(100, 5);
    expect(whole.className).not.toMatch(/cut-/);
    expect((grid.querySelector(".mkt-guide__cell--empty") as HTMLElement).style.width).toBe("100%");

    // half-hour ticks at 10:00, 10:30, 11:00 and 11:30 (20, 50, 80, 110 min in)
    const ticks = screen.getAllByTestId("guide-tick");
    expect(ticks.map(inlineStart).map((x) => Math.round(x * 1000) / 1000)).toEqual([
      16.667, 41.667, 66.667, 91.667,
    ]);
    expect(ticks.map((t) => t.classList.contains("mkt-guide__tick--half"))).toEqual([
      false,
      true,
      false,
      true,
    ]);
  });

  it("GDE-28: a tick too close to the window's end to show its time is left out", async () => {
    // 10:25 local: the window is 09:55 … 11:55; 11:30 has 25 min, 12:00 is outside
    NOW = new Date(2026, 9, 28, 10, 25).getTime();
    vi.useFakeTimers({ toFake: ["Date"], now: NOW });
    get.mockResolvedValue({
      server_time: iso(0),
      channels: [{ channel: header("c1", 1, "Movies"), blocks: [block("c1", "A", -30, 90)] }],
    });
    openGuide();
    await screen.findByTestId("guide-grid");
    expect(screen.getAllByTestId("guide-tick")).toHaveLength(4); // 10:00 … 11:30

    // 10:04 local: the window ends 11:34, 4 min after the 11:30 tick
    cleanup();
    NOW = new Date(2026, 9, 28, 10, 4).getTime();
    vi.setSystemTime(NOW);
    get.mockResolvedValue({
      server_time: iso(0),
      channels: [{ channel: header("c1", 1, "Movies"), blocks: [block("c1", "A", -30, 90)] }],
    });
    openGuide();
    await screen.findByTestId("guide-grid");
    const xs = screen.getAllByTestId("guide-tick").map(inlineStart);
    expect(xs).toHaveLength(3); // 10:00, 10:30, 11:00 (not 11:30)
    expect(Math.max(...xs)).toBeLessThan(100);
  });

  it("GDE-24: Arabic empty state title", async () => {
    localStorage.setItem("mkt:locale", "ar");
    get.mockResolvedValue({ server_time: iso(0), channels: [] });
    openGuide();
    expect(await screen.findByText("لا توجد قنوات بعد")).toBeInTheDocument();
  });

  it("GDE-26: keyboard: named toolbar, select and toggle; Enter tunes; dialog closes on Escape", async () => {
    get.mockResolvedValue(basePayload());
    const user = userEvent.setup();
    openGuide();
    await screen.findByTestId("guide-grid");
    expect(screen.getByRole("toolbar", { name: "Guide controls" })).toBeInTheDocument();
    expect(screen.getByRole("combobox", { name: "Category" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "What's on now" })).toHaveAttribute("aria-pressed");
    for (const c of screen.getAllByTestId("guide-cell")) expect(c.tagName).toBe("BUTTON");
    expect(screen.getByRole("button", { name: "Now Playing" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Later Show" })).toBeInTheDocument();
    expect(screen.getByTestId("now-line")).toHaveAttribute("aria-hidden", "true");
    document
      .querySelectorAll(".mkt-guide__nowline, .mkt-guide__cellprogress")
      .forEach((el) => expect(el).toHaveAttribute("aria-hidden", "true"));

    // tab order: select → toggle → the row's channel (it tunes) → its cells
    await user.tab();
    while (document.activeElement !== screen.getByRole("combobox", { name: "Category" })) {
      await user.tab();
    }
    await user.tab();
    expect(screen.getByRole("button", { name: "What's on now" })).toHaveFocus();
    await user.tab();
    expect(screen.getAllByRole("link", { name: /^Watch channel / })[0]).toHaveFocus();
    await user.tab();
    expect(cellNamed("Now Playing")).toHaveFocus();
    await user.tab();
    const later = cellNamed("Later Show");
    expect(later).toHaveFocus();

    await user.keyboard("{Enter}");
    const dialog = await screen.findByRole("dialog", { name: "Later Show" });
    await waitFor(() => expect(dialog.contains(document.activeElement)).toBe(true));
    await user.tab();
    expect(dialog.contains(document.activeElement)).toBe(true);
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    await waitFor(() => expect(later).toHaveFocus());

    await user.click(cellNamed("Now Playing"));
    expect(await screen.findByText("LIVE PLAYER")).toBeInTheDocument();
  });

  it("GDE-26: Enter on the airing cell tunes", async () => {
    get.mockResolvedValue(basePayload());
    const user = userEvent.setup();
    openGuide();
    await screen.findByTestId("guide-grid");
    cellNamed("Now Playing").focus();
    await user.keyboard("{Enter}");
    expect(await screen.findByText("LIVE PLAYER")).toBeInTheDocument();
  });
});
