import { afterEach, describe, expect, it, vi } from "vitest";

const { get, put } = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn() }));
vi.mock("./api", async () => {
  const actual = await vi.importActual<typeof import("./api")>("./api");
  return { ...actual, api: { ...actual.api, get, put } };
});

import { loadPins, resetPinsStore, savePins } from "./pins";

const A = { id: "a", name: "A" };
const B = { id: "b", name: "B" };
const C = { id: "c", name: "C" };
const doc = (list: Array<{ id: string; name: string }>) => ({
  library_ids: list.map((p) => p.id),
  items: list.map((p, i) => ({ ...p, position: i })),
});

afterEach(() => {
  resetPinsStore();
  get.mockReset();
  put.mockReset();
});

describe("pins store", () => {
  it("sends saves one after another, so two quick moves never overlap", async () => {
    get.mockResolvedValue(doc([A, B]));
    await loadPins("u1");
    const order: string[] = [];
    let release!: () => void;
    put.mockImplementationOnce(async (_p: string, body: { library_ids: string[] }) => {
      order.push(`start ${body.library_ids.join("")}`);
      await new Promise<void>((r) => (release = r));
      order.push("end 1");
      return doc([B, A]);
    });
    put.mockImplementationOnce(async (_p: string, body: { library_ids: string[] }) => {
      order.push(`start ${body.library_ids.join("")}`);
      return doc([B, A, C]);
    });
    const first = savePins([B, A]);
    const second = savePins([B, A, C]);
    await vi.waitFor(() => expect(order).toEqual(["start ba"]));
    release();
    expect(await second).toEqual([B, A, C]);
    await first;
    expect(order).toEqual(["start ba", "end 1", "start bac"]);
  });

  it("a refused save goes back to the list the server last confirmed", async () => {
    get.mockResolvedValue(doc([A]));
    await loadPins("u1");
    put.mockResolvedValueOnce(doc([A, B]));
    await savePins([A, B]);
    put.mockRejectedValueOnce(new Error("refused"));
    await expect(savePins([A, B, C])).rejects.toThrow("refused");
    expect(await loadPins("u1")).toEqual([A, B]);
  });

  it("never shows another user's pins while the new user's load", async () => {
    get.mockResolvedValueOnce(doc([A]));
    await loadPins("u1");
    let answer!: (d: unknown) => void;
    get.mockReturnValueOnce(new Promise((r) => (answer = r)));
    const next = loadPins("u2");
    // u2 asks again while its first read is out: it waits for its own list
    const again = loadPins("u2");
    answer(doc([C]));
    expect(await next).toEqual([C]);
    expect(await again).toEqual([C]);
  });
});
