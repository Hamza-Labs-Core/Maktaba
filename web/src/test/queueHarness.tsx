// Shared helpers for the queue page / component tests.
import type { ReactElement } from "react";
import { render } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { I18nProvider } from "../lib/i18n";

type Table = Record<string, unknown | ((url: string) => unknown)>;

/** Wires a vi.fn() api.get to a path -> payload table (query string ignored). */
export function routeGet(
  get: { mockImplementation: (f: (url: string) => Promise<unknown>) => unknown },
  table: Table
) {
  get.mockImplementation((url: string) => {
    const path = url.split("?")[0];
    if (!(path in table)) return Promise.reject(new Error(`unmocked GET ${url}`));
    const v = table[path];
    return Promise.resolve(typeof v === "function" ? (v as (u: string) => unknown)(url) : v);
  });
}

export function renderAt(ui: ReactElement, path: string, route = path) {
  return render(
    <I18nProvider>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path={route} element={ui} />
          <Route path="*" element={<p>elsewhere</p>} />
        </Routes>
      </MemoryRouter>
    </I18nProvider>
  );
}

/** In-memory SSE hub for vi.mock("../lib/ws"). */
export function makeHub() {
  const channels = new Map<string, Set<(ev: unknown) => void>>();
  return {
    subscribe: (ch: string, fn: (ev: unknown) => void) => {
      if (!channels.has(ch)) channels.set(ch, new Set());
      channels.get(ch)!.add(fn);
      return () => channels.get(ch)?.delete(fn);
    },
    emit: (ch: string, ev: unknown) => channels.get(ch)?.forEach((f) => f(ev)),
    count: (ch: string) => channels.get(ch)?.size ?? 0,
    reset: () => channels.clear(),
  };
}
