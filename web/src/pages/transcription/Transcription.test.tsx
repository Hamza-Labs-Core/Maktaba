import { describe, it, expect, vi, beforeEach } from "vitest";
import { act, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { get, hub } = vi.hoisted(() => {
  const channels = new Map<string, Set<(ev: unknown) => void>>();
  return {
    get: vi.fn(),
    hub: {
      subscribe: (ch: string, fn: (ev: unknown) => void) => {
        if (!channels.has(ch)) channels.set(ch, new Set());
        channels.get(ch)!.add(fn);
        return () => channels.get(ch)?.delete(fn);
      },
      emit: (ch: string, ev: unknown) => channels.get(ch)?.forEach((f) => f(ev)),
      reset: () => channels.clear(),
    },
  };
});
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get } };
});
vi.mock("../../lib/ws", () => ({ subscribe: hub.subscribe }));

import { Transcription } from "./Transcription";
import { stagesKey } from "../../lib/useTranscriptionSummary";
import type { QueuesSnapshot } from "../../lib/queueApi";
import type { TranscriptionSummary } from "../../lib/transcriptionApi";
import { renderAt, routeGet } from "../../test/queueHarness";
import { axeViolations } from "../../test/axe";

const NOW = new Date().toISOString();

const summaryFixture: TranscriptionSummary = {
  generated_at: NOW,
  totals: {
    videos: 150,
    videos_transcribed: 40,
    audio_hours: 150,
    hours_transcribed: 40,
    hours_remaining: 110,
    segments: 12345,
  },
  stages: [
    { stage: "transcribe", pending: 75, running: 3, done: 40, failed: 2 },
    { stage: "subtitle_gen", pending: 0, running: 1, done: 38, failed: 0 },
    { stage: "index", pending: 5, running: 0, done: 30, failed: 1 },
  ],
  rate: {
    last_10m: {
      window_sec: 600,
      jobs: 6,
      audio_hours: 5,
      audio_hours_per_hour: 30,
      realtime_factor: 30,
      avg_job_rtf: 31.5,
    },
    last_1h: {
      window_sec: 3600,
      jobs: 30,
      audio_hours: 20,
      audio_hours_per_hour: 20,
      realtime_factor: 20,
      avg_job_rtf: null,
    },
  },
  eta: { seconds: 13200, at: NOW, basis: "10m" },
  libraries: [
    {
      library_id: "lib-a",
      name: "Lectures",
      videos: 100,
      videos_done: 40,
      hours: 100,
      hours_done: 40,
    },
    { library_id: "lib-b", name: "Series", videos: 300, videos_done: 0, hours: 50, hours_done: 0 },
  ],
  failures: [
    {
      job_id: 901,
      video_id: "vid-1",
      title: "Lesson 1",
      library_name: "Lectures",
      stage: "transcribe",
      error_cause: "gpu_oom",
      error: "CUDA out of memory",
      finished_at: NOW,
      attempts: 3,
    },
  ],
};

function snapshot(pending: number): QueuesSnapshot {
  const q = (stage: string, p: number) => ({
    stage,
    counts: {
      pending: p,
      ready: p,
      running: 1,
      paused: 0,
      failed_open: 0,
      done_24h: 10,
      skipped_24h: 0,
      awaiting_review: 0,
    },
  });
  return {
    at: NOW,
    queues: [q("transcribe", pending), q("subtitle_gen", 0), q("probe", 7)],
  } as unknown as QueuesSnapshot;
}

const snapEvent = (s: QueuesSnapshot) => ({
  type: "queues.snapshot",
  at: NOW,
  payload: s as unknown as Record<string, unknown>,
});

const summaryCalls = () =>
  get.mock.calls.filter((c) => c[0] === "/api/transcription/summary").length;

function setup(summary: TranscriptionSummary = summaryFixture) {
  routeGet(get, { "/api/transcription/summary": summary });
  return renderAt(<Transcription />, "/transcription");
}

describe("Transcription", () => {
  beforeEach(() => {
    localStorage.clear();
    get.mockReset();
    hub.reset();
  });

  it("shows progress, speed, time left and the stage counters", async () => {
    setup();
    await screen.findByRole("progressbar", { name: "Audio hours transcribed" });
    expect(screen.getByRole("heading", { level: 1, name: "Transcription" })).toBeInTheDocument();
    const hours = screen.getByRole("progressbar", { name: "Audio hours transcribed" });
    expect(hours).toHaveAttribute("aria-valuenow", "27");
    expect(screen.getByText("40 h of 150 h")).toBeInTheDocument();
    expect(screen.getByText("110 h left")).toBeInTheDocument();
    expect(screen.getByText("40 of 150")).toBeInTheDocument();
    expect(screen.getByText("12,345 subtitle segments")).toBeInTheDocument();

    expect(screen.getByText("30× realtime")).toBeInTheDocument();
    expect(screen.getByText("20× realtime")).toBeInTheDocument();
    expect(screen.getByText(/5 h of audio from 6 videos/)).toHaveTextContent("31.5× per video");
    expect(screen.getByText("3 h 40 min")).toBeInTheDocument();
    expect(screen.getByText("At the speed of the last 10 minutes")).toBeInTheDocument();

    const transcribe = screen.getByRole("link", { name: "Transcribe" });
    expect(transcribe).toHaveAttribute("href", "/queue/transcribe");
    const card = transcribe.closest("li") as HTMLElement;
    expect(within(card).getByText("Waiting").nextSibling).toHaveTextContent("75");
    expect(within(card).getByText("Failed").nextSibling).toHaveTextContent("2");
    expect(screen.getByRole("link", { name: "Search index" })).toBeInTheDocument();
  });

  it("shows what the workers decode now and times the rest by it", async () => {
    setup({
      ...summaryFixture,
      rate: {
        ...summaryFixture.rate,
        live: { window_sec: 600, workers: 2, audio_hours: 25, realtime_factor: 150 },
      },
      eta: { seconds: 2640, at: NOW, basis: "live" },
    });
    expect(await screen.findByText("Decoding now")).toBeInTheDocument();
    expect(screen.getByText("150× realtime")).toBeInTheDocument();
    expect(screen.getByText("25 h of audio in 10 minutes, 2 workers")).toBeInTheDocument();
    expect(screen.getByText("At the speed the workers decode now")).toBeInTheDocument();
  });

  it("links failures to the item history page", async () => {
    setup();
    const link = await screen.findByRole("link", { name: /Lesson 1/ });
    expect(link).toHaveAttribute("href", "/queue/items/video/vid-1");
    const item = link.closest("li") as HTMLElement;
    expect(within(item).getByText("gpu_oom")).toBeInTheDocument();
    expect(within(item).getByText(/CUDA out of memory/)).toBeInTheDocument();
    expect(within(item).getByText(/attempt 3/)).toBeInTheDocument();
  });

  it("sorts the library table by column", async () => {
    setup();
    const table = await screen.findByRole("table");
    const names = () =>
      within(table)
        .getAllByRole("rowheader")
        .map((h) => h.textContent?.replace(/[⁨⁩]/g, ""));
    // default: hours, largest first
    expect(names()).toEqual(["Lectures", "Series"]);
    const videos = within(table).getByRole("columnheader", { name: /Videos/ });
    expect(videos).toHaveAttribute("aria-sort", "none");
    await userEvent.click(within(videos).getByRole("button"));
    expect(videos).toHaveAttribute("aria-sort", "descending");
    expect(names()).toEqual(["Series", "Lectures"]);
    await userEvent.click(within(videos).getByRole("button"));
    expect(videos).toHaveAttribute("aria-sort", "ascending");
    expect(names()).toEqual(["Lectures", "Series"]);
    // the phone sort select drives the same state
    await userEvent.selectOptions(screen.getByRole("combobox", { name: "Sort by" }), "name:1");
    expect(names()).toEqual(["Lectures", "Series"]);
    expect(within(table).getByRole("columnheader", { name: /Library/ })).toHaveAttribute(
      "aria-sort",
      "ascending"
    );
  });

  it("explains an unknown ETA and empty sections", async () => {
    setup({
      ...summaryFixture,
      eta: { seconds: null, at: null, basis: null },
      libraries: [],
      failures: [],
    });
    expect(
      await screen.findByText("Not known yet: nothing was transcribed in the last hour.")
    ).toBeInTheDocument();
    expect(screen.getByText("No libraries yet.")).toBeInTheDocument();
    expect(screen.getByText("No open failures.")).toBeInTheDocument();
  });

  it("refreshes when the transcription queues move on the snapshot stream", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      setup();
      await screen.findByText("110 h left");
      expect(summaryCalls()).toBe(1);
      // the first snapshot only sets the baseline
      act(() => hub.emit("queues", snapEvent(snapshot(75))));
      // other stages moving does not refetch
      act(() =>
        hub.emit("queues", {
          ...snapEvent({ ...snapshot(75), queues: snapshot(75).queues.slice(0, 2) }),
        })
      );
      await act(async () => {
        await vi.advanceTimersByTimeAsync(6000);
      });
      expect(summaryCalls()).toBe(1);
      // transcribe pending changes -> one reload
      act(() => hub.emit("queues", snapEvent(snapshot(74))));
      await act(async () => {
        await vi.advanceTimersByTimeAsync(10);
      });
      expect(summaryCalls()).toBe(2);
      // a burst within 5 s collapses into one trailing reload
      act(() => hub.emit("queues", snapEvent(snapshot(73))));
      act(() => hub.emit("queues", snapEvent(snapshot(72))));
      await act(async () => {
        await vi.advanceTimersByTimeAsync(1000);
      });
      expect(summaryCalls()).toBe(2);
      await act(async () => {
        await vi.advanceTimersByTimeAsync(5000);
      });
      expect(summaryCalls()).toBe(3);
    } finally {
      vi.useRealTimers();
    }
  });

  it("polls every 5 s once the stream has been silent for 10 s", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      setup();
      await screen.findByText("Live");
      await act(async () => {
        await vi.advanceTimersByTimeAsync(9000);
      });
      expect(summaryCalls()).toBe(1);
      await act(async () => {
        await vi.advanceTimersByTimeAsync(7000); // 10 s silence + one 5 s poll
      });
      expect(summaryCalls()).toBe(2);
      expect(screen.getByText("Live updates paused, refreshing every 5 s")).toBeInTheDocument();
      // a snapshot stops the polling
      act(() => hub.emit("queues", snapEvent(snapshot(75))));
      expect(screen.getByText("Live")).toBeInTheDocument();
      const before = summaryCalls();
      await act(async () => {
        await vi.advanceTimersByTimeAsync(6000);
      });
      expect(summaryCalls()).toBe(before);
    } finally {
      vi.useRealTimers();
    }
  });

  it("offers a retry when the first load fails", async () => {
    get.mockRejectedValueOnce(new Error("down"));
    renderAt(<Transcription />, "/transcription");
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Could not load the transcription report."
    );
    routeGet(get, { "/api/transcription/summary": summaryFixture });
    await userEvent.click(screen.getByRole("button", { name: "Try again" }));
    expect(await screen.findByText("110 h left")).toBeInTheDocument();
  });

  it("renders in Arabic, right to left", async () => {
    localStorage.setItem("mkt:locale", "ar");
    setup();
    expect(await screen.findByRole("link", { name: "التفريغ" })).toHaveAttribute(
      "href",
      "/queue/transcribe"
    );
    expect(screen.getByRole("heading", { level: 1, name: "التفريغ النصي" })).toBeInTheDocument();
    expect(screen.getByText("آخر الإخفاقات")).toBeInTheDocument();
    expect(document.documentElement.dir).toBe("rtl");
    document.documentElement.dir = "ltr";
  });

  it("has no axe violations", async () => {
    const { container } = setup();
    await screen.findByRole("table");
    expect(await axeViolations(container)).toEqual([]);
  });
});

describe("stagesKey", () => {
  it("fingerprints only the transcription stages, order-independent", () => {
    const a = snapshot(5);
    const b = { ...a, queues: [...a.queues].reverse() } as QueuesSnapshot;
    expect(stagesKey(a)).toBe(stagesKey(b));
    expect(stagesKey(a)).not.toContain("probe");
    expect(stagesKey(snapshot(6))).not.toBe(stagesKey(a));
    expect(stagesKey(null)).toBe("");
  });
});
