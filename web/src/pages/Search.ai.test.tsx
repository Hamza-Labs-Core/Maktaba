// Story 49.14 — the Search page's AI mode tab (web/test-cases/search.md SRC-60 to SRC-69):
// the tabs in the URL, Results not waiting on AI mode, the sources shown first and the
// sentences appended as they come, a citation's link to the player at its moment, a
// follow-up sending the earlier turn, each refusal's message, the request stopped on leaving
// the tab or the page, Arabic, and axe in both languages.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { Route, useLocation, useNavigate, type NavigateFunction } from "react-router-dom";

const { post, get, del } = vi.hoisted(() => ({ post: vi.fn(), get: vi.fn(), del: vi.fn() }));
vi.mock("../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../lib/api")>("../lib/api");
  return { ...actual, api: { ...actual.api, post, get, delete: del } };
});

import { Search } from "./Search";
import { ToastProvider } from "@ds/components/Toast/Toast";
import { adminUser, memberUser, renderPage } from "../test/pageHarness";
import { axeViolations } from "../test/axe";
import { forgetAnswers } from "../lib/pageCache";
import { fakeSearchServer, passage, titleVideo, transcriptVideo } from "../test/searchServer";
import en from "../i18n/en.json";
import ar from "../i18n/ar.json";

const E = en as Record<string, string>;
const A = ar as Record<string, string>;

let go: NavigateFunction;
function Loc() {
  const { pathname, search, hash } = useLocation();
  go = useNavigate();
  return <span data-testid="loc">{pathname + search + hash}</span>;
}
const loc = () => screen.getByTestId("loc").textContent ?? "";
const params = () => new URLSearchParams(loc().split("?")[1]?.split("#")[0] ?? "");

function renderSearch(path: string, user = memberUser, history: string[] = []) {
  return renderPage(
    <ToastProvider>
      <Search />
      <Loc />
    </ToastProvider>,
    {
      path,
      route: "/search",
      user,
      history,
      extraRoutes: (
        <>
          <Route path="/videos/:videoId/watch" element={<p>player page</p>} />
          <Route path="/settings/rules" element={<p>rules page</p>} />
          <Route path="/help/search" element={<p>help page</p>} />
        </>
      ),
    }
  );
}

const SOURCES = {
  q: "What is patience?",
  passages: [
    {
      n: 1,
      video_id: "v1",
      title: "On patience",
      library_name: "Lessons",
      start_sec: 61.5,
      end_sec: 70,
      text: "Patience is holding oneself back from despair.",
    },
    {
      n: 2,
      video_id: "v2",
      title: "Its kinds",
      library_name: "Lessons",
      start_sec: 125,
      text: "It is of three kinds.",
    },
  ],
};
const S1 = {
  text: "Patience is holding oneself back from despair.",
  citations: [{ n: 1, video_id: "v1", start_sec: 61.5, end_sec: 70, quote: "holding" }],
};
const S2 = {
  text: "It has three kinds.",
  citations: [
    { n: 2, video_id: "v2", start_sec: 125, quote: "three kinds" },
    { n: 1, video_id: "v1", start_sec: 61.5 },
  ],
};
const ev = (name: string, data: unknown) => `event: ${name}\ndata: ${JSON.stringify(data)}\n\n`;

/** One AI answer the test writes piece by piece; `signal` is the request's. */
interface Stream {
  body: Record<string, unknown>;
  signal: AbortSignal;
  write(s: string): void;
  close(): void;
}

let streams: Stream[] = [];
let refuse: ((body: Record<string, unknown>) => Response | null) | null = null;

function fakeFetch() {
  const enc = new TextEncoder();
  return vi.fn((url: string, init: RequestInit) => {
    if (url !== "/api/search/ai") return Promise.reject(new Error(`unmocked fetch ${url}`));
    const body = JSON.parse(String(init.body)) as Record<string, unknown>;
    const refused = refuse?.(body);
    if (refused) return Promise.resolve(refused);
    let ctrl!: ReadableStreamDefaultController<Uint8Array>;
    const stream = new ReadableStream<Uint8Array>({
      start(c) {
        ctrl = c;
      },
    });
    const s: Stream = {
      body,
      signal: init.signal!,
      write: (x) => ctrl.enqueue(enc.encode(x)),
      close: () => ctrl.close(),
    };
    init.signal?.addEventListener("abort", () => {
      try {
        ctrl.error(new DOMException("aborted", "AbortError"));
      } catch {
        /* closed */
      }
    });
    streams.push(s);
    return Promise.resolve(
      new Response(stream, { status: 200, headers: { "Content-Type": "text/event-stream" } })
    );
  });
}

const problem = (status: number, body: Record<string, unknown>, headers = {}) =>
  new Response(JSON.stringify({ status, title: "no", ...body }), {
    status,
    headers: { "Content-Type": "application/problem+json", ...headers },
  });

const corpus = {
  titles: [titleVideo("t1", "Patience in hardship")],
  transcripts: [transcriptVideo("r1", "Friday talk", [passage(1, 30, "on <mark>patience</mark>")])],
};
const searchCalls = () => post.mock.calls.filter((c) => c[0] === "/api/search");
const tab = (name: string) => screen.getByRole("tab", { name });
const aiBox = () => document.querySelector<HTMLInputElement>("#mkt-ai-q")!;
const turns = () => screen.queryAllByTestId("ai-turn");
const write = async (s: string, i = streams.length - 1) => {
  await act(async () => {
    streams[i]!.write(s);
    await new Promise((r) => setTimeout(r, 0));
  });
};

beforeEach(() => {
  post.mockReset();
  get.mockReset();
  del.mockReset();
  sessionStorage.clear();
  localStorage.clear();
  forgetAnswers("libraries");
  fakeSearchServer({ post, get, del }, corpus);
  streams = [];
  refuse = null;
  vi.stubGlobal("fetch", fakeFetch());
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("AI mode tab (story 49.14)", () => {
  /** A home where "sabr" is a recent search, its results kept. */
  const withRecentSabr = () =>
    fakeSearchServer({ post, get, del }, corpus, {
      recent: [
        {
          search_id: "sid-rec",
          q: "sabr",
          mode: "hybrid",
          created_at: "2026-11-24T00:00:00Z",
          title_count: 1,
          transcript_count: 0,
          live: true,
        },
      ],
      existing: {
        "sid-rec": {
          q: "sabr",
          mode: "hybrid",
          corpus: { titles: [titleVideo("s1", "A lesson on sabr")], transcripts: [] },
        },
      },
    });
  const recentSabr = () => screen.findByRole("button", { name: /^sabr/ });
  /** Holds each POST /api/search from here until released. */
  const holdSearch = () => {
    const answer = post.getMockImplementation()!;
    let release!: () => void;
    const held = new Promise<void>((done) => (release = done));
    post.mockImplementation(async (url: string, body: unknown) => {
      if (url === "/api/search") await held;
      return answer(url, body) as unknown;
    });
    return () =>
      act(async () => {
        release();
        await new Promise((r) => setTimeout(r, 20));
      });
  };
  const asked = () => searchCalls().map((c) => (c[1] as { q: string }).q);

  it("SRC-60: Results is the default; AI mode rides in the URL and asks the words in the box once", async () => {
    renderSearch("/search?q=patience&mode=hybrid");
    expect(tab(E["search.tab.results"]!)).toHaveAttribute("aria-selected", "true");
    await screen.findByTestId("search-videos");
    // the search answered (its video listed); SRC-60b and SRC-60c open AI mode before that
    await screen.findByText("Patience in hardship", {}, { timeout: 3000 });
    expect(streams).toHaveLength(0);

    fireEvent.click(tab(E["search.tab.ai"]!));
    await waitFor(() => expect(params().get("tab")).toBe("ai"));
    expect(params().get("q")).toBe("patience");
    expect(tab(E["search.tab.ai"]!)).toHaveAttribute("aria-selected", "true");
    await waitFor(() => expect(streams).toHaveLength(1));
    expect(streams[0]!.body).toEqual({ q: "patience" });

    // back to Results: the same results at once, no new search; AI mode's request stopped
    const before = searchCalls().length;
    fireEvent.click(tab(E["search.tab.results"]!));
    await waitFor(() => expect(params().get("tab")).toBeNull());
    expect(params().get("q")).toBe("patience");
    expect(streams[0]!.signal.aborted).toBe(true);
    expect(await screen.findByTestId("search-videos")).toBeInTheDocument();
    expect(searchCalls()).toHaveLength(before);
    // and to AI mode again: the conversation is kept (stopped), not asked again
    fireEvent.click(tab(E["search.tab.ai"]!));
    expect(await screen.findByText(E["search.ai.stopped"]!)).toBeInTheDocument();
    expect(streams).toHaveLength(1);
  });

  it("SRC-60b: AI mode opened as the results appear, before the URL shows their sid: the search is kept", async () => {
    renderSearch("/search?q=patience&mode=hybrid");
    // the results are on screen before the router, which renders the URL in a transition, has
    // their sid: the tab is clicked at that very moment
    const opened = new Promise<void>((done) => {
      const seen = new MutationObserver(() => {
        if (!screen.queryByText("Patience in hardship")) return;
        seen.disconnect();
        expect(params().get("sid")).toBeNull();
        fireEvent.click(tab(E["search.tab.ai"]!));
        done();
      });
      seen.observe(document.body, { childList: true, subtree: true });
    });
    await opened;
    await waitFor(() => expect(params().get("tab")).toBe("ai"));
    expect(params().get("sid")).not.toBeNull();
    fireEvent.click(tab(E["search.tab.results"]!));
    await waitFor(() => expect(params().get("tab")).toBeNull());
    expect(await screen.findByText("Patience in hardship")).toBeInTheDocument();
    expect(searchCalls()).toHaveLength(1);
  });

  it("SRC-60c: a search that answers after AI mode was opened leaves the person there, and is kept", async () => {
    const release = holdSearch();
    renderSearch("/search?q=patience&mode=hybrid");
    await waitFor(() => expect(searchCalls()).toHaveLength(1));
    fireEvent.click(tab(E["search.tab.ai"]!));
    await waitFor(() => expect(params().get("tab")).toBe("ai"));
    await release();
    expect(params().get("tab")).toBe("ai");
    expect(params().get("sid")).not.toBeNull();
    fireEvent.click(tab(E["search.tab.results"]!));
    expect(await screen.findByText("Patience in hardship")).toBeInTheDocument();
    expect(searchCalls()).toHaveLength(1);
  });

  describe.each([
    ["en", E],
    ["ar", A],
  ])("SRC-60d: Results shows the search the URL names (%s)", (lang, L) => {
    beforeEach(() => localStorage.setItem("mkt:locale", lang));
    const results = () => tab(L["search.tab.results"]!);
    const ai = () => tab(L["search.tab.ai"]!);

    it("a recent search opened from AI mode", async () => {
      withRecentSabr();
      renderSearch("/search?q=patience&mode=hybrid");
      await screen.findByText("Patience in hardship");
      fireEvent.click(ai());
      await waitFor(() => expect(params().get("tab")).toBe("ai"));
      fireEvent.click(await recentSabr());
      await waitFor(() => expect(params().get("sid")).toBe("sid-rec"));
      fireEvent.click(results());
      expect(await screen.findByText("A lesson on sabr")).toBeInTheDocument();
      expect(params().get("q")).toBe("sabr");
      expect(params().get("sid")).toBe("sid-rec");
      expect(document.documentElement.dir || "ltr").toBe(lang === "ar" ? "rtl" : "ltr");
    });

    it("Back to another entry in AI mode", async () => {
      renderSearch("/search?q=sabr&tab=ai");
      await waitFor(() => expect(streams).toHaveLength(1));
      act(() => void go("/search?q=patience"));
      await screen.findByText("Patience in hardship");
      await waitFor(() => expect(params().get("sid")).not.toBeNull());
      act(() => void go(-1));
      await waitFor(() => expect(params().get("tab")).toBe("ai"));
      fireEvent.click(results());
      await waitFor(() => expect(asked()).toEqual(["patience", "sabr"]));
      await waitFor(() => expect(params().get("sid")).not.toBeNull());
      expect(params().get("q")).toBe("sabr");
    });

    it("AI mode and then Back before it renders: the next search opens in Results", async () => {
      withRecentSabr();
      renderSearch("/search?q=patience&mode=hybrid", memberUser, ["/search?q=earlier"]);
      await screen.findByText("Patience in hardship");
      await waitFor(() => expect(params().get("sid")).not.toBeNull());
      act(() => {
        ai().click();
        void go(-1);
      });
      await waitFor(() => expect(params().get("q")).toBe("earlier"));
      fireEvent.click(await recentSabr());
      await waitFor(() => expect(params().get("sid")).toBe("sid-rec"));
      expect(params().get("tab")).toBeNull();
    });

    it("a recent search and then Back before it renders: Results shows the search it was", async () => {
      withRecentSabr();
      renderSearch("/search?q=patience&mode=hybrid");
      await screen.findByText("Patience in hardship");
      fireEvent.click(ai());
      await waitFor(() => expect(params().get("tab")).toBe("ai"));
      const recent = await recentSabr();
      act(() => {
        recent.click();
        void go(-1);
      });
      fireEvent.click(results());
      await waitFor(() => expect(params().get("tab")).toBeNull());
      expect(params().get("q")).toBe("patience");
      expect(asked()).toEqual(["patience"]);
    });

    it("AI mode and then Back, before it renders, to an entry of the same URL", async () => {
      withRecentSabr();
      const entry = "/search?q=sabr&mode=hybrid&sid=sid-rec";
      renderSearch(entry, memberUser, [entry]);
      await screen.findByText("A lesson on sabr");
      act(() => {
        ai().click();
        void go(-1);
      });
      fireEvent.click(await recentSabr());
      await act(async () => {
        await new Promise((r) => setTimeout(r, 20));
      });
      expect(params().get("tab")).toBeNull();
      expect(streams).toHaveLength(0);
    });

    it("a search that answers after the same words were opened in another order", async () => {
      const release = holdSearch();
      renderSearch("/search?q=patience&mode=hybrid");
      await waitFor(() => expect(asked()).toEqual(["patience"]));
      fireEvent.click(ai());
      await waitFor(() => expect(params().get("tab")).toBe("ai"));
      act(() => void go("/search?q=patience&mode=hybrid&sort=newest&tab=ai"));
      await waitFor(() => expect(params().get("sort")).toBe("newest"));
      await release();
      expect(params().get("sort")).toBe("newest");
      expect(params().get("sid")).toBeNull();
    });

    it("new words, then AI mode before they render: it asks the new words", async () => {
      renderSearch("/search?q=patience&mode=hybrid");
      await screen.findByText("Patience in hardship");
      await waitFor(() => expect(params().get("sid")).not.toBeNull());
      const form = document.querySelector<HTMLFormElement>(".mkt-search__form")!;
      fireEvent.change(form.querySelector("input")!, { target: { value: "sabr" } });
      act(() => {
        form.requestSubmit();
        ai().click();
      });
      await waitFor(() => expect(streams).toHaveLength(1));
      expect(streams[0]!.body).toEqual({ q: "sabr" });
      expect(params().get("q")).toBe("sabr");
    });

    it("a search that answers after the URL moved to another leaves that one be", async () => {
      const release = holdSearch();
      renderSearch("/search?q=patience&mode=hybrid");
      await waitFor(() => expect(asked()).toEqual(["patience"]));
      fireEvent.click(ai());
      await waitFor(() => expect(params().get("tab")).toBe("ai"));
      act(() => void go("/search?q=sabr&tab=ai"));
      await waitFor(() => expect(params().get("q")).toBe("sabr"));
      await release();
      expect(params().get("q")).toBe("sabr");
      expect(params().get("tab")).toBe("ai");
      expect(params().get("sid")).toBeNull();
      fireEvent.click(results());
      await waitFor(() => expect(asked()).toEqual(["patience", "sabr"]));
    });
  });

  it("SRC-61: a link to the AI tab does not run the Results search until Results is opened", async () => {
    renderSearch("/search?q=patience&tab=ai");
    await waitFor(() => expect(streams).toHaveLength(1));
    expect(searchCalls()).toHaveLength(0);
    fireEvent.click(tab(E["search.tab.results"]!));
    await screen.findByTestId("search-videos");
    expect(searchCalls()).toHaveLength(1);
  });

  it("SRC-62: the place in line, then the sources at once, then each sentence appended; citations open the player", async () => {
    renderSearch("/search?tab=ai");
    expect(screen.getByText(E["search.ai.intro"]!)).toBeInTheDocument();
    fireEvent.change(aiBox(), { target: { value: "What is patience?" } });
    fireEvent.submit(aiBox().closest("form")!);
    await waitFor(() => expect(streams).toHaveLength(1));
    expect(streams[0]!.body).toEqual({ q: "What is patience?" });
    const turn = turns()[0]!;
    expect(within(turn).getByRole("heading", { level: 2 })).toHaveTextContent("What is patience?");
    expect(within(turn).getByRole("status")).toHaveTextContent(E["search.ai.asking"]!);

    await write(": ping\n\n" + ev("queue", { position: 2 }));
    expect(within(turn).getByRole("status")).toHaveTextContent(
      "Another answer is being written. Your question is number 2 in line."
    );
    // a piece that ends inside an event
    const src = ev("sources", SOURCES);
    await write(src.slice(0, 40));
    expect(within(turn).queryByTestId("ai-sources")).toBeNull();
    await write(src.slice(40));
    const sources = within(turn).getByTestId("ai-sources");
    expect(within(sources).getAllByRole("listitem")).toHaveLength(2);
    expect(within(turn).getByRole("status")).toHaveTextContent(E["search.ai.writing"]!);
    expect(within(turn).queryByTestId("ai-answer")).toBeNull();

    await write(ev("text", S1));
    expect(within(turn).getAllByTestId("ai-sentence")).toHaveLength(1);
    await write(ev("text", S2));
    const sentences = within(turn).getAllByTestId("ai-sentence");
    expect(sentences.map((s) => s.textContent?.trim())).toEqual([
      `${S1.text}[1]`,
      `${S2.text}[2][1]`,
    ]);
    const cite = within(sentences[1]!).getByRole("link", { name: "Source 2: Its kinds at 2:05" });
    expect(cite).toHaveAttribute("href", "/videos/v2/watch?t=125");
    expect(
      within(sentences[0]!).getByRole("link", { name: "Source 1: On patience at 1:01" })
    ).toHaveAttribute("href", "/videos/v1/watch?t=61");
    await write(ev("done", { answered: true, sentences: 2, dropped: 0 }));
    expect(turn).toHaveAttribute("data-status", "done");
    expect(within(turn).getByRole("status")).toBeEmptyDOMElement();

    fireEvent.click(cite);
    expect(await screen.findByText("player page")).toBeInTheDocument();
  });

  it("SRC-63: a follow-up sends the earlier question and answer as history; asking again stops the live answer", async () => {
    renderSearch("/search?q=What%20is%20patience%3F&tab=ai");
    await waitFor(() => expect(streams).toHaveLength(1));
    await write(
      ev("sources", SOURCES) + ev("text", S1) + ev("text", S2) + ev("done", { answered: true })
    );
    expect(screen.getByText(E["search.ai.followUp"]!)).toBeInTheDocument();
    fireEvent.change(aiBox(), { target: { value: "And its kinds?" } });
    fireEvent.submit(aiBox().closest("form")!);
    await waitFor(() => expect(streams).toHaveLength(2));
    expect(streams[1]!.body).toEqual({
      q: "And its kinds?",
      history: [{ q: "What is patience?", answer: `${S1.text} ${S2.text}` }],
    });
    await write(ev("sources", SOURCES));
    // asked again while it writes: the live answer is stopped, its sources stay
    fireEvent.change(aiBox(), { target: { value: "Name them" } });
    fireEvent.submit(aiBox().closest("form")!);
    await waitFor(() => expect(streams).toHaveLength(3));
    expect(streams[1]!.signal.aborted).toBe(true);
    expect(turns()[1]).toHaveAttribute("data-status", "stopped");
    expect(within(turns()[1]!).getByTestId("ai-sources")).toBeInTheDocument();
    expect(turns()).toHaveLength(3);
    // New conversation forgets them all
    fireEvent.click(screen.getByRole("button", { name: E["search.ai.new"] }));
    expect(turns()).toHaveLength(0);
    expect(streams[2]!.signal.aborted).toBe(true);
  });

  it("SRC-64: passages that do not answer stay shown with a note; no passages says so", async () => {
    renderSearch("/search?q=who&tab=ai");
    await waitFor(() => expect(streams).toHaveLength(1));
    await write(
      ev("sources", SOURCES) + ev("done", { answered: false, reason: "not-in-passages" })
    );
    const turn = turns()[0]!;
    expect(within(turn).getByRole("status")).toHaveTextContent(E["search.ai.notAnswered"]!);
    expect(within(within(turn).getByTestId("ai-sources")).getAllByRole("listitem")).toHaveLength(2);
    // a source opens the player at its passage
    expect(
      within(turn).getByRole("link", { name: "Source 1: On patience at 1:01" })
    ).toHaveAttribute("href", "/videos/v1/watch?t=61");

    fireEvent.change(aiBox(), { target: { value: "zzz" } });
    fireEvent.submit(aiBox().closest("form")!);
    await waitFor(() => expect(streams).toHaveLength(2));
    await write(
      ev("sources", { q: "zzz", passages: [] }) +
        ev("done", { answered: false, reason: "no-passages" })
    );
    expect(within(turns()[1]!).getByRole("status")).toHaveTextContent(E["search.ai.noPassages"]!);

    // a timeout before any sentence is a timeout, not passages that do not answer
    fireEvent.change(aiBox(), { target: { value: "slow" } });
    fireEvent.submit(aiBox().closest("form")!);
    await waitFor(() => expect(streams).toHaveLength(3));
    await write(ev("sources", SOURCES) + ev("done", { answered: false, reason: "timeout" }));
    expect(within(turns()[2]!).getByRole("status")).toHaveTextContent(E["search.ai.timeout"]!);
  });

  it("SRC-65: an error keeps what was shown and says the answer stopped; so does a cut stream", async () => {
    renderSearch("/search?q=q1&tab=ai");
    await waitFor(() => expect(streams).toHaveLength(1));
    await write(ev("sources", SOURCES) + ev("text", S1) + ev("error", { code: "model-failed" }));
    let turn = turns()[0]!;
    expect(turn).toHaveAttribute("data-status", "failed");
    expect(within(turn).getByRole("status")).toHaveTextContent(E["search.ai.failed"]!);
    expect(within(turn).getAllByTestId("ai-sentence")).toHaveLength(1);
    expect(within(turn).getByTestId("ai-sources")).toBeInTheDocument();

    fireEvent.change(aiBox(), { target: { value: "q2" } });
    fireEvent.submit(aiBox().closest("form")!);
    await waitFor(() => expect(streams).toHaveLength(2));
    await write(ev("sources", SOURCES));
    await act(async () => {
      streams[1]!.close();
      await new Promise((r) => setTimeout(r, 0));
    });
    turn = turns()[1]!;
    await waitFor(() => expect(turn).toHaveAttribute("data-status", "failed"));
    expect(within(turn).getByRole("status")).toHaveTextContent(E["search.ai.failed"]!);
  });

  it("SRC-66: no language model: how to enable it, a settings link for an admin and the help page", async () => {
    refuse = () =>
      problem(409, {
        type: "https://maktaba.tv/problems/ai-unavailable",
        code: "ai-unavailable",
        help: { settings: "/settings/rules?section=tagcheck&q=url", help: "/help/search#ai-mode" },
      });
    renderSearch("/search?q=patience&tab=ai", adminUser);
    const note = await screen.findByTestId("ai-unavailable");
    expect(note).toHaveTextContent(E["search.ai.unavailable"]!);
    expect(note.closest(".mkt-ai__note")).not.toHaveAttribute("role", "alert");
    expect(
      within(note).getByRole("link", { name: E["search.ai.unavailable.settings"] })
    ).toHaveAttribute("href", "/settings/rules?section=tagcheck&q=url");
    const help = within(note).getByRole("link", { name: E["search.ai.unavailable.help"] });
    expect(help).toHaveAttribute("href", "/help/search#ai-mode");
    fireEvent.click(help);
    expect(await screen.findByText("help page")).toBeInTheDocument();
  });

  it("SRC-66b: a member is told to ask an admin, with the help page", async () => {
    refuse = () => problem(409, { code: "ai-unavailable", help: { settings: "/settings/rules" } });
    renderSearch("/search?q=patience&tab=ai");
    const note = await screen.findByTestId("ai-unavailable");
    expect(note).toHaveTextContent(E["search.ai.unavailable.member"]!);
    expect(
      within(note).queryByRole("link", { name: E["search.ai.unavailable.settings"] })
    ).toBeNull();
    // the help path when the answer names none
    expect(
      within(note).getByRole("link", { name: E["search.ai.unavailable.help"] })
    ).toHaveAttribute("href", "/help/search#ai-mode");
  });

  it("SRC-67: AI mode turned off, and a full line with when to ask again", async () => {
    refuse = () => problem(409, { code: "ai-off" });
    renderSearch("/search?q=patience&tab=ai");
    expect(await screen.findByTestId("ai-off")).toHaveTextContent(E["search.ai.off"]!);

    refuse = () => problem(429, { code: "ai-busy", position: 9 }, { "Retry-After": "45" });
    fireEvent.change(aiBox(), { target: { value: "again" } });
    fireEvent.submit(aiBox().closest("form")!);
    const busy = await screen.findByTestId("ai-busy");
    expect(busy).toHaveTextContent(
      "AI mode is busy: other questions are waiting. Ask again in 45 seconds."
    );
    refuse = null;
    fireEvent.click(within(busy).getByRole("button", { name: E["search.ai.again"] }));
    await waitFor(() => expect(streams).toHaveLength(1));
    expect(streams[0]!.body).toEqual({ q: "again" });
  });

  it("SRC-68: leaving the page stops the answer", async () => {
    const { unmount } = renderSearch("/search?q=patience&tab=ai");
    await waitFor(() => expect(streams).toHaveLength(1));
    unmount();
    await waitFor(() => expect(streams[0]!.signal.aborted).toBe(true));
  });

  it("SRC-69: reads in Arabic, right to left", async () => {
    localStorage.setItem("mkt:locale", "ar");
    renderSearch("/search?q=ما الصبر&tab=ai");
    expect(screen.getByRole("tab", { name: A["search.tab.ai"] })).toHaveAttribute(
      "aria-selected",
      "true"
    );
    expect(screen.getByRole("tablist", { name: A["search.tabs"] })).toBeInTheDocument();
    await waitFor(() => expect(streams).toHaveLength(1));
    await write(ev("queue", { position: 3 }));
    expect(turns()[0]).toHaveTextContent("تُكتب إجابة أخرى الآن. سؤالك رقم 3 في الصف.");
    await write(ev("sources", SOURCES) + ev("text", S1) + ev("done", { answered: true }));
    expect(screen.getByRole("heading", { name: A["search.ai.sources"] })).toBeInTheDocument();
    expect(
      screen.getAllByRole("link", { name: "المصدر 1: On patience عند 1:01" }).length
    ).toBeGreaterThan(0);
    expect(screen.getByTestId("ai-answer")).toHaveAttribute("dir", "auto");
    expect(document.documentElement.dir).toBe("rtl");
  });

  it.each(["en", "ar"])(
    "SRC-69 (%s): axe finds nothing with an answer, its sources and a refusal shown",
    async (locale) => {
      localStorage.setItem("mkt:locale", locale);
      renderSearch("/search?q=patience&tab=ai", adminUser);
      await waitFor(() => expect(streams).toHaveLength(1));
      await write(
        ev("sources", SOURCES) + ev("text", S1) + ev("text", S2) + ev("done", { answered: true })
      );
      refuse = () =>
        problem(409, { code: "ai-unavailable", help: { settings: "/settings/rules" } });
      fireEvent.change(aiBox(), { target: { value: "more" } });
      fireEvent.submit(aiBox().closest("form")!);
      await screen.findByTestId("ai-unavailable");
      expect(await axeViolations(document.body)).toEqual([]);
    },
    30_000
  );
});
