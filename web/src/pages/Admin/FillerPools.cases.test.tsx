// Admin Filler pools, case by case from web/test-cases/admin-filler.md (FIL-*).
// Uses the shared page harness (RequireAuth + RouteErrorBoundary + a signed-in
// user), so unlike FillerPools.test.tsx it does not mock ../../lib/auth.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Route } from "react-router-dom";
import { ToastProvider } from "@ds/components/Toast/Toast";
import { renderPage, memberUser, quietCrash } from "../../test/pageHarness";

const { get, post, del } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), del: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post, delete: del } };
});

import { ApiError } from "../../lib/api";
import { FillerPools } from "./FillerPools";

function problem(status: number, title: string) {
  return new ApiError({ type: "about:blank", status, title });
}

const POOLS = {
  items: [
    {
      id: "p1",
      name: "News clips",
      external_key: "news.topic",
      items: [
        {
          id: "i1",
          pool_id: "p1",
          video_id: "v1",
          type: "filler",
          title: "Headlines",
          duration_ms: 90_000,
        },
        {
          id: "i2",
          pool_id: "p1",
          video_id: "v2",
          type: "bumper",
          title: "Sting",
          duration_ms: 30_000,
        },
      ],
    },
    { id: "p2", name: "Bumpers", channel_id: "c1", items: [] },
  ],
};
const VIDEOS = {
  kind: "video",
  items: [{ kind: "video", id: "v9", name: "Station ident", count: 1, duration_ms: 15_000 }],
};

let pools: unknown;
let sources: unknown;

function answer(v: unknown): Promise<unknown> {
  if (v instanceof Error) return Promise.reject(v);
  if (typeof v === "function") return answer((v as () => unknown)());
  return Promise.resolve(v);
}

function routeGet(url: string): Promise<unknown> {
  const path = url.split("?")[0];
  if (path === "/api/filler/pools") return answer(pools);
  if (path === "/api/channels/sources") return answer(sources);
  return Promise.reject(new Error(`unmocked GET ${url}`));
}

const poolCalls = () => get.mock.calls.filter(([u]) => u === "/api/filler/pools");

function renderPools(opts: Partial<Parameters<typeof renderPage>[1]> = {}) {
  return renderPage(
    <ToastProvider>
      <FillerPools />
    </ToastProvider>,
    { path: "/admin/filler", ...opts }
  );
}

const toggle = (name: RegExp | string) => screen.getByRole("button", { name });
const card = (name: RegExp | string) =>
  toggle(name).closest(".mkt-fillerpools__pool") as HTMLElement;

describe("Admin Filler pools (test-cases/admin-filler.md)", () => {
  beforeEach(() => {
    for (const f of [get, post, del]) f.mockReset();
    pools = POOLS;
    sources = VIDEOS;
    get.mockImplementation(routeGet);
  });
  afterEach(() => vi.restoreAllMocks());

  it("FIL-01: lists pools with clip count, running time and badges", async () => {
    renderPools();
    expect(await screen.findByRole("heading", { name: "Filler pools" })).toBeInTheDocument();
    expect(screen.getByText(/^Clips, bumpers and station idents/)).toBeInTheDocument();
    const news = toggle(/news clips/i);
    expect(news).toHaveTextContent("2 clips · 2 min");
    expect(within(card(/news clips/i)).getByText("From plan")).toBeInTheDocument();
    expect(within(card(/news clips/i)).queryByText("One channel")).toBeNull();
    const bumpers = toggle(/^bumpers/i);
    expect(bumpers).toHaveTextContent("0 clips · 0 min");
    expect(within(card(/^bumpers/i)).getByText("One channel")).toBeInTheDocument();
    for (const c of [card(/news clips/i), card(/^bumpers/i)]) {
      expect(within(c).getByRole("button", { name: "Delete" })).toBeInTheDocument();
    }
    expect(news).toHaveAttribute("aria-expanded", "false");
    expect(bumpers).toHaveAttribute("aria-expanded", "false");
  });

  it("FIL-02: expanding a pool shows its items, the plan note and the player links", async () => {
    const user = userEvent.setup();
    renderPools();
    await user.click(await screen.findByRole("button", { name: /news clips/i }));
    expect(toggle(/news clips/i)).toHaveAttribute("aria-expanded", "true");
    const news = card(/news clips/i);
    expect(
      within(news).getByText(
        "The lineup plan writes this pool: the next lineup run replaces its clips with the plan's."
      )
    ).toBeInTheDocument();
    const item = (title: string) => within(news).getByRole("link", { name: title }).closest("li")!;
    expect(within(item("Headlines")).getByText("Filler")).toBeInTheDocument();
    expect(within(item("Sting")).getByText("Bumper")).toBeInTheDocument();
    expect(within(news).getByRole("link", { name: "Headlines" })).toHaveAttribute(
      "href",
      "/videos/v1/watch?t=0"
    );
    expect(within(news).getByRole("button", { name: "Remove Headlines" })).toBeInTheDocument();
    await user.click(toggle(/news clips/i));
    expect(toggle(/news clips/i)).toHaveAttribute("aria-expanded", "false");
    await user.click(toggle(/news clips/i));
    await user.click(toggle(/^bumpers/i));
    expect(toggle(/news clips/i)).toHaveAttribute("aria-expanded", "false");
    const bumpers = card(/^bumpers/i);
    expect(within(bumpers).getByText("No items yet.")).toBeInTheDocument();
    expect(within(bumpers).queryByText(/lineup plan writes/)).toBeNull();
  });

  it("FIL-03: items: [] shows the empty state; the add-pool controls still work", async () => {
    pools = { items: [] };
    post.mockResolvedValue({ id: "p3", name: "Idents", items: [] });
    const user = userEvent.setup();
    renderPools();
    expect(await screen.findByText("No filler pools")).toBeInTheDocument();
    expect(
      screen.getByText("Create a pool, then add clips by searching the library.")
    ).toBeInTheDocument();
    expect(screen.getByLabelText("Search pools")).toBeInTheDocument();
    await user.type(screen.getByLabelText("Pool name"), "Idents");
    await user.click(screen.getByRole("button", { name: "Add pool" }));
    expect(post).toHaveBeenCalledWith("/api/filler/pools", { name: "Idents" });
  });

  it.each([
    ["items: null", { items: null }],
    ["missing items key", {}],
  ])("FIL-04: %s shows the empty state", async (_, body) => {
    pools = body;
    renderPools();
    expect(await screen.findByText("No filler pools")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it.each([
    ["items: null", { id: "p1", name: "A", items: null }],
    ["no items key", { id: "p1", name: "A" }],
  ])("FIL-04: a pool with %s shows 0 clips and opens", async (_, pool) => {
    pools = { items: [pool] };
    const user = userEvent.setup();
    renderPools();
    const a = await screen.findByRole("button", { name: /^A\s?\d/ });
    expect(a).toHaveTextContent("0 clips · 0 min");
    await user.click(a);
    expect(screen.getByText("No items yet.")).toBeInTheDocument();
    expect(screen.getByLabelText("Kind")).toBeInTheDocument();
    expect(
      screen.getByRole("searchbox", { name: "Add a clip from the library" })
    ).toBeInTheDocument();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("FIL-05: missing optional fields fall back without crashing", async () => {
    pools = {
      items: [
        {
          id: "p1",
          name: "A",
          external_key: null,
          channel_id: null,
          items: [
            {
              id: "i1",
              pool_id: "p1",
              video_id: "v1",
              type: "filler",
              title: null,
              duration_ms: null,
            },
            { id: "i2", pool_id: "p1", video_id: "v2", type: "unknown_kind" },
          ],
        },
      ],
    };
    const user = userEvent.setup();
    renderPools();
    await user.click(await screen.findByRole("button", { name: /^A\s?\d/ }));
    const a = card(/^A\s?\d/);
    expect(within(a).queryByText("From plan")).toBeNull();
    expect(within(a).queryByText("One channel")).toBeNull();
    expect(within(a).getByRole("link", { name: "v1" })).toBeInTheDocument();
    expect(within(a).getByRole("button", { name: "Remove v1" })).toBeInTheDocument();
    const i1 = within(a).getByRole("link", { name: "v1" }).closest("li")!;
    expect(within(i1).getByText("0 min")).toBeInTheDocument();
    const i2 = within(a).getByRole("link", { name: "v2" }).closest("li")!;
    expect(i2).not.toHaveTextContent("filler.kind.");
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("FIL-06: a pool with name null does not crash the search", async () => {
    quietCrash();
    pools = { items: [{ id: "p1", name: null, items: [] }] };
    const user = userEvent.setup();
    renderPools();
    await user.type(await screen.findByLabelText("Search pools"), "a");
    expect(screen.queryByRole("alert")).toBeNull();
    expect(screen.getByRole("heading", { name: "Filler pools" })).toBeInTheDocument();
  });

  it.each([
    ["500 problem", problem(500, "Internal error"), "Internal error"],
    ["network failure", new TypeError("Failed to fetch"), "Something went wrong."],
    ["403 problem", problem(403, "Forbidden"), "Forbidden"],
  ])("FIL-07: pools load %s shows an error with Try again", async (_, err, text) => {
    pools = err;
    const user = userEvent.setup();
    renderPools();
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent(text);
    expect(screen.queryByText("No filler pools")).toBeNull();
    pools = POOLS;
    await user.click(within(alert).getByRole("button", { name: "Try again" }));
    expect(await screen.findByRole("button", { name: /news clips/i })).toBeInTheDocument();
  });

  it("FIL-09: Add pool needs a name; the name is trimmed and the new pool opens", async () => {
    post.mockImplementation(() => {
      pools = {
        items: [...POOLS.items, { id: "p3", name: "Idents", items: [] }],
      };
      return Promise.resolve({ id: "p3", name: "Idents", items: [] });
    });
    const user = userEvent.setup();
    renderPools();
    await screen.findByRole("button", { name: /news clips/i });
    const add = screen.getByRole("button", { name: "Add pool" });
    const name = screen.getByLabelText("Pool name");
    expect(add).toBeDisabled();
    await user.type(name, " ");
    expect(add).toBeDisabled();
    await user.clear(name);
    await user.type(name, " Idents ");
    expect(add).toBeEnabled();
    await user.click(add);
    expect(post).toHaveBeenCalledWith("/api/filler/pools", { name: "Idents" });
    expect(name).toHaveValue("");
    const idents = await screen.findByRole("button", { name: /^idents/i });
    expect(idents).toHaveAttribute("aria-expanded", "true");
    await user.type(name, "x{Enter}");
    expect(post).toHaveBeenCalledTimes(1);
    expect(screen.getByRole("heading", { name: "Filler pools" })).toBeInTheDocument();
  });

  it.each([
    ["409 problem", problem(409, "A pool with that name exists"), "A pool with that name exists"],
    ["network failure", new TypeError("Failed to fetch"), "Something went wrong."],
  ])("FIL-10: create pool %s toasts and keeps the name", async (_, err, text) => {
    post.mockRejectedValueOnce(err);
    const user = userEvent.setup();
    renderPools();
    await screen.findByRole("button", { name: /news clips/i });
    await user.type(screen.getByLabelText("Pool name"), "News clips");
    const before = poolCalls().length;
    await user.click(screen.getByRole("button", { name: "Add pool" }));
    expect(await screen.findByText(text)).toBeInTheDocument();
    expect(screen.getByLabelText("Pool name")).toHaveValue("News clips");
    expect(poolCalls().length).toBe(before);
  });

  it("FIL-11: delete a pool asks first; Cancel sends nothing; an error toasts", async () => {
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(false);
    const user = userEvent.setup();
    renderPools();
    await screen.findByRole("button", { name: /^bumpers/i });
    await user.click(within(card(/^bumpers/i)).getByRole("button", { name: "Delete" }));
    expect(confirm).toHaveBeenCalledWith("Delete the pool “Bumpers”?");
    expect(del).not.toHaveBeenCalled();

    confirm.mockReturnValue(true);
    del.mockImplementationOnce(() => {
      pools = { items: [POOLS.items[0]] };
      return Promise.resolve(undefined);
    });
    await user.click(within(card(/^bumpers/i)).getByRole("button", { name: "Delete" }));
    expect(del).toHaveBeenCalledWith("/api/filler/pools/p2");
    await waitFor(() => expect(screen.queryByRole("button", { name: /^bumpers/i })).toBeNull());

    del.mockRejectedValueOnce(problem(409, "Pool in use"));
    await user.click(within(card(/news clips/i)).getByRole("button", { name: "Delete" }));
    expect(await screen.findByText("Pool in use")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /news clips/i })).toBeInTheDocument();
  });

  it("FIL-12: adds a clip with the chosen kind; the kind stays across pools", async () => {
    post.mockImplementation(() => {
      pools = {
        items: [
          POOLS.items[0],
          {
            ...POOLS.items[1],
            items: [
              {
                id: "i9",
                pool_id: "p2",
                video_id: "v9",
                type: "bumper",
                title: "Station ident",
                duration_ms: 15_000,
              },
            ],
          },
        ],
      };
      return Promise.resolve({ items: [] });
    });
    const user = userEvent.setup();
    renderPools();
    await user.click(await screen.findByRole("button", { name: /^bumpers/i }));
    await user.selectOptions(screen.getByLabelText("Kind"), "bumper");
    await user.type(
      screen.getByRole("searchbox", { name: "Add a clip from the library" }),
      "ident"
    );
    await user.click(await screen.findByRole("button", { name: /station ident/i }));
    expect(post).toHaveBeenCalledWith("/api/filler/pools/p2/items", {
      items: [{ video_id: "v9", type: "bumper" }],
    });
    await waitFor(() => expect(toggle(/^bumpers/i)).toHaveTextContent("1 clip"));
    await user.click(toggle(/news clips/i));
    expect(screen.getByLabelText("Kind")).toHaveValue("bumper");
  });

  it.each([
    ["400 problem", problem(400, "Video already in pool"), "Video already in pool"],
    ["network failure", new TypeError("Failed to fetch"), "Something went wrong."],
  ])("FIL-13: add clip %s toasts; the pool stays open", async (_, err, text) => {
    post.mockRejectedValueOnce(err);
    const user = userEvent.setup();
    renderPools();
    await user.click(await screen.findByRole("button", { name: /^bumpers/i }));
    await user.click(await screen.findByRole("button", { name: /station ident/i }));
    expect(await screen.findByText(text)).toBeInTheDocument();
    expect(toggle(/^bumpers/i)).toHaveAttribute("aria-expanded", "true");
    expect(toggle(/^bumpers/i)).toHaveTextContent("0 clips");
  });

  it("FIL-14: removes a clip without asking; a failure toasts and keeps it", async () => {
    const confirm = vi.spyOn(window, "confirm");
    del.mockImplementationOnce(() => {
      pools = {
        items: [{ ...POOLS.items[0], items: [POOLS.items[0].items[1]] }, POOLS.items[1]],
      };
      return Promise.resolve(undefined);
    });
    const user = userEvent.setup();
    renderPools();
    await user.click(await screen.findByRole("button", { name: /news clips/i }));
    await user.click(screen.getByRole("button", { name: "Remove Headlines" }));
    expect(confirm).not.toHaveBeenCalled();
    expect(del).toHaveBeenCalledWith("/api/filler/items/i1");
    await waitFor(() => expect(screen.queryByRole("link", { name: "Headlines" })).toBeNull());
    expect(toggle(/news clips/i)).toHaveTextContent("1 clip");

    del.mockRejectedValueOnce(problem(500, "Down"));
    await user.click(screen.getByRole("button", { name: "Remove Sting" }));
    expect(await screen.findByText("Down")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Sting" })).toBeInTheDocument();
  });

  it("FIL-15: video search loading, empty, null, error and a hit with detail null", async () => {
    let release: (v: unknown) => void = () => {};
    sources = () => new Promise((r) => (release = r));
    const user = userEvent.setup();
    renderPools();
    await user.click(await screen.findByRole("button", { name: /^bumpers/i }));
    const body = card(/^bumpers/i);
    expect(within(body).getByText("Loading…")).toBeInTheDocument();
    release({ items: [] });
    expect(await within(body).findByText("Nothing to pick from yet.")).toBeInTheDocument();

    sources = { items: [] };
    await user.type(
      within(body).getByRole("searchbox", { name: "Add a clip from the library" }),
      "zz"
    );
    expect(await within(body).findByText("Nothing matches “zz”.")).toBeInTheDocument();
    const calls = get.mock.calls.filter(([u]) => String(u).startsWith("/api/channels/sources"));
    expect(calls.map(([u]) => new URLSearchParams(String(u).split("?")[1]).get("q"))).toEqual([
      "",
      "zz",
    ]); // debounced: no request for "z"

    for (const [answerWith, text] of [
      [{ items: null }, "Nothing matches “zzz”."],
      [{}, "Nothing matches “zzzz”."],
      [problem(500, "Down"), "Something went wrong."],
    ] as const) {
      sources = answerWith;
      await user.type(
        within(body).getByRole("searchbox", { name: "Add a clip from the library" }),
        "z"
      );
      expect(await within(body).findByText(text)).toBeInTheDocument();
    }
    sources = {
      items: [
        { kind: "video", id: "v9", name: "Station ident", duration_ms: 15_000, detail: null },
      ],
    };
    await user.clear(within(body).getByRole("searchbox", { name: "Add a clip from the library" }));
    const hit = await within(body).findByRole("button", { name: /station ident/i });
    expect(hit).toHaveTextContent("Station ident0 min");
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("FIL-16: the pool search matches name and plan key, case-insensitively", async () => {
    const user = userEvent.setup();
    renderPools();
    await screen.findByRole("button", { name: /news clips/i });
    const search = screen.getByLabelText("Search pools");
    await user.type(search, "NEWS");
    expect(screen.getByRole("button", { name: /news clips/i })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /^bumpers/i })).toBeNull();
    await user.clear(search);
    await user.type(search, "topic");
    expect(screen.getByRole("button", { name: /news clips/i })).toBeInTheDocument();
    await user.clear(search);
    await user.type(search, "zzz");
    expect(screen.queryByRole("button", { name: /news clips/i })).toBeNull();
    await user.clear(search);
    expect(screen.getByRole("button", { name: /^bumpers/i })).toBeInTheDocument();
  });

  // Bug: with pools present but none matching, the page shows the "No filler
  // pools / Create a pool..." empty state. Fix in FillerPools.tsx needs a new
  // i18n key (e.g. fillerPools.noMatch: "No pool matches “{q}”.").
  it.fails("FIL-16: no match says nothing matches, not 'No filler pools'", async () => {
    const user = userEvent.setup();
    renderPools();
    await screen.findByRole("button", { name: /news clips/i });
    await user.type(screen.getByLabelText("Search pools"), "zzz");
    expect(screen.queryByText("No filler pools")).toBeNull();
    expect(screen.getByText(/no pool matches/i)).toBeInTheDocument();
  });

  it("FIL-17: a non-admin sees Admins only and no requests fire", async () => {
    renderPools({ user: memberUser });
    expect(await screen.findByText("Admins only")).toBeInTheDocument();
    expect(
      screen.getByText("You need administrator access to view this page.")
    ).toBeInTheDocument();
    expect(get).not.toHaveBeenCalled();
  });

  it("FIL-18: a signed-out visitor is sent to /login with the page as from", () => {
    renderPools({ user: null });
    expect(screen.getByText("login page (from /admin/filler)")).toBeInTheDocument();
    expect(get).not.toHaveBeenCalled();
  });

  it("FIL-19: Back from the pools page returns to where the admin came from", async () => {
    const user = userEvent.setup();
    renderPools({
      history: ["/admin/channels"],
      extraRoutes: <Route path="/admin/channels" element={<p>channels page</p>} />,
    });
    await user.click(await screen.findByRole("button", { name: /news clips/i }));
    expect(screen.getByRole("link", { name: "Headlines" })).toHaveAttribute(
      "href",
      "/videos/v1/watch?t=0"
    );
    await user.click(screen.getByRole("button", { name: "go back" }));
    expect(screen.getByText("channels page")).toBeInTheDocument();
  });

  it("FIL-20: a render crash shows the route error fallback; navigation still works", async () => {
    quietCrash();
    // an object where a string is drawn: "Objects are not valid as a React child"
    pools = { items: [{ id: "p1", name: {}, items: [] }] };
    const user = userEvent.setup();
    renderPools();
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "This page hit an error and could not be shown."
    );
    await user.click(screen.getByRole("link", { name: "go elsewhere" }));
    expect(screen.getByText("elsewhere")).toBeInTheDocument();
    pools = POOLS;
    await user.click(screen.getByRole("button", { name: "go back" }));
    expect(await screen.findByRole("button", { name: /news clips/i })).toBeInTheDocument();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("FIL-24: labels, expanded state and the search listbox", async () => {
    const user = userEvent.setup();
    renderPools();
    await screen.findByRole("button", { name: /news clips/i });
    expect(screen.getByLabelText("Search pools")).toBeInTheDocument();
    expect(screen.getByLabelText("Pool name")).toBeInTheDocument();
    const news = toggle(/news clips/i);
    expect(news.tagName).toBe("BUTTON");
    news.focus();
    await user.keyboard("{Enter}");
    expect(news).toHaveAttribute("aria-expanded", "true");
    await user.keyboard(" ");
    expect(news).toHaveAttribute("aria-expanded", "false");
    await user.keyboard("{Enter}");
    expect(screen.getByLabelText("Kind")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Remove Headlines" })).toHaveTextContent("✕");
    const list = await screen.findByRole("listbox", { name: "Add a clip from the library" });
    expect(within(list).getAllByRole("option")).toHaveLength(1);
    expect(within(list).getByRole("button", { name: /station ident/i })).toBeInTheDocument();
  });
});
