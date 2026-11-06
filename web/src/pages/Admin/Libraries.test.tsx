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
import { likeNameKey } from "./libraries/YouTubeState";
import { axeViolations } from "../../test/axe";
import { renderPage, memberUser, adminUser } from "../../test/pageHarness";
import { routeGet } from "../../test/queueHarness";
import type { User } from "../../lib/auth";
import en from "../../i18n/en.json";
import ar from "../../i18n/ar.json";

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
      problem: null,
    },
  ],
  settings: { language: "ar" },
  created_at: "2026-10-17T09:00:00Z",
  updated_at: "2026-10-17T09:00:00Z",
  counts: counts(47273, 3),
  last_scan: {
    job_id: "j-old",
    state: "done",
    created_at: "2026-10-20T10:00:00Z",
    finished_at: "2026-10-20T10:05:00Z",
    error: null,
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
      local_path: "/Volumes/Originals/Backup/Youtube/Othman",
      reachable: false,
      problem: "not-found",
    },
  ],
  settings: {},
  created_at: "2026-10-17T09:00:00Z",
  updated_at: "2026-10-17T09:00:00Z",
  counts: counts(1),
  last_scan: null,
  scanning: false,
  conflicts: [
    {
      root: "/Volumes/Originals/Backup/Youtube/Othman",
      other_root: "/Volumes/Originals/Backup/Youtube/Othman",
      library_id: "big",
      library_name: "Dr. Othman Alkamees",
      kind: "same",
      other_count: 47273,
    },
  ],
};
const DATA = {
  items: [BIG, SMALL],
  conflict_groups: [
    {
      libraries: [
        { id: "big", name: "Dr. Othman Alkamees", videos: 47273 },
        { id: "small", name: "Othman (dup)", videos: 1 },
      ],
      kind: "same",
      suggested_target: "big",
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
  grants: 2,
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
    // the name opens the library's own admin page
    expect(within(big).getByRole("link", { name: /Dr\. Othman Alkamees/ })).toHaveAttribute(
      "href",
      "/admin/libraries/big"
    );
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
    expect(within(panel).getByText("Same folder")).toBeInTheDocument();
    expect(within(panel).getByText("/Volumes/Originals/Backup/Youtube/Othman")).toHaveAttribute(
      "dir",
      "ltr"
    );
    expect(
      within(panel).getByRole("button", { name: /Merge into .*Dr\. Othman Alkamees/ })
    ).toBeInTheDocument();
  });

  // Epic 56 stories 56.07 T07 and 56.08 T04: the collection mode and the Plex import
  it("sets a library's collection mode and lists the Plex items not in Maktaba", async () => {
    const big = {
      ...BIG,
      collection_mode: "tab_only",
      plex_collections: { imported: 2, missing_videos: 1 },
    };
    open({ ...DATA, items: [big, SMALL] }, adminUser, {
      "/api/libraries/big/plex-missing": {
        items: [
          { collection: "Favourites", rating_key: "199", title: "Not here", file: "/x/not.mp4" },
        ],
      },
    });
    patch.mockResolvedValue({});
    await screen.findByRole("heading", { level: 2, name: "Dr. Othman Alkamees" });
    const c = card("Dr. Othman Alkamees");
    expect(
      within(c).getByText("Plex collections: 2 imported, 1 videos not in Maktaba")
    ).toBeInTheDocument();
    const user = userEvent.setup();
    await user.selectOptions(
      within(c).getByRole("combobox", { name: en["libraryAdmin.collectionMode"] }),
      "hide_items"
    );
    expect(patch).toHaveBeenCalledWith("/api/libraries/big", { collection_mode: "hide_items" });
    await user.click(within(c).getByRole("button", { name: en["libraryAdmin.plex.skipped"] }));
    expect(await within(c).findByText("Not here")).toBeInTheDocument();
  });

  it("the collection mode's words are Arabic in Arabic", () => {
    expect(ar["libraryAdmin.collectionMode.hideItems"]).toBe("اعرض المجموعة بدل مقاطعها");
    expect(ar["libraryAdmin.plex.collections"]).toContain("{n}");
  });

  it("follows a scan already running after a reload", async () => {
    open({
      items: [
        {
          ...BIG,
          scanning: true,
          last_scan: {
            job_id: "j1",
            state: "running",
            created_at: "2026-10-26T10:00:00Z",
            finished_at: null,
            error: null,
          },
        },
        SMALL,
      ],
      conflict_groups: [],
    });
    const big = await screen.findByRole("heading", { level: 2, name: "Dr. Othman Alkamees" });
    const li = big.closest("li") as HTMLElement;
    // the scan is picked up in an effect after the first render: wait for it
    expect(await within(li).findByRole("button", { name: "Scanning…" })).toBeDisabled();
    expect(await within(li).findByRole("status")).toHaveTextContent(
      "Looking for new and changed files"
    );
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
        if (!path)
          return {
            places: [
              {
                name: "Originals",
                path: "/Volumes/Originals",
                local_path: "/Users/me/nas-nfs/Originals",
              },
            ],
          };
        const local = path.replace("/Volumes/Originals", "/Users/me/nas-nfs/Originals");
        if (path === "/Volumes/Originals")
          return {
            path,
            local_path: local,
            parent: "/Volumes",
            entries: [{ name: "Lectures", path: "/Volumes/Originals/Lectures" }],
            truncated: false,
          };
        return {
          path,
          local_path: local,
          parent: "/Volumes/Originals",
          entries: [],
          truncated: false,
        };
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
    post.mockResolvedValue({
      moved: 0,
      duplicates_removed: 1,
      roots_added: 0,
      into: { ...BIG, roots: BIG.roots.map((r) => r.path) },
    });
    const panel = await screen.findByRole("region", { name: "Folders claimed twice" });
    await user.click(within(panel).getByRole("button", { name: /Merge into/ }));
    const dialog = screen.getByRole("dialog", { name: /Merge .*Othman \(dup\)/ });
    expect(within(dialog).getByLabelText("Merge into")).toHaveValue("big");
    const impact = await within(dialog).findByRole("list", { name: "What goes with it" });
    expect(impact).toHaveTextContent("3 watch records");
    expect(within(dialog).getByText(/2 people lose access/)).toBeInTheDocument();
    const go = within(dialog).getByRole("button", { name: "Merge" });
    expect(go).toBeDisabled();
    const confirm = within(dialog).getByLabelText(/^Type .*Othman \(dup\).* to confirm/);
    await user.type(confirm, "othman (dup)");
    expect(go).toBeDisabled();
    // the name to type is shown whole, with Copy
    expect(within(dialog).getByText("Othman (dup)", { selector: "code" })).toBeInTheDocument();
    expect(within(dialog).getByRole("button", { name: "Copy" })).toBeInTheDocument();
    // the library merged into is the wrong name, and the dialog says so
    await user.clear(confirm);
    await user.type(confirm, BIG.name);
    expect(go).toBeDisabled();
    expect(within(dialog).getByRole("status")).toHaveTextContent("the library you're merging into");
    // spaces don't count against it
    await user.clear(confirm);
    await user.type(confirm, "  Othman   (dup) ");
    expect(go).toBeEnabled();
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
    expect(within(dialog).getByText(/2 people lose access/)).toBeInTheDocument();
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

  // the Queues page's dialog (components/queue/YouTubeSourcesDialog), for that library
  for (const locale of ["en", "ar"] as const) {
    it(`opens the YouTube sources of a library (${locale})`, async () => {
      const cat = (locale === "ar" ? ar : en) as Record<string, string>;
      if (locale === "ar") localStorage.setItem("mkt:locale", "ar");
      try {
        const user = userEvent.setup();
        const withSource = {
          ...BIG,
          youtube: { sources: 2, on: 1, source: null, guess: null },
        };
        open({ ...DATA, items: [withSource, SMALL] }, adminUser, {
          "/api/libraries/small/youtube/sources": { items: [] },
        });
        await screen.findByRole("heading", { level: 2, name: "Othman (dup)" });
        // a library with sources has YouTube sources…; one with none, Add YouTube source
        expect(
          within(card("Dr. Othman Alkamees")).getByRole("button", {
            name: cat["queue.offline.sources"],
          })
        ).toBeInTheDocument();
        await user.click(
          within(card("Othman (dup)")).getByRole("button", { name: cat["libraryAdmin.yt.add"] })
        );
        const title = cat["queue.yt.title"]!.replace("{name}", "");
        const dialog = await screen.findByRole("dialog", { name: new RegExp(title) });
        expect(dialog).toHaveAccessibleName(expect.stringContaining("Othman (dup)"));
        expect(await within(dialog).findByText(cat["queue.yt.none"]!)).toBeInTheDocument();
        expect(get).toHaveBeenCalledWith("/api/libraries/small/youtube/sources");
        expect(get).not.toHaveBeenCalledWith("/api/libraries/big/youtube/sources");
      } finally {
        localStorage.removeItem("mkt:locale");
      }
    });
  }

  it("shows the empty state", async () => {
    open({ items: [], conflict_groups: [] });
    expect(await screen.findByText("No libraries yet")).toBeInTheDocument();
  });
});

// slot 0175: each library's YouTube on its card, from the one manage read
const SRC = "5a000000-0000-4000-8000-0000000000c1";
const CHAN = "UC" + "q".repeat(22);
function lib(id: string, name: string, youtube: unknown) {
  return {
    ...SMALL,
    id,
    name,
    roots: [{ path: `/media/${id}`, local_path: `/media/${id}`, reachable: true, problem: null }],
    conflicts: [],
    youtube,
  };
}
const NONE = { sources: 0, on: 0, source: null, guess: null };
const ONE_OFF = {
  sources: 1,
  on: 0,
  source: {
    id: SRC,
    kind: "channel",
    external_id: CHAN,
    url: `https://www.youtube.com/channel/${CHAN}`,
    mode: "off",
    last_on_mode: "new_only",
  },
  guess: null,
};
const GUESS = {
  kind: "channel",
  external_id: CHAN,
  url: `https://www.youtube.com/channel/${CHAN}`,
  title: "Weekly Lessons",
  evidence: "filename",
  sample_youtube_id: "vvvvvvvvvv1",
  ids_found: 8,
  ids_matched: 8,
  looked_at: "2026-11-01T10:00:00Z",
  followed_by: [] as { id: string; name: string }[],
};

describe("a library's YouTube on its card", () => {
  beforeEach(() => {
    for (const f of [get, post, put, patch, del]) f.mockReset();
  });
  afterEach(() => vi.restoreAllMocks());
  const manage = (...items: unknown[]) => ({ items, conflict_groups: [] });

  it("one source: its state and a switch; on sends {on: true} and reloads", async () => {
    const user = userEvent.setup();
    open(manage(lib("l1", "Garden talks", ONE_OFF)));
    patch.mockResolvedValue({
      source: { ...ONE_OFF.source, mode: "new_only" },
      refresh: { outcome: "inserted" },
    });
    await screen.findByRole("heading", { level: 2, name: "Garden talks" });
    const c = card("Garden talks");
    expect(within(c).getByText("YouTube: 1 source · off")).toBeInTheDocument();
    const sw = within(c).getByRole("switch", { name: `Downloads ${CHAN}` });
    expect(sw).not.toBeChecked();
    expect(sw).toHaveAttribute("aria-checked", "false");
    expect(within(c).getByRole("button", { name: "YouTube sources…" })).toBeInTheDocument();
    const reads = get.mock.calls.filter((x) => x[0] === "/api/libraries/manage").length;
    await user.click(sw);
    expect(patch).toHaveBeenCalledWith(`/api/libraries/l1/youtube/sources/${SRC}`, { on: true });
    expect(await within(c).findByRole("status")).toHaveTextContent("Turned on");
    await waitFor(() =>
      expect(get.mock.calls.filter((x) => x[0] === "/api/libraries/manage").length).toBe(reads + 1)
    );
  });

  it("several sources: a summary and the Sources button, no switch", async () => {
    open(manage(lib("l2", "Evening circle", { sources: 3, on: 1, source: null, guess: null })));
    await screen.findByRole("heading", { level: 2, name: "Evening circle" });
    const c = card("Evening circle");
    expect(within(c).getByText("YouTube: 3 sources · 1 on")).toBeInTheDocument();
    expect(within(c).queryByRole("switch")).toBeNull();
    expect(within(c).getByRole("button", { name: "YouTube sources…" })).toBeInTheDocument();
  });

  it("none: Add YouTube source, and Look for its channel asks the API", async () => {
    const user = userEvent.setup();
    open(manage(lib("l3", "Morning notes", NONE)));
    post.mockResolvedValue({ ...GUESS, library_id: "l3" });
    await screen.findByRole("heading", { level: 2, name: "Morning notes" });
    const c = card("Morning notes");
    expect(within(c).getByText("YouTube: no source")).toBeInTheDocument();
    expect(within(c).getByRole("button", { name: "Add YouTube source" })).toBeInTheDocument();
    expect(within(c).queryByRole("button", { name: "YouTube sources…" })).toBeNull();
    // the page load asked nothing of YouTube
    expect(post).not.toHaveBeenCalled();
    await user.click(within(c).getByRole("button", { name: "Look for its channel" }));
    expect(post).toHaveBeenCalledWith("/api/libraries/l3/youtube/guess");
    expect(await within(c).findByRole("status")).toHaveTextContent("Found the channel");
  });

  it("a guess: Add as source adds it off; a library that follows it offers the merge", async () => {
    const user = userEvent.setup();
    const linked = lib("l5", "Weekly Lessons", ONE_OFF);
    const guessed = lib("l4", "Lessons copy", {
      ...NONE,
      guess: { ...GUESS, followed_by: [{ id: "l5", name: "Weekly Lessons" }] },
    });
    open(manage(guessed, linked));
    post.mockResolvedValue({ source: { id: "new" }, refresh: null });
    await screen.findByRole("heading", { level: 2, name: "Lessons copy" });
    const c = card("Lessons copy");
    expect(within(c).getByText(/Looks like channel .*Weekly Lessons/)).toBeInTheDocument();
    expect(within(c).getByText("(8 of 8 videos, from their file names)")).toBeInTheDocument();
    await user.click(within(c).getByRole("button", { name: "Add as source" }));
    expect(post).toHaveBeenCalledWith("/api/libraries/l4/youtube/sources", {
      url: `https://www.youtube.com/channel/${CHAN}`,
      mode: "off",
    });
    expect(await within(c).findByRole("status")).toHaveTextContent("Added as a source, off.");

    expect(within(c).getByText(/already follows this channel/)).toBeInTheDocument();
    await user.click(within(c).getByRole("button", { name: /Merge into .*Weekly Lessons/ }));
    const dialog = await screen.findByRole("dialog", { name: /Merge/ });
    expect(within(dialog).getByLabelText("Merge into")).toHaveValue("l5");
  });

  // slot 0182: the sources Maktaba derives from the linked videos
  it("a derived source says so, off; a derived clash is the duplicate hint, with no Add", async () => {
    const derived = lib("l9", "Reciter one", {
      ...ONE_OFF,
      source: { ...ONE_OFF.source, kind: "playlist", derived: true },
    });
    const holder = lib("l10", "Reciters", ONE_OFF);
    const clash = lib("l11", "Reciters again", {
      ...NONE,
      guess: {
        ...GUESS,
        title: "Reciters",
        evidence: "linked",
        ids_found: 1,
        ids_matched: 1,
        derived: true,
        followed_by: [{ id: "l10", name: "Reciters" }],
      },
    });
    open(manage(derived, holder, clash));
    await screen.findByRole("heading", { level: 2, name: "Reciter one" });
    const d = card("Reciter one");
    expect(within(d).getByText("Found from its linked videos")).toBeInTheDocument();
    expect(within(d).getByRole("switch", { name: `Downloads ${CHAN}` })).not.toBeChecked();
    expect(within(card("Reciters")).queryByText("Found from its linked videos")).toBeNull();
    const c = card("Reciters again");
    expect(within(c).getByText("(1 of 1 videos, from their links to YouTube)")).toBeInTheDocument();
    expect(within(c).queryByRole("button", { name: "Add as source" })).toBeNull();
    expect(within(c).getByText(/already follows this channel/)).toBeInTheDocument();
    expect(within(c).getByRole("button", { name: /Merge into .*Reciters/ })).toBeInTheDocument();
  });

  it("a guess that found nothing says so and offers to look again", async () => {
    open(
      manage(
        lib("l6", "Old tapes", {
          ...NONE,
          guess: {
            ...GUESS,
            kind: null,
            external_id: null,
            url: null,
            title: null,
            ids_matched: 0,
          },
        })
      )
    );
    await screen.findByRole("heading", { level: 2, name: "Old tapes" });
    const c = card("Old tapes");
    expect(within(c).getByText("No channel found from its files.")).toBeInTheDocument();
    expect(within(c).getByRole("button", { name: "Look again" })).toBeInTheDocument();
    expect(within(c).queryByRole("button", { name: "Add as source" })).toBeNull();
  });

  it("a like-named library with a source is flagged as maybe the same; no axe violations", async () => {
    open(manage(lib("l7", "Weekly Lessons archive", NONE), lib("l8", "weekly lessons", ONE_OFF)));
    await screen.findByRole("heading", { level: 2, name: "Weekly Lessons archive" });
    const c = card("Weekly Lessons archive");
    expect(within(c).getByText(/May be the same as .*weekly lessons/)).toBeInTheDocument();
    expect(
      within(c).getByRole("button", { name: /Merge into .*weekly lessons/ })
    ).toBeInTheDocument();
    // the one with the source is not flagged back
    expect(within(card("weekly lessons")).queryByText(/May be the same/)).toBeNull();
    expect(await axeViolations(document.body)).toEqual([]);
  });

  it("Import from Tartube: the path is offered, editable, and the report lists every kind", async () => {
    const user = userEvent.setup();
    let state: unknown = {
      job_id: null,
      state: null,
      error: null,
      created_at: null,
      finished_at: null,
      path: null,
      report: null,
      suggested_path: "/media/Youtube/tartube.db",
    };
    open(manage(lib("l9", "Garden talks", NONE)), adminUser, {
      "/api/queues/youtube-tartube": () => state,
    });
    post.mockImplementation(() => {
      state = {
        job_id: "77",
        state: "done",
        error: null,
        created_at: "2026-11-02T10:00:00Z",
        finished_at: "2026-11-02T10:01:00Z",
        path: "/media/Youtube/tartube_db_export.json",
        suggested_path: "/media/Youtube/tartube.db",
        report: {
          entries: 5,
          created: [
            { name: "Garden talks", library: "Garden talks", library_id: "l9", kind: "channel" },
          ],
          existing: [],
          skipped: [
            {
              name: "A search",
              url: "https://www.youtube.com/results?search_query=x",
              reason: "search",
            },
          ],
          not_in_maktaba: [
            { name: "Elsewhere", kind: "channel", url: "https://www.youtube.com/@elsewhere" },
          ],
          counts: {
            created: 1,
            existing: 0,
            skipped: 1,
            not_in_maktaba: 1,
            seeded: 4,
            downloaded: 4,
          },
        },
      };
      return Promise.resolve({ job_id: 77, outcome: "inserted" });
    });
    await screen.findByRole("heading", { level: 2, name: "Garden talks" });
    await user.click(screen.getByRole("button", { name: "Import from Tartube…" }));
    const dialog = await screen.findByRole("dialog", {
      name: "Import YouTube sources from Tartube",
    });
    const field = within(dialog).getByLabelText("Tartube database or export on the home");
    await waitFor(() => expect(field).toHaveValue("/media/Youtube/tartube.db"));
    await user.clear(field);
    await user.type(field, "/media/Youtube/tartube_db_export.json");
    await user.click(within(dialog).getByRole("button", { name: "Import" }));
    expect(post).toHaveBeenCalledWith("/api/queues/youtube-tartube", {
      path: "/media/Youtube/tartube_db_export.json",
    });
    expect(
      await within(dialog).findByText(
        /1 added \(off\) · 0 already there · 1 skipped · 1 not in Maktaba yet/
      )
    ).toBeInTheDocument();
    expect(within(dialog).getByText("Not in Maktaba yet (1)")).toBeInTheDocument();
    expect(within(dialog).getByText("Elsewhere")).toBeInTheDocument();
    expect(within(dialog).getByText(/a YouTube search, not a channel/)).toBeInTheDocument();
    // no library was made: the page has the one it had
    expect(screen.getAllByRole("heading", { level: 2 })).toHaveLength(1);
  });
});

describe("helpers", () => {
  it("crumbs splits an absolute path, and keeps another form whole", () => {
    expect(crumbs("/Volumes/A").map((c) => c.path)).toEqual(["/", "/Volumes", "/Volumes/A"]);
    expect(crumbs("smb://nas/share")).toEqual([
      { name: "smb://nas/share", path: "smb://nas/share" },
    ]);
  });

  it("likeNameKey drops case, Arabic marks, tatweel and punctuation", () => {
    expect(likeNameKey("Weekly  Lessons!")).toBe("weekly lessons");
    expect(likeNameKey("الدُّروسُ الأسبوعيـة")).toBe(likeNameKey("الدروس الاسبوعية"));
  });

  it("cleanRoots drops blanks and repeats and keeps each one's row", () => {
    expect(cleanRoots([" /a ", "", "/b", "/a"])).toEqual({ roots: ["/a", "/b"], rowOf: [0, 2] });
  });
});
