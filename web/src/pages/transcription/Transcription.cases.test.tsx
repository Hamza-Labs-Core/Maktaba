// Cases from web/test-cases/transcription.md (TRN-*), rendered through the
// shared page harness (RequireAuth + route error boundary + signed-in user).
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Route } from "react-router-dom";

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
      count: (ch: string) => channels.get(ch)?.size ?? 0,
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
import { ApiError } from "../../lib/api";
import type { TranscriptionSummary } from "../../lib/transcriptionApi";
import { routeGet } from "../../test/queueHarness";
import { memberUser, quietCrash, renderPage } from "../../test/pageHarness";

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
    { library_id: "lib-c", name: "Fiqh", videos: 10, videos_done: 10, hours: 5, hours_done: 5 },
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

const SUMMARY = "/api/transcription/summary";

function setup(
  summary: unknown = summaryFixture,
  opts: { user?: typeof memberUser | null; extra?: boolean } = {}
) {
  routeGet(get, { [SUMMARY]: summary });
  return renderPage(<Transcription />, {
    path: "/transcription",
    user: opts.user,
    extraRoutes: (
      <>
        <Route path="/queue/:stage" element={<p>stage page</p>} />
        <Route path="/queue/items/:kind/:id" element={<p>item page</p>} />
      </>
    ),
  });
}

const summaryCalls = () => get.mock.calls.filter((c) => c[0] === SUMMARY).length;
const noCrash = () =>
  expect(screen.queryByText("This page hit an error and could not be shown.")).toBeNull();
const ready = () => screen.findByRole("heading", { name: "Stages" });
const rowNames = () =>
  within(screen.getByRole("table"))
    .getAllByRole("rowheader")
    .map((h) => h.textContent?.replace(/[⁨⁩]/g, ""));

/** the fixture with one (possibly nested) key set to null or removed */
function summaryWith(path: string, mode: "null" | "missing"): TranscriptionSummary {
  const s = structuredClone(summaryFixture) as unknown as Record<string, unknown>;
  const keys = path.split(".");
  let o: Record<string, unknown> = s;
  for (const k of keys.slice(0, -1)) o = o[k] as Record<string, unknown>;
  const last = keys[keys.length - 1];
  if (mode === "null") o[last] = null;
  else delete o[last];
  return s as unknown as TranscriptionSummary;
}

describe("Transcription (test-cases/transcription.md)", () => {
  beforeEach(() => {
    get.mockReset();
    hub.reset();
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("TRN-02: shows Loading under the title while pending", async () => {
    setup(new Promise(() => {}));
    expect(await screen.findByRole("status")).toHaveTextContent("Loading…");
    expect(screen.getByRole("heading", { level: 1, name: "Transcription" })).toBeInTheDocument();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("TRN-04: a failed refresh keeps the data and says it is stale", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    let n = 0;
    setup(() => {
      n++;
      return n === 2
        ? Promise.reject(new ApiError({ type: "about:blank", title: "Boom", status: 500 }))
        : summaryFixture;
    });
    expect(await screen.findByText("110 h left")).toBeInTheDocument();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(15_100);
    });
    expect(n).toBe(2);
    expect(screen.getByText("Could not refresh; showing the last report")).toBeInTheDocument();
    expect(screen.getByText("110 h left")).toBeInTheDocument();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5_000);
    });
    expect(screen.getByText("Live updates paused, refreshing every 5 s")).toBeInTheDocument();
  });

  it("TRN-05: 401 / 403 / truncated JSON / HTML summary show the load error", async () => {
    const variants: unknown[] = [
      () =>
        Promise.reject(new ApiError({ type: "about:blank", title: "Unauthorized", status: 401 })),
      () => Promise.reject(new ApiError({ type: "about:blank", title: "Forbidden", status: 403 })),
      () => Promise.reject(new SyntaxError("Unexpected end of JSON input")),
      "<!doctype html><html></html>",
    ];
    for (const v of variants) {
      const { unmount } = setup(v);
      const alert = await screen.findByRole("alert");
      expect(alert).toHaveTextContent("Could not load the transcription report.");
      expect(within(alert).getByRole("button", { name: "Try again" })).toBeInTheDocument();
      noCrash();
      unmount();
    }
  });

  it.each(["stages", "libraries", "failures"])(
    "TRN-06: %s null or missing shows the empty state",
    async (field) => {
      for (const mode of ["null", "missing"] as const) {
        const { unmount } = setup(summaryWith(field, mode));
        expect(await ready(), mode).toBeInTheDocument();
        noCrash();
        if (field === "stages") {
          const card = screen.getByRole("link", { name: "Transcribe" }).closest("li")!;
          expect(within(card).getByText("Waiting").nextSibling).toHaveTextContent("0");
        }
        if (field === "libraries") expect(screen.getByText("No libraries yet.")).toBeVisible();
        if (field === "failures") expect(screen.getByText("No open failures.")).toBeVisible();
        unmount();
      }
    }
  );

  it.each(["totals", "rate", "rate.last_10m", "rate.live", "eta", "totals.segments"])(
    "TRN-07: %s null or missing does not blank the page",
    async (field) => {
      for (const mode of ["null", "missing"] as const) {
        const { unmount } = setup(summaryWith(field, mode));
        expect(await ready(), mode).toBeInTheDocument();
        expect(screen.getByRole("heading", { name: "Speed" })).toBeInTheDocument();
        expect(screen.queryByText(/NaN/)).toBeNull();
        expect(screen.queryByText("Decoding now")).toBeNull();
        noCrash();
        unmount();
      }
    }
  );

  it("TRN-08: libraries with empty / null names show 'No library' and sort", async () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    setup({
      ...summaryFixture,
      libraries: [
        { library_id: "", name: "", videos: 3, videos_done: 1, hours: 2, hours_done: 1 },
        { library_id: "l2", name: null, videos: 3, videos_done: 1, hours: 2, hours_done: 1 },
        { library_id: "l3", name: "Fiqh", videos: 3, videos_done: 1, hours: 2, hours_done: 1 },
      ],
    });
    expect(await screen.findByRole("table")).toBeInTheDocument();
    // equal hours: the name tie-break puts the unnamed rows (sorted as "") first
    expect(rowNames()).toEqual(["No library", "No library", "Fiqh"]);
    await userEvent.click(screen.getByRole("button", { name: /^Library/ }));
    expect(rowNames()[2]).toBe("Fiqh");
    noCrash();
    expect(vi.mocked(console.error).mock.calls.some((c) => String(c[0]).includes("same key"))).toBe(
      false
    );
  });

  it("TRN-09: empty lists and zero totals: no NaN, every empty text", async () => {
    const zero = {
      ...summaryFixture.rate.last_10m,
      jobs: 0,
      realtime_factor: 0,
      avg_job_rtf: null,
    };
    setup({
      ...summaryFixture,
      totals: {
        videos: 0,
        videos_transcribed: 0,
        audio_hours: 0,
        hours_transcribed: 0,
        hours_remaining: 0,
        segments: 0,
      },
      stages: [],
      libraries: [],
      failures: [],
      eta: { seconds: null, at: null, basis: null },
      rate: { last_10m: zero, last_1h: { ...zero, window_sec: 3600 } },
    });
    expect(await screen.findByText("0 h of 0 h")).toBeInTheDocument();
    expect(screen.getAllByText("0%").length).toBeGreaterThan(0);
    expect(screen.queryByText(/NaN/)).toBeNull();
    expect(
      screen.getByText("Not known yet: nothing was transcribed in the last hour.")
    ).toBeInTheDocument();
    expect(screen.getByText("No libraries yet.")).toBeInTheDocument();
    expect(screen.getByText("No open failures.")).toBeInTheDocument();
    expect(screen.queryByText(/per video/)).toBeNull();
  });

  it("TRN-10: render crash shows the route error fallback; Back works", async () => {
    quietCrash();
    setup({
      ...summaryFixture,
      failures: [{ ...summaryFixture.failures[0], error: { not: "text" } }],
    });
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "This page hit an error and could not be shown."
    );
    await userEvent.click(screen.getByRole("link", { name: "go elsewhere" }));
    expect(await screen.findByText("elsewhere")).toBeInTheDocument();
    routeGet(get, { [SUMMARY]: summaryFixture });
    await userEvent.click(screen.getByRole("button", { name: "go back" }));
    expect(await screen.findByText("110 h left")).toBeInTheDocument();
  });

  it("TRN-11: signed-out user is sent to /login; no fetch, no stream", async () => {
    setup(summaryFixture, { user: null });
    expect(await screen.findByText("login page (from /transcription)")).toBeInTheDocument();
    expect(summaryCalls()).toBe(0);
    expect(hub.count("queues")).toBe(0);
  });

  it("TRN-12: a non-admin sees the whole report; a 403 shows the load error", async () => {
    const a = setup(summaryFixture, { user: memberUser });
    expect(await screen.findByText("110 h left")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /Lesson 1/ })).toHaveAttribute(
      "href",
      "/queue/items/video/vid-1"
    );
    a.unmount();
    setup(
      () => Promise.reject(new ApiError({ type: "about:blank", title: "Forbidden", status: 403 })),
      { user: memberUser }
    );
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Could not load the transcription report."
    );
  });

  it("TRN-13: time left: done, 10 min / 1 h / live basis, no date without at", async () => {
    const cases: Array<[TranscriptionSummary["eta"], string[]]> = [
      [{ seconds: 0, at: null, basis: null }, ["Everything is transcribed"]],
      [{ seconds: 7200, at: NOW, basis: "10m" }, ["At the speed of the last 10 minutes"]],
      [{ seconds: 7200, at: NOW, basis: "1h" }, ["At the speed of the last hour"]],
      [{ seconds: 7200, at: NOW, basis: "live" }, ["At the speed the workers decode now"]],
    ];
    for (const [eta, texts] of cases) {
      const { unmount } = setup({ ...summaryFixture, eta });
      await ready();
      for (const t of texts) expect(screen.getByText(t)).toBeInTheDocument();
      if (eta.seconds) expect(screen.getByText(/^Done around /)).toBeInTheDocument();
      unmount();
    }
    setup({ ...summaryFixture, eta: { seconds: 7200, at: null, basis: "1h" } });
    await ready();
    expect(screen.getByText("2 h")).toBeInTheDocument();
    expect(screen.queryByText(/^Done around /)).toBeNull();
  });

  it("TRN-14: Decoding now only with workers; lost hours only when given", async () => {
    const live = { window_sec: 600, workers: 0, audio_hours: 25, realtime_factor: 150 };
    const a = setup({ ...summaryFixture, rate: { ...summaryFixture.rate, live } });
    await ready();
    expect(screen.queryByText("Decoding now")).toBeNull();
    a.unmount();
    const b = setup({
      ...summaryFixture,
      rate: { ...summaryFixture.rate, live: { ...live, workers: 2, lost_hours: 0.5 } },
    });
    await ready();
    expect(screen.getByText("Decoding now")).toBeInTheDocument();
    expect(screen.getByText(/25 h of audio in 10 minutes, 2 workers/)).toHaveTextContent(
      "0.5 h redone after a restart or failure"
    );
    b.unmount();
    setup({ ...summaryFixture, rate: { ...summaryFixture.rate, live: { ...live, workers: 2 } } });
    await ready();
    expect(screen.getByText(/2 workers/)).not.toHaveTextContent("redone");
  });

  it("TRN-15: the sort select and the headers drive the same order", async () => {
    setup();
    await screen.findByRole("table");
    const hours = screen.getByRole("columnheader", { name: /Hours/ });
    expect(hours).toHaveAttribute("aria-sort", "descending");
    expect(rowNames()).toEqual(["Lectures", "Series", "Fiqh"]);
    const select = screen.getByRole("combobox", { name: "Sort by" });
    await userEvent.selectOptions(select, "name:1");
    expect(rowNames()).toEqual(["Fiqh", "Lectures", "Series"]);
    expect(screen.getByRole("columnheader", { name: /Library/ })).toHaveAttribute(
      "aria-sort",
      "ascending"
    );
    await userEvent.click(
      within(screen.getByRole("columnheader", { name: /Videos/ })).getByRole("button")
    );
    expect(select).toHaveValue("videos:-1");
    expect(rowNames()).toEqual(["Series", "Lectures", "Fiqh"]);
  });

  it("TRN-16: failures: cause, unknown cause, unknown stage, links", async () => {
    vi.spyOn(console, "warn").mockImplementation(() => {});
    const f = summaryFixture.failures[0];
    setup({
      ...summaryFixture,
      failures: [
        { ...f, error_cause: "decode_error", error: "ffmpeg exit 1", library_name: "Fiqh" },
        {
          ...f,
          job_id: 902,
          video_id: "vid-2",
          title: "Lesson 2",
          error_cause: null,
          error: null,
          library_name: null,
          finished_at: null,
        },
        { ...f, job_id: 903, video_id: "vid-3", title: "Lesson 3", stage: "diarize" },
      ],
    });
    const one = (await screen.findByRole("link", { name: /Lesson 1/ })).closest("li")!;
    expect(within(one).getByText("Transcribe")).toBeInTheDocument();
    expect(within(one).getByText(/ffmpeg exit 1/)).toBeInTheDocument();
    expect(within(one).getByText(/Fiqh.* · attempt 3 · /)).toBeInTheDocument();
    const two = screen.getByRole("link", { name: /Lesson 2/ }).closest("li")!;
    expect(within(two).getByText("unknown cause")).toBeInTheDocument();
    expect(within(two).getByText("attempt 3")).toBeInTheDocument();
    const three = screen.getByRole("link", { name: /Lesson 3/ }).closest("li")!;
    expect(within(three).getByText("diarize")).toBeInTheDocument();
    expect(screen.queryByText("transcription.stage.diarize")).toBeNull();
  });

  it("TRN-19: a snapshot whose counts are null does not throw (stagesKey)", async () => {
    setup();
    await ready();
    const bad = {
      type: "queues.snapshot",
      at: NOW,
      payload: { queues: [{ stage: "transcribe", counts: null }] },
    };
    expect(() => act(() => hub.emit("queues", bad))).not.toThrow();
  });

  it("TRN-19: snapshots without payload, of another type or empty do not refetch each time", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    setup();
    await ready();
    const before = summaryCalls();
    act(() => {
      hub.emit("queues", { type: "queues.snapshot", at: NOW, payload: { queues: [] } });
      hub.emit("queues", { type: "queues.snapshot", at: NOW, payload: { queues: null } });
      hub.emit("queues", { type: "queues.snapshot", at: NOW, payload: {} });
      hub.emit("queues", { type: "queues.snapshot", at: NOW });
      hub.emit("queues", { type: "other" });
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(6_000);
    });
    expect(summaryCalls() - before).toBeLessThanOrEqual(1);
    noCrash();
  });

  it("TRN-20: leaving the page stops polling and the stream", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    setup();
    await ready();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(16_000);
    });
    expect(screen.getByText("Live updates paused, refreshing every 5 s")).toBeInTheDocument();
    expect(hub.count("queues")).toBe(1);
    await userEvent.click(screen.getByRole("link", { name: "go elsewhere" }));
    expect(await screen.findByText("elsewhere")).toBeInTheDocument();
    const after = summaryCalls();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(20_000);
    });
    expect(summaryCalls()).toBe(after);
    expect(hub.count("queues")).toBe(0);
  });

  it("TRN-21: stage and failure links go to their pages; Back shows the report", async () => {
    setup();
    await userEvent.click(await screen.findByRole("link", { name: "Transcribe" }));
    expect(await screen.findByText("stage page")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "go back" }));
    await userEvent.click(await screen.findByRole("link", { name: /Lesson 1/ }));
    expect(await screen.findByText("item page")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "go back" }));
    expect(await screen.findByText("110 h left")).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByText("Loading…")).toBeNull());
  });
});
