// Story 50.24: the console's moderation queue. The API is the authority (a customer gets 401 and
// the wrong staff role 403, tested in the cloud); here: Access denied on 403, the open notices
// and reports, a takedown's body by scope, every decision needs its reason, the restore window,
// the strikes and a reinstate, and the refusals read in the console's words. Arabic renders.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { setStaffToken } from "../api";
import { renderAt, staffSession, stubFetch } from "../test/harness";

beforeEach(() => setStaffToken(null));
afterEach(() => {
  vi.unstubAllGlobals();
  localStorage.clear();
});

const Q = "/v1/admin/moderation";

function queue(over: Record<string, unknown> = {}) {
  return {
    notices: [
      {
        id: "n-1",
        source: "form",
        status: "open",
        claimant: "Studio A",
        email: "rights@studio.test",
        address: "1 Road",
        phone: "",
        work: "Film X",
        urls: ["https://watch.maktaba.tv/home-a/news"],
        programme: "",
        at: "2026-11-14T20:00:00Z",
        explanation: "the whole film",
        signature: "A. Person",
        privacy: true,
        urgent: false,
        channel_id: "ch-1",
        edge_known: true,
        received_at: "2026-11-14T21:00:00Z",
        unfounded: 1,
        suspended: false,
      },
      {
        id: "n-0",
        source: "form",
        status: "rejected",
        claimant: "Old Claimant",
        email: "old@x.test",
        address: "",
        phone: "",
        work: "Song",
        urls: [],
        programme: "",
        explanation: "",
        signature: "",
        privacy: false,
        urgent: false,
        edge_known: false,
        received_at: "2026-11-01T21:00:00Z",
        unfounded: 0,
        suspended: false,
        decision: "not ours",
      },
    ],
    reports: [
      {
        id: "r-1",
        category: "illegal",
        about: "",
        status: "open",
        channel: "https://watch.maktaba.tv/home-a/news",
        channel_id: "ch-1",
        edge_known: true,
        programme: "News",
        at: "2026-11-14T19:00:00Z",
        email: "",
        text: "something illegal",
        urgent: true,
        received_at: "2026-11-14T19:05:00Z",
      },
    ],
    actions: [
      {
        id: "a-1",
        kind: "takedown",
        basis: "notice",
        scope: "item",
        notice_id: "n-9",
        channel_id: "ch-1",
        programme: "Film Y",
        home_state: "applied",
        reason: "valid notice",
        actor: "ops@example.com",
        created_at: "2026-11-10T09:00:00Z",
      },
    ],
    counter_notices: [
      {
        id: "c-1",
        action_id: "a-1",
        name: "Amina",
        email: "amina@home.test",
        explanation: "my own recording",
        filed_at: "2026-11-11T09:00:00Z",
        restore_after: "2026-11-25T09:00:00Z",
        restore_open: false,
      },
    ],
    strikes: [
      {
        id: "s-1",
        publisher_id: "pub-1",
        notice_id: "n-9",
        action_id: "a-1",
        at: "2026-11-10T09:00:00Z",
        expires_at: "2027-11-10T09:00:00Z",
        active: true,
      },
    ],
    terminated: ["pub-7"],
    strike_limit: 3,
    strike_window_months: 12,
    ...over,
  };
}

describe("moderation queue", () => {
  it("renders Access denied when the API answers 403", async () => {
    stubFetch({
      "POST /v1/staff/refresh": staffSession([]),
      [`GET ${Q}`]: [403, { error: "admin_access_denied", message: "denied" }],
    });
    renderAt("/moderation");
    expect(await screen.findByText("Access denied")).toBeInTheDocument();
    expect(screen.queryByRole("table")).not.toBeInTheDocument();
  });

  it("lists the open notices and reports, the actions, the counter-notices and the strikes", async () => {
    stubFetch({ "POST /v1/staff/refresh": staffSession(), [`GET ${Q}`]: [200, queue()] });
    renderAt("/moderation");
    expect(await screen.findByText("Studio A")).toBeInTheDocument();
    expect(screen.getByText("Asked for privacy")).toBeInTheDocument();
    expect(screen.getByText("1 unfounded notices before")).toBeInTheDocument();
    expect(screen.getByText("something illegal")).toBeInTheDocument();
    expect(screen.getByText("Urgent")).toBeInTheDocument();
    expect(screen.getByText("Applied")).toBeInTheDocument();
    expect(screen.getByText(/Restore from/)).toBeInTheDocument();
    expect(screen.getByText("Terminated")).toBeInTheDocument();
    expect(screen.getByText(/not ours/)).toBeInTheDocument();
    expect(
      screen.getByText("A publisher's broadcasting ends at 3 valid copyright notices in 12 months.")
    ).toBeInTheDocument();
  });

  it("takes down the programme a notice names, with its reason", async () => {
    const f = stubFetch({
      "POST /v1/staff/refresh": staffSession(),
      [`GET ${Q}`]: [200, queue()],
      [`POST ${Q}/actions`]: [201, { id: "a-2" }],
    });
    renderAt("/moderation");
    const u = userEvent.setup();
    await u.click(await screen.findByRole("button", { name: "Take down for notice n-1" }));
    const dialog = await screen.findByRole("dialog");
    const submit = within(dialog).getByRole("button", { name: "Take down" });
    expect(submit).toBeDisabled();
    expect(within(dialog).getByLabelText(/Channel \(cloud id\)/)).toHaveValue("ch-1");
    await u.type(within(dialog).getByLabelText(/Programme title/), "Film X");
    await u.click(within(dialog).getByLabelText(/It is on air now/));
    await u.type(within(dialog).getByLabelText(/Reason/), "valid notice");
    await u.click(submit);
    await waitFor(() => expect(f.find("POST", `${Q}/actions`)).toHaveLength(1));
    expect(f.find("POST", `${Q}/actions`)[0]!.body).toEqual({
      basis: "notice",
      notice_id: "n-1",
      scope: "item",
      channel: "ch-1",
      at: "2026-11-14T20:00:00.000Z",
      programme: "Film X",
      airing_now: true,
      reason: "valid notice",
    });
  });

  it("takes down a source for a report, and shows the API's refusal", async () => {
    const f = stubFetch({
      "POST /v1/staff/refresh": staffSession(),
      [`GET ${Q}`]: [200, queue()],
      [`POST ${Q}/actions`]: [409, { error: "edge_channel_unknown", message: "" }],
    });
    renderAt("/moderation");
    const u = userEvent.setup();
    await u.click(await screen.findByRole("button", { name: "Act on report r-1" }));
    const dialog = await screen.findByRole("dialog");
    await u.selectOptions(within(dialog).getByLabelText("Applies to"), "source");
    await u.selectOptions(within(dialog).getByLabelText("Source"), "speaker");
    await u.type(within(dialog).getByLabelText(/Source identifier/), "Reciter A");
    await u.type(within(dialog).getByLabelText(/Reason/), "illegal");
    await u.click(within(dialog).getByRole("button", { name: "Take down" }));
    expect(await within(dialog).findByText(/edge id is not known yet/)).toBeInTheDocument();
    expect(f.find("POST", `${Q}/actions`)[0]!.body).toEqual({
      basis: "report",
      report_id: "r-1",
      scope: "source",
      channel: "ch-1",
      source_kind: "speaker",
      source_ref: "Reciter A",
      reason: "illegal",
    });
  });

  it("rejects a notice as unfounded only with a reason", async () => {
    const f = stubFetch({
      "POST /v1/staff/refresh": staffSession(),
      [`GET ${Q}`]: [200, queue()],
      [`POST ${Q}/notices/n-1/reject`]: [204],
    });
    renderAt("/moderation");
    const u = userEvent.setup();
    await u.click(await screen.findByRole("button", { name: "Reject notice n-1" }));
    const dialog = await screen.findByRole("dialog");
    const confirm = within(dialog).getByRole("button", { name: "Reject" });
    expect(confirm).toBeDisabled();
    await u.click(within(dialog).getByLabelText(/Unfounded/));
    await u.type(within(dialog).getByLabelText(/Reason/), "no rights shown");
    await u.click(confirm);
    await waitFor(() => expect(f.find("POST", `${Q}/notices/n-1/reject`)).toHaveLength(1));
    expect(f.find("POST", `${Q}/notices/n-1/reject`)[0]!.body).toEqual({
      reason: "no rights shown",
      unfounded: true,
    });
  });

  it("restores a takedown, reading a closed window in the console's words", async () => {
    stubFetch({
      "POST /v1/staff/refresh": staffSession(),
      [`GET ${Q}`]: [200, queue()],
      [`POST ${Q}/actions/a-1/restore`]: [409, { error: "restore_window_closed", message: "" }],
    });
    renderAt("/moderation");
    const u = userEvent.setup();
    await u.click(await screen.findByRole("button", { name: "Restore a-1" }));
    const dialog = await screen.findByRole("dialog");
    await u.type(within(dialog).getByLabelText(/Reason/), "counter-notice");
    await u.click(within(dialog).getByRole("button", { name: "Restore" }));
    expect(await within(dialog).findByText(/10 business days have passed/)).toBeInTheDocument();
  });

  it("reinstates a terminated publisher and records a lawsuit", async () => {
    const f = stubFetch({
      "POST /v1/staff/refresh": staffSession(),
      [`GET ${Q}`]: [200, queue()],
      [`POST ${Q}/publishers/pub-7/reinstate`]: [204],
      [`POST ${Q}/counter-notices/c-1/lawsuit`]: [204],
    });
    renderAt("/moderation");
    const u = userEvent.setup();
    await u.click(await screen.findByRole("button", { name: "Reinstate publisher pub-7" }));
    let dialog = await screen.findByRole("dialog");
    await u.type(within(dialog).getByLabelText(/Reason/), "appeal upheld");
    await u.click(within(dialog).getByRole("button", { name: "Reinstate" }));
    await waitFor(() => expect(f.find("POST", `${Q}/publishers/pub-7/reinstate`)).toHaveLength(1));

    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    await u.click(await screen.findByRole("button", { name: "Claimant went to court" }));
    dialog = await screen.findByRole("dialog");
    await u.type(within(dialog).getByLabelText(/Reason/), "court filing 123");
    await u.click(within(dialog).getByRole("button", { name: "Claimant went to court" }));
    await waitFor(() => expect(f.find("POST", `${Q}/counter-notices/c-1/lawsuit`)).toHaveLength(1));
  });

  it("enters a notice that came by email", async () => {
    const f = stubFetch({
      "POST /v1/staff/refresh": staffSession(),
      [`GET ${Q}`]: [200, queue({ notices: [], reports: [] })],
      [`POST ${Q}/notices`]: [202, { notice_id: "n-5" }],
    });
    renderAt("/moderation");
    const u = userEvent.setup();
    const enter = await screen.findByRole("button", { name: "Enter notice" });
    await u.type(screen.getByLabelText("Claimant's name"), "Studio B");
    await u.type(screen.getByLabelText("Claimant's email"), "b@studio.test");
    await u.type(screen.getByLabelText("The work"), "Film Z");
    await u.type(
      screen.getByLabelText(/Channel links/),
      "https://watch.maktaba.tv/a/b https://other.test/x"
    );
    await u.type(screen.getByLabelText("Signature"), "B");
    expect(enter).toBeDisabled();
    await u.click(screen.getByLabelText(/good-faith and accuracy/));
    await u.click(enter);
    await waitFor(() => expect(f.find("POST", `${Q}/notices`)).toHaveLength(1));
    expect(f.find("POST", `${Q}/notices`)[0]!.body).toMatchObject({
      claimant_name: "Studio B",
      urls: ["https://watch.maktaba.tv/a/b", "https://other.test/x"],
      good_faith: true,
      accuracy: true,
    });
  });
});
