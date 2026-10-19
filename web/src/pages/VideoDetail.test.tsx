// Video detail cases from web/test-cases/video-detail.md.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { screen, waitFor, within, fireEvent } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Route, useLocation } from "react-router-dom";
import { ToastProvider } from "@ds/components/Toast/Toast";
import { ApiError } from "../lib/api";
import { renderPage, memberUser, adminUser, quietCrash } from "../test/pageHarness";
import { makeHub } from "../test/queueHarness";

const { get, post, patch, hub } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  patch: vi.fn(),
  hub: { current: null as null | ReturnType<typeof makeHub> },
}));
vi.mock("../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../lib/api")>("../lib/api");
  return { ...actual, api: { ...actual.api, get, post, patch } };
});
const subscribe = vi.fn((ch: string, fn: (ev: unknown) => void) => {
  const off = hub.current!.subscribe(ch, fn);
  const spy = vi.fn(off);
  unsubscribes.push(spy);
  return spy;
});
const unsubscribes: Array<ReturnType<typeof vi.fn>> = [];
vi.mock("../lib/ws", () => ({
  subscribe: (ch: string, fn: (ev: unknown) => void) => subscribe(ch, fn),
}));

import { VideoDetail } from "./VideoDetail";

type Answer = unknown | ((url: string) => unknown);

const problem = (status: number, title: string, detail?: string) =>
  new ApiError({ type: "about:blank", title, status, detail });
const reject = (e: unknown) => () => Promise.reject(e);

const V1 = {
  id: "v1",
  title: "Lecture 1",
  filename: "l1.mp4",
  description: "Intro talk",
  state: "ready",
  detected_language: "en",
  library_id: "lib-1",
  display_title: "Lecture 1",
  display_title_source: "rules",
};
const EMPTY_CONTEXT = { related_in_library: [], more_like_this: [] };

let table: Record<string, Answer> = {};

/** Serves GETs by path (query string ignored). Values may be functions that throw or return promises. */
function serve(over: Record<string, Answer> = {}) {
  table = {
    "/api/videos/v1": V1,
    "/api/videos/v1/context": EMPTY_CONTEXT,
    ...over,
  };
  get.mockImplementation((url: string) => {
    const path = url.split("?")[0];
    if (!(path in table)) return Promise.reject(new Error(`unmocked GET ${url}`));
    const v = table[path];
    try {
      return Promise.resolve(typeof v === "function" ? (v as (u: string) => unknown)(url) : v);
    } catch (e) {
      return Promise.reject(e);
    }
  });
}

function Loc() {
  const { pathname, search } = useLocation();
  return <span data-testid="loc">{pathname + search}</span>;
}

function renderVideo(
  path = "/videos/v1",
  opts: { user?: typeof adminUser | null; history?: string[] } = {}
) {
  return renderPage(
    <ToastProvider>
      <VideoDetail />
      <Loc />
    </ToastProvider>,
    {
      path,
      route: "/videos/:videoId",
      user: opts.user === undefined ? memberUser : opts.user,
      history: opts.history,
      extraRoutes: (
        <>
          <Route path="/videos/:videoId/watch" element={<p>player page</p>} />
          <Route path="/library" element={<p>library page</p>} />
        </>
      ),
    }
  );
}

const gets = (suffix: string) =>
  get.mock.calls.map((c) => String(c[0])).filter((u) => u.split("?")[0].endsWith(suffix));

async function openTab(name: string) {
  await userEvent.setup().click(await screen.findByRole("tab", { name }));
}

describe("VideoDetail (test-cases/video-detail.md)", () => {
  beforeEach(() => {
    get.mockReset();
    post.mockReset();
    patch.mockReset();
    subscribe.mockClear();
    unsubscribes.length = 0;
    hub.current = makeHub();
  });
  afterEach(() => vi.restoreAllMocks());

  it("VID-01: header, badges, tabs and Play link render", async () => {
    serve();
    renderVideo();
    const h1 = await screen.findByRole("heading", { level: 1, name: "Lecture 1" });
    expect(h1).toHaveAttribute("dir", "auto");
    expect(screen.getByText("en", { selector: ".mk-badge" })).toBeInTheDocument();
    expect(screen.getByText("ready", { selector: ".mk-badge" })).toHaveClass("mk-badge--success");
    const display = screen.getByTestId("display-title");
    expect(display).toHaveTextContent("On-air title:");
    expect(display).toHaveTextContent("Lecture 1");
    expect(display).toHaveTextContent("by the rules");
    const tablist = screen.getByRole("tablist", { name: "Lecture 1" });
    expect(
      within(tablist)
        .getAllByRole("tab")
        .map((t) => t.textContent)
    ).toEqual(["Watch", "Metadata", "Transcript", "Chapters", "Files", "Processing", "Statistics"]);
    expect(screen.getByRole("tab", { name: "Watch" })).toHaveAttribute("aria-selected", "true");
    expect(screen.getByText("Intro talk")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "▶ Play" })).toHaveAttribute(
      "href",
      "/videos/v1/watch"
    );
  });

  it("VID-01: with title null the h1 shows the filename", async () => {
    serve({ "/api/videos/v1": { ...V1, title: null } });
    renderVideo();
    expect(await screen.findByRole("heading", { level: 1, name: "l1.mp4" })).toBeInTheDocument();
  });

  const tabEmpty: Array<[string, string, string, string]> = [
    ["Transcript", "/api/videos/v1/segments", "items", "No transcript available."],
    ["Chapters", "/api/videos/v1/chapters", "items", "Nothing here yet."],
    ["Files", "/api/videos/v1/subtitles", "items", "Nothing here yet."],
    ["Processing", "/api/jobs", "items", "Nothing here yet."],
  ];
  for (const [tab, path, field, text] of tabEmpty) {
    it.each([
      ["null", { [field]: null }],
      ["missing", {}],
      ["500", reject(problem(500, "boom"))],
    ])(`VID-02: ${tab} with ${field} %s shows its empty state`, async (_l, answer) => {
      serve({ [path]: answer });
      renderVideo();
      await openTab(tab);
      expect(await screen.findByText(text)).toBeInTheDocument();
      if (tab === "Files") {
        expect(screen.getByRole("heading", { level: 2, name: "Subtitles" })).toBeInTheDocument();
      }
      expect(screen.queryByRole("alert")).toBeNull();
    });
  }

  it.todo("VID-02: a failed tab fetch shows an error message, not the empty state");

  it.each([
    ["lists null", { related_in_library: null, more_like_this: null }],
    ["empty object", {}],
    [
      "null facts lists",
      {
        facts: { genres: null, cast: null, attribution: null, rating: null },
        related_in_library: [],
        more_like_this: [],
      },
    ],
    ["500", reject(problem(500, "boom"))],
  ])("VID-03: context %s shows the minimal card", async (_l, answer) => {
    serve({ "/api/videos/v1/context": answer });
    renderVideo();
    expect(await screen.findByText("No web metadata yet for this video.")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Find metadata" })).toBeInTheDocument();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("VID-03: a null entry in cast is skipped", async () => {
    serve({
      "/api/videos/v1/context": { facts: { summary: "S", cast: [null, { name: "A" }] } },
    });
    renderVideo();
    expect(await screen.findByText("S")).toBeInTheDocument();
    expect(screen.getByText("Cast:").parentElement).toHaveTextContent("Cast: A");
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it.each([
    ["candidates null", { candidates: null }],
    ["empty object", {}],
    ["500", reject(problem(500, "boom"))],
  ])("VID-04: enrichment %s shows No suggestions", async (_l, answer) => {
    serve({ "/api/videos/v1/enrichment": answer });
    renderVideo();
    await openTab("Metadata");
    expect(await screen.findByText("No suggestions")).toBeInTheDocument();
    expect(screen.getByText("Nothing found yet. Search with your own words, or search again from what Maktaba knows about this video.")).toBeInTheDocument();
  });

  it("VID-04: a candidate with fields null shows the headline but no diff; search null is empty", async () => {
    serve({
      "/api/videos/v1/enrichment": {
        candidates: [
          {
            id: "c1",
            provider: "tmdb",
            external_id: "tmdb:1",
            confidence: 0.8,
            accepted: false,
            fields: null,
          },
        ],
      },
    });
    post.mockResolvedValue({ candidates: null });
    renderVideo();
    await openTab("Metadata");
    expect(await screen.findByText(/We found this might be tmdb:1/)).toBeInTheDocument();
    expect(screen.getByText("80% match")).toBeInTheDocument();
    expect(screen.queryByRole("table")).toBeNull();
    expect(screen.getByRole("button", { name: "Accept" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Dismiss" })).toBeInTheDocument();
    const user = userEvent.setup();
    await user.type(screen.getByRole("searchbox", { name: "Search manually" }), "x");
    await user.click(screen.getByRole("button", { name: "Search manually" }));
    expect(await screen.findByText("No suggestions")).toBeInTheDocument();
  });

  it.todo("VID-04: an enrichment load failure shows an error, not No suggestions");

  it("VID-05: stats with zeros and viewers null", async () => {
    serve({
      "/api/videos/v1/stats": {
        total_views: 0,
        unique_viewers: 0,
        avg_completion: 0,
        avg_watch_sec: 0,
        completion_rate: 0,
        viewers: null,
      },
    });
    renderVideo("/videos/v1", { user: adminUser });
    await openTab("Statistics");
    await screen.findByText("Total views");
    const values = Array.from(document.querySelectorAll(".mkt-kpi__value")).map(
      (e) => e.textContent
    );
    expect(values).toEqual(["0", "0", "0%", "0m", "0%"]);
    expect(screen.queryByText("Per-user breakdown")).toBeNull();
  });

  it("VID-05: stats {} shows dashes, not undefined or NaN", async () => {
    serve({ "/api/videos/v1/stats": {} });
    renderVideo("/videos/v1", { user: adminUser });
    await openTab("Statistics");
    await screen.findByText("Total views");
    const values = Array.from(document.querySelectorAll(".mkt-kpi__value")).map(
      (e) => e.textContent
    );
    expect(values).toEqual(["—", "—", "—", "—", "—"]);
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("VID-06: render crash shows route error fallback; Back still works", async () => {
    quietCrash();
    let bad = true;
    serve({
      // an object where the description string is rendered
      "/api/videos/v1": () => (bad ? { ...V1, description: {} } : V1),
    });
    renderVideo();
    expect(
      await screen.findByText("This page hit an error and could not be shown.")
    ).toBeInTheDocument();
    const user = userEvent.setup();
    await user.click(screen.getByRole("link", { name: "go elsewhere" }));
    expect(screen.getByText("elsewhere")).toBeInTheDocument();
    bad = false;
    await user.click(screen.getByRole("button", { name: "go back" }));
    expect(await screen.findByText("Intro talk")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it.each([404, 403])(
    "VID-07/VID-10: a %s shows Video not found with a link to the Library; only the header is requested",
    async (status) => {
      serve({ "/api/videos/v-secret": reject(problem(status, "video not found")) });
      renderVideo("/videos/v-secret");
      const alert = await screen.findByRole("alert");
      expect(alert).toHaveAttribute("data-kind", "not_found");
      expect(alert).toHaveTextContent("Video not found.");
      expect(alert).toHaveTextContent("video not found");
      expect(screen.queryByRole("tablist")).toBeNull();
      expect(get.mock.calls.map((c) => c[0])).toEqual(["/api/videos/v-secret"]);
      await userEvent.setup().click(within(alert).getByRole("link", { name: "Library" }));
      expect(screen.getByText("library page")).toBeInTheDocument();
    }
  );

  it("VID-08: non-admin: no title edit, no per-user stats", async () => {
    serve({
      "/api/videos/v1": { ...V1, display_title: "T" },
      "/api/videos/v1/stats": {
        total_views: 1,
        unique_viewers: 1,
        avg_completion: 50,
        avg_watch_sec: 60,
        completion_rate: 0.5,
        viewers: [
          {
            user_id: "u2",
            username: "sara",
            times_watched: 1,
            total_watch_sec: 60,
            best_percent: 50,
          },
        ],
      },
    });
    renderVideo();
    await screen.findByRole("heading", { level: 1 });
    expect(screen.queryByRole("button", { name: "Change" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Back to the automatic title" })).toBeNull();
    await openTab("Statistics");
    await screen.findByText("Total views");
    expect(screen.queryByText("Per-user breakdown")).toBeNull();
    expect(screen.queryByText("sara")).toBeNull();
  });

  it("VID-08: non-admin with no display title sees no DisplayTitle block; admin sees edit", async () => {
    serve({ "/api/videos/v1": { ...V1, display_title: null, display_title_source: null } });
    const first = renderVideo();
    await screen.findByRole("heading", { level: 1 });
    expect(screen.queryByTestId("display-title")).toBeNull();
    first.unmount();
    renderVideo("/videos/v1", { user: adminUser });
    expect(await screen.findByRole("button", { name: "Change" })).toBeInTheDocument();
  });

  it.fails("VID-08: non-admin sees no Accept / Dismiss / Search manually", async () => {
    serve({
      "/api/videos/v1/enrichment": {
        candidates: [
          { id: "c1", provider: "tmdb", external_id: "tmdb:1", confidence: 0.8, accepted: false },
        ],
      },
    });
    renderVideo();
    await openTab("Metadata");
    await screen.findByText(/We found this might be/);
    expect(screen.queryByRole("button", { name: "Accept" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Search manually" })).toBeNull();
  });

  it("VID-09: signed-out user is sent to /login and no video is requested", () => {
    serve();
    renderVideo("/videos/v1", { user: null });
    expect(screen.getByText("login page (from /videos/v1)")).toBeInTheDocument();
    expect(get).not.toHaveBeenCalled();
  });

  it.each([
    ["500", problem(500, "db down"), "db down"],
    ["network failure", new TypeError("Failed to fetch"), "Something went wrong."],
    ["malformed JSON", new SyntaxError("Unexpected token b"), "Something went wrong."],
  ])("VID-11: %s is a server error with Try again, not not-found", async (_l, e, desc) => {
    let fail = true;
    serve({ "/api/videos/v1": () => (fail ? Promise.reject(e) : V1) });
    renderVideo();
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveAttribute("data-kind", "server");
    expect(alert).not.toHaveTextContent("Video not found.");
    expect(alert).toHaveTextContent(desc);
    fail = false;
    await userEvent.setup().click(within(alert).getByRole("button", { name: "Try again" }));
    expect(await screen.findByRole("heading", { level: 1, name: "Lecture 1" })).toBeInTheDocument();
  });

  it("VID-12: loading state for the header and each tab", async () => {
    const pending = () => new Promise(() => {});
    serve({ "/api/videos/v1": pending });
    const first = renderVideo();
    expect(screen.getByText("Loading…")).toBeInTheDocument();
    expect(screen.queryByRole("tablist")).toBeNull();
    first.unmount();
    serve({
      "/api/videos/v1/context": pending,
      "/api/videos/v1/enrichment": pending,
      "/api/videos/v1/segments": pending,
      "/api/videos/v1/chapters": pending,
      "/api/videos/v1/subtitles": pending,
      "/api/jobs": pending,
      "/api/videos/v1/stats": pending,
    });
    renderVideo();
    await screen.findByRole("tablist");
    expect(document.querySelector(".mkt-context-card")).toBeNull();
    for (const tab of ["Metadata", "Transcript", "Chapters", "Files", "Processing", "Statistics"]) {
      await openTab(tab);
      expect(within(screen.getByRole("tabpanel")).getByText("Loading…")).toBeInTheDocument();
    }
  });

  it("VID-13: transcript timestamps link to the player at ?t=", async () => {
    serve({
      "/api/videos/v1/segments": {
        items: [
          { id: 1, seq: 1, start_sec: 65.7, end_sec: 70, text: "Hello", speaker: "A" },
          { id: 2, seq: 2, start_sec: 3725, end_sec: 3730, text: "Bye", speaker: null },
        ],
      },
    });
    renderVideo();
    await openTab("Transcript");
    const rows = await screen.findAllByText((_, el) => el?.tagName === "P" && !!el.className);
    const segs = rows.filter((r) => r.classList.contains("mkt-transcript__seg"));
    expect(segs.map((s) => s.textContent)).toEqual(["1:05 A: Hello", "1:02:05Bye"]);
    expect(screen.getByRole("link", { name: "1:05" })).toHaveAttribute(
      "href",
      "/videos/v1/watch?t=65"
    );
    expect(screen.getByRole("link", { name: "1:02:05" })).toHaveAttribute(
      "href",
      "/videos/v1/watch?t=3725"
    );
    await userEvent.setup().click(screen.getByRole("link", { name: "1:05" }));
    expect(screen.getByText("player page")).toBeInTheDocument();
  });

  it("VID-14: transcript over 400 segments; Load more shows the rest", async () => {
    serve({
      "/api/videos/v1/segments": {
        items: Array.from({ length: 450 }, (_, i) => ({
          id: i,
          seq: i,
          start_sec: i,
          end_sec: i + 1,
          text: `s${i}`,
        })),
      },
    });
    renderVideo();
    await openTab("Transcript");
    await screen.findByText("s0");
    expect(document.querySelectorAll("p.mkt-transcript__seg")).toHaveLength(400);
    await userEvent.setup().click(screen.getByRole("button", { name: "Load more" }));
    expect(document.querySelectorAll("p.mkt-transcript__seg")).toHaveLength(450);
    expect(screen.queryByRole("button", { name: "Load more" })).toBeNull();
  });

  it("VID-15: chapters list and links", async () => {
    serve({
      "/api/videos/v1/chapters": {
        items: [
          { id: 1, seq: 1, start_sec: 0, end_sec: 60, title: "Opening" },
          { id: 2, seq: 2, start_sec: 125.9, end_sec: 300, title: "المقدمة" },
        ],
      },
    });
    renderVideo();
    await openTab("Chapters");
    const opening = await screen.findByRole("link", { name: "0:00 Opening" });
    expect(opening).toHaveAttribute("href", "/videos/v1/watch?t=0");
    expect(opening.closest("ol")).not.toBeNull();
    const intro = screen.getByRole("link", { name: "2:05 المقدمة" });
    expect(intro).toHaveAttribute("href", "/videos/v1/watch?t=125");
    expect(within(intro).getByText("المقدمة")).toHaveAttribute("dir", "auto");
  });

  it("VID-16: files tab subtitles list and default badge", async () => {
    serve({
      "/api/videos/v1/subtitles": {
        items: [
          {
            id: 1,
            language: "ar",
            format: "vtt",
            source: "asr",
            is_default: true,
            url: "/api/videos/v1/subtitles/1.vtt",
          },
          {
            id: 2,
            language: "en",
            format: "srt",
            source: "file",
            is_default: false,
            url: "/api/videos/v1/subtitles/2.srt",
          },
        ],
      },
    });
    renderVideo();
    await openTab("Files");
    expect(await screen.findByRole("link", { name: "ar · vtt" })).toHaveAttribute(
      "href",
      "/api/videos/v1/subtitles/1.vtt"
    );
    expect(screen.getByRole("link", { name: "en · srt" })).toHaveAttribute(
      "href",
      "/api/videos/v1/subtitles/2.srt"
    );
    const items = screen.getAllByRole("listitem");
    expect(within(items[0]).getByText("default")).toBeInTheDocument();
    expect(within(items[1]).queryByText("default")).toBeNull();
  });

  it("VID-17: processing tab rows and live updates", async () => {
    serve({
      "/api/jobs": {
        items: [
          {
            id: "j1",
            video_id: "v1",
            stage: "transcribe",
            state: "running",
            attempts: 1,
            estimated_remaining_sec: 90,
          },
          { id: "j2", video_id: "v2", stage: "probe", state: "running", attempts: 1 },
          { id: "j3", video_id: "v1", stage: "ocr", state: "queued", attempts: 0 },
        ],
      },
    });
    renderVideo();
    await openTab("Processing");
    const table = await screen.findByRole("table", { name: "Processing" });
    const row = () => within(table).getByText("Transcribe").closest("tr")!;
    expect(Array.from(row().querySelectorAll("td")).map((td) => td.textContent?.trim())).toEqual([
      "Transcribe",
      "running",
      "1:30",
      "1",
    ]);
    expect(within(table).getByText("ocr")).toBeInTheDocument();
    expect(within(table).getAllByRole("row")).toHaveLength(3);
    expect(subscribe).toHaveBeenCalledWith("library/lib-1", expect.any(Function));
    hub.current!.emit("library/lib-1", {
      type: "job",
      payload: {
        job: {
          id: "j1",
          video_id: "v1",
          stage: "transcribe",
          state: "failed",
          attempts: 2,
          error: "OOM",
        },
      },
    });
    await waitFor(() => expect(within(row()).getByText("failed")).toHaveClass("mk-badge--error"));
    expect(row()).toHaveTextContent("OOM");
    expect(row().lastElementChild).toHaveTextContent("2");
    hub.current!.emit("library/lib-1", {
      type: "job",
      payload: { job: { id: "j9", video_id: "v2", stage: "probe", state: "done", attempts: 1 } },
    });
    expect(within(table).getAllByRole("row")).toHaveLength(3);
    await openTab("Watch");
    expect(unsubscribes.at(-1)).toHaveBeenCalled();
  });

  it("VID-17: with library_id null, subscribe is not called", async () => {
    serve({ "/api/videos/v1": { ...V1, library_id: null }, "/api/jobs": { items: [] } });
    renderVideo();
    await openTab("Processing");
    await screen.findByText("Nothing here yet.");
    expect(subscribe).not.toHaveBeenCalled();
  });

  it("VID-18: stats tab cards; admin per-user table", async () => {
    serve({
      "/api/videos/v1/stats": {
        total_views: 12,
        unique_viewers: 3,
        avg_completion: 64.4,
        avg_watch_sec: 3720,
        completion_rate: 0.25,
        viewers: [
          {
            user_id: "u1",
            username: "مها",
            times_watched: 4,
            total_watch_sec: 7200,
            best_percent: 100,
          },
        ],
      },
    });
    renderVideo("/videos/v1", { user: adminUser });
    await openTab("Statistics");
    await screen.findByText("Total views");
    const values = Array.from(document.querySelectorAll(".mkt-kpi__value")).map(
      (e) => e.textContent
    );
    expect(values).toEqual(["12", "3", "64%", "1h 2m", "25%"]);
    await userEvent.setup().click(screen.getByText("Per-user breakdown"));
    const table = screen.getByRole("table", { name: "Per-user breakdown" });
    expect(within(table).getByText("مها")).toHaveAttribute("dir", "auto");
    const cells = Array.from(within(table).getAllByRole("row")[1].querySelectorAll("td")).map(
      (td) => td.textContent
    );
    expect(cells).toEqual(["مها", "4", "2h", "100%"]);
  });

  it("VID-19: stats error shows ErrorState inside the tab only", async () => {
    serve({ "/api/videos/v1/stats": reject(problem(500, "stats failed")) });
    renderVideo();
    await openTab("Statistics");
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Something went wrong.");
    expect(alert).toHaveTextContent("stats failed");
    expect(screen.getByRole("tabpanel")).toContainElement(alert);
    await openTab("Watch");
    expect(screen.queryByRole("alert")).toBeNull();
    expect(screen.getByText("Intro talk")).toBeInTheDocument();
  });

  it("VID-20: MediaContextCard full card", async () => {
    serve({
      "/api/videos/v1/context": {
        facts: {
          rating: { imdb: 8.53, tmdb: 7 },
          runtime_min: 95,
          genres: ["Drama", "History"],
          content_rating: "PG-13",
          cast: [{ name: "A" }, { name: "B" }],
          director: "D",
          summary: "ملخص",
          summary_lang: "ar",
          attribution: [
            { provider: "TMDB", url: "https://www.themoviedb.org/movie/1" },
            { provider: "Wikidata" },
          ],
        },
        related_in_library: [
          { video_id: "v2", title: "Part 2", reason: "same_series" },
          { video_id: "v3", title: "Other", reason: "shared_cast", via: "A" },
        ],
        more_like_this: [{ video_id: "v4", title: "Similar", score: 0.9 }],
      },
    });
    renderVideo();
    expect(await screen.findByText("★ IMDB 8.5 · TMDB 7.0")).toBeInTheDocument();
    expect(screen.getByText("95 min")).toBeInTheDocument();
    expect(screen.getByText("PG-13")).toBeInTheDocument();
    expect(screen.getByText("Drama")).toBeInTheDocument();
    expect(screen.getByText("History")).toBeInTheDocument();
    const summary = screen.getByText("ملخص");
    expect(summary).toHaveAttribute("lang", "ar");
    expect(summary).toHaveAttribute("dir", "auto");
    expect(screen.getByText("Director:").parentElement).toHaveTextContent("Director: D");
    expect(screen.getByText("Cast:").parentElement).toHaveTextContent("Cast: A, B");
    const tmdb = screen.getByRole("link", { name: "Data from TMDB ↗" });
    expect(tmdb).toHaveAttribute("target", "_blank");
    expect(tmdb).toHaveAttribute("rel", "noopener noreferrer");
    expect(screen.queryByRole("link", { name: "Data from Wikidata ↗" })).toBeNull();
    expect(screen.getByText("Data from Wikidata ↗")).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Related in your library" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /Part 2/ })).toHaveTextContent("Part 2Same series");
    expect(screen.getByRole("link", { name: /Other/ })).toHaveTextContent("OtherShared cast · A");
    expect(screen.getByRole("heading", { name: "More like this" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Similar" })).toHaveAttribute("href", "/videos/v4");
    expect(screen.queryByRole("link", { name: "Find metadata" })).toBeNull();
  });

  it("VID-20: a numeric rating shows one decimal", async () => {
    serve({ "/api/videos/v1/context": { facts: { rating: 7.25 } } });
    renderVideo();
    expect(await screen.findByText(`★ ${(7.25).toFixed(1)}`)).toBeInTheDocument();
  });

  it("VID-21: Find metadata opens the Metadata tab; ?tab=enrichment deep link too", async () => {
    serve({ "/api/videos/v1/enrichment": { candidates: [] } });
    const first = renderVideo();
    await userEvent.setup().click(await screen.findByRole("link", { name: "Find metadata" }));
    expect(screen.getByTestId("loc")).toHaveTextContent("/videos/v1?tab=enrichment");
    expect(screen.getByRole("tab", { name: "Metadata" })).toHaveAttribute("aria-selected", "true");
    expect(await screen.findByText("No suggestions")).toBeInTheDocument();
    first.unmount();
    renderVideo("/videos/v1?tab=enrichment");
    expect(await screen.findByRole("tab", { name: "Metadata" })).toHaveAttribute(
      "aria-selected",
      "true"
    );
  });

  it("VID-22: related link switches to another video; Back returns to v1", async () => {
    serve({
      "/api/videos/v1/context": {
        related_in_library: [{ video_id: "v2", title: "Part 2", reason: "same_series" }],
        more_like_this: [],
      },
      "/api/videos/v1/chapters": {
        items: [{ id: 1, seq: 1, start_sec: 0, end_sec: 1, title: "v1 chapter" }],
      },
      "/api/videos/v2": { ...V1, id: "v2", title: "Lecture 2", display_title: "L2" },
      "/api/videos/v2/context": EMPTY_CONTEXT,
    });
    renderVideo();
    await openTab("Chapters");
    await screen.findByText("v1 chapter");
    await openTab("Watch");
    await userEvent.setup().click(await screen.findByRole("link", { name: /Part 2/ }));
    expect(screen.getByTestId("loc")).toHaveTextContent("/videos/v2");
    expect(await screen.findByRole("heading", { level: 1, name: "Lecture 2" })).toBeInTheDocument();
    expect(screen.queryByText("Lecture 1")).toBeNull();
    expect(gets("/api/videos/v2")).toHaveLength(1);
    expect(gets("/api/videos/v2/context")).toHaveLength(1);
    expect(screen.getByRole("tab", { name: "Watch" })).toHaveAttribute("aria-selected", "true");
    await userEvent.setup().click(screen.getByRole("button", { name: "go back" }));
    expect(await screen.findByRole("heading", { level: 1, name: "Lecture 1" })).toBeInTheDocument();
  });

  it("VID-23: enrichment accept / dismiss / manual search", async () => {
    const candidate = {
      id: "c1",
      provider: "tmdb",
      external_id: "tmdb:42",
      title: "Dune",
      year: 2021,
      confidence: 0.873,
      accepted: false,
      fields: [
        {
          field: "title",
          current: "dune.mkv",
          proposed: "Dune",
          would_change: true,
          protected: false,
        },
        {
          field: "description",
          current: "mine",
          proposed: "theirs",
          would_change: false,
          protected: true,
        },
      ],
    };
    serve({ "/api/videos/v1/enrichment": { candidates: [candidate] } });
    let resolveAccept: (v: unknown) => void = () => {};
    post.mockImplementation((url: string) => {
      if (url.endsWith("/accept")) return new Promise((r) => (resolveAccept = r));
      if (url.endsWith("/dismiss")) return Promise.resolve(undefined);
      return Promise.resolve({
        candidates: [
          {
            id: "c7",
            provider: "tmdb",
            external_id: "tmdb:7",
            title: "Dune 1984",
            confidence: 0.5,
          },
        ],
      });
    });
    renderVideo("/videos/v1", { user: adminUser });
    await openTab("Metadata");
    const user = userEvent.setup();
    expect(await screen.findByText(/We found this might be Dune \(2021\)/)).toBeInTheDocument();
    expect(screen.getByText("87% match")).toBeInTheDocument();
    const diff = screen.getByRole("table");
    expect(within(diff).getByText("mine").closest("tr")).toHaveTextContent("Won't change");
    const before = gets("/enrichment").length;
    await user.click(screen.getByRole("button", { name: "Accept" }));
    expect(post).toHaveBeenCalledWith("/api/videos/v1/enrichment/accept", {
      external_id: "tmdb:42",
    });
    expect(screen.getByRole("button", { name: "Accept" })).toHaveAttribute("aria-busy", "true");
    expect(screen.getByRole("button", { name: "Dismiss" })).toBeDisabled();
    resolveAccept(undefined);
    expect(await screen.findByText("Metadata applied.")).toBeInTheDocument();
    await waitFor(() => expect(gets("/enrichment").length).toBe(before + 1));
    const panel = document.querySelector(".mkt-enrich-panel") as HTMLElement;
    await user.click(await within(panel).findByRole("button", { name: "Dismiss" }));
    expect(post).toHaveBeenCalledWith("/api/videos/v1/enrichment/dismiss", {
      external_id: "tmdb:42",
    });
    expect(await screen.findByText("Suggestion dismissed.")).toBeInTheDocument();
    const box = screen.getByRole("searchbox", { name: "Search manually" });
    const search = screen.getByRole("button", { name: "Search manually" });
    expect(search).toBeDisabled();
    await user.type(box, "   ");
    expect(search).toBeDisabled();
    await user.clear(box);
    await user.type(box, " Dune 1984 ");
    await user.click(search);
    expect(post).toHaveBeenLastCalledWith("/api/videos/v1/enrichment/search", {
      query: "Dune 1984",
    });
    expect(await screen.findByText(/We found this might be Dune 1984/)).toBeInTheDocument();
  });

  it("VID-23: accept failure shows an error toast", async () => {
    serve({
      "/api/videos/v1/enrichment": {
        candidates: [
          { id: "c1", provider: "tmdb", external_id: "tmdb:42", confidence: 0.5, accepted: false },
        ],
      },
    });
    post.mockRejectedValue(problem(500, "accept failed"));
    renderVideo("/videos/v1", { user: adminUser });
    await openTab("Metadata");
    await userEvent.setup().click(await screen.findByRole("button", { name: "Accept" }));
    expect(await screen.findByText("accept failed")).toBeInTheDocument();
  });

  it.todo("VID-23: pressing Enter in the manual search box runs the search");

  it("VID-24: DisplayTitle validation, trim, error, cancel and Escape", async () => {
    serve({ "/api/videos/v1": { ...V1, display_title: "Auto", display_title_source: "rules" } });
    patch
      .mockRejectedValueOnce(problem(422, "invalid", "title too long"))
      .mockResolvedValueOnce({ id: "v1", display_title: "New", display_title_source: "manual" });
    renderVideo("/videos/v1", { user: adminUser });
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "Change" }));
    const box = screen.getByRole("textbox", { name: "On-air title" });
    const save = screen.getByRole("button", { name: "Save" });
    await user.clear(box);
    expect(save).toBeDisabled();
    await user.type(box, "   ");
    expect(save).toBeDisabled();
    expect(box).toHaveAttribute("maxLength", "200");
    await user.clear(box);
    await user.type(box, " New ");
    await user.click(save);
    expect(await screen.findByRole("alert")).toHaveTextContent("title too long");
    expect(screen.getByRole("textbox", { name: "On-air title" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(patch).toHaveBeenLastCalledWith("/api/videos/v1", { display_title: "New" });
    await waitFor(() => expect(screen.queryByRole("textbox", { name: "On-air title" })).toBeNull());
    expect(screen.getByTestId("display-title")).toHaveTextContent("New");
    expect(screen.getByTestId("display-title")).toHaveTextContent("set by hand");
    await user.click(screen.getByRole("button", { name: "Change" }));
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    expect(screen.queryByRole("textbox", { name: "On-air title" })).toBeNull();
    await user.click(screen.getByRole("button", { name: "Change" }));
    fireEvent.keyDown(screen.getByRole("textbox", { name: "On-air title" }), { key: "Escape" });
    expect(screen.queryByRole("textbox", { name: "On-air title" })).toBeNull();
  });

  it("VID-24: an unknown display_title_source shows no raw key", async () => {
    serve({ "/api/videos/v1": { ...V1, display_title_source: "foo" } });
    renderVideo();
    await screen.findByTestId("display-title");
    expect(document.body.textContent).not.toContain("video.displayTitle.source");
  });

  it("VID-25: Arabic titles, description, transcript and tab labels", async () => {
    localStorage.setItem("mkt:locale", "ar");
    serve({
      "/api/videos/v1": {
        ...V1,
        title: "السيرة النبوية ( 18 )",
        description: "وصف قصير",
        detected_language: "ar",
      },
      "/api/videos/v1/segments": {
        items: [{ id: 1, seq: 1, start_sec: 1, end_sec: 2, text: "نص عربي" }],
      },
      "/api/videos/v1/chapters": {
        items: [{ id: 1, seq: 1, start_sec: 0, end_sec: 1, title: "فصل" }],
      },
    });
    renderVideo();
    const h1 = await screen.findByRole("heading", { level: 1, name: "السيرة النبوية ( 18 )" });
    expect(h1).toHaveAttribute("dir", "auto");
    await waitFor(() => expect(document.documentElement).toHaveAttribute("dir", "rtl"));
    expect(document.documentElement).toHaveAttribute("lang", "ar");
    expect(screen.getByText("وصف قصير")).toHaveAttribute("dir", "auto");
    expect(screen.queryByRole("tab", { name: "Transcript" })).toBeNull();
    const tabs = screen.getAllByRole("tab");
    await userEvent.setup().click(tabs[2]);
    expect((await screen.findByText("نص عربي")).closest("p")).toHaveAttribute("dir", "auto");
    await userEvent.setup().click(tabs[3]);
    expect(await screen.findByText("فصل")).toHaveAttribute("dir", "auto");
  });

  it.todo("VID-25: in RTL, ArrowLeft moves to the next tab (DS Tabs)");
});
