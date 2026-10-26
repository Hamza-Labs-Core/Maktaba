import { describe, expect, it, vi } from "vitest";

import { WORKER_UPDATE_WAIT_MS, pageIsStale, reloadOnStaleChunks, watchTakeovers } from "./pwa";

describe("reloadOnStaleChunks", () => {
  it("reloads once when an older release's chunk is gone, not in a loop", () => {
    const target = new EventTarget();
    const reload = vi.fn();
    const store = new Map<string, string>();
    let clock = 1_000_000;
    reloadOnStaleChunks(
      {
        addEventListener: target.addEventListener.bind(target),
        location: { reload } as unknown as Location,
      } as unknown as Window,
      { getItem: (k) => store.get(k) ?? null, setItem: (k, v) => void store.set(k, v) },
      () => clock
    );
    target.dispatchEvent(new Event("vite:preloadError", { cancelable: true }));
    expect(reload).toHaveBeenCalledTimes(1);
    clock += 5_000;
    target.dispatchEvent(new Event("vite:preloadError", { cancelable: true }));
    expect(reload).toHaveBeenCalledTimes(1);
    clock += 120_000;
    target.dispatchEvent(new Event("vite:preloadError", { cancelable: true }));
    expect(reload).toHaveBeenCalledTimes(2);
  });
});

// The reload goes through the page's service worker, which may be an older release's (the
// app's window keeps its profile across updates): the worker is updated first, so the
// server's worker (which takes over at once and deletes the older caches) answers it.
describe("reloadOnStaleChunks under an older release's service worker", () => {
  function page(workers: Parameters<typeof reloadOnStaleChunks>[3], order: string[] = []) {
    const target = new EventTarget();
    const reload = vi.fn(() => void order.push("reload"));
    reloadOnStaleChunks(
      {
        addEventListener: target.addEventListener.bind(target),
        location: { reload } as unknown as Location,
      } as unknown as Window,
      null,
      () => 1_000_000,
      workers
    );
    const fail = () => target.dispatchEvent(new Event("vite:preloadError", { cancelable: true }));
    return { fail, reload };
  }
  const registration = (update: () => Promise<unknown>) => ({
    getRegistration: vi.fn(async () => ({ update }) as unknown as ServiceWorkerRegistration),
  });

  it("updates the worker, then reloads", async () => {
    const order: string[] = [];
    const p = page(
      registration(async () => void order.push("update")),
      order
    );
    p.fail();
    await vi.waitFor(() => expect(p.reload).toHaveBeenCalledTimes(1));
    expect(order).toEqual(["update", "reload"]);
  });

  it("reloads anyway when the update hangs, fails, or no worker is there", async () => {
    vi.useFakeTimers();
    try {
      const hung = page(registration(() => new Promise(() => {})));
      hung.fail();
      await vi.advanceTimersByTimeAsync(WORKER_UPDATE_WAIT_MS - 1);
      expect(hung.reload).not.toHaveBeenCalled();
      await vi.advanceTimersByTimeAsync(1);
      expect(hung.reload).toHaveBeenCalledTimes(1);

      const failed = page(registration(() => Promise.reject(new TypeError("offline"))));
      failed.fail();
      await vi.advanceTimersByTimeAsync(0);
      expect(failed.reload).toHaveBeenCalledTimes(1);

      const none = page({ getRegistration: vi.fn(async () => undefined) });
      none.fail();
      await vi.advanceTimersByTimeAsync(0);
      expect(none.reload).toHaveBeenCalledTimes(1);

      // a browser without service workers: at once
      const bare = page(null);
      bare.fail();
      expect(bare.reload).toHaveBeenCalledTimes(1);
    } finally {
      vi.useRealTimers();
    }
  });
});

/** A page whose entry script is `script`, as Vite writes index.html. */
function pageNaming(script: string): Document {
  return new DOMParser().parseFromString(
    `<html><head><script type="module" crossorigin src="/assets/${script}"></script></head></html>`,
    "text/html"
  );
}

const served = (script: string) => () =>
  Promise.resolve(
    new Response(`<script type="module" crossorigin src="/assets/${script}"></script>`, {
      status: 200,
    })
  );

describe("pageIsStale", () => {
  it("is stale when the server's page names another entry script", async () => {
    expect(await pageIsStale(pageNaming("index-old.js"), served("index-new.js"))).toBe(true);
  });

  it("is current when it names the same one, or says nothing when it cannot tell", async () => {
    expect(await pageIsStale(pageNaming("index-new.js"), served("index-new.js"))).toBe(false);
    const offline = () => Promise.reject(new TypeError("offline"));
    expect(await pageIsStale(pageNaming("index-old.js"), offline)).toBe(false);
    const down = () => Promise.resolve(new Response("down", { status: 502 }));
    expect(await pageIsStale(pageNaming("index-old.js"), down)).toBe(false);
    expect(
      await pageIsStale(
        new DOMParser().parseFromString("<p>x</p>", "text/html"),
        served("index-new.js")
      )
    ).toBe(false);
  });
});

describe("watchTakeovers", () => {
  // a new worker takes the page at once (sw.js skipWaiting): what the page does then
  function takeover(stale: boolean) {
    const sw = new EventTarget();
    const win = new EventTarget();
    const reload = vi.fn();
    const offered = vi.fn();
    win.addEventListener("mkt:sw-update", offered);
    watchTakeovers(
      sw as unknown as ServiceWorkerContainer,
      () => undefined,
      Object.assign(win, { location: { reload } }) as unknown as Window,
      () => Promise.resolve(stale)
    );
    sw.dispatchEvent(new Event("controllerchange"));
    return { reload, offered };
  }

  it("offers the reload to a page older than the server's release, never reloads it", async () => {
    const { reload, offered } = takeover(true);
    await vi.waitFor(() => expect(offered).toHaveBeenCalledTimes(1));
    expect(reload).not.toHaveBeenCalled();
  });

  it("says nothing to a page that runs the release already (a first visit, a fresh load)", async () => {
    const { reload, offered } = takeover(false);
    await new Promise((r) => setTimeout(r, 0));
    expect(offered).not.toHaveBeenCalled();
    expect(reload).not.toHaveBeenCalled();
  });

  it("reloads once when the takeover is the update the user accepted", async () => {
    vi.resetModules();
    const pwa = await import("./pwa");
    const postMessage = vi.fn();
    pwa.applyServiceWorkerUpdate({
      waiting: { postMessage },
    } as unknown as ServiceWorkerRegistration);
    expect(postMessage).toHaveBeenCalledWith({ type: "SKIP_WAITING" });
    const sw = new EventTarget();
    const reload = vi.fn();
    pwa.watchTakeovers(
      sw as unknown as ServiceWorkerContainer,
      () => undefined,
      Object.assign(new EventTarget(), { location: { reload } }) as unknown as Window,
      () => Promise.resolve(false)
    );
    sw.dispatchEvent(new Event("controllerchange"));
    sw.dispatchEvent(new Event("controllerchange"));
    expect(reload).toHaveBeenCalledTimes(1);
  });
});
