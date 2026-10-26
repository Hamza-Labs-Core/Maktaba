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
import { AdminLibraries } from "./Libraries";
import { crumbs } from "./libraries/FolderPicker";
import { cleanRoots } from "./libraries/LibraryForm";
import { renderPage, memberUser, adminUser } from "../../test/pageHarness";
import { routeGet } from "../../test/queueHarness";
import type { User } from "../../lib/auth";

const counts = (total: number, missing = 0) => ({
  total,
  missing,
  failed: 0,
  ready: total - missing,
  processing: 0,
});

const BIG = {
  id: "big",
  name: "Dr. Othman Alkamees",
  roots: [
    {
      path: "/Volumes/Originals/Backup/Youtube/Othman",
      local_path: "/Users/me/nas-nfs/Backup/Youtube/Othman",
      reachable: true,
    },
  ],
  settings: { language: "ar" },
  counts: counts(47273, 3),
  last_scan: {
    job_id: "j-old",
    state: "done",
    created_at: "2026-10-20T10:00:00Z",
    finished_at: "2026-10-20T10:05:00Z",
  },
  scanning: false,
  conflicts: [
    {
      root: "/Volumes/Originals/Backup/Youtube/Othman",
      other_root: "/Volumes/Originals/Backup/Youtube/Othman",
      library_id: "small",
      library_name: "Othman (dup)",
      kind: "same",
      other_count: 1,
    },
  ],
};
const SMALL = {
  id: "small",
  name: "Othman (dup)",
  roots: [
    {
      path: "/Volumes/Originals/Backup/Youtube/Othman",
      reachable: false,
      problem: "not-found",
    },
  ],
  settings: {},
  counts: counts(1),
  last_scan: null,
  scanning: false,
  conflicts: [],
};
const DATA = {
  libraries: [BIG, SMALL],
  conflict_groups: [
    {
      root: "/Volumes/Originals/Backup/Youtube/Othman",
      kind: "same",
      library_ids: ["big", "small"],
      target_id: "big",
    },
  ],
};
const IMPACT = {
  videos: 1,
  transcripts: 1,
  playback_states: 2,
  watch_sessions: 3,
  series: 0,
  collections: 0,
  channel_blocks: 0,
};

function open(data: unknown = DATA, user: User | null = adminUser, extra = {}) {
  routeGet(get, {
    "/api/libraries/manage": () => data,
    "/api/libraries/small/impact": IMPACT,
    "/api/libraries/big/impact": IMPACT,
    ...extra,
  });
  return renderPage(
    <ToastProvider>
      <AdminLibraries />
    </ToastProvider>,
    { path: "/admin/libraries", user }
  );
}
function problem(status: number, title: string, extra: Record<string, unknown> = {}) {
  return new ApiError({ type: "about:blank", title, status, ...extra });
}
const card = (name: string) =>
  screen.getByRole("heading", { level: 2, name }).closest("li") as HTMLElement;

describe("AdminLibraries", () => {
  beforeEach(() => {
    for (const f of [get, post, put, patch, del]) f.mockReset();
  });
  afterEach(() => vi.restoreAllMocks());

  it("lists libraries with folders, reachability, counts, last scan and conflicts", async () => {
    open();
    expect(
      await screen.findByRole("heading", { level: 2, name: "Dr. Othman Alkamees" })
    ).toBeInTheDocument();
    const big = card("Dr. Othman Alkamees");
    expect(within(big).getByText(/47,273 videos/)).toBeInTheDocument();
    expect(within(big).getByText(/3 missing/)).toBeInTheDocument();
    expect(within(big).getByText("Reachable")).toBeInTheDocument();
    expect(within(big).getByText("/Users/me/nas-nfs/Backup/Youtube/Othman")).toHaveAttribute(
      "dir",
      "ltr"
    );
    expect(within(big).getByText(/Last scan: .*\(finished\)/)).toBeInTheDocument();
    expect(within(big).getByText(/Also in .*Othman \(dup\)/)).toBeInTheDocument();
    const small = card("Othman (dup)");
    expect(within(small).getByText("Not reachable: not-found")).toBeInTheDocument();
    expect(within(small).getByText("Never scanned")).toBeInTheDocument();
    expect(within(small).getByRole("button", { name: /Empty trash/ })).toBeDisabled();

    const panel = screen.getByRole("region", { name: "Folders claimed twice" });
    expect(within(panel).getByText("Keeps the videos")).toBeInTheDocument();
    expect(
      within(panel).getByRole("button", { name: /Merge into .*Dr\. Othman Alkamees/ })
    ).toBeInTheDocument();
  });

  it("follows a scan already running after a reload", async () => {
    open({
      libraries: [{ ...BIG, scanning: true, last_scan: { job_id: "j1", state: "running" } }, SMALL],
      conflict_groups: [],
    });
    const big = await screen.findByRole("heading", { level: 2, name: "Dr. Othman Alkamees" });
    const li = big.closest("li") as HTMLElement;
    expect(within(li).getByRole("button", { name: "Scanning…" })).toBeDisabled();
    expect(within(li).getByRole("status")).toHaveTextContent("Looking for new and changed files");
    expect(screen.queryByRole("region", { name: "Folders claimed twice" })).toBeNull();
  });

  it("refuses non-admins without asking the API", () => {
    open(DATA, memberUser);
    expect(screen.getByText("Admins only")).toBeInTheDocument();
    expect(get).not.toHaveBeenCalled();
  });

  it("shows a load error with Try again", async () => {
    let fail = true;
    open(DATA, adminUser, {
      "/api/libraries/manage": () => {
        if (fail) throw problem(500, "boom", { detail: "database down" });
        return DATA;
      },
    });
    expect(await screen.findByText("database down")).toBeInTheDocument();
    fail = false;
    await userEvent.click(screen.getByRole("button", { name: "Try again" }));
    expect(
      await screen.findByRole("heading", { level: 2, name: "Othman (dup)" })
    ).toBeInTheDocument();
  });

  it("adds a library with a folder chosen in the picker", async () => {
    const user = userEvent.setup();
    open(DATA, adminUser, {
      "/api/libraries/folders": (url: string) => {
        const path = new URL(url, "http://x").searchParams.get("path");
        if (!path) return { places: [{ name: "Originals", path: "/Volumes/Originals" }] };
        if (path === "/Volumes/Originals")
          return {
            path,
            parent: "/Volumes",
            entries: [{ name: "Lectures", path: "/Volumes/Originals/Lectures" }],
          };
        return { path, parent: "/Volumes/Originals", entries: [] };
      },
    });
    post.mockResolvedValue({ id: "new" });
    await screen.findByRole("heading", { level: 2, name: "Othman (dup)" });
    await user.click(screen.getAllByRole("button", { name: "Add library" })[0]!);
    const dialog = screen.getByRole("dialog", { name: "Add library" });
    await user.type(within(dialog).getByLabelText(/^Name/), "Lectures");
    await user.click(within(dialog).getByRole("button", { name: "Browse…" }));
    const picker = await within(dialog).findByRole("group", { name: "Choose a folder" });
    expect(within(picker).getByRole("button", { name: "Use this folder" })).toBeDisabled();
    await user.click(await within(picker).findByRole("button", { name: /\/Volumes\/Originals/ }));
    await user.click(await within(picker).findByRole("button", { name: /Lectures/ }));
    await within(picker).findByText("No folders inside this one.");
    const trail = within(picker).getByRole("navigation", { name: "Folder path" });
    expect(within(trail).getByText("Lectures")).toHaveAttribute("aria-current", "location");
    await user.click(within(picker).getByRole("button", { name: "Use this folder" }));
    expect(within(dialog).getByLabelText(/^Folder 1/)).toHaveValue("/Volumes/Originals/Lectures");
    await user.click(within(dialog).getByRole("button", { name: "Add library" }));
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith("/api/libraries", {
        name: "Lectures",
        roots: ["/Volumes/Originals/Lectures"],
      })
    );
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  });

  it("shows a refused folder's error next to that folder", async () => {
    const user = userEvent.setup();
    open();
    post.mockRejectedValue(
      problem(422, "validation failed", {
        errors: [{ field: "roots[1]", message: "not-found" }],
      })
    );
    await screen.findByRole("heading", { level: 2, name: "Othman (dup)" });
    await user.click(screen.getAllByRole("button", { name: "Add library" })[0]!);
    const dialog = screen.getByRole("dialog", { name: "Add library" });
    await user.type(within(dialog).getByLabelText(/^Name/), "Two");
    await user.type(within(dialog).getByLabelText(/^Folder 1/), "/a");
    await user.click(within(dialog).getByRole("button", { name: "Add another folder" }));
    await user.click(within(dialog).getByRole("button", { name: "Add another folder" }));
    // folder 2 stays blank: roots[1] is the third row
    await user.type(within(dialog).getByLabelText(/^Folder 3/), "/missing");
    await user.click(within(dialog).getByRole("button", { name: "Add library" }));
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith("/api/libraries", {
        name: "Two",
        roots: ["/a", "/missing"],
      })
    );
    // the folder buttons inside the form never submit it
    expect(post).toHaveBeenCalledTimes(1);
    const third = within(dialog).getByLabelText(/^Folder 3/);
    await waitFor(() => expect(third).toHaveAccessibleDescription(/not-found/));
    expect(within(dialog).getByLabelText(/^Folder 1/)).not.toHaveAccessibleDescription(/not-found/);
  });

  it("shows an overlap refusal (no field) on top of the form", async () => {
    const user = userEvent.setup();
    open();
    post.mockRejectedValue(
      problem(422, "library roots overlap", { detail: "/a overlaps existing library root /" })
    );
    await screen.findByRole("heading", { level: 2, name: "Othman (dup)" });
    await user.click(screen.getAllByRole("button", { name: "Add library" })[0]!);
    const dialog = screen.getByRole("dialog", { name: "Add library" });
    await user.type(within(dialog).getByLabelText(/^Name/), "X");
    await user.type(within(dialog).getByLabelText(/^Folder 1/), "/a");
    await user.click(within(dialog).getByRole("button", { name: "Add library" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent(
      "/a overlaps existing library root /"
    );
  });

  it("edits the name and settings, refusing settings that are not JSON", async () => {
    const user = userEvent.setup();
    open();
    patch.mockResolvedValue({ id: "big" });
    await screen.findByRole("heading", { level: 2, name: "Dr. Othman Alkamees" });
    await user.click(within(card("Dr. Othman Alkamees")).getByRole("button", { name: "Edit" }));
    const dialog = screen.getByRole("dialog", { name: /Edit .*Dr\. Othman Alkamees/ });
    const name = within(dialog).getByLabelText(/^Name/);
    await user.clear(name);
    await user.type(name, "Othman");
    const settings = within(dialog).getByLabelText(/^Settings \(JSON\)/);
    await user.clear(settings);
    await user.type(settings, "nope");
    expect(within(dialog).getByRole("button", { name: "Save" })).toBeDisabled();
    expect(settings).toHaveAccessibleDescription(/not valid JSON/);
    await user.clear(settings);
    await user.type(settings, '{{"language": "en"}');
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() =>
      expect(patch).toHaveBeenCalledWith("/api/libraries/big", {
        name: "Othman",
        settings: { language: "en" },
      })
    );
  });

  it("merges from the conflicts panel only after the name is typed", async () => {
    const user = userEvent.setup();
    open();
    post.mockResolvedValue({ moved: 0, duplicates_removed: 1, roots_added: 0 });
    const panel = await screen.findByRole("region", { name: "Folders claimed twice" });
    await user.click(within(panel).getByRole("button", { name: /Merge into/ }));
    const dialog = screen.getByRole("dialog", { name: /Merge .*Othman \(dup\)/ });
    expect(within(dialog).getByLabelText("Merge into")).toHaveValue("big");
    const impact = await within(dialog).findByRole("list", { name: "What goes with it" });
    expect(impact).toHaveTextContent("3 watch records");
    const go = within(dialog).getByRole("button", { name: "Merge" });
    expect(go).toBeDisabled();
    const confirm = within(dialog).getByLabelText(/^Type .*Othman \(dup\).* to confirm/);
    await user.type(confirm, "othman (dup)");
    expect(go).toBeDisabled();
    await user.clear(confirm);
    await user.type(confirm, "Othman (dup)");
    await user.click(go);
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith("/api/libraries/small/merge", {
        into: "big",
        confirm: "Othman (dup)",
      })
    );
  });

  it("shows a refused merge inside the dialog", async () => {
    const user = userEvent.setup();
    open();
    post.mockRejectedValue(problem(412, "confirmation required", { detail: "name mismatch" }));
    await screen.findByRole("heading", { level: 2, name: "Othman (dup)" });
    await user.click(within(card("Othman (dup)")).getByRole("button", { name: "Merge…" }));
    const dialog = screen.getByRole("dialog", { name: /Merge/ });
    await user.type(within(dialog).getByLabelText(/to confirm/), "Othman (dup)");
    await user.click(within(dialog).getByRole("button", { name: "Merge" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent("name mismatch");
  });

  it("removes a library without ever asking to purge files", async () => {
    const user = userEvent.setup();
    open();
    del.mockResolvedValue(undefined);
    await screen.findByRole("heading", { level: 2, name: "Othman (dup)" });
    await user.click(within(card("Othman (dup)")).getByRole("button", { name: "Remove…" }));
    const dialog = screen.getByRole("dialog", { name: /Remove .*Othman \(dup\)/ });
    expect(within(dialog).getByText(/files stay on the disk/)).toBeInTheDocument();
    expect(
      await within(dialog).findByRole("list", { name: "What goes with it" })
    ).toHaveTextContent("1 videos");
    const go = within(dialog).getByRole("button", { name: "Remove library" });
    expect(go).toBeDisabled();
    await user.type(within(dialog).getByLabelText(/to confirm/), "Othman (dup)");
    await user.click(go);
    await waitFor(() => expect(del).toHaveBeenCalledTimes(1));
    expect(del.mock.calls[0]).toEqual(["/api/libraries/small"]);
    expect(String(del.mock.calls[0]![0])).not.toMatch(/purge/);
  });

  it("empties the trash of missing videos", async () => {
    const user = userEvent.setup();
    open();
    post.mockResolvedValue({ removed: 3 });
    await screen.findByRole("heading", { level: 2, name: "Dr. Othman Alkamees" });
    await user.click(
      within(card("Dr. Othman Alkamees")).getByRole("button", { name: "Empty trash (3)" })
    );
    const dialog = screen.getByRole("dialog", { name: "Empty trash" });
    expect(dialog).toHaveTextContent(/Remove the 3 missing videos/);
    await user.click(within(dialog).getByRole("button", { name: "Empty trash" }));
    await waitFor(() => expect(post).toHaveBeenCalledWith("/api/libraries/big/empty-trash"));
    expect(await screen.findByText("Removed 3 missing videos.")).toBeInTheDocument();
  });

  it("shows the empty state", async () => {
    open({ libraries: [], conflict_groups: [] });
    expect(await screen.findByText("No libraries yet")).toBeInTheDocument();
  });
});

describe("helpers", () => {
  it("crumbs splits an absolute path, and keeps another form whole", () => {
    expect(crumbs("/Volumes/A").map((c) => c.path)).toEqual(["/", "/Volumes", "/Volumes/A"]);
    expect(crumbs("smb://nas/share")).toEqual([
      { name: "smb://nas/share", path: "smb://nas/share" },
    ]);
  });

  it("cleanRoots drops blanks and repeats and keeps each one's row", () => {
    expect(cleanRoots([" /a ", "", "/b", "/a"])).toEqual({ roots: ["/a", "/b"], rowOf: [0, 2] });
  });
});
