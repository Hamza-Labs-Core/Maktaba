// The channel strip preview and its settings (Story 27.11): direction and
// clock placement, now / next lines, clock rotation, digits and DST; the
// strip form's save (only what differs from the global settings) and reset.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "../../../lib/i18n";
import { StripPreview } from "./StripPreview";
import {
  DEFAULT_STRIP,
  clockIndex,
  effectiveStrip,
  formatClock,
  stripDefaultsFromRules,
  stripDirection,
} from "../../../lib/strip";
import type { RuleSection } from "../../../lib/rulesApi";

const { get, patch, del } = vi.hoisted(() => ({ get: vi.fn(), patch: vi.fn(), del: vi.fn() }));
vi.mock("../../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../../lib/api")>("../../../lib/api");
  return { ...actual, api: { ...actual.api, get, patch, delete: del } };
});

import { StripSection, stripBody } from "./StripSection";

const NOW = "من زاوية أخرى · الحلقة 45 — في الخنادق… لا يوجد ملحدون!";
const NEXT = "الإيمان سؤال وجواب · الحلقة 14 — ثمرات عبادة الله";

function wrap(ui: React.ReactElement) {
  return render(<I18nProvider>{ui}</I18nProvider>);
}

describe("strip helpers", () => {
  it("rotates the clocks every clock_seconds", () => {
    expect(clockIndex(4, 8, 0)).toBe(0);
    expect(clockIndex(4, 8, 7_999)).toBe(0);
    expect(clockIndex(4, 8, 8_000)).toBe(1);
    expect(clockIndex(4, 8, 31_999)).toBe(3);
    expect(clockIndex(4, 8, 32_000)).toBe(0);
    expect(clockIndex(0, 8, 1000)).toBe(-1);
  });

  it("prints each city's wall time with daylight saving (2026)", () => {
    // 2026-07-01 12:00 UTC: Cairo EEST +3, New York EDT -4, Makkah +3
    const summer = new Date("2026-07-01T12:00:00Z");
    expect(formatClock(summer, "Africa/Cairo", "24h", false)).toBe("15:00");
    expect(formatClock(summer, "America/New_York", "24h", false)).toBe("08:00");
    expect(formatClock(summer, "Asia/Riyadh", "24h", false)).toBe("15:00");
    expect(formatClock(summer, "UTC", "24h", false)).toBe("12:00");
    // 2026-01-15: Cairo EET +2, New York EST -5
    const winter = new Date("2026-01-15T12:00:00Z");
    expect(formatClock(winter, "Africa/Cairo", "24h", false)).toBe("14:00");
    expect(formatClock(winter, "America/New_York", "24h", false)).toBe("07:00");
    // New York springs forward on 2026-03-08 at 02:00 local (07:00 UTC)
    expect(formatClock(new Date("2026-03-08T06:59:00Z"), "America/New_York", "24h", false)).toBe(
      "01:59"
    );
    expect(formatClock(new Date("2026-03-08T07:00:00Z"), "America/New_York", "24h", false)).toBe(
      "03:00"
    );
  });

  it("prints 12 h and Arabic-Indic digits", () => {
    const at = new Date("2026-07-01T12:05:00Z");
    expect(formatClock(at, "America/New_York", "12h", false)).toBe("08:05 AM");
    expect(formatClock(at, "Asia/Riyadh", "12h", true)).toBe("٠٣:٠٥ م");
    expect(formatClock(at, "Asia/Riyadh", "24h", true)).toBe("١٥:٠٥");
  });

  it("is right to left for Arabic titles unless set", () => {
    expect(stripDirection(DEFAULT_STRIP, [NOW])).toBe("rtl");
    expect(stripDirection(DEFAULT_STRIP, ["The Story of Yusuf"])).toBe("ltr");
    expect(stripDirection({ ...DEFAULT_STRIP, direction: "ltr" }, [NOW])).toBe("ltr");
  });

  it("lays a channel's own settings over the rules' values", () => {
    const sections = [
      {
        key: "tv_strip",
        title: "",
        what: "",
        fields: [
          { name: "enabled", value: true },
          { name: "clock_seconds", value: 5 },
          { name: "font_file", value: "/x.ttf" },
        ],
      },
    ] as unknown as RuleSection[];
    const g = stripDefaultsFromRules(sections);
    expect(g.enabled).toBe(true);
    expect(g.clock_seconds).toBe(5);
    expect("font_file" in g).toBe(false);
    const e = effectiveStrip(g, { position: "top", enabled: false });
    expect(e.position).toBe("top");
    expect(e.enabled).toBe(false);
    expect(e.clock_seconds).toBe(5);
  });

  it("saves only what differs from the global settings", () => {
    const draft = { ...DEFAULT_STRIP, position: "top" as const };
    const body = stripBody(DEFAULT_STRIP, draft, "on") as Record<string, unknown>;
    expect(body.position).toBe("top");
    expect(body.enabled).toBe(true);
    expect(body.height_pct).toBeNull();
    expect(
      (stripBody(DEFAULT_STRIP, draft, "follow") as Record<string, unknown>).enabled
    ).toBeNull();
  });
});

describe("StripPreview", () => {
  afterEach(() => vi.useRealTimers());

  it("shows now and next right to left with the clock on the left", () => {
    wrap(
      <StripPreview
        settings={DEFAULT_STRIP}
        now={NOW}
        next={NEXT}
        clock={() => new Date("2026-07-01T12:00:00Z")}
      />
    );
    const bar = screen.getByTestId("strip-bar");
    expect(bar).toHaveAttribute("dir", "rtl");
    expect(bar).toHaveAttribute("data-clock-side", "left");
    expect(bar).toHaveAttribute("data-position", "bottom");
    expect(screen.getByTestId("strip-now")).toHaveTextContent(NOW);
    expect(screen.getByTestId("strip-next")).toHaveTextContent(`التالي: ${NEXT}`);
    // epoch 2026-07-01T12:00Z / 8 s → clock 0 (Makkah), Arabic-Indic digits
    const clock = screen.getByTestId("strip-clock");
    expect(clock).toHaveTextContent("مكة المكرمة");
    expect(clock).toHaveTextContent("١٥:٠٠");
  });

  it("is left to right for Latin titles, at the top, with Western digits", () => {
    wrap(
      <StripPreview
        settings={{ ...DEFAULT_STRIP, position: "top", show_next: false }}
        now="The Story of Yusuf"
        next="Surah Al-Kahf"
        clock={() => new Date("2026-07-01T12:00:00Z")}
      />
    );
    const bar = screen.getByTestId("strip-bar");
    expect(bar).toHaveAttribute("dir", "ltr");
    expect(bar).toHaveAttribute("data-clock-side", "right");
    expect(bar).toHaveAttribute("data-position", "top");
    expect(screen.queryByTestId("strip-next")).toBeNull();
    expect(screen.getByTestId("strip-clock")).toHaveTextContent("Makkah");
    expect(screen.getByTestId("strip-clock")).toHaveTextContent("15:00");
  });

  it("moves to the next city every clock_seconds", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-07-01T12:00:00Z"));
    wrap(<StripPreview settings={DEFAULT_STRIP} now={NOW} next={NEXT} />);
    expect(screen.getByTestId("strip-clock")).toHaveTextContent("مكة المكرمة");
    act(() => {
      vi.advanceTimersByTime(8_000);
    });
    expect(screen.getByTestId("strip-clock")).toHaveTextContent("القاهرة");
    act(() => {
      vi.advanceTimersByTime(8_000);
    });
    expect(screen.getByTestId("strip-clock")).toHaveTextContent("غرينتش");
    expect(screen.getByTestId("strip-clock")).toHaveTextContent("١٢:٠٠");
    act(() => {
      vi.advanceTimersByTime(8_000);
    });
    expect(screen.getByTestId("strip-clock")).toHaveTextContent("نيويورك");
    expect(screen.getByTestId("strip-clock")).toHaveTextContent("٠٨:٠٠");
  });

  it("hides the clock when it is off", () => {
    wrap(<StripPreview settings={{ ...DEFAULT_STRIP, show_clock: false }} now={NOW} />);
    expect(screen.queryByTestId("strip-clock")).toBeNull();
  });
});

describe("StripSection", () => {
  const channel = (strip: unknown) => ({
    id: "c1",
    number: 1,
    name: "Kids",
    mode: "shuffle",
    enabled: true,
    sort_order: 0,
    art: {
      logo: { overridden: false },
      watermark: { overridden: false, position: "top-right", width_pct: 12, margin_pct: 2 },
      loading: { overridden: false },
      strip,
    },
  });

  beforeEach(() => {
    for (const f of [get, patch, del]) f.mockReset();
    get.mockImplementation((path: string) => {
      if (path === "/api/rules") {
        return Promise.resolve({
          sections: [
            { key: "tv_strip", title: "", what: "", fields: [{ name: "enabled", value: false }] },
          ],
        });
      }
      if (path === "/api/channels/now") {
        return Promise.resolve({
          server_time: "2026-07-01T12:00:00Z",
          channels: [
            {
              channel: { id: "c1", number: 1, name: "Kids" },
              current: {
                channel_id: "c1",
                kind: "program",
                start: "",
                stop: "",
                title: "x",
                on_air: NOW,
              },
              next: {
                channel_id: "c1",
                kind: "program",
                start: "",
                stop: "",
                title: "y",
                on_air: NEXT,
              },
            },
          ],
        });
      }
      return Promise.reject(new Error(path));
    });
  });

  it("previews the channel's real now / next and saves only the changes", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    patch.mockResolvedValue(channel({ enabled: true, position: "top" }));
    wrap(
      <StripSection
        channelId="c1"
        channelName="Kids"
        art={channel(null).art as never}
        onChange={onChange}
      />
    );
    await waitFor(() => expect(screen.getByTestId("strip-now")).toHaveTextContent(NOW));
    expect(screen.getByTestId("strip-next")).toHaveTextContent(NEXT);
    await user.selectOptions(screen.getByLabelText("Show the strip"), "on");
    await user.selectOptions(screen.getByLabelText("Where"), "top");
    await user.click(screen.getByRole("button", { name: "Save strip" }));
    expect(patch).toHaveBeenCalledTimes(1);
    const [path, body] = patch.mock.calls[0] as [string, Record<string, unknown>];
    expect(path).toBe("/api/channels/c1/strip");
    expect(body.enabled).toBe(true);
    expect(body.position).toBe("top");
    expect(body.clock_seconds).toBeNull();
    expect(onChange).toHaveBeenCalled();
  });

  it("refuses an unknown time zone and goes back to the defaults", async () => {
    const user = userEvent.setup();
    del.mockResolvedValue(channel(null));
    wrap(
      <StripSection
        channelId="c1"
        channelName="Kids"
        art={channel({ position: "top" }).art as never}
        onChange={() => {}}
      />
    );
    const zone = (await screen.findAllByLabelText(/time zone/i))[0];
    await user.clear(zone);
    await user.type(zone, "Mars/Olympus");
    expect(await screen.findByText(/not a time zone/i)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Save strip" })).toBeDisabled();
    await user.click(screen.getByRole("button", { name: "Use the Settings defaults" }));
    expect(del).toHaveBeenCalledWith("/api/channels/c1/strip");
  });
});
