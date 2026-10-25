// Story 35.5 AC8 on the Updates page: the home's backups, and putting one back only once
// the backup's name is typed (every change since it is lost); a refusal is said in the
// page's language by its code.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post } };
});

import { ApiError } from "../../lib/api";
import { I18nProvider } from "../../lib/i18n";
import { ToastProvider } from "@ds/components/Toast/Toast";
import en from "../../i18n/en.json";
import ar from "../../i18n/ar.json";
import { UpdatesBackups } from "./UpdatesBackups";

const NAME = "pre-v0.2.0-to-v0.3.0.dump";

const list = {
  running: "0.3.0",
  kept: [
    { version: "0.2.0", schema: 140 },
    { version: "0.3.0", schema: 149, running: true },
  ],
  backups: [
    {
      name: "maktaba-2026-10-23_0300.dump",
      bytes: 3 * 1024 ** 2,
      kind: "daily",
      at: "2026-10-23T03:00:00Z",
    },
    {
      name: NAME,
      bytes: 2 * 1024 ** 3,
      kind: "update",
      from: "0.2.0",
      to: "0.3.0",
      at: "2026-10-20T03:00:00Z",
    },
  ],
};
const plan = {
  backup: NAME,
  schema: 140,
  release: "0.2.0",
  running: "0.3.0",
  at: "2026-10-20T03:00:00Z",
};

function renderIt(lang?: string) {
  if (lang) localStorage.setItem("mkt:locale", lang);
  return render(
    <I18nProvider>
      <ToastProvider>
        <UpdatesBackups />
      </ToastProvider>
    </I18nProvider>
  );
}

beforeEach(() => {
  get.mockReset();
  post.mockReset();
  localStorage.clear();
  get.mockImplementation((path: string) => {
    if (path === "/api/cluster/updates/backups") return Promise.resolve(list);
    if (path === `/api/cluster/updates/backups/${NAME}/restore`) return Promise.resolve(plan);
    return Promise.reject(new Error("unexpected " + path));
  });
});

describe("UpdatesBackups", () => {
  it("lists the backups by what they are", async () => {
    renderIt();
    const row = await screen.findByTestId(`updates-backup-${NAME}`);
    expect(row).toHaveTextContent(
      en["updates.backups.kind.update"].replace("{from}", "0.2.0").replace("{to}", "0.3.0")
    );
    expect(row).toHaveTextContent("2.0 GiB");
    expect(screen.getByTestId("updates-backup-maktaba-2026-10-23_0300.dump")).toHaveTextContent(
      en["updates.backups.kind.daily"]
    );
  });

  it("restores only once the backup's name is typed, then says the home restarts", async () => {
    post.mockResolvedValue(plan);
    renderIt();
    const user = userEvent.setup();
    const row = await screen.findByTestId(`updates-backup-${NAME}`);
    await user.click(within(row).getByRole("button", { name: en["updates.backups.restore"] }));
    expect(await screen.findByTestId("updates-backups-plan")).toHaveTextContent("0.2.0");
    const dialog = screen.getByRole("dialog");
    const go = within(dialog).getByRole("button", { name: en["updates.backups.restore"] });
    expect(go).toBeDisabled();
    await user.type(within(dialog).getByRole("textbox"), "pre-v0.2.0");
    expect(go).toBeDisabled();
    await user.clear(within(dialog).getByRole("textbox"));
    await user.type(within(dialog).getByRole("textbox"), NAME);
    expect(go).toBeEnabled();
    await user.click(go);
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith(`/api/cluster/updates/backups/${NAME}/restore`, {
        confirm: NAME,
      })
    );
    expect(
      await screen.findByText(en["updates.backups.restoreDone"].replace("{release}", "0.2.0"))
    ).toBeInTheDocument();
  });

  it("says a refusal in the page's language", async () => {
    get.mockImplementation((path: string) => {
      if (path === "/api/cluster/updates/backups") return Promise.resolve(list);
      return Promise.reject(
        new ApiError({
          type: "about:blank#restore_no_release",
          title: "conflict",
          status: 409,
          detail: "install 0.2.0 by hand",
          code: "restore_no_release",
        })
      );
    });
    renderIt("ar");
    const user = userEvent.setup();
    const row = await screen.findByTestId(`updates-backup-${NAME}`);
    await user.click(within(row).getByRole("button", { name: ar["updates.backups.restore"] }));
    expect(
      await screen.findByText(ar["updates.backups.refused.restore_no_release"], { exact: false })
    ).toBeInTheDocument();
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("is not shown where the home's server cannot restore", async () => {
    get.mockRejectedValue(new Error("503"));
    const { container } = renderIt();
    await waitFor(() => expect(get).toHaveBeenCalled());
    expect(container.querySelector("[data-testid=updates-backups]")).toBeNull();
  });

  it("names a restore under way and offers no other", async () => {
    get.mockImplementation(() => Promise.resolve({ ...list, restoring: NAME }));
    renderIt();
    expect(await screen.findByTestId("updates-backups-restoring")).toHaveTextContent(NAME);
    for (const b of screen.getAllByRole("button", { name: en["updates.backups.restore"] }))
      expect(b).toBeDisabled();
  });
});
