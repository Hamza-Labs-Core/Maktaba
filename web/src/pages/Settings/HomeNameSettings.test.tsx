// Settings → Account: an admin renames the home (home.name); the field shows the
// name in use (GET /api/auth/me home_name) and Arabic renders.
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

import { HomeNameSettings } from "./HomeNameSettings";

function renderField() {
  render(
    <I18nProvider>
      <ToastProvider>
        <HomeNameSettings />
      </ToastProvider>
    </I18nProvider>
  );
}

describe("HomeNameSettings", () => {
  beforeEach(() => {
    localStorage.clear();
    get.mockReset();
    patch.mockReset();
  });

  it("shows the name in use and saves a new one", async () => {
    const user = userEvent.setup();
    get.mockResolvedValueOnce({ home_name: "Sara's Mac mini" });
    get.mockResolvedValueOnce({ home_name: "Living room" });
    patch.mockResolvedValue({});
    renderField();
    const box = await screen.findByRole("textbox", { name: "Name" });
    await waitFor(() => expect(box).toHaveValue("Sara's Mac mini"));
    const save = screen.getByRole("button", { name: "Save name" });
    expect(save).toBeDisabled(); // nothing changed
    await user.clear(box);
    await user.type(box, "  Living room ");
    await user.click(save);
    expect(patch).toHaveBeenCalledWith("/api/settings", { "home.name": "Living room" });
    await waitFor(() => expect(box).toHaveValue("Living room"));
    expect(await screen.findByText("The home's name is saved.")).toBeInTheDocument();
  });

  it("an empty name goes back to the computer's", async () => {
    const user = userEvent.setup();
    get.mockResolvedValueOnce({ home_name: "Living room" });
    get.mockResolvedValueOnce({ home_name: "Sara's Mac mini" });
    patch.mockResolvedValue({});
    renderField();
    const box = await screen.findByRole("textbox", { name: "Name" });
    await waitFor(() => expect(box).toHaveValue("Living room"));
    await user.clear(box);
    await user.click(screen.getByRole("button", { name: "Save name" }));
    expect(patch).toHaveBeenCalledWith("/api/settings", { "home.name": "" });
    await waitFor(() => expect(box).toHaveValue("Sara's Mac mini"));
  });

  it("renders in Arabic", async () => {
    localStorage.setItem("mkt:locale", "ar");
    get.mockResolvedValue({ home_name: "x" });
    renderField();
    expect(await screen.findByRole("heading", { name: "اسم الجهاز الرئيسي" })).toBeInTheDocument();
  });
});
