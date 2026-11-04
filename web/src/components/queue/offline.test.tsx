// Epic 47 on the Queues page: the action dialog, the YouTube sources dialog and the per-item panel, per state and
// action, admin-only rendering, and axe.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { useState } from "react";
import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { deferred } from "../../test/deferred";

const { get, post, patch, del } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  patch: vi.fn(),
  del: vi.fn(),
}));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post, patch, delete: del } };
});

import { OfflineActionDialog } from "./OfflineActionDialog";
import { YouTubeSourcesDialog } from "./YouTubeSourcesDialog";
import { OfflineItemsPanel } from "./OfflineItemsPanel";
import { renderAt, routeGet } from "../../test/queueHarness";
import { axeViolations } from "../../test/axe";
import { LIB, offlineItem, source } from "../../test/fixtures/offline";

beforeEach(() => {
  get.mockReset();
  post.mockReset();
  patch.mockReset();
  del.mockReset();
});

describe("OfflineActionDialog", () => {
  const res = { stage: "convert_mp4", results: [], enqueued: 1, coalesced: 0, skipped: 0 };

  it("a conversion starts as a dry run, and says so", async () => {
    post.mockResolvedValue(res);
    const onDone = vi.fn();
    renderAt(
      <OfflineActionDialog
        target={{ action: "convert", libraries: [{ id: LIB, name: "Islamic lectures" }] }}
        onClose={() => {}}
        onDone={onDone}
      />,
      "/queue"
    );
    expect(screen.getByRole("checkbox", { name: /Dry run/ })).toBeChecked();
    await userEvent.click(screen.getByRole("button", { name: "Queue the dry run" }));
    expect(post).toHaveBeenCalledWith(`/api/libraries/${LIB}/convert`, {
      dry_run: true,
      all: undefined,
    });
    expect(await screen.findByRole("status")).toHaveTextContent("1 queued");
    expect(onDone).toHaveBeenCalled();
  });

  it("a real duplicate search, for each library", async () => {
    post.mockResolvedValue({ ...res, stage: "dedup_scan" });
    renderAt(
      <OfflineActionDialog
        target={{
          action: "dedup",
          libraries: [
            { id: "a", name: "A" },
            { id: "b", name: "B" },
          ],
        }}
        onClose={() => {}}
      />,
      "/queue"
    );
    await userEvent.click(screen.getByRole("checkbox", { name: /Dry run/ }));
    await userEvent.click(screen.getByRole("button", { name: "Queue it" }));
    await screen.findByRole("status");
    expect(post.mock.calls.map((c) => c[0])).toEqual([
      "/api/libraries/a/dedup-scan",
      "/api/libraries/b/dedup-scan",
    ]);
    expect(post.mock.calls[0][1]).toEqual({ dry_run: false, all: undefined });
  });

  it("a refresh with its options; a step not set up yet is reported", async () => {
    post.mockResolvedValue({ ...res, enqueued: 0, skipped: 1 });
    renderAt(
      <OfflineActionDialog
        target={{ action: "youtube_refresh", libraries: [{ id: LIB, name: "L" }] }}
        onClose={() => {}}
      />,
      "/queue"
    );
    await userEvent.click(screen.getByRole("checkbox", { name: /Read every video/ }));
    await userEvent.click(screen.getByRole("button", { name: "Queue it" }));
    expect(post).toHaveBeenCalledWith(`/api/libraries/${LIB}/youtube/refresh`, {
      all: undefined,
      full_listing: true,
      refresh: undefined,
    });
    expect(await screen.findByText(/not set up on the workers yet/)).toBeInTheDocument();
  });

  it("shows the API's refusal", async () => {
    post.mockRejectedValue(new Error("admin-only"));
    renderAt(
      <OfflineActionDialog
        target={{ action: "convert", libraries: [{ id: LIB, name: "L" }] }}
        onClose={() => {}}
      />,
      "/queue"
    );
    await userEvent.click(screen.getByRole("button", { name: "Queue the dry run" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("admin-only");
  });
});

describe("YouTubeSourcesDialog", () => {
  const lib = { id: LIB, name: "Islamic lectures" };
  const sourcesUrl = `/api/libraries/${LIB}/youtube/sources`;

  it("lists, adds, sets the mode, switches off and removes", async () => {
    routeGet(get, { [sourcesUrl]: { items: [source()] } });
    post.mockResolvedValue({ source: source({ id: "new" }), refresh: { outcome: "inserted" } });
    patch.mockResolvedValue({ source: source(), refresh: null });
    del.mockResolvedValue(undefined);
    vi.spyOn(window, "confirm").mockReturnValue(true);
    const onChanged = vi.fn();
    renderAt(
      <YouTubeSourcesDialog library={lib} onClose={() => {}} onChanged={onChanged} />,
      "/queue"
    );
    expect(
      await screen.findByRole("link", { name: "UCaaaaaaaaaaaaaaaaaaaaaa" })
    ).toBeInTheDocument();
    expect(screen.getByText(/300 downloaded/)).toBeInTheDocument();

    await userEvent.type(
      screen.getByLabelText("Channel or playlist URL"),
      "https://www.youtube.com/@x"
    );
    await userEvent.selectOptions(screen.getByLabelText("Mode"), "new_only");
    await userEvent.click(screen.getByRole("button", { name: "Add" }));
    expect(post).toHaveBeenCalledWith(sourcesUrl, {
      url: "https://www.youtube.com/@x",
      mode: "new_only",
      folder: undefined,
    });
    expect(await screen.findByRole("status")).toHaveTextContent("added and its refresh queued");

    await userEvent.selectOptions(
      screen.getByLabelText("Mode of UCaaaaaaaaaaaaaaaaaaaaaa"),
      "new_only"
    );
    expect(patch).toHaveBeenCalledWith(`${sourcesUrl}/${source().id}`, { mode: "new_only" });

    await userEvent.click(
      screen.getByRole("switch", { name: "Downloads UCaaaaaaaaaaaaaaaaaaaaaa" })
    );
    expect(patch).toHaveBeenLastCalledWith(`${sourcesUrl}/${source().id}`, { on: false });

    await userEvent.click(screen.getByRole("button", { name: "Remove UCaaaaaaaaaaaaaaaaaaaaaa" }));
    expect(del).toHaveBeenCalledWith(`${sourcesUrl}/${source().id}`);
    expect(onChanged).toHaveBeenCalled();
  });

  it("a list read before a removal does not bring the removed source back", async () => {
    const older = deferred<unknown>();
    const answers: Promise<unknown>[] = [
      Promise.resolve({ items: [source()] }),
      // the read after the mode change: held
      older.promise,
      // the read after the removal
      Promise.resolve({ items: [] }),
    ];
    get.mockImplementation(() => answers.shift() ?? Promise.resolve({ items: [] }));
    patch.mockResolvedValue({ source: source(), refresh: null });
    del.mockResolvedValue(undefined);
    vi.spyOn(window, "confirm").mockReturnValue(true);
    renderAt(<YouTubeSourcesDialog library={lib} onClose={() => {}} />, "/queue");
    const name = "UCaaaaaaaaaaaaaaaaaaaaaa";
    expect(await screen.findByRole("link", { name })).toBeInTheDocument();

    await userEvent.selectOptions(screen.getByLabelText(`Mode of ${name}`), "new_only");
    await waitFor(() => expect(get).toHaveBeenCalledTimes(2));
    await userEvent.click(screen.getByRole("button", { name: `Remove ${name}` }));
    expect(del).toHaveBeenCalledWith(`${sourcesUrl}/${source().id}`);
    await waitFor(() => expect(get).toHaveBeenCalledTimes(3));
    await waitFor(() => expect(screen.queryByRole("link", { name })).toBeNull());

    older.resolve({ items: [source()] });
    await act(async () => {
      await older.promise;
    });
    expect(screen.queryByRole("link", { name })).toBeNull();
  });

  it("every source has its switch, off or on; on sends {on: true} and the mode shows only while on", async () => {
    const off = source({ id: "s-off", external_id: "UCoffoffoffoffoffoffoffo", mode: "off" });
    const on = source({ id: "s-on", external_id: "UCononononononononononon", mode: "new_only" });
    routeGet(get, { [sourcesUrl]: { items: [off, on] } });
    patch.mockResolvedValue({
      source: { ...off, mode: "new_only", last_on_mode: "new_only" },
      refresh: { outcome: "inserted" },
    });
    renderAt(<YouTubeSourcesDialog library={lib} onClose={() => {}} />, "/queue");
    const offSwitch = await screen.findByRole("switch", {
      name: "Downloads UCoffoffoffoffoffoffoffo",
    });
    const onSwitch = screen.getByRole("switch", { name: "Downloads UCononononononononononon" });
    expect(offSwitch).not.toBeChecked();
    expect(offSwitch).toHaveAttribute("aria-checked", "false");
    expect(onSwitch).toBeChecked();
    expect(onSwitch).toHaveAttribute("aria-checked", "true");
    expect(screen.queryByLabelText("Mode of UCoffoffoffoffoffoffoffo")).toBeNull();
    const mode = screen.getByLabelText("Mode of UCononononononononononon");
    expect(within(mode).queryByRole("option", { name: "Off" })).toBeNull();

    await userEvent.click(offSwitch);
    expect(patch).toHaveBeenCalledWith(`${sourcesUrl}/s-off`, { on: true });
    expect(await screen.findByRole("status")).toHaveTextContent(
      "Turned on; its refresh is queued."
    );
  });

  it("saves the options as the pipeline reads them", async () => {
    routeGet(get, { [sourcesUrl]: { items: [source()] } });
    patch.mockResolvedValue({ source: source(), refresh: null });
    renderAt(<YouTubeSourcesDialog library={lib} onClose={() => {}} />, "/queue");
    await userEvent.click(
      await screen.findByRole("button", { name: "Options… UCaaaaaaaaaaaaaaaaaaaaaa" })
    );
    await userEvent.selectOptions(screen.getByLabelText("Highest quality"), "720");
    await userEvent.click(screen.getByRole("checkbox", { name: "Audio only" }));
    await userEvent.type(screen.getByLabelText("Cookies from browser"), "firefox");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(patch).toHaveBeenCalledWith(`${sourcesUrl}/${source().id}`, {
      options: {
        max_height: 720,
        sub_langs: "ar,en",
        auto_subs: true,
        audio_only: true,
        cookies_from_browser: "firefox",
        limit_rate: null,
      },
    });
  });

  it("retries gone/skipped, resumes the back catalogue and imports Tartube", async () => {
    routeGet(get, { [sourcesUrl]: { items: [source()] } });
    post.mockImplementation((url: string) =>
      Promise.resolve(
        url.endsWith("/retry")
          ? { wanted: 4, new: 1, backfill: 3, results: [] }
          : { stage: "x", results: [], enqueued: 1, coalesced: 0, skipped: 0 }
      )
    );
    renderAt(<YouTubeSourcesDialog library={lib} onClose={() => {}} />, "/queue");
    await userEvent.click(
      await screen.findByRole("button", { name: "Retry gone/skipped (4) UCaaaaaaaaaaaaaaaaaaaaaa" })
    );
    expect(post).toHaveBeenCalledWith(`/api/libraries/${LIB}/youtube/retry`, {
      states: ["gone", "skipped", "failed"],
      source_id: source().id,
    });
    expect(await screen.findByRole("status")).toHaveTextContent("4 downloads are wanted again");

    await userEvent.click(screen.getByRole("button", { name: "Resume back catalogue (40)" }));
    expect(post).toHaveBeenCalledWith(`/api/libraries/${LIB}/youtube/backfill`);

    await userEvent.click(screen.getByRole("button", { name: "Import from Tartube…" }));
    await userEvent.type(
      screen.getByLabelText("Path on the home"),
      "/Volumes/O/tartube_db_export.json"
    );
    await userEvent.click(screen.getByRole("button", { name: "Import" }));
    expect(post).toHaveBeenCalledWith(`/api/libraries/${LIB}/youtube/tartube`, {
      path: "/Volumes/O/tartube_db_export.json",
      channels: undefined,
    });
  });

  it("shows a source's problem, an empty list and the API's refusal; has no axe violations", async () => {
    routeGet(get, {
      [sourcesUrl]: { items: [source({ status: "bot_check", error: "Sign in to confirm" })] },
    });
    post.mockRejectedValue(new Error("not a YouTube channel or playlist URL"));
    renderAt(<YouTubeSourcesDialog library={lib} onClose={() => {}} />, "/queue");
    expect(
      await screen.findByText(/YouTube asked for a bot check: Sign in to confirm/)
    ).toBeInTheDocument();
    await userEvent.type(screen.getByLabelText("Channel or playlist URL"), "https://x.example/a");
    await userEvent.click(screen.getByRole("button", { name: "Add" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("not a YouTube channel");
    expect(await axeViolations(document.body)).toEqual([]);
  });

  it("an empty list says how to start", async () => {
    routeGet(get, { [sourcesUrl]: { items: [] } });
    renderAt(<YouTubeSourcesDialog library={lib} onClose={() => {}} />, "/queue");
    expect(await screen.findByText(/No sources yet/)).toBeInTheDocument();
  });
});

describe("OfflineItemsPanel", () => {
  it("shows where each item is, with the job's Cancel and Retry", async () => {
    routeGet(get, {
      "/api/queues/offline-items": {
        items: [
          offlineItem(),
          offlineItem({
            id: 502,
            stage: "dedup_scan",
            step: "dedup",
            state: "done",
            subject_label: "Lecture 06",
            waiting_for_conversion: true,
            progress: null,
          }),
          offlineItem({
            id: 503,
            stage: "youtube_fetch",
            step: "download",
            state: "running",
            subject_kind: "library",
            subject_label: "Islamic lectures",
            downloading: "abcdefghijk",
            progress: null,
          }),
          offlineItem({
            id: 504,
            stage: "convert_mp4",
            state: "failed",
            subject_label: "Lecture 07",
            error: "frames differ",
            progress: null,
          }),
        ],
        next: null,
      },
    });
    post.mockResolvedValue({});
    const { container } = renderAt(<OfflineItemsPanel />, "/queue");
    expect(await screen.findByText("Converting")).toBeInTheDocument();
    expect(screen.getByText("40 of 100")).toBeInTheDocument();
    expect(screen.getByText("Waiting for conversion")).toBeInTheDocument();
    expect(screen.getByText("fetching abcdefghijk")).toBeInTheDocument();
    expect(screen.getByText("frames differ")).toBeInTheDocument();
    // a check waiting for its conversion has no controls of its own
    const waiting = screen.getByText(/Lecture 06/).closest("tr")!;
    expect(within(waiting).queryByRole("button")).toBeNull();

    await userEvent.click(screen.getByRole("button", { name: "Cancel Lecture 05" }));
    expect(post).toHaveBeenCalledWith("/api/jobs/501/cancel");
    await userEvent.click(screen.getByRole("button", { name: "Retry Lecture 07" }));
    expect(post).toHaveBeenCalledWith("/api/jobs/504/retry");
    expect(await axeViolations(container)).toEqual([]);
  });

  it("filters by step", async () => {
    routeGet(get, { "/api/queues/offline-items": { items: [], next: null } });
    renderAt(<OfflineItemsPanel />, "/queue");
    expect(await screen.findByText(/Nothing is waiting/)).toBeInTheDocument();
    await userEvent.selectOptions(screen.getByLabelText("Step"), "dedup");
    await waitFor(() =>
      expect(get.mock.calls.some((c) => String(c[0]).includes("step=dedup"))).toBe(true)
    );
  });

  // every finished job reloads the list: one still on its way when a job is
  // cancelled has the job as running, and must not bring it back
  it("a reload that began before a Cancel does not bring the cancelled job back", async () => {
    const older = deferred<unknown>();
    const answers: Promise<unknown>[] = [
      Promise.resolve({ items: [offlineItem()], next: null }),
      older.promise,
      Promise.resolve({ items: [], next: null }),
    ];
    get.mockImplementation(() => answers.shift() ?? Promise.resolve({ items: [], next: null }));
    post.mockResolvedValue({});
    function Ticking() {
      const [tick, setTick] = useState(0);
      return (
        <>
          <button onClick={() => setTick((n) => n + 1)}>job finished</button>
          <OfflineItemsPanel tick={tick} />
        </>
      );
    }
    renderAt(<Ticking />, "/queue");
    expect(await screen.findByRole("link", { name: /Lecture 05/ })).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "job finished" }));
    await waitFor(() => expect(get).toHaveBeenCalledTimes(2));

    await userEvent.click(screen.getByRole("button", { name: "Cancel Lecture 05" }));
    expect(post).toHaveBeenCalledWith("/api/jobs/501/cancel");
    expect(await screen.findByText(/Nothing is waiting/)).toBeInTheDocument();

    older.resolve({ items: [offlineItem()], next: null });
    await act(async () => {
      await older.promise;
    });
    expect(screen.queryByRole("link", { name: /Lecture 05/ })).toBeNull();
    expect(screen.getByText(/Nothing is waiting/)).toBeInTheDocument();
  });

  it("Show more read before a Cancel does not append its older page", async () => {
    const page2 = deferred<unknown>();
    const answers: Promise<unknown>[] = [
      Promise.resolve({ items: [offlineItem()], next: "c1" }),
      page2.promise,
      Promise.resolve({ items: [], next: null }),
    ];
    get.mockImplementation(() => answers.shift() ?? Promise.resolve({ items: [], next: null }));
    post.mockResolvedValue({});
    renderAt(<OfflineItemsPanel />, "/queue");
    expect(await screen.findByRole("link", { name: /Lecture 05/ })).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Show more" }));
    expect(get).toHaveBeenLastCalledWith(expect.stringContaining("before=c1"));

    await userEvent.click(screen.getByRole("button", { name: "Cancel Lecture 05" }));
    expect(await screen.findByText(/Nothing is waiting/)).toBeInTheDocument();

    page2.resolve({ items: [offlineItem({ id: 502, subject_label: "Lecture 06" })], next: null });
    await act(async () => {
      await page2.promise;
    });
    expect(screen.queryByRole("link", { name: /Lecture 06/ })).toBeNull();
    expect(screen.getByText(/Nothing is waiting/)).toBeInTheDocument();
  });
});
