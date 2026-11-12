// Epic 57 story 57.15 (T04): the time rules editor on the web, as an admin or a
// guardian uses it and as a viewer meets it. The home decides who may read and
// write; the page shows the home's refusal and offers no editor then.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { get, post, put } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post, put } };
});

import { ApiError } from "../../lib/api";
import { ParentalFixture } from "../../lib/parental";
import { forgetAnswers } from "../../lib/pageCache";
import type { TimeRule } from "../../lib/parentalApi";
import { renderPage, memberUser, adminUser } from "../../test/pageHarness";
import type { User } from "../../lib/auth";
import { TimeRulesPage, ruleProblem } from "./TimeRules";
import { FamilyTime } from "./FamilyTime";

const CHILD = "11111111-2222-3333-4444-555555555555";
const guardian: User = { id: "u-guardian", username: "mum", is_admin: false };
const problem = (status: number, extra: Record<string, unknown> = {}) =>
  new ApiError({ type: "about:blank", title: "refused", status, ...extra } as never);

interface Home {
  rules?: unknown[] | ApiError;
  usage?: unknown | ApiError;
  children?: unknown[];
}

function home(h: Home = {}) {
  get.mockImplementation((path: string) => {
    if (path.endsWith("/time-rules")) {
      if (h.rules instanceof ApiError) return Promise.reject(h.rules);
      return Promise.resolve({ rules: h.rules ?? [], time_zone: "Asia/Riyadh" });
    }
    if (path.endsWith("/time-usage")) {
      if (h.usage instanceof ApiError) return Promise.reject(h.usage);
      return Promise.resolve(
        h.usage ?? { today: "2026-11-11", time_zone: "Asia/Riyadh", usage: [], grants: [] }
      );
    }
    if (path === "/api/libraries/summary")
      return Promise.resolve({ items: [{ id: "lib-1", name: "Cartoons", videos: 3 }] });
    if (path === "/api/parental/children") return Promise.resolve({ children: h.children ?? [] });
    return Promise.reject(problem(404));
  });
}

function open(user: User = guardian, locale: "en" | "ar" = "en") {
  localStorage.setItem("mkt:locale", locale);
  return renderPage(
    <ParentalFixture value={{ children: [{ id: CHILD, username: "sara", maxTier: 9 }] }}>
      <TimeRulesPage />
    </ParentalFixture>,
    { path: `/family/${CHILD}/time`, route: "/family/:userId/time", user }
  );
}

const rule = (over: Partial<TimeRule> = {}): TimeRule => ({
  libraryId: "",
  days: 127,
  slotStart: "",
  slotEnd: "",
  dailyMinutes: 60,
  ...over,
});

beforeEach(() => {
  for (const f of [get, post, put]) f.mockReset();
  localStorage.clear();
  forgetAnswers("parental:");
});

describe("ruleProblem", () => {
  it("accepts a slot, a daily limit, or both", () => {
    expect(ruleProblem(rule())).toBeNull();
    expect(
      ruleProblem(rule({ slotStart: "07:00", slotEnd: "20:00", dailyMinutes: null }))
    ).toBeNull();
    // a slot may cross midnight
    expect(ruleProblem(rule({ slotStart: "22:00", slotEnd: "06:00" }))).toBeNull();
    expect(ruleProblem(rule({ dailyMinutes: 0 }))).toBeNull();
  });

  it("names what is wrong", () => {
    expect(ruleProblem(rule({ days: 0 }))).toBe("parental.time.error.days");
    expect(ruleProblem(rule({ slotStart: "07:00" }))).toBe("parental.time.error.slot");
    expect(ruleProblem(rule({ slotStart: "07:00", slotEnd: "07:00" }))).toBe(
      "parental.time.error.slot"
    );
    expect(ruleProblem(rule({ dailyMinutes: 1.5 }))).toBe("parental.time.error.daily");
    expect(ruleProblem(rule({ dailyMinutes: 2000 }))).toBe("parental.time.error.daily");
    expect(ruleProblem(rule({ dailyMinutes: NaN }))).toBe("parental.time.error.daily");
    expect(ruleProblem(rule({ dailyMinutes: null }))).toBe("parental.time.error.empty");
  });
});

describe("the time rules editor (Users > the user > Time)", () => {
  it("a guardian sees the child's rules, in the home's time zone", async () => {
    home({
      rules: [{ id: "r1", days: 127, slot_start: "07:00", slot_end: "20:00", daily_minutes: 60 }],
    });
    open();
    expect(
      await screen.findByRole("heading", { level: 1, name: /Time: sara/ })
    ).toBeInTheDocument();
    expect(screen.getByText(/Asia\/Riyadh/)).toBeInTheDocument();
    const r = screen.getByTestId("parental-rule");
    expect(within(r).getByLabelText("From")).toHaveValue("07:00");
    expect(within(r).getByLabelText("To")).toHaveValue("20:00");
    expect(within(r).getByLabelText("Minutes a day")).toHaveValue(60);
    for (const day of ["Sunday", "Wednesday", "Saturday"])
      expect(within(r).getByLabelText(day)).toBeChecked();
    expect(within(r).getByLabelText("Applies to")).toHaveValue("");
    // nothing changed yet: nothing to save
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
    expect(get).toHaveBeenCalledWith(`/api/parental/users/${CHILD}/time-rules`);
  });

  it("a guardian sets a rule: the first one starts at one hour, 07:00 to 20:00", async () => {
    home();
    put.mockResolvedValue({
      rules: [{ id: "r1", days: 127, slot_start: "07:00", slot_end: "20:00", daily_minutes: 60 }],
      time_zone: "Asia/Riyadh",
    });
    open();
    expect(await screen.findByTestId("parental-time-none")).toBeInTheDocument();
    await userEvent.click(
      screen.getByRole("button", { name: "Start with 1 hour a day, 07:00 to 20:00" })
    );
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() =>
      expect(put).toHaveBeenCalledWith(`/api/parental/users/${CHILD}/time-rules`, {
        rules: [{ days: 127, slot_start: "07:00", slot_end: "20:00", daily_minutes: 60 }],
      })
    );
    expect(await screen.findByText("Time rules saved.")).toBeInTheDocument();
    await waitFor(() => expect(screen.getByRole("button", { name: "Save" })).toBeDisabled());
  });

  it("edits days, the daily limit and the library, and saves what the form shows", async () => {
    home({ rules: [{ id: "r1", days: 127, daily_minutes: 60 }] });
    put.mockResolvedValue({ rules: [], time_zone: "Asia/Riyadh" });
    open(adminUser);
    const r = await screen.findByTestId("parental-rule");
    await userEvent.click(within(r).getByLabelText("Friday"));
    await userEvent.click(within(r).getByLabelText("Saturday"));
    const minutes = within(r).getByLabelText("Minutes a day");
    await userEvent.clear(minutes);
    await userEvent.type(minutes, "45");
    await userEvent.selectOptions(within(r).getByLabelText("Applies to"), "lib-1");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(put).toHaveBeenCalledTimes(1));
    // Sunday to Thursday: bits 0 to 4
    expect(put.mock.calls[0]![1]).toEqual({
      rules: [{ days: 31, library_id: "lib-1", daily_minutes: 45 }],
    });
  });

  it("will not save a rule with no day, and says why", async () => {
    home({ rules: [{ id: "r1", days: 1, daily_minutes: 60 }] });
    open();
    const r = await screen.findByTestId("parental-rule");
    await userEvent.click(within(r).getByLabelText("Sunday"));
    expect(within(r).getByRole("alert")).toHaveTextContent("Choose at least one day.");
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
    expect(put).not.toHaveBeenCalled();
  });

  it("removing every rule saves an empty list: no time limit", async () => {
    home({ rules: [{ id: "r1", days: 127, daily_minutes: 60 }] });
    put.mockResolvedValue({ rules: [], time_zone: "Asia/Riyadh" });
    open();
    await screen.findByTestId("parental-rule");
    await userEvent.click(screen.getByRole("button", { name: "Remove rule" }));
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() =>
      expect(put).toHaveBeenCalledWith(`/api/parental/users/${CHILD}/time-rules`, { rules: [] })
    );
  });

  it("says a slot that ends before it starts runs past midnight", async () => {
    home({ rules: [{ id: "r1", days: 127, slot_start: "21:00", slot_end: "06:00" }] });
    open();
    expect(await screen.findByText("These hours run past midnight.")).toBeInTheDocument();
  });

  it("shows the home's refusal of a save, and keeps the form", async () => {
    home({ rules: [{ id: "r1", days: 127, daily_minutes: 60 }] });
    put.mockRejectedValue(problem(403));
    open();
    const r = await screen.findByTestId("parental-rule");
    await userEvent.click(within(r).getByLabelText("Friday"));
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(
      await screen.findByText(
        "Only an administrator or this account's guardian can change its time."
      )
    ).toBeInTheDocument();
    expect(within(r).getByLabelText("Friday")).not.toBeChecked();

    put.mockRejectedValue(problem(422));
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(
      await screen.findByText(
        "Your home did not accept these rules. Check each rule and try again."
      )
    ).toBeInTheDocument();
  });

  it("shows today's watching and the last days, and gives more time", async () => {
    const usage = {
      today: "2026-11-11",
      time_zone: "Asia/Riyadh",
      usage: [
        { day: "2026-11-11", library_id: "lib-1", seconds: 2400 },
        { day: "2026-11-10", library_id: "lib-1", seconds: 3600 },
      ],
      grants: [] as unknown[],
    };
    home({ rules: [{ id: "r1", days: 127, daily_minutes: 60 }], usage });
    post.mockImplementation(() => {
      usage.grants = [{ id: "g1", day: "2026-11-11", minutes: 30, used_seconds: 0 }];
      return Promise.resolve({ id: "g1", day: "2026-11-11", minutes: 30, used_seconds: 0 });
    });
    open();
    expect(await screen.findByTestId("parental-time-today")).toHaveTextContent(
      "Watched today: 40 minutes."
    );
    const table = screen.getByRole("table");
    expect(within(table).getByRole("row", { name: /Tuesday.*1 hour/ })).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "30 minutes more" }));
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith(`/api/parental/users/${CHILD}/time-grants`, { minutes: 30 })
    );
    expect(await screen.findByText("Added for today: 30 minutes.")).toBeInTheDocument();
    await waitFor(() =>
      expect(screen.getByTestId("parental-time-today")).toHaveTextContent(
        "Extra time given today: 30 minutes."
      )
    );
  });

  it("a viewer has no page: the home answers 403 and no editor shows", async () => {
    home({ rules: problem(403) });
    open(memberUser);
    expect(await screen.findByRole("alert")).toBeInTheDocument();
    expect(screen.queryByTestId("parental-rule")).toBeNull();
    expect(screen.queryByRole("button", { name: "Save" })).toBeNull();
    expect(screen.queryByRole("button", { name: /minutes more/ })).toBeNull();
    // the page asked for the rules only: nothing else of the child was read
    expect(get).toHaveBeenCalledTimes(1);
    expect(put).not.toHaveBeenCalled();
  });

  it("a guardian of another child, or an account that is not restricted: the home's 404", async () => {
    home({ rules: problem(404) });
    open();
    expect(
      await screen.findByText("This account is not restricted, or it is not one you look after.")
    ).toBeInTheDocument();
    expect(screen.queryByTestId("parental-rule")).toBeNull();
  });

  it("is Arabic in Arabic: the days, the fields, the page right to left", async () => {
    home({
      rules: [{ id: "r1", days: 127, slot_start: "07:00", slot_end: "20:00", daily_minutes: 60 }],
    });
    open(guardian, "ar");
    const r = await screen.findByTestId("parental-rule");
    expect(document.documentElement).toHaveAttribute("dir", "rtl");
    expect(within(r).getByLabelText("الجمعة")).toBeChecked();
    expect(within(r).getByText("ساعات المشاهدة")).toBeInTheDocument();
    expect(within(r).getByText("الحد اليومي")).toBeInTheDocument();
    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("الوقت");
    expect(screen.getByRole("button", { name: "حفظ" })).toBeInTheDocument();
  });
});

describe("the list of restricted accounts", () => {
  function list(user: User = guardian) {
    return renderPage(<FamilyTime />, { path: "/family", user });
  }

  it("lists the accounts the home says this person looks after, each with its Time link", async () => {
    home({
      children: [
        { id: "u2", username: "omar", max_tier: 0 },
        { id: CHILD, username: "sara", max_tier: 13 },
      ],
    });
    list();
    const links = await screen.findAllByRole("link", { name: /Time/ });
    expect(links.map((a) => a.getAttribute("href"))).toEqual([
      "/family/u2/time",
      `/family/${CHILD}/time`,
    ]);
    expect(links[0]).toHaveTextContent("omar");
    expect(links[0]).toHaveTextContent("All ages");
    expect(links[1]).toHaveTextContent("13+");
  });

  it("someone who looks after no one sees an empty list", async () => {
    home({ children: [] });
    list(memberUser);
    expect(await screen.findByText("No restricted accounts")).toBeInTheDocument();
    expect(screen.queryByTestId("parental-children")).toBeNull();
  });
});
