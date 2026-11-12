// Epic 57: what the web knows of the signed-in account's parental limit, and how it
// asks the home about time and about a channel.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, render, renderHook, screen, waitFor } from "@testing-library/react";

const { get } = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock("./api", async () => {
  const actual = await vi.importActual<typeof import("./api")>("./api");
  return { ...actual, api: { ...actual.api, get } };
});

import { ApiError } from "./api";
import {
  ACCESS_MIN_MS,
  ACCESS_RETRY_MS,
  ParentalProvider,
  TIME_POLL_MS,
  TIME_POLL_NEAR_MS,
  showsTimeLeft,
  useChannelAccess,
  useMyParental,
  useMyTime,
} from "./parental";
import { TestAuth, memberUser } from "../test/pageHarness";
import type { User } from "./auth";

function Probe() {
  const p = useMyParental();
  return (
    <p data-testid="probe">
      {JSON.stringify({
        a: p.answered,
        r: p.restricted,
        t: p.maxTier,
        c: p.children.map((c) => c.username),
      })}
    </p>
  );
}

function mount(user: User | null) {
  return render(
    <TestAuth user={user}>
      <ParentalProvider>
        <Probe />
      </ParentalProvider>
    </TestAuth>
  );
}

const probe = () => JSON.parse(screen.getByTestId("probe").textContent ?? "{}") as unknown;

beforeEach(() => {
  get.mockReset();
});
afterEach(() => {
  vi.useRealTimers();
});

describe("ParentalProvider", () => {
  it("nobody is restricted before the home answers, or outside the provider", () => {
    get.mockReturnValue(new Promise(() => {}));
    mount(memberUser);
    expect(probe()).toEqual({ a: false, r: false, t: null, c: [] });
    const { result } = renderHook(() => useMyParental());
    expect(result.current.restricted).toBe(false);
  });

  it("asks nothing while signed out", async () => {
    mount(null);
    await act(async () => {});
    expect(get).not.toHaveBeenCalled();
  });

  it("a restricted account: its limit, and it looks after no one (not asked)", async () => {
    get.mockResolvedValue({ restricted: true, max_tier: 9 });
    mount(memberUser);
    await waitFor(() => expect(probe()).toEqual({ a: true, r: true, t: 9, c: [] }));
    expect(get).toHaveBeenCalledTimes(1);
    expect(get).toHaveBeenCalledWith("/api/me/parental");
  });

  it("a guardian: not restricted, with the children the home lists", async () => {
    get.mockImplementation((path: string) =>
      Promise.resolve(
        path === "/api/me/parental"
          ? { restricted: false }
          : { children: [{ id: "u1", username: "sara", max_tier: 13 }] }
      )
    );
    mount(memberUser);
    await waitFor(() => expect(probe()).toEqual({ a: true, r: false, t: null, c: ["sara"] }));
  });

  it("a home that does not answer leaves nobody restricted", async () => {
    get.mockRejectedValue(new ApiError({ type: "about:blank", title: "not found", status: 404 }));
    mount(memberUser);
    await act(async () => {});
    expect(probe()).toEqual({ a: false, r: false, t: null, c: [] });
  });

  it("another account starts clean", async () => {
    get.mockResolvedValue({ restricted: true, max_tier: 9 });
    const { rerender } = mount(memberUser);
    await waitFor(() => expect(probe()).toMatchObject({ r: true }));
    get.mockReturnValue(new Promise(() => {}));
    rerender(
      <TestAuth user={{ id: "u-other", username: "other", is_admin: false }}>
        <ParentalProvider>
          <Probe />
        </ParentalProvider>
      </TestAuth>
    );
    expect(probe()).toEqual({ a: false, r: false, t: null, c: [] });
  });
});

describe("useMyTime", () => {
  it("asks nothing while off", async () => {
    renderHook(() => useMyTime(false));
    await act(async () => {});
    expect(get).not.toHaveBeenCalled();
  });

  it("asks the home every 30 seconds, and every 10 near the end", async () => {
    vi.useFakeTimers();
    get.mockResolvedValue({ allowed: true, limited: true, remaining_seconds: 3000 });
    const { result } = renderHook(() => useMyTime(true));
    await act(async () => {});
    expect(result.current.time?.remainingSeconds).toBe(3000);
    expect(showsTimeLeft(result.current.time)).toBe(false);
    await act(async () => vi.advanceTimersByTime(TIME_POLL_MS - 100));
    expect(get).toHaveBeenCalledTimes(1);

    get.mockResolvedValue({ allowed: true, limited: true, remaining_seconds: 200 });
    await act(async () => vi.advanceTimersByTime(200));
    expect(get).toHaveBeenCalledTimes(2);
    expect(showsTimeLeft(result.current.time)).toBe(true);
    await act(async () => vi.advanceTimersByTime(TIME_POLL_NEAR_MS + 100));
    expect(get).toHaveBeenCalledTimes(3);
  });

  it("keeps the last answer when the home does not answer, and asks again", async () => {
    vi.useFakeTimers();
    get.mockResolvedValueOnce({ allowed: true, limited: true, remaining_seconds: 3000 });
    get.mockRejectedValue(new Error("offline"));
    const { result } = renderHook(() => useMyTime(true));
    await act(async () => {});
    await act(async () => vi.advanceTimersByTime(TIME_POLL_MS + 100));
    expect(result.current.time?.remainingSeconds).toBe(3000);
    await act(async () => vi.advanceTimersByTime(TIME_POLL_MS + 100));
    expect(get).toHaveBeenCalledTimes(3);
  });

  it("ask() asks now, and stops asking once unmounted", async () => {
    vi.useFakeTimers();
    get.mockResolvedValue({ allowed: false, limited: true, reason: "limit_reached" });
    const { result, unmount } = renderHook(() => useMyTime(true));
    await act(async () => {});
    await act(async () => result.current.ask());
    expect(get).toHaveBeenCalledTimes(2);
    unmount();
    await act(async () => vi.advanceTimersByTime(5 * TIME_POLL_MS));
    expect(get).toHaveBeenCalledTimes(2);
  });
});

describe("useChannelAccess", () => {
  it("asks nothing with no channel", async () => {
    const { result } = renderHook(() => useChannelAccess(null));
    await act(async () => {});
    expect(get).not.toHaveBeenCalled();
    expect(result.current).toEqual({ channelId: null, allowed: true, until: null });
  });

  it("asks again when the programme ends", async () => {
    vi.useFakeTimers();
    const until = new Date(Date.now() + 10 * 60_000).toISOString();
    get.mockResolvedValue({ restricted: true, allowed: false, until });
    const { result } = renderHook(() => useChannelAccess("c1"));
    await act(async () => {});
    expect(result.current).toEqual({ channelId: "c1", allowed: false, until });
    await act(async () => vi.advanceTimersByTime(9 * 60_000));
    expect(get).toHaveBeenCalledTimes(1);
    get.mockResolvedValue({ restricted: true, allowed: true, until: null });
    await act(async () => vi.advanceTimersByTime(62_000));
    expect(get).toHaveBeenCalledTimes(2);
    expect(result.current.allowed).toBe(true);
  });

  it("never asks sooner than five seconds after an answer, and keeps asking about a refusal", async () => {
    vi.useFakeTimers();
    get.mockResolvedValue({
      restricted: true,
      allowed: false,
      until: new Date(Date.now() - 1000).toISOString(),
    });
    renderHook(() => useChannelAccess("c1"));
    await act(async () => {});
    await act(async () => vi.advanceTimersByTime(ACCESS_MIN_MS - 100));
    expect(get).toHaveBeenCalledTimes(1);
    get.mockResolvedValue({ restricted: true, allowed: false, until: null });
    await act(async () => vi.advanceTimersByTime(200));
    expect(get).toHaveBeenCalledTimes(2);
    await act(async () => vi.advanceTimersByTime(ACCESS_RETRY_MS + 100));
    expect(get).toHaveBeenCalledTimes(3);
  });

  it("a failed call answers allowed, and another channel starts with no answer", async () => {
    get.mockRejectedValue(new Error("offline"));
    const { result, rerender } = renderHook(({ id }) => useChannelAccess(id), {
      initialProps: { id: "c1" },
    });
    await waitFor(() => expect(result.current.channelId).toBe("c1"));
    expect(result.current.allowed).toBe(true);
    get.mockReturnValue(new Promise(() => {}));
    rerender({ id: "c2" });
    expect(result.current.channelId).toBeNull();
  });
});
