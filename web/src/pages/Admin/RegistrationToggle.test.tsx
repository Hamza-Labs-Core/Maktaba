// Admin → Users: the open-registration toggle reads auth.open_registration and
// PATCHes it; Arabic renders.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "../../lib/i18n";
import { ToastProvider } from "@ds/components/Toast/Toast";

const { get, patch } = vi.hoisted(() => ({ get: vi.fn(), patch: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, patch } };
});

import { RegistrationToggle, isOpen } from "./RegistrationToggle";

function renderToggle() {
  render(
    <I18nProvider>
      <ToastProvider>
        <RegistrationToggle />
      </ToastProvider>
    </I18nProvider>
  );
}

describe("RegistrationToggle", () => {
  beforeEach(() => {
    localStorage.clear();
    get.mockReset();
    patch.mockReset();
  });

  it("reads the stored value (bool or the text true)", () => {
    expect(isOpen(true)).toBe(true);
    expect(isOpen("true")).toBe(true);
    expect(isOpen(false)).toBe(false);
    expect(isOpen(undefined)).toBe(false);
  });

  it("shows closed by default and opens registration", async () => {
    const user = userEvent.setup();
    get.mockResolvedValue({});
    patch.mockResolvedValue({ "auth.open_registration": true });
    renderToggle();
    const box = await screen.findByRole("checkbox", { name: /Open registration/ });
    await waitFor(() => expect(box).toBeEnabled());
    expect(box).not.toBeChecked();
    await user.click(box);
    expect(patch).toHaveBeenCalledWith("/api/settings", { "auth.open_registration": true });
    await waitFor(() => expect(box).toBeChecked());
    expect(await screen.findByText("Registration is open.")).toBeInTheDocument();
  });

  it("closes an open registration", async () => {
    const user = userEvent.setup();
    get.mockResolvedValue({ "auth.open_registration": true });
    patch.mockResolvedValue({ "auth.open_registration": false });
    renderToggle();
    const box = await screen.findByRole("checkbox", { name: /Open registration/ });
    await waitFor(() => expect(box).toBeChecked());
    await user.click(box);
    expect(patch).toHaveBeenCalledWith("/api/settings", { "auth.open_registration": false });
    await waitFor(() => expect(box).not.toBeChecked());
  });

  it("renders in Arabic", async () => {
    localStorage.setItem("mkt:locale", "ar");
    get.mockResolvedValue({});
    renderToggle();
    expect(await screen.findByRole("checkbox", { name: /فتح التسجيل/ })).toBeInTheDocument();
  });
});
