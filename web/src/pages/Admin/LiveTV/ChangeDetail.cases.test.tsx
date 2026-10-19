// The change detail cases from web/test-cases/admin-live-tv-change-detail.md (LCD-*):
// unknown ids, load errors, null and missing data in the change and its sub-endpoints,
// the action flows and their errors (stage / verify, schedule, swap now, cancel, roll
// back), the preview's errors and bad expiry, polling, permissions, crash recovery,
// navigation, Arabic and keyboard basics. Rendered through the shared page harness.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Link, Route } from "react-router-dom";

const { get, post, patch } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), patch: vi.fn() }));
vi.mock("../../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../../lib/api")>("../../../lib/api");
  return { ...actual, api: { ...actual.api, get, post, patch } };
});

// the shared player: jsdom cannot play HLS; record what each switcher was asked to play
const { tuned } = vi.hoisted(() => ({ tuned: [] as Array<{ path: string; url: string }> }));
vi.mock("@maktaba/tvplayer", async () => {
  const React = await import("react");
  return {
    useTvSwitcher: (o: {
      channel: { id: string } | null;
      pathKey?: string;
      onSettled?: (c: unknown, from: unknown) => void;
      resolve: (c: unknown, ctx: unknown) => Promise<{ url: string }>;
    }) => {
      const [current, setCurrent] = React.useState<unknown>(null);
      const tune = React.useCallback((c: { id: string }) => {
        void o.resolve(c, { attempt: 0, signal: new AbortController().signal }).then((s) => {
          tuned.push({ path: o.pathKey ?? "", url: s.url });
          setCurrent(c);
          o.onSettled?.(c, null);
        });
      }, []);
      const id = o.channel?.id;
      React.useEffect(() => {
        if (o.channel) tune(o.channel);
      }, [id]);
      return {
        state: { current, target: null, phase: current ? "playing" : "idle" },
        videoRef: [() => {}, () => {}],
        activeIndex: 0,
        gestureRef: () => {},
        tune,
        onKeyDown: () => {},
      };
    },
  };
});

import { ApiError, type Problem } from "../../../lib/api";
import { memberUser, quietCrash, renderPage } from "../../../test/pageHarness";
import type { Preview, TvChangeDetail } from "../../../lib/tvChanges";
import { AdminTvChangeDetail } from "./ChangeDetail";
import { PreviewPanel } from "./PreviewPanel";
import {
  CH_HADITH,
  CH_QURAN,
  answer,
  draftChange,
  liveChange,
  readyChange,
} from "./changeFixtures";

const ROUTE = "/admin/live-tv/changes/:id";
const at = (id: string) => `/admin/live-tv/changes/${id}`;

const preview: Preview = {
  hls_base: "/stream/staging/channel/",
  token: "c.1.x",
  m3u: "http://mac:8081/staging/lineup.m3u?pt=c.1.x",
  xmltv: "http://mac:8081/staging/xmltv.xml?pt=c.1.x",
  expires_at: new Date(Date.now() + 3600_000).toISOString(),
  channels: [
    {
      id: CH_QURAN,
      number: 101,
      name: "Quran",
      url: `/stream/staging/channel/${CH_QURAN}/index.m3u8?pt=c.1.x`,
      live_url: `/stream/channel/${CH_QURAN}/index.m3u8`,
    },
    {
      id: CH_HADITH,
      number: 115,
      name: "Hadith",
      changed: true,
      url: `/stream/staging/channel/${CH_HADITH}/index.m3u8?pt=c.1.x`,
      live_url: `/stream/channel/${CH_HADITH}/index.m3u8`,
    },
  ],
};

type Answer = (url: string) => unknown;
/** Routes the mocked GETs; `over` replaces the answer of a path (without the query). */
function routeGets(over: Record<string, Answer> = {}) {
  get.mockImplementation(async (url: string) => {
    const p = url.split("?")[0]!;
    if (over[p]) return over[p](url);
    return answer(url);
  });
}
/** The change detail GET answers `c` (other paths: the fixtures). */
function serve(c: unknown, over: Record<string, Answer> = {}) {
  const id = (c as { id?: string } | null)?.id ?? readyChange.id;
  routeGets({ [`/api/tv/changes/${id}`]: () => c, ...over });
}

function apiError(status: number, title: string, extra: Record<string, unknown> = {}) {
  return new ApiError({ type: "about:blank", title, status, ...extra } as Problem);
}

const extraRoutes = (
  <>
    <Route path="/admin/live-tv/changes" element={<p>changes list page</p>} />
    <Route path="/queue/:stage" element={<p>queue page</p>} />
  </>
);

function open(id: string = readyChange.id, opts: Partial<Parameters<typeof renderPage>[1]> = {}) {
  return renderPage(<AdminTvChangeDetail />, { path: at(id), route: ROUTE, extraRoutes, ...opts });
}

const detailCalls = (id: string) =>
  get.mock.calls.filter((c) => String(c[0]) === `/api/tv/changes/${id}`).length;
const noCrash = () => expect(screen.queryByText("Something went wrong.")).not.toBeInTheDocument();
const chip = () => document.querySelector<HTMLElement>("[data-testid=change-detail] [data-state]");

let visibility: DocumentVisibilityState = "visible";

beforeEach(() => {
  tuned.length = 0;
  get.mockReset();
  post.mockReset();
  patch.mockReset();
  routeGets();
  post.mockImplementation(async (path: string) => {
    if (path.endsWith("/preview")) return preview;
    throw new Error(`unexpected POST ${path}`);
  });
  visibility = "visible";
  Object.defineProperty(document, "visibilityState", {
    configurable: true,
    get: () => visibility,
  });
});
afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
  window.localStorage.removeItem("mkt:locale");
});

describe("Change detail (test-cases/admin-live-tv-change-detail.md)", () => {
  it("LCD-03: Schedules paging and a channel that fails to load", async () => {
    const page = (n: number) => ({
      channels: [{ id: `ch-${n}`, number: n, name: `Channel ${n}`, reason: "regenerated" }],
      page: n,
      pages: 2,
      total: 11,
    });
    routeGets({
      [`/api/tv/changes/${readyChange.id}/schedule-diff`]: (u) => {
        if (u.includes("channel=")) return Promise.reject(new Error("nope"));
        return page(u.includes("page=2") ? 2 : 1);
      },
    });
    open();
    await screen.findByTestId("lineup-diff");
    const user = userEvent.setup();
    await user.click(screen.getByRole("tab", { name: "Schedules" }));
    await screen.findByText("Channel 1");
    const pager = screen.getByRole("navigation", { name: "Schedule pages" });
    const [prev, next] = within(pager).getAllByRole("button");
    expect(prev).toBeDisabled();
    await user.click(next!);
    expect(await screen.findByText("Channel 2")).toBeInTheDocument();
    expect(get).toHaveBeenCalledWith(expect.stringContaining("schedule-diff?page=2"));
    expect(next).toBeDisabled();
    await user.click(screen.getByRole("button", { name: /Channel 2/ }));
    expect(await screen.findByText("Could not load this schedule.")).toBeInTheDocument();
  });

  it("LCD-03: the schedule list itself failing shows its error", async () => {
    routeGets({
      [`/api/tv/changes/${readyChange.id}/schedule-diff`]: () =>
        Promise.reject(apiError(500, "Internal Server Error")),
    });
    open();
    await screen.findByTestId("lineup-diff");
    await userEvent.setup().click(screen.getByRole("tab", { name: "Schedules" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Internal Server Error");
  });

  it("LCD-06: Checks tab: reasons, error, slot line", async () => {
    serve({
      ...readyChange,
      verify_report: {
        ...readyChange.verify_report,
        runtime: "slots",
        slot: "green",
        status: "warm",
        reasons: ["the staging slot did not start"],
        error: "probe timed out",
      },
    });
    open();
    await screen.findByTestId("change-detail");
    await userEvent.setup().click(screen.getByRole("tab", { name: "Checks" }));
    expect(await screen.findByTestId("checks-reasons")).toHaveTextContent(
      "the staging slot did not start"
    );
    expect(screen.getByTestId("checks-slot")).toHaveTextContent("green");
    expect(screen.getByText(/probe timed out/)).toBeInTheDocument();
    const rows = screen.getAllByRole("row").slice(1);
    expect(rows.map((r) => r.dataset.result)).toEqual(["warn", "pass", "pass", "pass", "skip"]);
  });

  it("LCD-08: Stage disables the actions while busy, then says Staging and shows Staged; 204 waits for the poll", async () => {
    serve(draftChange);
    open(draftChange.id);
    expect(await screen.findByTestId("pending-summary")).toHaveTextContent("101 Quran recitations");
    let resolve!: (v: unknown) => void;
    post.mockImplementationOnce(() => new Promise((r) => (resolve = r)));
    const user = userEvent.setup();
    await user.click(screen.getByTestId("action-stage"));
    for (const b of screen.getAllByTestId(/^action-[a-z]+$/)) expect(b).toBeDisabled();
    await act(async () =>
      resolve({ ...draftChange, state: "staged", allowed_actions: ["cancel"] })
    );
    expect(await screen.findByRole("status")).toHaveTextContent(
      "Staging: the release and the new schedules are being built."
    );
    expect(chip()).toHaveTextContent("Staged");
  });

  it("LCD-08: Stage answering 204 says Staging; a draft without a pending summary says when the diff comes", async () => {
    serve({ ...draftChange, pending_summary: undefined });
    open(draftChange.id);
    expect(
      await screen.findByText("The diff is computed when the change is staged.")
    ).toBeInTheDocument();
    post.mockResolvedValueOnce(undefined);
    await userEvent.setup().click(screen.getByTestId("action-stage"));
    expect(await screen.findByRole("status")).toHaveTextContent("Staging");
    expect(chip()).toHaveTextContent("Draft");
  });

  it("LCD-09: an unknown id shows the error with 'All changes' and is not polled again", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    routeGets({
      "/api/tv/changes/does-not-exist": () => Promise.reject(apiError(404, "Not Found")),
    });
    open("does-not-exist");
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Could not load the changes: Not Found"
    );
    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("TV changes");
    await act(async () => {
      await vi.advanceTimersByTimeAsync(6000);
    });
    expect(detailCalls("does-not-exist")).toBe(1);
    await userEvent
      .setup({ advanceTimers: vi.advanceTimersByTime })
      .click(screen.getByRole("link", { name: "All changes" }));
    expect(screen.getByText("changes list page")).toBeInTheDocument();
  });

  it("LCD-09: the id is URL-encoded in the request", async () => {
    routeGets({ "/api/tv/changes/a%2Fb": () => Promise.reject(apiError(404, "Not Found")) });
    open("a%2Fb");
    await screen.findByRole("alert");
    expect(get).toHaveBeenCalledWith("/api/tv/changes/a%2Fb");
  });

  describe("LCD-10: load errors", () => {
    it.each([
      ["500", apiError(500, "Internal Server Error"), "Internal Server Error"],
      ["403", apiError(403, "Forbidden"), "Forbidden"],
      ["network", new TypeError("Failed to fetch"), "Failed to fetch"],
      ["malformed JSON", new SyntaxError("Unexpected token <"), "Unexpected token"],
    ])(
      "first load %s: the error section, then the change once a poll succeeds",
      async (_n, err, text) => {
        vi.useFakeTimers({ shouldAdvanceTime: true });
        let fail = true;
        serve(readyChange);
        const ok = get.getMockImplementation()!;
        get.mockImplementation(async (u: string) =>
          fail && u === at(readyChange.id).replace("/admin/live-tv", "/api/tv")
            ? Promise.reject(err)
            : ok(u)
        );
        open();
        expect(await screen.findByRole("alert")).toHaveTextContent(
          `Could not load the changes: ${text}`
        );
        fail = false;
        await act(async () => {
          await vi.advanceTimersByTimeAsync(2100);
        });
        expect(await screen.findByTestId("change-detail")).toBeInTheDocument();
        expect(screen.queryByRole("alert")).not.toBeInTheDocument();
      }
    );

    it("after a good load: the change stays and the error shows until the next good read", async () => {
      vi.useFakeTimers({ shouldAdvanceTime: true });
      open();
      await screen.findByTestId("change-detail");
      serve(null, {
        [`/api/tv/changes/${readyChange.id}`]: () => Promise.reject(new Error("boom")),
      });
      await act(async () => {
        await vi.advanceTimersByTimeAsync(2100);
      });
      expect(screen.getByTestId("change-detail")).toBeInTheDocument();
      expect(screen.getByTestId("load-error")).toHaveTextContent(
        "Could not load the changes: boom"
      );
      routeGets();
      await act(async () => {
        await vi.advanceTimersByTimeAsync(2100);
      });
      expect(screen.queryByTestId("load-error")).not.toBeInTheDocument();
    });
  });

  describe("LCD-11: change top-level arrays null or missing", () => {
    const user = () => userEvent.setup();
    const tab = async (name: string) => user().click(screen.getByRole("tab", { name }));
    const runs: Array<[string, Record<string, unknown>, () => Promise<void>]> = [
      [
        "allowed_actions null",
        { allowed_actions: null },
        async () => expect(screen.getByText("Nothing to do in this state.")).toBeInTheDocument(),
      ],
      [
        "allowed_actions missing",
        { allowed_actions: undefined },
        async () => expect(screen.getByText("Nothing to do in this state.")).toBeInTheDocument(),
      ],
      [
        "kinds null",
        { kinds: null },
        async () =>
          expect(
            screen.getByText("Kinds", { selector: "dt" }).nextElementSibling
          ).toHaveTextContent("—"),
      ],
      [
        "approved_by null",
        { approved_by: null },
        async () =>
          expect(
            screen.getByText(/^Approved by/, { selector: "dt" }).nextElementSibling
          ).toHaveTextContent("—"),
      ],
      [
        "approved_by missing",
        { approved_by: undefined },
        async () =>
          expect(
            screen.getByText(/^Approved by/, { selector: "dt" }).nextElementSibling
          ).toHaveTextContent("—"),
      ],
      [
        "audit null",
        { audit: null },
        async () => {
          await tab("History");
          expect(screen.getByText("No audit rows.")).toBeInTheDocument();
        },
      ],
      [
        "audit missing",
        { audit: undefined },
        async () => {
          await tab("History");
          expect(screen.getByText("No audit rows.")).toBeInTheDocument();
        },
      ],
      [
        "events null",
        { events: null },
        async () => {
          await tab("History");
          expect(within(screen.getByTestId("history")).queryAllByRole("listitem")).toHaveLength(0);
        },
      ],
      [
        "users null",
        { users: null },
        async () => expect(screen.getByText(/^aaaaaaaa · /)).toBeInTheDocument(),
      ],
      [
        "verify_report null",
        { verify_report: null },
        async () => {
          await tab("Checks");
          expect(screen.getByText("The checks run once the change is staged.")).toBeInTheDocument();
        },
      ],
      [
        "verify_report.checks and reasons null",
        { verify_report: { checks: null, reasons: null } },
        async () => {
          await tab("Checks");
          expect(screen.getByText("The checks run once the change is staged.")).toBeInTheDocument();
        },
      ],
      [
        "source null",
        { source: null },
        async () => expect(screen.getByText("Admin")).toBeInTheDocument(),
      ],
    ];
    it.each(runs)("%s: no crash", async (_n, over, check) => {
      serve({ ...readyChange, ...over });
      open();
      await screen.findByTestId("change-detail");
      await check();
      noCrash();
    });
  });

  describe("LCD-12: diff and pending_summary arrays null or missing", () => {
    const lineup = readyChange.diff.lineup!;
    const tab = (name: string) => userEvent.setup().click(screen.getByRole("tab", { name }));

    it("diff null: lineup not yet, no art change, no settings change", async () => {
      for (const diff of [null, undefined]) {
        serve({ ...readyChange, diff });
        const { unmount } = open();
        expect(
          await screen.findByText("The diff is computed when the change is staged.")
        ).toBeInTheDocument();
        await tab("Binary & art");
        expect(screen.getByText("No art change.")).toBeInTheDocument();
        await tab("Settings");
        expect(await screen.findByText("No settings change.")).toBeInTheDocument();
        noCrash();
        unmount();
      }
    });

    it.each(["added", "removed", "renumbered", "renamed", "changed"])(
      "lineup.%s null or missing shows None. for that group",
      async (group) => {
        for (const v of [null, undefined]) {
          serve({
            ...readyChange,
            diff: { ...readyChange.diff, lineup: { ...lineup, [group]: v } },
          });
          const { unmount } = open();
          const diff = await screen.findByTestId("lineup-diff");
          expect(within(diff).getByText("None.")).toBeInTheDocument();
          unmount();
        }
      }
    );

    it("a changed channel without fields shows its name only", async () => {
      serve({
        ...readyChange,
        diff: {
          ...readyChange.diff,
          lineup: { ...lineup, changed: [{ ...lineup.changed[0]!, fields: null }] },
        },
      });
      open();
      expect(await screen.findByTestId("lineup-diff")).toHaveTextContent("101 Quran recitations:");
    });

    it("schedules null, art null, art what null, settings tv_output null: no crash", async () => {
      serve({
        ...readyChange,
        diff: {
          ...readyChange.diff,
          schedules: null,
          art: [{ ...readyChange.diff.art![0]!, what: null }],
          settings: { tv_output: null },
        },
      });
      open();
      await screen.findByTestId("lineup-diff");
      await tab("Binary & art");
      const art = screen.getByTestId("art-diff");
      expect(within(art).getByRole("heading", { name: "115 Hadith" })).toBeInTheDocument();
      expect(within(art).queryAllByRole("img")).toHaveLength(0);
      await tab("Settings");
      await screen.findByTestId("settings-diff");
      noCrash();
    });

    it("art null shows no art change", async () => {
      serve({ ...readyChange, diff: { ...readyChange.diff, art: null } });
      open();
      await screen.findByTestId("lineup-diff");
      await tab("Binary & art");
      expect(screen.getByText("No art change.")).toBeInTheDocument();
    });

    it("a draft whose pending channels / settings are null or missing", async () => {
      for (const ps of [
        { channels: null, settings: null },
        {},
        { channels: null, settings: ["tv_output"] },
      ]) {
        serve({ ...draftChange, pending_summary: ps });
        const { unmount } = open(draftChange.id);
        const p = await screen.findByTestId("pending-summary");
        expect(within(p).getAllByText("None.").length).toBeGreaterThan(0);
        noCrash();
        unmount();
      }
    });
  });

  describe("LCD-13: sub-endpoint arrays null or missing", () => {
    const sched = `/api/tv/changes/${readyChange.id}/schedule-diff`;
    const user = () => userEvent.setup();

    it("schedule page channels null / missing → an empty list", async () => {
      for (const body of [
        { channels: null, page: 1, pages: 1, total: 3 },
        { page: 1, pages: 1, total: 3 },
      ]) {
        routeGets({ [sched]: () => body });
        const { unmount } = open();
        await screen.findByTestId("lineup-diff");
        await user().click(screen.getByRole("tab", { name: "Schedules" }));
        const d = await screen.findByTestId("schedule-diff");
        expect(within(d).queryAllByRole("button")).toHaveLength(0);
        noCrash();
        unmount();
      }
    });

    it("a channel's before / after null → 'Nothing planned.' in both columns", async () => {
      routeGets({
        [sched]: (u) =>
          u.includes("channel=")
            ? { ...(answer(u) as object), before: null, after: null, unchanged: false }
            : answer(u),
      });
      open();
      await screen.findByTestId("lineup-diff");
      await user().click(screen.getByRole("tab", { name: "Schedules" }));
      const d = await screen.findByTestId("schedule-diff");
      await user().click(within(d).getByRole("button", { name: /Quran recitations/ }));
      await waitFor(() => expect(screen.getAllByText("Nothing planned.")).toHaveLength(2));
    });

    it("boundaries channels null / missing → the no-switches text", async () => {
      for (const body of [
        { ...(answer(`/api/tv/changes/${readyChange.id}/boundaries`) as object), channels: null },
        {},
      ]) {
        routeGets({ [`/api/tv/changes/${readyChange.id}/boundaries`]: () => body });
        const { unmount } = open();
        await screen.findByTestId("change-detail");
        await user().click(screen.getByTestId("action-approve"));
        const b = await screen.findByTestId("boundaries");
        await waitFor(() => expect(b).toHaveTextContent("No channel changes its schedule"));
        unmount();
      }
    });

    it("preview channels null / missing → no players, no crash; guide after null → empty", async () => {
      for (const ch of [null, undefined]) {
        post.mockImplementation(async () => ({ ...preview, channels: ch }));
        const { unmount } = open();
        await screen.findByTestId("change-detail");
        await user().click(screen.getByRole("tab", { name: "Preview" }));
        await screen.findByTestId("preview-m3u");
        expect(screen.queryAllByTestId(/^preview-ch-/)).toHaveLength(0);
        noCrash();
        unmount();
      }
      post.mockImplementation(async () => preview);
      routeGets({
        [sched]: (u) => ({ ...(answer(u) as object), after: null, unchanged: false }),
      });
      open();
      await screen.findByTestId("change-detail");
      await user().click(screen.getByRole("tab", { name: "Preview" }));
      await screen.findByTestId("preview-guide");
      await waitFor(() => expect(get).toHaveBeenCalledWith(expect.stringContaining("channel=")));
      noCrash();
    });

    it("the list without config → the dialog uses the change's own swap wait", async () => {
      routeGets({ "/api/tv/changes": () => ({}) });
      serve({ ...readyChange, swap_wait: "immediate" }, { "/api/tv/changes": () => ({}) });
      open();
      await screen.findByTestId("change-detail");
      await user().click(screen.getByTestId("action-approve"));
      expect(
        await screen.findByRole("radio", { name: "every channel switches at once" })
      ).toBeChecked();
    });

    it("rollback {} → the button stays usable, no crash", async () => {
      routeGets({ "/api/tv/rollback": () => ({}) });
      open(liveChange.id);
      expect(await screen.findByTestId("rollback-mode")).toBeInTheDocument();
      expect(screen.getByTestId("action-rollback")).toBeEnabled();
      noCrash();
    });
  });

  describe("LCD-14: Re-verify success and refusals", () => {
    it.each([
      ["success", null, "status", "The checks run again."],
      [
        "409 state",
        apiError(409, "Conflict", { state: "swapping" }),
        "alert",
        "Not possible now: the change is Swapping.",
      ],
      [
        "409 in flight",
        apiError(409, "Conflict", { error: "change_in_flight" }),
        "alert",
        "Another change is in flight; this one waits for its turn.",
      ],
      ["501", apiError(501, "Not Implemented"), "alert", "Not available yet."],
      ["network", new TypeError("Failed to fetch"), "alert", "Failed to fetch"],
    ] as const)("%s", async (_n, err, role, text) => {
      open();
      await screen.findByTestId("change-detail");
      let settle!: () => void;
      post.mockImplementationOnce(
        () =>
          new Promise((res, rej) => {
            settle = () => (err ? rej(err) : res(readyChange));
          })
      );
      await userEvent.setup().click(screen.getByTestId("action-verify"));
      expect(post).toHaveBeenCalledWith(`/api/tv/changes/${readyChange.id}/verify`);
      for (const b of screen.getAllByTestId(/^action-[a-z]+$/)) expect(b).toBeDisabled();
      await act(async () => settle());
      expect(await screen.findByRole(role)).toHaveTextContent(text);
      for (const b of screen.getAllByTestId(/^action-[a-z]+$/)) expect(b).toBeEnabled();
    });
  });

  it("LCD-18: a past or empty time is blocked; a valid one saves with the chosen mode", async () => {
    open();
    await screen.findByTestId("change-detail");
    const user = userEvent.setup();
    await user.click(screen.getByTestId("action-schedule"));
    await user.click(await screen.findByRole("radio", { name: "At a set time" }));
    const input = screen.getByLabelText("Swap at");
    await user.clear(input);
    await user.type(input, "2020-01-01T00:00");
    expect(screen.getByText("Choose a time in the future.")).toBeInTheDocument();
    expect(input).toHaveAttribute("aria-invalid", "true");
    expect(screen.getByTestId("dialog-confirm")).toBeDisabled();
    await user.clear(input);
    expect(screen.getByText("Choose a time in the future.")).toBeInTheDocument();
    expect(screen.getByTestId("dialog-confirm")).toBeDisabled();
    await user.type(input, "2030-01-01T03:30");
    expect(input).toHaveAttribute("aria-invalid", "false");
    await user.click(screen.getByRole("radio", { name: "every channel switches at once" }));
    await waitFor(() =>
      expect(get).toHaveBeenCalledWith(
        expect.stringMatching(/\/boundaries\?at=2030-01-01T.*&swap_wait=immediate$/)
      )
    );
    patch.mockResolvedValueOnce({ ...readyChange, swap_at: "2030-01-01T03:30:00Z" });
    await user.click(screen.getByTestId("dialog-confirm"));
    expect(patch).toHaveBeenCalledWith(`/api/tv/changes/${readyChange.id}`, {
      swap_at: expect.stringMatching(/^2030-01-01T/),
      swap_wait: "immediate",
    });
    expect(await screen.findByRole("status")).toHaveTextContent("Swap time saved.");
  });

  describe("LCD-19: Swap now (promote)", () => {
    const approved: TvChangeDetail = {
      ...readyChange,
      state: "approved",
      swap_wait: "boundary",
      allowed_actions: ["promote", "schedule", "cancel"],
    };

    it("the dialog has no 'When', ignores the backdrop, closes on Escape; a new mode is saved first, then promoted", async () => {
      serve(approved);
      open();
      await screen.findByTestId("change-detail");
      const user = userEvent.setup();
      await user.click(screen.getByTestId("action-promote"));
      const dialog = await screen.findByRole("dialog", { name: "Swap change #42 now" });
      expect(within(dialog).queryByRole("group", { name: "When" })).toBeNull();
      expect(within(dialog).getByTestId("dialog-confirm")).toHaveTextContent("Swap now");
      // the backdrop does not close it
      await user.pointer({ keys: "[MouseLeft]", target: dialog.parentElement! });
      expect(screen.getByRole("dialog")).toBeInTheDocument();
      // Escape still does
      await user.keyboard("{Escape}");
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument();

      await user.click(screen.getByTestId("action-promote"));
      await user.click(
        await screen.findByRole("radio", { name: "every channel switches at once" })
      );
      patch.mockResolvedValueOnce({ ...approved, swap_wait: "immediate" });
      post.mockResolvedValueOnce({ ...approved, state: "swapping", allowed_actions: [] });
      await user.click(screen.getByTestId("dialog-confirm"));
      expect(patch).toHaveBeenCalledWith(`/api/tv/changes/${readyChange.id}`, {
        swap_at: null,
        swap_wait: "immediate",
      });
      expect(post).toHaveBeenCalledWith(`/api/tv/changes/${readyChange.id}/promote`);
      expect(patch.mock.invocationCallOrder[0]!).toBeLessThan(post.mock.invocationCallOrder[0]!);
      expect(await screen.findByRole("status")).toHaveTextContent(
        "Swapping: the channels switch now."
      );
      expect(chip()).toHaveTextContent("Swapping");
    });

    it("the same mode promotes straight away", async () => {
      serve(approved);
      open();
      await screen.findByTestId("change-detail");
      const user = userEvent.setup();
      await user.click(screen.getByTestId("action-promote"));
      post.mockResolvedValueOnce({ ...approved, state: "swapping", allowed_actions: [] });
      await user.click(await screen.findByTestId("dialog-confirm"));
      expect(patch).not.toHaveBeenCalled();
      expect(post).toHaveBeenCalledWith(`/api/tv/changes/${readyChange.id}/promote`);
    });
  });

  describe("LCD-20: approve / promote conflicts and 501", () => {
    it.each([
      [
        { error: "second_admin_required" },
        409,
        "A second admin approves this change: you made it.",
      ],
      [
        { error: "ack_param_change_required" },
        409,
        "This change changes the picture of a pushed channel: acknowledge it first.",
      ],
      [
        { error: "change_in_flight" },
        409,
        "Another change is in flight; this one waits for its turn.",
      ],
      [{ state: "cancelled" }, 409, "Not possible now: the change is Cancelled."],
      [{}, 409, "Conflict"],
      [{}, 501, "Not available yet."],
    ])("approve %j %i: the dialog stays with the reason", async (extra, status, text) => {
      open();
      await screen.findByTestId("change-detail");
      const user = userEvent.setup();
      await user.click(screen.getByTestId("action-approve"));
      post.mockRejectedValueOnce(
        apiError(status, status === 501 ? "Not Implemented" : "Conflict", extra)
      );
      await user.click(await screen.findByTestId("dialog-confirm"));
      const dialog = screen.getByRole("dialog");
      expect(await within(dialog).findByRole("alert")).toHaveTextContent(text);
      expect(within(dialog).getByTestId("dialog-confirm")).toBeEnabled();
    });

    it("promote 409 live", async () => {
      serve({ ...readyChange, state: "approved", allowed_actions: ["promote"] });
      open();
      await screen.findByTestId("change-detail");
      const user = userEvent.setup();
      await user.click(screen.getByTestId("action-promote"));
      post.mockRejectedValueOnce(apiError(409, "Conflict", { state: "live" }));
      await user.click(await screen.findByTestId("dialog-confirm"));
      expect(await within(screen.getByRole("dialog")).findByRole("alert")).toHaveTextContent(
        "Not possible now: the change is Live."
      );
    });

    it("a failed mode change stops before approve is sent", async () => {
      open();
      await screen.findByTestId("change-detail");
      const user = userEvent.setup();
      await user.click(screen.getByTestId("action-approve"));
      await user.click(
        await screen.findByRole("radio", { name: "every channel switches at once" })
      );
      patch.mockRejectedValueOnce(apiError(409, "Conflict", { state: "swapping" }));
      await user.click(screen.getByTestId("dialog-confirm"));
      expect(await within(screen.getByRole("dialog")).findByRole("alert")).toHaveTextContent(
        "Not possible now: the change is Swapping."
      );
      expect(post).not.toHaveBeenCalledWith(expect.stringContaining("/approve"), expect.anything());
    });
  });

  describe("LCD-21: cancel", () => {
    it("the dialog's Cancel closes it with no request; confirming cancels the change", async () => {
      open();
      await screen.findByTestId("change-detail");
      const user = userEvent.setup();
      await user.click(screen.getByTestId("action-cancel"));
      let dialog = await screen.findByRole("dialog", { name: "Cancel change #42" });
      await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
      expect(post).not.toHaveBeenCalled();

      await user.click(screen.getByTestId("action-cancel"));
      dialog = await screen.findByRole("dialog");
      let resolve!: (v: unknown) => void;
      post.mockImplementationOnce(() => new Promise((r) => (resolve = r)));
      const confirm = within(dialog).getByRole("button", { name: "Cancel the change" });
      await user.click(confirm);
      expect(confirm).toBeDisabled();
      expect(post).toHaveBeenCalledWith(`/api/tv/changes/${readyChange.id}/cancel`);
      await act(async () => resolve({ ...readyChange, state: "cancelled", allowed_actions: [] }));
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
      expect(await screen.findByRole("status")).toHaveTextContent("Cancelled.");
      expect(chip()).toHaveTextContent("Cancelled");
      expect(screen.getByText("Nothing to do in this state.")).toBeInTheDocument();
    });

    it("409: the dialog stays and announces the reason inside it", async () => {
      open();
      await screen.findByTestId("change-detail");
      const user = userEvent.setup();
      await user.click(screen.getByTestId("action-cancel"));
      post.mockRejectedValueOnce(apiError(409, "Conflict", { state: "swapping" }));
      await user.click(await screen.findByRole("button", { name: "Cancel the change" }));
      const dialog = screen.getByRole("dialog");
      expect(await within(dialog).findByRole("alert")).toHaveTextContent(
        "Not possible now: the change is Swapping."
      );
    });
  });

  describe("LCD-22: roll back", () => {
    it.each([
      [{ mode: "warm" }, "Warm (instant): the channels still switching go back"],
      [{ mode: "cold" }, "Cold (~3 min): the previous version is staged again, then swapped"],
    ])("%j shows its hint", async (point, text) => {
      routeGets({ "/api/tv/rollback": () => point });
      open(liveChange.id);
      expect(await screen.findByTestId("rollback-mode")).toHaveTextContent(text);
    });

    it("cold: the dialog explains it, confirm creates the rollback change and reloads", async () => {
      routeGets({ "/api/tv/rollback": () => ({ mode: "cold" }) });
      open(liveChange.id);
      await screen.findByTestId("rollback-mode");
      const user = userEvent.setup();
      await user.click(screen.getByTestId("action-rollback"));
      const dialog = await screen.findByRole("dialog");
      expect(dialog).toHaveTextContent("The previous slot has stopped");
      const before = detailCalls(liveChange.id);
      post.mockResolvedValueOnce({
        mode: "cold",
        change_id: liveChange.id,
        rollback_change_id: "r1",
      });
      await user.click(within(dialog).getByRole("button", { name: "Roll back" }));
      expect(await screen.findByRole("status")).toHaveTextContent(
        "Rollback change created: it is staged and swapped when ready."
      );
      await waitFor(() => expect(detailCalls(liveChange.id)).toBeGreaterThan(before));
    });

    it("none: disabled inside a focusable wrapper with the reason", async () => {
      routeGets({ "/api/tv/rollback": () => ({ mode: "none", reason: "no_rollback_point" }) });
      open(liveChange.id);
      await waitFor(() => expect(screen.getByTestId("action-rollback")).toBeDisabled());
      const wrap = screen.getByTestId("action-rollback-wrap");
      expect(wrap).toHaveAttribute("tabindex", "0");
      await userEvent.setup().hover(wrap);
      expect(await screen.findByRole("tooltip")).toHaveTextContent(
        "No rollback point: nothing went on the air before this."
      );
    });

    it("the point failing keeps the button usable with no hint", async () => {
      routeGets({ "/api/tv/rollback": () => Promise.reject(new Error("down")) });
      open(liveChange.id);
      await screen.findByTestId("change-detail");
      await waitFor(() => expect(get).toHaveBeenCalledWith("/api/tv/rollback"));
      expect(screen.getByTestId("action-rollback")).toBeEnabled();
      expect(screen.queryByTestId("rollback-mode")).not.toBeInTheDocument();
    });

    it("409: the dialog stays with the reason", async () => {
      open(liveChange.id);
      await screen.findByTestId("rollback-mode");
      const user = userEvent.setup();
      await user.click(screen.getByTestId("action-rollback"));
      post.mockRejectedValueOnce(apiError(409, "Conflict", { state: "swapping" }));
      const dialog = await screen.findByRole("dialog");
      await user.click(within(dialog).getByRole("button", { name: "Roll back" }));
      expect(await within(dialog).findByRole("alert")).toHaveTextContent(
        "Not possible now: the change is Swapping."
      );
    });
  });

  it("LCD-23: a failed preview says so, does not retry by itself, and Try again plays", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    post.mockRejectedValueOnce(new Error("boom"));
    renderPage(<PreviewPanel change={readyChange} />, { path: "/x" });
    expect(await screen.findByText(/The preview could not be prepared: boom/)).toBeInTheDocument();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(10_000);
    });
    expect(post).toHaveBeenCalledTimes(1);
    await userEvent
      .setup({ advanceTimers: vi.advanceTimersByTime })
      .click(screen.getByRole("button", { name: "Try again" }));
    expect(await screen.findByTestId("preview-m3u")).toBeInTheDocument();
    await waitFor(() =>
      expect(tuned[0]).toEqual({ path: "staging", url: preview.channels[1]!.url })
    );
    expect(screen.getByRole("status")).toHaveTextContent("The preview links work until");
  });

  it.each([
    ["missing", undefined],
    ["not a date", "not-a-date"],
    ["in the past", new Date(Date.now() - 3600_000).toISOString()],
  ])("LCD-24: expires_at %s re-mints at most once per 5 s", async (_n, expires) => {
    post.mockImplementation(async () => ({ ...preview, expires_at: expires }));
    renderPage(<PreviewPanel change={readyChange} />, { path: "/x" });
    await screen.findByTestId("preview-m3u");
    // real time: a NaN timeout would re-mint on every turn of the event loop
    await new Promise((r) => setTimeout(r, 150));
    expect(post).toHaveBeenCalledTimes(1);
    noCrash();
  });

  it("LCD-25: the change every 2 s, the config every 60 s, the rollback point only when allowed; nothing hidden or after leaving", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const errors = vi.spyOn(console, "error");
    const count = (p: string) => get.mock.calls.filter((c) => String(c[0]) === p).length;
    const listCalls = () =>
      get.mock.calls.filter((c) => String(c[0]).startsWith("/api/tv/changes?")).length;
    const adv = (ms: number) =>
      act(async () => {
        await vi.advanceTimersByTimeAsync(ms);
      });
    open(liveChange.id);
    await screen.findByTestId("change-detail");
    await waitFor(() => expect(count("/api/tv/rollback")).toBe(1));
    expect(detailCalls(liveChange.id)).toBe(1);
    expect(listCalls()).toBe(1);
    await adv(2000);
    expect(detailCalls(liveChange.id)).toBe(2);
    expect(count("/api/tv/rollback")).toBe(2);
    await adv(58_000);
    expect(listCalls()).toBe(2);
    const d = detailCalls(liveChange.id);
    act(() => {
      visibility = "hidden";
      document.dispatchEvent(new Event("visibilitychange"));
    });
    await adv(10_000);
    expect(detailCalls(liveChange.id)).toBe(d);
    await act(async () => {
      visibility = "visible";
      document.dispatchEvent(new Event("visibilitychange"));
      await Promise.resolve();
    });
    expect(detailCalls(liveChange.id)).toBe(d + 1);
    await userEvent
      .setup({ advanceTimers: vi.advanceTimersByTime })
      .click(screen.getByRole("link", { name: "go elsewhere" }));
    const all = get.mock.calls.length;
    await adv(10_000);
    expect(get.mock.calls.length).toBe(all);
    expect(errors).not.toHaveBeenCalled();
  });

  it("LCD-25: a change without rollback never reads the rollback point", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    open();
    await screen.findByTestId("change-detail");
    await act(async () => {
      await vi.advanceTimersByTimeAsync(4000);
    });
    expect(get).not.toHaveBeenCalledWith("/api/tv/rollback");
  });

  it("LCD-26: a non-admin sees 'Admins only' and nothing is read", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    open(readyChange.id, { user: memberUser });
    expect(screen.getByText("Admins only")).toBeInTheDocument();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(6000);
    });
    expect(get).not.toHaveBeenCalled();
  });

  it("LCD-26: signed out goes to /login with the path", () => {
    open(readyChange.id, { user: null });
    expect(screen.getByText(`login page (from ${at(readyChange.id)})`)).toBeInTheDocument();
    expect(get).not.toHaveBeenCalled();
  });

  it("LCD-26: a 403 from the server shows its message and 'All changes'", async () => {
    serve(null, {
      [`/api/tv/changes/${readyChange.id}`]: () => Promise.reject(apiError(403, "Forbidden")),
    });
    open();
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Could not load the changes: Forbidden"
    );
    expect(screen.getByRole("link", { name: "All changes" })).toHaveAttribute(
      "href",
      "/admin/live-tv/changes"
    );
  });

  it("LCD-27: a render crash shows the route error fallback; leaving and Back work", async () => {
    quietCrash();
    serve({ ...readyChange, title: {} });
    open();
    expect(await screen.findByRole("alert")).toHaveTextContent("Something went wrong.");
    const user = userEvent.setup();
    await user.click(screen.getByRole("link", { name: "go elsewhere" }));
    expect(screen.getByText("elsewhere")).toBeInTheDocument();
    routeGets();
    await user.click(screen.getByRole("button", { name: "go back" }));
    expect(await screen.findByTestId("change-detail")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("LCD-28: All changes, Back, the job link", async () => {
    serve({ ...readyChange, source: { kind: "write_channels", stage: "tv_plan" } });
    open();
    await screen.findByTestId("change-detail");
    const user = userEvent.setup();
    await user.click(screen.getByRole("link", { name: "All changes" }));
    expect(screen.getByText("changes list page")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "go back" }));
    await screen.findByTestId("change-detail");
    await user.click(screen.getByRole("link", { name: "the pipeline job" }));
    expect(screen.getByText("queue page")).toBeInTheDocument();
  });

  it("LCD-28: moving from one change to another starts clean (data, tab, message)", async () => {
    const ui = (
      <>
        <AdminTvChangeDetail />
        <Link to={at(liveChange.id)}>to the live change</Link>
      </>
    );
    renderPage(ui, { path: at(readyChange.id), route: ROUTE, extraRoutes });
    await screen.findByTestId("change-detail");
    const user = userEvent.setup();
    await user.click(screen.getByRole("tab", { name: "Checks" }));
    post.mockResolvedValueOnce(readyChange);
    await user.click(screen.getByTestId("action-verify"));
    expect(await screen.findByRole("status")).toHaveTextContent("The checks run again.");
    await user.click(screen.getByRole("link", { name: "to the live change" }));
    expect(await screen.findByRole("heading", { level: 1 })).toHaveTextContent(
      "#41 Channel plan of 14 June"
    );
    expect(screen.queryByText(/Ramadan grid/)).not.toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Lineup" })).toHaveAttribute("aria-selected", "true");
    expect(screen.queryByText("The checks run again.")).not.toBeInTheDocument();
  });

  it("LCD-30: Arabic, right to left", async () => {
    window.localStorage.setItem("mkt:locale", "ar");
    open();
    await screen.findByTestId("change-detail");
    expect(document.documentElement.getAttribute("dir")).toBe("rtl");
    expect(screen.getByRole("toolbar", { name: "الإجراءات" })).toBeInTheDocument();
    expect(screen.getByRole("tablist", { name: "ما يفعله التغيير" })).toBeInTheDocument();
    expect(screen.getByTestId("action-approve")).toHaveTextContent("اعتماد");
    expect(chip()).toHaveTextContent("جاهز");
    expect(screen.getByRole("link", { name: "كل التغييرات" })).toBeInTheDocument();
    // "105 → 115" stays left to right
    const arrow = within(screen.getByTestId("lineup-diff"))
      .getAllByText(/→/)
      .find((e) => e.getAttribute("dir") === "ltr");
    expect(arrow).toBeTruthy();
    await userEvent.setup().click(screen.getByTestId("action-approve"));
    expect(await screen.findByRole("dialog")).toHaveTextContent("اعتماد التغيير");
  });

  it("LCD-32: toolbar, tabs with arrow keys, the dialog's groups, labels, Escape and focus return", async () => {
    open();
    await screen.findByTestId("change-detail");
    expect(screen.getByRole("toolbar", { name: "Actions" })).toBeInTheDocument();
    const tablist = screen.getByRole("tablist", { name: "What the change does" });
    expect(within(tablist).getAllByRole("tab")).toHaveLength(7);
    const user = userEvent.setup();
    const lineup = screen.getByRole("tab", { name: "Lineup" });
    lineup.focus();
    await user.keyboard("{ArrowRight}");
    expect(screen.getByRole("tab", { name: "Schedules" })).toHaveAttribute("aria-selected", "true");
    expect(screen.getByRole("tabpanel")).toBeInTheDocument();
    expect(document.querySelector(".mkt-tvx-status")).toHaveAttribute("aria-live", "polite");

    const approve = screen.getByTestId("action-approve");
    await user.click(approve);
    const dialog = await screen.findByRole("dialog", { name: "Approve change #42" });
    expect(dialog).toHaveAttribute("aria-modal", "true");
    expect(within(dialog).getByRole("group", { name: "When" })).toBeInTheDocument();
    expect(within(dialog).getByRole("group", { name: "How channels switch" })).toBeInTheDocument();
    await user.click(within(dialog).getByRole("radio", { name: "At a set time" }));
    expect(within(dialog).getByLabelText("Swap at")).toHaveAttribute("type", "datetime-local");
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    await waitFor(() => expect(approve).toHaveFocus());
  });
});
