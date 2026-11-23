// Story 49.14 — AI mode's client: POST /api/search/ai (story 49.12) asked and its Server-Sent
// Events read, for the Search page (pages/SearchAI.tsx) and the big screen's Search (and so
// the TV launchers' /tv/ build). The guide is docs/developers/contributing/ai-mode.md.
//
// The answer is a stream over a POST, so EventSource (GET only) cannot read it: the body is
// read with fetch, the pieces cut into events wherever they were split. Events, in order:
//   queue   {position}                 another answer is being written; the place in line
//   sources {q, passages[]}            the numbered passages, before any sentence
//   text    {text, citations[]}        one checked sentence: each citation a video and moment
//   done    {answered, reason?, ...}   the end; answered false: the passages do not answer
//   error   {code}                     the answer stopped; the sentences sent stand
// A refusal comes before the stream as problem+json: 409 ai-unavailable (no language model:
// `help` names the settings and the help page), 409 ai-off, 429 ai-busy (Retry-After).
// Aborting the signal closes the connection: the home stops the model and gives the turn on.
import { useCallback, useEffect, useRef, useState } from "react";
import {
  ApiError,
  type Problem,
  apiUrl,
  authHeaders,
  bearerAuth,
  csrfHeaders,
  retryAfterSeconds,
} from "./api";

export const AI_PATH = "/api/search/ai";
/** The earlier turns the home gives the model (it keeps the last three). */
export const AI_HISTORY_TURNS = 3;

export interface AIPassage {
  n: number;
  video_id: string;
  title: string;
  library_name?: string;
  poster_url?: string;
  start_sec: number;
  end_sec?: number;
  text: string;
}

export interface AICitation {
  n: number;
  video_id: string;
  start_sec: number;
  end_sec?: number;
  quote?: string;
}

export interface AISentence {
  text: string;
  citations: AICitation[];
}

export type AIEvent =
  | { type: "queue"; position: number }
  | { type: "sources"; q: string; passages: AIPassage[] }
  | { type: "text"; sentence: AISentence }
  | {
      type: "done";
      answered: boolean;
      reason?: string;
      sentences?: number;
      dropped?: number;
      first_token_ms?: number;
      took_ms?: number;
    }
  | { type: "error"; code: string };

/** An earlier question and its answer, as `history` sends it. */
export interface AIHistoryTurn {
  q: string;
  answer: string;
}

export interface AIAsk {
  q: string;
  history?: AIHistoryTurn[];
  filters?: Record<string, unknown>;
}

/** Why the home would not answer: a 409, a 429 or any other failure before the stream. */
export interface AIRefusal {
  /** ai-unavailable, ai-off, ai-busy, or "failed" for anything else */
  code: "ai-unavailable" | "ai-off" | "ai-busy" | "failed";
  status: number;
  /** ai-unavailable: the web app's paths of the model server's setting and of the help page */
  help?: { settings?: string; help?: string };
  /** ai-busy: the place the question would have had */
  position?: number;
  /** ai-busy: seconds until it may ask again */
  retryAfter?: number;
  /** "failed": the error, for its words */
  error?: unknown;
}

/** The help page AI mode's messages point to. */
export const AI_HELP_PATH = "/help/search#ai-mode";

/** One raw event of a Server-Sent Events stream. */
export interface SSEMessage {
  event: string;
  data: string;
}

/**
 * Cuts a Server-Sent Events stream into its events, whatever pieces it arrives in: a piece
 * may end inside a line, inside "\r\n", or between an event's lines. Comment lines (": ping")
 * are skipped; an event without `event:` is "message"; data lines join with "\n".
 */
export class SSEReader {
  private buf = "";
  private event = "";
  private data: string[] = [];
  private cr = false;

  /** The events the piece completes. */
  push(piece: string): SSEMessage[] {
    const out: SSEMessage[] = [];
    let text = piece;
    // a "\r" that ended the last piece was a line's end: a "\n" starting this one is its pair
    if (this.cr && text.startsWith("\n")) text = text.slice(1);
    this.cr = text.endsWith("\r");
    this.buf += text;
    for (;;) {
      const m = /\r\n|\r|\n/.exec(this.buf);
      if (!m) break;
      const line = this.buf.slice(0, m.index);
      this.buf = this.buf.slice(m.index + m[0].length);
      this.line(line, out);
    }
    return out;
  }

  /** The event a stream that ends without its blank line still completes. */
  end(): SSEMessage[] {
    const out: SSEMessage[] = [];
    if (this.buf) this.line(this.buf, out);
    this.buf = "";
    this.line("", out);
    return out;
  }

  private line(line: string, out: SSEMessage[]) {
    if (line === "") {
      if (this.data.length)
        out.push({ event: this.event || "message", data: this.data.join("\n") });
      this.event = "";
      this.data = [];
      return;
    }
    if (line.startsWith(":")) return;
    const colon = line.indexOf(":");
    const field = colon < 0 ? line : line.slice(0, colon);
    let value = colon < 0 ? "" : line.slice(colon + 1);
    if (value.startsWith(" ")) value = value.slice(1);
    if (field === "event") this.event = value;
    else if (field === "data") this.data.push(value);
  }
}

const num = (v: unknown, d = 0) => (typeof v === "number" && Number.isFinite(v) ? v : d);
const str = (v: unknown) => (typeof v === "string" ? v : "");
const rec = (v: unknown): Record<string, unknown> =>
  v && typeof v === "object" && !Array.isArray(v) ? (v as Record<string, unknown>) : {};
const arr = (v: unknown): unknown[] => (Array.isArray(v) ? v : []);

function passageOf(v: unknown): AIPassage | null {
  const p = rec(v);
  const id = str(p.video_id);
  if (!id) return null;
  return {
    n: num(p.n),
    video_id: id,
    title: str(p.title),
    library_name: str(p.library_name) || undefined,
    poster_url: str(p.poster_url) || undefined,
    start_sec: Math.max(0, num(p.start_sec)),
    end_sec: typeof p.end_sec === "number" ? p.end_sec : undefined,
    text: str(p.text),
  };
}

function citationOf(v: unknown): AICitation | null {
  const c = rec(v);
  const id = str(c.video_id);
  if (!id) return null;
  return {
    n: num(c.n),
    video_id: id,
    start_sec: Math.max(0, num(c.start_sec)),
    end_sec: typeof c.end_sec === "number" ? c.end_sec : undefined,
    quote: str(c.quote) || undefined,
  };
}

/** A raw event read as AI mode's, tolerantly; null for one this client does not know. */
export function aiEventOf(m: SSEMessage): AIEvent | null {
  let d: Record<string, unknown>;
  try {
    d = rec(JSON.parse(m.data));
  } catch {
    return null;
  }
  switch (m.event) {
    case "queue":
      return { type: "queue", position: num(d.position, 1) };
    case "sources":
      return {
        type: "sources",
        q: str(d.q),
        passages: arr(d.passages)
          .map(passageOf)
          .filter((p): p is AIPassage => p !== null),
      };
    case "text": {
      const text = str(d.text).trim();
      if (!text) return null;
      return {
        type: "text",
        sentence: {
          text,
          citations: arr(d.citations)
            .map(citationOf)
            .filter((c): c is AICitation => c !== null),
        },
      };
    }
    case "done":
      return {
        type: "done",
        answered: d.answered === true,
        reason: str(d.reason) || undefined,
        sentences: num(d.sentences),
        dropped: num(d.dropped),
        first_token_ms: typeof d.first_token_ms === "number" ? d.first_token_ms : undefined,
        took_ms: typeof d.took_ms === "number" ? d.took_ms : undefined,
      };
    case "error":
      return { type: "error", code: str(d.code) || "failed" };
    default:
      return null;
  }
}

/** The refusal an error before the stream is (any error: "failed"). */
export function aiRefusalOf(e: unknown): AIRefusal {
  if (e instanceof ApiError) {
    const p = e.problem as Problem & Record<string, unknown>;
    const code = str(p.code) || (p.type ? p.type.split("/").pop() : "") || "";
    if (code === "ai-unavailable") {
      const h = rec(p.help);
      return {
        code,
        status: e.status,
        help: { settings: str(h.settings) || undefined, help: str(h.help) || undefined },
      };
    }
    if (code === "ai-off") return { code, status: e.status };
    if (code === "ai-busy" || e.status === 429)
      return {
        code: "ai-busy",
        status: e.status,
        position: typeof p.position === "number" ? p.position : undefined,
        retryAfter: e.retryAfter,
      };
    return { code: "failed", status: e.status, error: e };
  }
  return { code: "failed", status: 0, error: e };
}

const isAbort = (e: unknown, signal?: AbortSignal) =>
  signal?.aborted || (e instanceof DOMException && e.name === "AbortError");

/**
 * Asks AI mode and hands each event to `onEvent` as it comes. Resolves when the stream ends
 * (a stream cut before `done` or `error` gives an `error` event, code "stream-ended"; a
 * connection lost mid-answer, code "network") or quietly when `signal` aborts. Rejects with
 * an ApiError when the home refuses before the stream (aiRefusalOf reads it).
 */
export async function askAI(
  body: AIAsk,
  onEvent: (e: AIEvent) => void,
  signal?: AbortSignal
): Promise<void> {
  // the headers are read at each send: a renewed bearer token goes with the retry
  const send = () =>
    fetch(apiUrl(AI_PATH), {
      method: "POST",
      credentials: bearerAuth() ? "omit" : "include",
      headers: {
        Accept: "text/event-stream",
        "Content-Type": "application/json",
        ...csrfHeaders(),
        ...authHeaders(),
      },
      body: JSON.stringify(body),
      signal,
    });
  let res: Response;
  try {
    res = await send();
    // a bearer sign-in's access token ran out (a TV launcher left on Search): a new one, and
    // once more, as every other call does (api.ts request)
    const bearer = bearerAuth();
    if (res.status === 401 && bearer?.token() && (await bearer.refresh())) res = await send();
  } catch (e) {
    if (isAbort(e, signal)) return;
    throw e;
  }
  if (!res.ok) {
    const text = await res.text().catch(() => "");
    let problem: Problem;
    try {
      const parsed = JSON.parse(text) as Problem;
      problem = { ...parsed, status: parsed.status || res.status };
    } catch {
      problem = {
        type: "about:blank",
        title: res.statusText || "request failed",
        status: res.status,
      };
    }
    throw new ApiError(problem, retryAfterSeconds(res.headers.get("Retry-After")));
  }
  if (!res.body) {
    onEvent({ type: "error", code: "stream-ended" });
    return;
  }
  const reader = res.body.getReader();
  const decoder = new TextDecoder();
  const sse = new SSEReader();
  let ended = false;
  // an abort ends a read the body is still waiting on (whatever fetch does with the signal)
  const onAbort = () => void reader.cancel().catch(() => {});
  signal?.addEventListener("abort", onAbort);
  const deliver = (msgs: SSEMessage[]) => {
    for (const m of msgs) {
      if (ended || signal?.aborted) return;
      const ev = aiEventOf(m);
      if (!ev) continue;
      if (ev.type === "done" || ev.type === "error") ended = true;
      onEvent(ev);
    }
  };
  try {
    for (;;) {
      const { value, done } = await reader.read();
      if (done) break;
      deliver(sse.push(decoder.decode(value, { stream: true })));
      if (ended) break;
    }
    if (!ended) deliver(sse.push(decoder.decode()).concat(sse.end()));
  } catch (e) {
    if (isAbort(e, signal)) return;
    if (!ended) {
      ended = true;
      onEvent({ type: "error", code: "network" });
    }
    return;
  } finally {
    signal?.removeEventListener("abort", onAbort);
    if (ended || signal?.aborted) reader.cancel().catch(() => {});
  }
  if (!ended && !signal?.aborted) onEvent({ type: "error", code: "stream-ended" });
}

// ---- a conversation: the turns on screen, shared by the page and the big screen ----

export type AITurnStatus =
  | "asking" // sent, nothing back yet
  | "waiting" // in line (position)
  | "writing" // the sources are shown; sentences come
  | "done"
  | "stopped" // the person asked again or left: what was shown stands
  | "failed" // an `error` event or a lost connection: what was shown stands
  | "refused"; // a 409 / 429 / failure before the stream

export interface AITurnState {
  id: number;
  q: string;
  status: AITurnStatus;
  position?: number;
  passages: AIPassage[];
  /** the sources event came (an empty list is an answer too) */
  sourced: boolean;
  sentences: AISentence[];
  answered?: boolean;
  reason?: string;
  errorCode?: string;
  refusal?: AIRefusal;
}

export const newTurn = (id: number, q: string): AITurnState => ({
  id,
  q,
  status: "asking",
  passages: [],
  sourced: false,
  sentences: [],
});

/** A turn after one event (pure). */
export function applyAIEvent(t: AITurnState, ev: AIEvent): AITurnState {
  if (t.status === "stopped" || t.status === "failed" || t.status === "done") return t;
  switch (ev.type) {
    case "queue":
      return { ...t, status: "waiting", position: ev.position };
    case "sources":
      return { ...t, status: "writing", position: undefined, passages: ev.passages, sourced: true };
    case "text":
      return {
        ...t,
        status: "writing",
        position: undefined,
        sentences: [...t.sentences, ev.sentence],
      };
    case "done":
      return {
        ...t,
        status: "done",
        position: undefined,
        answered: ev.answered && t.sentences.length > 0,
        reason: ev.reason,
      };
    case "error":
      return { ...t, status: "failed", position: undefined, errorCode: ev.code };
  }
}

/** The answer a turn wrote, as `history` sends it. */
export const answerText = (t: AITurnState) =>
  t.sentences
    .map((s) => s.text)
    .join(" ")
    .trim();

/** The `history` of a next question: the last turns that have both a question and an answer. */
export function historyOf(turns: AITurnState[]): AIHistoryTurn[] {
  return turns
    .map((t) => ({ q: t.q, answer: answerText(t) }))
    .filter((h) => h.q && h.answer)
    .slice(-AI_HISTORY_TURNS);
}

/** A turn still on its way (asking, in line or writing). */
export const turnLive = (t: AITurnState | undefined) =>
  !!t && (t.status === "asking" || t.status === "waiting" || t.status === "writing");

export interface AIConversation {
  turns: AITurnState[];
  /** Asks `q` as the next turn (a live turn is stopped first; its words stand). */
  ask(q: string, filters?: Record<string, unknown>): void;
  /** Stops the live turn, if any (the person left the tab). */
  stop(): void;
  /** Forgets the conversation (and stops its live turn). */
  reset(): void;
}

/**
 * The conversation of an AI mode tab: each ask a turn, its history the earlier turns. Only
 * one turn is live: asking again, stop(), reset() or leaving the page (unmount) aborts it.
 */
export function useAIConversation(ask: typeof askAI = askAI): AIConversation {
  const [turns, setTurns] = useState<AITurnState[]>([]);
  const turnsRef = useRef(turns);
  turnsRef.current = turns;
  const live = useRef<{ id: number; ctrl: AbortController } | null>(null);
  const seq = useRef(0);
  const askRef = useRef(ask);
  askRef.current = ask;

  const update = useCallback((id: number, f: (t: AITurnState) => AITurnState) => {
    setTurns((all) => {
      const next = all.map((t) => (t.id === id ? f(t) : t));
      turnsRef.current = next;
      return next;
    });
  }, []);

  const stop = useCallback(() => {
    const l = live.current;
    if (!l) return;
    live.current = null;
    l.ctrl.abort();
    update(l.id, (t) => (turnLive(t) ? { ...t, status: "stopped", position: undefined } : t));
  }, [update]);

  const run = useCallback(
    (q: string, filters?: Record<string, unknown>) => {
      const question = q.trim();
      if (!question) return;
      stop();
      const history = historyOf(turnsRef.current);
      const id = ++seq.current;
      const ctrl = new AbortController();
      live.current = { id, ctrl };
      setTurns((all) => {
        const next = [...all, newTurn(id, question)];
        turnsRef.current = next;
        return next;
      });
      const mine = () => live.current?.id === id && !ctrl.signal.aborted;
      const body: AIAsk = { q: question };
      if (history.length) body.history = history;
      if (filters) body.filters = filters;
      askRef
        .current(
          body,
          (ev) => {
            if (!mine()) return;
            update(id, (t) => applyAIEvent(t, ev));
            if (ev.type === "done" || ev.type === "error") live.current = null;
          },
          ctrl.signal
        )
        .then(() => {
          if (live.current?.id === id) live.current = null;
        })
        .catch((e) => {
          if (!mine()) return;
          live.current = null;
          update(id, (t) => ({ ...t, status: "refused", refusal: aiRefusalOf(e) }));
        });
    },
    [stop, update]
  );

  const reset = useCallback(() => {
    stop();
    turnsRef.current = [];
    setTurns([]);
  }, [stop]);

  // leaving the page closes the connection. Only a real unmount: StrictMode's rehearsal
  // (cleanup, then setup again at once) must not abort the turn its first pass asked.
  const mounted = useRef(false);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      setTimeout(() => {
        if (!mounted.current) live.current?.ctrl.abort();
      }, 0);
    };
  }, []);

  return { turns, ask: run, stop, reset };
}

/**
 * An AI mode tab's life: leaving it (`active` false) stops the live turn; opening it with
 * words in the box (`q`) and no conversation yet asks them, then calls `onAsked`. Typing while
 * it is open does not.
 */
export function useAITab(conv: AIConversation, active: boolean, q: string, onAsked?: () => void) {
  const open = useRef(false);
  const { stop, ask, turns } = conv;
  const empty = turns.length === 0;
  const qRef = useRef(q);
  qRef.current = q;
  const onAskedRef = useRef(onAsked);
  onAskedRef.current = onAsked;
  useEffect(() => {
    if (!active) {
      open.current = false;
      stop();
      return;
    }
    if (open.current) return;
    open.current = true;
    const question = qRef.current.trim();
    if (empty && question) {
      ask(question);
      onAskedRef.current?.();
    }
  }, [active, empty, stop, ask]);
}
