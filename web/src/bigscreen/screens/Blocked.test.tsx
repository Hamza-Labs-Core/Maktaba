// Epic 58 story 58.14 T03 (the unit half; the Playwright half drives /tv/ by remote): the
// big screen's blocked page, time's up and managed lines. Ask a parent is the first
// control and takes focus; there is no parent PIN; the line says a parent answers from
// their phone; the pages are in Arabic too; the player's own screens give way to these
// inside the big screen.
import { describe, it, expect, vi, beforeEach } from "vitest";
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
import { BlockedOr } from "../../components/parental/AskParent";
import { TimesUp } from "../../components/parental/TimeState";
import { ParentalScreens } from "../../components/parental/screens";
import { BIG_PARENTAL_SCREENS, BigBlocked, BigBlockedOr, BigManaged, BigTimesUp } from "./Blocked";

const child: Partial<MyParental> = { restricted: true, maxTier: 9 };
const onCheck = vi.fn();

function show(
  ui: React.ReactElement,
  value: Partial<MyParental> = child,
  locale: "en" | "ar" = "en"
) {
  localStorage.setItem("mkt:locale", locale);
  return renderPage(<ParentalFixture value={value}>{ui}</ParentalFixture>, {
    path: "/big/video/v1",
    user: memberUser,
  });
}

beforeEach(() => {
  for (const f of [get, post, onCheck]) f.mockReset();
  localStorage.clear();
  sessionStorage.clear();
  // no request of the account's own yet; a guardian with a PIN exists (never offered here)
  get.mockImplementation((path: string) =>
    path === "/api/me/parental/requests"
      ? Promise.resolve({ requests: [] })
      : path === "/api/me/parental/guardians"
        ? Promise.resolve({ guardians: [{ id: "g1", username: "mama" }] })
        : Promise.reject(new ApiError({ type: "about:blank", title: "x", status: 404 } as never))
  );
});

describe("the big screen's blocked page", () => {
  it("says the limit, focuses Ask a parent, says a parent answers from their phone, and has no PIN", async () => {
    show(<BigBlocked kind="video" id="v1" onCheck={onCheck} />);
    const page = screen.getByTestId("parental-blocked");
    expect(page).toHaveTextContent("This isn't available");
    expect(page).toHaveTextContent("Your account is limited to 9+.");
    const ask = within(page).getByRole("button", { name: "Ask a parent" });
    expect(ask).toHaveAttribute("data-bs-autofocus");
    expect(ask).toHaveAttribute("data-bs-focus", "parental:ask");
    expect(screen.getByTestId("parental-ask-on-phone")).toHaveTextContent(
      "A parent can answer from Maktaba on their phone."
    );
    expect(within(page).queryByRole("button", { name: /PIN/ })).toBeNull();
    expect(get).not.toHaveBeenCalledWith("/api/me/parental/guardians");
    // nothing of the item: no title, no picture
    expect(page.querySelector("img")).toBeNull();
  });

  it("asking sends the id the page had, then offers Check again, which asks the home", async () => {
    post.mockResolvedValue({ request: { id: "q1", status: "pending" } });
    show(<BigBlocked kind="series" id="s9" onCheck={onCheck} />);
    await userEvent.click(screen.getByRole("button", { name: "Ask a parent" }));
    expect(post).toHaveBeenCalledWith("/api/me/parental/requests", {
      subject_kind: "series",
      subject_id: "s9",
    });
    expect(await screen.findByTestId("parental-ask-waiting")).toHaveTextContent(
      "Request sent. Waiting for a parent."
    );
    const check = screen.getByRole("button", { name: "Check again" });
    expect(check).toHaveAttribute("data-bs-autofocus");
    await userEvent.click(check);
    expect(onCheck).toHaveBeenCalled();
  });

  it("a request waiting is followed, and a parent's yes opens the item by itself, once", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      const request = (status: string) => ({
        id: "q1",
        subject_kind: "video",
        subject_id: "v1",
        status,
        created_at: new Date(Date.now() - 60_000).toISOString(),
        ...(status === "approved" ? { decided_at: new Date().toISOString() } : {}),
        expires_at: new Date(Date.now() + 3600_000).toISOString(),
      });
      let list = [request("pending")];
      get.mockImplementation((path: string) =>
        path === "/api/me/parental/requests"
          ? Promise.resolve({ requests: list })
          : Promise.reject(new Error("no"))
      );
      show(<BigBlocked kind="video" id="v1" onCheck={onCheck} />);
      await screen.findByTestId("parental-ask-waiting");
      expect(screen.queryByRole("button", { name: "Ask a parent" })).toBeNull();
      list = [request("approved")];
      await act(async () => {
        await vi.advanceTimersByTimeAsync(15_000);
      });
      expect(onCheck).toHaveBeenCalledTimes(1);
      expect(screen.getByTestId("parental-ask-approved")).toHaveTextContent("A parent said yes.");
    } finally {
      vi.useRealTimers();
    }
  });

  it("is in Arabic, right to left", async () => {
    show(<BigBlocked kind="video" id="v1" onCheck={onCheck} />, child, "ar");
    const page = await screen.findByTestId("parental-blocked");
    expect(page).toHaveTextContent("هذا غير متاح");
    expect(within(page).getByRole("button", { name: "اطلب من أحد الوالدين" })).toBeInTheDocument();
    expect(screen.getByTestId("parental-ask-on-phone")).toHaveTextContent(
      "يمكن لأحد الوالدين الرد من Maktaba على هاتفه."
    );
    await waitFor(() => expect(document.documentElement).toHaveAttribute("dir", "rtl"));
  });

  it("an account limited only by its device has nothing to ask", () => {
    show(<BigBlocked kind="video" id="v1" onCheck={onCheck} />, { maxTier: 9, deviceLimit: 9 });
    expect(screen.getByTestId("parental-blocked")).toHaveTextContent(
      "This device is limited to 9+."
    );
    expect(within(screen.getByTestId("parental-blocked")).queryByRole("button")).toBeNull();
    expect(screen.queryByTestId("parental-ask-on-phone")).toBeNull();
  });

  it("an account with no limit sees the not-found line, not the blocked page", () => {
    show(<BigBlockedOr kind="video" id="v1" onCheck={onCheck} message="Video not found" />, {});
    expect(screen.queryByTestId("parental-blocked")).toBeNull();
    expect(screen.getByRole("alert")).toHaveTextContent("Video not found");
  });
});

describe("the big screen's time's up", () => {
  it("says when watching opens again and asks for 15, 30 or 60 minutes", async () => {
    post.mockResolvedValue({ request: { id: "t1", status: "pending" } });
    show(
      <BigTimesUp
        reason="limit_reached"
        nextAllowedAt={new Date(Date.now() + 3 * 3600_000).toISOString()}
        savedAt="12:34"
        onCheck={onCheck}
      />
    );
    const page = screen.getByTestId("parental-times-up");
    expect(page).toHaveTextContent("12:34");
    expect(screen.getByTestId("parental-time-next")).toBeInTheDocument();
    const first = screen.getByTestId("parental-ask-15");
    expect(first).toHaveAttribute("data-bs-autofocus");
    expect(screen.getByTestId("parental-ask-30")).toBeInTheDocument();
    expect(screen.getByTestId("parental-ask-60")).toBeInTheDocument();
    expect(within(page).queryByRole("button", { name: /PIN/ })).toBeNull();
    await userEvent.click(screen.getByTestId("parental-ask-30"));
    expect(post).toHaveBeenCalledWith("/api/me/parental/requests", {
      subject_kind: "time",
      subject_id: "30",
    });
    expect(await screen.findByTestId("parental-ask-waiting")).toBeInTheDocument();
    expect(screen.queryByTestId("parental-ask-15")).toBeNull();
    expect(screen.getByTestId("parental-check")).toHaveAttribute("data-bs-autofocus");
  });
});

describe("inside the big screen, the player's screens are the big screen's", () => {
  it("BlockedOr and TimesUp draw the 10-foot pages", () => {
    show(
      <ParentalScreens.Provider value={BIG_PARENTAL_SCREENS}>
        <BlockedOr kind="video" id="v1" onCheck={onCheck} fallback={<p>missing</p>} />
        <TimesUp reason="outside_slot" nextAllowedAt={null} onCheck={onCheck} />
      </ParentalScreens.Provider>
    );
    expect(screen.getByTestId("parental-blocked")).toHaveClass("bs-parental");
    expect(screen.getByTestId("parental-times-up")).toHaveClass("bs-parental");
    expect(screen.getAllByTestId("parental-ask-on-phone")).toHaveLength(2);
  });
});

describe("Settings' managed lines", () => {
  it("a restricted account: a parent manages it, and its limit", () => {
    show(<BigManaged />);
    expect(screen.getByTestId("big-parental-managed-line")).toHaveTextContent(
      "A parent manages this account."
    );
    expect(screen.getByTestId("big-parental-limit")).toHaveTextContent("Content limit: 9+");
  });

  it("in Arabic", () => {
    show(<BigManaged />, child, "ar");
    expect(screen.getByTestId("big-parental-managed-line")).toHaveTextContent(
      "يدير أحد الوالدين هذا الحساب."
    );
    expect(screen.getByTestId("big-parental-limit")).toHaveTextContent("حدّ المحتوى");
  });

  it("an account limited by its device says the device's limit only", () => {
    show(<BigManaged />, { maxTier: 13, deviceLimit: 13 });
    expect(screen.queryByTestId("big-parental-managed-line")).toBeNull();
    expect(screen.getByTestId("big-parental-device")).toHaveTextContent(
      "This device is limited to 13+."
    );
  });

  it("an account with no limit: nothing", () => {
    show(<BigManaged />, {});
    expect(screen.queryByTestId("big-parental-managed")).toBeNull();
  });
});
