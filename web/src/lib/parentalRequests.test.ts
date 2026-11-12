// Epic 57 story 57.06 on the web: what became of a child's request is the home's
// word. The home is asked while the request waits and not when asking is no use;
// the tab's own mark only fills in until the home has answered.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, renderHook } from "@testing-library/react";

const { get } = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock("./api", async () => {
  const actual = await vi.importActual<typeof import("./api")>("./api");
  return { ...actual, api: { ...actual.api, get } };
});

import { ApiError } from "./api";
import type { AskRequest, MyRequest } from "./familyApi";
import { ASKED_TIME, noteAsked, wasAsked } from "./parentalAsked";
import {
  REQUEST_POLL_MS,
  REQUEST_POLL_STOP_MS,
  latestFor,
  recheckMyRequests,
  stateOf,
  useMyRequest,
} from "./parentalRequests";

const PATH = "/api/me/parental/requests";
const problem = (status: number) =>
  new ApiError({ type: "about:blank", title: "refused", status } as never);
const soon = (ms = 7 * 24 * 3600_000) => new Date(Date.now() + ms).toISOString();

/** A request as the home lists it. */
const row = (status: string, over: Record<string, unknown> = {}) => ({
  id: "q1",
  subject_kind: "video",
  subject_id: "v1",
  status,
  created_at: new Date(Date.now() - 60_000).toISOString(),
  expires_at: soon(),
  ...over,
});
/** The home's list from now on. */
const home = (...requests: unknown[]) => get.mockResolvedValue({ requests });
const asked = (over: Partial<AskRequest> = {}): AskRequest => ({
  id: "q1",
  childId: "u1",
  subjectKind: "video",
  subjectId: "v1",
  minutes: 0,
  status: "pending",
  createdAt: new Date().toISOString(),
  expiresAt: soon(),
  ...over,
});

const see = (state: "visible" | "hidden") => {
  Object.defineProperty(document, "visibilityState", { value: state, configurable: true });
  document.dispatchEvent(new Event("visibilitychange"));
};
/** Lets the answers that are on their way arrive. */
const settle = () => act(async () => {});
const pass = (ms: number) =>
  act(async () => {
    await vi.advanceTimersByTimeAsync(ms);
  });

const onApproved = vi.fn();
function follow(user: string | null = "u1", what = "video:v1", on = true) {
  return renderHook(({ user, what, on }) => useMyRequest(user, what, on, onApproved), {
    initialProps: { user, what, on },
  });
}

beforeEach(() => {
  get.mockReset();
  onApproved.mockReset();
  sessionStorage.clear();
  vi.useFakeTimers();
});
afterEach(() => {
  vi.useRealTimers();
  Reflect.deleteProperty(document, "visibilityState");
});

describe("reading the home's list", () => {
  const list = (...rows: Partial<MyRequest>[]) =>
    rows.map((r, i) => ({
      id: `q${i}`,
      subjectKind: "video",
      subjectId: "v1",
      minutes: 0,
      status: "pending" as const,
      createdAt: "",
      expiresAt: "",
      decidedAt: null,
      ...r,
    }));

  it("takes the newest request for the thing, and for time any request for time", () => {
    const all = list(
      { subjectId: "v2" },
      { subjectKind: "time", subjectId: "30", status: "denied" },
      { status: "approved" },
      { status: "denied" },
      { subjectKind: "time", subjectId: "15" }
    );
    expect(latestFor(all, "video:v1")?.id).toBe("q2");
    expect(latestFor(all, ASKED_TIME)?.id).toBe("q1");
    expect(latestFor(all, "series:v1")).toBeNull();
  });

  it("says what a request is to the page", () => {
    const now = Date.parse("2026-11-12T10:00:00Z");
    const [r] = list({ expiresAt: "2026-11-12T11:00:00Z" });
    expect(stateOf(null, "video:v1", false, now)).toBe("none");
    expect(stateOf(r, "video:v1", false, now)).toBe("pending");
    // waiting past its own end is over, whatever the row still says
    expect(stateOf(r, "video:v1", false, now + 3600_000)).toBe("expired");
    expect(stateOf({ ...r, status: "expired" }, "video:v1", false, now)).toBe("expired");
    expect(stateOf({ ...r, status: "denied" }, "video:v1", false, now + 7200_000)).toBe("denied");
    // an approval this page did not wait for is history
    expect(stateOf({ ...r, status: "approved" }, "video:v1", false, now)).toBe("none");
    expect(stateOf({ ...r, status: "approved" }, "video:v1", true, now)).toBe("approved");
    // a no to more time lasts its day
    const no = { ...r, subjectKind: "time", status: "denied" as const };
    expect(stateOf(no, ASKED_TIME, false, now)).toBe("denied");
    expect(stateOf(no, ASKED_TIME, false, now + 3600_000)).toBe("none");
  });
});

describe("what the home says", () => {
  it("nothing asked: nothing waits, and the home is asked once", async () => {
    home();
    const { result } = follow();
    await settle();
    expect(result.current.state).toBe("none");
    expect(get).toHaveBeenCalledWith(PATH);
    await pass(10 * REQUEST_POLL_MS);
    expect(get).toHaveBeenCalledTimes(1);
  });

  it("a waiting request is asked about every 15 seconds", async () => {
    home(row("pending"));
    const { result } = follow();
    await settle();
    expect(result.current.state).toBe("pending");
    await pass(REQUEST_POLL_MS - 100);
    expect(get).toHaveBeenCalledTimes(1);
    await pass(200);
    expect(get).toHaveBeenCalledTimes(2);
    await pass(REQUEST_POLL_MS);
    expect(get).toHaveBeenCalledTimes(3);
    expect(onApproved).not.toHaveBeenCalled();
  });

  it("a yes runs onApproved once, and the asking stops", async () => {
    home(row("pending"));
    const { result } = follow();
    await settle();
    home(row("approved", { decided_at: new Date().toISOString() }));
    await pass(REQUEST_POLL_MS);
    expect(result.current.state).toBe("approved");
    expect(onApproved).toHaveBeenCalledTimes(1);
    expect(wasAsked("u1", "video:v1")).toBe(false);
    await pass(10 * REQUEST_POLL_MS);
    expect(get).toHaveBeenCalledTimes(2);
    // asked again by a tap: the same yes is not acted on twice
    act(() => result.current.recheck());
    await settle();
    expect(onApproved).toHaveBeenCalledTimes(1);
  });

  it("a no is said, and the asking stops", async () => {
    home(row("pending"));
    const { result } = follow();
    await settle();
    home(row("denied"));
    await pass(REQUEST_POLL_MS);
    expect(result.current.state).toBe("denied");
    expect(onApproved).not.toHaveBeenCalled();
    expect(wasAsked("u1", "video:v1")).toBe(false);
    await pass(10 * REQUEST_POLL_MS);
    expect(get).toHaveBeenCalledTimes(2);
  });

  it("an expired request is over, and the asking stops", async () => {
    home(row("pending"));
    const { result } = follow();
    await settle();
    home(row("expired"));
    await pass(REQUEST_POLL_MS);
    expect(result.current.state).toBe("expired");
    await pass(10 * REQUEST_POLL_MS);
    expect(get).toHaveBeenCalledTimes(2);
  });

  it("a request reaching its own end is asked about then", async () => {
    home(row("pending", { expires_at: soon(5_000) }));
    const { result } = follow();
    await settle();
    expect(result.current.state).toBe("pending");
    home(row("expired"));
    await pass(5_100);
    expect(get).toHaveBeenCalledTimes(2);
    expect(result.current.state).toBe("expired");
  });

  it("a yes given before this page saw the request wait is history: nothing is opened", async () => {
    home(row("approved"));
    const { result } = follow();
    await settle();
    expect(result.current.state).toBe("none");
    expect(onApproved).not.toHaveBeenCalled();
    await pass(10 * REQUEST_POLL_MS);
    expect(get).toHaveBeenCalledTimes(1);
  });

  it("a no from before is still said when the page opens", async () => {
    home(row("denied"));
    const { result } = follow();
    await settle();
    expect(result.current.state).toBe("denied");
  });

  it("the newest request for the thing counts: asked again after a no", async () => {
    home(row("pending", { id: "q2" }), row("denied"));
    const { result } = follow();
    await settle();
    expect(result.current.state).toBe("pending");
  });

  it("more time follows the request for time, whatever minutes it asked", async () => {
    home(row("pending"), row("pending", { id: "t1", subject_kind: "time", subject_id: "30" }));
    const { result } = follow("u1", ASKED_TIME);
    await settle();
    expect(result.current.state).toBe("pending");
    home(row("pending"), row("approved", { id: "t1", subject_kind: "time", subject_id: "30" }));
    await pass(REQUEST_POLL_MS);
    expect(result.current.state).toBe("approved");
    expect(onApproved).toHaveBeenCalledTimes(1);
  });
});

describe("when asking is no use", () => {
  it("does not ask while the tab is hidden, and asks what came due when it shows", async () => {
    home(row("pending"));
    follow();
    await settle();
    act(() => see("hidden"));
    await pass(10 * REQUEST_POLL_MS);
    expect(get).toHaveBeenCalledTimes(1);
    await act(async () => see("visible"));
    expect(get).toHaveBeenCalledTimes(2);
    await pass(REQUEST_POLL_MS + 100);
    expect(get).toHaveBeenCalledTimes(3);
  });

  it("a yes given while the tab was hidden is acted on when it shows", async () => {
    home(row("pending"));
    const { result } = follow();
    await settle();
    act(() => see("hidden"));
    home(row("approved"));
    await pass(3 * REQUEST_POLL_MS);
    expect(onApproved).not.toHaveBeenCalled();
    await act(async () => see("visible"));
    expect(onApproved).toHaveBeenCalledTimes(1);
    expect(result.current.state).toBe("approved");
  });

  it("stops after 30 minutes with no change; Check again asks and starts it again", async () => {
    home(row("pending"));
    const { result } = follow();
    await settle();
    await pass(REQUEST_POLL_STOP_MS + REQUEST_POLL_MS);
    const asks = get.mock.calls.length;
    expect(asks).toBeGreaterThan(100);
    expect(asks).toBeLessThanOrEqual(REQUEST_POLL_STOP_MS / REQUEST_POLL_MS + 2);
    await pass(20 * REQUEST_POLL_MS);
    expect(get).toHaveBeenCalledTimes(asks);
    // still waiting, as far as the home last said
    expect(result.current.state).toBe("pending");
    act(() => result.current.recheck());
    await settle();
    expect(get).toHaveBeenCalledTimes(asks + 1);
    await pass(REQUEST_POLL_MS + 100);
    expect(get).toHaveBeenCalledTimes(asks + 2);
  });

  it("Check again anywhere on the page asks for every request followed, acting on nothing", async () => {
    home(row("pending"));
    const { result } = follow();
    await settle();
    home(row("approved"));
    act(() => recheckMyRequests());
    await settle();
    expect(get).toHaveBeenCalledTimes(2);
    // the tap's owner acts itself
    expect(onApproved).not.toHaveBeenCalled();
    expect(result.current.state).toBe("approved");
  });

  it("stops when the page goes away", async () => {
    home(row("pending"));
    const { unmount } = follow();
    await settle();
    unmount();
    await pass(10 * REQUEST_POLL_MS);
    expect(get).toHaveBeenCalledTimes(1);
    recheckMyRequests();
    expect(get).toHaveBeenCalledTimes(1);
  });

  it("an account that is not restricted is never asked about", async () => {
    home(row("pending"));
    const { result } = follow("u1", "video:v1", false);
    await settle();
    expect(get).not.toHaveBeenCalled();
    expect(result.current.state).toBe("none");
  });

  it("no answer keeps what is known and asks again", async () => {
    home(row("pending"));
    const { result } = follow();
    await settle();
    get.mockRejectedValue(new Error("offline"));
    await pass(REQUEST_POLL_MS);
    expect(result.current.state).toBe("pending");
    home(row("denied"));
    await pass(REQUEST_POLL_MS);
    expect(result.current.state).toBe("denied");
  });
});

describe("the tab's own mark", () => {
  it("shows waiting before the home answers, and the home's word replaces it", async () => {
    noteAsked("u1", "video:v1", soon());
    home(row("denied"));
    const { result } = follow();
    // first paint: what this tab remembers
    expect(result.current.state).toBe("pending");
    await settle();
    expect(result.current.state).toBe("denied");
    expect(wasAsked("u1", "video:v1")).toBe(false);
  });

  it("a mark the home knows nothing of is dropped", async () => {
    noteAsked("u1", "video:v1", soon());
    home();
    const { result } = follow();
    await settle();
    expect(result.current.state).toBe("none");
    expect(wasAsked("u1", "video:v1")).toBe(false);
  });

  it("a marked request the home has since approved is opened", async () => {
    noteAsked("u1", "video:v1", soon());
    home(row("approved"));
    const { result } = follow();
    await settle();
    expect(onApproved).toHaveBeenCalledTimes(1);
    expect(result.current.state).toBe("approved");
  });

  it("is written from the home's answer: a request made elsewhere waits here too", async () => {
    home(row("pending"));
    const { result } = follow();
    await settle();
    expect(result.current.state).toBe("pending");
    expect(wasAsked("u1", "video:v1")).toBe(true);
  });

  it("an ask just made waits at once and is then asked about", async () => {
    home();
    const { result } = follow();
    await settle();
    act(() => result.current.sent(asked()));
    expect(result.current.state).toBe("pending");
    expect(wasAsked("u1", "video:v1")).toBe(true);
    home(row("denied"));
    await pass(REQUEST_POLL_MS);
    expect(result.current.state).toBe("denied");
  });

  it("an answer asked for before the ask was made does not undo it", async () => {
    let answer: (v: unknown) => void = () => {};
    get.mockReturnValue(new Promise((r) => (answer = r)));
    const { result } = follow();
    act(() => result.current.sent(asked()));
    // the list read before the request existed arrives now
    await act(async () => answer({ requests: [] }));
    expect(result.current.state).toBe("pending");
    expect(wasAsked("u1", "video:v1")).toBe(true);
  });

  it("a home without the route leaves the mark as all that is known, and is not asked again", async () => {
    get.mockRejectedValue(problem(404));
    const { result } = follow();
    await settle();
    expect(result.current.state).toBe("none");
    act(() => result.current.sent(asked({ expiresAt: soon(60_000) })));
    expect(result.current.state).toBe("pending");
    await pass(3 * REQUEST_POLL_MS);
    expect(get).toHaveBeenCalledTimes(1);
    // the mark's own expiry still ends the wait
    await pass(60_000);
    expect(result.current.state).toBe("none");
  });
});

describe("another account in the same tab", () => {
  it("never sees the first one's request, mark or answer on its way", async () => {
    noteAsked("u1", "video:v1", soon());
    let answer: (v: unknown) => void = () => {};
    get.mockReturnValueOnce(new Promise((r) => (answer = r)));
    const { result, rerender } = follow("u1");
    expect(result.current.state).toBe("pending");

    // her sister signs in before the home has answered for the first child
    get.mockResolvedValue({ requests: [] });
    rerender({ user: "u2", what: "video:v1", on: true });
    expect(result.current.state).toBe("none");
    await act(async () => answer({ requests: [row("approved")] }));
    expect(result.current.state).toBe("none");
    expect(onApproved).not.toHaveBeenCalled();
    expect(wasAsked("u2", "video:v1")).toBe(false);
    // and the first child's mark is still hers
    expect(wasAsked("u1", "video:v1")).toBe(true);
  });

  it("starts from the home's word for itself", async () => {
    home(row("denied"));
    const { result, rerender } = follow("u1");
    await settle();
    expect(result.current.state).toBe("denied");
    home();
    rerender({ user: "u2", what: "video:v1", on: true });
    expect(result.current.state).toBe("none");
    await settle();
    expect(result.current.state).toBe("none");
    expect(get).toHaveBeenCalledTimes(2);
  });

  it("another thing on the same page starts from nothing too", async () => {
    home(row("pending"));
    const { result, rerender } = follow("u1");
    await settle();
    expect(result.current.state).toBe("pending");
    rerender({ user: "u1", what: "video:v2", on: true });
    expect(result.current.state).toBe("none");
    await settle();
    expect(result.current.state).toBe("none");
  });
});
