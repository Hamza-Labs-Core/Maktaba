// /queue/libraries: one tile per library, the counted filters and the sort in
// the address, search, select and the bulk actions (Re-queue, the Epic 47
// actions), a tile's YouTube badge opening its sources, the admins-only gate,
// a load error, Arabic (right to left) and axe.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Route, useLocation } from "react-router-dom";

const { get, post, put, patch, del, hub } = vi.hoisted(() => {
  const channels = new Map<string, Set<(ev: unknown) => void>>();
  return {
    get: vi.fn(),
    post: vi.fn(),
    put: vi.fn(),
    patch: vi.fn(),
    del: vi.fn(),
    hub: {
      subscribe: (ch: string, fn: (ev: unknown) => void) => {
        if (!channels.has(ch)) channels.set(ch, new Set());
        channels.get(ch)!.add(fn);
        return () => channels.get(ch)?.delete(fn);
      },
    },
  };
});
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post, put, patch, delete: del } };
});
vi.mock("../../lib/ws", () => ({ subscribe: hub.subscribe }));

import { LibraryDashboard } from "./LibraryDashboard";
import { renderPage, memberUser } from "../../test/pageHarness";
import { routeGet } from "../../test/queueHarness";
import { axeViolations } from "../../test/axe";
import { librariesFixture, snapshotFixture } from "../../test/fixtures/queues";
import { source, summary } from "../../test/fixtures/offline";
import type { User } from "../../lib/auth";

const [lectures, stories] = librariesFixture;
const calm = { ...stories, library_id: "lib-3", name: "Archive", videos: 5000 };
const summaries = {
  items: [
    summary({ library_id: "lib-1", name: lectures.name }),
    summary({
      library_id: "lib-2",
      name: stories.name,
      youtube: { ...summary().youtube, sources_on: 0 },
      not_mp4: 0,
      duplicates: 0,
    }),
    summary({ library_id: "lib-3", name: "Archive", not_mp4: 0, duplicates: 0 }),
  ],
  cross_library_duplicates: 0,
};

function Where() {
  const { search } = useLocation();
  return <output aria-label="address">{search}</output>;
}

function open(over: Record<string, unknown> = {}, opts: { user?: User; path?: string } = {}) {
  routeGet(get, {
    "/api/queues/libraries": { items: [lectures, stories, calm] },
    "/api/queues/offline-summary": summaries,
    "/api/queues/libraries/activity": {
      days: ["2026-11-01", "2026-11-02"],
      items: [{ library_id: "lib-1", done: [5, 7], failed: [1, 0] }],
    },
    "/api/queues": snapshotFixture,
    "/api/libraries/lib-2/youtube/sources": { items: [source({ library_id: "lib-2" })] },
    ...over,
  });
  return renderPage(
    <>
      <LibraryDashboard />
      <Where />
    </>,
    {
      path: opts.path ?? "/queue/libraries",
      route: "/queue/libraries",
      user: opts.user,
      extraRoutes: <Route path="/admin/libraries/:id" element={<p>library page</p>} />,
    }
  );
}
const tiles = () =>
  screen.getAllByRole("listitem").filter((li) => li.classList.contains("mkt-ld-tile"));
const tileNames = () =>
  tiles().map((li) => within(li).getByRole("heading").textContent?.replace(/[⁨⁩]/g, ""));

beforeEach(() => {
  for (const f of [get, post, put, patch, del]) f.mockReset();
});
afterEach(() => {
  document.documentElement.removeAttribute("dir");
});

describe("LibraryDashboard", () => {
  it("draws a tile per library, the most urgent first, with counted filters; no axe violations", async () => {
    const { container } = open();
    expect(await screen.findByRole("heading", { name: "Library dashboard" })).toBeInTheDocument();
    await waitFor(() => expect(tiles()).toHaveLength(3));
    expect(tileNames()).toEqual(["Islamic lectures", "Archive", "قصة وآية"]);
    expect(
      screen.getByText("3 libraries, 7,752 videos. 8 failed items, 37 files to convert.")
    ).toBeInTheDocument();
    const filters = screen.getByRole("group", { name: "Show" });
    expect(within(filters).getByRole("button", { name: "All 3" })).toHaveAttribute(
      "aria-pressed",
      "true"
    );
    expect(within(filters).getByRole("button", { name: "Needs attention 1" })).toBeInTheDocument();
    expect(within(filters).getByRole("button", { name: "YouTube off 1" })).toBeInTheDocument();
    // the trend reached the tile that has one
    expect(within(tiles()[0]).getByText("12 done in 2 days")).toBeInTheDocument();
    expect(await axeViolations(container)).toEqual([]);
  });

  it("filters, sorts and searches, keeping the filter and the sort in the address", async () => {
    open();
    await waitFor(() => expect(tiles()).toHaveLength(3));
    await userEvent.click(screen.getByRole("button", { name: "YouTube off 1" }));
    expect(tileNames()).toEqual(["قصة وآية"]);
    expect(screen.getByText("Showing 1 of 3 libraries")).toBeInTheDocument();
    expect(screen.getByLabelText("address")).toHaveTextContent("?show=youtubeOff");
    await userEvent.click(screen.getByRole("button", { name: "All 3" }));
    await userEvent.selectOptions(screen.getByRole("combobox", { name: "Sort" }), "size");
    expect(tileNames()).toEqual(["Archive", "Islamic lectures", "قصة وآية"]);
    expect(screen.getByLabelText("address")).toHaveTextContent("?sort=size");
    await userEvent.type(screen.getByRole("searchbox", { name: "Search libraries" }), "zzz");
    expect(screen.getByText("No library matches this filter or search.")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Show all libraries" }));
    expect(tiles()).toHaveLength(3);
  });

  it("opens at the filter the address names", async () => {
    open({}, { path: "/queue/libraries?show=failing&sort=name" });
    await waitFor(() => expect(tiles()).toHaveLength(1));
    expect(tileNames()).toEqual(["Islamic lectures"]);
    expect(screen.getByRole("combobox", { name: "Sort" })).toHaveValue("name");
  });

  it("selects the shown libraries and runs the bulk actions on them", async () => {
    open();
    await waitFor(() => expect(tiles()).toHaveLength(3));
    expect(screen.getByRole("button", { name: "Re-queue selected" })).toBeDisabled();
    expect(screen.queryByRole("button", { name: /With selected/ })).toBeNull();
    await userEvent.click(screen.getByRole("button", { name: "YouTube off 1" }));
    await userEvent.click(screen.getByRole("button", { name: "Select the 1 shown" }));
    await userEvent.click(screen.getByRole("button", { name: "All 3" }));
    await userEvent.click(screen.getByRole("checkbox", { name: "Select Islamic lectures" }));
    expect(screen.getByText("2 selected")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Re-queue selected" }));
    const dlg = await screen.findByRole("dialog", { name: "Queue work" });
    expect(within(dlg).getByRole("checkbox", { name: /Islamic lectures/ })).toBeChecked();
    expect(within(dlg).getByRole("checkbox", { name: /قصة وآية/ })).toBeChecked();
    expect(within(dlg).getByRole("checkbox", { name: /Archive/ })).not.toBeChecked();
    await userEvent.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());

    await userEvent.click(screen.getByRole("button", { name: "With selected (2)" }));
    expect(screen.getAllByRole("menuitem").map((m) => m.textContent)).toEqual([
      "Refresh from YouTube",
      "Convert to MP4",
      "Find duplicates",
    ]);
    await userEvent.click(screen.getByRole("menuitem", { name: "Find duplicates" }));
    const action = await screen.findByRole("dialog");
    expect(within(action).getByText(/Islamic lectures/)).toBeInTheDocument();
    expect(within(action).getByText(/قصة وآية/)).toBeInTheDocument();
    await userEvent.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());

    await userEvent.click(screen.getByRole("button", { name: "Clear selection" }));
    expect(screen.getByText("0 selected")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Select the 3 shown" }));
    await userEvent.click(screen.getByRole("button", { name: "Unselect the shown" }));
    expect(screen.getByText("0 selected")).toBeInTheDocument();
  });

  it("a tile's YouTube badge opens its sources; its name opens the library page", async () => {
    open();
    await waitFor(() => expect(tiles()).toHaveLength(3));
    await userEvent.click(
      screen.getByRole("button", { name: "YouTube sources of قصة وآية: all 3 off. Open them" })
    );
    const dlg = await screen.findByRole("dialog");
    expect((await within(dlg).findAllByText(/UCaaaaaaaaaaaaaaaaaaaaaa/)).length).toBeGreaterThan(0);
    await userEvent.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    await userEvent.click(screen.getByRole("link", { name: /Archive/ }));
    expect(await screen.findByText("library page")).toBeInTheDocument();
  });

  it("without the summaries hides the filters that need them, and the badges", async () => {
    open({ "/api/queues/offline-summary": Promise.reject(new Error("500")) });
    await waitFor(() => expect(tiles()).toHaveLength(3));
    expect(screen.queryByRole("button", { name: /YouTube off/ })).toBeNull();
    expect(screen.queryByRole("button", { name: /To convert/ })).toBeNull();
    expect(screen.queryByRole("button", { name: /YouTube sources of/ })).toBeNull();
  });

  it("a load error offers Retry", async () => {
    let fail = true;
    open({
      "/api/queues/libraries": () => (fail ? Promise.reject(new Error("500")) : { items: [calm] }),
    });
    expect(
      await screen.findByText("The libraries could not be read. Try again.")
    ).toBeInTheDocument();
    fail = false;
    await userEvent.click(screen.getByRole("button", { name: "Try again" }));
    await waitFor(() => expect(tiles()).toHaveLength(1));
  });

  it("is the admins' page: a member sees the admins-only message and nothing is read", async () => {
    open({}, { user: memberUser });
    expect(
      await screen.findByText("You need administrator access to view this page.")
    ).toBeInTheDocument();
    expect(get).not.toHaveBeenCalled();
  });

  it("in Arabic reads right to left with Arabic words and digits", async () => {
    localStorage.setItem("mkt:locale", "ar");
    try {
      open();
      expect(await screen.findByRole("heading", { name: "لوحة المكتبات" })).toBeInTheDocument();
      await waitFor(() => expect(tiles()).toHaveLength(3));
      await act(async () => {});
      expect(document.documentElement.getAttribute("dir")).toBe("rtl");
      expect(screen.getByRole("group", { name: "عرض" })).toBeInTheDocument();
      expect(screen.getByText(/^٣ مكتبة، /)).toBeInTheDocument();
    } finally {
      localStorage.removeItem("mkt:locale");
    }
  });
});
