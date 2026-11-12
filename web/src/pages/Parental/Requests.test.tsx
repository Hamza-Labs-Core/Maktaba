// Epic 57 family settings on the web: the requests that wait, and a guardian's
// answers. One thing is approved for a while (an allow rule); a request for more
// time is approved with minutes (a time grant). The home refuses whoever may not.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post } };
});

import { ParentalFixture } from "../../lib/parental";
import { forgetAnswers } from "../../lib/pageCache";
import { renderPage, memberUser } from "../../test/pageHarness";
import type { User } from "../../lib/auth";
import { RequestsPage } from "./Requests";
import { familyGet, problem, type FamilyHome } from "./familyHome";

const guardian: User = { id: "u-guardian", username: "mum", is_admin: false };
const refresh = vi.fn();
const child = { id: "c1", username: "sara" };
const content = {
  id: "q1",
  child_id: "c1",
  subject_kind: "video",
  subject_id: "v1",
  status: "pending",
  created_at: "2026-11-11T10:00:00Z",
  child,
  subject: {
    kind: "video",
    id: "v1",
    title: "Moon Landing",
    tier: 13,
    poster_url: "/api/videos/v1/poster",
  },
};
const time = {
  id: "q2",
  child_id: "c1",
  subject_kind: "time",
  subject_id: "45",
  minutes: 45,
  status: "pending",
  created_at: "2026-11-11T11:00:00Z",
  child,
  subject: { kind: "time", id: "45" },
};

function open(h: FamilyHome, user: User = guardian, locale: "en" | "ar" = "en") {
  localStorage.setItem("mkt:locale", locale);
  get.mockImplementation(familyGet(h));
  return renderPage(
    <ParentalFixture value={{ refresh }}>
      <RequestsPage />
    </ParentalFixture>,
    { path: "/family/requests", user }
  );
}

beforeEach(() => {
  for (const f of [get, post, refresh]) f.mockReset();
  localStorage.clear();
  forgetAnswers("parental:");
});

describe("Requests", () => {
  it("lists what waits: the title, the kind, the rating, who asked", async () => {
    open({ requests: [content, time] });
    const rows = await screen.findAllByTestId("family-request");
    expect(get).toHaveBeenCalledWith("/api/parental/requests?status=pending");
    expect(rows[0]).toHaveTextContent("Moon Landing");
    expect(rows[0]).toHaveTextContent("Video");
    expect(rows[0]).toHaveTextContent("13+");
    expect(rows[0]).toHaveTextContent(/Asked by sara/);
    expect(rows[1]).toHaveTextContent("More time today: 45 minutes");
  });

  it("approves one thing for today by default: an expiry before tomorrow", async () => {
    open({ requests: [content] });
    const row = await screen.findByTestId("family-request");
    expect(within(row).getByLabelText("Allow")).toHaveValue("today");
    post.mockResolvedValue({ request: { ...content, status: "approved" } });
    await userEvent.click(within(row).getByRole("button", { name: "Approve" }));
    const [path, body] = post.mock.calls[0] as [string, { expires_at: string }];
    expect(path).toBe("/api/parental/requests/q1/approve");
    const end = Date.parse(body.expires_at);
    expect(end).toBeGreaterThan(Date.now());
    expect(end - Date.now()).toBeLessThanOrEqual(24 * 60 * 60 * 1000);
    await waitFor(() => expect(screen.queryByTestId("family-request")).toBeNull());
    // the count beside Family is read again
    expect(refresh).toHaveBeenCalled();
  });

  it("approves one thing with no end when Always is chosen", async () => {
    open({ requests: [content] });
    const row = await screen.findByTestId("family-request");
    await userEvent.selectOptions(within(row).getByLabelText("Allow"), "always");
    post.mockResolvedValue({ request: { ...content, status: "approved" } });
    await userEvent.click(within(row).getByRole("button", { name: "Approve" }));
    expect(post).toHaveBeenCalledWith("/api/parental/requests/q1/approve", {});
  });

  it("answers a request for more time with minutes: what was asked, or another amount", async () => {
    open({ requests: [time] });
    const row = await screen.findByTestId("family-request");
    const give = within(row).getByLabelText("Time to give");
    // the minutes asked for are offered, and chosen
    expect(give).toHaveValue("45");
    expect(within(row).queryByLabelText("Allow")).toBeNull();
    await userEvent.selectOptions(give, "30");
    post.mockResolvedValue({ request: { ...time, status: "approved" }, grant: { minutes: 30 } });
    await userEvent.click(within(row).getByRole("button", { name: "Approve" }));
    expect(post).toHaveBeenCalledWith("/api/parental/requests/q2/approve", { minutes: 30 });
    expect(await screen.findByText("Added for today: 30 minutes.")).toBeVisible();
  });

  it("denies", async () => {
    open({ requests: [content, time] });
    const rows = await screen.findAllByTestId("family-request");
    post.mockResolvedValue({ request: { ...content, status: "denied" } });
    await userEvent.click(within(rows[0]).getByRole("button", { name: "Deny" }));
    expect(post).toHaveBeenCalledWith("/api/parental/requests/q1/deny");
    await waitFor(() => expect(screen.getAllByTestId("family-request")).toHaveLength(1));
  });

  it("when someone else answered first, says so and reads the list again", async () => {
    const h: FamilyHome = { requests: [content] };
    open(h);
    const row = await screen.findByTestId("family-request");
    post.mockRejectedValue(problem(409, { code: "request_decided" }));
    h.requests = [];
    await userEvent.click(within(row).getByRole("button", { name: "Approve" }));
    expect(await screen.findByText("Someone already answered this request.")).toBeVisible();
    expect(await screen.findByText("No requests waiting")).toBeInTheDocument();
  });

  it("a subject the guardian cannot open shows no title", async () => {
    open({
      requests: [{ ...content, subject: { kind: "video", id: "v1", hidden: true } }],
    });
    const row = await screen.findByTestId("family-request");
    expect(row).toHaveTextContent("In a library you cannot open");
    expect(row).not.toHaveTextContent("Moon Landing");
  });

  it("someone the home refuses sees its refusal, and no list", async () => {
    open({ requests: problem(403) }, memberUser);
    expect(await screen.findByRole("alert")).toBeInTheDocument();
    expect(screen.queryByTestId("family-requests")).toBeNull();
  });

  it("is in Arabic, right to left", async () => {
    open({ requests: [content, time] }, guardian, "ar");
    const rows = await screen.findAllByTestId("family-request");
    expect(document.documentElement).toHaveAttribute("dir", "rtl");
    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("الطلبات");
    expect(within(rows[0]).getByRole("button", { name: "موافقة" })).toBeInTheDocument();
    expect(within(rows[0]).getByRole("button", { name: "رفض" })).toBeInTheDocument();
    expect(rows[1]).toHaveTextContent(/وقت إضافي اليوم/);
  });
});
