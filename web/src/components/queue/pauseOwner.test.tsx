// A paused stage says who paused it (2026-11-02: the home's extract stage sat paused by
// nobody for eleven days, and the Queues page said only "Paused").
import { afterEach, describe, expect, it } from "vitest";
import { screen } from "@testing-library/react";
import { QueueCard } from "./QueueCard";
import { pauseOwner } from "./format";
import { renderAt } from "../../test/queueHarness";
import { queueSummary, stageInfo } from "../../test/fixtures/queues";
import en from "../../i18n/en.json";
import ar from "../../i18n/ar.json";

const t = (key: string, vars?: Record<string, string | number>) => {
  let s = (en as Record<string, string>)[key] ?? key;
  for (const [k, v] of Object.entries(vars ?? {})) s = s.replace(`{${k}}`, String(v));
  return s;
};
const strip = (s: string) => s.replace(/[⁨⁩]/g, "");

const KEYS = [
  "queue.card.pausedBy",
  "queue.detail.pausedBanner",
  "queue.detail.pausedBannerNoTime",
  "queue.pause.byUser",
  "queue.pause.byAdmin",
  "queue.pause.byStorage",
  "queue.pause.bySystem",
  "queue.pause.byNobody",
];

describe("pauseOwner", () => {
  it("names who paused a stage", () => {
    expect(strip(pauseOwner({ paused_by: "user:abc", paused_by_name: "mahmoud" }, t))).toBe(
      "mahmoud"
    );
    expect(pauseOwner({ paused_by: "user:abc", paused_by_name: null }, t)).toBe("an admin");
    expect(pauseOwner({ paused_by: "storage_canonicalize" }, t)).toBe("the share path conversion");
    expect(pauseOwner({ paused_by: "system" }, t)).toBe("Maktaba");
    expect(pauseOwner({ paused_by: null }, t)).toBe("no one on record");
    expect(strip(pauseOwner({ paused_by: "other_tool" }, t))).toBe("other_tool");
  });

  it("ships every string in Arabic too", () => {
    for (const k of KEYS) {
      expect((en as Record<string, string>)[k], k).toBeTruthy();
      expect((ar as Record<string, string>)[k], k).toBeTruthy();
    }
  });
});

describe("QueueCard: a paused stage", () => {
  afterEach(() => {
    localStorage.removeItem("mkt:locale");
    document.documentElement.dir = "ltr";
  });

  const paused = stageInfo({
    stage: "extract",
    label: "Extract audio",
    paused: true,
    paused_at: new Date(Date.now() - 3600_000).toISOString(),
    paused_by: "user:11111111-1111-1111-1111-111111111111",
    paused_by_name: "mahmoud",
  });
  const summary = queueSummary("extract", {
    counts: { pending: 847, paused: 476 },
    waiting_reason: "paused",
  });

  it("says who paused it", () => {
    renderAt(<QueueCard info={paused} summary={summary} />, "/queue");
    const flag = screen.getByTestId("queue-card-paused");
    expect(strip(flag.textContent ?? "")).toContain("Paused by mahmoud");
  });

  it("says when no one is on record", () => {
    renderAt(
      <QueueCard
        info={{ ...paused, paused_by: null, paused_by_name: null, paused_at: null }}
        summary={summary}
      />,
      "/queue"
    );
    expect(screen.getByTestId("queue-card-paused").textContent).toContain(
      "Paused by no one on record"
    );
  });

  it("says it in Arabic", () => {
    localStorage.setItem("mkt:locale", "ar");
    renderAt(
      <QueueCard info={{ ...paused, paused_by: "storage_canonicalize" }} summary={summary} />,
      "/queue"
    );
    expect(screen.getByTestId("queue-card-paused").textContent).toContain(
      "أوقفه مؤقتًا: تحويل مسارات المشاركة"
    );
  });

  it("shows nothing for a stage that runs", () => {
    renderAt(
      <QueueCard info={{ ...paused, paused: false, paused_by: null }} summary={summary} />,
      "/queue"
    );
    expect(screen.queryByTestId("queue-card-paused")).toBeNull();
  });
});
