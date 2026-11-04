import { describe, it, expect } from "vitest";
import { readOrder } from "./readOrder";

function deferred<T>() {
  let resolve!: (v: T) => void;
  const promise = new Promise<T>((r) => (resolve = r));
  return { promise, resolve };
}

describe("readOrder", () => {
  it("drops a read that began before a change", () => {
    const o = readOrder();
    const t = o.begin();
    o.changed();
    expect(o.fresh(t)).toBe(false);
    expect(o.fresh(o.begin())).toBe(true);
  });

  it("drops an answer older than one already applied, never a newer one", () => {
    const o = readOrder();
    const a = o.begin();
    const b = o.begin();
    expect(o.fresh(b)).toBe(true);
    expect(o.fresh(a)).toBe(false);
    // a later poll still applies: a busy page keeps updating
    expect(o.fresh(o.begin())).toBe(true);
  });

  it("applies no read while a save is under way, nor one that began before it ended", async () => {
    const o = readOrder();
    const save = deferred<string>();
    const saving = o.saving(() => save.promise);
    const during = o.begin();
    expect(o.fresh(during)).toBe(false);
    save.resolve("ok");
    await expect(saving).resolves.toBe("ok");
    expect(o.fresh(during)).toBe(false);
    expect(o.fresh(o.begin())).toBe(true);
  });

  it("a save that fails still makes older reads stale, and the failure is the caller's", async () => {
    const o = readOrder();
    const t = o.begin();
    await expect(o.saving(() => Promise.reject(new Error("no")))).rejects.toThrow("no");
    expect(o.fresh(t)).toBe(false);
  });
});
