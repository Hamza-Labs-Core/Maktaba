// The per-video matches page (web/test-cases/search.md SRC-36..):
//   /search/video/:videoId?q=&mode=&sid=&vp=&tp=
//   GET /api/search/passages?sid=&video_id=&order=time&offset=&limit=[&include_times=1]
// with the player (POST /api/stream/sessions) marking every match.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Route, useLocation } from "react-router-dom";

const { post, get, del } = vi.hoisted(() => ({
  post: vi.fn(),
  get: vi.fn(),
  del: vi.fn(),
}));
vi.mock("../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../lib/api")>("../lib/api");
  return { ...actual, api: { ...actual.api, post, get, delete: del } };
});

import { SearchVideo } from "./SearchVideo";
import { memberUser, renderPage } from "../test/pageHarness";
import {
  type Corpus,
  type FakeServerOptions,
  fakeSearchServer,
  passage,
  titleVideo,
  transcriptVideo,
} from "../test/searchServer";

const SESSION = {
  session_id: "s1",
  mode: "direct",
  manifest_url: "",
  direct_url: "/api/stream/s1/file",
  expires_at: "2099-01-01T00:00:00Z",
};

const corpus: Corpus = {
  titles: [titleVideo("v2", "Only the title")],
  transcripts: [
    // best first, as the server keeps them; the page orders by time
    transcriptVideo("v1", "Long talk", [
      passage(3, 300, "late <mark>fox</mark>"),
      passage(1, 10, "early <mark>fox</mark>"),
      passage(2, 120, "middle <mark>fox</mark>"),
    ]),
  ],
};

function serve(opts: FakeServerOptions = {}) {
  return fakeSearchServer({ post, get, del }, corpus, {
    existing: { "sid-a": { q: "fox", mode: "hybrid", corpus } },
    passagesPageSize: 2,
    otherPost: (url) => {
      if (url === "/api/stream/sessions") return SESSION;
      return { tracking: false };
    },
    ...opts,
  });
}

function Loc() {
  const { pathname, search } = useLocation();
  return <span data-testid="loc">{pathname + search}</span>;
}
const locParams = () =>
  new URLSearchParams((screen.getByTestId("loc").textContent ?? "").split("?")[1] ?? "");

function renderMatches(path = "/search/video/v1?q=fox&mode=hybrid&sid=sid-a&vp=2&tp=3") {
  return renderPage(
    <>
      <SearchVideo />
      <Loc />
    </>,
    {
      path,
      route: "/search/video/:videoId",
      user: memberUser,
      extraRoutes: <Route path="/search" element={<p>results page</p>} />,
    }
  );
}

const matchButtons = () =>
  within(screen.getByTestId("matches-list")).queryAllByRole("button", { name: /fox/ });

async function video(): Promise<HTMLVideoElement> {
  return (await screen.findByTestId("mkt-video")) as HTMLVideoElement;
}

describe("SearchVideo (test-cases/search.md)", () => {
  let play: ReturnType<typeof vi.spyOn>;
  beforeEach(() => {
    post.mockReset();
    get.mockReset();
    del.mockReset();
    sessionStorage.clear();
    // the results page remembered the sizes of sid-a
    sessionStorage.setItem(
      "mkt:search:sizes",
      JSON.stringify({ "sid-a": { pageSize: 20, passagesPageSize: 2 } })
    );
    play = vi.spyOn(HTMLMediaElement.prototype, "play").mockResolvedValue(undefined);
    vi.spyOn(HTMLMediaElement.prototype, "pause").mockImplementation(() => {});
    vi.spyOn(HTMLMediaElement.prototype, "load").mockImplementation(() => {});
  });
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("SRC-36: lists every match by time with its time, type and snippet; count and terms; back link keeps the pages", async () => {
    serve();
    renderMatches();
    await waitFor(() => expect(matchButtons()).toHaveLength(2));
    expect(get).toHaveBeenCalledWith(
      "/api/search/passages?sid=sid-a&video_id=v1&order=time&offset=0&limit=2&include_times=1"
    );
    expect(matchButtons().map((b) => b.textContent)).toEqual([
      "[0:10]Transcriptearly fox",
      "[2:00]Transcriptmiddle fox",
    ]);
    expect(within(matchButtons()[0]!).getByText("fox").tagName).toBe("MARK");
    expect(screen.getByTestId("matches-count")).toHaveTextContent("3 matches");
    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("Long talk");
    const back = screen.getByRole("link", { name: /Back to results/ });
    expect(back).toHaveAttribute("href", "/search?q=fox&mode=hybrid&sid=sid-a&vp=2&tp=3");
    expect(screen.getByRole("link", { name: "fox" })).toHaveAttribute(
      "href",
      back.getAttribute("href")
    );
    expect(screen.getByText("Lectures")).toBeInTheDocument();

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: /Load more/ }));
    await waitFor(() => expect(matchButtons()).toHaveLength(3));
    expect(get).toHaveBeenCalledWith(
      "/api/search/passages?sid=sid-a&video_id=v1&order=time&offset=2&limit=2"
    );
    expect(matchButtons()[2]).toHaveTextContent("[5:00]");
    expect(screen.queryByRole("button", { name: /Load more/ })).toBeNull();
    expect(screen.queryByText("No matches in the transcript")).toBeNull();

    await user.click(back);
    expect(screen.getByText("results page")).toBeInTheDocument();
  });

  it("SRC-37: clicking a match seeks the player there and plays", async () => {
    serve();
    renderMatches();
    const el = await video();
    await waitFor(() => expect(matchButtons()).toHaveLength(2));
    await userEvent.setup().click(matchButtons()[1]!);
    expect(el.currentTime).toBe(120);
    expect(play).toHaveBeenCalled();
    expect(matchButtons()[1]).toHaveAttribute("aria-current", "true");
  });

  it("SRC-38: Next / Previous match jump from the playback time (all match times, not just the loaded page)", async () => {
    serve();
    renderMatches();
    const el = await video();
    await waitFor(() => expect(matchButtons()).toHaveLength(2));
    const user = userEvent.setup();
    const next = screen.getByRole("button", { name: "Next match" });
    const prev = screen.getByRole("button", { name: "Previous match" });
    expect(prev).toBeDisabled();
    await user.click(next);
    expect(el.currentTime).toBe(10);
    await user.click(next);
    expect(el.currentTime).toBe(120);
    // the third match is not loaded yet, but `times` has it
    await user.click(next);
    expect(el.currentTime).toBe(300);
    expect(next).toBeDisabled();
    await user.click(prev);
    expect(el.currentTime).toBe(120);
    // playback moving on is followed (timeupdate)
    el.currentTime = 200;
    fireEvent(el, new Event("timeupdate"));
    await user.click(prev);
    expect(el.currentTime).toBe(120);
    expect(play).toHaveBeenCalledTimes(5);
  });

  it("SRC-39: every match is marked on the progress bar", async () => {
    serve();
    renderMatches();
    const el = await video();
    await waitFor(() => expect(matchButtons()).toHaveLength(2));
    Object.defineProperty(el, "duration", { configurable: true, value: 600 });
    fireEvent(el, new Event("loadedmetadata"));
    const marks = await screen.findByTestId("mkt-player-marks");
    const spans = Array.from(marks.querySelectorAll<HTMLElement>(".mkt-player__mark"));
    expect(spans.map((s) => s.style.insetInlineStart)).toEqual([
      `${(10 / 600) * 100}%`,
      "20%",
      "50%",
    ]);
  });

  it("SRC-40: a title-only match shows the highlighted title and no transcript matches", async () => {
    serve();
    renderMatches("/search/video/v2?q=fox&mode=hybrid&sid=sid-a");
    expect(await screen.findByText("No matches in the transcript")).toBeInTheDocument();
    const list = screen.getByTestId("matches-list");
    const row = within(list).getByText("Title").closest("li")!;
    expect(row.querySelector("mark")).toHaveTextContent("Only the title");
    expect(screen.getByTestId("matches-count")).toHaveTextContent("0 matches");
    expect(screen.getByRole("button", { name: "Next match" })).toBeDisabled();
  });

  it("SRC-41: an expired sid runs the search again, keeps the video, replaces the sid and says so", async () => {
    const state = serve({ pageSize: 20 });
    state.expired.add("sid-a");
    renderMatches("/search/video/v1?q=fox&mode=fts&sid=sid-a&vp=4&tp=1&t=30");
    await waitFor(() => expect(locParams().get("sid")).toBe("sid-1"));
    expect(post).toHaveBeenCalledWith("/api/search", { q: "fox", mode: "fts", view: "list" });
    // the results pages are clamped to the new totals (1 title)
    expect(locParams().get("vp")).toBeNull();
    expect(locParams().get("t")).toBe("30");
    expect(screen.getByTestId("loc")).toHaveTextContent(/^\/search\/video\/v1\?/);
    await waitFor(() => expect(matchButtons()).toHaveLength(2));
    expect(get).toHaveBeenCalledWith(
      "/api/search/passages?sid=sid-1&video_id=v1&order=time&offset=0&limit=2&include_times=1"
    );
    expect(screen.getByRole("status")).toHaveTextContent(
      "These results were refreshed; the order may have changed."
    );
    expect(screen.getByRole("link", { name: /Back to results/ })).toHaveAttribute(
      "href",
      "/search?q=fox&mode=fts&sid=sid-1"
    );
  });

  it("SRC-41: a link without a sid runs the search (no notice)", async () => {
    serve();
    renderMatches("/search/video/v1?q=fox&mode=hybrid");
    await waitFor(() => expect(locParams().get("sid")).toBe("sid-1"));
    await waitFor(() => expect(matchButtons()).toHaveLength(2));
    expect(screen.queryByRole("status")).toBeNull();
  });

  it("SRC-42: a video that is not in the results says so", async () => {
    serve();
    renderMatches("/search/video/zz?q=fox&mode=hybrid&sid=sid-a");
    expect(
      await screen.findByText("This video is not in these search results.")
    ).toBeInTheDocument();
  });

  it("SRC-43: Arabic: strings and the back arrow flip", async () => {
    localStorage.setItem("mkt:locale", "ar");
    serve();
    renderMatches();
    await waitFor(() => expect(matchButtons()).toHaveLength(2));
    expect(screen.getByRole("link", { name: /العودة إلى النتائج/ })).toHaveTextContent("→");
    expect(screen.getByRole("button", { name: "المطابقة التالية" })).toBeInTheDocument();
    expect(screen.getByTestId("matches-count")).toHaveTextContent("عدد المطابقات: ٣");
  });
});
