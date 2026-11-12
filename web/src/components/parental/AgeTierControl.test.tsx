// Epic 57 story 57.01: the age rating an admin sets on a video and on what holds videos.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { put } = vi.hoisted(() => ({ put: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, put } };
});

import { ApiError } from "../../lib/api";
import { I18nProvider } from "../../lib/i18n";
import type { User } from "../../lib/auth";
import type { TierTarget } from "../../lib/parentalApi";
import { TestAuth, adminUser, memberUser } from "../../test/pageHarness";
import { AgeTierControl, choiceOf, tierOf } from "./AgeTierControl";

function show(
  target: TierTarget,
  opts: { user?: User | null; current?: number | null; locale?: "en" | "ar" } = {}
) {
  localStorage.setItem("mkt:locale", opts.locale ?? "en");
  return render(
    <I18nProvider>
      <TestAuth user={opts.user === undefined ? adminUser : opts.user}>
        <AgeTierControl target={target} {...("current" in opts ? { current: opts.current } : {})} />
      </TestAuth>
    </I18nProvider>
  );
}

beforeEach(() => {
  put.mockReset();
  localStorage.clear();
});

describe("AgeTierControl (57.01)", () => {
  it("maps choices and tiers", () => {
    expect(choiceOf(undefined)).toBe("");
    expect(choiceOf(null)).toBe("none");
    expect(choiceOf(13)).toBe("13");
    expect(choiceOf(7)).toBe("");
    expect(tierOf("0")).toBe(0);
    expect(tierOf("none")).toBeNull();
  });

  it("offers the tiers youngest first, and nothing to save until one is chosen", () => {
    show({ kind: "series", id: "s1" });
    const select = screen.getByLabelText("Age rating");
    expect([...select.querySelectorAll("option")].map((o) => o.textContent)).toEqual([
      "Choose a rating",
      "All ages",
      "9+",
      "13+",
      "16+",
      "18+",
      "No rating of my own",
    ]);
    expect(screen.getByRole("button", { name: "Set rating" })).toBeDisabled();
  });

  it("sets a series' rating and says how many videos are rated again", async () => {
    put.mockResolvedValue({ tier: 13, source: "admin_container", recompute_count: 42 });
    show({ kind: "series", id: "s1" });
    await userEvent.selectOptions(screen.getByLabelText("Age rating"), "13");
    await userEvent.click(screen.getByRole("button", { name: "Set rating" }));
    await waitFor(() => expect(put).toHaveBeenCalledWith("/api/series/s1/age-tier", { tier: 13 }));
    expect(await screen.findByRole("status")).toHaveTextContent(
      "Saved. Videos being rated again: 42."
    );
  });

  it("clears a video's own rating and shows what it falls back to", async () => {
    put.mockResolvedValue({ tier: 9, source: "library_default", recompute_count: 1 });
    show({ kind: "video", id: "v1" }, { current: 16 });
    expect(screen.getByLabelText("Age rating")).toHaveValue("16");
    await userEvent.selectOptions(screen.getByLabelText("Age rating"), "none");
    await userEvent.click(screen.getByRole("button", { name: "Set rating" }));
    await waitFor(() =>
      expect(put).toHaveBeenCalledWith("/api/videos/v1/age-tier", { tier: null })
    );
    expect(await screen.findByRole("status")).toHaveTextContent("This video is now rated: 9+.");
  });

  it("a library's control is its default rating", async () => {
    put.mockResolvedValue({ tier: 0, source: "library_default", recompute_count: 900 });
    show({ kind: "library", id: "l1" });
    await userEvent.selectOptions(
      screen.getByLabelText("Age rating for everything in this library"),
      "0"
    );
    await userEvent.click(screen.getByRole("button", { name: "Set rating" }));
    await waitFor(() =>
      expect(put).toHaveBeenCalledWith("/api/libraries/l1/age-tier", { tier: 0 })
    );
  });

  it("shows nothing to someone who is not an admin, signed in or not", () => {
    const { unmount } = show({ kind: "video", id: "v1" }, { user: memberUser });
    expect(screen.queryByTestId("age-tier")).toBeNull();
    unmount();
    show({ kind: "video", id: "v1" }, { user: null });
    expect(screen.queryByTestId("age-tier")).toBeNull();
  });

  it("shows the home's refusal", async () => {
    put.mockRejectedValue(new ApiError({ type: "about:blank", title: "forbidden", status: 403 }));
    show({ kind: "channel", id: "c1" });
    await userEvent.selectOptions(screen.getByLabelText("Age rating"), "9");
    await userEvent.click(screen.getByRole("button", { name: "Set rating" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Only an administrator can set an age rating."
    );
    expect(screen.queryByRole("status")).toBeNull();
  });

  it("is Arabic in Arabic: the same order, the plus on the correct side", () => {
    show({ kind: "collection", id: "c1" }, { locale: "ar" });
    const select = screen.getByLabelText("التصنيف العمري");
    expect([...select.querySelectorAll("option")].map((o) => o.textContent)).toEqual([
      "اختر تصنيفًا",
      "لكل الأعمار",
      "‎+9",
      "‎+13",
      "‎+16",
      "‎+18",
      "بلا تصنيف من عندي",
    ]);
  });

  // the GET of each rated thing carries what an admin set (age_tier), for an admin alone
  it('shows the rating the home says is set, with no "choose"', () => {
    show({ kind: "library", id: "l1" }, { current: 13 });
    const select = screen.getByLabelText("Age rating for everything in this library");
    expect(select).toHaveValue("13");
    expect(select.querySelector('option[value=""]')).toBeNull();
  });

  it('shows "no rating" when the home says none is set (null), not "choose"', () => {
    show({ kind: "series", id: "s1" }, { current: null });
    expect(screen.getByLabelText("Age rating")).toHaveValue("none");
  });

  it("follows the page's answer when it comes late, until the admin chooses", async () => {
    const target: TierTarget = { kind: "channel", id: "c1" };
    const ui = (current?: number | null) => (
      <I18nProvider>
        <TestAuth user={adminUser}>
          <AgeTierControl target={target} {...(current === undefined ? {} : { current })} />
        </TestAuth>
      </I18nProvider>
    );
    localStorage.setItem("mkt:locale", "en");
    const { rerender } = render(ui());
    expect(screen.getByLabelText("Age rating")).toHaveValue("");
    rerender(ui(9));
    expect(screen.getByLabelText("Age rating")).toHaveValue("9");
    // an answer without the field (an older home, a PATCH's answer) changes nothing
    rerender(ui());
    expect(screen.getByLabelText("Age rating")).toHaveValue("9");
    await userEvent.selectOptions(screen.getByLabelText("Age rating"), "16");
    rerender(ui(0));
    expect(screen.getByLabelText("Age rating")).toHaveValue("16");
  });
});
