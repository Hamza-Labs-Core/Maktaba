// Epic 57 story 57.17: the review list of detected age ratings: an admin decides,
// a guardian reads, anyone else is refused by the home.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post } };
});

import { ApiError } from "../../lib/api";
import { forgetAnswers } from "../../lib/pageCache";
import { renderPage, memberUser, adminUser } from "../../test/pageHarness";
import type { User } from "../../lib/auth";
import { TierReview } from "./TierReview";

const ITEMS = [
  {
    video_id: "v1",
    title: "A rough film",
    library_id: "l1",
    library_name: "Films",
    state: "unconfirmed",
    detected_tier: 16,
    confidence: 0.93,
    categories: {
      violence: { level: 3, evidence: [75, 1260] },
      coarse_language: { level: 1, evidence: [] },
      sexual_content: { level: 0, evidence: [] },
    },
    tier: 16,
    source: "detected",
  },
  {
    video_id: "v2",
    title: "A quiet talk",
    library_id: "l2",
    library_name: "Lectures",
    state: "low_confidence",
    detected_tier: 9,
    confidence: 0.5,
    categories: {},
    tier: null,
    source: null,
  },
  {
    video_id: "v3",
    title: "No transcript",
    library_id: "l2",
    library_name: "Lectures",
    state: "not_checked",
    detected_tier: null,
    confidence: 0,
    categories: {},
    not_checked: "no_transcript",
    tier: null,
    source: null,
  },
];

function open(user: User = adminUser, locale: "en" | "ar" = "en") {
  localStorage.setItem("mkt:locale", locale);
  return renderPage(<TierReview />, { path: "/parental/review", user });
}

const rowOf = (title: string) => screen.getByRole("link", { name: title }).closest("tr")!;

beforeEach(() => {
  get.mockReset();
  post.mockReset();
  localStorage.clear();
  forgetAnswers("parental:");
  get.mockResolvedValue({ items: ITEMS, total: 3, min_confidence: 0.8, limit: 50, offset: 0 });
});

describe("TierReview (57.17)", () => {
  it("lists what nobody has decided: the state, the detected rating, what was heard and where", async () => {
    open();
    expect(await screen.findAllByTestId("parental-review-row")).toHaveLength(3);
    expect(get).toHaveBeenCalledWith("/api/parental/review?limit=50&offset=0");
    expect(screen.getByText(/at least 80% sure/)).toBeInTheDocument();

    const rough = rowOf("A rough film");
    expect(within(rough).getByText("Not confirmed")).toBeInTheDocument();
    expect(within(rough).getByText("93% sure")).toBeInTheDocument();
    expect(within(rough).getByText(/Violence \(strong\)/)).toBeInTheDocument();
    expect(within(rough).getByText(/Coarse language \(mild\)/)).toBeInTheDocument();
    expect(within(rough).queryByText(/Sexual content/)).toBeNull();
    // the evidence times open the video there
    expect(within(rough).getByRole("link", { name: "Play from 1:15" })).toHaveAttribute(
      "href",
      "/videos/v1/watch?t=75"
    );
    expect(within(rough).getByText("Detected from the transcript")).toBeInTheDocument();

    const quiet = rowOf("A quiet talk");
    expect(within(quiet).getByText("Not sure")).toBeInTheDocument();
    expect(within(quiet).getByText("Nothing noted")).toBeInTheDocument();
    expect(within(rowOf("No transcript")).getByText("Not checked")).toBeInTheDocument();
    expect(screen.getByText("1 to 3 of 3")).toBeInTheDocument();
  });

  it("an admin confirms the detected rating with one tap", async () => {
    post.mockResolvedValue({ tier: 16, source: "admin_video", recompute_count: 1 });
    open();
    await screen.findAllByTestId("parental-review-row");
    const rough = rowOf("A rough film");
    expect(within(rough).getByLabelText("Rating for A rough film")).toHaveValue("16");
    await userEvent.click(within(rough).getByRole("button", { name: "Confirm" }));
    await waitFor(() => expect(post).toHaveBeenCalledWith("/api/parental/review/v1", { tier: 16 }));
    expect(await screen.findByText("This video is now rated: 16+.")).toBeInTheDocument();
    // the list is read again: a decided video leaves it
    await waitFor(() => expect(get).toHaveBeenCalledTimes(2));
  });

  it("an admin gives another rating, or none", async () => {
    post.mockResolvedValue({ tier: 13, source: "admin_video", recompute_count: 1 });
    open();
    await screen.findAllByTestId("parental-review-row");
    const quiet = rowOf("A quiet talk");
    await userEvent.selectOptions(within(quiet).getByLabelText("Rating for A quiet talk"), "13");
    await userEvent.click(within(quiet).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(post).toHaveBeenCalledWith("/api/parental/review/v2", { tier: 13 }));

    post.mockResolvedValue({ tier: null, source: null, recompute_count: 1 });
    const none = rowOf("No transcript");
    expect(within(none).getByLabelText("Rating for No transcript")).toHaveValue("none");
    await userEvent.click(within(none).getByRole("button", { name: "Save" }));
    await waitFor(() =>
      expect(post).toHaveBeenLastCalledWith("/api/parental/review/v3", { tier: null })
    );
  });

  it("filters by state and starts again at the first page", async () => {
    open();
    await screen.findAllByTestId("parental-review-row");
    await userEvent.selectOptions(screen.getByLabelText("Show"), "low_confidence");
    await waitFor(() =>
      expect(get).toHaveBeenLastCalledWith(
        "/api/parental/review?limit=50&offset=0&state=low_confidence"
      )
    );
  });

  it("pages through a long list", async () => {
    get.mockResolvedValue({ items: ITEMS, total: 120, min_confidence: 0.8, limit: 50, offset: 0 });
    open();
    await screen.findAllByTestId("parental-review-row");
    expect(screen.getByRole("button", { name: "Previous" })).toBeDisabled();
    await userEvent.click(screen.getByRole("button", { name: "Next" }));
    await waitFor(() =>
      expect(get).toHaveBeenLastCalledWith("/api/parental/review?limit=50&offset=50")
    );
  });

  it("a guardian reads the list and has nothing to decide with", async () => {
    open(memberUser);
    await screen.findAllByTestId("parental-review-row");
    expect(screen.queryByRole("columnheader", { name: "Your rating" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Confirm" })).toBeNull();
    expect(screen.queryByRole("combobox", { name: /Rating for/ })).toBeNull();
    expect(post).not.toHaveBeenCalled();
  });

  it("a viewer is refused by the home: no list", async () => {
    get.mockRejectedValue(new ApiError({ type: "about:blank", title: "forbidden", status: 403 }));
    open(memberUser);
    expect(await screen.findByRole("alert")).toBeInTheDocument();
    expect(screen.queryByTestId("parental-review-row")).toBeNull();
  });

  it("shows the home's refusal of a decision", async () => {
    post.mockRejectedValue(new ApiError({ type: "about:blank", title: "forbidden", status: 403 }));
    open();
    await screen.findAllByTestId("parental-review-row");
    await userEvent.click(within(rowOf("A rough film")).getByRole("button", { name: "Confirm" }));
    expect(
      await screen.findByText("Only an administrator can set an age rating.")
    ).toBeInTheDocument();
  });

  it("says so when there is nothing to review", async () => {
    get.mockResolvedValue({ items: [], total: 0, min_confidence: 0.8, limit: 50, offset: 0 });
    open();
    expect(await screen.findByText("Nothing to review")).toBeInTheDocument();
  });

  it("is Arabic in Arabic, with the tier's plus on the correct side", async () => {
    open(adminUser, "ar");
    await screen.findAllByTestId("parental-review-row");
    expect(document.documentElement).toHaveAttribute("dir", "rtl");
    expect(
      screen.getByRole("heading", { level: 1, name: "التصنيفات المكتشفة" })
    ).toBeInTheDocument();
    const rough = rowOf("A rough film");
    expect(within(rough).getByText("غير مؤكَّد")).toBeInTheDocument();
    expect(within(rough).getByText(/عنف \(شديد\)/)).toBeInTheDocument();
    // a left-to-right mark before +16
    expect(within(rough).getAllByText("‎+16").length).toBeGreaterThan(0);
  });
});
