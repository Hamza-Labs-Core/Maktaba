// The strip's colours (Story 27.11): the picker and the hex field update the
// live preview, a bad hex is refused, a palette applies, reset goes back to
// the defaults; the accent rule faces the picture; Settings shows a colour
// control for the tv_strip colours.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { act, fireEvent, render, screen, within, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "../../../lib/i18n";
import { DEFAULT_STRIP, ruleThickness } from "../../../lib/strip";
import { controlOf, valueProblem } from "../../Settings/ruleKinds";
import type { RuleField } from "../../../lib/rulesApi";
import { StripPreview } from "./StripPreview";

const { get, patch, del } = vi.hoisted(() => ({ get: vi.fn(), patch: vi.fn(), del: vi.fn() }));
vi.mock("../../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../../lib/api")>("../../../lib/api");
  return { ...actual, api: { ...actual.api, get, patch, delete: del } };
});

import { StripSection } from "./StripSection";

const art = {
  logo: { overridden: false },
  watermark: { overridden: false, position: "top-right", width_pct: 12, margin_pct: 2 },
  loading: { overridden: false },
  strip: null,
};

function hex(k: string) {
  return within(screen.getByTestId(`color-${k}`)).getByRole("textbox");
}
const barBg = () => screen.getByTestId("strip-bar").style.background;

async function openSection() {
  render(
    <I18nProvider>
      <StripSection channelId="c1" channelName="Kids" art={art as never} onChange={() => {}} />
    </I18nProvider>
  );
  // the global settings and the now / next lines have arrived
  await waitFor(() => expect(get).toHaveBeenCalledTimes(2));
  await act(async () => {
    await Promise.resolve();
  });
  return userEvent.setup();
}

describe("strip colours", () => {
  beforeEach(() => {
    for (const f of [get, patch, del]) f.mockReset();
    get.mockImplementation((path: string) =>
      path === "/api/rules" ? Promise.resolve({ sections: [] }) : Promise.resolve({ channels: [] })
    );
  });

  it("defaults to green and gold, the rule at the top of a bottom bar", async () => {
    await openSection();
    expect(hex("bg_color")).toHaveValue("#0b5d3b");
    expect(hex("next_color")).toHaveValue("#e8d9a8");
    expect(barBg()).toContain("rgba(11, 93, 59, 0.9)");
    const rule = screen.getByTestId("strip-rule");
    expect(rule).toHaveAttribute("data-edge", "top");
    expect(rule.style.background).toContain("rgb(201, 162, 39)");
    expect(screen.getByRole("button", { name: /green & gold/i })).toHaveAttribute(
      "aria-pressed",
      "true"
    );
  });

  it("updates the preview from the picker and from the hex field", async () => {
    const user = await openSection();
    const picker = screen
      .getByTestId("color-bg_color")
      .querySelector('input[type="color"]') as HTMLInputElement;
    fireEvent.change(picker, { target: { value: "#112233" } });
    expect(hex("bg_color")).toHaveValue("#112233");
    expect(barBg()).toContain("rgba(17, 34, 51, 0.9)");
    await user.clear(hex("text_color"));
    await user.type(hex("text_color"), "#FFEE00");
    expect(screen.getByTestId("strip-bar").style.color).toBe("rgb(255, 238, 0)");
  });

  it("refuses a hex that is no colour", async () => {
    const user = await openSection();
    await user.clear(hex("accent_color"));
    await user.type(hex("accent_color"), "#c9a2");
    expect(within(screen.getByTestId("color-accent_color")).getByRole("alert")).toHaveTextContent(
      /#RRGGBB/
    );
    expect(hex("accent_color")).toHaveAttribute("aria-invalid", "true");
    expect(screen.getByRole("button", { name: "Save strip" })).toBeDisabled();
  });

  it("applies a palette and resets to the default", async () => {
    const user = await openSection();
    await user.click(screen.getByRole("button", { name: /maroon & gold/i }));
    expect(hex("bg_color")).toHaveValue("#5c0f1d");
    expect(hex("clock_text_color")).toHaveValue("#3a0a12");
    expect(hex("indicator_color")).toHaveValue("#e8c35a");
    expect(barBg()).toContain("rgba(92, 15, 29, 0.9)");
    await user.click(screen.getByRole("button", { name: /^navy$/i }));
    expect(barBg()).toContain("rgba(11, 31, 58, 0.85)");
    await user.click(screen.getByRole("button", { name: /reset colours to default/i }));
    expect(hex("bg_color")).toHaveValue("#0b5d3b");
    expect(barBg()).toContain("rgba(11, 93, 59, 0.9)");
    // the opacity slider beside the background
    fireEvent.change(screen.getByRole("slider"), { target: { value: "0.5" } });
    expect(barBg()).toContain("rgba(11, 93, 59, 0.5)");
  });
});

describe("the accent rule and the colours of each part", () => {
  it("faces the picture and uses the part colours", () => {
    render(
      <I18nProvider>
        <StripPreview
          settings={{ ...DEFAULT_STRIP, position: "top", show_channel_name: true }}
          now="The Story of Yusuf"
          next="Surah Al-Kahf"
          channelName="Kids"
          clock={() => new Date("2026-07-01T12:00:00Z")}
        />
      </I18nProvider>
    );
    expect(screen.getByTestId("strip-rule")).toHaveAttribute("data-edge", "bottom");
    expect(screen.getByTestId("strip-next").style.color).toBe("rgb(232, 217, 168)");
    expect(screen.getByTestId("strip-clock").style.color).toBe("rgb(11, 61, 39)");
    expect(screen.getByTestId("strip-channel").style.color).toBe("rgb(11, 61, 39)");
    expect(ruleThickness(96, 4)).toBe(4);
    expect(ruleThickness(96, 0.5)).toBe(1);
    expect(ruleThickness(96, 0)).toBe(0);
  });

  it("draws no rule at 0", () => {
    render(
      <I18nProvider>
        <StripPreview settings={{ ...DEFAULT_STRIP, rule_height: 0 }} now="x" />
      </I18nProvider>
    );
    expect(screen.queryByTestId("strip-rule")).toBeNull();
  });
});

describe("Settings colour control", () => {
  const field = (over: Partial<RuleField>): RuleField =>
    ({
      name: "bg_color",
      kind: "str",
      group: "Strip look",
      label: "",
      help: "",
      default: "#0b5d3b",
      value: "#0b5d3b",
      changed: false,
      ...over,
    }) as RuleField;

  it("is a colour picker for a #RRGGBB _color setting, and checks the hex", () => {
    expect(controlOf(field({}))).toBe("color");
    expect(controlOf(field({ name: "next_color", default: "#e8d9a8" }))).toBe("color");
    expect(controlOf(field({ name: "bg_name", default: "x" }))).toBe("text");
    expect(valueProblem(field({}), "#123")).toEqual({ key: "settings.rules.err.color" });
    expect(valueProblem(field({}), "#a1b2c3")).toBeNull();
  });
});
