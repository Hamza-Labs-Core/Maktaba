import { describe, it, expect } from "vitest";
import { screen } from "@testing-library/react";
import { QueueCard, serviceName } from "./QueueCard";
import { renderAt } from "../../test/queueHarness";
import { queueSummary, stageInfo } from "../../test/fixtures/queues";

const plexIndex = stageInfo({ stage: "plex_index", label: "Plex index", extension: "plex" });

describe("QueueCard: plex_index and a stalling Plex", () => {
  it("says Plex is not answering when its waiting rows back off", () => {
    const summary = {
      ...queueSummary("plex_index", {
        counts: { pending: 3 },
        wave: { done: 12, total: 15 },
        waiting_reason: "service_retrying",
      }),
      retrying_service: "plex",
    };
    renderAt(<QueueCard info={plexIndex} summary={summary} />, "/queue");
    expect(screen.getByRole("link", { name: "Plex index" })).toBeTruthy();
    expect(screen.getByText("Plex not answering, retrying")).toBeTruthy();
  });

  it("flags the stall while other jobs keep running", () => {
    const summary = {
      ...queueSummary("plex_index", {
        counts: { pending: 2, running: 1 },
        wave: { done: 4, total: 7 },
        rate_per_min: 1,
        waiting_reason: null,
      }),
      retrying_service: "plex",
    };
    renderAt(<QueueCard info={plexIndex} summary={summary} />, "/queue");
    expect(screen.getByText("Plex not answering, retrying")).toBeTruthy();
  });

  it("names services for display", () => {
    expect(serviceName("plex")).toBe("Plex");
    expect(serviceName("dizquetv")).toBe("dizqueTV");
    expect(serviceName("other")).toBe("other");
  });
});
