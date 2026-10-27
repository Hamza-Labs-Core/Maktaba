// Story 16.5: the app's usage-data queue. Events wait here (and in
// localStorage, so a closed tab or a dropped network keeps them) until a flush
// sends them, at most 100 a request. At most 1,000 wait: the oldest go first.
// A send that fails on the network, a 5xx or a 429 is put back and retried with
// exponential backoff (2 s, 4 s, ... 5 min), and so is one refused because no
// one is signed in (401: it goes once someone is); any other 4xx means the
// home refused the batch itself, which resending cannot fix, so it is dropped. Nothing here
// awaits on the UI's path: enqueue only schedules.

export type PayloadValue = string | number | boolean;

export interface TelemetryEvent {
  kind: string;
  payload: Record<string, PayloadValue>;
  ts: string;
}

/** Sender posts one batch; it throws on failure (an ApiError carries .status). */
export type Sender = (batch: TelemetryEvent[]) => Promise<void>;

export interface QueueOptions {
  send: Sender;
  /** where waiting events are kept between launches; null: memory only */
  storageKey?: string | null;
  max?: number;
  batch?: number;
  flushMs?: number;
  maxBackoffMs?: number;
}

export const QUEUE_MAX = 1000;
export const BATCH_MAX = 100;

export class TelemetryQueue {
  private events: TelemetryEvent[] = [];
  private timer: ReturnType<typeof setTimeout> | null = null;
  private failures = 0;
  private sending = false;
  private stopped = false;
  private readonly opts: Required<QueueOptions>;

  constructor(opts: QueueOptions) {
    this.opts = {
      storageKey: null,
      max: QUEUE_MAX,
      batch: BATCH_MAX,
      flushMs: 30_000,
      maxBackoffMs: 300_000,
      ...opts,
    };
    this.events = this.load();
    if (this.events.length > 0) this.schedule(this.opts.flushMs);
  }

  get size(): number {
    return this.events.length;
  }

  /** the delay before the next retry after n failures in a row */
  backoffMs(n: number): number {
    return Math.min(this.opts.maxBackoffMs, 2 ** n * 1000);
  }

  enqueue(e: TelemetryEvent): void {
    if (this.stopped) return;
    this.events.push(e);
    if (this.events.length > this.opts.max) {
      this.events.splice(0, this.events.length - this.opts.max); // the oldest go
    }
    this.persist();
    if (this.failures === 0) this.schedule(this.opts.flushMs);
  }

  /** flush sends one batch now; the rest follows on the next tick. */
  async flush(): Promise<void> {
    this.cancel();
    if (this.stopped || this.sending || this.events.length === 0) return;
    const batch = this.events.splice(0, this.opts.batch);
    this.sending = true;
    try {
      await this.opts.send(batch);
      this.failures = 0;
    } catch (e) {
      const status = (e as { status?: number } | null)?.status ?? 0;
      if (status >= 400 && status < 500 && status !== 429 && status !== 401) {
        // refused as it is: sending it again would be refused again
        this.failures = 0;
        console.warn(`[usage data] the home refused a batch (${status}); dropped`);
      } else if (!this.stopped) {
        this.events = [...batch, ...this.events].slice(-this.opts.max);
        this.failures++;
        this.persist();
        this.schedule(this.backoffMs(this.failures));
        return;
      }
    } finally {
      this.sending = false;
    }
    this.persist();
    if (this.events.length > 0) this.schedule(this.opts.flushMs);
  }

  /** stop drops every waiting event and sends nothing more (opt-out, forget). */
  stop(): void {
    this.stopped = true;
    this.cancel();
    this.events = [];
    this.persist();
  }

  private schedule(ms: number): void {
    if (this.timer !== null || this.stopped) return;
    this.timer = setTimeout(() => {
      this.timer = null;
      void this.flush();
    }, ms);
  }

  private cancel(): void {
    if (this.timer !== null) {
      clearTimeout(this.timer);
      this.timer = null;
    }
  }

  private load(): TelemetryEvent[] {
    if (!this.opts.storageKey) return [];
    try {
      const list = JSON.parse(localStorage.getItem(this.opts.storageKey) ?? "[]") as unknown;
      return Array.isArray(list) ? (list as TelemetryEvent[]).slice(-this.opts.max) : [];
    } catch {
      return [];
    }
  }

  private persist(): void {
    if (!this.opts.storageKey) return;
    try {
      if (this.events.length === 0) localStorage.removeItem(this.opts.storageKey);
      else localStorage.setItem(this.opts.storageKey, JSON.stringify(this.events));
    } catch {
      /* storage full or refused: the events stay in memory */
    }
  }
}
