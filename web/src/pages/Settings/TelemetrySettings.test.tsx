// Settings → Privacy → Usage data (story 16.5): the app's switch opts this app
// in and out; "Delete what this app sent" calls DELETE /api/telemetry/app/{id}.
// An admin also sees the server's switch, a separate answer: turning one on
// does not turn on the other, and a viewer never sees the server's.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { ToastProvider } from "@ds/components/Toast/Toast";
import { I18nProvider } from "../../lib/i18n";

const { get, post, del, patch } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  del: vi.fn(),
  patch: vi.fn(),
}));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post, delete: del, patch } };
});

import { TelemetrySettings } from "./TelemetrySettings";
import { pseudonym, readConsent } from "../../lib/telemetry/consent";
import { resetTelemetryForTests } from "../../lib/telemetry/client";

function renderSection(isAdmin = false) {
  render(
    <I18nProvider>
      <ToastProvider>
        <MemoryRouter>
          <TelemetrySettings isAdmin={isAdmin} />
        </MemoryRouter>
      </ToastProvider>
    </I18nProvider>
  );
}

function serverStatus(sharing: boolean, enabled = true) {
  return {
    sharing,
    enabled,
    pending: 0,
    oldest_pending: null,
    last_sent_at: null,
    last_error: "",
    preview: { version: "0.3.27", library_size: "1k-10k" },
  };
}

describe("TelemetrySettings", () => {
  beforeEach(() => {
    resetTelemetryForTests();
    localStorage.clear();
    get.mockReset().mockResolvedValue(serverStatus(false));
    post.mockReset().mockResolvedValue(undefined);
    del.mockReset().mockResolvedValue(undefined);
    patch.mockReset().mockResolvedValue({});
  });

  it("is off until the user turns it on, and Delete needs something to delete", () => {
    renderSection();
    expect(screen.getByRole("switch")).not.toBeChecked();
    expect(screen.getByText("Not sharing this app's usage data")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Delete what this app sent" })).toBeDisabled();
    // a viewer is never shown the server's switch, nor is it asked for
    expect(screen.queryByTestId("server-usage-data")).toBeNull();
    expect(get).not.toHaveBeenCalled();
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
    expect(screen.getByRole("button", { name: "Delete what this app sent" })).toBeEnabled();
  });

  it("Delete what this app sent asks the home to delete under this app's id", async () => {
    const user = userEvent.setup();
    renderSection();
    await user.click(screen.getByRole("switch"));
    const p = pseudonym();
    await user.click(screen.getByRole("button", { name: "Delete what this app sent" }));
    expect(del).toHaveBeenCalledWith(`/api/telemetry/app/${p}`);
    expect(
      await screen.findByText("Asked Maktaba to delete what this app sent.")
    ).toBeInTheDocument();
    await waitFor(() => expect(pseudonym()).not.toBe(p));
  });

  it("an admin's server switch is separate from the app's, shows what is sent, and saves", async () => {
    const user = userEvent.setup();
    renderSection(true);
    const block = await screen.findByTestId("server-usage-data");
    expect(get).toHaveBeenCalledWith("/api/telemetry/server");
    const [appSwitch, serverSwitch] = screen.getAllByRole("switch");
    expect(serverSwitch).not.toBeChecked();
    await waitFor(() => expect(serverSwitch).toBeEnabled());
    expect(block).toHaveTextContent('"library_size": "1k-10k"');

    get.mockResolvedValue(serverStatus(true));
    await user.click(serverSwitch);
    expect(patch).toHaveBeenCalledWith("/api/settings", { "telemetry.share_server_data": true });
    await waitFor(() => expect(screen.getAllByRole("switch")[1]).toBeChecked());
    // the server's yes is not this app's
    expect(appSwitch).not.toBeChecked();
    expect(readConsent()).toBeNull();

    await user.click(screen.getByRole("button", { name: "Delete what this server sent" }));
    expect(post).toHaveBeenCalledWith("/api/telemetry/server/forget", {});
  });

  it("says when the server's configuration turns usage data off", async () => {
    get.mockResolvedValue(serverStatus(false, false));
    renderSection(true);
    expect(await screen.findByRole("note")).toHaveTextContent('[telemetry] enabled = "off"');
    expect(screen.getAllByRole("switch")[1]).toBeDisabled();
  });
});
