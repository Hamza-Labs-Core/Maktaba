// Story 16.5: the first-launch question lists what is and is never shared,
// records the answer (dated) and is not asked again; it is a notice, not a dialog.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { I18nProvider } from "../lib/i18n";

const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock("../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../lib/api")>("../lib/api");
  return { ...actual, api: { ...actual.api, get, post } };
});

import { TelemetryPrompt } from "./TelemetryPrompt";
import { pseudonym, readConsent } from "../lib/telemetry/consent";
import { queuedForTests, resetTelemetryForTests } from "../lib/telemetry/client";

function renderPrompt() {
  return render(
    <I18nProvider>
      <MemoryRouter initialEntries={["/library"]}>
        <TelemetryPrompt />
      </MemoryRouter>
    </I18nProvider>
  );
}

describe("TelemetryPrompt", () => {
  beforeEach(() => {
    resetTelemetryForTests();
    localStorage.clear();
    get.mockReset().mockResolvedValue({ items: [] });
    post.mockReset().mockResolvedValue(undefined);
  });

  it("asks on first launch, listing what is shared and what never is", () => {
    renderPrompt();
    expect(screen.getByRole("heading", { name: "Help improve Maktaba" })).toBeInTheDocument();
    expect(screen.queryByRole("dialog")).toBeNull();
    for (const text of [
      "The app's version, your operating system and language",
      "Your library's size, as a rough range",
      "How often each part of the app is used",
      "Error reports, with file paths and addresses removed",
      "File names, transcripts, searches, who you are, or your IP address",
    ]) {
      expect(screen.getByText(text)).toBeInTheDocument();
    }
    // asked, not answered: nothing collected yet
    expect(pseudonym()).toBeNull();
    expect(queuedForTests()).toBe(0);
  });

  it("opting in records the dated consent and starts collecting", async () => {
    const user = userEvent.setup();
    renderPrompt();
    await user.click(screen.getByRole("button", { name: "Share usage data" }));
    expect(screen.queryByTestId("telemetry-prompt")).toBeNull();
    const c = readConsent();
    expect(c?.granted).toBe(true);
    expect(Number.isNaN(Date.parse(c?.at ?? ""))).toBe(false);
    expect(pseudonym()).toMatch(/^[A-Z2-7]{16}$/);
  });

  it("declining records it, sends nothing, and is not asked again", async () => {
    const user = userEvent.setup();
    const { unmount } = renderPrompt();
    await user.click(screen.getByRole("button", { name: "No thanks" }));
    expect(screen.queryByTestId("telemetry-prompt")).toBeNull();
    expect(readConsent()?.granted).toBe(false);
    expect(pseudonym()).toBeNull();
    unmount();
    renderPrompt();
    expect(screen.queryByTestId("telemetry-prompt")).toBeNull();
    expect(get).not.toHaveBeenCalled();
    expect(post).not.toHaveBeenCalled();
  });
});
