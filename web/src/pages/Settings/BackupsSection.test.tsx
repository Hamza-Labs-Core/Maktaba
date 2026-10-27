// Settings → Storage & discovery: the backups of the library database (where
// the dumps go, when, how many are kept, the room they may take) and the dumps
// there are.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { get, put, del } = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), del: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, put, delete: del } };
});

import { ApiError } from "../../lib/api";
import { BackupsSection } from "./BackupsSection";
import { renderPage } from "../../test/pageHarness";
import { routeGet } from "../../test/queueHarness";

const GB = 2 ** 30;
const HOME = "/Users/me/Library/Application Support/Maktaba";
const SETTINGS = {
  dir: "",
  nightly: true,
  every_days: 1,
  hour: 3,
  keep: 7,
  update_keep: 0,
  pre_update: true,
  reserve_percent: 5,
  reserve_min_gb: 2,
  max_share_percent: 20,
};
const DUMPS = [
  {
    name: "maktaba-20261025-030000.dump",
    kind: "nightly",
    bytes: 2 * GB,
    at: "2026-10-25T03:00:00Z",
  },
  {
    name: "maktaba-pre-update-v0.40.dump",
    kind: "update",
    bytes: 3 * GB,
    at: "2026-10-20T10:00:00Z",
  },
];
const DOC = {
  available: true,
  file: `${HOME}/server.toml`,
  saved: SETTINGS,
  running: SETTINGS,
  restart_needed: false,
  can_restart: true,
  dir: `${HOME}/backups`,
  default_dir: `${HOME}/backups`,
  disk: {
    mount: "/",
    name: "Macintosh HD",
    free_bytes: 100 * GB,
    total_bytes: 500 * GB,
    fs_type: "apfs",
    local: true,
    suitable: true,
    internal: true,
    current: true,
    suggested_dir: HOME,
  },
  dumps: DUMPS,
  dumps_bytes: 5 * GB,
  update_keep: 3,
};

function problem(status: number, extra: Record<string, unknown> = {}) {
  return new ApiError({ type: "about:blank", title: "refused", status, ...extra });
}

function open(doc: unknown = DOC, onRestartNeeded?: (b: boolean) => void) {
  routeGet(get, { "/api/server/backups": () => doc });
  return renderPage(<BackupsSection onRestartNeeded={onRestartNeeded} />, {
    path: "/settings/storage",
  });
}

describe("BackupsSection", () => {
  beforeEach(() => {
    for (const f of [get, put, del]) f.mockReset();
  });
  afterEach(() => vi.restoreAllMocks());

  it("shows the saved settings and the dumps there are, with their disk", async () => {
    open();
    const folder = await screen.findByRole("textbox", { name: /^Backups folder/ });
    expect(folder).toHaveValue("");
    expect(folder).toHaveAccessibleDescription(
      `Leave it empty to keep them beside the database: ${HOME}/backups`
    );
    expect(
      screen.getByRole("switch", { name: "Back up the database on a schedule" })
    ).toBeChecked();
    expect(screen.getByRole("combobox", { name: "How often" })).toHaveValue("1");
    expect(screen.getByRole("combobox", { name: /^At/ })).toHaveValue("3");
    expect(screen.getByRole("spinbutton", { name: /^Scheduled dumps to keep/ })).toHaveValue(7);
    expect(
      screen.getByRole("spinbutton", { name: /^Pre-update dumps to keep/ })
    ).toHaveAccessibleDescription("0 keeps as many as the Updates settings say (now 3).");
    expect(
      screen.getByRole("spinbutton", { name: /^Keep free on the disk \(percent\)/ })
    ).toHaveValue(5);
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
    // the pre-update dump is on: no warning
    expect(screen.queryByText(/cannot put the database back/)).toBeNull();

    const table = screen.getByRole("table", { name: "Database dumps" });
    const rows = within(table).getAllByRole("row");
    expect(rows[1]).toHaveTextContent("maktaba-20261025-030000.dump");
    expect(rows[1]).toHaveTextContent("Scheduled");
    expect(rows[1]).toHaveTextContent("2.0 GB");
    expect(rows[2]).toHaveTextContent("Before an update");
    expect(rows[2]).toHaveTextContent("/");
    expect(screen.getByText("5.0 GB")).toBeInTheDocument();
    expect(screen.getByText("100.0 GB free of 500.0 GB")).toBeInTheDocument();
  });

  it("explains calmly when there is no server to ask", async () => {
    open({ available: false, reason: "no supervisor" });
    expect(
      await screen.findByText(/can only be changed here when Maktaba runs/)
    ).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Save" })).toBeNull();
  });

  it("saves only what changed, and tells the page a restart waits", async () => {
    const onRestartNeeded = vi.fn();
    open(DOC, onRestartNeeded);
    const user = userEvent.setup();
    const keep = await screen.findByRole("spinbutton", { name: /^Scheduled dumps to keep/ });
    await user.clear(keep);
    await user.type(keep, "14");
    await user.selectOptions(screen.getByRole("combobox", { name: "How often" }), "n");
    const every = screen.getByRole("spinbutton", { name: /^Every how many days/ });
    await user.clear(every);
    await user.type(every, "3");
    await user.selectOptions(screen.getByRole("combobox", { name: /^At/ }), "1");
    put.mockResolvedValue({
      ...DOC,
      saved: { ...SETTINGS, keep: 14, every_days: 3, hour: 1 },
      restart_needed: true,
    });
    await user.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() =>
      expect(put).toHaveBeenCalledWith("/api/server/backups", { keep: 14, every_days: 3, hour: 1 })
    );
    expect(await screen.findByText(/^Saved\./)).toBeInTheDocument();
    expect(onRestartNeeded).toHaveBeenLastCalledWith(true);
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
  });

  it("puts each refused value under its field", async () => {
    open();
    const user = userEvent.setup();
    const share = await screen.findByRole("spinbutton", { name: /^The most of the disk/ });
    await user.clear(share);
    await user.type(share, "100");
    put.mockRejectedValue(
      problem(422, {
        errors: [
          { field: "backups.max_share_percent", message: "max_share_percent must be 1..100" },
        ],
      })
    );
    await user.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() =>
      expect(put).toHaveBeenCalledWith("/api/server/backups", { max_share_percent: 100 })
    );
    expect(await screen.findByText("max_share_percent must be 1..100")).toBeInTheDocument();
    expect(share).toHaveAccessibleDescription(/max_share_percent must be 1\.\.100/);
  });

  it("checks a typed number before sending it", async () => {
    open();
    const user = userEvent.setup();
    const keep = await screen.findByRole("spinbutton", { name: /^Scheduled dumps to keep/ });
    await user.clear(keep);
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByText("Type a number.")).toBeInTheDocument();
    expect(put).not.toHaveBeenCalled();
  });

  it("warns when the dump before updates is turned off", async () => {
    open();
    const user = userEvent.setup();
    await user.click(
      await screen.findByRole("switch", { name: "Back up the database before each update" })
    );
    expect(screen.getByRole("alert")).toHaveTextContent(
      "Without it, an update whose migrations fail cannot put the database back."
    );
    put.mockResolvedValue({ ...DOC, saved: { ...SETTINGS, pre_update: false } });
    await user.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() =>
      expect(put).toHaveBeenCalledWith("/api/server/backups", { pre_update: false })
    );
  });

  for (const [answer, move] of [
    ["Move them to the new folder", true],
    ["Leave them where they are", false],
  ] as const) {
    it(`a new folder whose old one holds dumps asks first: ${answer}`, async () => {
      open();
      const user = userEvent.setup();
      await user.type(
        await screen.findByRole("textbox", { name: /^Backups folder/ }),
        "/Volumes/Big/Backups"
      );
      put.mockRejectedValueOnce(
        problem(422, {
          errors: [{ field: "backups.move_dumps", message: "the old folder holds dumps" }],
          dumps: 4,
          dumps_bytes: 6 * GB,
          from: `${HOME}/backups`,
        })
      );
      put.mockResolvedValueOnce({
        ...DOC,
        saved: { ...SETTINGS, dir: "/Volumes/Big/Backups" },
        restart_needed: true,
      });
      await user.click(screen.getByRole("button", { name: "Save" }));
      const dialog = await screen.findByRole("dialog");
      expect(dialog).toHaveTextContent(`The old folder ${HOME}/backups holds dumps: 4 (6.0 GB).`);
      expect(dialog).toHaveTextContent(/no longer listed/);
      // the question is not an error
      expect(screen.queryByText("the old folder holds dumps")).toBeNull();
      await user.click(within(dialog).getByRole("button", { name: answer }));
      await waitFor(() =>
        expect(put).toHaveBeenLastCalledWith("/api/server/backups", {
          dir: "/Volumes/Big/Backups",
          move_dumps: move,
        })
      );
      expect(put).toHaveBeenCalledTimes(2);
      await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
      expect(await screen.findByText(/^Saved\./)).toBeInTheDocument();
    });
  }

  it("deletes a dump after asking", async () => {
    open();
    const user = userEvent.setup();
    await user.click(
      await screen.findByRole("button", { name: "Delete maktaba-20261025-030000.dump" })
    );
    const dialog = screen.getByRole("dialog", { name: "Delete this dump?" });
    expect(del).not.toHaveBeenCalled();
    del.mockResolvedValue({ ...DOC, dumps: [DUMPS[1]], dumps_bytes: 3 * GB });
    await user.click(within(dialog).getByRole("button", { name: "Delete" }));
    await waitFor(() =>
      expect(del).toHaveBeenCalledWith("/api/server/backups/dumps/maktaba-20261025-030000.dump")
    );
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(screen.queryByText("maktaba-20261025-030000.dump")).toBeNull();
    expect(screen.getByText(/All the dumps together/)).toHaveTextContent("3.0 GB");
  });

  it("cancelling the delete keeps the dump", async () => {
    open();
    const user = userEvent.setup();
    await user.click(
      await screen.findByRole("button", { name: "Delete maktaba-20261025-030000.dump" })
    );
    await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Cancel" }));
    expect(del).not.toHaveBeenCalled();
    expect(screen.getByText("maktaba-20261025-030000.dump")).toBeInTheDocument();
  });

  it("says why a dump an update may still need cannot be deleted (409)", async () => {
    open();
    const user = userEvent.setup();
    await user.click(
      await screen.findByRole("button", { name: "Delete maktaba-pre-update-v0.40.dump" })
    );
    del.mockRejectedValue(problem(409));
    const dialog = screen.getByRole("dialog");
    await user.click(within(dialog).getByRole("button", { name: "Delete" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent(
      /An update under way may still need this dump/
    );
  });
});
