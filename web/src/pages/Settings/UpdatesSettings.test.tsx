// Story 35.3 TC7: the automatic updates settings card - the next planned update, the
// window format and the pin are checked before saving; a good save sends the keys.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { get, put, post } = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), post: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, put, post } };
});

import { ToastProvider } from "@ds/components/Toast/Toast";
import { I18nProvider } from "../../lib/i18n";
import { UpdatesSettings } from "./UpdatesSettings";
import {
  compareVersions,
  pinProblem,
  validWindow,
  type UpdatesOverview,
} from "../../lib/updatesApi";

function overview(over: Partial<UpdatesOverview> = {}): UpdatesOverview {
  return {
    current_version: "1.5.0",
    watermark: "1.5.0",
    policy: {
      auto_install: true,
      channel: "stable",
      window: "03:00-05:00",
      window_overrun_minutes: 120,
      min_release_age_hours: 24,
      pinned_version: "",
      deferred_until: null,
      skipped_versions: [],
      node_failure_policy: "hold_platform",
      keep_versions: 3,
    },
    auto_install_available: true,
    plan: {
      target: { version: "1.6.0", tag: "v1.6.0", channel: "stable", verify_state: "verified" },
      held: false,
      stepping_stone: false,
      auto_install: true,
      due: false,
      next_run_at: "2026-10-22T03:00:00Z",
    },
    catalog: [],
    check_stale: false,
    time_zone: "Europe/London",
    ...over,
  };
}

function renderCard() {
  return render(
    <I18nProvider>
      <ToastProvider>
        <UpdatesSettings />
      </ToastProvider>
    </I18nProvider>
  );
}

beforeEach(() => {
  get.mockReset();
  put.mockReset();
  get.mockResolvedValue(overview());
  put.mockResolvedValue({ section: "updates", changed: [] });
});

describe("validators", () => {
  it("accepts windows across midnight and refuses bad ones", () => {
    expect(validWindow("03:00-05:00")).toBe(true);
    expect(validWindow("23:00-01:00")).toBe(true);
    expect(validWindow("3am")).toBe(false);
    expect(validWindow("24:00-01:00")).toBe(false);
    expect(validWindow("03:00-03:30")).toBe(false);
  });
  it("compares versions numerically and refuses a pin below the installed version", () => {
    expect(compareVersions("1.10.0", "1.9.0")).toBe(1);
    expect(compareVersions("1.5.0-rc.1", "1.5.0")).toBe(-1);
    expect(pinProblem("", "1.5.0")).toBe("");
    expect(pinProblem("latest", "1.5.0")).toBe("bad_pin");
    expect(pinProblem("1.4.9", "1.5.0")).toBe("pin_below_installed");
    expect(pinProblem("v1.5.0", "1.5.0")).toBe("");
  });
});

describe("UpdatesSettings", () => {
  it("shows the next planned update", async () => {
    renderCard();
    expect(await screen.findByTestId("updates-next")).toHaveTextContent("1.6.0");
  });

  it("refuses a bad window and a pin below the installed version before saving", async () => {
    const user = userEvent.setup();
    renderCard();
    const win = await screen.findByLabelText(/Quiet window/);
    await user.clear(win);
    await user.type(win, "3am");
    expect(await screen.findByText(/HH:MM-HH:MM, longer than 30 minutes/)).toBeInTheDocument();
    const save = screen.getByRole("button", { name: "Save update settings" });
    expect(save).toBeDisabled();

    await user.clear(win);
    await user.type(win, "23:00-01:00");
    const pin = screen.getByLabelText(/Stay at or below version/);
    await user.type(pin, "1.4.9");
    expect(await screen.findByText(/below the installed version 1.5.0/)).toBeInTheDocument();
    expect(save).toBeDisabled();
    expect(put).not.toHaveBeenCalled();

    await user.clear(pin);
    await user.type(pin, "1.6.9");
    expect(save).toBeEnabled();
    await user.click(save);
    await waitFor(() => expect(put).toHaveBeenCalledTimes(1));
    expect(put).toHaveBeenCalledWith("/api/cluster/updates/settings", {
      auto_install: true,
      channel: "stable",
      window: "23:00-01:00",
      min_release_age_hours: 24,
      pinned_version: "1.6.9",
    });
  });

  it("says when this build only checks (phase A) and when the check is stale", async () => {
    get.mockResolvedValue(overview({ auto_install_available: false, check_stale: true }));
    renderCard();
    expect(await screen.findByTestId("updates-phase-a")).toBeInTheDocument();
    expect(screen.getByRole("alert")).toHaveTextContent("14 days");
  });

  it("renders nothing without the updates API", async () => {
    get.mockRejectedValue(new Error("404"));
    const { container } = renderCard();
    await waitFor(() => expect(get).toHaveBeenCalled());
    expect(container).toBeEmptyDOMElement();
  });
});
