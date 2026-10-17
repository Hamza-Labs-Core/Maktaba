// Server-sent-events fan-in (Story 7.16 transport; consumed by
// ProcessingQueue, VideoDetail, …).
//
// The API mounts SSE — NOT raw WebSocket — at:
//
//   /ws/jobs                 global job lifecycle
//   /ws/queues               2 s `queues.snapshot` (ephemeral, no replay)
//   /ws/library/{id}         per-library video changes
//   /ws/playback/{video_id}  playback position fan-out
//
// Every frame is the AC-3 envelope `{ type, at, payload }` (see
// api/internal/handlers/ws/ws.go Event). The old code connected a
// WebSocket to the unmounted `/ws/v1/events` and read `msg.job`; both
// were dead. EventSource auto-reconnects and replays via the standard
// `Last-Event-ID` header, so we lean on it and only add a bounded
// manual backoff for the construction-failure path.

export interface ServerEvent {
  type: string;
  at: string;
  payload?: Record<string, unknown>;
}

type Listener = (ev: ServerEvent) => void;

const BASE = (import.meta.env.VITE_API_BASE ?? "") as string;

export type Channel = "jobs" | "queues" | `library/${string}` | `playback/${string}`;

class Subscription {
  private es: EventSource | null = null;
  private listeners = new Set<Listener>();
  private backoff = 1000;
  private retry: ReturnType<typeof setTimeout> | null = null;
  private closed = false;
  // Last server event id seen (payload._event_id). A native EventSource
  // reconnect replays through Last-Event-ID by itself; after a manual
  // reopen (constructor failure / readyState CLOSED) that header is
  // gone, so the id is passed as ?last_event_id= (ws.go accepts both).
  private lastEventId: string | null = null;

  constructor(
    private readonly path: string,
    private readonly onEmpty: () => void
  ) {}

  add(fn: Listener): () => void {
    this.listeners.add(fn);
    if (!this.es && !this.closed && !this.retry) this.open();
    return () => {
      this.listeners.delete(fn);
      if (this.listeners.size === 0) this.close();
    };
  }

  private url(): string {
    if (this.lastEventId === null) return `${BASE}${this.path}`;
    const sep = this.path.includes("?") ? "&" : "?";
    return `${BASE}${this.path}${sep}last_event_id=${encodeURIComponent(this.lastEventId)}`;
  }

  private open(): void {
    if (this.closed) return;
    this.retry = null;
    let es: EventSource;
    try {
      es = new EventSource(this.url(), { withCredentials: true });
    } catch {
      this.scheduleReopen();
      return;
    }
    this.es = es;
    es.onopen = () => {
      this.backoff = 1000;
    };
    es.onmessage = (e: MessageEvent) => {
      let ev: ServerEvent;
      try {
        ev = JSON.parse(e.data) as ServerEvent;
      } catch {
        return; // ignore comment/heartbeat frames
      }
      const id = ev.payload?._event_id;
      if (typeof id === "number" || typeof id === "string") this.lastEventId = String(id);
      // Copy: a listener may unsubscribe while we iterate.
      [...this.listeners].forEach((fn) => fn(ev));
    };
    es.onerror = () => {
      // EventSource retries on its own; if the browser closed it for
      // good, fall back to a bounded manual reopen.
      if (es.readyState === EventSource.CLOSED) {
        this.es = null;
        this.scheduleReopen();
      }
    };
  }

  private scheduleReopen(): void {
    if (this.closed || this.listeners.size === 0) return;
    this.retry = setTimeout(() => this.open(), this.backoff);
    this.backoff = Math.min(this.backoff * 2, 30_000);
  }

  private close(): void {
    this.closed = true;
    if (this.retry) {
      clearTimeout(this.retry);
      this.retry = null;
    }
    this.es?.close();
    this.es = null;
    this.onEmpty();
  }
}

// One Subscription (= one EventSource) per channel, ref-counted by its
// listeners. Before this, every subscribe() opened its own connection.
const subscriptions = new Map<string, Subscription>();

// subscribe opens (or re-uses, per channel) an SSE stream and invokes
// `fn` for every decoded envelope. The returned disposer removes the
// listener and tears the stream down once no listeners remain.
export function subscribe(channel: Channel, fn: Listener): () => void {
  let sub = subscriptions.get(channel);
  if (!sub) {
    const created = new Subscription(`/ws/${channel}`, () => {
      if (subscriptions.get(channel) === created) subscriptions.delete(channel);
    });
    subscriptions.set(channel, created);
    sub = created;
  }
  return sub.add(fn);
}
