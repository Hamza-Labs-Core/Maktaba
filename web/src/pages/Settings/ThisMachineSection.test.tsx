import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { get, post, put } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post, put } };
});

import { ThisMachineSection, gb } from "./ThisMachineSection";
import { renderPage, memberUser } from "../../test/pageHarness";
import { routeGet } from "../../test/queueHarness";

const G = 1024 ** 3;
// a J4125 Synology with 6 GB, an Intel render device, btrfs on spinning disks
const DOC = {
  available: true,
  machine: {
    platform: "dsm",
    platform_from: "env",
    arch: "amd64",
    memory: {
      limit_bytes: 6 * G,
      total_bytes: 32 * G,
      cgroup_limit_bytes: 6 * G,
      available_bytes: 4 * G,
    },
    cpu: {
      cores: 2,
      online: 4,
      quota: 2,
      model: "Intel(R) Celeron(R) J4125 CPU @ 2.00GHz",
      avx2: false,
      avx512f: false,
    },
    gpu: { render: [{ path: "/dev/dri/renderD128", driver: "i915" }], nvidia: [] },
    kernel: { release: "4.4.302+", landlock: 0 },
  },
  data_disk: { kind: "rotational", fs_type: "btrfs", mount: "/var/lib/maktaba" },
  tier: "standard",
  detected: "standard",
  chosen: false,
  saved: "auto",
  restart_needed: false,
  can_restart: true,
  moved_up: false,
  reason: { tier: "standard", memory_gb: 6, gpu: "" },
  defaults: {
    pipeline_workers: 2,
    shared_buffers: "512MB",
    work_mem: "8MB",
    quiet_hours: "01:00-06:00",
  },
  nas: true,
  quiet_hours: "01:00-06:00",
  quiet_set: false,
  quiet_default: "01:00-06:00",
  rotational: true,
  disk_how: "rotational",
};

function open(doc: unknown = DOC, user = undefined as typeof memberUser | undefined) {
  routeGet(get, { "/api/server/machine": () => doc });
  return renderPage(<ThisMachineSection pollMs={1} />, { path: "/settings/machine", user });
}

describe("ThisMachineSection", () => {
  beforeEach(() => {
    for (const f of [get, post, put]) f.mockReset();
    localStorage.removeItem("mkt:locale");
  });
  afterEach(() => {
    vi.restoreAllMocks();
    localStorage.removeItem("mkt:locale");
  });

  // T09: the tier and why, in English
  it("shows the tier, why, and what the server found", async () => {
    open();
    expect(await screen.findByTestId("tier-reason")).toHaveTextContent(
      "Standard: 6 GB, no GPU for video"
    );
    expect(screen.getByText("Detected from the memory Maktaba may use.")).toBeInTheDocument();
    expect(screen.getByText("Synology DSM")).toBeInTheDocument();
    expect(screen.getByText(/6 GB for Maktaba, 4 GB available now/)).toBeInTheDocument();
    expect(
      screen.getByText("The container is limited to 6 GB of the machine's 32 GB.")
    ).toBeInTheDocument();
    expect(
      screen.getByText("The container may use 2 of the machine's 4 cores.")
    ).toBeInTheDocument();
    expect(
      screen.getByText("No AVX2: speech to text uses a smaller model here.")
    ).toBeInTheDocument();
    expect(screen.getByText("i915 (/dev/dri/renderD128)")).toBeInTheDocument();
    expect(screen.getByText("Spinning disks (btrfs)")).toBeInTheDocument();
    expect(screen.getByText("Pipeline workers: at most 2")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
  });

  // T09: the same in Arabic
  it("says the tier and why in Arabic", async () => {
    localStorage.setItem("mkt:locale", "ar");
    open();
    const reason = await screen.findByTestId("tier-reason");
    expect(reason.textContent).toContain("قياسي");
    expect(reason.textContent).toContain("غيغابايت");
    expect(reason.textContent).toContain("لا معالج رسوميات للفيديو");
    expect(screen.getByText("أقراص دوّارة (btrfs)")).toBeInTheDocument();
  });

  it("counts the ZFS cache as available, or says it could not be read", async () => {
    const zfs = structuredClone(DOC);
    zfs.machine.platform = "truenas";
    Object.assign(zfs.machine.memory, {
      limit_bytes: 32 * G,
      total_bytes: 32 * G,
      available_bytes: 31 * G,
      arc_reclaimable_bytes: 30 * G,
    });
    open(zfs);
    expect(await screen.findByText(/31 GB available now/)).toBeInTheDocument();
    expect(screen.getByText(/including 30 GB the ZFS cache will give back/)).toBeInTheDocument();

    const unread = structuredClone(zfs);
    Object.assign(unread.machine.memory, {
      available_bytes: 1 * G,
      arc_reclaimable_bytes: 0,
      arc_unread: true,
    });
    open(unread);
    expect(
      await screen.findByText(
        "The ZFS cache could not be read, so free memory may look lower than it is."
      )
    ).toBeInTheDocument();
  });

  it("says when more memory moved the tier up", async () => {
    open({
      ...DOC,
      tier: "strong",
      detected: "strong",
      moved_up: true,
      reason: { tier: "strong", memory_gb: 32, gpu: "" },
    });
    expect(
      await screen.findByText(
        "This machine now has more memory, so Maktaba moved it to the Strong tier."
      )
    ).toBeInTheDocument();
  });

  it("saves an admin's pick for the next start, and restarts", async () => {
    open();
    await screen.findByTestId("tier-reason");
    put.mockResolvedValue({ ...DOC, saved: "light", restart_needed: true });
    await userEvent.selectOptions(screen.getByLabelText("Tier for this machine"), "light");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(put).toHaveBeenCalledWith("/api/server/machine", { tier: "light" });
    expect(await screen.findByText(/restart the server to apply it/)).toBeInTheDocument();

    post.mockResolvedValue({ restarting: true });
    routeGet(get, {
      "/api/server/machine": () => ({
        ...DOC,
        tier: "light",
        chosen: true,
        saved: "light",
        reason: { tier: "light", memory_gb: 6, gpu: "" },
      }),
    });
    await userEvent.click(screen.getByRole("button", { name: "Restart now" }));
    expect(post).toHaveBeenCalledWith("/api/server/restart");
    await waitFor(() => expect(screen.getByTestId("tier-reason")).toHaveTextContent("Light: 6 GB"));
    expect(screen.getByText("Picked by an admin.")).toBeInTheDocument();
  });

  it("is admins' only, and says when the server has no supervisor", async () => {
    open(DOC, memberUser);
    expect(await screen.findByText("Admins only")).toBeInTheDocument();
    expect(get).not.toHaveBeenCalled();
    open({ available: false, reason: "no supervisor" });
    expect(
      await screen.findByText(
        "This machine's details are not available: the server runs without its supervisor."
      )
    ).toBeInTheDocument();
  });

  // Epic 59 story 59.02: the tier's Postgres memory and quiet hours; the window is saved
  // (and back to the tier's); a disk that cannot be read is asked about
  it("shows and saves the quiet hours", async () => {
    open();
    expect(await screen.findByText("Database memory: 512MB")).toBeInTheDocument();
    expect(screen.getByText("Quiet hours: 01:00-06:00")).toBeInTheDocument();
    expect(screen.getByText("The database is tuned for spinning disks.")).toBeInTheDocument();
    const input = screen.getByRole("textbox", { name: /Quiet hours/ });
    expect(input).toHaveValue("01:00-06:00");
    put.mockResolvedValue({
      ...DOC,
      quiet_hours: "22:00-06:00",
      quiet_set: true,
      restart_needed: true,
    });
    await userEvent.clear(input);
    await userEvent.type(input, "22:00-06:00");
    await userEvent.click(screen.getByRole("button", { name: "Save quiet hours" }));
    expect(put).toHaveBeenCalledWith("/api/server/machine", { quiet_hours: "22:00-06:00" });
    put.mockResolvedValue({ ...DOC });
    await userEvent.click(
      await screen.findByRole("button", { name: "Use the tier's (01:00-06:00)" })
    );
    expect(put).toHaveBeenLastCalledWith("/api/server/machine", { quiet_default: true });
  });

  it("asks whether an unread data disk is an SSD", async () => {
    open({ ...DOC, data_disk: { kind: "unknown", fs_type: "zfs" }, disk_how: "unknown" });
    const ask = await screen.findByLabelText("Is Maktaba's data folder on an SSD?");
    put.mockResolvedValue({
      ...DOC,
      data_disk: { kind: "unknown", fs_type: "zfs" },
      disk_how: "asked",
      rotational: false,
    });
    await userEvent.selectOptions(ask, "ssd");
    expect(put).toHaveBeenCalledWith("/api/server/machine", { data_disk: "ssd" });
    expect(await screen.findByText("As you answered (zfs)")).toBeInTheDocument();
  });

  it("rounds GB as people read them", () => {
    expect(gb(3.7 * G)).toBe(3.7);
    expect(gb(31.6 * G)).toBe(32);
  });
});
