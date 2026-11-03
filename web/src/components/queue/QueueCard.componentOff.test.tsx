// Epic 51 story 51.14 T11: a stage its component holds back is a grey off tile that
// names the component and the queued jobs, links to Components, and is never red or
// amber; a failing signature still wins.
import { afterEach, describe, expect, it } from "vitest";
import { screen } from "@testing-library/react";
import { QueueCard } from "./QueueCard";
import { queueVisualState } from "./format";
import { renderAt } from "../../test/queueHarness";
import { queueSummary, stageInfo } from "../../test/fixtures/queues";
import { axeViolations } from "../../test/axe";
import { lookupMessage } from "../../lib/i18n";
import type { QueueSummary, StageHealth } from "../../lib/queueApi";

const off: StageHealth = {
  state: "off",
  window_hours: 6,
  failed_window: 0,
  finished_window: 0,
  failure_share: 0,
  waiting: [
    {
      on: "component:transcription",
      count: 1840,
      healthy: null,
      message: null,
      since: null,
      component: "transcription",
      phase: "off",
    },
  ],
  needs_you: 0,
  ignored: 0,
  failing: null,
};

const info = stageInfo({ stage: "transcribe", label: "Transcribe", enabled: false });

function summary(health: StageHealth): QueueSummary {
  return {
    ...queueSummary("transcribe", { counts: { pending: 1840 } }),
    waiting_reason: "component_off",
    component: "transcription",
    health,
  };
}

afterEach(() => localStorage.removeItem("mkt:locale"));

describe("a stage its component holds back", () => {
  it("is a grey off tile with the component, the count and a link (English)", async () => {
    expect(queueVisualState(info, summary(off))).toBe("off");
    const { container } = renderAt(<QueueCard info={info} summary={summary(off)} />, "/queue");
    expect(container.querySelector('[data-state="off"]')).toBeTruthy();
    expect(container.querySelector('[data-state="failing"], [data-state="waiting"]')).toBeNull();
    const flag = screen.getByTestId("queue-card-component");
    expect(flag.textContent).toContain("Waiting: Transcription is off · 1,840 queued");
    expect(flag.className).toContain("mkt-q-card__flag--off");
    const link = screen.getByRole("link", { name: "Components" });
    expect(link.getAttribute("href")).toBe("/settings/components");
    expect(await axeViolations(container)).toEqual([]);
  });

  it("says stopped until you turn it on while held", () => {
    const held = { ...off, waiting: [{ ...off.waiting[0], phase: "stopped" }] };
    renderAt(<QueueCard info={info} summary={summary(held)} />, "/queue");
    expect(screen.getByTestId("queue-card-component").textContent).toContain(
      "Waiting: Transcription is stopped until you turn it on · 1,840 queued"
    );
  });

  it("reads in Arabic", async () => {
    localStorage.setItem("mkt:locale", "ar");
    const { container } = renderAt(<QueueCard info={info} summary={summary(off)} />, "/queue");
    const title = lookupMessage("ar", "components.transcription.title") ?? "";
    expect(title).not.toBe("");
    const flag = screen.getByTestId("queue-card-component");
    expect(flag.textContent).toContain(title);
    expect(flag.textContent).toContain("في الانتظار");
    expect(screen.getByRole("link", { name: "المكوّنات" })).toBeTruthy();
    expect(await axeViolations(container)).toEqual([]);
  });

  it("is red when its stage also fails", () => {
    const failing: StageHealth = {
      ...off,
      state: "failing",
      failing: { signature: "boom", class: "unknown", count: 40, example: "boom", subject: null },
    };
    expect(queueVisualState(info, summary(failing))).toBe("failing");
  });
});
