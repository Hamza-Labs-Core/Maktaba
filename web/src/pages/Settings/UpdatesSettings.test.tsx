// Story 35.3 TC7: the automatic updates settings card - the next planned update, the
// window format and the pin are checked before saving; a good save sends the keys.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { get, put, post } = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), post: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, put, post } };
});

import { ToastProvider } from "@ds/components/Toast/Toast";
import { ApiError } from "../../lib/api";
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
  it("orders versions as the server does: prereleases by identifier, a checkout's build after its tag", () => {
    expect(compareVersions("1.5.0-rc.10", "1.5.0-rc.9")).toBe(1);
    expect(compareVersions("1.5.0-beta", "1.5.0-rc")).toBe(-1);
    expect(compareVersions("0.2.0-12-gabc1234", "0.2.0")).toBe(1);
    expect(compareVersions("0.2.0-12-gabc1234", "0.2.1")).toBe(-1);
    expect(compareVersions("v0.2.0-dirty", "v0.2.0")).toBe(1);
    expect(compareVersions("v0.2.0-dirty", "v0.2.0-1-gabc1234")).toBe(-1);
    expect(compareVersions("1.0.0-rc.1-3-g1a2b3c4d", "1.0.0")).toBe(-1);
    // the server refuses a pin below a checkout's build of that release
    expect(pinProblem("0.2.0", "0.2.0-12-gabc1234")).toBe("pin_below_installed");
    expect(pinProblem("0.2.1", "0.2.0-12-gabc1234")).toBe("");
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

describe("UpdatesSettings (test-cases/settings-about.md)", () => {
  const next = () => screen.findByTestId("updates-next");
  const target = overview().plan.target;

  it("SAB-15: the next update texts (at / held / waiting / none)", async () => {
    const at = new Date("2026-10-22T03:00:00Z").toLocaleString();
    const cases: [Partial<UpdatesOverview["plan"]>, string][] = [
      [{}, `Version 1.6.0 installs on ${at}.`],
      [{ held: true }, "Version 1.6.0 waits for your approval (Live TV -> Changes)."],
      [
        { next_run_at: undefined, waiting: "release_age" },
        "Version 1.6.0 installs once it is old enough.",
      ],
      [{ next_run_at: undefined }, "Version 1.6.0 is available; automatic install is off."],
      // a reason this build has no text for: a generic line, not the raw key
      [{ next_run_at: undefined, waiting: "something_new" }, "Version 1.6.0 is available."],
      [{ target: undefined }, "No update planned: you are up to date."],
    ];
    for (const [plan, text] of cases) {
      get.mockResolvedValue(overview({ plan: { ...overview().plan, target, ...plan } }));
      const { unmount } = renderCard();
      expect(await next()).toHaveTextContent(text);
      unmount();
    }
  });

  it("SAB-16: the window, age and pin are checked before saving", async () => {
    const user = userEvent.setup();
    renderCard();
    const win = await screen.findByLabelText(/Quiet window/);
    const age = screen.getByLabelText(/Release age/);
    const pin = screen.getByLabelText(/Stay at or below version/);
    const save = screen.getByRole("button", { name: "Save update settings" });
    const badWindow = /HH:MM-HH:MM, longer than 30 minutes/;

    for (const w of ["03:00-03:30", "24:00-01:00", "3am"]) {
      await user.clear(win);
      await user.type(win, w);
      expect(screen.getByText(badWindow)).toBeInTheDocument();
      expect(win).toHaveAttribute("aria-invalid", "true");
      expect(win.getAttribute("aria-describedby")).toBeTruthy();
      expect(save).toBeDisabled();
    }
    await user.clear(win);
    await user.type(win, " 23:00-01:00 ");
    expect(screen.queryByText(badWindow)).not.toBeInTheDocument();

    for (const a of ["", "-1", "1.5", "abc"]) {
      await user.clear(age);
      if (a) await user.type(age, a);
      expect(screen.getByText("Write a whole number of hours, 0 or more.")).toBeInTheDocument();
      expect(save).toBeDisabled();
    }
    await user.clear(age);
    await user.type(age, "0");
    expect(screen.queryByText(/whole number of hours/)).not.toBeInTheDocument();

    const pins: [string, string | null][] = [
      ["latest", "Write a version such as 1.5.9."],
      ["1.4.9", "A pin cannot be below the installed version 1.5.0."],
      ["v1.5.0", null],
      ["1.6.0-rc.1", null],
      ["", null],
    ];
    for (const [p, err] of pins) {
      await user.clear(pin);
      if (p) await user.type(pin, p);
      if (err) {
        expect(screen.getByText(err)).toBeInTheDocument();
        expect(save).toBeDisabled();
      } else {
        expect(save).toBeEnabled();
      }
    }
    expect(put).not.toHaveBeenCalled();
    await user.click(save);
    await waitFor(() => expect(put).toHaveBeenCalledTimes(1));
    expect(put.mock.calls[0][1]).toMatchObject({ window: "23:00-01:00", min_release_age_hours: 0 });
  });

  it("SAB-17: a save toasts success and reloads; errors toast and keep the draft", async () => {
    const user = userEvent.setup();
    renderCard();
    await user.selectOptions(await screen.findByLabelText("Release channel"), "beta");
    await user.click(screen.getByRole("switch", { name: /Install updates automatically/ }));
    const save = screen.getByRole("button", { name: "Save update settings" });

    // (e) pending, then (a) success
    let done!: (v: unknown) => void;
    put.mockReturnValueOnce(new Promise((r) => (done = r)));
    await user.click(save);
    expect(save).toBeDisabled();
    expect(put.mock.calls[0][1]).toMatchObject({ channel: "beta", auto_install: false });
    get.mockClear();
    get.mockResolvedValue(
      overview({ policy: { ...overview().policy, channel: "beta", auto_install: false } })
    );
    await act(async () => done({ section: "updates", changed: ["channel"] }));
    expect(await screen.findByRole("status")).toHaveTextContent("Update settings saved.");
    expect(get).toHaveBeenCalledWith("/api/cluster/updates");
    await waitFor(() => expect(save).toBeEnabled());

    // (b) 400, (c) 403, (d) network
    const fails: [unknown, string][] = [
      [
        new ApiError({
          type: "about:blank",
          title: "Bad Request",
          status: 400,
          detail: "bad_window",
        }),
        "Bad Request: bad_window",
      ],
      [new ApiError({ type: "about:blank", title: "Forbidden", status: 403 }), "Forbidden"],
      [new TypeError("Failed to fetch"), "Something went wrong."],
    ];
    await user.selectOptions(screen.getByLabelText("Release channel"), "stable");
    for (const [err, text] of fails) {
      put.mockRejectedValueOnce(err);
      await user.click(save);
      expect(await screen.findByText(text)).toBeInTheDocument();
      expect(screen.getByLabelText("Release channel")).toHaveValue("stable");
      expect(save).toBeEnabled();
    }
  });

  it("SAB-18: plan null or missing shows No update planned and the form", async () => {
    const noPlan = overview();
    delete (noPlan as Partial<UpdatesOverview>).plan;
    for (const o of [overview({ plan: null as unknown as UpdatesOverview["plan"] }), noPlan]) {
      get.mockResolvedValue(o);
      const { unmount } = renderCard();
      expect(await next()).toHaveTextContent("No update planned: you are up to date.");
      expect(screen.getByRole("button", { name: "Save update settings" })).toBeInTheDocument();
      unmount();
    }
  });

  it("SAB-19: null lists and policy fields do not crash the card", async () => {
    const errors = vi.spyOn(console, "error").mockImplementation(() => {});
    const base = overview();
    const variants: [string, UpdatesOverview][] = [
      ["catalog null", { ...base, catalog: null as unknown as [] }],
      ["catalog missing", { ...base, catalog: undefined as unknown as [] }],
      [
        "skipped null",
        { ...base, policy: { ...base.policy, skipped_versions: null as unknown as [] } },
      ],
      [
        "pin null",
        { ...base, policy: { ...base.policy, pinned_version: null as unknown as string } },
      ],
      ["window null", { ...base, policy: { ...base.policy, window: null as unknown as string } }],
      ["time zone missing", { ...base, time_zone: undefined as unknown as string }],
    ];
    for (const [name, o] of variants) {
      get.mockResolvedValue(o);
      const { unmount } = renderCard();
      expect(await next(), name).toBeInTheDocument();
      if (name === "pin null") expect(screen.getByLabelText(/Stay at or below/)).toHaveValue("");
      if (name === "window null") {
        expect(screen.getByLabelText(/Quiet window/)).toHaveValue("");
        expect(screen.getByRole("button", { name: "Save update settings" })).toBeDisabled();
      }
      unmount();
    }
    // policy null: draftOf throws inside the load try, so the card hides itself
    get.mockResolvedValue({ ...base, policy: null });
    const { container } = renderCard();
    await waitFor(() => expect(get).toHaveBeenCalled());
    expect(container).toBeEmptyDOMElement();
    expect(errors).not.toHaveBeenCalledWith(expect.stringMatching(/`value` prop on `input`/));
  });
});
