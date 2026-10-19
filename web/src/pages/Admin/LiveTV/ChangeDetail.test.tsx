// Story 33.11 TC2 / TC3: the change detail - each tab renders its fixture (renumber,
// add, remove, schedule diff, settings diff, art, checks failures first, history with
// the audit rows), the actions follow allowed_actions (rollback: warm / cold / none, 33.9),
// and the approve dialog (expected switch times, the param-change acknowledgement, the
// second-admin rule).
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, within, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { I18nProvider } from "../../../lib/i18n";

const { get, post, patch } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), patch: vi.fn() }));
vi.mock("../../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../../lib/api")>("../../../lib/api");
  return { ...actual, api: { ...actual.api, get, post, patch } };
});
const me = vi.hoisted(() => ({ id: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" }));
vi.mock("../../../lib/auth", () => ({
  useAuth: () => ({ user: { id: me.id, username: "mahmoud", is_admin: true } }),
}));

import { AdminTvChangeDetail } from "./ChangeDetail";
import {
  ADMIN_ID,
  OTHER_ADMIN_ID,
  answer,
  changeList,
  draftChange,
  failedChange,
  liveChange,
  readyChange,
} from "./changeFixtures";
import type { TvChangeDetail } from "../../../lib/tvChanges";

function renderDetail(id: string) {
  return render(
    <I18nProvider>
      <MemoryRouter initialEntries={[`/admin/live-tv/changes/${id}`]}>
        <Routes>
          <Route path="/admin/live-tv/changes/:id" element={<AdminTvChangeDetail />} />
        </Routes>
      </MemoryRouter>
    </I18nProvider>
  );
}

let override: Partial<Record<string, TvChangeDetail>> = {};
beforeEach(() => {
  override = {};
  me.id = ADMIN_ID;
  get.mockReset();
  post.mockReset();
  patch.mockReset();
  get.mockImplementation(async (path: string) => {
    const m = path.match(/^\/api\/tv\/changes\/([^/?]+)$/);
    if (m && override[m[1]!]) return override[m[1]!];
    return answer(path);
  });
});
afterEach(() => vi.useRealTimers());

const actionIds = () =>
  screen.getAllByTestId(/^action-[a-z]+$/).map((b) => b.dataset.testid!.replace("action-", ""));

describe("Change detail", () => {
  it("shows the change, its checks summary and the actions of its state in a fixed order", async () => {
    renderDetail(readyChange.id);
    expect(await screen.findByRole("heading", { level: 1 })).toHaveTextContent(
      "#42 Ramadan grid and the Seerah channel"
    );
    expect(screen.getByTestId("progress")).toHaveTextContent("checked; waiting for an approval");
    expect(actionIds()).toEqual(["verify", "approve", "schedule", "cancel"]);
  });

  it("the Lineup tab: added, removed, renumbered 105 → 115, renamed, definition changes", async () => {
    renderDetail(readyChange.id);
    const lineup = await screen.findByTestId("lineup-diff");
    expect(lineup).toHaveTextContent("+ 130 Seerah");
    expect(lineup).toHaveTextContent("− 150 Kids classics");
    expect(lineup).toHaveTextContent("105 → 115");
    expect(lineup).toHaveTextContent("Quran → Quran recitations");
    expect(lineup).toHaveTextContent("grid and rules");
  });

  it("the Schedules tab: the regenerated channels; a channel opens before / after side by side", async () => {
    renderDetail(readyChange.id);
    await screen.findByTestId("lineup-diff");
    const user = userEvent.setup();
    await user.click(screen.getByRole("tab", { name: "Schedules" }));
    const sched = await screen.findByTestId("schedule-diff");
    expect(sched).toHaveTextContent("3 channels get a new schedule");
    expect(sched).toHaveTextContent("new channel");
    await user.click(within(sched).getByRole("button", { name: /Quran recitations/ }));
    expect(await screen.findByText("Taraweeh")).toBeInTheDocument();
    expect(get).toHaveBeenCalledWith(expect.stringContaining(`schedule-diff?channel=`));
    const before = screen.getByRole("list", { name: "On the air now" });
    const after = screen.getByRole("list", { name: "After the change" });
    expect(within(before).getByText("Tafsir 2").closest("li")).toHaveClass("mkt-tvx-del");
    expect(within(after).getByText("Taraweeh").closest("li")).toHaveClass("mkt-tvx-add");
    // the shared programme is not marked
    expect(within(after).getByText("Tafsir 3").closest("li")?.className).toBe("");
  });

  it("the Settings tab: the key diff of tv_output", async () => {
    renderDetail(readyChange.id);
    await screen.findByTestId("lineup-diff");
    await userEvent.setup().click(screen.getByRole("tab", { name: "Settings" }));
    const s = await screen.findByTestId("settings-diff");
    expect(s).toHaveTextContent("TV output (encoder)");
    expect(within(s).getByText("30")).toHaveClass("mkt-tvx-del");
    expect(within(s).getByText("25")).toHaveClass("mkt-tvx-add");
  });

  it("the Binary & art tab: before / after thumbnails from the live channel and the release", async () => {
    renderDetail(readyChange.id);
    await screen.findByTestId("lineup-diff");
    await userEvent.setup().click(screen.getByRole("tab", { name: "Binary & art" }));
    const art = await screen.findByTestId("art-diff");
    const imgs = within(art).getAllByRole("img");
    expect(imgs[0]).toHaveAttribute(
      "src",
      expect.stringContaining("/api/channels/00000000-0000-4000-8000-0000000000b2/art/logo")
    );
    expect(imgs[1]).toHaveAttribute(
      "src",
      expect.stringContaining(`?release=${readyChange.release_id}`)
    );
    expect(art).toHaveTextContent("The TV binary stays at 0.33.0.");
  });

  it("the Checks tab: failures first, then warnings, passes, skipped", async () => {
    override[readyChange.id] = {
      ...readyChange,
      verify_report: {
        ...readyChange.verify_report,
        checks: [...readyChange.verify_report.checks!, ...failedChange.verify_report.checks!],
      },
    };
    renderDetail(readyChange.id);
    await screen.findByTestId("lineup-diff");
    await userEvent.setup().click(screen.getByRole("tab", { name: "Checks" }));
    const rows = within(await screen.findByTestId("checks"))
      .getAllByRole("row")
      .slice(1);
    expect(rows.map((r) => r.dataset.result)).toEqual([
      "fail",
      "warn",
      "pass",
      "pass",
      "pass",
      "pass",
      "skip",
    ]);
    expect(rows[0]).toHaveTextContent("Programme on the air kept");
    expect(rows[0]).toHaveTextContent("the new schedule would cut the programme on the air");
    expect(rows[1]).toHaveTextContent("130 Seerah");
  });

  it("the History tab: the events with their actors and the audit rows", async () => {
    renderDetail(readyChange.id);
    await screen.findByTestId("lineup-diff");
    await userEvent.setup().click(screen.getByRole("tab", { name: "History" }));
    const h = await screen.findByTestId("history");
    expect(h).toHaveTextContent("Created");
    expect(h).toHaveTextContent("Checks passed");
    expect(h).toHaveTextContent("(Verifying → Ready)");
    const audit = within(h).getByTestId("audit");
    expect(audit).toHaveTextContent("mahmoud");
    expect(audit).toHaveTextContent("the system");
  });

  it("a draft: what it holds, Stage among its actions", async () => {
    renderDetail(draftChange.id);
    const p = await screen.findByTestId("pending-summary");
    expect(p).toHaveTextContent("101 Quran recitations");
    expect(p).toHaveTextContent("TV output (encoder)");
    expect(actionIds()).toEqual(["stage", "approve", "schedule", "cancel"]);
    post.mockResolvedValueOnce({
      ...draftChange,
      diff: { ...draftChange.diff, stage: { requested_at: "x" } },
    });
    await userEvent.setup().click(screen.getByTestId("action-stage"));
    expect(post).toHaveBeenCalledWith(`/api/tv/changes/${draftChange.id}/stage`);
    expect(await screen.findByRole("status")).toHaveTextContent("Staging");
  });

  it("a live change: Roll back says warm (instant) and the minutes left, and rolls back", async () => {
    renderDetail(liveChange.id);
    await screen.findByTestId("change-detail");
    expect(await screen.findByTestId("rollback-mode")).toHaveTextContent(
      "Warm (instant) — 12 min left"
    );
    const btn = screen.getByTestId("action-rollback");
    expect(btn).toBeEnabled();
    expect(screen.getByRole("link", { name: "the pipeline job" })).toHaveAttribute(
      "href",
      "/queue/write_channels"
    );
    post.mockResolvedValueOnce({ mode: "warm", change_id: liveChange.id, op_id: 7 });
    const user = userEvent.setup();
    await user.click(btn);
    const dialog = await screen.findByRole("dialog");
    expect(dialog).toHaveTextContent("Every channel switches back to the previous version now");
    await user.click(within(dialog).getByRole("button", { name: "Roll back" }));
    expect(post).toHaveBeenCalledWith("/api/tv/rollback", {});
    expect(await screen.findByRole("status")).toHaveTextContent("Rolling back");
  });

  it("Roll back is disabled with the reason when there is no rollback point", async () => {
    get.mockImplementation(async (path: string) =>
      path === "/api/tv/rollback" ? { mode: "none", reason: "retention_expired" } : answer(path)
    );
    renderDetail(liveChange.id);
    await screen.findByTestId("change-detail");
    await waitFor(() => expect(screen.getByTestId("action-rollback")).toBeDisabled());
    await userEvent.setup().hover(screen.getByTestId("action-rollback-wrap"));
    expect(await screen.findByRole("tooltip")).toHaveTextContent("The rollback point expired");
  });

  it("a failed change has no actions", async () => {
    renderDetail(failedChange.id);
    await screen.findByTestId("change-detail");
    expect(screen.queryAllByTestId(/^action-[a-z]+$/)).toHaveLength(0);
    expect(screen.getByText("Nothing to do in this state.")).toBeInTheDocument();
  });

  it("cancel asks first", async () => {
    renderDetail(readyChange.id);
    await screen.findByTestId("change-detail");
    const user = userEvent.setup();
    await user.click(screen.getByTestId("action-cancel"));
    expect(post).not.toHaveBeenCalled();
    post.mockResolvedValueOnce({ ...readyChange, state: "cancelled", allowed_actions: [] });
    await user.click(screen.getByRole("button", { name: "Cancel the change" }));
    expect(post).toHaveBeenCalledWith(`/api/tv/changes/${readyChange.id}/cancel`);
  });
});

describe("Approve dialog (TC3)", () => {
  it("shows the expected switch times and approves for as soon as possible", async () => {
    renderDetail(readyChange.id);
    await screen.findByTestId("change-detail");
    const user = userEvent.setup();
    await user.click(screen.getByTestId("action-approve"));
    const b = await screen.findByTestId("boundaries");
    await waitFor(() => expect(b).toHaveTextContent("Tafsir 1"));
    expect(b).toHaveTextContent("(longest wait: 30 min)");
    expect(b).toHaveTextContent("115 Hadith");
    post.mockResolvedValueOnce({
      ...readyChange,
      state: "approved",
      allowed_actions: ["promote", "schedule", "cancel"],
    });
    await user.click(screen.getByTestId("dialog-confirm"));
    expect(post).toHaveBeenCalledWith(`/api/tv/changes/${readyChange.id}/approve`, {
      swap_at: null,
    });
    expect(await screen.findByRole("status")).toHaveTextContent("Approved.");
  });

  it("asks for the parameter-change acknowledgement when verification flags it", async () => {
    override[readyChange.id] = {
      ...readyChange,
      verify_report: {
        checks: [
          {
            check: "push.continuity",
            channel_id: readyChange.diff.schedules![0]!.id,
            result: "fail",
            value: "param_change",
          },
        ],
      },
    };
    renderDetail(readyChange.id);
    await screen.findByTestId("change-detail");
    const user = userEvent.setup();
    await user.click(screen.getByTestId("action-approve"));
    const confirm = await screen.findByTestId("dialog-confirm");
    expect(confirm).toBeDisabled();
    await user.click(screen.getByRole("checkbox", { name: /pushed channel/ }));
    expect(confirm).toBeEnabled();
    post.mockResolvedValueOnce({ ...readyChange, state: "approved" });
    await user.click(confirm);
    expect(post).toHaveBeenCalledWith(`/api/tv/changes/${readyChange.id}/approve`, {
      swap_at: null,
      ack_param_change: true,
    });
  });

  it("the second-admin rule: the author cannot approve; another admin can", async () => {
    get.mockImplementation(async (path: string) =>
      path.startsWith("/api/tv/changes?")
        ? { ...changeList, config: { ...changeList.config, require_second_admin: true } }
        : answer(path)
    );
    renderDetail(readyChange.id);
    await screen.findByTestId("change-detail");
    const user = userEvent.setup();
    await waitFor(() =>
      expect(get).toHaveBeenCalledWith(expect.stringMatching(/^\/api\/tv\/changes\?/))
    );
    await user.click(screen.getByTestId("action-approve"));
    expect(await screen.findByTestId("second-admin")).toHaveTextContent("you made it");
    expect(screen.getByTestId("dialog-confirm")).toBeDisabled();
  });

  it("schedules a swap time", async () => {
    me.id = OTHER_ADMIN_ID;
    renderDetail(readyChange.id);
    await screen.findByTestId("change-detail");
    const user = userEvent.setup();
    await user.click(screen.getByTestId("action-schedule"));
    await user.click(screen.getByRole("radio", { name: "At a set time" }));
    patch.mockResolvedValueOnce({ ...readyChange, swap_at: "2030-01-01T03:30:00Z" });
    const input = document.querySelector<HTMLInputElement>('input[type="datetime-local"]')!;
    await user.clear(input);
    await user.type(input, "2030-01-01T03:30");
    await user.click(screen.getByTestId("dialog-confirm"));
    expect(patch).toHaveBeenCalledWith(
      `/api/tv/changes/${readyChange.id}`,
      expect.objectContaining({
        swap_wait: "boundary",
        swap_at: expect.stringMatching(/^2030-01-01T/),
      })
    );
  });
});
