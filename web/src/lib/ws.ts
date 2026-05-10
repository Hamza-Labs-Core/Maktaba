// WebSocket fan-in (consumed by ProcessingQueue, VideoDetail, etc.).
//
// Subscriptions live in this module so each page doesn't open its own
// socket. The connection auto-reconnects with exponential backoff up to
// 30 s.

type Listener<T> = (msg: T) => void;

const URL_PATH = '/ws/v1/events';

export interface WSMessage {
  type: string;
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  [key: string]: any;
}

class WSClient {
  private ws: WebSocket | null = null;
  private listeners = new Set<Listener<WSMessage>>();
  private backoff = 1000;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private shouldRun = false;

  start(): void {
    this.shouldRun = true;
    this.connect();
  }

  stop(): void {
    this.shouldRun = false;
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
    this.ws?.close();
    this.ws = null;
  }

  subscribe(fn: Listener<WSMessage>): () => void {
    this.listeners.add(fn);
    if (!this.ws && this.shouldRun) this.connect();
    return () => {
      this.listeners.delete(fn);
    };
  }

  private connect(): void {
    if (!this.shouldRun) return;
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
    const url = `${proto}//${location.host}${URL_PATH}`;
    try {
      this.ws = new WebSocket(url);
    } catch (e) {
      this.scheduleReconnect();
      return;
    }
    this.ws.addEventListener('open', () => {
      this.backoff = 1000;
    });
    this.ws.addEventListener('message', (ev) => {
      try {
        const msg = JSON.parse(ev.data) as WSMessage;
        this.listeners.forEach((fn) => fn(msg));
      } catch {
        // ignore non-JSON frames
      }
    });
    this.ws.addEventListener('close', () => {
      this.ws = null;
      this.scheduleReconnect();
    });
    this.ws.addEventListener('error', () => {
      this.ws?.close();
    });
  }

  private scheduleReconnect(): void {
    if (!this.shouldRun) return;
    this.reconnectTimer = setTimeout(() => this.connect(), this.backoff);
    this.backoff = Math.min(this.backoff * 2, 30_000);
  }
}

export const ws = new WSClient();
