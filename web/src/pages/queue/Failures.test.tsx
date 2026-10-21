import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post } };
});

import type { FailureItem, FailureList } from "../../lib/queueApi";
import { Failures, matchingCount, sharePrefix } from "./Failures";
import { renderAt, routeGet } from "../../test/queueHarness";

const LIB = "0a1b2c3d-0000-4000-8000-00000000abcd";

function item(over: Partial<FailureItem>): FailureItem {
  return {
    job_id: 1,
    stage: "content_hash",
    subject_kind: "video",
    subject_id: "11111111-1111-4111-8111-111111111111",
    label: "Lesson 1",
    path: "smb://nas/Originals/Lessons/a.mp4",
    library_id: LIB,
    library_name: "Lessons",
    state: "failed",
    status: "needs_you",
    message: "a.mp4: not found",
    signature: "file_missing",
    class: "item_permanent",
    parked_on: null,
    reason: "missing_confirmed",
    attempts: 1,
    max_attempts: 3,
    first_failed_at: "2026-10-20T10:00:00Z",
    last_failed_at: "2026-10-21T10:00:00Z",
    missing_confirmed_at: "2026-10-21T10:00:00Z",
    youtube_id: "yt1",
    access_check: null,
    ...over,
  };
}

const list: FailureList = {
  items: [
    item({ job_id: 9 }),
    item({
      job_id: 8,
      label: "Lesson 2",
      signature: "permission_denied",
      reason: "permission",
      message: "PermissionError: [Errno 13] Permission denied",
      youtube_id: null,
      access_check: {
        ok: false,
        error: "PermissionError: denied",
        host: "mac",
        checked_at: "2026-10-21T10:00:00Z",
      },
    }),
    item({
      job_id: 7,
      stage: "check_tags",
      subject_kind: "season",
      label: "Show · S1",
      path: null,
      status: "waiting",
      reason: "waiting:dependency:model:tagcheck",
      parked_on: "dependency:model:tagcheck",
      youtube_id: null,
    }),
  ],
  next: null,
  facets: {
    stage: [
      { value: "content_hash", count: 2 },
      { value: "check_tags", count: 1 },
    ],
    class: [],
    signature: [{ value: "file_missing", label: "content_hash", count: 853 }],
    library: [{ value: LIB, label: "Lessons", count: 3 }],
    status: [
      { value: "needs_you", count: 2 },
      { value: "waiting", count: 1 },
      { value: "ignored", count: 4 },
    ],
  },
};

beforeEach(() => {
  get.mockReset();
  post.mockReset();
  routeGet(get, { "/api/queues/failures": list });
  post.mockResolvedValue({ action: "retry", matched: 1, affected: 1, skipped: 0 });
});

describe("the failures page", () => {
  it("lists every failing item with why and since when", async () => {
    renderAt(<Failures />, "/queue/failures?stage=content_hash", "/queue/failures");
    expect(await screen.findByText("The file is gone (the scan confirmed it)")).toBeTruthy();
    expect(screen.getByText("No permission to read the file")).toBeTruthy();
    expect(screen.getByText("Waiting for the model")).toBeTruthy();
    expect(get.mock.calls[0][0]).toContain("stage=content_hash");
    const row = screen.getByText(/Lesson 1/).closest("tr")!;
    expect(within(row).getByText("smb://nas/Originals/Lessons/a.mp4")).toBeTruthy();
    expect(within(row).getByRole("button", { name: "Download again" })).toBeTruthy();
  });

  it("retries one item and a selection", async () => {
    const user = userEvent.setup();
    renderAt(<Failures />, "/queue/failures", "/queue/failures");
    const row = (await screen.findByText(/Lesson 1/)).closest("tr")!;
    await user.click(within(row).getByRole("button", { name: "Retry now" }));
    expect(post).toHaveBeenCalledWith("/api/queues/failures/actions", {
      action: "retry",
      job_ids: [9],
    });
    await user.click(screen.getByRole("checkbox", { name: "Select Lesson 2" }));
    const bar = screen.getByRole("group", { name: "1 selected" });
    await user.click(within(bar).getByRole("button", { name: "Ignore" }));
    expect(post).toHaveBeenLastCalledWith("/api/queues/failures/actions", {
      action: "ignore",
      job_ids: [8],
    });
    expect(await screen.findByRole("status")).toBeTruthy();
  });

  it("acts on a whole signature group through the filter", async () => {
    const user = userEvent.setup();
    renderAt(
      <Failures />,
      "/queue/failures?signature=file_missing&stage=content_hash",
      "/queue/failures"
    );
    const bar = await screen.findByRole("group", { name: "All 3 matching items" });
    await user.click(within(bar).getByRole("button", { name: "Check access" }));
    expect(post).toHaveBeenCalledWith("/api/queues/failures/actions", {
      action: "check_access",
      filter: { signature: "file_missing", stage: "content_hash" },
    });
  });

  it("adds a path rule for the selection", async () => {
    const user = userEvent.setup();
    renderAt(<Failures />, "/queue/failures", "/queue/failures");
    await user.click(await screen.findByRole("checkbox", { name: "Select Lesson 1" }));
    await user.click(screen.getByRole("button", { name: "Add a path rule" }));
    const form = screen.getByRole("form", { name: "Add a path rule" });
    expect((within(form).getByLabelText("Share prefix") as HTMLInputElement).value).toBe(
      "smb://nas/Originals/"
    );
    await user.type(within(form).getByLabelText("Local folder"), "/Volumes/Originals/");
    await user.click(within(form).getByRole("button", { name: "Save the rule and retry" }));
    expect(post).toHaveBeenCalledWith("/api/queues/failures/actions", {
      action: "path_map",
      path_map: { from: "smb://nas/Originals/", to: "/Volumes/Originals/" },
      job_ids: [9],
    });
  });

  it("fixes one path", async () => {
    const user = userEvent.setup();
    renderAt(<Failures />, "/queue/failures", "/queue/failures");
    const row = (await screen.findByText(/Lesson 1/)).closest("tr")!;
    await user.click(within(row).getByRole("button", { name: "Fix path" }));
    const form = screen.getByRole("form", { name: "Fix the path of Lesson 1" });
    const input = within(form).getByLabelText("Path");
    await user.clear(input);
    await user.type(input, "smb://nas/Originals/Lessons/b.mp4");
    await user.click(within(form).getByRole("button", { name: "Save and retry" }));
    expect(post).toHaveBeenCalledWith("/api/queues/failures/actions", {
      action: "fix_path",
      job_ids: [9],
      path: "smb://nas/Originals/Lessons/b.mp4",
    });
  });

  it("shows the permission error and the last check, and checks again", async () => {
    const user = userEvent.setup();
    renderAt(<Failures />, "/queue/failures", "/queue/failures");
    const row = (await screen.findByText(/Lesson 2/)).closest("tr")!;
    await user.click(within(row).getByRole("button", { name: "Permission…" }));
    const dialog = screen.getByRole("dialog", { name: "Permission to read the file" });
    expect(within(dialog).getByText(/Could not read it from mac/)).toBeTruthy();
    expect(within(dialog).getByText(/Maktaba cannot change permissions/)).toBeTruthy();
    await user.click(within(dialog).getByRole("button", { name: "Check access" }));
    expect(post).toHaveBeenCalledWith("/api/queues/failures/actions", {
      action: "check_access",
      job_ids: [8],
    });
  });

  it("asks before it removes", async () => {
    const user = userEvent.setup();
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(false);
    renderAt(<Failures />, "/queue/failures", "/queue/failures");
    const row = (await screen.findByText(/Lesson 1/)).closest("tr")!;
    await user.click(within(row).getByRole("button", { name: "Remove" }));
    expect(confirm).toHaveBeenCalled();
    expect(post).not.toHaveBeenCalled();
    confirm.mockRestore();
  });

  it("filters by status through the URL", async () => {
    const user = userEvent.setup();
    renderAt(<Failures />, "/queue/failures", "/queue/failures");
    await screen.findByText(/Lesson 1/);
    await user.selectOptions(screen.getByLabelText("Status"), "ignored");
    await waitFor(() => expect(get.mock.calls.at(-1)![0]).toContain("status=ignored"));
  });

  it("says when loading fails", async () => {
    get.mockRejectedValue(new Error("down"));
    renderAt(<Failures />, "/queue/failures", "/queue/failures");
    expect(await screen.findByRole("alert")).toBeTruthy();
  });
});

describe("helpers", () => {
  it("suggests the share prefix of a path", () => {
    expect(sharePrefix("smb://nas/Originals/x/y.mp4")).toBe("smb://nas/Originals/");
    expect(sharePrefix("Y:/a/b.mp4")).toBe("Y:/");
    expect(sharePrefix(null)).toBe("");
  });

  it("counts what a filter matches", () => {
    const s = list.facets.status;
    expect(matchingCount(s, "")).toBe(3);
    expect(matchingCount(s, "all")).toBe(7);
    expect(matchingCount(s, "ignored")).toBe(4);
    expect(matchingCount(undefined, "")).toBeUndefined();
  });
});
