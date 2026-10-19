// Admin password reset on the Users page (no mailer, so no self-service
// "forgot password"): a temporary password shown once, or a typed one.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "../../lib/i18n";
import { ToastProvider } from "@ds/components/Toast/Toast";

const { post } = vi.hoisted(() => ({ post: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, post } };
});
vi.mock("../../lib/auth", () => ({
  useAuth: () => ({ user: { id: "admin-1", username: "admin", is_admin: true } }),
}));

import { AdminUsers } from "./Users";

const bob = { id: "22222222-2222-2222-2222-222222222222", username: "bob", is_admin: false };

function renderUsers() {
  return render(
    <I18nProvider>
      <ToastProvider>
        <AdminUsers />
      </ToastProvider>
    </I18nProvider>
  );
}

async function openReset() {
  const user = userEvent.setup();
  renderUsers();
  await user.click(screen.getByRole("button", { name: "Reset password" }));
  const dialog = await screen.findByRole("dialog");
  return { user, dialog };
}

describe("AdminUsers — reset password", () => {
  beforeEach(() => {
    localStorage.setItem("mkt:admin:knownUsers", JSON.stringify([bob]));
  });
  afterEach(() => {
    localStorage.clear();
    vi.clearAllMocks();
  });

  it("makes a temporary password and shows it once", async () => {
    post.mockResolvedValue({
      temporary_password: "abcd-EFGH-jkmn-PQRS",
      must_change_password: true,
      sessions_revoked: 2,
    });
    const { user, dialog } = await openReset();
    expect(within(dialog).getByLabelText(/Make a temporary password/)).toBeChecked();
    await user.click(within(dialog).getByRole("button", { name: "Reset password" }));

    await waitFor(() =>
      expect(post).toHaveBeenCalledWith(`/api/users/${bob.id}/reset-password`, {
        mode: "temporary",
      })
    );
    const shown = await screen.findByDisplayValue("abcd-EFGH-jkmn-PQRS");
    expect(shown).toHaveAttribute("readonly");
    expect(screen.getByText(/shown only now/)).toBeInTheDocument();
    expect(await screen.findByText("Password reset. 2 sign-ins ended.")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Done" }));
    expect(screen.queryByDisplayValue("abcd-EFGH-jkmn-PQRS")).not.toBeInTheDocument();
  });

  it("sets a typed password, optionally asking for a change at next sign-in", async () => {
    post.mockResolvedValue({ must_change_password: true, sessions_revoked: 0 });
    const { user, dialog } = await openReset();
    await user.click(within(dialog).getByLabelText("Type the new password"));
    const submit = within(dialog).getByRole("button", { name: "Reset password" });

    const field = within(dialog).getByLabelText(/New password/);
    await user.type(field, "short");
    expect(submit).toBeDisabled();
    await user.type(field, "-but-longer");
    await user.click(within(dialog).getByLabelText(/Ask them to change it/));
    expect(submit).toBeEnabled();
    await user.click(submit);

    await waitFor(() =>
      expect(post).toHaveBeenCalledWith(`/api/users/${bob.id}/reset-password`, {
        mode: "set",
        password: "short-but-longer",
        must_change: true,
      })
    );
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });

  it("reports a failed reset and keeps the dialog open", async () => {
    const { ApiError } = await import("../../lib/api");
    post.mockRejectedValue(
      new ApiError({
        type: "about:blank",
        title: "not found",
        status: 404,
        detail: "user not found",
      })
    );
    const { user, dialog } = await openReset();
    await user.click(within(dialog).getByRole("button", { name: "Reset password" }));
    expect(await screen.findByText("user not found")).toBeInTheDocument();
    expect(screen.getByRole("dialog")).toBeInTheDocument();
  });
});
