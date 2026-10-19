// A fake WebSocket for the cluster hub tests: records what the page sends
// and lets a test open, feed and drop the connection. No real sockets.
import { act } from "@testing-library/react";

export class FakeSocket {
  static all: FakeSocket[] = [];
  static CONNECTING = 0;
  static OPEN = 1;
  static CLOSED = 3;
  readyState = 0;
  sent: Record<string, unknown>[] = [];
  onopen: ((e: unknown) => void) | null = null;
  onmessage: ((e: { data: string }) => void) | null = null;
  onclose: ((e: unknown) => void) | null = null;
  onerror: ((e: unknown) => void) | null = null;
  constructor(public url: string) {
    FakeSocket.all.push(this);
  }
  static get last(): FakeSocket {
    return FakeSocket.all[FakeSocket.all.length - 1];
  }
  send(s: string) {
    this.sent.push(JSON.parse(s) as Record<string, unknown>);
  }
  close() {
    this.readyState = 3;
  }
  open() {
    act(() => {
      this.readyState = 1;
      this.onopen?.({});
    });
  }
  recv(msg: unknown) {
    act(() => {
      this.onmessage?.({ data: JSON.stringify(msg) });
    });
  }
  drop() {
    act(() => {
      this.readyState = 3;
      this.onclose?.({});
    });
  }
  /** every topic this socket was asked to subscribe to, in order */
  subscribed(): string[] {
    return this.sent.filter((m) => m.op === "subscribe").flatMap((m) => m.topics as string[]);
  }
  unsubscribed(): string[] {
    return this.sent.filter((m) => m.op === "unsubscribe").flatMap((m) => m.topics as string[]);
  }
}
