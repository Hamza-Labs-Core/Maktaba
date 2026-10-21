// TV Guide cases from web/test-cases/guide.md (GDE-*). api.get is mocked at
// the wrapper layer, so channels.ts (guideFrom / fromHeader / fromBlock) runs
// for real, as in Guide.test.tsx; the page is mounted with the shared page
// harness (RequireAuth + RouteErrorBoundary + a signed-in user).
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
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
    expect(empty.style.width).toBe("840px"); // full width: 210 min × 4 px

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
    expect(screen.getByText("Something went wrong.")).toBeInTheDocument();
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

  it.each([500, 401, 403])(
    "GDE-13: first load failing with %i shows the error state; a later refresh recovers",
    async (status) => {
      vi.useFakeTimers({ shouldAdvanceTime: true });
      get.mockRejectedValueOnce(new ApiError({ type: "about:blank", title: "x", status }));
      get.mockResolvedValue(basePayload());
      openGuide();
      expect(await screen.findByText("Could not load the guide.")).toBeInTheDocument();
      expect(screen.getByText("Something went wrong.")).toBeInTheDocument();
      expect(screen.queryByRole("toolbar")).toBeNull();
      expect(screen.queryByTestId("guide-grid")).toBeNull();
      expect(screen.queryByRole("button", { name: /try again|retry/i })).toBeNull();
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
    expect(parseFloat(row1[1].style.width)).toBe(40);

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
    expect(err.mock.calls.some((c) => String(c[0]).includes("GET /api/channels/guide?"))).toBe(
      true
    );
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
          blocks: [block("c1", "A", 0, 30), block("c1", "B", 50, 90)],
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
    expect(screen.getByTestId("now-line").getAttribute("style")).toMatch(
      /inset-inline-start: 120px/
    );
    await user.click(screen.getByRole("button", { name: "ما يُعرض الآن" }));
    expect(within(screen.getByTestId("whats-on-now")).getByText("تلاوة")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "عرض الشبكة" })).toBeInTheDocument();
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

    // tab order: select → toggle → cells
    await user.tab();
    while (document.activeElement !== screen.getByRole("combobox", { name: "Category" })) {
      await user.tab();
    }
    await user.tab();
    expect(screen.getByRole("button", { name: "What's on now" })).toHaveFocus();
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
