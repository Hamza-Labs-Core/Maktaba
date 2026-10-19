// Overflow "cycle" (Story 27.11): a long strip line in pages that take turns,
// as the streaming engine lays them out: word boundaries, balanced pages,
// brackets kept per page, the verse reference last, each page's time, and
// the «التالي» line paging more slowly.
import { describe, it, expect, vi, afterEach } from "vitest";
import { act, render, screen } from "@testing-library/react";
import { I18nProvider } from "../../../lib/i18n";
import {
  DEFAULT_STRIP,
  NEXT_PAGE_FACTOR,
  PAGE_MARGIN,
  pageIndicator,
  pageLine,
  pageSeconds,
  estimateWidth,
  runeEms,
  stripLayout,
} from "../../../lib/strip";
import { StripPreview } from "./StripPreview";

// 10 output pixels a character: deterministic widths
const m10 = (t: string) => [...t].length * 10;
const unbracket = (p: string) => p.replace(/[﴿﴾{}()«»]/g, "");
const words = (t: string) => t.split(/\s+/).filter(Boolean);

function balanced(p: string): boolean {
  const pairs: Record<string, string> = { "﴿": "﴾", "{": "}", "(": ")", "«": "»" };
  const st: string[] = [];
  for (const ch of p) {
    if (pairs[ch]) st.push(ch);
    else if (Object.values(pairs).includes(ch)) {
      if (pairs[st.pop() ?? ""] !== ch) return false;
    }
  }
  return st.length === 0;
}

describe("pageLine", () => {
  it("breaks a Latin line at words into balanced pages that fit", () => {
    const text = "The Story of Yusuf and his brothers in the land of Egypt";
    const pages = pageLine(text, 200, m10);
    expect(pages.length).toBeGreaterThan(1);
    expect(pages.join(" ")).toBe(text);
    for (const p of pages) expect(m10(p)).toBeLessThanOrEqual(200 * PAGE_MARGIN);
    // balanced: no page is much wider than the others
    const ws = pages.map(m10);
    expect(Math.max(...ws) - Math.min(...ws)).toBeLessThanOrEqual(80);
    // and no narrower width keeps the same count
    expect(pageLine(text, 200, m10).length).toBe(pages.length);
  });

  it("breaks an Arabic line at words and keeps a short one whole", () => {
    const text = "من زاوية أخرى · الحلقة 45 — في الخنادق… لا يوجد ملحدون!";
    const pages = pageLine(text, 300, m10);
    expect(pages.length).toBeGreaterThan(1);
    expect(words(pages.join(" "))).toEqual(words(text));
    expect(pageLine("الحلقة 45", 300, m10)).toEqual(["الحلقة 45"]);
  });

  it("keeps a quoted verse in order with brackets closed per page and its reference last", () => {
    const text =
      "تفسير قوله تعالى ﴿يا أيها الذين آمنوا كتب عليكم الصيام كما كتب على الذين من قبلكم﴾";
    const pages = pageLine(text, 260, m10, "البقرة ١٨٣");
    expect(pages.length).toBeGreaterThan(2);
    for (const p of pages) expect(balanced(p)).toBe(true);
    const last = pages[pages.length - 1];
    expect(last.endsWith("البقرة ١٨٣")).toBe(true);
    // the words, without the brackets added at page edges, are the line's in order
    const body = pages
      .map(unbracket)
      .join(" ")
      .replace(" · البقرة ١٨٣", "")
      .replace(/ البقرة ١٨٣$/, "");
    expect(words(body)).toEqual(words(unbracket(text)));
    // a page that starts inside the verse reopens it
    expect(pages.slice(1).some((p) => p.startsWith("﴿"))).toBe(true);
  });

  it("puts the reference on its own page when it does not fit the last one", () => {
    const pages = pageLine(
      "بداية تفسير سورة البقرة كاملة",
      200,
      m10,
      "البقرة ١٨٣ – ١٨٥ والآيات بعدها"
    );
    expect(pages[pages.length - 1]).toBe("البقرة ١٨٣ – ١٨٥ والآيات بعدها");
  });

  it("shows a fuller page longer, between 1 and 2 times page_seconds", () => {
    expect(pageSeconds(0, 1000, 4)).toBe(4);
    expect(pageSeconds(900, 1000, 4)).toBeCloseTo(4 * (0.75 + 0.75 * 0.9));
    expect(pageSeconds(5000, 1000, 4)).toBe(8);
    expect(pageIndicator(0, 2, true)).toBe("١/٢");
    expect(pageIndicator(1, 3, false)).toBe("2/3");
  });
});

describe("the engine's layout", () => {
  it("lays the strip out on 1920×1080 as strip.go does", () => {
    const g = stripLayout(DEFAULT_STRIP, { clock: true, hasNext: true });
    expect(g.h).toBe(96); // round(1080 × 9%) = 97 → even
    expect(g.pad).toBe(24);
    expect(g.clockW).toBe(182);
    expect(g.titlePx).toBe(40); // floor(96 × 42%)
    expect(g.nextPx).toBe(26);
    expect(g.indicatorPx).toBe(26);
    expect(g.clockLabelPx).toBe(24);
    expect(g.clockTimePx).toBe(38);
    const ind = estimateWidth("٠٠/٠٠", 26) + 24;
    expect(g.indicatorW).toBeCloseTo(ind);
    expect(g.areaWidth).toBeCloseTo(1920 - 182 - 48 - ind);
    const named = stripLayout(
      { ...DEFAULT_STRIP, show_channel_name: true },
      { clock: true, hasNext: true, channelName: "قناة" }
    );
    expect(named.nameW).toBeCloseTo(4 * 40 * 0.55 + 48);
    expect(named.areaWidth).toBeCloseTo(g.areaWidth - named.nameW - 24);
    expect(
      stripLayout({ ...DEFAULT_STRIP, height_pct: 1 }, { clock: false, hasNext: false }).h
    ).toBe(16);
  });

  it("estimates widths per character in ems", () => {
    expect(runeEms(" ")).toBe(0.28);
    expect(runeEms("َ")).toBe(0); // a haraka
    expect(runeEms("‏")).toBe(0);
    expect(runeEms("ب")).toBe(0.5);
    expect(runeEms("7")).toBe(0.56);
    expect(runeEms("٣")).toBe(0.56);
    expect(runeEms("Q")).toBe(0.68);
    expect(runeEms("q")).toBe(0.52);
    expect(runeEms("!")).toBe(0.36);
    expect(runeEms("﴿")).toBe(0.36);
    expect(estimateWidth("ab", 10)).toBeCloseTo(10.4);
  });
});

describe("StripPreview paging", () => {
  afterEach(() => vi.useRealTimers());
  const s = { ...DEFAULT_STRIP, show_clock: false, overflow: "cycle" as const, page_seconds: 4 };
  const measure = (t: string) => [...t].length * 100; // ~15 characters a page
  const NOW = "في الخنادق لا يوجد ملحدون والحديث عن الإيمان عند الشدائد";

  it("cycles from page 1, the indicator per the digits rule, the next line more slowly", () => {
    vi.useFakeTimers();
    const geo = stripLayout(s, { clock: false, hasNext: true, measure });
    const m = (t: string) => measure(t);
    const pages = pageLine(NOW, geo.areaWidth, m);
    const nextPages = pageLine(`التالي: ${NOW}`, geo.areaWidth, m);
    expect(pages.length).toBeGreaterThan(2);
    render(
      <I18nProvider>
        <StripPreview settings={s} now={NOW} next={NOW} measure={measure} />
      </I18nProvider>
    );
    expect(screen.getByTestId("strip-now")).toHaveTextContent(pages[0]);
    expect(screen.getByTestId("strip-now-page")).toHaveTextContent(
      `١/${"٠١٢٣٤٥٦٧٨٩"[pages.length]}`
    );
    expect(screen.getByTestId("strip-next")).toHaveTextContent(nextPages[0]);
    const first = pageSeconds(m(pages[0]), geo.areaWidth, 4);
    const nextFirst = NEXT_PAGE_FACTOR * pageSeconds(m(nextPages[0]), geo.areaWidth, 4);
    expect(nextFirst).toBeGreaterThan(first);
    act(() => {
      vi.advanceTimersByTime(first * 1000 + 10);
    });
    expect(screen.getByTestId("strip-now")).toHaveTextContent(pages[1]);
    expect(screen.getByTestId("strip-now-page")).toHaveTextContent("٢/");
    // the «التالي» line is still on its first page
    expect(screen.getByTestId("strip-next")).toHaveTextContent(nextPages[0]);
    act(() => {
      vi.advanceTimersByTime((nextFirst - first) * 1000 + 10);
    });
    expect(screen.getByTestId("strip-next")).toHaveTextContent(nextPages[1]);
    // round the whole cycle back to page 1 (one page at a time: each page's
    // timer starts when it shows)
    const secs = pages.map((p) => pageSeconds(m(p), geo.areaWidth, 4));
    act(() => {
      vi.advanceTimersByTime((secs[1] - (nextFirst - first)) * 1000);
    });
    for (let i = 2; i < pages.length; i++) {
      expect(screen.getByTestId("strip-now")).toHaveTextContent(pages[i]);
      act(() => {
        vi.advanceTimersByTime(secs[i] * 1000 + 10);
      });
    }
    expect(screen.getByTestId("strip-now")).toHaveTextContent(pages[0]);
  });

  it("shows no indicator when it is off, or with one page", () => {
    render(
      <I18nProvider>
        <StripPreview settings={{ ...s, page_indicator: false }} now={NOW} measure={measure} />
      </I18nProvider>
    );
    expect(screen.getByTestId("strip-now")).toHaveAttribute("data-pages");
    expect(screen.queryByTestId("strip-now-page")).toBeNull();
  });

  it("truncate keeps one line with the reference after it", () => {
    render(
      <I18nProvider>
        <StripPreview
          settings={{ ...s, overflow: "truncate" }}
          now={NOW}
          nowRef="البقرة ١٨٣"
          measure={measure}
        />
      </I18nProvider>
    );
    expect(screen.getByTestId("strip-now")).toHaveAttribute("data-pages", "1");
    expect(screen.getByTestId("strip-now")).toHaveTextContent(`${NOW} · البقرة ١٨٣`);
  });
});
