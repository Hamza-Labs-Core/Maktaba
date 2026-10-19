// The Changes page cases from web/test-cases/admin-live-tv-changes.md (LTC-*): null and
// missing data, load errors, permissions, crash recovery, polling, navigation and the
// held-release errors. Rendered through the shared page harness (RequireAuth + the
// route error boundary + a signed-in user).
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Route } from "react-router-dom";

const { get, post, patch } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), patch: vi.fn() }));
vi.mock("../../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../../lib/api")>("../../../lib/api");
  return { ...actual, api: { ...actual.api, get, post, patch } };
});

import { ApiError, type Problem } from "../../../lib/api";
import { memberUser, quietCrash, renderPage } from "../../../test/pageHarness";
import type { ChangeList, SlotsView, TvChange } from "../../../lib/tvChanges";
import { AdminTvChanges } from "./Changes";
import { HeldReleaseCard } from "./HeldReleaseCard";
import { answer, changeList, draftChange, readyChange, releases } from "./changeFixtures";

const PATH = "/admin/live-tv/changes";

/** Routes the mocked GETs; `over` replaces the answer of a path (without the query). */
function routeGets(over: Record<string, (url: string) => unknown> = {}) {
  get.mockImplementation(async (url: string) => {
    const p = url.split("?")[0]!;
    if (over[p]) return over[p](url);
    if (p === "/api/cluster/updates") return { plan: { held: false } };
    return answer(url);
  });
}

function apiError(status: number, title: string, extra: Record<string, unknown> = {}) {
  return new ApiError({ type: "about:blank", title, status, ...extra } as Problem);
}

const extraRoutes = (
  <>
    <Route path="/admin/live-tv" element={<p>live tv page</p>} />
    <Route path="/admin/live-tv/changes/:id" element={<p>change detail page</p>} />
    <Route path="/settings" element={<p>settings page</p>} />
  </>
);

function open(opts: Partial<Parameters<typeof renderPage>[1]> = {}) {
  return renderPage(<AdminTvChanges />, { path: PATH, extraRoutes, ...opts });
}

const rowIds = () => screen.queryAllByTestId(/^change-\d+$/).map((r) => r.dataset.testid);
const calledPaths = () => get.mock.calls.map((c) => String(c[0]).split("?")[0]);
const noCrash = () => expect(screen.queryByText("Something went wrong.")).not.toBeInTheDocument();

let visibility: DocumentVisibilityState = "visible";
function setVisibility(v: DocumentVisibilityState) {
  visibility = v;
  document.dispatchEvent(new Event("visibilitychange"));
}

beforeEach(() => {
  get.mockReset();
  post.mockReset();
  patch.mockReset();
  routeGets();
  visibility = "visible";
  Object.defineProperty(document, "visibilityState", {
    configurable: true,
    get: () => visibility,
  });
});
afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

describe("Changes page (test-cases/admin-live-tv-changes.md)", () => {
  it("LTC-03: an empty list says so, every count is 0; empty filters say which", async () => {
    routeGets({ "/api/tv/changes": () => ({ ...changeList, items: [], in_flight: undefined }) });
    open();
    expect(await screen.findByText("No TV changes yet.")).toBeInTheDocument();
    expect(screen.getByText("No change is in flight.")).toBeInTheDocument();
    for (const f of ["All", "Drafts", "In flight", "Live", "Ended"])
      expect(screen.getByRole("button", { name: new RegExp(`^${f}\\s*0$`) })).toBeInTheDocument();

    // (b) fixtures without drafts / live / ended
    routeGets({
      "/api/tv/changes": () => ({ ...changeList, items: [readyChange] }),
    });
    const user = userEvent.setup();
    await user.click(screen.getByRole("link", { name: "go elsewhere" }));
    await user.click(screen.getByRole("button", { name: "go back" }));
    await screen.findByTestId("change-42");
    await user.click(screen.getByRole("button", { name: /^Drafts/ }));
    expect(screen.getByText("No drafts.")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /^Live/ }));
    expect(screen.getByText("No change is live.")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /^Ended/ }));
    expect(screen.getByText("No ended changes.")).toBeInTheDocument();
  });

  describe("LTC-04: /api/tv/changes arrays and objects null or missing", () => {
    const one = (c: Record<string, unknown>) => ({ ...draftChange, ...c }) as unknown as TvChange;
    const runs: Array<[string, unknown, (() => unknown)?]> = [
      ["items null", { ...changeList, items: null }, () => screen.findByText("No TV changes yet.")],
      [
        "items missing",
        { ...changeList, items: undefined },
        () => screen.findByText("No TV changes yet."),
      ],
      [
        "users null",
        { ...changeList, items: [draftChange], users: null },
        async () =>
          expect(
            within(await screen.findByTestId("change-43")).getByText(/^bbbbbbbb · /)
          ).toBeInTheDocument(),
      ],
      [
        "users missing",
        { ...changeList, items: [draftChange], users: undefined },
        async () =>
          expect(
            within(await screen.findByTestId("change-43")).getByText(/^bbbbbbbb · /)
          ).toBeInTheDocument(),
      ],
      [
        "config null",
        { ...changeList, config: null },
        async () => {
          await screen.findByTestId("change-43");
          expect(screen.queryByTestId("staging-settings")).not.toBeInTheDocument();
          expect(screen.queryByTestId("staging-off")).not.toBeInTheDocument();
        },
      ],
      [
        "config missing",
        { ...changeList, config: undefined },
        async () => {
          await screen.findByTestId("change-43");
          expect(screen.queryByTestId("staging-settings")).not.toBeInTheDocument();
        },
      ],
      [
        "config.auto_promote null",
        { ...changeList, config: { ...changeList.config, auto_promote: null } },
        () => screen.findByTestId("staging-settings"),
      ],
      [
        "kinds null",
        { ...changeList, items: [one({ kinds: null })] },
        async () =>
          expect(
            within(await screen.findByTestId("change-43")).getAllByRole("cell")[3]
          ).toHaveTextContent("—"),
      ],
      [
        "kinds missing",
        { ...changeList, items: [one({ kinds: undefined })] },
        async () =>
          expect(
            within(await screen.findByTestId("change-43")).getAllByRole("cell")[3]
          ).toHaveTextContent("—"),
      ],
      [
        "verify_report null and checks null",
        {
          ...changeList,
          items: [
            one({ verify_report: null }),
            one({ id: "x2", number: 2, verify_report: { checks: null } }),
          ],
        },
        async () => {
          expect(
            within(await screen.findByTestId("change-43")).getAllByRole("cell")[5]
          ).toHaveTextContent("—");
          expect(within(screen.getByTestId("change-2")).getAllByRole("cell")[5]).toHaveTextContent(
            "—"
          );
        },
      ],
      [
        "source null",
        { ...changeList, items: [one({ source: null })] },
        async () =>
          expect(
            within(await screen.findByTestId("change-43")).getAllByRole("cell")[4]
          ).toHaveTextContent("Admin"),
      ],
      [
        "unknown state",
        { ...changeList, items: [one({ state: "weird" })], in_flight: undefined },
        async () => {
          expect(within(await screen.findByTestId("change-43")).getByText("weird")).toHaveClass(
            "mkt-tvx-chip--neutral"
          );
          expect(screen.getByRole("button", { name: /^All\s*1$/ })).toBeInTheDocument();
          for (const f of ["Drafts", "In flight", "Live", "Ended"])
            expect(
              screen.getByRole("button", { name: new RegExp(`^${f}\\s*0$`) })
            ).toBeInTheDocument();
        },
      ],
      [
        "in_flight not in items",
        { ...changeList, in_flight: "no-such-id" },
        async () => {
          await screen.findByTestId("change-42");
          expect(screen.getByText("No change is in flight.")).toBeInTheDocument();
          expect(screen.queryByTestId("in-flight")).not.toBeInTheDocument();
        },
      ],
      [
        "JSON null body",
        null,
        async () =>
          expect(await screen.findByRole("alert")).toHaveTextContent(
            /^Could not load the changes:/
          ),
      ],
    ];
    it.each(runs)("%s: no crash", async (_name, body, check) => {
      routeGets({ "/api/tv/changes": () => body });
      open();
      await check?.();
      noCrash();
    });
  });

  describe("LTC-05: /api/tv/slots, /api/tv/releases, updates null or missing", () => {
    it("slots null / {} → an empty slot bar, no skeleton once answered", async () => {
      for (const body of [{ runtime: "slots", slots: null }, {}]) {
        routeGets({ "/api/tv/slots": () => body });
        const { unmount } = open();
        await screen.findByTestId("change-42");
        const bar = screen.getByRole("group", { name: "TV processes" });
        expect(bar.querySelector(".mkt-tvx-slot--skeleton")).toBeNull();
        expect(within(bar).queryByTestId(/^slot-home/)).toBeNull();
        noCrash();
        unmount();
      }
    });

    it("a slot without release or version says 'no release yet'", async () => {
      routeGets({
        "/api/tv/slots": () =>
          ({
            runtime: "slots",
            slots: [{ host_id: "home", slot: "blue", role: "live", state: "healthy" }],
          }) satisfies SlotsView,
      });
      open();
      expect(await screen.findByTestId("slot-home-blue")).toHaveTextContent("no release yet");
    });

    it("releases null / {} → 'No releases yet.'", async () => {
      for (const body of [{ items: null }, {}]) {
        routeGets({ "/api/tv/releases": () => body });
        const { unmount } = open();
        expect(await screen.findByText("No releases yet.")).toBeInTheDocument();
        unmount();
      }
    });

    it("a release with kinds null and no streaming version shows dashes", async () => {
      routeGets({
        "/api/tv/releases": () => ({
          items: [{ ...releases.items[0]!, kinds: null, streaming_version: "" }],
        }),
      });
      open();
      const gen = await screen.findByText("generation 6");
      const li = gen.closest("li")!;
      expect(within(li).getAllByText("—")).toHaveLength(2);
      noCrash();
    });

    it("updates with null fields → no held card, the page is fine", async () => {
      for (const body of [{ plan: null }, {}, { plan: { held: true, target: null } }]) {
        routeGets({ "/api/cluster/updates": () => body });
        const { unmount } = open();
        await screen.findByTestId("change-42");
        await waitFor(() => expect(calledPaths()).toContain("/api/cluster/updates"));
        expect(screen.queryByTestId("held-release")).not.toBeInTheDocument();
        noCrash();
        unmount();
      }
    });
  });

  it.fails(
    "LTC-06: slots / releases failing leave no endless skeleton (fails today: failures swallowed, needs a 'not available' i18n key)",
    async () => {
      routeGets({
        "/api/tv/slots": () => Promise.reject(apiError(500, "Internal Server Error")),
        "/api/tv/releases": () => Promise.reject(apiError(404, "Not Found")),
      });
      open();
      await screen.findByTestId("change-42");
      // the list is fine and no load error is shown for these
      expect(screen.queryByRole("alert")).not.toBeInTheDocument();
      expect(document.querySelector(".mkt-tvx-slot--skeleton")).toBeNull();
      expect(document.querySelector(".mkt-tvx-skeleton--short")).toBeNull();
    }
  );

  describe("LTC-07: the list fails on first load", () => {
    const runs: Array<[string, unknown, string]> = [
      ["500", apiError(500, "Internal Server Error"), "Internal Server Error"],
      ["403", apiError(403, "Forbidden"), "Forbidden"],
      ["401", apiError(401, "Unauthorized"), "Unauthorized"],
      ["network", new TypeError("Failed to fetch"), "Failed to fetch"],
      ["malformed JSON", new SyntaxError("Unexpected token < in JSON"), "Unexpected token"],
      [
        "409 with a state",
        apiError(409, "Conflict", { state: "swapping" }),
        "Not possible now: the change is Swapping.",
      ],
      ["501", apiError(501, "Not Implemented"), "Not available yet."],
    ];
    it.each(runs)(
      "%s: alert, skeleton, counts 0; the next good poll clears it",
      async (_n, err, text) => {
        vi.useFakeTimers({ shouldAdvanceTime: true });
        let fail = true;
        routeGets({
          "/api/tv/changes": (u) => (fail ? Promise.reject(err) : answer(u)),
        });
        open();
        const alert = await screen.findByRole("alert");
        expect(alert).toHaveTextContent(`Could not load the changes: ${text}`);
        expect(alert.parentElement).toHaveAttribute("aria-live", "polite");
        expect(screen.getByRole("button", { name: /^All\s*0$/ })).toBeInTheDocument();
        expect(document.querySelector(".mkt-tvx-card > .mkt-tvx-skeleton")).not.toBeNull();
        fail = false;
        await act(async () => {
          await vi.advanceTimersByTimeAsync(2100);
        });
        expect(screen.queryByRole("alert")).not.toBeInTheDocument();
        expect(rowIds()).toEqual(["change-43", "change-42", "change-41", "change-40"]);
      }
    );
  });

  it("LTC-08: a failure after a good load keeps the rows and shows the error until the next good read", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    open();
    await screen.findByTestId("change-42");
    const row = screen.getByTestId("change-41");
    routeGets({ "/api/tv/changes": () => Promise.reject(new Error("boom")) });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(2100);
    });
    expect(await screen.findByRole("alert")).toHaveTextContent("Could not load the changes: boom");
    expect(screen.getByTestId("change-41")).toBe(row);
    expect(document.querySelector(".mkt-tvx-card > .mkt-tvx-skeleton")).toBeNull();
    routeGets();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(2100);
    });
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("LTC-10: the in-flight banner links to the change; an untitled change says so", async () => {
    open();
    const banner = await screen.findByTestId("in-flight");
    expect(banner).toHaveTextContent("Change #42 is Ready: Ramadan grid and the Seerah channel");
    await userEvent.setup().click(banner);
    expect(screen.getByText("change detail page")).toBeInTheDocument();
  });

  it("LTC-10: an in-flight change without a title reads 'Untitled change'", async () => {
    routeGets({
      "/api/tv/changes": () => ({
        ...changeList,
        items: changeList.items.map((c) =>
          c.id === readyChange.id ? { ...c, title: "", state: "verifying" } : c
        ),
      }),
    });
    open();
    expect(await screen.findByTestId("in-flight")).toHaveTextContent(
      "Change #42 is Verifying: Untitled change"
    );
  });

  describe("LTC-11: the slot bar", () => {
    const twoHosts: SlotsView = {
      runtime: "slots",
      slots: [
        {
          host_id: "home",
          slot: "blue",
          role: "live",
          state: "healthy",
          release_id: "5e1ea5e0-aaaa",
          version: "0.33.0",
        },
        { host_id: "home", slot: "green", role: "standby", state: "warming" },
        { host_id: "node-2", slot: "blue", role: "live", state: "healthy" },
      ],
    };
    const inFlight = (c: Partial<TvChange>): ChangeList => ({
      ...changeList,
      items: changeList.items.map((x) => (x.id === readyChange.id ? { ...x, ...c } : x)),
    });

    it("hosts, roles, colours, release, version; verifying shows the checks progress", async () => {
      routeGets({
        "/api/tv/slots": () => twoHosts,
        "/api/tv/changes": () =>
          inFlight({
            state: "verifying",
            verify_report: {
              checks: [
                { check: "a", result: "pass" },
                { check: "b", result: "warn" },
                { check: "c", result: "fail" },
              ],
            },
          }),
      });
      open();
      const blue = await screen.findByTestId("slot-home-blue");
      const bar = screen.getByRole("group", { name: "TV processes" });
      expect(within(bar).getByText("Home")).toBeInTheDocument();
      expect(within(bar).getByText("node-2")).toBeInTheDocument();
      expect(blue).toHaveTextContent("LIVE");
      expect(blue).toHaveTextContent("blue");
      expect(blue).toHaveTextContent("release 5e1ea5e0");
      expect(blue).toHaveTextContent("0.33.0");
      expect(blue).toHaveTextContent("healthy");
      expect(screen.getByTestId("slot-home-green")).toHaveTextContent("STANDBY");
      expect(screen.getByTestId("slot-home-green")).toHaveTextContent("warming");
      expect(screen.getByTestId("slot-node-2-blue")).toBeInTheDocument();
      expect(screen.getAllByTestId("slot-staging")[0]).toHaveTextContent("checks 3 done, 1 failed");
      expect(screen.queryByText(/One TV process runs today/)).not.toBeInTheDocument();
    });

    it("a ready change shows 'change #N'; the single runtime shows its hint", async () => {
      open();
      expect(await screen.findByTestId("slot-staging")).toHaveTextContent("change #42");
      expect(screen.getByText(/One TV process runs today/)).toBeInTheDocument();
    });

    it("a draft in flight has no staging slot", async () => {
      routeGets({ "/api/tv/changes": () => inFlight({ state: "draft" }) });
      open();
      await screen.findByTestId("slot-home-blue");
      expect(screen.queryByTestId("slot-staging")).not.toBeInTheDocument();
    });
  });

  it("LTC-14: polls every 2 s while visible only, at once on show, never after leaving", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const tick = () =>
      act(async () => {
        await vi.advanceTimersByTimeAsync(2000);
      });
    const errors = vi.spyOn(console, "error");
    open();
    await screen.findByTestId("change-42");
    const count = (p: string) => calledPaths().filter((x) => x === p).length;
    expect(count("/api/tv/changes")).toBe(1);
    await tick();
    expect(count("/api/tv/changes")).toBe(2);
    expect(count("/api/tv/slots")).toBe(2);
    expect(count("/api/tv/releases")).toBe(2);

    act(() => setVisibility("hidden"));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(10_000);
    });
    expect(count("/api/tv/changes")).toBe(2);

    await act(async () => {
      setVisibility("visible");
      await Promise.resolve();
    });
    expect(count("/api/tv/changes")).toBe(3);

    await userEvent
      .setup({ advanceTimers: vi.advanceTimersByTime })
      .click(screen.getByRole("link", { name: "go elsewhere" }));
    const before = get.mock.calls.length;
    await act(async () => {
      await vi.advanceTimersByTimeAsync(10_000);
    });
    expect(get.mock.calls.length).toBe(before);
    // the held-release card reads once
    expect(count("/api/cluster/updates")).toBe(1);
    expect(errors).not.toHaveBeenCalled();
  });

  describe("LTC-15: the held release", () => {
    const held = {
      auto_install_available: true,
      plan: {
        held: true,
        target: { version: "1.9.0", release_url: "https://example.org/r/1.9.0" },
      },
    };

    it("a build that cannot install says so and offers no Approve", async () => {
      routeGets({ "/api/cluster/updates": () => ({ ...held, auto_install_available: false }) });
      renderPage(<HeldReleaseCard />, { path: "/x" });
      expect(await screen.findByTestId("held-not-built")).toBeInTheDocument();
      expect(
        screen.queryByRole("button", { name: "Approve and update now" })
      ).not.toBeInTheDocument();
    });

    it("a region named by the version; the release link opens a new tab", async () => {
      routeGets({ "/api/cluster/updates": () => held });
      renderPage(<HeldReleaseCard />, { path: "/x" });
      const region = await screen.findByRole("region", { name: /1\.9\.0/ });
      const link = within(region).getByRole("link");
      expect(link).toHaveAttribute("target", "_blank");
      expect(link).toHaveAttribute("href", "https://example.org/r/1.9.0");
    });

    it.each([
      ["an ApiError shows its message", apiError(409, "busy"), "busy"],
      ["another error shows the generic text", new Error("x"), "Something went wrong."],
    ])("%s; the buttons come back", async (_n, err, text) => {
      routeGets({ "/api/cluster/updates": () => held });
      post.mockRejectedValueOnce(err);
      renderPage(<HeldReleaseCard />, { path: "/x" });
      const user = userEvent.setup();
      await user.click(await screen.findByRole("button", { name: "Approve and update now" }));
      expect(await screen.findByRole("alert")).toHaveTextContent(text);
      expect(screen.getByRole("button", { name: "Approve and update now" })).toBeEnabled();
      expect(screen.getByRole("button", { name: "Skip this version" })).toBeEnabled();
    });

    it("404 on updates → no card", async () => {
      routeGets({ "/api/cluster/updates": () => Promise.reject(apiError(404, "Not Found")) });
      renderPage(<HeldReleaseCard />, { path: "/x" });
      await waitFor(() => expect(calledPaths()).toContain("/api/cluster/updates"));
      expect(screen.queryByTestId("held-release")).not.toBeInTheDocument();
    });

    it("after a success with nothing held any more, a status note stays", async () => {
      let h: unknown = held;
      routeGets({ "/api/cluster/updates": () => h });
      post.mockImplementationOnce(async () => {
        h = { plan: { held: false } };
        return {};
      });
      renderPage(<HeldReleaseCard />, { path: "/x" });
      await userEvent
        .setup()
        .click(await screen.findByRole("button", { name: "Skip this version" }));
      expect(await screen.findByRole("status")).toHaveTextContent(
        "Version 1.9.0 will not be installed automatically."
      );
      expect(screen.queryByTestId("held-release")).not.toBeInTheDocument();
    });
  });

  it("LTC-16: a non-admin sees 'Admins only' and nothing polls", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    open({ user: memberUser });
    expect(screen.getByText("Admins only")).toBeInTheDocument();
    expect(
      screen.getByText("You need administrator access to view this page.")
    ).toBeInTheDocument();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(6000);
    });
    expect(get).not.toHaveBeenCalled();
  });

  it("LTC-17: a signed-out visitor is sent to /login and nothing is read", () => {
    open({ user: null });
    expect(screen.getByText(`login page (from ${PATH})`)).toBeInTheDocument();
    expect(get).not.toHaveBeenCalled();
  });

  it("LTC-18: a render crash shows the route error fallback; leaving and Back work", async () => {
    quietCrash();
    routeGets({
      "/api/tv/changes": () => ({
        ...changeList,
        items: [{ ...draftChange, title: {} as unknown as string }],
      }),
    });
    open();
    expect(await screen.findByRole("alert")).toHaveTextContent("Something went wrong.");
    const user = userEvent.setup();
    await user.click(screen.getByRole("link", { name: "go elsewhere" }));
    expect(screen.getByText("elsewhere")).toBeInTheDocument();
    // the crashed page's poll stopped
    const n = get.mock.calls.length;
    await new Promise((r) => setTimeout(r, 0));
    expect(get.mock.calls.length).toBe(n);
    routeGets();
    await user.click(screen.getByRole("button", { name: "go back" }));
    expect(await screen.findByTestId("change-42")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("LTC-19: links go to Live TV and the change; Back returns with the filter reset to All", async () => {
    open({ path: `${PATH}?filter=live` });
    await screen.findByTestId("change-42");
    // the query is ignored: the filter is not in the URL
    expect(screen.getByRole("button", { name: /^All/ })).toHaveAttribute("aria-pressed", "true");
    const user = userEvent.setup();
    await user.click(screen.getByRole("link", { name: "Back to Live TV" }));
    expect(screen.getByText("live tv page")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "go back" }));
    await screen.findByTestId("change-42");
    await user.click(screen.getByRole("button", { name: /^Ended/ }));
    await user.click(screen.getByRole("link", { name: "Regenerate Hadith" }));
    expect(screen.getByText("change detail page")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "go back" }));
    await screen.findByTestId("change-42");
    expect(screen.getByRole("button", { name: /^All/ })).toHaveAttribute("aria-pressed", "true");
    // the settings link
    await user.click(
      within(screen.getByTestId("staging-settings")).getByRole("link", {
        name: /TV staging settings/,
      })
    );
    expect(screen.getByText("settings page")).toBeInTheDocument();
  });

  it("LTC-23: table roles, pressed filters, polite error region, hidden skeletons, checks label, tab order", async () => {
    let resolve!: (v: unknown) => void;
    routeGets({ "/api/tv/changes": () => new Promise((r) => (resolve = r)) });
    open();
    const skeleton = await waitFor(() => {
      const s = document.querySelector(".mkt-tvx-card > .mkt-tvx-skeleton");
      expect(s).not.toBeNull();
      return s!;
    });
    expect(skeleton).toHaveAttribute("aria-hidden", "true");
    await act(async () => resolve(changeList));
    const table = await screen.findByRole("table", { name: "TV changes" });
    expect(within(table).getAllByRole("columnheader")).toHaveLength(7);
    expect(within(table).getAllByRole("row")).toHaveLength(5);
    expect(within(table).getAllByRole("cell").length).toBe(4 * 7);
    for (const b of within(screen.getByRole("group", { name: "Show" })).getAllByRole("button"))
      expect(b).toHaveAttribute("aria-pressed");
    expect(
      within(screen.getByTestId("change-42")).getByLabelText("3 passed, 1 warnings, 0 failed")
    ).toBeInTheDocument();
    expect(document.querySelector(".mkt-tvx-status")).toHaveAttribute("aria-live", "polite");
    // keyboard: the in-flight link and the row title links are reachable with Tab
    const user = userEvent.setup();
    const reached: Element[] = [];
    for (let i = 0; i < 20; i++) {
      await user.tab();
      if (document.activeElement) reached.push(document.activeElement);
    }
    expect(reached).toContain(screen.getByTestId("in-flight"));
    expect(reached).toContain(
      screen.getByRole("link", { name: "Ramadan grid and the Seerah channel" })
    );
    expect(reached).toContain(screen.getByRole("button", { name: /^Drafts/ }));
  });
});
