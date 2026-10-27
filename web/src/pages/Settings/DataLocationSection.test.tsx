// Settings → Storage & discovery: where Maktaba keeps its data, and moving it
// to another disk (check, confirm, the server away while it copies, back).
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post } };
});

import { ApiError } from "../../lib/api";
import { DataLocationSection } from "./DataLocationSection";
import { renderPage } from "../../test/pageHarness";
import { routeGet } from "../../test/queueHarness";

const GB = 2 ** 30;
const HOME = "/Users/me/Library/Application Support/Maktaba";
const INTERNAL = {
  mount: "/",
  name: "Macintosh HD",
  free_bytes: 100 * GB,
  total_bytes: 500 * GB,
  fs_type: "apfs",
  local: true,
  suitable: true,
  suggested_dir: HOME,
  internal: true,
  current: true,
};
const BIG = {
  ...INTERNAL,
  mount: "/Volumes/Big",
  name: "Big",
  free_bytes: 3000 * GB,
  total_bytes: 4000 * GB,
  suggested_dir: "/Volumes/Big/Maktaba",
  internal: false,
  current: false,
};
const NAS = {
  ...BIG,
  mount: "/Volumes/nas",
  name: "nas",
  fs_type: "smbfs",
  local: false,
  suitable: false,
  problem: "target_not_local",
  suggested_dir: "/Volumes/nas/Maktaba",
};
const DOC = {
  available: true,
  data_dir: HOME,
  state_dir: `${HOME}/state`,
  default_data_dir: HOME,
  total_bytes: 42 * GB,
  folders: [
    {
      name: "postgres",
      path: `${HOME}/postgres`,
      bytes: 30 * GB,
      files: 2000,
      exists: true,
      disk: "/",
    },
    { name: "backups", path: `${HOME}/backups`, bytes: 12 * GB, files: 7, exists: true, disk: "/" },
    { name: "chroma", path: `${HOME}/chroma`, bytes: 0, files: 0, exists: false, disk: "/" },
  ],
  disks: [INTERNAL],
  move: { state: "none" },
  can_move: true,
};
const OK_CHECK = {
  ok: true,
  target: "/Volumes/Big/Maktaba",
  need_bytes: 42 * GB,
  free_bytes: 3000 * GB,
  problems: [],
  warnings: [],
};

function problem(status: number, extra: Record<string, unknown> = {}) {
  return new ApiError({ type: "about:blank", title: "refused", status, ...extra });
}

function open(doc: unknown = DOC, version: () => unknown = () => ({ version: "1" })) {
  routeGet(get, {
    "/api/server/storage": typeof doc === "function" ? (doc as () => unknown) : () => doc,
    "/api/server/storage/disks": { disks: [INTERNAL, BIG, NAS] },
    "/api/system/version": version,
  });
  return renderPage(<DataLocationSection pollMs={1} />, { path: "/settings/storage" });
}

async function chooseBig() {
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: "Move to another disk" }));
  await user.click(await screen.findByRole("radio", { name: /Big/ }));
  return user;
}

describe("DataLocationSection", () => {
  beforeEach(() => {
    get.mockReset();
    post.mockReset();
  });
  afterEach(() => vi.restoreAllMocks());

  it("lists the data folders with their size and disk, and each disk's free space", async () => {
    open();
    const table = await screen.findByRole("table", { name: "Maktaba's data folders" });
    const rows = within(table).getAllByRole("row");
    expect(rows[1]).toHaveTextContent("Library database");
    expect(rows[1]).toHaveTextContent(`${HOME}/postgres`);
    expect(rows[1]).toHaveTextContent("30.0 GB");
    expect(rows[2]).toHaveTextContent("Backups");
    // a folder not made yet has no size
    expect(rows[3]).toHaveTextContent("Search index");
    expect(rows[3]).toHaveTextContent("—");
    expect(screen.getByText("42.0 GB")).toBeInTheDocument();
    expect(screen.getByText("100.0 GB free of 500.0 GB")).toBeInTheDocument();
  });

  it("hides the move where it cannot be done, and says why", async () => {
    open({ ...DOC, can_move: false, reason: "This machine is a node." });
    expect(
      await screen.findByText(/cannot be moved from here\. This machine is a node\./)
    ).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Move to another disk" })).toBeNull();
  });

  it("lists the disks to move to; one that cannot hold the data cannot be chosen", async () => {
    open();
    await chooseBig();
    expect(screen.getByRole("textbox", { name: /^Folder/ })).toHaveValue("/Volumes/Big/Maktaba");
    expect(screen.getByRole("radio", { name: /nas/ })).toBeDisabled();
    expect(screen.getByText("Not a disk connected to this computer.")).toBeInTheDocument();
    // where it is now is not a place to move to
    expect(screen.getByRole("radio", { name: /Macintosh HD/ })).toBeDisabled();
    // Move waits for a check
    expect(screen.getByRole("button", { name: "Move" })).toBeDisabled();
  });

  it("a check's problems block the move, in the page's words", async () => {
    open();
    const user = await chooseBig();
    post.mockResolvedValue({
      ...OK_CHECK,
      ok: false,
      problems: [
        { code: "target_not_empty", message: "en" },
        { code: "brand_new", message: "A finding this page has no text for." },
      ],
      warnings: [{ code: "disk_tight", message: "en", params: { free_after_bytes: 5 * GB } }],
    });
    await user.click(screen.getByRole("button", { name: "Check" }));
    expect(post).toHaveBeenCalledWith("/api/server/storage/check", {
      target: "/Volumes/Big/Maktaba",
    });
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("That folder is not empty");
    expect(alert).toHaveTextContent("A finding this page has no text for.");
    expect(alert).toHaveTextContent("only 5.0 GB would be left free");
    expect(screen.getByRole("button", { name: "Move" })).toBeDisabled();
  });

  it("moves after a check and a confirm, waits for the server, then shows the move done", async () => {
    let pings = 0;
    let moved = false;
    let back = false;
    const done = {
      ...DOC,
      data_dir: "/Volumes/Big/Maktaba",
      move: {
        state: "done",
        target: "/Volumes/Big/Maktaba",
        old: [`${HOME}/postgres.moved-20261026`],
      },
    };
    open(
      () => (moved ? done : DOC),
      () => {
        pings += 1;
        // away while it copies
        if (!back) throw new TypeError("network error");
        return { version: "1" };
      }
    );
    const user = await chooseBig();
    post.mockImplementation(async (path: string) => {
      if (path === "/api/server/storage/check") return OK_CHECK;
      moved = true;
      return { state: "requested", target: "/Volumes/Big/Maktaba" };
    });
    await user.click(screen.getByRole("button", { name: "Check" }));
    expect(await screen.findByText(/It fits: the move needs 42\.0 GB/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Move" }));
    const dialog = screen.getByRole("dialog", { name: "Move Maktaba's data?" });
    expect(dialog).toHaveTextContent("Maktaba stops for everyone");
    expect(dialog).toHaveTextContent("The old copy is kept until you delete it.");
    expect(post).toHaveBeenCalledTimes(1);
    await user.click(within(dialog).getByRole("button", { name: "Move" }));
    expect(post).toHaveBeenLastCalledWith("/api/server/storage/move", {
      target: "/Volumes/Big/Maktaba",
    });
    expect(
      await screen.findByText(/Moving Maktaba's data to \/Volumes\/Big\/Maktaba/)
    ).toBeInTheDocument();
    await waitFor(() => expect(pings).toBeGreaterThanOrEqual(2));
    back = true;
    expect(
      await screen.findByText("Maktaba's data was moved to /Volumes/Big/Maktaba.")
    ).toBeInTheDocument();
    expect(screen.getByText(`${HOME}/postgres.moved-20261026`)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Move" })).toBeNull();
  });

  it("shows the refusal when the move is refused after the check", async () => {
    open();
    const user = await chooseBig();
    post.mockImplementation(async (path: string) => {
      if (path === "/api/server/storage/check") return OK_CHECK;
      throw problem(422, {
        ...OK_CHECK,
        ok: false,
        problems: [{ code: "move_unfinished", message: "en", params: { target: "/Volumes/X" } }],
      });
    });
    await user.click(screen.getByRole("button", { name: "Check" }));
    await user.click(await screen.findByRole("button", { name: "Move" }));
    await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Move" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "A move to /Volumes/X is not finished yet."
    );
  });

  it("says when the server cannot move its data itself (409)", async () => {
    open();
    const user = await chooseBig();
    post.mockImplementation(async (path: string) => {
      if (path === "/api/server/storage/check") return OK_CHECK;
      throw problem(409, { code: "no-supervisor" });
    });
    await user.click(screen.getByRole("button", { name: "Check" }));
    await user.click(await screen.findByRole("button", { name: "Move" }));
    await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Move" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(/cannot move its data itself/);
  });

  it("shows a failed move with its reason under Details", async () => {
    open({
      ...DOC,
      move: { state: "failed", target: "/Volumes/Big/Maktaba", error: "no space left on device" },
    });
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Moving Maktaba's data to /Volumes/Big/Maktaba failed. Maktaba keeps running from where it was."
    );
    expect(screen.getByText("no space left on device")).toBeInTheDocument();
  });

  it("waits for a move asked for already, then shows how it went", async () => {
    let calls = 0;
    routeGet(get, {
      "/api/server/storage": () => {
        calls += 1;
        return calls < 3
          ? { ...DOC, move: { state: "requested", target: "/Volumes/Big/Maktaba" } }
          : { ...DOC, move: { state: "done", target: "/Volumes/Big/Maktaba" } };
      },
      "/api/system/version": { version: "1" },
    });
    renderPage(<DataLocationSection pollMs={1} />, { path: "/settings/storage" });
    expect(await screen.findByText(/Moving Maktaba's data to/)).toBeInTheDocument();
    await waitFor(() =>
      expect(
        screen.getByText("Maktaba's data was moved to /Volumes/Big/Maktaba.")
      ).toBeInTheDocument()
    );
  });
});
