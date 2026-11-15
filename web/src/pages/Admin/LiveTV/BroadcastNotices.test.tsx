// The Broadcast panel's notices (Epic 50 story 50.24): nothing shows without notices; a
// takedown's statement names the programme and the claimant, the strike warning counts toward
// the limit, a takedown that can be answered opens the counter-notice form, which needs every
// element and both statements and shows the cloud's refusal; the end of broadcasting shows;
// a cloud that can't be reached says so. Arabic renders.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "../../../lib/i18n";
import { ApiError } from "../../../lib/api";

const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock("../../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../../lib/api")>("../../../lib/api");
  return { ...actual, api: { ...actual.api, get, post } };
});

import { BroadcastNotices } from "./BroadcastNotices";
import type { ModerationView } from "../../../lib/tvBroadcastApi";

const takedownView = (over: Partial<ModerationView["records"]> = {}): ModerationView => ({
  cloud: "ok",
  records: {
    statements: [
      {
        id: "s-1",
        kind: "takedown",
        action_id: "a-1",
        params: { programme: "Film X", claimant: "Studio A" },
        created_at: "2026-11-14T10:00:00Z",
        can_counter: true,
      },
    ],
    reports: [
      {
        id: "r-1",
        category: "abuse",
        channel: "https://watch.maktaba.tv/home-a/news",
        programme: "News",
        at: "2026-11-14T09:00:00Z",
        text: "shouting",
        status: "open",
        received_at: "2026-11-14T09:05:00Z",
      },
    ],
    strikes: 2,
    strike_limit: 3,
    strike_window_months: 12,
    terminated: false,
    ...over,
  },
  takedowns: [
    {
      id: "a-1",
      notice_id: "n-1",
      scope: "item",
      programme: "Film X",
      applied_at: "2026-11-14T10:01:00Z",
    },
  ],
});

function renderNotices() {
  return render(
    <I18nProvider>
      <BroadcastNotices />
    </I18nProvider>
  );
}

beforeEach(() => {
  localStorage.clear();
  get.mockReset();
  post.mockReset();
});

describe("BroadcastNotices", () => {
  it("shows nothing without notices", async () => {
    get.mockResolvedValue({
      cloud: "ok",
      records: { statements: [], reports: [], strikes: 0 },
      takedowns: [],
    });
    const { container } = renderNotices();
    await waitFor(() => expect(get).toHaveBeenCalledWith("/api/tv/broadcast/moderation"));
    expect(container).toBeEmptyDOMElement();
  });

  it("shows nothing when the read fails", async () => {
    get.mockRejectedValue(
      new ApiError({ type: "x", title: "x", status: 403, detail: "" } as never)
    );
    const { container } = renderNotices();
    await waitFor(() => expect(get).toHaveBeenCalled());
    expect(container).toBeEmptyDOMElement();
  });

  it("names the takedown, the claimant, the strikes and the reports", async () => {
    get.mockResolvedValue(takedownView());
    renderNotices();
    expect(
      await screen.findByText(
        '"Film X" was taken off your channel after a copyright notice. You can respond with a counter-notice.'
      )
    ).toBeInTheDocument();
    expect(screen.getByText("The notice came from Studio A.")).toBeInTheDocument();
    expect(screen.getByRole("alert")).toHaveTextContent(
      "Your channels have 2 valid copyright notices in the last 12 months. At 3, broadcasting ends for this account."
    );
    expect(screen.getByText(/Abuse or harassment/)).toBeInTheDocument();
    expect(screen.getByText("shouting")).toBeInTheDocument();
    expect(screen.getByText("Taken down on this home")).toBeInTheDocument();
  });

  it("files a counter-notice only with every element, and shows the cloud's refusal", async () => {
    get.mockResolvedValue(takedownView());
    post.mockRejectedValueOnce(
      new ApiError({
        type: "x",
        title: "x",
        status: 409,
        detail: "",
        code: "broadcast.counterNoticeFiled",
      } as never)
    );
    post.mockResolvedValueOnce({ id: "c-1", restore_after: "2026-11-28T10:00:00Z" });
    renderNotices();
    await userEvent.click(await screen.findByRole("button", { name: "Counter-notice" }));
    const submit = screen.getByRole("button", { name: "File counter-notice" });
    expect(submit).toBeDisabled();
    await userEvent.type(screen.getByLabelText("Your full name"), "Amina");
    await userEvent.type(screen.getByLabelText("Your postal address"), "1 Road");
    await userEvent.type(screen.getByLabelText("Email"), "amina@home.test");
    await userEvent.type(
      screen.getByLabelText("Why the takedown was a mistake"),
      "my own recording"
    );
    await userEvent.type(screen.getByLabelText("Type your full name as your signature"), "Amina");
    await userEvent.click(screen.getByLabelText(/mistake or misidentification/));
    expect(submit).toBeDisabled(); // both statements
    await userEvent.click(screen.getByLabelText(/jurisdiction of the court/));
    expect(submit).toBeEnabled();

    await userEvent.click(submit);
    expect(
      await screen.findByText("A counter-notice was already filed for this takedown.")
    ).toBeInTheDocument();
    expect(post).toHaveBeenCalledWith("/api/tv/broadcast/moderation/counter-notices", {
      action_id: "a-1",
      name: "Amina",
      address: "1 Road",
      phone: "",
      email: "amina@home.test",
      explanation: "my own recording",
      mistake: true,
      consent: true,
      signature: "Amina",
    });
    await userEvent.click(submit);
    await waitFor(() => expect(get).toHaveBeenCalledTimes(2));
  });

  it("shows the end of broadcasting, and a cloud away", async () => {
    get.mockResolvedValue({ cloud: "unreachable", takedowns: [] });
    const { unmount } = renderNotices();
    expect(
      await screen.findByText(
        "Maktaba Cloud can't be reached right now, so its notices may be out of date."
      )
    ).toBeInTheDocument();
    unmount();
    get.mockResolvedValue(takedownView({ terminated: true, statements: [], strikes: 3 }));
    renderNotices();
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Broadcasting has ended for this account under our repeat-infringer policy."
    );
  });

  it("renders in Arabic", async () => {
    localStorage.setItem("mkt:locale", "ar");
    get.mockResolvedValue(takedownView());
    renderNotices();
    expect(await screen.findByText("ملاحظات من مكتبة")).toBeInTheDocument();
    expect(screen.getByText(/أُزيل "Film X" من قناتك/)).toBeInTheDocument();
  });
});
