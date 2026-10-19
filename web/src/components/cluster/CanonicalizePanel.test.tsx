// Story 31.3 — the home's automatic path conversion on the Machines page:
// where it stands, why it waits, the last conversion, revert and resume.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "../../lib/i18n";
import { ToastProvider } from "@ds/components/Toast/Toast";
import type { CanonStatus } from "../../lib/clusterApi";

const { post } = vi.hoisted(() => ({ post: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, post } };
});

import { CanonicalizePanel } from "./CanonicalizePanel";

const status = (state: CanonStatus["state"], request: CanonStatus["request"] = null) => ({
  state,
  request,
  requested_at: null,
  requested_by: null,
});

function renderPanel(s: CanonStatus | null, onChanged = vi.fn()) {
  render(
    <I18nProvider>
      <ToastProvider>
        <CanonicalizePanel status={s} onChanged={onChanged} />
      </ToastProvider>
    </I18nProvider>
  );
  return onChanged;
}

describe("Canonical paths panel", () => {
  beforeEach(() => {
    localStorage.clear();
    post.mockReset();
  });

  it("says why it waits", () => {
    renderPanel(
      status({
        phase: "blocked",
        checked_at: "2026-10-19T10:00:00Z",
        problems: [{ code: "unmounted", detail: "/Volumes/Originals/Backup/Youtube" }],
      })
    );
    expect(screen.getByRole("status")).toHaveTextContent(/Waiting: the paths are converted/);
    expect(screen.getByText("Not mounted: /Volumes/Originals/Backup/Youtube")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Revert last conversion" })).toBeNull();
  });

  it("shows the run in progress", () => {
    renderPanel(status({ phase: "applying", run: { batch: "b1", done: 2000, total: 148593 } }));
    expect(screen.getByRole("status")).toHaveTextContent("Converting the stored paths…");
    expect(screen.getByRole("progressbar", { name: "Paths converted" })).toBeInTheDocument();
    expect(screen.getByText(/2,000 of 148,593/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Check now" })).toBeDisabled();
  });

  it("reverts the last conversion after a confirmation", async () => {
    post.mockResolvedValue(status({ phase: "done" }, "revert"));
    const changed = renderPanel(
      status({
        phase: "done",
        last: { batch: "b1", changes: 148593, finished_at: "2026-10-19T10:00:00Z" },
        servers_added: [{ name: "home-nas", addresses: ["192.168.50.94", "Home-NAS.lan"] }],
        plex_added: ["smb://home-nas/Originals/=/volume1/Originals/"],
      })
    );
    expect(screen.getByText(/Last conversion: 148,593 values/)).toBeInTheDocument();
    expect(screen.getByText(/smb:\/\/home-nas\/ \(192\.168\.50\.94/)).toBeInTheDocument();
    expect(
      screen.getByText(/smb:\/\/home-nas\/Originals\/=\/volume1\/Originals\//)
    ).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Revert last conversion" }));
    expect(post).not.toHaveBeenCalled();
    await userEvent.click(screen.getByRole("button", { name: "Revert" }));
    expect(post).toHaveBeenCalledWith("/api/cluster/storage/canonicalize", { action: "revert" });
    expect(changed).toHaveBeenCalled();
  });

  it("resumes after a revert and checks now", async () => {
    post.mockResolvedValue(status({ phase: "held", held: true }, "resume"));
    renderPanel(
      status(
        {
          phase: "held",
          held: true,
          reverted: { batch: "b1", rows: 9, at: "2026-10-19T10:00:00Z" },
        },
        "run"
      )
    );
    expect(screen.getByText(/Reverted: 9 values put back/)).toBeInTheDocument();
    expect(screen.getByText(/Asked: check now/)).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Convert automatically again" }));
    expect(post).toHaveBeenCalledWith("/api/cluster/storage/canonicalize", { action: "resume" });
    await userEvent.click(screen.getByRole("button", { name: "Check now" }));
    expect(post).toHaveBeenLastCalledWith("/api/cluster/storage/canonicalize", { action: "run" });
  });

  it("renders before the first look and in Arabic", () => {
    localStorage.setItem("mkt:locale", "ar");
    renderPanel(status({ phase: "blocked", problems: [{ code: "no_shares" }] }));
    expect(screen.getByText("المسارات الموحّدة")).toBeInTheDocument();
    expect(screen.getByText("لا توجد مشاركة شبكية موصولة على المنزل.")).toBeInTheDocument();
  });

  it("renders with no report yet", () => {
    renderPanel(null);
    expect(screen.getByRole("status")).toHaveTextContent(/The home has not looked yet/);
  });
});
