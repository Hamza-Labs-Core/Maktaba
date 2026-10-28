// Epic 47 pages: /queue/file-actions and /queue/duplicates per state and
// action, the failures page's new reasons and Restore, admin-only rendering,
// and axe.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { get, post, admin } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  admin: { value: true },
}));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post } };
});
vi.mock("../../components/queue/useIsAdmin", () => ({ useIsAdmin: () => admin.value }));

import { FileActions } from "./FileActions";
import { Duplicates } from "./Duplicates";
import { Failures } from "./Failures";
import { renderAt, routeGet } from "../../test/queueHarness";
import { axeViolations } from "../../test/axe";
import { LIB, fileAction, group, offlineItem, trash } from "../../test/fixtures/offline";
import type { FailureItem } from "../../lib/queueApi";

beforeEach(() => {
  get.mockReset();
  post.mockReset();
  admin.value = true;
});

describe("/queue/file-actions", () => {
  const rows = [
    fileAction(),
    fileAction({
      id: "fa-2",
      kind: "convert",
      state: "failed",
      trash_path: null,
      new_path: null,
      restorable: false,
      error: "verification failed",
      verify: { ok: false, reasons: ["frames 1200 != 1310"] },
      video_title: "Lecture 09",
    }),
    fileAction({
      id: "fa-3",
      kind: "youtube_fetch",
      old_path: null,
      trash_path: null,
      restorable: false,
    }),
    fileAction({ id: "fa-4", restore_queued: true, video_title: "Lecture 10" }),
  ];

  it("lists the ledger and the trash, restores a row and filters", async () => {
    routeGet(get, { "/api/queues/file-actions": { items: rows, next: null, trash } });
    post.mockResolvedValue({ action_id: rows[0].id, job_id: 7, outcome: "inserted" });
    const { container } = renderAt(<FileActions />, "/queue/file-actions");
    expect(await screen.findByText(/files: 4 · 3 GB · next purge/)).toBeInTheDocument();
    expect(screen.getByText(/verification failed · frames 1200 != 1310/)).toBeInTheDocument();
    expect(screen.getByText("Restore queued")).toBeInTheDocument();
    // only the restorable row that is not queued has the button
    expect(screen.getAllByRole("button", { name: /^Restore/ })).toHaveLength(1);
    expect(await axeViolations(container)).toEqual([]);

    await userEvent.click(screen.getByRole("button", { name: /^Restore/ }));
    expect(post).toHaveBeenCalledWith(`/api/queues/file-actions/${rows[0].id}/restore`);
    expect(await screen.findByRole("status")).toHaveTextContent("Restore of Lecture 04 queued.");

    await userEvent.selectOptions(screen.getByLabelText("What"), "convert");
    await userEvent.click(screen.getByRole("checkbox", { name: "Can be restored" }));
    await waitFor(() =>
      expect(
        get.mock.calls.some((c) => /kind=convert/.test(c[0]) && /restorable=true/.test(c[0]))
      ).toBe(true)
    );
  });

  it("an empty ledger and an empty trash", async () => {
    routeGet(get, { "/api/queues/file-actions": { items: [], next: null, trash: [] } });
    renderAt(<FileActions />, "/queue/file-actions");
    expect(await screen.findByText("No file actions match.")).toBeInTheDocument();
    expect(screen.getByText("Every library's trash is empty.")).toBeInTheDocument();
  });

  it("is admin only: a viewer sees no ledger and nothing is fetched", () => {
    admin.value = false;
    renderAt(<FileActions />, "/queue/file-actions");
    expect(screen.getByRole("heading", { name: "File actions" })).toBeInTheDocument();
    expect(screen.queryByRole("table")).toBeNull();
    expect(get).not.toHaveBeenCalled();
  });
});

describe("/queue/duplicates", () => {
  it("shows the members, chooses the keeper, applies and dismisses", async () => {
    const g = group();
    routeGet(get, { "/api/queues/duplicates": { items: [g], next: null } });
    post.mockResolvedValue({ group_id: g.id, job_id: 1, outcome: "inserted" });
    const { container } = renderAt(<Duplicates />, "/queue/duplicates");
    expect(await screen.findByText("1920×1080")).toBeInTheDocument();
    expect(screen.getByText("Watched by 2, finished by 1")).toBeInTheDocument();
    expect(screen.getByText("Not watched")).toBeInTheDocument();
    expect(screen.getByText("smb://nas/O/Playlists/Seerah 01.mp4")).toBeInTheDocument();
    // the first file is the keeper until another is chosen
    expect(screen.getByRole("radio", { name: "Keep Seerah 01" })).toBeChecked();
    expect(await axeViolations(container)).toEqual([]);

    await userEvent.click(screen.getByRole("radio", { name: "Keep Seerah 01 (playlist)" }));
    await userEvent.click(screen.getByRole("button", { name: "Apply" }));
    expect(post).toHaveBeenCalledWith(`/api/queues/duplicates/${g.id}/apply`, {
      keeper_video_id: g.members[1].video_id,
    });
    expect(await screen.findByRole("status")).toHaveTextContent("The merge is queued.");

    await userEvent.click(screen.getByRole("button", { name: "Dismiss" }));
    expect(post).toHaveBeenCalledWith(`/api/queues/duplicates/${g.id}/dismiss`);
  });

  it("applies every open identical group at once", async () => {
    const a = group({ id: "ga", tier: "exact" });
    const b = group({ id: "gb", tier: "exact" });
    const queued = group({
      id: "gc",
      tier: "exact",
      job: { ...offlineItem({ id: 3, stage: "dedup_apply", state: "pending" }) },
    });
    routeGet(get, { "/api/queues/duplicates": { items: [a, b, queued], next: null } });
    post.mockResolvedValue({ outcome: "inserted" });
    renderAt(<Duplicates />, "/queue/duplicates");
    await userEvent.click(await screen.findByRole("button", { name: "Apply all identical (2)" }));
    await waitFor(() => expect(post).toHaveBeenCalledTimes(2));
    expect(post.mock.calls.map((c) => c[0])).toEqual([
      "/api/queues/duplicates/ga/apply",
      "/api/queues/duplicates/gb/apply",
    ]);
  });

  it("a merged group has no actions; an empty list; filters in the URL", async () => {
    routeGet(get, {
      "/api/queues/duplicates": (url: string) =>
        url.includes("status=applied")
          ? {
              items: [group({ status: "applied", keeper_video_id: group().members[1].video_id })],
              next: null,
            }
          : { items: [], next: null },
    });
    renderAt(<Duplicates />, "/queue/duplicates");
    expect(await screen.findByText("No duplicate groups match.")).toBeInTheDocument();
    await userEvent.selectOptions(screen.getByLabelText("Status"), "applied");
    const art = await screen.findByRole("article");
    expect(within(art).queryByRole("button", { name: "Apply" })).toBeNull();
    expect(within(art).getByRole("radio", { name: "Keep Seerah 01 (playlist)" })).toBeChecked();
    expect(within(art).getByRole("radio", { name: "Keep Seerah 01" })).toBeDisabled();
  });

  it("is admin only", () => {
    admin.value = false;
    renderAt(<Duplicates />, "/queue/duplicates");
    expect(screen.queryByRole("article")).toBeNull();
    expect(get).not.toHaveBeenCalled();
  });
});

describe("failures page: the Epic 47 kinds", () => {
  const base: FailureItem = {
    job_id: 77,
    stage: "convert_mp4",
    subject_kind: "video",
    subject_id: "11111111-1111-4111-8111-111111111111",
    label: "Lecture 09",
    path: "smb://nas/O/09.mkv",
    library_id: LIB,
    library_name: "Islamic lectures",
    state: "failed",
    status: "needs_you",
    message: "frames differ",
    signature: "convert_verify:frames differ",
    class: "unknown",
    parked_on: null,
    reason: "convert_verify",
    attempts: 2,
    max_attempts: 2,
    first_failed_at: null,
    last_failed_at: null,
    missing_confirmed_at: null,
    youtube_id: null,
    access_check: null,
    file_action: { id: "fa-9", kind: "convert", state: "failed", restorable: false },
  };
  const facets = { stage: [], class: [], signature: [], library: [], status: [] };

  it("words each reason, links the ledger and restores where it can", async () => {
    const items: FailureItem[] = [
      base,
      { ...base, job_id: 78, reason: "trash_changed", label: "Purge", file_action: null },
      {
        ...base,
        job_id: 79,
        reason: "dedup_keeper_unreadable",
        label: "Seerah",
        file_action: { id: "fa-10", kind: "dedup", state: "done", restorable: true },
      },
      { ...base, job_id: 80, reason: "youtube_rate_limited", label: "Channel", file_action: null },
    ];
    routeGet(get, { "/api/queues/failures": { items, next: null, facets } });
    post.mockResolvedValue({});
    renderAt(<Failures />, "/queue/failures");
    expect(
      await screen.findByText("The new MP4 did not pass the check; the original is untouched")
    ).toBeInTheDocument();
    expect(
      screen.getByText("A file in the trash changed; nothing was deleted")
    ).toBeInTheDocument();
    expect(screen.getByText("The file to keep cannot be read")).toBeInTheDocument();
    expect(screen.getByText("YouTube is limiting requests")).toBeInTheDocument();
    expect(screen.getAllByRole("link", { name: "File actions" })).toHaveLength(2);
    const restore = screen.getAllByRole("button", { name: "Restore" });
    expect(restore).toHaveLength(1);
    await userEvent.click(restore[0]);
    expect(post).toHaveBeenCalledWith("/api/queues/file-actions/fa-10/restore");
    expect(await screen.findByText("Restore queued.")).toBeInTheDocument();
  });
});
