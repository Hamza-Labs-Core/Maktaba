// Epic 57 story 57.08 on the web: a child's activity for its guardians. The list
// is paged by the home's cursor; a time event carries minutes and no subject.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { get } = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get } };
});

import { ParentalFixture } from "../../lib/parental";
import { forgetAnswers } from "../../lib/pageCache";
import { renderPage, memberUser } from "../../test/pageHarness";
import type { User } from "../../lib/auth";
import { ActivityPage } from "./Activity";
import { CHILD, familyGet, type FamilyHome } from "./familyHome";

const guardian: User = { id: "u-guardian", username: "mum", is_admin: false };

const first = [
  { id: "e1", kind: "time_up", at: "2026-11-11T18:00:00Z", minutes: 60 },
  {
    id: "e2",
    kind: "time_granted",
    at: "2026-11-11T17:00:00Z",
    minutes: 30,
    guardian: { id: "g1", username: "mum" },
  },
  {
    id: "e3",
    kind: "blocked",
    at: "2026-11-11T16:00:00Z",
    subject: { kind: "video", id: "v1", title: "Night Film", tier: 18 },
  },
  {
    id: "e4",
    kind: "asked",
    at: "2026-11-11T15:00:00Z",
    subject: { kind: "time", id: "45" },
    minutes: 45,
  },
  { id: "e5", kind: "signal", at: "2026-11-11T14:00:00Z", device: "ios" },
  {
    id: "e6",
    kind: "watched",
    at: "2026-11-11T13:00:00Z",
    subject: { kind: "video", id: "v2", hidden: true },
  },
];

function open(h: FamilyHome, user: User = guardian, locale: "en" | "ar" = "en") {
  localStorage.setItem("mkt:locale", locale);
  get.mockImplementation(familyGet(h));
  return renderPage(
    <ParentalFixture value={{ children: [{ id: CHILD, username: "sara", maxTier: 9 }] }}>
      <ActivityPage />
    </ParentalFixture>,
    { path: `/family/${CHILD}/activity`, route: "/family/:userId/activity", user }
  );
}

beforeEach(() => {
  get.mockReset();
  localStorage.clear();
  forgetAnswers("parental:");
});

describe("Activity", () => {
  it("lists the events with their kinds; time events show minutes and no subject", async () => {
    open({ activity: () => ({ events: first }) });
    const table = await screen.findByTestId("family-activity");
    const rows = within(table).getAllByRole("row").slice(1);
    expect(rows).toHaveLength(6);
    expect(rows[0]).toHaveTextContent("Time was up");
    expect(rows[0]).toHaveTextContent("1 hour");
    expect(rows[1]).toHaveTextContent("More time given");
    expect(rows[1]).toHaveTextContent("30 minutes · mum");
    expect(rows[2]).toHaveTextContent("Blocked");
    expect(rows[2]).toHaveTextContent("Night Film");
    // a request for more time: the minutes, never "time" as a title
    expect(rows[3]).toHaveTextContent("Asked");
    expect(rows[3]).toHaveTextContent("45 minutes");
    expect(rows[4]).toHaveTextContent("Device asked for a looser limit");
    expect(rows[4]).toHaveTextContent("ios");
    expect(rows[5]).toHaveTextContent("In a library you cannot open");
    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("Activity: sara");
    // one page only: nothing more to load
    expect(screen.queryByRole("button", { name: "Load more" })).toBeNull();
  });

  it("loads the next page with the home's cursor", async () => {
    open({
      activity: (q) =>
        q.get("cursor") === "next-1"
          ? { events: [{ id: "e9", kind: "denied", at: "2026-11-10T10:00:00Z" }] }
          : { events: first.slice(0, 2), next_cursor: "next-1" },
    });
    await screen.findByTestId("family-activity");
    await userEvent.click(screen.getByRole("button", { name: "Load more" }));
    await waitFor(() =>
      expect(within(screen.getByTestId("family-activity")).getAllByRole("row")).toHaveLength(4)
    );
    expect(get).toHaveBeenCalledWith(`/api/users/${CHILD}/parental/activity?cursor=next-1`);
    expect(screen.queryByRole("button", { name: "Load more" })).toBeNull();
  });

  it("a quiet re-read of the first page keeps the pages already loaded", async () => {
    const h: FamilyHome = {
      activity: (q) =>
        q.get("cursor") === "next-1"
          ? { events: [{ id: "e9", kind: "denied", at: "2026-11-10T10:00:00Z" }] }
          : { events: first.slice(0, 2), next_cursor: "next-1" },
    };
    // the page was open before: its kept answer shows at once, and is read again
    const before = open(h);
    await screen.findByTestId("family-activity");
    before.unmount();
    let land: () => void = () => {};
    get.mockImplementation((url: string) =>
      url === `/api/users/${CHILD}/parental/activity`
        ? new Promise((resolve) => {
            land = () => resolve({ events: first.slice(0, 2), next_cursor: "next-1" });
          })
        : familyGet(h)(url)
    );
    renderPage(
      <ParentalFixture value={{ children: [{ id: CHILD, username: "sara", maxTier: 9 }] }}>
        <ActivityPage />
      </ParentalFixture>,
      { path: `/family/${CHILD}/activity`, route: "/family/:userId/activity", user: guardian }
    );
    await screen.findByTestId("family-activity");
    await userEvent.click(screen.getByRole("button", { name: "Load more" }));
    const rows = () => within(screen.getByTestId("family-activity")).getAllByRole("row");
    await waitFor(() => expect(rows()).toHaveLength(4));
    // the re-read lands after the second page did
    land();
    await new Promise((resolve) => setTimeout(resolve, 20));
    expect(rows()).toHaveLength(4);
    expect(screen.queryByRole("button", { name: "Load more" })).toBeNull();
  });

  it("filters by kind, asking the home", async () => {
    open({
      activity: (q) => ({
        events: first.filter((e) => !q.get("kind") || e.kind === q.get("kind")),
      }),
    });
    await screen.findByTestId("family-activity");
    await userEvent.selectOptions(screen.getByLabelText("Show"), "time_granted");
    await waitFor(() =>
      expect(within(screen.getByTestId("family-activity")).getAllByRole("row")).toHaveLength(2)
    );
    expect(get).toHaveBeenCalledWith(`/api/users/${CHILD}/parental/activity?kind=time_granted`);
  });

  it("says when there is nothing", async () => {
    open({ activity: () => ({ events: [] }) });
    expect(await screen.findByText("Nothing here yet")).toBeInTheDocument();
  });

  it("someone who does not look after the account is told so", async () => {
    // the home answers 404, as for an account that is not there
    open({}, memberUser);
    expect(
      await screen.findByText("This account is not restricted, or it is not one you look after.")
    ).toBeInTheDocument();
    expect(screen.queryByTestId("family-activity")).toBeNull();
  });

  it("is in Arabic, right to left", async () => {
    open({ activity: () => ({ events: first }) }, guardian, "ar");
    const table = await screen.findByTestId("family-activity");
    expect(document.documentElement).toHaveAttribute("dir", "rtl");
    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("النشاط");
    expect(within(table).getByText("انتهى الوقت")).toBeInTheDocument();
    expect(within(table).getByText("مُنح وقت إضافي")).toBeInTheDocument();
  });
});
