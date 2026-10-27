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
import { StorageSettings } from "./StorageSettings";
import { renderPage, memberUser, adminUser } from "../../test/pageHarness";
import { routeGet } from "../../test/queueHarness";
import type { User } from "../../lib/auth";

const S = {
  media: {
    path_map: [{ db: "/Volumes/Originals", local: "/Users/me/nas-nfs/Originals" }],
    roots: ["/Volumes/Originals/Backup"],
  },
  server: {
    mdns: true,
    listen: ":8080",
    web_listen: ":8088",
    stream_addr: ":8090",
    public_url: "",
    web_public_url: "",
  },
};
const DOC = {
  available: true,
  file: "/Users/me/Library/Application Support/Maktaba/server.toml",
  saved: S,
  running: S,
  restart_needed: false,
  can_restart: true,
  reachability: [
    {
      library_id: "l1",
      library_name: "Lectures",
      root: "/Volumes/Originals/Lectures",
      local: "/Users/me/nas-nfs/Originals/Lectures",
      reachable: true,
    },
    {
      library_id: "l2",
      library_name: "Old",
      root: "/srv/old",
      local: "/srv/old",
      reachable: false,
      problem: "not-found",
    },
  ],
};

// the data location and backups sections have their own tests
// (DataLocationSection.test.tsx, BackupsSection.test.tsx)
const NO_STORAGE = { available: false, reason: "no supervisor" };
const NO_BACKUPS = { available: false, reason: "no supervisor" };

function open(
  doc: unknown = DOC,
  user: User | null = adminUser,
  props: { onReload?: () => void } = {}
) {
  routeGet(get, {
    "/api/server/storage": NO_STORAGE,
    "/api/server/backups": NO_BACKUPS,
    "/api/server/settings": () => doc,
    "/api/system/version": { version: "1.0.0" },
  });
  return renderPage(
    <ToastProvider>
      <StorageSettings pollMs={1} {...props} />
    </ToastProvider>,
    { path: "/settings/storage", user }
  );
}
function problem(status: number, title: string, extra: Record<string, unknown> = {}) {
  return new ApiError({ type: "about:blank", title, status, ...extra });
}

describe("StorageSettings", () => {
  beforeEach(() => {
    for (const f of [get, post, put, patch, del]) f.mockReset();
  });
  afterEach(() => vi.restoreAllMocks());

  it("shows the saved settings and which folders this machine reaches", async () => {
    open();
    expect(await screen.findByLabelText(/^Library path 1/)).toHaveValue("/Volumes/Originals");
    expect(screen.getByLabelText(/^On this computer 1/)).toHaveValue("/Users/me/nas-nfs/Originals");
    expect(screen.getByLabelText(/^On this computer 1/)).toHaveAttribute("dir", "ltr");
    expect(screen.getByLabelText(/^Media folder 1/)).toHaveValue("/Volumes/Originals/Backup");
    expect(screen.getByRole("switch", { name: /find the server/ })).toBeChecked();
    expect(screen.getByLabelText(/^Server address/)).toHaveValue(":8080");
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
    expect(screen.queryByText(/waiting for a restart/)).toBeNull();

    const table = screen.getByRole("table", { name: "Library folders from this computer" });
    const rows = within(table).getAllByRole("row");
    expect(rows[1]).toHaveTextContent("Lectures");
    expect(rows[1]).toHaveTextContent("Reachable");
    expect(rows[2]).toHaveTextContent("Not reachable: not-found");
  });

  it("explains calmly when the server has no settings to change", async () => {
    open({ available: false, reason: "no supervisor: this API runs without maktaba-server" });
    expect(await screen.findByText(/can only be changed when Maktaba runs/)).toBeInTheDocument();
    expect(screen.getByText(/no supervisor/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Save" })).toBeNull();
  });

  it("refuses non-admins without asking the API", () => {
    open(DOC, memberUser);
    expect(screen.getByText("Admins only")).toBeInTheDocument();
    expect(get).not.toHaveBeenCalled();
  });

  it("shows a load error with Try again", async () => {
    let fail = true;
    routeGet(get, {
      "/api/server/storage": NO_STORAGE,
      "/api/server/backups": NO_BACKUPS,
      "/api/server/settings": () => {
        if (fail) throw problem(502, "bad gateway", { detail: "supervisor not answering" });
        return DOC;
      },
    });
    renderPage(
      <ToastProvider>
        <StorageSettings />
      </ToastProvider>,
      { path: "/settings/storage" }
    );
    expect(await screen.findByText("supervisor not answering")).toBeInTheDocument();
    fail = false;
    await userEvent.click(screen.getByRole("button", { name: "Try again" }));
    expect(await screen.findByLabelText(/^Library path 1/)).toBeInTheDocument();
  });

  it("saves only what changed and warns when an address changes", async () => {
    const user = userEvent.setup();
    open();
    put.mockResolvedValue({ ...DOC, restart_needed: true });
    const listen = await screen.findByLabelText(/^Server address/);
    expect(screen.queryByRole("note")).toBeNull();
    await user.clear(listen);
    await user.type(listen, ":9090");
    expect(screen.getByRole("note")).toHaveTextContent(/changed an address/);
    await user.click(screen.getByRole("switch", { name: /find the server/ }));
    await user.click(screen.getByRole("button", { name: "Add a translation" }));
    await user.type(screen.getByLabelText(/^Library path 2/), "/Volumes/Archive");
    await user.type(screen.getByLabelText(/^On this computer 2/), "/Users/me/archive");
    await user.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() =>
      expect(put).toHaveBeenCalledWith("/api/server/settings", {
        media: {
          path_map: [
            { db: "/Volumes/Originals", local: "/Users/me/nas-nfs/Originals" },
            { db: "/Volumes/Archive", local: "/Users/me/archive" },
          ],
        },
        server: { listen: ":9090", mdns: false },
      })
    );
    expect(await screen.findByText(/waiting for a restart/)).toBeInTheDocument();
  });

  it("shows each refused value next to its field", async () => {
    const user = userEvent.setup();
    open();
    put.mockRejectedValue(
      problem(422, "invalid settings", {
        errors: [
          { field: "server.web_listen", message: "must differ from listen" },
          { field: "media.path_map[0].local", message: "not a directory" },
          { field: "media.path_map[1]", message: 'a path cannot hold ";" or "="' },
          { field: "media.roots[0]", message: "not absolute" },
        ],
      })
    );
    const web = await screen.findByLabelText(/^Web app address/);
    await user.clear(web);
    await user.type(web, ":8080");
    await user.clear(screen.getByLabelText(/^Media folder 1/));
    await user.type(screen.getByLabelText(/^Media folder 1/), "relative");
    await user.click(screen.getByRole("button", { name: "Add a translation" }));
    await user.type(screen.getByLabelText(/^Library path 2/), "/Volumes/A=B");
    await user.type(screen.getByLabelText(/^On this computer 2/), "/x");
    await user.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() =>
      expect(screen.getByLabelText(/^Web app address/)).toHaveAccessibleDescription(
        /must differ from listen/
      )
    );
    expect(screen.getByLabelText(/^On this computer 1/)).toHaveAccessibleDescription(
      /not a directory/
    );
    expect(screen.getByLabelText(/^Media folder 1/)).toHaveAccessibleDescription(/not absolute/);
    // a pair refused as a whole shows on its local side
    expect(screen.getByLabelText(/^On this computer 2/)).toHaveAccessibleDescription(/cannot hold/);
    // nothing left over for the top of the form
    expect(screen.queryByText(/media\./)).toBeNull();
  });

  it("restarts after asking, waits for the server to come back, then reloads", async () => {
    const user = userEvent.setup();
    const onReload = vi.fn();
    open({ ...DOC, restart_needed: true }, adminUser, { onReload });
    post.mockResolvedValue({ restarting: true });
    let pings = 0;
    routeGet(get, {
      "/api/server/storage": NO_STORAGE,
      "/api/server/backups": NO_BACKUPS,
      "/api/server/settings": { ...DOC, restart_needed: true },
      "/api/system/version": () => {
        pings += 1;
        if (pings === 1) throw new TypeError("network error");
        return { version: "1.0.0" };
      },
    });
    await user.click(await screen.findByRole("button", { name: "Restart the server" }));
    const dialog = screen.getByRole("dialog", { name: "Restart the server?" });
    expect(post).not.toHaveBeenCalled();
    await user.click(within(dialog).getByRole("button", { name: "Restart the server" }));
    expect(post).toHaveBeenCalledWith("/api/server/restart");
    await waitFor(() => expect(onReload).toHaveBeenCalledTimes(1));
    expect(pings).toBe(2);
  });

  it("saved backup settings wait for the same restart", async () => {
    const user = userEvent.setup();
    const backups = {
      available: true,
      file: DOC.file,
      saved: {
        dir: "",
        nightly: true,
        every_days: 1,
        hour: 4,
        keep: 7,
        update_keep: 0,
        pre_update: true,
        reserve_percent: 5,
        reserve_min_gb: 2,
        max_share_percent: 20,
      },
      running: {},
      restart_needed: true,
      can_restart: true,
      dir: "/data/backups",
      default_dir: "/data/backups",
      dumps: [],
      dumps_bytes: 0,
      update_keep: 3,
    };
    const onReload = vi.fn();
    post.mockResolvedValue({ restarting: true });
    let pings = 0;
    routeGet(get, {
      "/api/server/storage": NO_STORAGE,
      "/api/server/backups": backups,
      "/api/server/settings": DOC,
      "/api/system/version": () => {
        pings += 1;
        if (pings === 1) throw new TypeError("network error");
        return { version: "1.0.0" };
      },
    });
    renderPage(
      <ToastProvider>
        <StorageSettings pollMs={1} onReload={onReload} />
      </ToastProvider>,
      { path: "/settings/storage" }
    );
    // the server settings need no restart; the backups do
    expect(await screen.findByText(/waiting for a restart/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Restart the server" }));
    await user.click(
      within(screen.getByRole("dialog")).getByRole("button", { name: "Restart the server" })
    );
    expect(post).toHaveBeenCalledWith("/api/server/restart");
    await waitFor(() => expect(onReload).toHaveBeenCalledTimes(1));
  });

  it("says to restart by hand when the server cannot restart itself", async () => {
    open({ ...DOC, restart_needed: true, can_restart: false });
    expect(await screen.findByText(/cannot restart itself/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Restart the server" })).toBeNull();
  });

  it("explains a refused restart (409)", async () => {
    const user = userEvent.setup();
    open({ ...DOC, restart_needed: true });
    post.mockRejectedValue(
      problem(409, "no-supervisor", { detail: "no supervisor: restart the server yourself" })
    );
    await user.click(await screen.findByRole("button", { name: "Restart the server" }));
    await user.click(
      within(screen.getByRole("dialog")).getByRole("button", { name: "Restart the server" })
    );
    expect(await screen.findByRole("alert")).toHaveTextContent(/cannot restart itself/);
  });
});
