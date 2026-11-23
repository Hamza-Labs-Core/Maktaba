// Story 49.14 — AI mode's client: the stream cut wherever its pieces split, each event read,
// the refusals (409 ai-unavailable with its help, 409 ai-off, 429 ai-busy with Retry-After),
// a cut stream, cancelling, and the conversation (history, one live turn).
import { describe, it, expect, vi, afterEach } from "vitest";
import { act, renderHook, waitFor } from "@testing-library/react";
import {
  type AIEvent,
  type AITurnState,
  SSEReader,
  aiEventOf,
  aiRefusalOf,
  applyAIEvent,
  askAI,
  historyOf,
  newTurn,
  useAIConversation,
  useAITab,
} from "./searchAI";
import { StrictMode, createElement, type ReactNode } from "react";

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
      text: "Patience is holding oneself back.",
    },
    { n: 2, video_id: "v2", title: "Kinds", start_sec: 5, text: "Three kinds." },
  ],
};
const TEXT1 = {
  text: "Patience is holding oneself back.",
  citations: [{ n: 1, video_id: "v1", start_sec: 61.5, end_sec: 70, quote: "holding" }],
};
const TEXT2 = { text: "It has three kinds.", citations: [{ n: 2, video_id: "v2", start_sec: 5 }] };
const DONE = { answered: true, sentences: 2, dropped: 0, took_ms: 900 };

const ev = (name: string, data: unknown) => `event: ${name}\ndata: ${JSON.stringify(data)}\n\n`;
const STREAM =
  ": ping\n\n" +
  ev("queue", { position: 2 }) +
  ev("queue", { position: 1 }) +
  ev("sources", SOURCES) +
  ev("text", TEXT1) +
  ev("text", TEXT2) +
  ev("done", DONE);

/** A Response whose body arrives in `pieces`. */
function streamed(pieces: string[], init: ResponseInit = {}) {
  const enc = new TextEncoder();
  const body = new ReadableStream<Uint8Array>({
    start(c) {
      for (const p of pieces) c.enqueue(enc.encode(p));
      c.close();
    },
  });
  return new Response(body, {
    status: 200,
    headers: { "Content-Type": "text/event-stream" },
    ...init,
  });
}

function problem(
  status: number,
  body: Record<string, unknown>,
  headers: Record<string, string> = {}
) {
  return new Response(JSON.stringify({ status, title: "no", ...body }), {
    status,
    headers: { "Content-Type": "application/problem+json", ...headers },
  });
}

async function collect(res: Response | (() => Response), signal?: AbortSignal) {
  const fetchMock = vi.fn(() => Promise.resolve(typeof res === "function" ? res() : res));
  vi.stubGlobal("fetch", fetchMock);
  const events: AIEvent[] = [];
  await askAI({ q: "What is patience?" }, (e) => events.push(e), signal);
  return { events, fetchMock };
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("SSEReader", () => {
  it("reads the same events wherever the pieces split, CRLF and CR too", () => {
    const whole = (r: SSEReader, pieces: string[]) => [
      ...pieces.flatMap((p) => r.push(p)),
      ...r.end(),
    ];
    const expected = whole(new SSEReader(), [STREAM]);
    expect(expected.map((m) => m.event)).toEqual([
      "queue",
      "queue",
      "sources",
      "text",
      "text",
      "done",
    ]);
    // every split point, and one character at a time
    for (let i = 1; i < STREAM.length; i++)
      expect(whole(new SSEReader(), [STREAM.slice(0, i), STREAM.slice(i)])).toEqual(expected);
    expect(whole(new SSEReader(), STREAM.split(""))).toEqual(expected);
    const crlf = STREAM.replace(/\n/g, "\r\n");
    for (let i = 1; i < crlf.length; i++)
      expect(whole(new SSEReader(), [crlf.slice(0, i), crlf.slice(i)])).toEqual(expected);
    expect(whole(new SSEReader(), [STREAM.replace(/\n/g, "\r")])).toEqual(expected);
  });

  it("joins data lines, skips comments, and ends an event the stream left open", () => {
    const r = new SSEReader();
    expect(r.push(": hello\ndata: a\ndata:b\n\nevent: x\ndata: {}")).toEqual([
      { event: "message", data: "a\nb" },
    ]);
    expect(r.end()).toEqual([{ event: "x", data: "{}" }]);
  });
});

describe("aiEventOf", () => {
  it("reads each event and leaves out what it cannot use", () => {
    expect(aiEventOf({ event: "queue", data: '{"position":3}' })).toEqual({
      type: "queue",
      position: 3,
    });
    const s = aiEventOf({
      event: "sources",
      data: JSON.stringify({ q: "x", passages: [...SOURCES.passages, { n: 3 }, "junk"] }),
    });
    expect(s?.type === "sources" && s.passages.map((p) => p.video_id)).toEqual(["v1", "v2"]);
    const t = aiEventOf({
      event: "text",
      data: JSON.stringify({ ...TEXT1, citations: [...TEXT1.citations, { n: 9 }] }),
    });
    expect(t).toEqual({
      type: "text",
      sentence: { text: TEXT1.text, citations: TEXT1.citations },
    });
    expect(aiEventOf({ event: "text", data: '{"text":"  "}' })).toBeNull();
    expect(
      aiEventOf({ event: "done", data: '{"answered":false,"reason":"not-in-passages"}' })
    ).toMatchObject({ type: "done", answered: false, reason: "not-in-passages" });
    expect(aiEventOf({ event: "error", data: '{"code":"model-failed"}' })).toEqual({
      type: "error",
      code: "model-failed",
    });
    expect(aiEventOf({ event: "later", data: "{}" })).toBeNull();
    expect(aiEventOf({ event: "text", data: "not json" })).toBeNull();
  });
});

describe("askAI", () => {
  it("posts the question and hands each event over in order, pieces split anywhere", async () => {
    const cut = [STREAM.slice(0, 7), STREAM.slice(7, 80), STREAM.slice(80, 81), STREAM.slice(81)];
    const { events, fetchMock } = await collect(streamed(cut));
    expect(events.map((e) => e.type)).toEqual([
      "queue",
      "queue",
      "sources",
      "text",
      "text",
      "done",
    ]);
    const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("/api/search/ai");
    expect(init.method).toBe("POST");
    expect(JSON.parse(init.body as string)).toEqual({ q: "What is patience?" });
    expect((init.headers as Record<string, string>).Accept).toBe("text/event-stream");
  });

  it("an error event ends the answer; a stream cut before done says so", async () => {
    let r = await collect(
      streamed([ev("sources", SOURCES), ev("text", TEXT1), ev("error", { code: "model-failed" })])
    );
    expect(r.events.map((e) => e.type)).toEqual(["sources", "text", "error"]);
    expect(r.events[2]).toEqual({ type: "error", code: "model-failed" });
    r = await collect(streamed([ev("sources", SOURCES), ev("text", TEXT1)]));
    expect(r.events[r.events.length - 1]).toEqual({ type: "error", code: "stream-ended" });
  });

  it("a connection lost mid-answer is an error event, code network", async () => {
    const enc = new TextEncoder();
    let n = 0;
    const body = new ReadableStream<Uint8Array>({
      pull(c) {
        if (n++ === 0) c.enqueue(enc.encode(ev("sources", SOURCES)));
        else c.error(new TypeError("network"));
      },
    });
    const { events } = await collect(new Response(body, { status: 200 }));
    expect(events.map((e) => e.type)).toEqual(["sources", "error"]);
    expect(events[1]).toEqual({ type: "error", code: "network" });
  });

  it("409 ai-unavailable carries its help; 409 ai-off; 429 ai-busy its place and Retry-After", async () => {
    const refusal = async (res: Response) => {
      vi.stubGlobal(
        "fetch",
        vi.fn(() => Promise.resolve(res))
      );
      try {
        await askAI({ q: "x" }, () => {});
      } catch (e) {
        return aiRefusalOf(e);
      }
      throw new Error("no refusal");
    };
    expect(
      await refusal(
        problem(409, {
          type: "https://maktaba.tv/problems/ai-unavailable",
          code: "ai-unavailable",
          help: {
            settings: "/settings/rules?section=tagcheck&q=url",
            help: "/help/search#ai-mode",
          },
        })
      )
    ).toEqual({
      code: "ai-unavailable",
      status: 409,
      help: { settings: "/settings/rules?section=tagcheck&q=url", help: "/help/search#ai-mode" },
    });
    expect(await refusal(problem(409, { code: "ai-off" }))).toEqual({
      code: "ai-off",
      status: 409,
    });
    // the type alone names it too
    expect(
      await refusal(problem(409, { type: "https://maktaba.tv/problems/ai-off" }))
    ).toMatchObject({ code: "ai-off" });
    expect(
      await refusal(problem(429, { code: "ai-busy", position: 9 }, { "Retry-After": "30" }))
    ).toEqual({ code: "ai-busy", status: 429, position: 9, retryAfter: 30 });
    const other = await refusal(problem(500, { code: "x" }));
    expect(other.code).toBe("failed");
    expect(other.status).toBe(500);
  });

  it("an abort before or during the answer resolves quietly, with no more events", async () => {
    const ctrl = new AbortController();
    ctrl.abort();
    vi.stubGlobal(
      "fetch",
      vi.fn((_u: string, init: RequestInit) =>
        init.signal?.aborted
          ? Promise.reject(new DOMException("aborted", "AbortError"))
          : Promise.resolve(streamed([STREAM]))
      )
    );
    const events: AIEvent[] = [];
    await askAI({ q: "x" }, (e) => events.push(e), ctrl.signal);
    expect(events).toEqual([]);

    // mid-answer: the reader stops, the stream is cancelled, nothing after the abort
    const enc = new TextEncoder();
    const ctrl2 = new AbortController();
    let cancelled = false;
    let step = 0;
    const body = new ReadableStream<Uint8Array>({
      pull(c) {
        if (step++ === 0) c.enqueue(enc.encode(ev("sources", SOURCES)));
        else return new Promise(() => {}); // the model still writing
      },
      cancel() {
        cancelled = true;
      },
    });
    vi.stubGlobal(
      "fetch",
      vi.fn(() => Promise.resolve(new Response(body, { status: 200 })))
    );
    const got: AIEvent[] = [];
    const p = askAI(
      { q: "x" },
      (e) => {
        got.push(e);
        if (e.type === "sources") setTimeout(() => ctrl2.abort(), 0);
      },
      ctrl2.signal
    );
    await p;
    expect(got.map((e) => e.type)).toEqual(["sources"]);
    await waitFor(() => expect(cancelled).toBe(true));
  });
});

describe("the conversation", () => {
  it("a turn follows its events: in line, sources first, sentences appended, done", () => {
    let t: AITurnState = newTurn(1, "q");
    t = applyAIEvent(t, { type: "queue", position: 2 });
    expect(t).toMatchObject({ status: "waiting", position: 2 });
    t = applyAIEvent(t, { type: "sources", q: "q", passages: SOURCES.passages });
    expect(t).toMatchObject({ status: "writing", sourced: true, position: undefined });
    t = applyAIEvent(t, { type: "text", sentence: TEXT1 });
    t = applyAIEvent(t, { type: "text", sentence: TEXT2 });
    expect(t.sentences.map((s) => s.text)).toEqual([TEXT1.text, TEXT2.text]);
    t = applyAIEvent(t, { type: "done", answered: true });
    expect(t).toMatchObject({ status: "done", answered: true });
    // nothing changes a finished turn
    expect(applyAIEvent(t, { type: "text", sentence: TEXT1 })).toBe(t);
    // not answered: the passages stay
    let u = applyAIEvent(newTurn(2, "q"), { type: "sources", q: "q", passages: SOURCES.passages });
    u = applyAIEvent(u, { type: "done", answered: false, reason: "not-in-passages" });
    expect(u).toMatchObject({ status: "done", answered: false, reason: "not-in-passages" });
    expect(u.passages).toHaveLength(2);
  });

  it("history: the last three turns that have both a question and an answer", () => {
    const turn = (id: number, q: string, words: string[]): AITurnState => ({
      ...newTurn(id, q),
      status: "done",
      sentences: words.map((text) => ({ text, citations: [] })),
    });
    const turns = [
      turn(1, "a", ["A1.", "A2."]),
      turn(2, "b", []),
      turn(3, "c", ["C."]),
      turn(4, "d", ["D."]),
      turn(5, "e", ["E."]),
    ];
    expect(historyOf(turns)).toEqual([
      { q: "c", answer: "C." },
      { q: "d", answer: "D." },
      { q: "e", answer: "E." },
    ]);
    expect(historyOf(turns.slice(0, 2))).toEqual([{ q: "a", answer: "A1. A2." }]);
  });

  it("a follow-up sends the earlier turn as history; asking again stops the live turn; unmount aborts", async () => {
    const signals: AbortSignal[] = [];
    const bodies: unknown[] = [];
    let finish: ((events: AIEvent[]) => void) | null = null;
    const fake = vi.fn(
      (body: unknown, on: (e: AIEvent) => void, signal?: AbortSignal) =>
        new Promise<void>((resolve) => {
          bodies.push(body);
          signals.push(signal!);
          finish = (events) => {
            events.forEach(on);
            resolve();
          };
        })
    );
    const { result, unmount } = renderHook(() => useAIConversation(fake));
    act(() => result.current.ask("What is patience?"));
    expect(bodies[0]).toEqual({ q: "What is patience?" });
    act(() =>
      finish!([
        { type: "sources", q: "", passages: SOURCES.passages },
        { type: "text", sentence: TEXT1 },
        { type: "done", answered: true },
      ])
    );
    await waitFor(() => expect(result.current.turns[0]!.status).toBe("done"));

    act(() => result.current.ask("And its kinds?"));
    expect(bodies[1]).toEqual({
      q: "And its kinds?",
      history: [{ q: "What is patience?", answer: TEXT1.text }],
    });
    // asked again while it waits: the first is stopped and its events ignored
    act(() => result.current.ask("Its kinds, please"));
    expect(signals[1]!.aborted).toBe(true);
    expect(result.current.turns[1]!.status).toBe("stopped");
    expect(result.current.turns.map((t) => t.q)).toEqual([
      "What is patience?",
      "And its kinds?",
      "Its kinds, please",
    ]);
    // the stopped turn had no answer: it is not history
    expect(bodies[2]).toMatchObject({ history: [{ q: "What is patience?" }] });

    // stop (leaving the tab)
    act(() => result.current.stop());
    expect(signals[2]!.aborted).toBe(true);
    expect(result.current.turns[2]!.status).toBe("stopped");

    // leaving the page aborts a live turn
    act(() => result.current.ask("Once more"));
    unmount();
    await waitFor(() => expect(signals[3]!.aborted).toBe(true));
  });

  it("a refusal is the turn's", async () => {
    const fake = vi.fn(() => Promise.reject(Object.assign(new Error("x"), {})));
    const { result } = renderHook(() => useAIConversation(fake));
    act(() => result.current.ask("q"));
    await waitFor(() => expect(result.current.turns[0]!.status).toBe("refused"));
    expect(result.current.turns[0]!.refusal?.code).toBe("failed");
  });

  it("a tab opened with words asks them once; StrictMode's rehearsal does not abort it; typing does not ask", async () => {
    const signals: AbortSignal[] = [];
    const bodies: unknown[] = [];
    const fake = vi.fn((body: unknown, _on: (e: AIEvent) => void, signal?: AbortSignal) => {
      bodies.push(body);
      signals.push(signal!);
      return new Promise<void>(() => {});
    });
    const wrapper = ({ children }: { children: ReactNode }) =>
      createElement(StrictMode, null, children);
    const { result, rerender, unmount } = renderHook(
      ({ active, q }: { active: boolean; q: string }) => {
        const conv = useAIConversation(fake);
        useAITab(conv, active, q);
        return conv;
      },
      { wrapper, initialProps: { active: true, q: "patience" } }
    );
    expect(bodies).toEqual([{ q: "patience" }]);
    await new Promise((r) => setTimeout(r, 10));
    expect(signals[0]!.aborted).toBe(false);
    expect(result.current.turns[0]!.status).toBe("asking");
    // typing while the tab is open asks nothing
    rerender({ active: true, q: "patience and" });
    expect(bodies).toHaveLength(1);
    // leaving the tab stops it; coming back with a conversation asks nothing
    rerender({ active: false, q: "patience and" });
    expect(signals[0]!.aborted).toBe(true);
    rerender({ active: true, q: "patience and" });
    expect(bodies).toHaveLength(1);
    unmount();
  });
});
