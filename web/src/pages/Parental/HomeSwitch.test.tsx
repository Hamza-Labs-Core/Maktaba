// Epic 57 story 57.12 on the web: the Family page of a home that has just updated. An admin
// is told once that a child's device signed in, sees that parental controls are off and
// that nothing is hidden, and can turn them on; off is refused by the home while an account
// is restricted. A guardian and an older home get none of it. English and Arabic.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { get, put, post } = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), post: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, put, post } };
});

import { ParentalFixture } from "../../lib/parental";
import { forgetAnswers } from "../../lib/pageCache";
import { renderPage, adminUser } from "../../test/pageHarness";
import type { User } from "../../lib/auth";
import { Family } from "./Family";
import { familyGet, problem, type FamilyHome } from "./familyHome";
import en from "../../i18n/en.json";
import ar from "../../i18n/ar.json";

const guardian: User = { id: "u-guardian", username: "mum", is_admin: false };
const off = {
  enabled: false,
  restricted_accounts: 0,
  can_disable: true,
  child_devices: 1,
  child_device_notice: true,
};
const on = { ...off, enabled: true, child_devices: 0, child_device_notice: false };

function open(h: FamilyHome, user: User = adminUser, locale: "en" | "ar" = "en") {
  localStorage.setItem("mkt:locale", locale);
  get.mockImplementation(familyGet(h));
  return renderPage(
    <ParentalFixture value={{}}>
      <Family />
    </ParentalFixture>,
    { path: "/family", user }
  );
}

beforeEach(() => {
  get.mockReset();
  put.mockReset();
  post.mockReset();
  localStorage.clear();
  forgetAnswers("parental:");
});

describe("Family, in a home that has just updated", () => {
  it("tells an admin a child's device signed in, and that nothing is hidden yet", async () => {
    open({ children: [], accounts: [], settings: off });
    const notice = await screen.findByTestId("parental-notice");
    expect(within(notice).getByRole("heading")).toHaveTextContent("A child may be using Maktaba");
    expect(notice).toHaveTextContent(
      "A device signed in to Maktaba says a child uses it. Set up parental controls."
    );
    // Set up leads to making an account restricted, on this page
    expect(within(notice).getByRole("link", { name: "Set up" })).toHaveAttribute(
      "href",
      "#family-make"
    );
    expect(await screen.findByTestId("family-make")).toHaveAttribute(
      "aria-labelledby",
      "family-make"
    );
    expect(screen.getByTestId("parental-enabled-state")).toHaveTextContent(
      "Parental controls are off. Nothing is hidden from anyone."
    );
    expect(screen.getByTestId("parental-enabled-devices")).toHaveTextContent(
      "Devices that say a child uses them: 1"
    );
  });

  it("dismisses the notice through the home, once", async () => {
    post.mockResolvedValue({ ...off, child_device_notice: false });
    open({ children: [], accounts: [], settings: off });
    const notice = await screen.findByTestId("parental-notice");
    await userEvent.click(within(notice).getByRole("button", { name: "Dismiss" }));
    await waitFor(() => expect(screen.queryByTestId("parental-notice")).toBeNull());
    expect(post).toHaveBeenCalledTimes(1);
    expect(post.mock.calls[0]![0]).toBe("/api/parental/settings/notice/dismiss");
    // the switch stays: still off, still offered
    expect(screen.getByRole("button", { name: "Turn on" })).toBeEnabled();
  });

  it("turns parental controls on", async () => {
    put.mockResolvedValue(on);
    open({ children: [], accounts: [], settings: off });
    await userEvent.click(await screen.findByRole("button", { name: "Turn on" }));
    await waitFor(() =>
      expect(screen.getByTestId("parental-enabled-state")).toHaveTextContent(
        "Parental controls are on."
      )
    );
    expect(put).toHaveBeenCalledWith("/api/parental/settings", { enabled: true });
    expect(screen.queryByTestId("parental-notice")).toBeNull();
    expect(screen.getByRole("button", { name: "Turn off" })).toBeEnabled();
  });

  it("offers no way off while an account is restricted", async () => {
    open({
      children: [{ id: "u2", username: "omar", max_tier: 9 }],
      accounts: [],
      settings: { ...on, restricted_accounts: 1, can_disable: false },
    });
    expect(await screen.findByTestId("parental-enabled-blocked")).toHaveTextContent(
      "Lift every restriction before turning this off."
    );
    expect(screen.queryByRole("button", { name: "Turn off" })).toBeNull();
  });

  it("shows the home's refusal when an account became restricted meanwhile", async () => {
    put.mockRejectedValue(problem(409, { code: "restrictions_exist" }));
    open({ children: [], accounts: [], settings: on });
    await userEvent.click(await screen.findByRole("button", { name: "Turn off" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Lift every restriction before turning this off."
    );
    expect(screen.queryByRole("button", { name: "Turn off" })).toBeNull();
    expect(screen.getByTestId("parental-enabled-state")).toHaveTextContent(
      "Parental controls are on."
    );
  });

  it("shows a failure to change it and keeps what the home last said", async () => {
    put.mockRejectedValue(problem(500));
    open({ children: [], accounts: [], settings: off });
    await userEvent.click(await screen.findByRole("button", { name: "Turn on" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Could not change this. Try again.");
    expect(screen.getByRole("button", { name: "Turn on" })).toBeEnabled();
  });

  it("a guardian is shown none of it, and the page never asks the home for it", async () => {
    open({ children: [{ id: "u2", username: "omar", max_tier: 9 }], settings: off }, guardian);
    await screen.findByTestId("parental-children");
    expect(screen.queryByTestId("parental-notice")).toBeNull();
    expect(screen.queryByTestId("parental-enabled")).toBeNull();
    expect(get.mock.calls.map((c) => c[0])).not.toContain("/api/parental/settings");
  });

  it("an older home, with no such route, leaves the section out", async () => {
    open({ children: [], accounts: [] });
    await screen.findByTestId("family-make");
    expect(screen.queryByTestId("parental-enabled")).toBeNull();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("reads in Arabic", async () => {
    open({ children: [], accounts: [], settings: off }, adminUser, "ar");
    const notice = await screen.findByTestId("parental-notice");
    expect(within(notice).getByRole("heading")).toHaveTextContent("ربما يستخدم طفلٌ Maktaba");
    expect(within(notice).getByRole("link", { name: "إعداد" })).toBeInTheDocument();
    expect(within(notice).getByRole("button", { name: "إغلاق" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "الرقابة الأبوية" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "تفعيل" })).toBeInTheDocument();
  });

  it("has the story's strings, and the notification's, in both languages", () => {
    const e = en as Record<string, string>;
    const a = ar as Record<string, string>;
    expect(e["parental.notice.title"]).toBe("A child may be using Maktaba");
    expect(e["parental.notice.action"]).toBe("Set up");
    expect(e["parental.enabled"]).toBe("Parental controls");
    expect(e["parental.enabled.offBlocked"]).toBe(
      "Lift every restriction before turning this off."
    );
    expect(a["parental.notice.title"]).toBe("ربما يستخدم طفلٌ Maktaba");
    expect(a["parental.enabled.offBlocked"]).toBe("ارفع كل التقييدات قبل إيقاف هذا.");
    // the notification centre shows the home's notice by this key
    expect(e["notify.parental.child_device"]).toBe(e["parental.notice.body"]);
    expect(a["notify.parental.child_device"]).toBe(a["parental.notice.body"]);
  });
});
