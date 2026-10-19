// After an admin reset to a temporary password, RequireAuth shows only
// the change-password form until a new password is saved.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { I18nProvider } from "./i18n";

const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock("./api", async () => {
  const actual = await vi.importActual<typeof import("./api")>("./api");
  return { ...actual, api: { ...actual.api, get, post } };
});
vi.mock("./sw", () => ({ purgeApiCacheOnLogout: vi.fn().mockResolvedValue(undefined) }));

import { AuthProvider, RequireAuth } from "./auth";

function renderApp() {
  return render(
    <I18nProvider>
      <AuthProvider>
        <MemoryRouter>
          <RequireAuth>
            <p>the library</p>
          </RequireAuth>
        </MemoryRouter>
      </AuthProvider>
    </I18nProvider>
  );
}

describe("RequireAuth — required password change", () => {
  beforeEach(() => {
    get.mockReset();
    post.mockReset();
  });

  it("shows only the change form, then the app once the password is changed", async () => {
    get.mockResolvedValue({
      user: { id: "u1", username: "bob", is_admin: false, must_change_password: true },
    });
    post.mockResolvedValue(undefined);
    const user = userEvent.setup();
    renderApp();

    expect(
      await screen.findByRole("heading", { name: "Choose a new password" })
    ).toBeInTheDocument();
    expect(screen.queryByText("the library")).not.toBeInTheDocument();

    const save = screen.getByRole("button", { name: "Save the new password" });
    await user.type(screen.getByLabelText("Temporary password"), "abcd-EFGH-jkmn-PQRS");
    await user.type(screen.getByLabelText("New password"), "abcd-EFGH-jkmn-PQRS");
    await user.type(screen.getByLabelText("Confirm password"), "abcd-EFGH-jkmn-PQRS");
    expect(
      screen.getByText("Choose a password that is not the temporary one.")
    ).toBeInTheDocument();
    expect(save).toBeDisabled();

    await user.clear(screen.getByLabelText("New password"));
    await user.clear(screen.getByLabelText("Confirm password"));
    await user.type(screen.getByLabelText("New password"), "my-own-password");
    await user.type(screen.getByLabelText("Confirm password"), "my-own-password");
    await user.click(save);

    await waitFor(() =>
      expect(post).toHaveBeenCalledWith("/api/me/change-password", {
        current_password: "abcd-EFGH-jkmn-PQRS",
        new_password: "my-own-password",
      })
    );
    expect(await screen.findByText("the library")).toBeInTheDocument();
  });

  it("says so when the temporary password is wrong", async () => {
    const { ApiError } = await import("./api");
    get.mockResolvedValue({
      user: { id: "u1", username: "bob", is_admin: false, must_change_password: true },
    });
    post.mockRejectedValue(
      new ApiError({ type: "x", title: "invalid credentials", status: 401, detail: "wrong" })
    );
    const user = userEvent.setup();
    renderApp();
    await user.type(await screen.findByLabelText("Temporary password"), "nope-nope");
    await user.type(screen.getByLabelText("New password"), "my-own-password");
    await user.type(screen.getByLabelText("Confirm password"), "my-own-password");
    await user.click(screen.getByRole("button", { name: "Save the new password" }));
    expect(await screen.findByText("The temporary password is not right.")).toBeInTheDocument();
    expect(screen.queryByText("the library")).not.toBeInTheDocument();
  });

  it("renders the app straight away without the flag", async () => {
    get.mockResolvedValue({ user: { id: "u1", username: "bob", is_admin: false } });
    renderApp();
    expect(await screen.findByText("the library")).toBeInTheDocument();
  });
});
