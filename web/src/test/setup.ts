// Vitest global setup: registers @testing-library/jest-dom matchers,
// auto-cleans the React tree between tests, and installs a real
// in-memory localStorage (the jsdom build wired here exposes only a
// partial Storage — getItem/setItem but no removeItem/clear — which
// breaks both test isolation and any code path that calls them).
import "@testing-library/jest-dom/vitest";
import { afterEach, beforeEach } from "vitest";
import { cleanup } from "@testing-library/react";

class MemoryStorage implements Storage {
  private map = new Map<string, string>();
  get length(): number {
    return this.map.size;
  }
  clear(): void {
    this.map.clear();
  }
  getItem(key: string): string | null {
    return this.map.has(key) ? this.map.get(key)! : null;
  }
  key(index: number): string | null {
    return [...this.map.keys()][index] ?? null;
  }
  removeItem(key: string): void {
    this.map.delete(key);
  }
  setItem(key: string, value: string): void {
    this.map.set(key, String(value));
  }
}

Object.defineProperty(globalThis, "localStorage", {
  configurable: true,
  value: new MemoryStorage(),
});

// No real sockets in unit tests: jsdom's WebSocket would dial localhost.
// Without one the cluster socket reports "unavailable" and the pages use
// their SSE / REST fallbacks; socket tests stub a fake WebSocket.
Object.defineProperty(globalThis, "WebSocket", {
  configurable: true,
  writable: true,
  value: undefined,
});

beforeEach(() => {
  localStorage.clear();
});

afterEach(() => {
  cleanup();
});
