// Epic 51 story 51.14 §3: after a component is turned back on, the component_catchup
// stage's tile says what it catches up for while it has work: "Catching up after
// Transcription was off". The components come from its jobs; a job that does not name
// them reads "after a component was off". No other stage has the line.
import { afterEach, describe, expect, it } from "vitest";
import { screen } from "@testing-library/react";
import { QueueCard } from "./QueueCard";
import { renderAt } from "../../test/queueHarness";
import { jobRef, queueSummary, stageInfo } from "../../test/fixtures/queues";
import { axeViolations } from "../../test/axe";
import { lookupMessage } from "../../lib/i18n";
import type { QueueSummary } from "../../lib/queueApi";

const info = stageInfo({ stage: "component_catchup", label: "Catch up" });

function running(components?: string[]): QueueSummary {
  return {
    ...queueSummary("component_catchup", { counts: { running: 1, pending: 0 } }),
    running_jobs: [
      jobRef({
        stage: "component_catchup",
        state: "running",
        progress: { step: "catch_up", steps: ["catch_up"], done_units: 40, total_units: 100 },
        ...(components ? { components } : {}),
      }),
    ],
  };
}

afterEach(() => localStorage.removeItem("mkt:locale"));

describe("the catch-up tile", () => {
  it("names the components it catches up for (English)", async () => {
    const { container } = renderAt(
      <QueueCard info={info} summary={running(["transcription"])} />,
      "/queue"
    );
    expect(screen.getByTestId("queue-card-catchup").textContent).toContain(
      "Catching up after Transcription was off"
    );
    expect(await axeViolations(container)).toEqual([]);
  });

  it("says a component when its job does not name one", () => {
    renderAt(<QueueCard info={info} summary={running()} />, "/queue");
    expect(screen.getByTestId("queue-card-catchup").textContent).toContain(
      "Catching up after a component was off"
    );
  });

  it("has no line once it has no work, and no other stage has it", () => {
    const idle = queueSummary("component_catchup", { counts: { running: 0, pending: 0 } });
    const { unmount } = renderAt(<QueueCard info={info} summary={idle} />, "/queue");
    expect(screen.queryByTestId("queue-card-catchup")).toBeNull();
    unmount();
    const other = stageInfo({ stage: "transcribe", label: "Transcribe" });
    renderAt(
      <QueueCard info={other} summary={{ ...running(["transcription"]), stage: "transcribe" }} />,
      "/queue"
    );
    expect(screen.queryByTestId("queue-card-catchup")).toBeNull();
  });

  it("reads in Arabic", async () => {
    localStorage.setItem("mkt:locale", "ar");
    const { container } = renderAt(
      <QueueCard info={info} summary={running(["transcription"])} />,
      "/queue"
    );
    const title = lookupMessage("ar", "components.transcription.title") ?? "";
    expect(title).not.toBe("");
    expect(screen.getByTestId("queue-card-catchup").textContent).toContain(
      `استكمال ما فات بعد إيقاف ${title}`
    );
    expect(await axeViolations(container)).toEqual([]);
  });
});
