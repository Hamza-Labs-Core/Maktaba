// /admin/libraries/:id: every section of one library with its reads, the
// #section the address names, the queue actions, failing items linked to
// their items, the conversion settings saved inside the library's settings
// (other settings kept), the library's access saved as grants, an unknown
// library, a load error, the admins-only gate, Arabic and axe.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Route } from "react-router-dom";

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

import { ApiError } from "../../lib/api";
import { LibraryAdmin, LIBRARY_SECTIONS } from "./LibraryAdmin";
import { convertSettings } from "./library/sections";
import { renderPage, memberUser } from "../../test/pageHarness";
import { routeGet } from "../../test/queueHarness";
import { axeViolations } from "../../test/axe";
import { librariesFixture, snapshotFixture } from "../../test/fixtures/queues";
import { source, summary } from "../../test/fixtures/offline";
import type { User } from "../../lib/auth";

const ID = "lib-1";
const LIB = {
  id: ID,
  name: "Islamic lectures",
  roots: [
    { path: "/media/lectures", local_path: "/media/lectures", reachable: true, problem: null },
    { path: "/media/old", local_path: "/media/old", reachable: false, problem: "not-found" },
  ],
  settings: { language: "ar", convert: { allow_reencode: false, keep: 1 } },
  created_at: "2026-10-17T09:00:00Z",
  updated_at: "2026-10-17T09:00:00Z",
  counts: { total: 1840, missing: 4, failed: 2, ready: 1834, processing: 0 },
  last_scan: null,
  scanning: false,
  conflicts: [],
};
const OTHER = { ...LIB, id: "lib-2", name: "قصة وآية", settings: {}, roots: [LIB.roots[0]] };
const failure = (i: number) => ({
  job_id: 100 + i,
  stage: "link",
  subject_kind: "video",
  subject_id: `v-${i}`,
  label: `Lecture ${i}`,
  path: null,
  library_id: ID,
  library_name: LIB.name,
  state: "failed",
  status: "failing",
  message: "no match on YouTube",
  signature: null,
  class: null,
  parked_on: null,
  reason: "",
  attempts: 3,
  max_attempts: 3,
  first_failed_at: null,
  last_failed_at: null,
  missing_confirmed_at: null,
  youtube_id: null,
});
const ACL = {
  users: [
    { id: "u-1", username: "sara" },
    { id: "u-2", username: "omar" },
  ],
  libraries: [{ id: ID, name: LIB.name }],
  grants: [
    { user_id: "u-1", library_id: ID, role: "read" },
    { user_id: "u-2", library_id: "lib-2", role: "admin" },
  ],
};

function open(over: Record<string, unknown> = {}, opts: { user?: User; path?: string } = {}) {
  routeGet(get, {
    "/api/libraries/manage": { items: [LIB, OTHER], conflict_groups: [] },
    "/api/queues/libraries": { items: librariesFixture },
    [`/api/libraries/${ID}/offline-summary`]: summary({ library_id: ID }),
    "/api/queues/libraries/activity": {
      days: ["2026-11-01", "2026-11-02"],
      items: [{ library_id: ID, done: [40, 2], failed: [3, 0] }],
    },
    "/api/queues/failures": {
      items: [failure(1), failure(2)],
      next: null,
      facets: {
        library: [{ value: ID, count: 8 }],
        stage: [],
        class: [],
        signature: [],
        status: [],
      },
    },
    "/api/queues": snapshotFixture,
    [`/api/libraries/${ID}/stats`]: {
      total_videos: 1840,
      total_duration_sec: 7200 * 3,
      source_size_bytes: 5 * 2 ** 30,
      derived_size_bytes: 2 ** 30,
    },
    [`/api/libraries/${ID}/series`]: { total: 12, items: [] },
    [`/api/libraries/${ID}/youtube/sources`]: { items: [source({ library_id: ID })] },
    "/api/admin/library-acl": ACL,
    ...over,
  });
  return renderPage(<LibraryAdmin />, {
    path: opts.path ?? `/admin/libraries/${ID}`,
    route: "/admin/libraries/:id",
    user: opts.user,
    extraRoutes: (
      <>
        <Route path="/admin/libraries" element={<p>manage page</p>} />
        <Route path="/queue/items/:kind/:id" element={<p>item page</p>} />
      </>
    ),
  });
}
const section = (name: string) => screen.getByRole("region", { name });
async function ready() {
  expect(
    await screen.findByRole("heading", { level: 1, name: /Islamic lectures/ })
  ).toBeInTheDocument();
}

beforeEach(() => {
  for (const f of [get, post, put, patch, del]) f.mockReset();
});
afterEach(() => vi.restoreAllMocks());

describe("LibraryAdmin", () => {
  it("shows every section, in the page's nav, with what it reads; no axe violations", async () => {
    const { container } = open();
    await ready();
    const nav = screen.getByRole("navigation", { name: "Sections of this library" });
    expect(
      within(nav)
        .getAllByRole("link")
        .map((a) => a.getAttribute("href"))
    ).toEqual(LIBRARY_SECTIONS.map((s) => `#${s}`));
    const overview = section("Overview");
    await waitFor(() => expect(within(overview).getByText("12")).toBeInTheDocument());
    expect(within(overview).getByText("1 folders not reachable")).toBeInTheDocument();
    expect(within(overview).getByText("5 GB")).toBeInTheDocument();
    expect(within(overview).getByText("6")).toBeInTheDocument(); // hours

    const queues = section("Queue status");
    expect(await within(queues).findByText("8 failed")).toBeInTheDocument();
    expect(within(queues).getByRole("link", { name: "All 8 in Failing items" })).toHaveAttribute(
      "href",
      `/queue/failures?library=${ID}`
    );
    expect(within(queues).getByRole("link", { name: "Lecture 1" })).toHaveAttribute(
      "href",
      "/queue/items/video/v-1"
    );
    expect(
      within(queues).getByRole("link", { name: "Every queue item of this library" })
    ).toHaveAttribute("href", `/queue/items/library/${ID}`);
    expect(
      within(queues).getByRole("img", {
        name: /jobs finished per day over 2 days, 42 done and 3 failed/,
      })
    ).toBeInTheDocument();

    // YouTube sources inline, not in a dialog
    expect(
      (await within(section("YouTube sources")).findAllByText(/UCaaaaaaaaaaaaaaaaaaaaaa/)).length
    ).toBeGreaterThan(0);
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(within(section("Conversion to MP4")).getByText("37")).toBeInTheDocument();
    expect(
      within(section("Duplicates")).getByRole("link", { name: "2 duplicates to review" })
    ).toHaveAttribute("href", `/queue/duplicates?library=${ID}`);
    // four files in the trash and four videos missing from disk
    expect(within(section("Trash")).getAllByText("4")).toHaveLength(2);
    expect(within(section("Settings")).getByText("language, convert")).toBeInTheDocument();
    expect(await within(section("Access")).findByRole("combobox", { name: /sara/ })).toHaveValue(
      "read"
    );
    expect(within(section("Access")).getByRole("combobox", { name: /omar/ })).toHaveValue("none");
    expect(await axeViolations(container)).toEqual([]);
  });

  it("opens at the #section the address names", async () => {
    const scroll = vi.fn();
    Element.prototype.scrollIntoView = scroll;
    open({}, { path: `/admin/libraries/${ID}#convert` });
    await ready();
    await waitFor(() => expect(document.activeElement?.id).toBe("convert"));
    expect(scroll).toHaveBeenCalled();
  });

  it("runs the queue actions for this library", async () => {
    post.mockResolvedValue({ queued: 1, jobs: [], dry_run: true, items: [] });
    open();
    await ready();
    const queues = section("Queue status");
    await userEvent.click(within(queues).getByRole("button", { name: "Re-queue" }));
    const dlg = await screen.findByRole("dialog", { name: "Queue work" });
    expect(within(dlg).getByRole("checkbox", { name: /Islamic lectures/ })).toBeChecked();
    await userEvent.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    for (const name of ["Refresh from YouTube", "Convert to MP4", "Find duplicates"]) {
      await userEvent.click(within(queues).getByRole("button", { name }));
      const d = await screen.findByRole("dialog");
      expect(within(d).getByText(/Islamic lectures/)).toBeInTheDocument();
      await userEvent.keyboard("{Escape}");
      await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    }
  });

  it("saves the conversion settings inside the library's settings, keeping the rest", async () => {
    expect(convertSettings(undefined)).toEqual({ allow_reencode: false, auto: false });
    expect(convertSettings({ convert: [] })).toEqual({ allow_reencode: false, auto: false });
    patch.mockResolvedValue({ id: ID });
    open();
    await ready();
    const conv = section("Conversion to MP4");
    const save = within(conv).getByRole("button", { name: "Save conversion settings" });
    expect(save).toBeDisabled();
    await userEvent.click(
      within(conv).getByRole("switch", { name: "Re-encode video MP4 cannot hold" })
    );
    await userEvent.click(
      within(conv).getByRole("switch", { name: "Convert new files after each scan" })
    );
    await userEvent.click(save);
    await waitFor(() =>
      expect(patch).toHaveBeenCalledWith(`/api/libraries/${ID}`, {
        settings: { language: "ar", convert: { allow_reencode: true, keep: 1, auto: true } },
      })
    );
    expect(await screen.findByText("Conversion settings saved.")).toBeInTheDocument();
  });

  it("shows the API's refusal when the settings do not save", async () => {
    patch.mockRejectedValue(
      new ApiError({ type: "about:blank", title: "Forbidden", status: 403, detail: "admins only" })
    );
    open();
    await ready();
    const conv = section("Conversion to MP4");
    await userEvent.click(
      within(conv).getByRole("switch", { name: "Convert new files after each scan" })
    );
    await userEvent.click(within(conv).getByRole("button", { name: "Save conversion settings" }));
    expect(await screen.findByText(/admins only/)).toBeInTheDocument();
  });

  it("saves only the changed access, as grants on this library", async () => {
    put.mockResolvedValue({});
    open();
    await ready();
    const access = section("Access");
    const omar = await within(access).findByRole("combobox", { name: /omar/ });
    const save = within(access).getByRole("button", { name: /Save/ });
    expect(save).toBeDisabled();
    await userEvent.selectOptions(omar, "write");
    await userEvent.click(within(access).getByRole("button", { name: /Save/ }));
    await waitFor(() =>
      expect(put).toHaveBeenCalledWith("/api/admin/library-acl", {
        grants: [{ user_id: "u-2", library_id: ID, role: "write" }],
      })
    );
  });

  it("the settings open the library's edit, merge and remove", async () => {
    open();
    await ready();
    const settings = section("Settings");
    await userEvent.click(within(settings).getByRole("button", { name: "Edit" }));
    expect(await screen.findByRole("dialog")).toBeInTheDocument();
    await userEvent.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    await userEvent.click(within(settings).getByRole("button", { name: /Remove/ }));
    expect(await screen.findByRole("dialog")).toBeInTheDocument();
  });

  it("an unknown library says so and links back to Manage libraries", async () => {
    open({}, { path: "/admin/libraries/nope" });
    expect(await screen.findByText("Library not found")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("link", { name: "Manage libraries" }));
    expect(await screen.findByText("manage page")).toBeInTheDocument();
  });

  it("a load error offers to try again; the queue reads failing leave the rest", async () => {
    let fail = true;
    open({
      "/api/libraries/manage": () =>
        fail ? Promise.reject(new Error("500")) : { items: [LIB], conflict_groups: [] },
      "/api/queues/libraries": Promise.reject(new Error("500")),
      "/api/queues/failures": Promise.reject(new Error("500")),
    });
    await userEvent.click(await screen.findByRole("button", { name: "Try again" }));
    fail = false;
    await userEvent.click(await screen.findByRole("button", { name: "Try again" }));
    await ready();
    expect(within(section("Queue status")).getAllByText("Loading…").length).toBeGreaterThan(0);
  });

  it("is the admins' page: a member sees the admins-only message and nothing is read", async () => {
    open({}, { user: memberUser });
    expect(
      await screen.findByText("You need administrator access to view this page.")
    ).toBeInTheDocument();
    expect(get).not.toHaveBeenCalled();
  });

  it("in Arabic names the sections in Arabic, right to left", async () => {
    localStorage.setItem("mkt:locale", "ar");
    try {
      open();
      await ready();
      expect(document.documentElement.getAttribute("dir")).toBe("rtl");
      expect(screen.getByRole("navigation", { name: "أقسام هذه المكتبة" })).toBeInTheDocument();
      expect(screen.getByRole("region", { name: "نظرة عامة" })).toBeInTheDocument();
    } finally {
      localStorage.removeItem("mkt:locale");
    }
  });
});
