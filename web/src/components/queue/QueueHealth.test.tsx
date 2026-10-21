import { describe, it, expect } from "vitest";
import { screen } from "@testing-library/react";
import { QueueCard, failuresLink, waitingOnLabel } from "./QueueCard";
import { queueVisualState } from "./format";
import { renderAt } from "../../test/queueHarness";
import { queueSummary, stageInfo } from "../../test/fixtures/queues";
import type { StageHealth } from "../../lib/queueApi";

// Epic 40: a tile is red only when one new error dominates; waiting jobs and a few
// broken items never paint it red.

const ok: StageHealth = {
  state: "ok",
  window_hours: 6,
  failed_window: 3,
  finished_window: 500,
  failure_share: 0.006,
  waiting: [],
  needs_you: 853,
  ignored: 0,
  failing: null,
};

const info = stageInfo({ stage: "content_hash", label: "File identity" });

describe("queue tile health", () => {
  it("keeps a queue with a small share of failures neutral, with links to its items", () => {
    const summary = {
      ...queueSummary("content_hash", {
        counts: { failed_open: 853 },
        wave: { done: 9, total: 9 },
      }),
      health: ok,
    };
    expect(queueVisualState(info, summary)).toBe("done");
    renderAt(<QueueCard info={info} summary={summary} />, "/queue");
    const failed = screen.getByRole("link", { name: "853 failed" });
    expect(failed.getAttribute("href")).toBe("/queue/failures?stage=content_hash");
    const needs = screen.getByRole("link", { name: "853 need you" });
    expect(needs.getAttribute("href")).toBe("/queue/failures?stage=content_hash&status=needs_you");
    expect(document.querySelector('[data-state="failing"]')).toBeNull();
  });

  it("says what parked jobs wait for", () => {
    const summary = {
      ...queueSummary("check_tags", { counts: { pending: 6577 } }),
      health: {
        ...ok,
        state: "waiting" as const,
        needs_you: 0,
        waiting: [
          {
            on: "dependency:model:tagcheck",
            count: 6577,
            healthy: false,
            message: "http://m:11434 is not answering",
            since: null,
          },
        ],
      },
    };
    const tags = stageInfo({ stage: "check_tags", label: "Tag check" });
    expect(queueVisualState(tags, summary)).toBe("waiting");
    renderAt(<QueueCard info={tags} summary={summary} />, "/queue");
    expect(screen.getByText("6,577 waiting for the model, resume by themselves")).toBeTruthy();
  });

  it("is red for a dominant new error, even while it runs", () => {
    const summary = {
      ...queueSummary("thumbnail", { counts: { running: 1, failed_open: 40 } }),
      health: {
        ...ok,
        state: "failing" as const,
        failing: {
          signature: "boom:x",
          class: "unknown",
          count: 40,
          example: "boom x",
          subject: null,
        },
      },
    };
    const thumb = stageInfo({ stage: "thumbnail", label: "Thumbnail" });
    expect(queueVisualState(thumb, summary)).toBe("failing");
    renderAt(<QueueCard info={thumb} summary={summary} />, "/queue");
    expect(screen.getByText("40 failing: boom:x")).toBeTruthy();
  });

  it("an API without health keeps the old rule", () => {
    const summary = queueSummary("probe", { counts: { failed_open: 2 } });
    expect(queueVisualState(stageInfo({ stage: "probe" }), summary)).toBe("failing");
  });

  it("words what jobs wait on", () => {
    const t = (k: string, v?: Record<string, string | number>) =>
      `${k}${v ? JSON.stringify(v) : ""}`;
    expect(waitingOnLabel("dependency:model:tagcheck", t)).toBe("queue.health.on.model");
    expect(waitingOnLabel("dependency:share:smb://nas/O", t)).toBe(
      'queue.health.on.share{"root":"smb://nas/O"}'
    );
    expect(waitingOnLabel("resource:disk", t)).toBe("queue.health.on.disk");
    expect(waitingOnLabel("resource:shm", t)).toBe("queue.health.on.shm");
    expect(failuresLink({ stage: "a b" })).toBe("/queue/failures?stage=a+b");
  });
});
