// The service worker itself (public/sw.js), run with a fake worker scope, Cache
// Storage and network. Its shell was cache-first under a build stamp that never
// changed, so a browser kept the first shell it saw for ever: after an update that
// shell named the old bundle's script, the server answered the missing script with
// the page, and the app (the Mac app's window above all) stayed blank.
import { describe, it, expect, vi } from "vitest";
import src from "../../public/sw.js?raw";
import { stampServiceWorker } from "./swStamp";

type Listener = (event: Record<string, unknown>) => void;

function loadWorker(
  network: (url: string) => Promise<Response>,
  source: string = src,
  // the profile's Cache Storage: one worker after another shares it
  stores = new Map<string, Map<string, Response>>()
) {
  const listeners: Record<string, Listener> = {};
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

/** A worker's install, then its activation (each waitUntil awaited). */
async function installAndActivate(w: ReturnType<typeof loadWorker>) {
  for (const phase of ["install", "activate"]) {
    await new Promise<void>((done) => {
      w.listeners[phase]({ waitUntil: (p: Promise<unknown>) => void p.then(() => done()) });
    });
  }
}

// The worker releases before 0.3.4 shipped (cache-first, every page and file; no takeover
// of its own): it kept the first page it cached for ever.
const CACHE_FIRST_WORKER = `
const SHELL_CACHE = "mkt-shell-v2";
self.addEventListener("install", (event) => {
  event.waitUntil(caches.open(SHELL_CACHE).then((c) => c.addAll(["/", "/index.html"])));
});
self.addEventListener("fetch", (event) => {
  const req = event.request;
  if (req.method !== "GET") return;
  if (new URL(req.url).pathname.startsWith("/api/")) return;
  event.respondWith(caches.match(req).then((cached) => cached || fetch(req)));
});
`;

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

  // The app's window keeps its profile (and its workers) across app updates: a worker an
  // older release installed still controls it when the updated server answers.
  describe("after an older release's worker", () => {
    it("takes over from a cache-first one and drops the page it kept", async () => {
      let build = "index-old.js";
      let online = true;
      const network = async () => {
        if (!online) throw new TypeError("offline");
        return page(build);
      };
      const stores = new Map<string, Map<string, Response>>();
      const old = loadWorker(network, CACHE_FIRST_WORKER, stores);
      await new Promise<void>((done) => {
        old.listeners.install({ waitUntil: (p: Promise<unknown>) => void p.then(() => done()) });
      });
      build = "index-new.js"; // the update
      // the older worker answers with the page it kept: it names a bundle the server no
      // longer has (the blank window)
      const stale = await dispatchFetch(old, "/", "navigate");
      expect(await stale!.text()).toContain("index-old.js");
      // the server's worker (the browser's update check finds it) takes over at once and
      // deletes the older cache
      const now = loadWorker(network, stampServiceWorker(src, "0.3.20-abcdef012345"), stores);
      await installAndActivate(now);
      expect(now.self.skipWaiting).toHaveBeenCalled();
      expect(now.self.clients.claim).toHaveBeenCalled();
      expect([...stores.keys()]).toEqual(["mkt-shell-0.3.20-abcdef012345"]);
      const fresh = await dispatchFetch(now, "/library/42", "navigate");
      expect(await fresh!.text()).toContain("index-new.js");
      // offline it falls back to its own page, never the older one
      online = false;
      const offline = await dispatchFetch(now, "/", "navigate");
      expect(await offline!.text()).toContain("index-new.js");
    });

    it("an older network-first one answers with the server's page", async () => {
      // every release since 0.3.4 ships this shell, under a stamp of its own
      let build = "index-old.js";
      const stores = new Map<string, Map<string, Response>>();
      const network = async () => page(build);
      const old = loadWorker(network, stampServiceWorker(src, "0.3.17-0123456789ab"), stores);
      await installAndActivate(old);
      await dispatchFetch(old, "/", "navigate");
      build = "index-new.js"; // the update
      const res = await dispatchFetch(old, "/library/42", "navigate");
      expect(await res!.text()).toContain("index-new.js");
      // and the server's worker that replaces it keeps only its own cache
      const now = loadWorker(network, stampServiceWorker(src, "0.3.20-abcdef012345"), stores);
      await installAndActivate(now);
      expect([...stores.keys()]).toEqual(["mkt-shell-0.3.20-abcdef012345"]);
    });
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
