// Story 16.5: the app's one question lists what is and is never shared, says it
// goes to Maktaba and not to the home's admin, records the answer (dated) and is
// not asked again; it is a notice, not a dialog.
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
    expect(screen.getByText(/goes to Maktaba, not to this home's admin/)).toBeInTheDocument();
    expect(screen.queryByText(/library's size/)).toBeNull();
    for (const text of [
      "The app's version, the kind of device, its system and language",
      "How often each part of the app is used",
      "Error reports, with paths, file names and addresses removed",
      "File names, titles, transcripts, searches, who you are, or your IP address",
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
    await user.click(screen.getByRole("button", { name: "Share" }));
    expect(screen.queryByTestId("telemetry-prompt")).toBeNull();
    const c = readConsent();
    expect(c?.granted).toBe(true);
    expect(Number.isNaN(Date.parse(c?.at ?? ""))).toBe(false);
    expect(pseudonym()).toMatch(/^[A-Z2-7]{24}$/);
    expect(get).not.toHaveBeenCalled(); // no library requests
  });

  it("declining records it, sends nothing, and is not asked again", async () => {
    const user = userEvent.setup();
    const { unmount } = renderPrompt();
    await user.click(screen.getByRole("button", { name: "Not now" }));
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
