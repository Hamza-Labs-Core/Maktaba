import { describe, it, expect, vi } from "vitest";
import { act, renderHook, waitFor } from "@testing-library/react";
import { cachedAnswer } from "./pageCache";
import { usePageData } from "./usePageData";

vi.mock("./auth", () => ({ useOptionalAuth: () => ({ user: { id: "u1" } }) }));

function deferred<T>() {
  let resolve!: (v: T) => void;
  const promise = new Promise<T>((r) => (resolve = r));
  return { promise, resolve };
}

describe("usePageData", () => {
  it("a read that started before a change never overwrites it", async () => {
    const read = deferred<{ name: string }>();
    const { result } = renderHook(() => usePageData("account:profile", () => read.promise));
    act(() => result.current.setData({ name: "new" }));
    await act(async () => read.resolve({ name: "old" }));
    expect(result.current.data).toEqual({ name: "new" });
    expect(cachedAnswer("u1", "account:profile")).toEqual({ name: "new" });
  });

  it("a change made after the page moved to another key is dropped, not kept under the old one", async () => {
    const { result, rerender } = renderHook(
      ({ k }: { k: string }) => usePageData(k, () => Promise.resolve({ title: k })),
      { initialProps: { k: "video:a" } }
    );
    await waitFor(() => expect(result.current.data).toEqual({ title: "video:a" }));
    const stale = result.current.setData;
    rerender({ k: "video:b" });
    await waitFor(() => expect(result.current.data).toEqual({ title: "video:b" }));
    act(() => stale((cur) => cur && { ...cur, title: "renamed a" }));
    expect(result.current.data).toEqual({ title: "video:b" });
    expect(cachedAnswer("u1", "video:a")).toEqual({ title: "video:a" });
  });
});
