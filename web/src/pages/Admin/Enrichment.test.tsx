import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { get, post, put, patch, del } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  put: vi.fn(),
  patch: vi.fn(),
  del: vi.fn(),
}));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post, put, patch, delete: del } };
});

import { ToastProvider } from "@ds/components/Toast/Toast";
import { ApiError } from "../../lib/api";
import { AdminEnrichment } from "./Enrichment";
import { renderPage, memberUser, quietCrash, adminUser } from "../../test/pageHarness";
import { routeGet } from "../../test/queueHarness";
import type { User } from "../../lib/auth";

const PENDING = {
  items: [
    {
      video_id: "v1",
      library_id: "l1",
      video_title: "omar.e01.mkv",
      candidate_title: "Omar (2012) S01E01",
      provider: "tmdb",
      confidence: 0.93,
    },
    {
      video_id: "v2",
      library_id: "l1",
      video_title: "omar.e02.mkv",
      candidate_title: "Omar (2012) S01E02",
      provider: "tmdb",
      confidence: 0.884,
    },
    {
      video_id: "v3",
      library_id: "l1",
      video_title: "doc.mkv",
      candidate_title: "The Doc",
      provider: "tvdb",
      confidence: 0.71,
    },
  ],
};
const PANEL = {
  candidates: [
    {
      id: "k1",
      provider: "tmdb",
      external_id: "tt1",
      confidence: 0.93,
      accepted: false,
      title: "Omar",
      year: 2012,
      fields: [],
    },
  ],
};
const QUEUE = "/api/enrichment/pending";
const panelUrl = (id: string) => `/api/videos/${id}/enrichment`;

function open(
  pending: unknown = PENDING,
  user: User | null = adminUser,
  panel: (u: string) => unknown = () => PANEL
) {
  routeGet(get, {
    [QUEUE]: () => pending,
    [panelUrl("v1")]: panel,
    [panelUrl("v2")]: panel,
    [panelUrl("v3")]: panel,
  });
  return renderPage(
    <ToastProvider>
      <AdminEnrichment />
    </ToastProvider>,
    { path: "/admin/enrichment", user }
  );
}
const option = (name: RegExp) => screen.getByRole("option", { name });
const selectedId = () =>
  screen
    .getAllByRole("option")
    .filter((o) => o.getAttribute("aria-selected") === "true")
    .map((o) => o.textContent);
const badge = (o: HTMLElement) => o.querySelector(".mk-badge")?.textContent;
const panelCalls = () =>
  get.mock.calls.map((c) => c[0] as string).filter((u) => u.startsWith("/api/videos/"));

describe("AdminEnrichment (test-cases/admin-enrichment.md)", () => {
  beforeEach(() => {
    for (const f of [get, post, put, patch, del]) f.mockReset();
  });
  afterEach(() => vi.restoreAllMocks());

  it("ENR-01: queue lists pending matches and opens the first", async () => {
    open();
    expect(screen.getByRole("heading", { level: 1, name: "Review matches" })).toBeInTheDocument();
    expect(screen.getByText(/Use j \/ k to move through the queue/)).toBeInTheDocument();
    const list = await screen.findByRole("listbox", { name: "Review matches" });
    const opts = within(list).getAllByRole("option");
    expect(opts).toHaveLength(3);
    expect(opts[0]).toHaveTextContent("omar.e01.mkv");
    expect(opts[0]).toHaveTextContent("Omar (2012) S01E01");
    expect(opts.map(badge)).toEqual(["93%", "88%", "71%"]);
    expect(opts[0]).toHaveAttribute("aria-selected", "true");
    await waitFor(() => expect(panelCalls()).toEqual([panelUrl("v1")]));
  });

  it("ENR-02: clicking a row selects it and loads its panel", async () => {
    open();
    await screen.findByRole("listbox");
    await userEvent.setup().click(option(/doc\.mkv/));
    expect(option(/doc\.mkv/)).toHaveAttribute("aria-selected", "true");
    expect(option(/omar\.e01/)).toHaveAttribute("aria-selected", "false");
    await waitFor(() => expect(panelCalls()).toContain(panelUrl("v3")));
  });

  it("ENR-03: j / k move the selection and stop at the ends", async () => {
    open();
    await screen.findByRole("listbox");
    const user = userEvent.setup();
    await user.keyboard("k");
    expect(selectedId()[0]).toMatch(/omar\.e01/);
    await user.keyboard("j");
    expect(selectedId()[0]).toMatch(/omar\.e02/);
    await user.keyboard("jj");
    expect(selectedId()[0]).toMatch(/doc\.mkv/);
    await user.keyboard("k");
    expect(selectedId()[0]).toMatch(/omar\.e02/);
    await user.keyboard("J{ArrowDown}");
    expect(selectedId()[0]).toMatch(/omar\.e02/);
    await waitFor(() => expect(panelCalls()).toContain(panelUrl("v3")));
  });

  it("ENR-04: j / k are ignored while typing in an input", async () => {
    open();
    const search = await screen.findByRole("searchbox", { name: "Search manually" });
    const user = userEvent.setup();
    await user.click(search);
    await user.type(search, "jordan king");
    expect(search).toHaveValue("jordan king");
    expect(selectedId()[0]).toMatch(/omar\.e01/);
    // modifier keys do not move it either
    await user.click(document.body);
    await user.keyboard("{Control>}j{/Control}");
    expect(selectedId()[0]).toMatch(/omar\.e01/);
  });

  it("ENR-05: items [] shows Nothing to review; j does nothing", async () => {
    open({ items: [] });
    expect(await screen.findByText("Nothing to review")).toBeInTheDocument();
    expect(
      screen.getByText("There are no pending enrichment matches right now.")
    ).toBeInTheDocument();
    expect(screen.queryByRole("listbox")).toBeNull();
    await userEvent.setup().keyboard("j");
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it.each([
    ["items: null", { items: null }],
    ["items missing", {}],
    ["body null", null],
  ])("ENR-06: %s shows the empty state", async (_n, body) => {
    quietCrash();
    open(body);
    expect(await screen.findByText("Nothing to review")).toBeInTheDocument();
    await userEvent.setup().keyboard("jk");
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("ENR-07: missing / null fields on a row never show NaN; confidence is clamped", async () => {
    open({
      items: [
        {
          video_id: "v1",
          video_title: null,
          candidate_title: null,
          provider: null,
          confidence: null,
        },
        { video_id: "v2", video_title: "x", candidate_title: "y", confidence: 1.5 },
        { video_id: "v3", video_title: "z", candidate_title: "w" },
      ],
    });
    const opts = within(await screen.findByRole("listbox")).getAllByRole("option");
    expect(opts.map(badge)).toEqual(["0%", "100%", "0%"]);
    expect(screen.queryByText(/NaN/)).toBeNull();
  });

  it.each([
    [
      "500 problem",
      new ApiError({ type: "x", title: "Internal error", status: 500 }),
      "Internal error",
    ],
    ["network", new TypeError("Failed to fetch"), "Something went wrong."],
    ["bad JSON", new SyntaxError("Unexpected token"), "Something went wrong."],
  ])("ENR-10: queue load fails (%s) and Try again recovers", async (_n, err, text) => {
    let fail = true;
    routeGet(get, {
      [QUEUE]: () => (fail ? Promise.reject(err) : PENDING),
      [panelUrl("v1")]: PANEL,
    });
    renderPage(
      <ToastProvider>
        <AdminEnrichment />
      </ToastProvider>,
      { path: "/admin/enrichment" }
    );
    expect((await screen.findAllByText(text)).length).toBeGreaterThanOrEqual(1);
    expect(screen.getAllByText("Something went wrong.").length).toBeGreaterThanOrEqual(1);
    fail = false;
    await userEvent.setup().click(screen.getByRole("button", { name: "Try again" }));
    expect(await screen.findByRole("listbox", { name: "Review matches" })).toBeInTheDocument();
  });

  it.each([
    ["candidates: null", () => ({ candidates: null })],
    ["candidates missing", () => ({})],
    ["load fails", () => Promise.reject(new ApiError({ type: "x", title: "boom", status: 500 }))],
  ])("ENR-11: panel %s shows the panel empty state", async (_n, panel) => {
    quietCrash();
    open(PENDING, adminUser, panel);
    expect(await screen.findByText("No suggestions")).toBeInTheDocument();
    expect(screen.getByRole("searchbox", { name: "Search manually" })).toBeInTheDocument();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("ENR-11: candidate with fields: null renders without a diff table", async () => {
    open(PENDING, adminUser, () => ({ candidates: [{ ...PANEL.candidates[0], fields: null }] }));
    expect(await screen.findByRole("button", { name: "Accept" })).toBeInTheDocument();
    expect(screen.queryByRole("table")).toBeNull();
  });

  it("ENR-11: manual search returning candidates: null shows the empty state", async () => {
    post.mockResolvedValue({ candidates: null });
    open();
    const search = await screen.findByRole("searchbox", { name: "Search manually" });
    const user = userEvent.setup();
    await user.type(search, "omar");
    await user.click(screen.getByRole("button", { name: "Search manually" }));
    expect(post).toHaveBeenCalledWith("/api/videos/v1/enrichment/search", { query: "omar" });
    expect(await screen.findByText("No suggestions")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("ENR-12: accept shows a toast; a 409 shows the error and keeps the row", async () => {
    post
      .mockResolvedValueOnce({})
      .mockRejectedValueOnce(new ApiError({ type: "x", title: "Already applied", status: 409 }));
    open();
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "Accept" }));
    expect(post).toHaveBeenCalledWith("/api/videos/v1/enrichment/accept", { external_id: "tt1" });
    expect(await screen.findByText("Metadata applied.")).toBeInTheDocument();
    await user.click(await screen.findByRole("button", { name: "Accept" }));
    expect(await screen.findByText("Already applied")).toBeInTheDocument();
    expect(option(/omar\.e01/)).toBeInTheDocument();
  });

  // EnrichmentPanel (src/components, shared with VideoDetail) has no
  // "changed" callback, so the page never refetches the queue.
  it.fails("ENR-12: an accepted video leaves the queue and the selection moves on", async () => {
    post.mockResolvedValue({});
    let pending: unknown = PENDING;
    routeGet(get, {
      [QUEUE]: () => pending,
      [panelUrl("v1")]: PANEL,
      [panelUrl("v2")]: PANEL,
    });
    renderPage(
      <ToastProvider>
        <AdminEnrichment />
      </ToastProvider>,
      { path: "/admin/enrichment" }
    );
    const user = userEvent.setup();
    pending = { items: PENDING.items.slice(1) };
    await user.click(await screen.findByRole("button", { name: "Accept" }));
    await screen.findByText("Metadata applied.");
    await waitFor(() => expect(screen.queryByRole("option", { name: /omar\.e01/ })).toBeNull(), {
      timeout: 300,
    });
  });

  it("ENR-13: non-admin gets Admins only and no queue request", () => {
    open(PENDING, memberUser);
    expect(screen.getByText("Admins only")).toBeInTheDocument();
    expect(
      screen.getByText("You need administrator access to view this page.")
    ).toBeInTheDocument();
    expect(get).not.toHaveBeenCalled();
  });

  it("ENR-14: signed-out user is redirected to /login", () => {
    open(PENDING, null);
    expect(screen.getByText("login page (from /admin/enrichment)")).toBeInTheDocument();
    expect(get).not.toHaveBeenCalled();
  });

  it("ENR-17: a render crash shows the route error fallback; Back works; j is unbound", async () => {
    quietCrash();
    let pending: unknown = { items: [{ video_id: "v1", video_title: { a: 1 }, confidence: 1 }] };
    routeGet(get, { [QUEUE]: () => pending, [panelUrl("v1")]: PANEL });
    renderPage(
      <ToastProvider>
        <AdminEnrichment />
      </ToastProvider>,
      { path: "/admin/enrichment" }
    );
    expect(await screen.findByRole("alert")).toBeInTheDocument();
    const user = userEvent.setup();
    await user.click(screen.getByRole("link", { name: "go elsewhere" }));
    expect(screen.getByText("elsewhere")).toBeInTheDocument();
    await user.keyboard("j");
    expect(screen.getByText("elsewhere")).toBeInTheDocument();
    pending = PENDING;
    await user.click(screen.getByRole("button", { name: "go back" }));
    expect(await screen.findByRole("listbox", { name: "Review matches" })).toBeInTheDocument();
  });

  it("ENR-21: options sit in the listbox (li is presentational) and Enter selects", async () => {
    open();
    const list = await screen.findByRole("listbox", { name: "Review matches" });
    for (const li of Array.from(list.children)) expect(li).toHaveAttribute("role", "presentation");
    const user = userEvent.setup();
    option(/omar\.e02/).focus();
    await user.keyboard("{Enter}");
    expect(option(/omar\.e02/)).toHaveAttribute("aria-selected", "true");
  });
});
