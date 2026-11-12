// Epic 57 story 57.15 (T04): the time a restricted account has, as the web shows it:
// the time left, the warnings five minutes and one minute before the end, and the
// time's-up screen, in English and Arabic.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";

const { get } = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get } };
});

import { I18nProvider } from "../../lib/i18n";
import { ParentalFixture, TIME_POLL_MS, warningLine } from "../../lib/parental";
import type { MyTime } from "../../lib/parentalApi";
import { HomeTime, TimeLeft, TimesUp, TimeWarning, WARNING_MS, timeWords } from "./TimeState";
import en from "../../i18n/en.json";
import ar from "../../i18n/ar.json";

const time = (over: Partial<MyTime> = {}): MyTime => ({
  allowed: true,
  limited: true,
  reason: "",
  remainingSeconds: 1800,
  slotEndsAt: null,
  nextAllowedAt: null,
  ...over,
});

function page(ui: ReactNode, locale: "en" | "ar" = "en") {
  localStorage.setItem("mkt:locale", locale);
  return render(<I18nProvider>{ui}</I18nProvider>);
}

const words = (locale: "en" | "ar") => {
  const cat = (locale === "ar" ? ar : en) as Record<string, string>;
  return {
    locale,
    t: (k: string, v?: Record<string, string | number>) =>
      (cat[k] ?? k).replace(/\{(\w+)\}/g, (m, name: string) =>
        v && name in v ? String(v[name]) : m
      ),
    n: (x: number) => String(x),
  };
};

beforeEach(() => {
  get.mockReset();
  localStorage.clear();
});
afterEach(() => {
  vi.useRealTimers();
});

describe("timeWords", () => {
  it("says minutes and hours in English", () => {
    expect(timeWords(59, words("en"))).toBe("less than a minute");
    expect(timeWords(60, words("en"))).toBe("1 minute");
    expect(timeWords(12 * 60 + 30, words("en"))).toBe("12 minutes");
    expect(timeWords(3600, words("en"))).toBe("1 hour");
    expect(timeWords(3600 + 5 * 60, words("en"))).toBe("1 hour and 5 minutes");
  });

  it("uses Arabic's plural forms", () => {
    expect(timeWords(60, words("ar"))).toBe("دقيقة واحدة");
    expect(timeWords(120, words("ar"))).toBe("دقيقتان");
    expect(timeWords(5 * 60, words("ar"))).toBe("5 دقائق");
    expect(timeWords(15 * 60, words("ar"))).toBe("15 دقيقة");
    expect(timeWords(2 * 3600, words("ar"))).toBe("ساعتان");
  });
});

describe("TimeLeft", () => {
  it("shows nothing while more than 15 minutes remain, then the time left", () => {
    const { rerender } = page(<TimeLeft time={time({ remainingSeconds: 16 * 60 })} />);
    expect(screen.queryByTestId("parental-time-left")).toBeNull();
    rerender(
      <I18nProvider>
        <TimeLeft time={time({ remainingSeconds: 15 * 60 })} />
      </I18nProvider>
    );
    expect(screen.getByTestId("parental-time-left")).toHaveTextContent("15 minutes left today");
  });

  it("Home shows it at any length (always)", () => {
    page(<TimeLeft time={time({ remainingSeconds: 2 * 3600 })} always />);
    expect(screen.getByTestId("parental-time-left")).toHaveTextContent("2 hours left today");
  });

  it("shows nothing for an account no rule covers now, or with no answer", () => {
    page(
      <>
        <TimeLeft time={time({ limited: false, remainingSeconds: null })} always />
        <TimeLeft time={null} always />
      </>
    );
    expect(screen.queryByTestId("parental-time-left")).toBeNull();
  });

  it("is Arabic in Arabic, in a right-to-left page", () => {
    page(<TimeLeft time={time({ remainingSeconds: 5 * 60 })} />, "ar");
    expect(document.documentElement).toHaveAttribute("dir", "rtl");
    expect(screen.getByTestId("parental-time-left")).toHaveTextContent("بقي ٥ دقائق اليوم");
  });
});

describe("the warnings", () => {
  it("knows the line an answer is under", () => {
    expect(warningLine(time({ remainingSeconds: 301 }))).toBeNull();
    expect(warningLine(time({ remainingSeconds: 300 }))).toBe(300);
    expect(warningLine(time({ remainingSeconds: 61 }))).toBe(300);
    expect(warningLine(time({ remainingSeconds: 60 }))).toBe(60);
    expect(warningLine(time({ allowed: false, remainingSeconds: 0 }))).toBeNull();
    expect(warningLine(null)).toBeNull();
  });

  it("warns at five minutes and again at one minute, each once", () => {
    vi.useFakeTimers();
    const show = (t: MyTime) => (
      <I18nProvider>
        <TimeWarning time={t} />
      </I18nProvider>
    );
    const { rerender } = render(show(time({ remainingSeconds: 400 })));
    expect(screen.queryByRole("alert")).toBeNull();

    rerender(show(time({ remainingSeconds: 290 })));
    expect(screen.getByRole("alert")).toHaveTextContent("Watching ends in 5 minutes.");
    // it goes away by itself, and the next answer under the same line does not bring it back
    act(() => vi.advanceTimersByTime(WARNING_MS + 10));
    expect(screen.queryByRole("alert")).toBeNull();
    rerender(show(time({ remainingSeconds: 200 })));
    expect(screen.queryByRole("alert")).toBeNull();

    rerender(show(time({ remainingSeconds: 55 })));
    expect(screen.getByRole("alert")).toHaveTextContent("Watching ends in 1 minute.");
  });

  it("warns again after more time came and ran down", () => {
    const show = (t: MyTime) => (
      <I18nProvider>
        <TimeWarning time={t} />
      </I18nProvider>
    );
    const { rerender } = render(show(time({ remainingSeconds: 50 })));
    expect(screen.getByRole("alert")).toBeInTheDocument();
    // a parent gave 30 minutes
    rerender(show(time({ remainingSeconds: 1850 })));
    rerender(show(time({ remainingSeconds: 299 })));
    expect(screen.getByRole("alert")).toHaveTextContent("5 minutes");
  });

  it("can be closed", async () => {
    page(<TimeWarning time={time({ remainingSeconds: 250 })} />);
    await userEvent.click(screen.getByRole("button", { name: "Close" }));
    expect(screen.queryByRole("alert")).toBeNull();
  });
});

describe("TimesUp", () => {
  it("says the daily limit is reached, when watching opens again and where the video stopped", () => {
    const at = new Date();
    at.setHours(23, 30, 0, 0);
    page(<TimesUp reason="limit_reached" nextAllowedAt={at.toISOString()} savedAt="12:34" />);
    const screenEl = screen.getByTestId("parental-times-up");
    expect(screenEl).toHaveAttribute("data-reason", "limit_reached");
    expect(screen.getByRole("heading", { name: "Time's up for today" })).toBeInTheDocument();
    expect(screenEl).toHaveTextContent(/You can watch again at 11:30/);
    expect(screenEl).toHaveTextContent("Your place in this video is saved at 12:34.");
    expect(screenEl).toHaveTextContent("Ask a parent for more time");
    // nothing here asks the home again by itself, and there is no request button on the web
    expect(screen.queryByRole("button")).toBeNull();
  });

  it("says it is not watching time yet outside a slot, naming the day when it is not today", () => {
    const at = new Date(Date.now() + 3 * 24 * 3600_000);
    page(<TimesUp reason="outside_slot" nextAllowedAt={at.toISOString()} />);
    expect(screen.getByRole("heading", { name: "Not watching time yet" })).toBeInTheDocument();
    const weekday = new Intl.DateTimeFormat("en", { weekday: "long" }).format(at);
    expect(screen.getByTestId("parental-times-up")).toHaveTextContent(weekday);
  });

  it("with no next time says only that the time is up", () => {
    page(<TimesUp reason="limit_reached" nextAllowedAt={null} />);
    expect(screen.getByTestId("parental-times-up")).not.toHaveTextContent("You can watch again");
  });

  it("Check again is the viewer's own tap", async () => {
    const onCheck = vi.fn();
    page(<TimesUp reason="limit_reached" nextAllowedAt={null} onCheck={onCheck} />);
    expect(onCheck).not.toHaveBeenCalled();
    await userEvent.click(screen.getByRole("button", { name: "Check again" }));
    expect(onCheck).toHaveBeenCalledTimes(1);
  });

  it("is Arabic in Arabic", () => {
    page(<TimesUp reason="outside_slot" nextAllowedAt={null} onCheck={() => {}} />, "ar");
    expect(document.documentElement).toHaveAttribute("dir", "rtl");
    expect(screen.getByRole("heading", { name: "لم يحن وقت المشاهدة بعد" })).toBeInTheDocument();
    expect(screen.getByTestId("parental-times-up")).toHaveTextContent(
      "اطلب من أحد والديك وقتًا إضافيًا"
    );
    expect(screen.getByRole("button", { name: "تحقّق مجددًا" })).toBeInTheDocument();
  });
});

describe("HomeTime", () => {
  const home = (restricted: boolean) =>
    page(
      <ParentalFixture value={{ restricted }}>
        <HomeTime />
      </ParentalFixture>
    );

  it("asks nothing and shows nothing for an account that is not restricted", async () => {
    home(false);
    await act(async () => {});
    expect(get).not.toHaveBeenCalled();
    expect(screen.queryByTestId("parental-time-left")).toBeNull();
  });

  it("shows a restricted account the time the home says is left, and asks again", async () => {
    vi.useFakeTimers();
    get.mockResolvedValue({ allowed: true, limited: true, remaining_seconds: 2400 });
    home(true);
    await act(async () => {});
    expect(get).toHaveBeenCalledWith("/api/me/parental/time");
    expect(screen.getByTestId("parental-time-left")).toHaveTextContent("40 minutes left today");
    // the web counts nothing itself: the number changes only with the home's next answer
    await act(async () => vi.advanceTimersByTime(TIME_POLL_MS - 1000));
    expect(screen.getByTestId("parental-time-left")).toHaveTextContent("40 minutes left today");
    get.mockResolvedValue({ allowed: true, limited: true, remaining_seconds: 2340 });
    await act(async () => vi.advanceTimersByTime(1500));
    expect(get).toHaveBeenCalledTimes(2);
    expect(screen.getByTestId("parental-time-left")).toHaveTextContent("39 minutes left today");
  });

  it("says the time is over when the home says so", async () => {
    get.mockResolvedValue({
      allowed: false,
      limited: true,
      reason: "limit_reached",
      remaining_seconds: 0,
      next_allowed_at: null,
    });
    home(true);
    expect(await screen.findByRole("heading", { name: "Time's up for today" })).toBeInTheDocument();
  });

  it("shows nothing for a restricted account with no time rules", async () => {
    get.mockResolvedValue({ allowed: true, limited: false, remaining_seconds: null });
    home(true);
    await act(async () => {});
    expect(screen.queryByTestId("parental-time-left")).toBeNull();
    expect(screen.queryByTestId("parental-times-up")).toBeNull();
  });
});
