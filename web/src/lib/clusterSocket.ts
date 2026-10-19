// The browser's one connection to the cluster hub (design-agent.md §5):
// a WebSocket at /api/cluster/ws, same origin, signed in by the session
// cookie.
//
//   browser -> hub  {op: "subscribe" | "unsubscribe", topics: [...]}
//                   {op: "cmd", id, session, name, args}   (admin)
//                   {op: "ping"}                            every 20 s
//   hub -> browser  {type: "snapshot", topic, data} on subscribe, then
//                   {type: "queues" | "job" | "workers" | "metrics" |
//                    "library" | "llm" | "process" | "log" | "dropped",
//                    data}, {type: "cmd.result", data: {id, ok, detail}},
//                   {type: "pong"}
//
// One socket for the whole app, shared by every hook: topics are
// ref-counted (the first listener subscribes, the last one unsubscribes),
// the socket reconnects with a bounded backoff and re-subscribes every
// live topic, and closes a few seconds after the last topic is gone.
//
// Callers that have an older path (the Processing page's SSE stream) use
// it while `socketFallback(status)` is true: the socket is missing, or
// failed and has not come back yet.
import { useEffect, useRef, useSyncExternalStore } from "react";
import { clusterChannelApi, type CommandResult, type ProcessCommand } from "./clusterApi";

export type SocketStatus = "idle" | "connecting" | "open" | "reconnecting" | "unavailable";

export interface HubMessage {
  type: string;
  topic?: string;
  data?: unknown;
}

export type HubListener = (msg: HubMessage) => void;

export const SOCKET_PATH = "/api/cluster/ws";
export const PING_MS = 20_000;
export const BACKOFF_START_MS = 1_000;
export const BACKOFF_MAX_MS = 30_000;
export const LINGER_MS = 5_000;
export const CMD_TIMEOUT_MS = 15_000;

/** Which topic a hub message belongs to (snapshot and log are routed apart). */
const TYPE_TOPIC: Record<string, string> = {
  queues: "queues",
  job: "jobs",
  workers: "workers",
  metrics: "metrics",
  library: "library",
  llm: "llm",
  process: "processes",
};

/** ws(s)://<host>/api/cluster/ws, from VITE_API_BASE when it is absolute. */
export function socketUrl(): string {
  const base = (import.meta.env.VITE_API_BASE ?? "") as string;
  const origin = /^https?:\/\//.test(base)
    ? base.replace(/\/+$/, "")
    : typeof window !== "undefined"
      ? window.location.origin
      : "http://localhost";
  return `${origin.replace(/^http/, "ws")}${SOCKET_PATH}`;
}

/** The logs topic for a target (an IP, a node id or a session) at a level. */
export function logsTopic(target: string, level: string): string {
  return `logs:${target}:${level}`;
}

/** true while the socket cannot serve and an older transport should. */
export function socketFallback(status: SocketStatus): boolean {
  return status === "reconnecting" || status === "unavailable";
}

interface PendingCmd {
  resolve: (r: CommandResult) => void;
  reject: (e: Error) => void;
  timer: ReturnType<typeof setTimeout>;
}

class ClusterSocket {
  private ws: WebSocket | null = null;
  private topics = new Map<string, Set<HubListener>>();
  private statusListeners = new Set<() => void>();
  private status: SocketStatus = "idle";
  private backoff = BACKOFF_START_MS;
  private retry: ReturnType<typeof setTimeout> | null = null;
  private linger: ReturnType<typeof setTimeout> | null = null;
  private pinger: ReturnType<typeof setInterval> | null = null;
  private pending = new Map<string, PendingCmd>();
  private failed = false;
  private seq = 0;

  getStatus = (): SocketStatus => this.status;

  onStatus = (fn: () => void): (() => void) => {
    this.statusListeners.add(fn);
    return () => this.statusListeners.delete(fn);
  };

  /** Listen to a topic; returns the disposer. */
  subscribe(topic: string, fn: HubListener): () => void {
    let set = this.topics.get(topic);
    if (!set) {
      set = new Set();
      this.topics.set(topic, set);
      this.send({ op: "subscribe", topics: [topic] });
    }
    set.add(fn);
    if (this.linger) {
      clearTimeout(this.linger);
      this.linger = null;
    }
    this.ensureOpen();
    const own = set;
    return () => {
      own.delete(fn);
      if (own.size > 0 || this.topics.get(topic) !== own) return;
      this.topics.delete(topic);
      this.send({ op: "unsubscribe", topics: [topic] });
      if (this.topics.size === 0 && !this.linger) {
        this.linger = setTimeout(() => {
          this.linger = null;
          if (this.topics.size === 0) this.shutdown("idle");
        }, LINGER_MS);
      }
    };
  }

  /**
   * Runs a per-process command (§3 cmd): over the socket when it is open,
   * else through POST /api/cluster/processes/{session}/cmd.
   */
  cmd(
    session: string,
    name: ProcessCommand,
    args: Record<string, unknown> = {}
  ): Promise<CommandResult> {
    if (!this.ws || this.status !== "open") return clusterChannelApi.cmd(session, name, args);
    const id = `cmd-${++this.seq}`;
    return new Promise<CommandResult>((resolve, reject) => {
      const timer = setTimeout(() => {
        this.pending.delete(id);
        reject(new Error("timeout"));
      }, CMD_TIMEOUT_MS);
      this.pending.set(id, { resolve, reject, timer });
      this.send({ op: "cmd", id, session, name, args });
    });
  }

  /** Test hook: forget every listener and close. */
  reset(): void {
    this.topics.clear();
    this.shutdown("idle");
    this.statusListeners.clear();
    this.failed = false;
    this.backoff = BACKOFF_START_MS;
  }

  private setStatus(s: SocketStatus): void {
    if (this.status === s) return;
    this.status = s;
    [...this.statusListeners].forEach((fn) => fn());
  }

  private send(msg: unknown): void {
    if (this.ws && this.status === "open") {
      try {
        this.ws.send(JSON.stringify(msg));
      } catch {
        // the close handler reconnects
      }
    }
  }

  private ensureOpen(): void {
    if (this.ws || this.retry || this.topics.size === 0) return;
    if (typeof WebSocket === "undefined") {
      this.setStatus("unavailable");
      return;
    }
    this.connect();
  }

  private connect(): void {
    this.retry = null;
    if (this.topics.size === 0) return;
    this.setStatus(this.failed ? "reconnecting" : "connecting");
    let ws: WebSocket;
    try {
      ws = new WebSocket(socketUrl());
    } catch {
      this.lost();
      return;
    }
    this.ws = ws;
    ws.onopen = () => {
      if (this.ws !== ws) return;
      this.backoff = BACKOFF_START_MS;
      this.failed = false;
      this.setStatus("open");
      if (this.topics.size > 0) this.send({ op: "subscribe", topics: [...this.topics.keys()] });
      if (this.pinger) clearInterval(this.pinger);
      this.pinger = setInterval(() => this.send({ op: "ping" }), PING_MS);
    };
    ws.onmessage = (e: MessageEvent) => {
      if (this.ws !== ws) return;
      let msg: HubMessage;
      try {
        msg = JSON.parse(String(e.data)) as HubMessage;
      } catch {
        return;
      }
      if (msg && typeof msg.type === "string") this.dispatch(msg);
    };
    ws.onclose = () => {
      if (this.ws !== ws) return;
      this.ws = null;
      this.lost();
    };
    ws.onerror = () => {
      // onclose follows and reconnects
    };
  }

  private lost(): void {
    if (this.pinger) clearInterval(this.pinger);
    this.pinger = null;
    this.failPending();
    if (this.topics.size === 0) {
      this.setStatus("idle");
      return;
    }
    this.failed = true;
    this.setStatus("reconnecting");
    this.retry = setTimeout(() => this.connect(), this.backoff);
    this.backoff = Math.min(this.backoff * 2, BACKOFF_MAX_MS);
  }

  private failPending(): void {
    for (const p of this.pending.values()) {
      clearTimeout(p.timer);
      p.reject(new Error("disconnected"));
    }
    this.pending.clear();
  }

  private shutdown(status: SocketStatus): void {
    if (this.retry) clearTimeout(this.retry);
    if (this.linger) clearTimeout(this.linger);
    if (this.pinger) clearInterval(this.pinger);
    this.retry = this.linger = this.pinger = null;
    const ws = this.ws;
    this.ws = null;
    this.failPending();
    try {
      ws?.close();
    } catch {
      // already closed
    }
    this.setStatus(status);
  }

  private deliver(topic: string, msg: HubMessage): void {
    const set = this.topics.get(topic);
    if (set) [...set].forEach((fn) => fn(msg));
  }

  private dispatch(msg: HubMessage): void {
    if (msg.type === "pong") return;
    if (msg.type === "cmd.result") {
      const d = (msg.data ?? {}) as { id?: string; ok?: boolean; detail?: string | null };
      const p = d.id ? this.pending.get(d.id) : undefined;
      if (p && d.id) {
        clearTimeout(p.timer);
        this.pending.delete(d.id);
        p.resolve({ ok: !!d.ok, detail: d.detail ?? null });
      }
      return;
    }
    if (msg.type === "snapshot" && typeof msg.topic === "string") {
      this.deliver(msg.topic, msg);
      return;
    }
    if (msg.type === "log") {
      for (const topic of [...this.topics.keys()])
        if (topic.startsWith("logs:")) this.deliver(topic, msg);
      return;
    }
    if (msg.type === "dropped") {
      for (const topic of [...this.topics.keys()]) this.deliver(topic, msg);
      return;
    }
    const topic = TYPE_TOPIC[msg.type];
    if (topic) this.deliver(topic, msg);
  }
}

export const clusterSocket = new ClusterSocket();

/** The socket's status, re-rendering on change. */
export function useSocketStatus(): SocketStatus {
  return useSyncExternalStore(
    clusterSocket.onStatus,
    clusterSocket.getStatus,
    clusterSocket.getStatus
  );
}

/**
 * Listens to one or more topics for as long as the component is mounted
 * (null: none). `onMessage` may change between renders.
 */
export function useSocketTopic(
  topics: string | readonly string[] | null,
  onMessage: HubListener
): SocketStatus {
  const ref = useRef(onMessage);
  ref.current = onMessage;
  const list = topics === null ? [] : typeof topics === "string" ? [topics] : [...topics];
  const key = list.join("\n");
  useEffect(() => {
    if (!key) return;
    const offs = key.split("\n").map((t) => clusterSocket.subscribe(t, (m) => ref.current(m)));
    return () => offs.forEach((off) => off());
  }, [key]);
  return useSocketStatus();
}
