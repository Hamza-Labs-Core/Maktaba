// The service worker itself (public/sw.js), run with a fake worker scope, Cache
// Storage and network. Its shell was cache-first under a build stamp that never
// changed, so a browser kept the first shell it saw for ever: after an update that
// shell named the old bundle's script, the server answered the missing script with
// the page, and the app (the Mac app's window above all) stayed blank.
import { describe, it, expect, vi } from "vitest";
import src from "../../public/sw.js?raw";
import { stampServiceWorker } from "./swStamp";

type Listener = (event: Record<string, unknown>) => void;

function loadWorker(network: (url: string) => Promise<Response>, source: string = src) {
  const listeners: Record<string, Listener> = {};
  const stores = new Map<string, Map<string, Response>>();
  const keyOf = (k: unknown) => (typeof k === "string" ? k : new URL((k as Request).url).pathname);
  const caches = {
    open: vi.fn(async (name: string) => {
      if (!stores.has(name)) stores.set(name, new Map());
      const store = stores.get(name)!;
      return {
        put: async (k: unknown, r: Response) => void store.set(keyOf(k), r),
        addAll: async (paths: string[]) => {
          for (const p of paths) store.set(p, await network(p));
        },
      };
    }),
    match: vi.fn(async (k: unknown) => {
      for (const s of stores.values()) {
        const hit = s.get(keyOf(k));
        if (hit) return hit.clone();
      }
      return undefined;
    }),
    keys: vi.fn(async () => [...stores.keys()]),
    delete: vi.fn(async (name: string) => stores.delete(name)),
  };
  const self = {
    addEventListener: (type: string, fn: Listener) => void (listeners[type] = fn),
    skipWaiting: vi.fn(),
    clients: { claim: vi.fn(async () => undefined) },
  };
  const fetchFn = vi.fn((req: Request | string) =>
    network(typeof req === "string" ? req : new URL(req.url).pathname)
  );
  new Function("self", "caches", "fetch", source)(self, caches, fetchFn);
  return { listeners, stores, caches, self, fetchFn };
}

async function dispatchFetch(
  w: ReturnType<typeof loadWorker>,
  path: string,
  mode: RequestMode = "no-cors"
): Promise<Response | undefined> {
  let answer: Promise<Response> | undefined;
  const waits: Promise<unknown>[] = [];
  const request = {
    url: "http://127.0.0.1:8088" + path,
    method: "GET",
    mode,
  } as unknown as Request;
  w.listeners.fetch({
    request,
    respondWith: (p: Promise<Response>) => void (answer = p),
    waitUntil: (p: Promise<unknown>) => void waits.push(p),
  });
  const res = answer ? await answer : undefined;
  await Promise.all(waits);
  return res;
}

const page = (script: string) =>
  new Response(`<script type="module" src="/assets/${script}"></script>`, { status: 200 });

describe("sw.js", () => {
  it("serves the current build's page, not the first one it cached", async () => {
    let build = "index-old.js";
    const w = loadWorker(async () => page(build));
    await new Promise<void>((done) => {
      w.listeners.install({ waitUntil: (p: Promise<unknown>) => void p.then(() => done()) });
    });
    expect(w.self.skipWaiting).toHaveBeenCalled();
    build = "index-new.js"; // an update
    const res = await dispatchFetch(w, "/", "navigate");
    expect(await res!.text()).toContain("index-new.js");
    const routed = await dispatchFetch(w, "/library/42", "navigate");
    expect(await routed!.text()).toContain("index-new.js");
  });

  it("falls back to the cached page offline", async () => {
    let online = true;
    const w = loadWorker(async () => {
      if (!online) throw new TypeError("offline");
      return page("index-new.js");
    });
    await dispatchFetch(w, "/", "navigate"); // cached on the way
    online = false;
    const res = await dispatchFetch(w, "/library/42", "navigate");
    expect(await res!.text()).toContain("index-new.js");
  });

  it("leaves the bundle, the API and live TV to the network", async () => {
    const w = loadWorker(async () => new Response("x"));
    for (const p of ["/assets/index-new.js", "/api/videos", "/stream/x/index.m3u8"]) {
      expect(await dispatchFetch(w, p)).toBeUndefined();
    }
  });

  it("on activate deletes every cache but this build's shell, then takes the pages", async () => {
    // the worker as a build stamps it (vite.config.ts): each release its own shell
    const w = loadWorker(
      async () => new Response("x"),
      stampServiceWorker(src, "0.3.2-abcdef012345")
    );
    for (const name of [
      "mkt-shell-v2",
      "mkt-shell-v3",
      "mkt-shell-0.3.1-0123456789ab",
      "mkt-api-v1",
    ]) {
      await w.caches.open(name);
    }
    await w.caches.open("mkt-shell-0.3.2-abcdef012345");
    await new Promise<void>((done) => {
      w.listeners.activate({ waitUntil: (p: Promise<unknown>) => void p.then(() => done()) });
    });
    expect([...w.stores.keys()]).toEqual(["mkt-shell-0.3.2-abcdef012345"]);
    expect(w.self.clients.claim).toHaveBeenCalled();
  });
});
