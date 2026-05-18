// Story 11.5 — queue dashboard speaks the real contracts: GET /api/jobs
// + GET /api/queue/stats, SSE /ws/jobs with the {type,at,payload}
// envelope (NOT the old dead /ws/v1/events + msg.job), and exposes
// inline pause/cancel actions.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, act } from "@testing-library/react";
import { I18nProvider } from "../lib/i18n";

const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock("../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../lib/api")>("../lib/api");
  return { ...actual, api: { ...actual.api, get, post } };
});

const { subscribe, fireEvent } = vi.hoisted(() => {
  let cb: ((ev: unknown) => void) | null = null;
  return {
    subscribe: vi.fn((_ch: string, fn: (ev: unknown) => void) => {
      cb = fn;
      return () => {
        cb = null;
      };
    }),
    fireEvent: (ev: unknown) => cb?.(ev),
  };
});
vi.mock("../lib/ws", () => ({ subscribe }));

import { ProcessingQueue } from "./ProcessingQueue";

function renderQ() {
  return render(
    <I18nProvider>
      <ProcessingQueue />
    </I18nProvider>
  );
}

describe("ProcessingQueue", () => {
  beforeEach(() => {
    get.mockReset();
    post.mockReset();
    subscribe.mockClear();
  });

  it("subscribes to the `jobs` SSE channel and merges {payload.job} envelopes", async () => {
    get.mockImplementation((url: string) =>
      url.startsWith("/api/jobs")
        ? Promise.resolve({
            items: [{ id: "j1", stage: "transcribe", state: "running", attempts: 1 }],
          })
        : Promise.resolve({
            by_stage: {},
            eta_sec: 0,
            total_in_flight: 0,
            oldest_pending_age_sec: 0,
          })
    );
    renderQ();
    expect(subscribe).toHaveBeenCalledWith("jobs", expect.any(Function));
    await waitFor(() => expect(screen.getByText("transcribe")).toBeInTheDocument());

    // AC-3 envelope: { type, at, payload:{ job } }. Coalesced flush is
    // ≤1Hz (1000ms); allow real time to elapse.
    act(() => {
      fireEvent({
        type: "job.updated",
        at: "2026-05-18T00:00:00Z",
        payload: { job: { id: "j2", stage: "embed", state: "running", attempts: 0 } },
      });
    });
    await waitFor(() => expect(screen.getByText("embed")).toBeInTheDocument(), {
      timeout: 2000,
    });
  });

  it("renders inline pause/cancel actions and posts the job action", async () => {
    get.mockImplementation((url: string) =>
      url.startsWith("/api/jobs")
        ? Promise.resolve({
            items: [{ id: "j1", stage: "transcribe", state: "running", attempts: 1 }],
          })
        : Promise.resolve({
            by_stage: {},
            eta_sec: 0,
            total_in_flight: 0,
            oldest_pending_age_sec: 0,
          })
    );
    post.mockResolvedValue(undefined);
    renderQ();
    const pause = await screen.findByRole("button", { name: /pause/i });
    act(() => pause.click());
    await waitFor(() => expect(post).toHaveBeenCalledWith("/api/jobs/j1/pause"));
  });
});
