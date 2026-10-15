import { describe, it, expect, vi } from "vitest";
import { render, screen, act } from "@testing-library/react";
import { renderHook } from "@testing-library/react";
import { I18nProvider } from "../../lib/i18n";
import en from "../../i18n/en.json";
import ar from "../../i18n/ar.json";
import { queueVisualState, ringDash, ringFraction } from "./format";
import { RingGauge } from "./RingGauge";
import { Sparkline } from "./Sparkline";
import { RateChart } from "./RateChart";
import { filterTimeline } from "./ItemParts";
import {
  queueSummary,
  stageInfo,
  libraryHistoryFixture,
  rateFixture,
} from "../../test/fixtures/queues";
import { useThrottledReload } from "./useThrottledReload";

describe("ring maths", () => {
  it("clamps and handles empty totals", () => {
    expect(ringFraction(5, 10)).toBe(0.5);
    expect(ringFraction(15, 10)).toBe(1);
    expect(ringFraction(-1, 10)).toBe(0);
    expect(ringFraction(3, 0)).toBe(0);
    const [dash, circ] = ringDash(1, 4, 10);
    expect(dash).toBeCloseTo(circ / 4);
  });

  it("RingGauge exposes its value as text", () => {
    render(<RingGauge done={1} total={4} state="running" label="Probe: 1 of 4 done" />);
    expect(screen.getByRole("img", { name: "Probe: 1 of 4 done" })).toBeInTheDocument();
  });
});

describe("queueVisualState", () => {
  const info = stageInfo({ stage: "probe" });
  it("orders running > failing > waiting > done", () => {
    expect(
      queueVisualState(info, queueSummary("probe", { counts: { running: 1, failed_open: 3 } }))
    ).toBe("running");
    expect(queueVisualState(info, queueSummary("probe", { counts: { failed_open: 3 } }))).toBe(
      "failing"
    );
    expect(queueVisualState(info, queueSummary("probe", { counts: { pending: 2 } }))).toBe(
      "waiting"
    );
    expect(queueVisualState(info, queueSummary("probe"))).toBe("done");
  });
  it("never paints disabled / not configured queues as failing", () => {
    const off = queueSummary("probe", {
      counts: { failed_open: 2 },
      waiting_reason: "not_configured",
    });
    expect(queueVisualState(info, off)).toBe("off");
    expect(
      queueVisualState(
        { ...info, enabled: false },
        queueSummary("probe", { counts: { failed_open: 2 } })
      )
    ).toBe("off");
  });
});

describe("charts", () => {
  it("Sparkline and RateChart carry text alternatives", () => {
    render(
      <I18nProvider>
        <Sparkline values={[1, 3, 2]} label="trend" />
        <RateChart points={rateFixture.points} />
      </I18nProvider>
    );
    expect(screen.getByRole("img", { name: "trend" })).toBeInTheDocument();
    expect(screen.getByRole("img", { name: /Jobs finished per 10 minutes/ })).toBeInTheDocument();
  });
});

describe("timeline filter", () => {
  it("splits changes and problems", () => {
    const t = libraryHistoryFixture.timeline;
    expect(filterTimeline(t, "all")).toHaveLength(t.length);
    expect(filterTimeline(t, "problems").map((e) => e.job.state)).toEqual(["failed"]);
    expect(filterTimeline(t, "changes").length).toBeGreaterThan(0);
  });
});

describe("useThrottledReload", () => {
  it("runs at most once per window and keeps the trailing call", () => {
    vi.useFakeTimers();
    const fn = vi.fn();
    const { rerender } = renderHook(({ tick }) => useThrottledReload(fn, tick, 1000), {
      initialProps: { tick: 0 },
    });
    rerender({ tick: 1 });
    expect(fn).not.toHaveBeenCalled(); // inside the first window
    act(() => {
      vi.advanceTimersByTime(1000);
    });
    expect(fn).toHaveBeenCalledTimes(1);
    rerender({ tick: 2 });
    rerender({ tick: 3 });
    act(() => {
      vi.advanceTimersByTime(1000);
    });
    expect(fn).toHaveBeenCalledTimes(2);
    vi.useRealTimers();
  });
});

describe("i18n parity for queue.*", () => {
  const keys = (o: Record<string, string>) => Object.keys(o).filter((k) => k.startsWith("queue."));
  it("ar has every queue.* key that en has, with the same placeholders", () => {
    const E = en as Record<string, string>;
    const A = ar as Record<string, string>;
    for (const k of keys(E)) {
      expect(A[k], k).toBeDefined();
      const ph = (s: string) => (s.match(/\{\w+\}/g) ?? []).sort().join(",");
      expect(ph(A[k]), k).toBe(ph(E[k]));
    }
  });
});
