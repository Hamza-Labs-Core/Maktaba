import { describe, it, expect, vi, beforeEach } from "vitest";
import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { get, post, put, patch, hub } = vi.hoisted(() => {
  const channels = new Map<string, Set<(ev: unknown) => void>>();
  return {
    get: vi.fn(),
    post: vi.fn(),
    put: vi.fn(),
    patch: vi.fn(),
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
  return { ...actual, api: { ...actual.api, get, post, put, patch } };
});
vi.mock("../../lib/ws", () => ({ subscribe: hub.subscribe }));

import { QueueOverview } from "./QueueOverview";
import { routeGet, renderAt } from "../../test/queueHarness";
import {
  events,
  extensionsFixture,
  failureGroupsFixture,
  healthSeriesFixture,
  librariesFixture,
  logFixture,
  snapshotFixture,
  workersFixture,
} from "../../test/fixtures/queues";

function setup(over: Partial<typeof snapshotFixture> = {}) {
  routeGet(get, {
    "/api/queues": { ...snapshotFixture, ...over },
    "/api/issues": { groups: failureGroupsFixture },
    "/api/queues/libraries": { items: librariesFixture },
    "/api/queues/health": { series: healthSeriesFixture },
    "/api/workers": { workers: workersFixture },
    "/api/extensions": { extensions: extensionsFixture },
    "/api/activity": { items: logFixture, next: null },
  });
  post.mockResolvedValue(undefined);
  return renderAt(<QueueOverview />, "/queue");
}

describe("QueueOverview", () => {
  beforeEach(() => {
    localStorage.clear();
    get.mockReset();
    post.mockReset();
    hub.reset();
  });

  it("renders health, workers, lanes, cards, issues, log and libraries from fixtures", async () => {
    setup();
    expect(await screen.findByRole("link", { name: "Length" })).toHaveAttribute(
      "href",
      "/queue/probe"
    );
    // health strip
    expect(screen.getByText("2 of 2 slots busy")).toBeInTheDocument();
    expect(screen.getByText("3 NAS timeouts in 10 min")).toBeInTheDocument();
    expect(screen.getByText("unreachable")).toBeInTheDocument();
    // workers
    expect(await screen.findByText(/NAS, detection, Plex/)).toBeInTheDocument();
    expect(screen.getByText(/transcription/, { selector: "span" })).toBeInTheDocument();
    // lane titles and cards colour-coded by state, with a text label
    const probe = document.querySelector('[data-stage="probe"]')!;
    expect(probe).toHaveAttribute("data-state", "running");
    expect(within(probe as HTMLElement).getByText("Running")).toBeInTheDocument();
    expect(document.querySelector('[data-stage="link"]')).toHaveAttribute("data-state", "failing");
    expect(document.querySelector('[data-stage="thumb_hash"]')).toHaveAttribute(
      "data-state",
      "done"
    );
    expect(document.querySelector('[data-stage="tag"]')).toHaveAttribute("data-state", "waiting");
    // hidden fp_gate is folded into Place episodes, not its own card
    expect(document.querySelector('[data-stage="fp_gate"]')).toBeNull();
    expect(screen.getByText(/Samples to candidates: 0 running, 3 waiting/)).toBeInTheDocument();
    // ring gauge is accessible
    expect(
      within(probe as HTMLElement).getByRole("img", { name: /2,410 of 2,752 done/ })
    ).toBeInTheDocument();
    // waiting reason and pool chip
    expect(
      within(document.querySelector('[data-stage="tag"]') as HTMLElement).getByText(
        "Waiting for earlier steps"
      )
    ).toBeInTheDocument();
    expect(within(probe as HTMLElement).getByText("nas_read 2/2")).toBeInTheDocument();
    // Media lane has waiting jobs, so it is open by default
    expect(document.querySelector("details.mkt-q-lane--media")).toHaveAttribute("open");
    // issues: grouped by cause with one action, no Apply flow by default
    const issues = screen.getByRole("heading", { name: "Needs attention" }).closest("section")!;
    expect(
      within(issues).getByText("YouTube asked to confirm we are not a bot")
    ).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Apply" })).toBeNull();
    // libraries table
    expect(screen.getByRole("link", { name: /Islamic lectures/ })).toHaveAttribute(
      "href",
      "/queue/items/library/lib-1"
    );
  });

  it("groups extension queues in an Extensions lane with a badge; unconfigured ones are off, not failing", async () => {
    setup();
    const plex = await waitFor(() => {
      const c = document.querySelector('[data-stage="plex_collection"]');
      expect(c).not.toBeNull();
      return c as HTMLElement;
    });
    expect(screen.getByRole("heading", { name: "Extensions" })).toBeInTheDocument();
    expect(within(plex).getByText("Plex extension")).toBeInTheDocument();
    const dz = document.querySelector('[data-stage="dizquetv_show"]') as HTMLElement;
    expect(dz).toHaveAttribute("data-state", "off");
    expect(within(dz).getByText("Not set up")).toBeInTheDocument();
    expect(within(dz).getByRole("link", { name: "Set up in Extensions" })).toHaveAttribute(
      "href",
      "/admin/extensions#dizquetv"
    );
    const seasons = document.getElementById("mkt-q-lane-seasons")!.closest("section")!;
    expect(seasons.querySelector('[data-stage="plex_collection"]')).toBeNull();
    expect(screen.getByRole("link", { name: "Extensions" })).toHaveAttribute(
      "href",
      "/admin/extensions"
    );
  });

  it("shows the small-thumbnail and review-page queues as their own cards", async () => {
    setup();
    expect(await screen.findByRole("link", { name: "Small thumbnail" })).toHaveAttribute(
      "href",
      "/queue/thumb_small"
    );
    expect(screen.getByRole("link", { name: "Review pages" })).toHaveAttribute(
      "href",
      "/queue/review_pages"
    );
    const pages = document.querySelector('[data-stage="review_pages"]') as HTMLElement;
    expect(pages).toHaveAttribute("data-state", "waiting");
    expect(within(pages).getByRole("img", { name: /3 of 4 done/ })).toBeInTheDocument();
    const small = document.querySelector('[data-stage="thumb_small"]') as HTMLElement;
    expect(within(small).getByRole("img", { name: /160 of 200 done/ })).toBeInTheDocument();
  });

  it("shows the waiting-for-a-worker reason when no live worker serves a queue", async () => {
    const queues = snapshotFixture.queues.map((q) =>
      q.stage === "transcribe" ? { ...q, no_worker: true, counts: { ...q.counts, running: 0 } } : q
    );
    setup({ queues });
    const card = await waitFor(() => {
      const c = document.querySelector('[data-stage="transcribe"]');
      expect(c).not.toBeNull();
      return c as HTMLElement;
    });
    expect(
      within(card).getByText("Waiting for a worker that serves Transcribe")
    ).toBeInTheDocument();
  });

  it("applies a live queues.snapshot (pause all flips the button)", async () => {
    setup();
    await screen.findByRole("button", { name: "Pause all" });
    act(() => hub.emit("queues", events.snapshot({ ...snapshotFixture, paused_all: true })));
    expect(await screen.findByRole("button", { name: "Resume all" })).toBeInTheDocument();
    expect(screen.getByRole("status", { name: "" })).toBeDefined();
  });

  it("pause all posts to the API", async () => {
    setup();
    await userEvent.click(await screen.findByRole("button", { name: "Pause all" }));
    expect(post).toHaveBeenCalledWith("/api/queues/pause-all");
  });

  it("retries an issue group by cause", async () => {
    setup();
    await userEvent.click(await screen.findByRole("button", { name: /Retry 2 \(NAS open p95/ }));
    expect(post).toHaveBeenCalledWith("/api/queues/fingerprint/retry", { cause: "nas_timeout" });
  });

  it("appends live job.log lines in one batch per second and filters problems", async () => {
    setup();
    await screen.findByRole("link", { name: "Length" });
    act(() => {
      hub.emit("jobs", events.jobLog({ message: "Length read: 41 min" }));
      hub.emit(
        "jobs",
        events.jobLog({ level: "error", message: "Disk went away", stage: "fingerprint" })
      );
    });
    expect(screen.queryByText(/Disk went away/)).toBeNull(); // batched
    expect(await screen.findByText(/Disk went away/, {}, { timeout: 2500 })).toBeInTheDocument();
    expect(screen.getByText(/Length read: 41 min/)).toBeInTheDocument();
    await userEvent.click(screen.getByLabelText("Problems only"));
    await waitFor(() => expect(screen.queryByText(/Length read: 41 min/)).toBeNull());
    expect(screen.getByText(/Disk went away/)).toBeInTheDocument();
  });

  it("queue work dialog posts a retrigger with the chosen mode", async () => {
    post.mockReset();
    setup();
    post.mockResolvedValue({
      results: [
        {
          subject_kind: "library",
          subject_id: "lib-1",
          stage: "detect",
          job_id: 5,
          outcome: "inserted",
        },
      ],
    });
    await userEvent.click(await screen.findByRole("button", { name: "Queue work…" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByRole("checkbox", { name: /Islamic lectures/ }));
    await userEvent.click(within(dialog).getByRole("radio", { name: /From the start/ }));
    await userEvent.click(within(dialog).getByRole("button", { name: "Queue work (1)" }));
    expect(post).toHaveBeenCalledWith("/api/queue/retrigger", {
      items: [{ subject_kind: "library", subject_id: "lib-1" }],
      mode: "force_cascade",
      priority: 50,
    });
    expect(
      await within(dialog).findByText(/1 queued, 0 added to jobs already waiting/)
    ).toBeInTheDocument();
  });

  it("limits a retrigger to the chosen queues", async () => {
    setup();
    post.mockResolvedValue({ results: [] });
    await userEvent.click(await screen.findByRole("button", { name: "Queue work…" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByRole("checkbox", { name: /Islamic lectures/ }));
    await userEvent.click(within(dialog).getByRole("checkbox", { name: "Place episodes" }));
    await userEvent.click(within(dialog).getByRole("checkbox", { name: "Length" })); // video-only: skipped for a library
    await userEvent.click(within(dialog).getByRole("button", { name: "Queue work (1)" }));
    expect(post).toHaveBeenCalledWith("/api/queue/retrigger", {
      items: [{ subject_kind: "library", subject_id: "lib-1", stage: "detect" }],
      mode: "queue",
      priority: 50,
    });
  });

  it("re-queues several libraries at once from the table", async () => {
    setup();
    await screen.findByRole("link", { name: /Islamic lectures/ });
    await userEvent.click(screen.getByLabelText("Select all libraries"));
    await userEvent.click(screen.getByRole("button", { name: "Re-queue selected (2)" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByRole("button", { name: "Queue work (2)" })).toBeEnabled();
  });

  it("filters the libraries table by name", async () => {
    setup();
    await screen.findByRole("link", { name: /Islamic lectures/ });
    fireEvent.change(screen.getAllByPlaceholderText("Search libraries")[0], {
      target: { value: "قصة" },
    });
    expect(screen.queryByRole("link", { name: /Islamic lectures/ })).toBeNull();
    expect(screen.getByRole("link", { name: /قصة وآية/ })).toBeInTheDocument();
  });

  it("uses only token colours (no inline colour styles) in light and dark", async () => {
    document.documentElement.setAttribute("data-theme", "dark");
    const { container } = setup();
    await screen.findByRole("link", { name: "Length" });
    const bad = [...container.querySelectorAll<HTMLElement>("[style]")].filter((el) =>
      /color|background|#|rgb|fill|stroke/i.test(el.getAttribute("style") ?? "")
    );
    expect(bad).toEqual([]);
    expect(container.querySelector("[fill^='#'], [stroke^='#']")).toBeNull();
    document.documentElement.removeAttribute("data-theme");
  });

  it("renders in Arabic with the localised waiting reason", async () => {
    localStorage.setItem("mkt:locale", "ar");
    setup();
    await waitFor(() => expect(document.querySelector('[data-stage="tag"]')).not.toBeNull());
    expect(
      within(document.querySelector('[data-stage="tag"]') as HTMLElement).getByText(
        "بانتظار الخطوات السابقة"
      )
    ).toBeInTheDocument();
    expect(document.documentElement.dir).toBe("rtl");
    document.documentElement.dir = "ltr";
  });

  it("falls back to polling /api/queues only after 10 s of stream silence", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      setup();
      await screen.findByRole("link", { name: "Length" });
      const snapshotCalls = () => get.mock.calls.filter((c) => c[0] === "/api/queues").length;
      expect(snapshotCalls()).toBe(1);
      await act(async () => {
        await vi.advanceTimersByTimeAsync(9000);
      });
      expect(snapshotCalls()).toBe(1);
      await act(async () => {
        await vi.advanceTimersByTimeAsync(7000); // 10 s silence + one 5 s poll
      });
      expect(snapshotCalls()).toBeGreaterThanOrEqual(2);
      // a stream snapshot stops the polling
      act(() => hub.emit("queues", events.snapshot(snapshotFixture)));
      const before = snapshotCalls();
      await act(async () => {
        await vi.advanceTimersByTimeAsync(6000);
      });
      expect(snapshotCalls()).toBe(before);
    } finally {
      vi.useRealTimers();
    }
  });
});
