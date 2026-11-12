// Epic 57 story 57.06 on the web: what this tab remembers having asked a parent
// for. A mark is one account's, counts until its request expires, and is dropped
// when the request is answered.
import { describe, it, expect, beforeEach, vi, afterEach } from "vitest";
import { act, renderHook } from "@testing-library/react";
import {
  ASKED_TIME,
  askedUntil,
  forgetAsked,
  noteAsked,
  useAsked,
  wasAsked,
} from "./parentalAsked";

const NOW = Date.parse("2026-11-12T10:00:00Z");
const at = (ms: number) => new Date(NOW + ms).toISOString();

beforeEach(() => {
  sessionStorage.clear();
});
afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

describe("what was asked", () => {
  it("counts until the request's own expiry", () => {
    noteAsked("u1", "video:v1", at(60_000));
    expect(wasAsked("u1", "video:v1", NOW)).toBe(true);
    expect(askedUntil("u1", "video:v1", NOW)).toBe(NOW + 60_000);
    expect(wasAsked("u1", "video:v1", NOW + 60_000)).toBe(false);
    expect(wasAsked("u1", "video:v2", NOW)).toBe(false);
  });

  it("is kept per account", () => {
    noteAsked("u1", ASKED_TIME, at(60_000));
    expect(wasAsked("u2", ASKED_TIME, NOW)).toBe(false);
    expect(wasAsked(null, ASKED_TIME, NOW)).toBe(false);
  });

  it("with no expiry from the home counts for the tab's life", () => {
    noteAsked("u1", "video:v1", "");
    expect(askedUntil("u1", "video:v1", NOW)).toBe(Infinity);
  });

  it("is dropped when answered, leaving the others", () => {
    noteAsked("u1", "video:v1", at(60_000));
    noteAsked("u1", ASKED_TIME, at(60_000));
    forgetAsked("u1", ASKED_TIME);
    expect(wasAsked("u1", ASKED_TIME, NOW)).toBe(false);
    expect(wasAsked("u1", "video:v1", NOW)).toBe(true);
  });

  it("an unreadable store is no mark at all", () => {
    sessionStorage.setItem("mkt:parental:asked:u1", "not json");
    expect(wasAsked("u1", "video:v1", NOW)).toBe(false);
    sessionStorage.setItem("mkt:parental:asked:u1", '["video:v1"]');
    expect(wasAsked("u1", "video:v1", NOW)).toBe(false);
  });

  it("lives for the page when sessionStorage cannot be used", () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    noteAsked("u9", "video:v1", at(60_000));
    expect(wasAsked("u9", "video:v1", NOW)).toBe(true);
    forgetAsked("u9", "video:v1");
    expect(wasAsked("u9", "video:v1", NOW)).toBe(false);
  });
});

describe("useAsked", () => {
  it("follows a mark noted or dropped elsewhere, and the account and the thing", () => {
    const { result, rerender } = renderHook(({ user, what }) => useAsked(user, what), {
      initialProps: { user: "u1", what: "video:v1" },
    });
    expect(result.current).toBe(false);
    act(() => noteAsked("u1", "video:v1", new Date(Date.now() + 60_000).toISOString()));
    expect(result.current).toBe(true);
    rerender({ user: "u1", what: "video:v2" });
    expect(result.current).toBe(false);
    rerender({ user: "u2", what: "video:v1" });
    expect(result.current).toBe(false);
    rerender({ user: "u1", what: "video:v1" });
    expect(result.current).toBe(true);
    act(() => forgetAsked("u1", "video:v1"));
    expect(result.current).toBe(false);
  });

  it("stops at the request's expiry without being asked", () => {
    vi.useFakeTimers();
    noteAsked("u1", ASKED_TIME, new Date(Date.now() + 60_000).toISOString());
    const { result } = renderHook(() => useAsked("u1", ASKED_TIME));
    expect(result.current).toBe(true);
    act(() => vi.advanceTimersByTime(59_000));
    expect(result.current).toBe(true);
    act(() => vi.advanceTimersByTime(2_000));
    expect(result.current).toBe(false);
  });
});
