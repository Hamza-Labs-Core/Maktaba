// Epic 57 stories 57.05 and 57.06 on the web: what a restricted account sees where
// the home hides something or its time is over. The blocked page shows no title,
// poster or rating; asking sends the id the page already had; a guardian's PIN is
// checked by the home alone.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { useState } from "react";
import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post } };
});

import { ApiError } from "../../lib/api";
import { ParentalFixture, type MyParental } from "../../lib/parental";
import { renderPage, memberUser } from "../../test/pageHarness";
import type { User } from "../../lib/auth";
import { ASKED_TIME, noteAsked, wasAsked } from "../../lib/parentalAsked";
import { AskTime, Blocked, BlockedOr, PinUnlock } from "./AskParent";
import { HomeTime, TimesUp } from "./TimeState";

const problem = (status: number, extra: Record<string, unknown> = {}, retry?: number) =>
  new ApiError({ type: "about:blank", title: "refused", status, ...extra } as never, retry);
const child: Partial<MyParental> = { restricted: true, maxTier: 9 };
const onCheck = vi.fn();

/**
 * The home: who can type a PIN, and (when `requests` is given) the account's own
 * requests, newest first. Without `requests` it is a home that has no such route.
 */
function home(unlockers: unknown[] = [], requests?: () => unknown[]) {
  get.mockImplementation((path: string) =>
    path === "/api/me/parental/guardians"
      ? Promise.resolve({ guardians: unlockers })
      : path === "/api/me/parental/requests" && requests
        ? Promise.resolve({ requests: requests() })
        : Promise.reject(problem(404))
  );
}

/** One of the account's own requests as the home lists it. */
const mine = (status: string, over: Record<string, unknown> = {}) => ({
  id: "q1",
  subject_kind: "video",
  subject_id: "v1",
  status,
  created_at: new Date(Date.now() - 60_000).toISOString(),
  expires_at: new Date(Date.now() + 6 * 3600_000).toISOString(),
  ...over,
});
const mineTime = (status: string, over: Record<string, unknown> = {}) =>
  mine(status, { id: "t1", subject_kind: "time", subject_id: "30", minutes: 30, ...over });
/** The home is asked about a waiting request this often (lib/parentalRequests). */
const POLL_MS = 15_000;

function show(
  ui: React.ReactElement,
  value: Partial<MyParental> = child,
  locale: "en" | "ar" = "en",
  user: User = memberUser
) {
  localStorage.setItem("mkt:locale", locale);
  return renderPage(<ParentalFixture value={value}>{ui}</ParentalFixture>, {
    path: "/videos/v1",
    user,
  });
}

/** A waiting request as the home answers it, expiring in `ms`. */
const waiting = (ms = 7 * 24 * 3600_000, over: Record<string, unknown> = {}) => ({
  request: {
    id: "q1",
    status: "pending",
    expires_at: new Date(Date.now() + ms).toISOString(),
    ...over,
  },
});

const blocked = <Blocked kind="video" id="v1" onCheck={onCheck} />;

beforeEach(() => {
  for (const f of [get, post, onCheck]) f.mockReset();
  localStorage.clear();
  sessionStorage.clear();
  home();
});

describe("the blocked page", () => {
  it("says the account's limit and offers Ask a parent", async () => {
    show(blocked);
    const page = screen.getByTestId("parental-blocked");
    expect(page).toHaveTextContent("This isn't available");
    expect(page).toHaveTextContent("Your account is limited to 9+.");
    expect(within(page).getByRole("img", { name: "Not available" })).toBeInTheDocument();
    expect(within(page).getByRole("button", { name: "Ask a parent" })).toBeInTheDocument();
    // no guardian has a PIN: the PIN way is not offered
    await waitFor(() => expect(get).toHaveBeenCalledWith("/api/me/parental/guardians"));
    expect(within(page).queryByRole("button", { name: "Enter parent PIN" })).toBeNull();
  });

  it("asking sends the id the page had, then waits; Check again asks the home for the thing", async () => {
    show(blocked);
    post.mockResolvedValue({ request: { id: "q1", status: "pending" } });
    await userEvent.click(screen.getByRole("button", { name: "Ask a parent" }));
    expect(post).toHaveBeenCalledWith("/api/me/parental/requests", {
      subject_kind: "video",
      subject_id: "v1",
    });
    expect(await screen.findByTestId("parental-ask-waiting")).toHaveTextContent(
      "Request sent. Waiting for a parent."
    );
    expect(screen.queryByRole("button", { name: "Ask a parent" })).toBeNull();
    await userEvent.click(screen.getByRole("button", { name: "Check again" }));
    expect(onCheck).toHaveBeenCalled();
  });

  it("remembers what was asked when the page is opened again", async () => {
    post.mockResolvedValue({ request: { id: "q1", status: "pending" } });
    const first = show(blocked);
    await userEvent.click(screen.getByRole("button", { name: "Ask a parent" }));
    await screen.findByTestId("parental-ask-waiting");
    first.unmount();
    show(blocked);
    expect(screen.getByTestId("parental-ask-waiting")).toBeInTheDocument();
    // another thing has not been asked for
    show(<Blocked kind="series" id="s1" onCheck={onCheck} />);
    expect(screen.getAllByRole("button", { name: "Ask a parent" })).toHaveLength(1);
  });

  it("says when too many requests wait", async () => {
    show(blocked);
    post.mockRejectedValue(problem(429, { code: "too_many_requests" }));
    await userEvent.click(screen.getByRole("button", { name: "Ask a parent" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "You have too many requests waiting."
    );
  });

  it("a sign-in limited only by its device is told so, and cannot ask", async () => {
    show(blocked, { restricted: false, maxTier: 13, deviceLimit: 13 });
    const page = screen.getByTestId("parental-blocked");
    expect(page).toHaveTextContent("This device is limited to 13+.");
    expect(within(page).queryByRole("button", { name: "Ask a parent" })).toBeNull();
    // only a restricted account has guardians to ask: the home is not asked
    expect(get).not.toHaveBeenCalled();
  });

  it("someone with no limit gets the ordinary not-found page", () => {
    show(<BlockedOr kind="video" id="v1" onCheck={onCheck} fallback={<p>not found</p>} />, {});
    expect(screen.getByText("not found")).toBeInTheDocument();
    expect(screen.queryByTestId("parental-blocked")).toBeNull();
  });

  it("is in Arabic", () => {
    show(blocked, child, "ar");
    const page = screen.getByTestId("parental-blocked");
    expect(document.documentElement).toHaveAttribute("dir", "rtl");
    expect(page).toHaveTextContent("هذا غير متاح");
    expect(within(page).getByRole("button", { name: "اطلب من أحد الوالدين" })).toBeInTheDocument();
  });
});

describe("a parent's PIN on the child's screen", () => {
  const guardians = [
    { id: "g2", username: "dad" },
    { id: "g1", username: "mum" },
  ];

  async function openPin() {
    await userEvent.click(await screen.findByRole("button", { name: "Enter parent PIN" }));
    return screen.findByRole("dialog");
  }

  it("the guardian picks their name and types six digits; the home opens it for today", async () => {
    home(guardians);
    show(blocked);
    const dialog = await openPin();
    const who = within(dialog).getByLabelText("Who is entering the PIN?");
    expect(
      within(who)
        .getAllByRole("option")
        .map((o) => o.textContent)
    ).toEqual(["dad", "mum"]);
    await userEvent.selectOptions(who, "g1");
    const pin = within(dialog).getByLabelText("Enter the 6-digit parent PIN");
    expect(pin).toHaveAttribute("type", "password");
    expect(pin).toHaveAttribute("dir", "ltr");
    expect(within(dialog).getByRole("button", { name: "Unlock" })).toBeDisabled();
    await userEvent.type(pin, "12x3456");
    expect(pin).toHaveValue("123456");
    post.mockResolvedValue({ rule: { id: "r1", expires_at: "2026-11-13T01:00:00Z" } });
    await userEvent.click(within(dialog).getByRole("button", { name: "Unlock" }));
    expect(post).toHaveBeenCalledWith("/api/me/parental/unlock", {
      guardian_id: "g1",
      pin: "123456",
      subject_kind: "video",
      subject_id: "v1",
    });
    expect(await screen.findByText("Unlocked for today.")).toBeVisible();
    expect(onCheck).toHaveBeenCalled();
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("a wrong PIN says so, clears the digits, and opens nothing", async () => {
    home(guardians);
    show(blocked);
    const dialog = await openPin();
    post.mockRejectedValue(problem(403, { code: "pin_wrong" }));
    await userEvent.type(within(dialog).getByLabelText("Enter the 6-digit parent PIN"), "000000");
    await userEvent.click(within(dialog).getByRole("button", { name: "Unlock" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent("That PIN isn't right.");
    expect(within(dialog).getByLabelText("Enter the 6-digit parent PIN")).toHaveValue("");
    expect(onCheck).not.toHaveBeenCalled();
  });

  it("too many tries says how long to wait", async () => {
    home(guardians);
    show(blocked);
    const dialog = await openPin();
    post.mockRejectedValue(problem(429, { code: "pin_locked", retry_after: 900 }));
    await userEvent.type(within(dialog).getByLabelText("Enter the 6-digit parent PIN"), "000000");
    await userEvent.click(within(dialog).getByRole("button", { name: "Unlock" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent(
      "Too many tries. Try again in 15 minutes."
    );
  });
});

describe("the time's-up screen", () => {
  const timesUp = <TimesUp reason="limit_reached" nextAllowedAt={null} onCheck={onCheck} />;

  it("a restricted account asks for more time: the minutes are the id", async () => {
    show(timesUp);
    const ask = screen.getByTestId("parental-ask-time");
    post.mockResolvedValue({ request: { id: "q2", status: "pending", subject_kind: "time" } });
    await userEvent.click(within(ask).getByRole("button", { name: "30 minutes" }));
    expect(post).toHaveBeenCalledWith("/api/me/parental/requests", {
      subject_kind: "time",
      subject_id: "30",
    });
    expect(await within(ask).findByTestId("parental-ask-waiting")).toHaveTextContent(
      "You asked for more time. Waiting for a parent."
    );
    expect(within(ask).queryByRole("button", { name: "30 minutes" })).toBeNull();
  });

  it("a parent's PIN gives minutes at once, and the screen asks the home again", async () => {
    home([{ id: "g1", username: "mum" }]);
    show(timesUp);
    await userEvent.click(await screen.findByRole("button", { name: "Enter parent PIN" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.selectOptions(within(dialog).getByLabelText("Time to give"), "60");
    await userEvent.type(within(dialog).getByLabelText("Enter the 6-digit parent PIN"), "123456");
    post.mockResolvedValue({ grant: { id: "t1", minutes: 60 } });
    await userEvent.click(within(dialog).getByRole("button", { name: "Unlock" }));
    expect(post).toHaveBeenCalledWith("/api/me/parental/unlock", {
      guardian_id: "g1",
      pin: "123456",
      subject_kind: "time",
      subject_id: "60",
    });
    expect(await screen.findByText("Added for today: 1 hour.")).toBeVisible();
    expect(onCheck).toHaveBeenCalled();
  });

  it("offers nothing to an account that is not restricted", () => {
    show(<AskTime onCheck={onCheck} />, {});
    expect(screen.queryByTestId("parental-ask-time")).toBeNull();
    expect(get).not.toHaveBeenCalled();
  });

  it("is in Arabic", () => {
    show(timesUp, child, "ar");
    const ask = screen.getByTestId("parental-ask-time");
    expect(within(ask).getByRole("group", { name: "اطلب وقتًا إضافيًا" })).toBeInTheDocument();
  });
});

// What this tab remembers having asked: one account's, for as long as the request
// waits, and gone once it is answered.
describe("what was asked", () => {
  const sister: User = { id: "u-sister", username: "noor", is_admin: false };
  const guardians = [{ id: "g1", username: "mum" }];
  const timesUp = <TimesUp reason="limit_reached" nextAllowedAt={null} onCheck={onCheck} />;

  async function unlock() {
    await userEvent.click(await screen.findByRole("button", { name: "Enter parent PIN" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.type(within(dialog).getByLabelText("Enter the 6-digit parent PIN"), "123456");
    await userEvent.click(within(dialog).getByRole("button", { name: "Unlock" }));
  }

  it("another account in the same tab does not inherit it", async () => {
    post.mockResolvedValue(waiting());
    const first = show(blocked);
    await userEvent.click(screen.getByRole("button", { name: "Ask a parent" }));
    await screen.findByTestId("parental-ask-waiting");
    first.unmount();
    // her sister signs in in the same tab and meets the same video
    show(blocked, child, "en", sister);
    expect(screen.queryByTestId("parental-ask-waiting")).toBeNull();
    expect(screen.getByRole("button", { name: "Ask a parent" })).toBeInTheDocument();
    expect(wasAsked(memberUser.id, "video:v1")).toBe(true);
    expect(wasAsked(sister.id, "video:v1")).toBe(false);
  });

  it("follows the thing when the page changes to another one while it stays open", async () => {
    post.mockResolvedValue(waiting());
    function Page() {
      const [id, setId] = useState("v1");
      return (
        <>
          <button type="button" onClick={() => setId("v2")}>
            next video
          </button>
          <Blocked kind="video" id={id} onCheck={onCheck} />
        </>
      );
    }
    show(<Page />);
    await userEvent.click(screen.getByRole("button", { name: "Ask a parent" }));
    await screen.findByTestId("parental-ask-waiting");
    await userEvent.click(screen.getByRole("button", { name: "next video" }));
    // v2 was never asked for
    expect(screen.queryByTestId("parental-ask-waiting")).toBeNull();
    expect(screen.getByRole("button", { name: "Ask a parent" })).toBeInTheDocument();
  });

  it("a request that expired can be asked again", async () => {
    // asked a while ago; the home's seven days ran out a minute ago
    noteAsked(memberUser.id, "video:v1", new Date(Date.now() - 60_000).toISOString());
    show(blocked);
    expect(screen.queryByTestId("parental-ask-waiting")).toBeNull();
    post.mockResolvedValue(waiting());
    await userEvent.click(screen.getByRole("button", { name: "Ask a parent" }));
    expect(await screen.findByTestId("parental-ask-waiting")).toBeInTheDocument();
  });

  it("stops waiting at the request's expiry while the page is open", async () => {
    post.mockResolvedValue(waiting(300));
    show(blocked);
    await userEvent.click(screen.getByRole("button", { name: "Ask a parent" }));
    await screen.findByTestId("parental-ask-waiting");
    // Check again after the expiry still finds it hidden: asking is offered again
    expect(
      await screen.findByRole("button", { name: "Ask a parent" }, { timeout: 2000 })
    ).toBeInTheDocument();
    expect(screen.queryByTestId("parental-ask-waiting")).toBeNull();
  });

  it("a parent's PIN answers the request for a thing: nothing waits after it", async () => {
    home(guardians);
    post.mockResolvedValue(waiting());
    show(blocked);
    await userEvent.click(screen.getByRole("button", { name: "Ask a parent" }));
    await screen.findByTestId("parental-ask-waiting");
    post.mockResolvedValue({ rule: { id: "r1", expires_at: "2099-01-01T01:00:00Z" } });
    await unlock();
    await waitFor(() => expect(onCheck).toHaveBeenCalled());
    expect(wasAsked(memberUser.id, "video:v1")).toBe(false);
  });

  it("more time can be asked for again after the minutes that were given ran out", async () => {
    // the home's answers about the time, in order: over, allowed (a parent said yes
    // to 15 minutes), over again (they ran out)
    let myTime: Record<string, unknown> = {
      allowed: false,
      limited: true,
      reason: "limit_reached",
      remaining_seconds: 0,
    };
    get.mockImplementation((path: string) =>
      path.startsWith("/api/me/parental/time")
        ? Promise.resolve(myTime)
        : path === "/api/me/parental/guardians"
          ? Promise.resolve({ guardians: [] })
          : Promise.reject(problem(404))
    );
    post.mockResolvedValue(waiting(6 * 3600_000, { subject_kind: "time" }));
    const first = show(<HomeTime />);
    const ask = await screen.findByTestId("parental-ask-time");
    await userEvent.click(within(ask).getByRole("button", { name: "15 minutes" }));
    await screen.findByTestId("parental-ask-waiting");
    expect(wasAsked(memberUser.id, ASKED_TIME)).toBe(true);

    myTime = { allowed: true, limited: true, remaining_seconds: 900 };
    await userEvent.click(screen.getByRole("button", { name: "Check again" }));
    await waitFor(() => expect(screen.queryByTestId("parental-times-up")).toBeNull());
    expect(screen.getByTestId("parental-time-left")).toHaveTextContent("15 minutes left today");
    first.unmount();

    // the 15 minutes ran out: the time's-up screen offers the asks again
    myTime = { allowed: false, limited: true, reason: "limit_reached", remaining_seconds: 0 };
    show(<HomeTime />);
    const again = await screen.findByTestId("parental-ask-time");
    expect(within(again).queryByTestId("parental-ask-waiting")).toBeNull();
    expect(within(again).getByRole("button", { name: "15 minutes" })).toBeInTheDocument();
  });

  it("a request for time still waits while the home says the time is over", async () => {
    post.mockResolvedValue(waiting(6 * 3600_000, { subject_kind: "time" }));
    const first = show(timesUp);
    await userEvent.click(screen.getByRole("button", { name: "30 minutes" }));
    await screen.findByTestId("parental-ask-waiting");
    first.unmount();
    show(timesUp);
    expect(screen.getByTestId("parental-ask-waiting")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "30 minutes" })).toBeNull();
  });

  it("a parent's PIN for more time answers the request for time", async () => {
    home(guardians);
    post.mockResolvedValue(waiting(6 * 3600_000, { subject_kind: "time" }));
    show(timesUp);
    await userEvent.click(await screen.findByRole("button", { name: "30 minutes" }));
    await screen.findByTestId("parental-ask-waiting");
    post.mockResolvedValue({ grant: { id: "t1", minutes: 30 } });
    await unlock();
    await waitFor(() => expect(onCheck).toHaveBeenCalled());
    expect(wasAsked(memberUser.id, ASKED_TIME)).toBe(false);
    expect(screen.queryByTestId("parental-ask-waiting")).toBeNull();
  });
});

// The digits are a parent's, typed on a child's screen: they stay nowhere.
describe("the PIN's digits", () => {
  const guardians = [{ id: "g1", username: "mum" }];
  // the dialog's owner keeps it open here, as a page does until the home is read again
  const dialog = (onUnlocked = vi.fn()) => (
    <PinUnlock
      open
      onClose={() => {}}
      guardians={guardians}
      kind="video"
      id="v1"
      onUnlocked={onUnlocked}
    />
  );
  const field = () => screen.getByLabelText("Enter the 6-digit parent PIN");

  it("are gone from the page once the home has unlocked", async () => {
    const onUnlocked = vi.fn();
    show(dialog(onUnlocked));
    await userEvent.type(field(), "123456");
    post.mockResolvedValue({ rule: { id: "r1", expires_at: "2099-01-01T01:00:00Z" } });
    await userEvent.click(screen.getByRole("button", { name: "Unlock" }));
    await waitFor(() => expect(onUnlocked).toHaveBeenCalled());
    expect(field()).toHaveValue("");
  });

  it("are gone on Cancel", async () => {
    show(dialog());
    await userEvent.type(field(), "1234");
    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(field()).toHaveValue("");
    expect(post).not.toHaveBeenCalled();
  });

  it("are a one-time code to the browser, which then does not offer to save them", () => {
    show(dialog());
    expect(field()).toHaveAttribute("autocomplete", "one-time-code");
    expect(field()).toHaveAttribute("type", "password");
  });
});

// What became of a request is the home's word: a yes opens the thing or resumes
// watching by itself, a no is said, an expired request brings the asks back.
describe("what a parent answered", () => {
  const sister: User = { id: "u-sister", username: "noor", is_admin: false };
  const guardians = [{ id: "g1", username: "mum" }];
  const timesUp = <TimesUp reason="limit_reached" nextAllowedAt={null} onCheck={onCheck} />;
  const requestAsks = () =>
    get.mock.calls.filter(([path]) => path === "/api/me/parental/requests").length;
  const pass = (ms: number) =>
    act(async () => {
      await vi.advanceTimersByTimeAsync(ms);
    });
  afterEach(() => {
    vi.useRealTimers();
    Reflect.deleteProperty(document, "visibilityState");
  });

  it("a request made on another device waits here too, with no mark in this tab", async () => {
    home([], () => [mine("pending")]);
    show(blocked);
    expect(await screen.findByTestId("parental-ask-waiting")).toHaveTextContent(
      "Request sent. Waiting for a parent."
    );
    expect(screen.queryByRole("button", { name: "Ask a parent" })).toBeNull();
    expect(post).not.toHaveBeenCalled();
  });

  it("a parent's yes opens the thing by itself, once", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    let list = [mine("pending")];
    home([], () => list);
    show(blocked);
    await screen.findByTestId("parental-ask-waiting");
    await pass(POLL_MS);
    expect(onCheck).not.toHaveBeenCalled();
    list = [mine("approved", { decided_at: new Date().toISOString() })];
    await pass(POLL_MS);
    expect(onCheck).toHaveBeenCalledTimes(1);
    expect(screen.getByTestId("parental-ask-approved")).toHaveTextContent("A parent said yes.");
    expect(screen.queryByTestId("parental-ask-waiting")).toBeNull();
    // answered: the home is not asked again, and the thing is not opened twice
    const asks = requestAsks();
    await pass(10 * POLL_MS);
    expect(requestAsks()).toBe(asks);
    expect(onCheck).toHaveBeenCalledTimes(1);
  });

  it("a parent's no is said, with Ask again and the PIN", async () => {
    let list = [mine("denied", { decided_at: new Date().toISOString() })];
    home(guardians, () => list);
    show(blocked);
    expect(await screen.findByTestId("parental-ask-denied")).toHaveTextContent("A parent said no.");
    expect(screen.queryByTestId("parental-ask-waiting")).toBeNull();
    expect(screen.queryByRole("button", { name: "Check again" })).toBeNull();
    expect(await screen.findByRole("button", { name: "Enter parent PIN" })).toBeInTheDocument();
    expect(onCheck).not.toHaveBeenCalled();
    // asking again makes a new request, which waits
    post.mockResolvedValue({ request: mine("pending", { id: "q2" }) });
    list = [mine("pending", { id: "q2" }), ...list];
    await userEvent.click(screen.getByRole("button", { name: "Ask again" }));
    expect(post).toHaveBeenCalledWith("/api/me/parental/requests", {
      subject_kind: "video",
      subject_id: "v1",
    });
    expect(await screen.findByTestId("parental-ask-waiting")).toBeInTheDocument();
    expect(screen.queryByTestId("parental-ask-denied")).toBeNull();
  });

  it("a no that comes while the page waits shows without a tap", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    let list = [mine("pending")];
    home([], () => list);
    show(blocked);
    await screen.findByTestId("parental-ask-waiting");
    list = [mine("denied")];
    await pass(POLL_MS);
    expect(screen.getByTestId("parental-ask-denied")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Ask again" })).toBeInTheDocument();
    expect(onCheck).not.toHaveBeenCalled();
  });

  it("an expired request brings Ask a parent back", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    let list = [mine("pending")];
    home([], () => list);
    show(blocked);
    await screen.findByTestId("parental-ask-waiting");
    list = [mine("expired")];
    await pass(POLL_MS);
    expect(screen.getByRole("button", { name: "Ask a parent" })).toBeInTheDocument();
    expect(screen.queryByTestId("parental-ask-waiting")).toBeNull();
    expect(screen.queryByTestId("parental-ask-denied")).toBeNull();
    expect(onCheck).not.toHaveBeenCalled();
  });

  it("the home's word replaces what this tab remembered", async () => {
    // the tab remembers asking; the home has since closed the request
    noteAsked(memberUser.id, "video:v1", new Date(Date.now() + 3600_000).toISOString());
    home([], () => [mine("expired")]);
    show(blocked);
    // first paint: the tab's mark
    expect(screen.getByTestId("parental-ask-waiting")).toBeInTheDocument();
    expect(await screen.findByRole("button", { name: "Ask a parent" })).toBeInTheDocument();
    expect(screen.queryByTestId("parental-ask-waiting")).toBeNull();
    expect(wasAsked(memberUser.id, "video:v1")).toBe(false);
  });

  it("a yes from before is not acted on: the page does not reload itself for old news", async () => {
    home([], () => [mine("approved")]);
    show(blocked);
    await waitFor(() => expect(requestAsks()).toBe(1));
    expect(await screen.findByRole("button", { name: "Ask a parent" })).toBeInTheDocument();
    expect(onCheck).not.toHaveBeenCalled();
  });

  it("stops asking while the tab is hidden, and after 30 minutes with no change", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    home([], () => [mine("pending")]);
    show(blocked);
    await screen.findByTestId("parental-ask-waiting");
    const see = (state: "visible" | "hidden") => {
      Object.defineProperty(document, "visibilityState", { value: state, configurable: true });
      document.dispatchEvent(new Event("visibilitychange"));
    };
    act(() => see("hidden"));
    let asks = requestAsks();
    await pass(10 * POLL_MS);
    expect(requestAsks()).toBe(asks);
    await act(async () => see("visible"));
    expect(requestAsks()).toBe(asks + 1);
    // nothing changes for half an hour: the asking ends, Check again stays
    await pass(31 * 60_000);
    asks = requestAsks();
    await pass(10 * POLL_MS);
    expect(requestAsks()).toBe(asks);
    expect(screen.getByTestId("parental-ask-waiting")).toBeInTheDocument();
    await act(async () => screen.getByRole("button", { name: "Check again" }).click());
    expect(requestAsks()).toBe(asks + 1);
    expect(onCheck).toHaveBeenCalledTimes(1);
  });

  it("another child signing in in the same tab sees her own state, not the first child's", async () => {
    const lists: Record<string, unknown[]> = { [memberUser.id]: [mine("denied")], [sister.id]: [] };
    let who = memberUser.id;
    home([], () => lists[who]);
    const first = show(blocked);
    await screen.findByTestId("parental-ask-denied");
    first.unmount();
    who = sister.id;
    show(blocked, child, "en", sister);
    expect(screen.queryByTestId("parental-ask-denied")).toBeNull();
    expect(await screen.findByRole("button", { name: "Ask a parent" })).toBeInTheDocument();
    await waitFor(() => expect(requestAsks()).toBe(2));
    expect(screen.queryByTestId("parental-ask-denied")).toBeNull();
    expect(screen.queryByTestId("parental-ask-waiting")).toBeNull();
  });

  it("a no is said in Arabic", async () => {
    home([], () => [mine("denied")]);
    show(blocked, child, "ar");
    expect(await screen.findByTestId("parental-ask-denied")).toHaveTextContent("رفض أحد الوالدين.");
    expect(screen.getByRole("button", { name: "اطلب مجددًا" })).toBeInTheDocument();
  });

  it("a yes is said in Arabic", async () => {
    noteAsked(memberUser.id, "video:v1", new Date(Date.now() + 3600_000).toISOString());
    home([], () => [mine("approved")]);
    show(blocked, child, "ar");
    expect(await screen.findByTestId("parental-ask-approved")).toHaveTextContent(
      "وافق أحد الوالدين."
    );
    expect(onCheck).toHaveBeenCalledTimes(1);
  });

  describe("for more time", () => {
    it("a yes asks the home for the time once and watching resumes, with no tap", async () => {
      vi.useFakeTimers({ shouldAdvanceTime: true });
      let myTime: Record<string, unknown> = {
        allowed: false,
        limited: true,
        reason: "limit_reached",
        remaining_seconds: 0,
      };
      let list = [mineTime("pending")];
      get.mockImplementation((path: string) =>
        path.startsWith("/api/me/parental/time")
          ? Promise.resolve(myTime)
          : path === "/api/me/parental/requests"
            ? Promise.resolve({ requests: list })
            : path === "/api/me/parental/guardians"
              ? Promise.resolve({ guardians: [] })
              : Promise.reject(problem(404))
      );
      const timeAsks = () =>
        get.mock.calls.filter(([path]) => String(path).startsWith("/api/me/parental/time")).length;
      show(<HomeTime />);
      const ask = await screen.findByTestId("parental-ask-time");
      expect(await within(ask).findByTestId("parental-ask-waiting")).toHaveTextContent(
        "You asked for more time. Waiting for a parent."
      );
      // refused with no next time: the time itself is not asked about again
      await pass(4 * POLL_MS);
      expect(timeAsks()).toBe(1);
      expect(screen.getByTestId("parental-times-up")).toBeInTheDocument();

      // a parent says yes on their own phone
      myTime = { allowed: true, limited: true, remaining_seconds: 1800 };
      list = [mineTime("approved", { decided_at: new Date().toISOString() })];
      await pass(POLL_MS);
      await waitFor(() => expect(screen.queryByTestId("parental-times-up")).toBeNull());
      expect(timeAsks()).toBe(2);
      expect(wasAsked(memberUser.id, ASKED_TIME)).toBe(false);
    });

    it("a no is said, with the minutes to ask again and the PIN", async () => {
      let list = [mineTime("denied", { decided_at: new Date().toISOString() })];
      home(guardians, () => list);
      show(timesUp);
      const ask = screen.getByTestId("parental-ask-time");
      expect(await within(ask).findByTestId("parental-ask-denied")).toHaveTextContent(
        "A parent said no."
      );
      expect(within(ask).getByRole("group", { name: "Ask again" })).toBeInTheDocument();
      expect(await within(ask).findByRole("button", { name: "Enter parent PIN" })).toBeVisible();
      expect(onCheck).not.toHaveBeenCalled();
      post.mockResolvedValue({ request: mineTime("pending", { id: "t2", subject_id: "15" }) });
      list = [mineTime("pending", { id: "t2", subject_id: "15" }), ...list];
      await userEvent.click(within(ask).getByRole("button", { name: "15 minutes" }));
      expect(post).toHaveBeenCalledWith("/api/me/parental/requests", {
        subject_kind: "time",
        subject_id: "15",
      });
      expect(await within(ask).findByTestId("parental-ask-waiting")).toBeInTheDocument();
      expect(within(ask).queryByTestId("parental-ask-denied")).toBeNull();
    });

    it("an expired request brings the minutes back", async () => {
      noteAsked(memberUser.id, ASKED_TIME, new Date(Date.now() + 3600_000).toISOString());
      home([], () => [mineTime("expired")]);
      show(timesUp);
      const ask = screen.getByTestId("parental-ask-time");
      expect(
        await within(ask).findByRole("group", { name: "Ask for more time" })
      ).toBeInTheDocument();
      expect(within(ask).getByRole("button", { name: "30 minutes" })).toBeInTheDocument();
      expect(within(ask).queryByTestId("parental-ask-waiting")).toBeNull();
      expect(within(ask).queryByTestId("parental-ask-denied")).toBeNull();
    });

    it("Check again asks what became of the request too", async () => {
      let list = [mineTime("pending")];
      home([], () => list);
      show(timesUp);
      const ask = screen.getByTestId("parental-ask-time");
      await within(ask).findByTestId("parental-ask-waiting");
      list = [mineTime("denied")];
      await userEvent.click(screen.getByRole("button", { name: "Check again" }));
      expect(onCheck).toHaveBeenCalledTimes(1);
      expect(await within(ask).findByTestId("parental-ask-denied")).toBeInTheDocument();
    });

    it("a no is said in Arabic", async () => {
      home([], () => [mineTime("denied")]);
      show(timesUp, child, "ar");
      const ask = screen.getByTestId("parental-ask-time");
      expect(await within(ask).findByTestId("parental-ask-denied")).toHaveTextContent(
        "رفض أحد الوالدين."
      );
      expect(within(ask).getByRole("group", { name: "اطلب مجددًا" })).toBeInTheDocument();
    });
  });
});
