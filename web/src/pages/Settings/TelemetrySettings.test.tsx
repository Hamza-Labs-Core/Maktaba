// Settings → Privacy → Usage analytics (story 16.5): the switch opts this device
// in and out; "Forget my device" calls DELETE /api/telemetry/devices/{pseudonym}.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { ToastProvider } from "@ds/components/Toast/Toast";
import { I18nProvider } from "../../lib/i18n";

const { get, post, del } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), del: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post, delete: del } };
});

import { TelemetrySettings } from "./TelemetrySettings";
import { pseudonym, readConsent } from "../../lib/telemetry/consent";
import { resetTelemetryForTests } from "../../lib/telemetry/client";

function renderSection() {
  render(
    <I18nProvider>
      <ToastProvider>
        <MemoryRouter>
          <TelemetrySettings />
        </MemoryRouter>
      </ToastProvider>
    </I18nProvider>
  );
}

describe("TelemetrySettings", () => {
  beforeEach(() => {
    resetTelemetryForTests();
    localStorage.clear();
    get.mockReset().mockResolvedValue({ items: [] });
    post.mockReset().mockResolvedValue(undefined);
    del.mockReset().mockResolvedValue(undefined);
  });

  it("is off until the user turns it on, and Forget needs something to forget", () => {
    renderSection();
    expect(screen.getByRole("switch")).not.toBeChecked();
    expect(screen.getByText("Not sharing usage data")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Forget my device" })).toBeDisabled();
  });

  it("turns sharing on and off (the kill switch)", async () => {
    const user = userEvent.setup();
    renderSection();
    await user.click(screen.getByRole("switch"));
    expect(screen.getByRole("switch")).toBeChecked();
    expect(readConsent()?.granted).toBe(true);
    expect(pseudonym()).not.toBeNull();
    await user.click(screen.getByRole("switch"));
    expect(screen.getByRole("switch")).not.toBeChecked();
    expect(readConsent()?.granted).toBe(false);
    // turning off deletes nothing: forgetting is its own step
    expect(del).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: "Forget my device" })).toBeEnabled();
  });

  it("Forget my device deletes this device's data on the server", async () => {
    const user = userEvent.setup();
    renderSection();
    await user.click(screen.getByRole("switch"));
    const p = pseudonym();
    await user.click(screen.getByRole("button", { name: "Forget my device" }));
    expect(del).toHaveBeenCalledWith(`/api/telemetry/devices/${p}`);
    expect(
      await screen.findByText("This device's usage data is deleted from the server.")
    ).toBeInTheDocument();
    await waitFor(() => expect(pseudonym()).not.toBe(p));
  });
});
