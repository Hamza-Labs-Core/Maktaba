// Library browser cases from web/test-cases/library.md.
// The "Load more" / cursor paging cases (LIB-16, LIB-17, LIB-18) live in
// LibraryBrowser.test.tsx.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Route, useLocation } from "react-router-dom";
import { ApiError } from "../lib/api";
import { renderPage, memberUser, adminUser, quietCrash } from "../test/pageHarness";

const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock("../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../lib/api")>("../lib/api");
  return { ...actual, api: { ...actual.api, get, post } };
});

import { ToastProvider } from "@ds/components/Toast/Toast";
import { LibraryBrowser } from "./LibraryBrowser";

type Payload = unknown | ((url: string) => unknown);

/** Routes /api/videos and /api/channels/now; a function value may throw or return a promise. */
function serve(videos: Payload, now: Payload = { server_time: "", items: [] }) {
  get.mockImplementation((url: string) => {
    const v = url.startsWith("/api/channels/now")
      ? now
      : url.startsWith("/api/videos")
        ? videos
        : undefined;
    if (v === undefined) return Promise.reject(new Error(`unmocked GET ${url}`));
    try {
      return Promise.resolve(typeof v === "function" ? (v as (u: string) => unknown)(url) : v);
    } catch (e) {
      return Promise.reject(e);
    }
  });
}

function videoCalls(): string[] {
  return get.mock.calls.map((c) => c[0] as string).filter((u) => u.startsWith("/api/videos?"));
}

function Loc() {
  const { pathname, search } = useLocation();
  return <span data-testid="loc">{pathname + search}</span>;
}

function renderLib(
  path = "/library",
  opts: { user?: typeof adminUser | null; history?: string[] } = {}
) {
  return renderPage(
    <ToastProvider>
      <LibraryBrowser />
      <Loc />
    </ToastProvider>,
    {
      path,
      route: path.startsWith("/library/") ? "/library/:libraryId" : "/library",
      user: opts.user === undefined ? memberUser : opts.user,
      history: opts.history,
    }
  );
}

const problem = (status: number, title: string) =>
  new ApiError({ type: "about:blank", title, status });

const cardTitles = () =>
  Array.from(document.querySelectorAll(".mkt-card__title")).map((e) => e.textContent);

describe("LibraryBrowser (test-cases/library.md)", () => {
  beforeEach(() => {
    get.mockReset();
    post.mockReset();
  });
  afterEach(() => vi.restoreAllMocks());

  it("LIB-01: grid shows one card per video with title, duration, language", async () => {
    serve({
      items: [
        {
          id: "v1",
          title: "Intro",
          filename: "a.mp4",
          state: "ready",
          duration_sec: 65,
          detected_language: "en",
        },
        { id: "v2", title: null, filename: "b.mp4", state: "ready", duration_sec: 3725 },
      ],
      next: null,
    });
    renderLib();
    expect(screen.getByRole("heading", { name: "All videos" })).toBeInTheDocument();
    const intro = await screen.findByRole("link", { name: /Intro/ });
    expect(intro).toHaveAttribute("href", "/videos/v1");
    expect(intro).toHaveTextContent("1:05");
    expect(intro).toHaveTextContent("· en");
    const b = screen.getByRole("link", { name: /b\.mp4/ });
    expect(b).toHaveAttribute("href", "/videos/v2");
    expect(b).toHaveTextContent("1:02:05");
    expect(document.querySelectorAll("li.mkt-card")).toHaveLength(2);
    const url = videoCalls()[0];
    expect(url).toContain("sort=created_at&order=desc");
    expect(url).toContain("limit=60");
    expect(url).not.toMatch(/library=|language=|cursor=/);
    expect(screen.queryByRole("button", { name: /load more/i })).toBeNull();
  });

  it("LIB-02: /library/:libraryId sends library=<id>, and a new id refetches", async () => {
    const A = "3f1c2a4e-0000-4000-8000-000000000001";
    const B = "3f1c2a4e-0000-4000-8000-000000000002";
    serve((url: string) =>
      url.includes(`library=${A}`)
        ? { items: [{ id: "a", filename: "from-a.mp4", state: "ready" }], next: null }
        : { items: [{ id: "b", filename: "from-b.mp4", state: "ready" }], next: null }
    );
    renderLib(`/library/${A}`, { history: [`/library/${B}`] });
    expect(await screen.findByText("from-a.mp4")).toBeInTheDocument();
    expect(videoCalls()[0]).toContain(`library=${A}`);
    await userEvent.setup().click(screen.getByRole("button", { name: "go back" }));
    expect(await screen.findByText("from-b.mp4")).toBeInTheDocument();
    expect(videoCalls().at(-1)).toContain(`library=${B}`);
    expect(screen.queryByText("from-a.mp4")).toBeNull();
  });

  it.each([
    ["items: null", { items: null, next: null }],
    ["items missing", { next: null }],
    ["items: []", { items: [], next: null }],
  ])("LIB-03: %s renders the first-run empty state", async (_label, payload) => {
    serve(payload);
    renderLib();
    expect(await screen.findByText("No videos yet")).toBeInTheDocument();
    expect(screen.getByText("Add a library and run a scan to get started.")).toBeInTheDocument();
    expect(screen.queryByRole("group", { name: "Language" })).toBeNull();
    expect(screen.queryByRole("button", { name: /load more/i })).toBeNull();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("LIB-04: null optional video fields do not crash in grid, list or sorts", async () => {
    serve({
      items: [
        {
          id: "v1",
          title: null,
          filename: "x.mp4",
          state: "ready",
          detected_language: null,
          duration_sec: null,
        },
        { id: "v2", title: "", filename: "y.mp4", state: "weird_new_state" },
      ],
      next: null,
    });
    renderLib();
    const user = userEvent.setup();
    const x = await screen.findByRole("link", { name: "x.mp4" });
    expect(x.querySelector(".mkt-card__meta")).toBeEmptyDOMElement();
    expect(screen.getByRole("link", { name: "y.mp4" })).toBeInTheDocument();
    expect(document.querySelector(".mk-badge")).toBeNull();
    await user.click(screen.getByRole("radio", { name: "List" }));
    const table = screen.getByRole("table", { name: "All videos" });
    const row = within(table).getByRole("link", { name: "x.mp4" }).closest("tr")!;
    expect(within(row).getAllByText("—")).toHaveLength(4);
    await user.selectOptions(screen.getByRole("combobox", { name: "Sort" }), "title");
    await user.selectOptions(screen.getByRole("combobox", { name: "Sort" }), "duration_sec");
    const again = await screen.findByRole("table", { name: "All videos" });
    expect(within(again).getByRole("link", { name: "y.mp4" })).toBeInTheDocument();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("LIB-04: a row with title and filename null renders and sort by Title does not throw", async () => {
    quietCrash();
    serve({
      items: [
        { id: "v1", title: null, filename: null, state: "ready" },
        { id: "v2", title: null, filename: null, state: "ready" },
        { id: "v3", title: "B", filename: "b.mp4", state: "ready" },
      ],
      next: null,
    });
    renderLib("/library?sort=title");
    await waitFor(() => expect(videoCalls()).toHaveLength(1));
    await new Promise((r) => setTimeout(r, 0));
    expect(screen.queryByRole("alert")).toBeNull();
    expect(screen.getByRole("link", { name: "B" })).toBeInTheDocument();
  });

  it("LIB-05: the grid no longer carries What's On Now (it moved to Home)", async () => {
    serve({ items: [{ id: "v1", filename: "a.mp4", state: "ready" }], next: null });
    renderLib();
    expect(await screen.findByText("a.mp4")).toBeInTheDocument();
    expect(get.mock.calls.some((c) => String(c[0]).startsWith("/api/channels/now"))).toBe(false);
    expect(screen.queryByTestId("whats-on-now-rail")).toBeNull();
  });

  it("LIB-06: admin on /library/:id sees Scan now, which POSTs the scan and says how it went", async () => {
    serve({ items: [], next: null });
    post.mockResolvedValueOnce({}).mockRejectedValueOnce(problem(500, "scan failed"));
    renderLib("/library/11111111-1111-4111-8111-111111111111", { user: adminUser });
    expect(await screen.findByText("No videos yet")).toBeInTheDocument();
    const user = userEvent.setup();
    const scan = screen.getByRole("button", { name: "Scan now" });
    await user.click(scan);
    expect(post).toHaveBeenCalledWith("/api/libraries/11111111-1111-4111-8111-111111111111/scan");
    expect(await screen.findByText("Scan started.")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Scan now" }));
    expect(post).toHaveBeenCalledTimes(2);
    expect(await screen.findByText("Could not start the scan: scan failed")).toBeInTheDocument();
    expect(screen.getByText("No videos yet")).toBeInTheDocument();
  });

  it("LIB-06: admin on /library (no id) sees no Scan now", async () => {
    serve({ items: [], next: null });
    renderLib("/library", { user: adminUser });
    expect(await screen.findByText("No videos yet")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Scan now" })).toBeNull();
  });

  it("LIB-07: non-admin never sees Scan now", async () => {
    serve({ items: [], next: null });
    renderLib("/library/11111111-1111-4111-8111-111111111111");
    expect(await screen.findByText("No videos yet")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Scan now" })).toBeNull();
    expect(post).not.toHaveBeenCalled();
  });

  it("LIB-08: a library outside the ACL (empty answer) shows an empty state, no videos", async () => {
    serve({ items: [], next: null });
    renderLib("/library/3f1c2a4e-0000-4000-8000-00000000dead");
    expect(await screen.findByText("No videos yet")).toBeInTheDocument();
    expect(document.querySelectorAll("li.mkt-card")).toHaveLength(0);
  });

  it("LIB-08: a 403 answer shows the ErrorState with the problem title", async () => {
    serve(() => Promise.reject(problem(403, "forbidden")));
    renderLib("/library/3f1c2a4e-0000-4000-8000-00000000dead");
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Something went wrong.");
    expect(alert).toHaveTextContent("forbidden");
    expect(screen.queryByText("No videos yet")).toBeNull();
  });

  it.todo(
    "LIB-08: a library without access shows a 'no access / not found' message, not the first-run text"
  );

  it("LIB-09: an unknown (valid UUID) libraryId shows the empty state", async () => {
    serve({ items: [], next: null });
    renderLib("/library/00000000-0000-4000-8000-000000000000");
    expect(await screen.findByText("No videos yet")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("LIB-09: /library/not-a-uuid shows Library not found and lists nothing", async () => {
    // The server ignores a non-UUID `library` and would return all videos.
    serve({ items: [{ id: "v1", filename: "everything.mp4", state: "ready" }], next: null });
    renderLib("/library/not-a-uuid");
    expect(await screen.findByRole("alert")).toHaveTextContent("Library not found.");
    expect(videoCalls()).toHaveLength(0);
    expect(screen.queryByText("everything.mp4")).toBeNull();
  });

  it("LIB-10: signed-out user is sent to /login and no videos are requested", () => {
    serve({ items: [], next: null });
    renderLib("/library/11111111-1111-4111-8111-111111111111?language=ar", { user: null });
    expect(
      screen.getByText(/^login page \(from \/library\/11111111-1111-4111-8111-111111111111/)
    ).toBeInTheDocument();
    expect(videoCalls()).toHaveLength(0);
  });

  it("LIB-10: the /login redirect keeps the query string in `from`", () => {
    serve({ items: [], next: null });
    renderLib("/library/11111111-1111-4111-8111-111111111111?language=ar", { user: null });
    expect(
      screen.getByText(
        "login page (from /library/11111111-1111-4111-8111-111111111111?language=ar)"
      )
    ).toBeInTheDocument();
  });

  it("LIB-11: render crash shows route error fallback; Back still works", async () => {
    quietCrash();
    let bad = true;
    serve(() =>
      bad
        ? {
            // an object where the title text goes: React cannot render it
            items: [{ id: "v1", filename: "a.mp4", title: { bad: true }, state: "ready" }],
            next: null,
          }
        : { items: [{ id: "v1", filename: "fixed.mp4", state: "ready" }], next: null }
    );
    renderLib("/library");
    expect(
      await screen.findByText("This page hit an error and could not be shown.")
    ).toBeInTheDocument();
    expect(screen.getByRole("navigation", { name: "test shell" })).toBeInTheDocument();
    const user = userEvent.setup();
    await user.click(screen.getByRole("link", { name: "go elsewhere" }));
    expect(screen.getByText("elsewhere")).toBeInTheDocument();
    bad = false;
    await user.click(screen.getByRole("button", { name: "go back" }));
    expect(await screen.findByText("fixed.mp4")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("LIB-12: loading skeleton while the list is pending", async () => {
    serve(() => new Promise(() => {}));
    renderLib();
    const busy = document.querySelector('ul[aria-busy="true"]')!;
    expect(busy).not.toBeNull();
    expect(busy.querySelectorAll("li.mkt-card")).toHaveLength(6);
    expect(screen.queryByText("No videos yet")).toBeNull();
    expect(screen.queryByRole("alert")).toBeNull();
    expect(screen.queryByRole("button", { name: /load more/i })).toBeNull();
    expect(screen.getByRole("combobox", { name: "Sort" })).toBeEnabled();
  });

  it("LIB-13: server error (500 problem) shows the ErrorState with retry", async () => {
    serve(() => Promise.reject(problem(500, "query videos failed")));
    renderLib();
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Something went wrong.");
    expect(alert).toHaveTextContent("query videos failed");
    expect(within(alert).getByRole("button", { name: "Try again" })).toBeInTheDocument();
    expect(screen.queryByText("No videos yet")).toBeNull();
  });

  it("LIB-13: an unknown ?sort= falls back to recently added instead of sending undefined", async () => {
    serve({ items: [], next: null });
    renderLib("/library?sort=bogus");
    await waitFor(() => expect(videoCalls()).toHaveLength(1));
    expect(videoCalls()[0]).toContain("sort=created_at");
  });

  it("the default sort is Recently added, newest first", async () => {
    serve({ items: [{ id: "v1", filename: "a.mp4", state: "ready" }], next: null });
    renderLib();
    await screen.findByText("a.mp4");
    const select = screen.getByRole("combobox", { name: "Sort" });
    expect(select).toHaveValue("created_at");
    expect(within(select).getByRole("option", { selected: true })).toHaveTextContent(
      "Recently added"
    );
    expect(videoCalls()[0]).toContain("sort=created_at&order=desc");
  });

  it("a search of the grid is by best match unless a sort was picked", async () => {
    serve({ items: [], next: null });
    const { unmount } = renderLib("/library?q=go");
    await waitFor(() => expect(videoCalls()).toHaveLength(1));
    const first = new URLSearchParams(videoCalls()[0].split("?")[1]);
    expect(first.get("sort")).toBe("relevance");
    expect(first.get("q")).toBe("go");
    // best match has one direction: no order is sent and there is no arrow
    expect(first.get("order")).toBeNull();
    expect(screen.getByRole("combobox", { name: "Sort" })).toHaveValue("relevance");
    expect(screen.queryByRole("button", { name: /ascending|descending/i })).toBeNull();
    unmount();

    // a sort the person picked wins over best match
    renderLib("/library?q=go&sort=title");
    await waitFor(() => expect(videoCalls()).toHaveLength(2));
    expect(videoCalls()[1]).toContain("sort=title&order=asc");
  });

  it("best match needs a search: ?sort=relevance alone is recently added", async () => {
    serve({ items: [{ id: "v1", filename: "a.mp4", state: "ready" }], next: null });
    renderLib("/library?sort=relevance");
    await screen.findByText("a.mp4");
    expect(videoCalls()[0]).toContain("sort=created_at&order=desc");
    const select = screen.getByRole("combobox", { name: "Sort" });
    expect(within(select).queryByRole("option", { name: "Best match" })).toBeNull();
  });

  it.each([
    ["network failure", new TypeError("Failed to fetch")],
    ["malformed JSON", new SyntaxError("Unexpected token n in JSON")],
  ])("LIB-14: %s shows the generic error", async (_label, err) => {
    serve(() => Promise.reject(err));
    renderLib();
    const alert = await screen.findByRole("alert");
    expect(within(alert).getAllByText("Something went wrong.")).toHaveLength(2);
    expect(within(alert).getByRole("button", { name: "Try again" })).toBeInTheDocument();
  });

  it("LIB-15: Try again re-fetches the list", async () => {
    let calls = 0;
    serve(() => {
      calls += 1;
      if (calls === 1) return Promise.reject(problem(500, "boom"));
      return { items: [{ id: "v1", filename: "a.mp4", state: "ready" }], next: null };
    });
    renderLib();
    const alert = await screen.findByRole("alert");
    await userEvent.setup().click(within(alert).getByRole("button", { name: "Try again" }));
    expect(await screen.findByText("a.mp4", {}, { timeout: 300 })).toBeInTheDocument();
    expect(videoCalls()).toHaveLength(2);
  });

  it("LIB-19: grid/list toggle persists in localStorage", async () => {
    const items = {
      items: [
        { id: "v1", filename: "a.mp4", state: "ready" },
        { id: "v2", filename: "b.mp4", state: "ready" },
      ],
      next: null,
    };
    serve(items);
    const first = renderLib();
    const user = userEvent.setup();
    await screen.findByText("a.mp4");
    expect(screen.getByRole("radio", { name: "Grid" })).toHaveAttribute("aria-checked", "true");
    await user.click(screen.getByRole("radio", { name: "List" }));
    const table = screen.getByRole("table", { name: "All videos" });
    expect(
      within(table)
        .getAllByRole("columnheader")
        .map((h) => h.textContent)
    ).toEqual(["Title", "Duration", "Language", "Watched", "State"]);
    expect(localStorage.getItem("mkt:lib:view")).toBe("list");
    first.unmount();
    renderLib();
    await screen.findByText("a.mp4");
    expect(screen.getByRole("table", { name: "All videos" })).toBeInTheDocument();
    expect(screen.getByRole("radio", { name: "List" })).toHaveAttribute("aria-checked", "true");
  });

  it("LIB-19: a throwing localStorage still renders the grid", async () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("denied");
    });
    vi.spyOn(localStorage, "getItem").mockImplementation(() => {
      throw new Error("denied");
    });
    serve({ items: [{ id: "v1", filename: "a.mp4", state: "ready" }], next: null });
    renderLib();
    expect(await screen.findByText("a.mp4")).toBeInTheDocument();
    expect(screen.getByRole("radio", { name: "Grid" })).toHaveAttribute("aria-checked", "true");
  });

  it("LIB-20: sort select and order toggle update the URL (replace) and the server's sort", async () => {
    serve({
      items: [
        { id: "a", title: "Beta", filename: "a.mp4", state: "ready", duration_sec: 10 },
        { id: "b", title: "alpha", filename: "b.mp4", state: "ready", duration_sec: 300 },
        { id: "c", title: "Gamma", filename: "c.mp4", state: "ready", duration_sec: 5 },
      ],
      next: null,
    });
    renderLib("/library", { history: ["/elsewhere"] });
    const user = userEvent.setup();
    await screen.findByText("Beta");
    expect(videoCalls()[0]).toContain("order=desc");
    await user.selectOptions(screen.getByRole("combobox", { name: "Sort" }), "title");
    expect(screen.getByTestId("loc")).toHaveTextContent(/^\/library\?sort=title$/);
    // titles start A to Z; the server sorts, the page keeps its order
    await waitFor(() => expect(videoCalls().at(-1)).toMatch(/sort=title&order=asc/));
    await waitFor(() => expect(cardTitles()).toEqual(["Beta", "alpha", "Gamma"]));
    await user.click(screen.getByRole("button", { name: "Descending" }));
    expect(screen.getByTestId("loc")).toHaveTextContent("order=desc");
    expect(screen.getByRole("button", { name: "Ascending" })).toBeInTheDocument();
    await waitFor(() => expect(videoCalls().at(-1)).toMatch(/sort=title&order=desc/));
    // a new sort starts in its own order
    await user.selectOptions(screen.getByRole("combobox", { name: "Sort" }), "duration_sec");
    expect(screen.getByTestId("loc")).not.toHaveTextContent("order=");
    await waitFor(() => expect(videoCalls().at(-1)).toMatch(/sort=duration_sec&order=desc/));
    await user.click(screen.getByRole("button", { name: "go back" }));
    expect(screen.getByText("elsewhere")).toBeInTheDocument();
  });

  it("LIB-20: every server sort is offered and sent as is", async () => {
    serve({ items: [{ id: "v1", filename: "a.mp4", state: "ready" }], next: null });
    renderLib();
    const user = userEvent.setup();
    await screen.findByText("a.mp4");
    const select = screen.getByRole("combobox", { name: "Sort" });
    const values = within(select)
      .getAllByRole("option")
      .map((o) => (o as HTMLOptionElement).value);
    // Recently added first, the default; Best match only with a search
    expect(values).toEqual([
      "created_at",
      "updated_at",
      "published_at",
      "title",
      "duration_sec",
      "plays",
    ]);
    for (const v of ["updated_at", "published_at", "plays"]) {
      await user.selectOptions(screen.getByRole("combobox", { name: "Sort" }), v);
      await waitFor(() => expect(videoCalls().at(-1)).toContain(`sort=${v}&order=desc`));
    }
  });

  it("LIB-26: year and watched filters go to the URL and the request; Clear filters clears all", async () => {
    serve((url: string) =>
      url.includes("watched=") ? { items: [], next: null } : { items: [], next: null }
    );
    renderLib("/library?year=2019&language=ar");
    const user = userEvent.setup();
    await waitFor(() => expect(videoCalls()).toHaveLength(1));
    const first = new URLSearchParams(videoCalls()[0].split("?")[1]);
    expect(first.get("year")).toBe("2019");
    expect(first.get("language")).toBe("ar");
    expect(screen.getByRole("spinbutton", { name: "Year" })).toHaveValue(2019);
    await user.selectOptions(screen.getByRole("combobox", { name: "Watched" }), "in_progress");
    expect(screen.getByTestId("loc")).toHaveTextContent("watched=in_progress");
    await waitFor(() => expect(videoCalls().at(-1)).toContain("watched=in_progress"));
    expect(videoCalls().at(-1)).toContain("year=2019");
    expect(await screen.findByText("No videos match your filters.")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Clear filters" }));
    expect(screen.getByTestId("loc")).toHaveTextContent(/^\/library$/);
    await waitFor(() => expect(videoCalls().at(-1)).not.toMatch(/year=|watched=|language=/));
  });

  it("LIB-27: under Most played a card shows its plays; under Date published its date", async () => {
    serve((url: string) => ({
      items: [
        {
          id: "v1",
          filename: "a.mp4",
          state: "ready",
          published_at: "2019-03-04T00:00:00Z",
          ...(url.includes("sort=plays") ? { plays: 12 } : {}),
        },
      ],
      next: null,
    }));
    renderLib("/library?sort=plays");
    const card = await screen.findByRole("link", { name: /a\.mp4/ });
    expect(card).toHaveTextContent("12 plays");
    expect(card).not.toHaveTextContent("2019");
    await userEvent
      .setup()
      .selectOptions(screen.getByRole("combobox", { name: "Sort" }), "published_at");
    await waitFor(() =>
      expect(screen.getByRole("link", { name: /a\.mp4/ })).not.toHaveTextContent("plays")
    );
    expect(screen.getByRole("link", { name: /a\.mp4/ })).toHaveTextContent("2019");
  });

  it("LIB-26: typing a year filters; a bad ?year= or ?watched= is not sent", async () => {
    serve({ items: [], next: null });
    renderLib("/library?year=19&watched=bogus");
    const user = userEvent.setup();
    await waitFor(() => expect(videoCalls()).toHaveLength(1));
    expect(videoCalls()[0]).not.toMatch(/year=|watched=/);
    const year = screen.getByRole("spinbutton", { name: "Year" });
    await user.clear(year);
    await user.type(year, "2021{Enter}");
    expect(screen.getByTestId("loc")).toHaveTextContent("year=2021");
    await waitFor(() => expect(videoCalls().at(-1)).toContain("year=2021"));
  });

  it("LIB-21: language chips filter; Clear filters on a filtered-out empty list", async () => {
    serve((url: string) =>
      url.includes("language=ar")
        ? { items: [], next: null }
        : {
            items: [
              { id: "v1", filename: "en.mp4", state: "ready", detected_language: "en" },
              { id: "v2", filename: "ar.mp4", state: "ready", detected_language: "ar" },
            ],
            next: null,
          }
    );
    renderLib("/library", { user: adminUser });
    const user = userEvent.setup();
    const group = await screen.findByRole("group", { name: "Language" });
    expect(
      within(group)
        .getAllByRole("button")
        .map((b) => b.textContent)
    ).toEqual(["ar", "en"]);
    await user.click(within(group).getByRole("button", { name: "ar" }));
    expect(screen.getByTestId("loc")).toHaveTextContent("language=ar");
    expect(await screen.findByText("No videos match your filters.")).toBeInTheDocument();
    expect(videoCalls().at(-1)).toContain("language=ar");
    expect(screen.queryByRole("button", { name: "Scan now" })).toBeNull();
    await user.click(screen.getByRole("button", { name: "Clear filters" }));
    expect(screen.getByTestId("loc")).not.toHaveTextContent("language=");
    expect(await screen.findByText("en.mp4")).toBeInTheDocument();
  });

  it("LIB-22: deep link with ?sort=title&order=asc&language=ar", async () => {
    serve({
      items: [
        { id: "v1", title: "ب", filename: "1.mp4", state: "ready", detected_language: "ar" },
        { id: "v2", title: "أ", filename: "2.mp4", state: "ready", detected_language: "ar" },
      ],
      next: null,
    });
    renderLib("/library?sort=title&order=asc&language=ar");
    await screen.findByText("أ");
    expect(screen.getByRole("combobox", { name: "Sort" })).toHaveValue("title");
    expect(screen.getByRole("button", { name: "Descending" })).toBeInTheDocument();
    const chip = within(screen.getByRole("group", { name: "Language" })).getByRole("button", {
      name: "ar",
    });
    expect(chip.querySelector(".mk-chip")).toHaveAttribute("data-selected", "true");
    expect(videoCalls()[0]).toContain("sort=title");
    expect(videoCalls()[0]).toContain("language=ar");
  });

  it("LIB-23: problem-state badges only for problem states", async () => {
    const states = [
      "ready",
      "transcoding",
      "missing",
      "corrupted",
      "failed",
      "ready_no_audio",
      "superseded",
    ];
    serve({
      items: states.map((s) => ({ id: s, filename: `${s}.mp4`, state: s })),
      next: null,
    });
    renderLib();
    await screen.findByText("ready.mp4");
    const badge = (name: string) => screen.getByText(name, { selector: ".mk-badge" });
    for (const name of ["Missing", "Corrupted", "Failed"]) {
      expect(badge(name)).toHaveClass("mk-badge--error");
    }
    for (const name of ["No audio", "Replaced"]) {
      expect(badge(name)).toHaveClass("mk-badge--warn");
    }
    for (const id of ["ready", "transcoding"]) {
      expect(screen.getByRole("link", { name: `${id}.mp4` }).querySelector(".mk-badge")).toBeNull();
    }
    await userEvent.setup().click(screen.getByRole("radio", { name: "List" }));
    const table = screen.getByRole("table", { name: "All videos" });
    for (const id of ["ready", "transcoding"]) {
      const row = within(table)
        .getByRole("link", { name: `${id}.mp4` })
        .closest("tr")!;
      expect(row.lastElementChild).toHaveTextContent("—");
    }
    expect(within(table).getByText("Missing")).toBeInTheDocument();
  });

  it("LIB-25: card click opens video detail; Back returns with filters and refetches", async () => {
    serve({
      items: [{ id: "v1", filename: "a.mp4", state: "ready", detected_language: "en" }],
      next: null,
    });
    renderPage(
      <>
        <LibraryBrowser />
        <Loc />
      </>,
      {
        path: "/library?sort=title&language=en",
        route: "/library",
        user: memberUser,
        extraRoutes: <Route path="/videos/:videoId" element={<p>video page</p>} />,
      }
    );
    const user = userEvent.setup();
    const link = await screen.findByRole("link", { name: /a\.mp4/ });
    expect(link).toHaveAttribute("href", "/videos/v1");
    const before = videoCalls().length;
    await user.click(link);
    expect(screen.getByText("video page")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "go back" }));
    expect(await screen.findByText("a.mp4")).toBeInTheDocument();
    expect(screen.getByTestId("loc")).toHaveTextContent("/library?sort=title&language=en");
    expect(videoCalls().length).toBe(before + 1);
    expect(videoCalls().at(-1)).toContain("sort=title");
    expect(videoCalls().at(-1)).toContain("language=en");
  });

  it("LIB-26: Arabic UI is rtl and titles are dir=auto", async () => {
    localStorage.setItem("mkt:locale", "ar");
    serve({
      items: [
        {
          id: "v1",
          title: "السيرة النبوية ( 18 )",
          filename: "a.mp4",
          state: "ready",
          duration_sec: 65,
        },
      ],
      next: null,
    });
    renderLib();
    const title = await screen.findByText("السيرة النبوية ( 18 )");
    expect(title).toHaveAttribute("dir", "auto");
    await waitFor(() => expect(document.documentElement).toHaveAttribute("dir", "rtl"));
    expect(document.documentElement).toHaveAttribute("lang", "ar");
    expect(screen.queryByRole("heading", { name: "Library" })).toBeNull();
    expect(screen.queryByRole("combobox", { name: "Sort" })).toBeNull();
  });
});
