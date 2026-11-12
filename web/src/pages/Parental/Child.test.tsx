// Epic 57 family settings on the web: one account's page, as an admin, its guardian
// and a stranger meet it. The home decides every role: the page offers the admin's
// controls only when the home says `access: admin`, and shows the home's refusal.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Route } from "react-router-dom";

const { get, post, put, del } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  put: vi.fn(),
  del: vi.fn(),
}));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post, put, delete: del } };
});

import { ParentalFixture } from "../../lib/parental";
import { forgetAnswers } from "../../lib/pageCache";
import { renderPage, memberUser, adminUser } from "../../test/pageHarness";
import type { User } from "../../lib/auth";
import { ChildPage } from "./Child";
import { forgetSubjectNames } from "./subjects";
import { tierKey } from "../../lib/parentalApi";
import ar from "../../i18n/ar.json";
import { CHILD, familyGet, policyAnswer, problem, type FamilyHome } from "./familyHome";

const guardian: User = { id: "u-guardian", username: "mum", is_admin: false };
const refresh = vi.fn();

function open(h: FamilyHome, user: User = adminUser, locale: "en" | "ar" = "en") {
  localStorage.setItem("mkt:locale", locale);
  get.mockImplementation(familyGet(h));
  return renderPage(
    <ParentalFixture value={{ children: [{ id: CHILD, username: "sara", maxTier: 9 }], refresh }}>
      <ChildPage />
    </ParentalFixture>,
    {
      path: `/family/${CHILD}`,
      route: "/family/:userId",
      user,
      extraRoutes: <Route path="/family" element={<p>the family page</p>} />,
    }
  );
}

beforeEach(() => {
  for (const f of [get, post, put, del, refresh]) f.mockReset();
  localStorage.clear();
  forgetAnswers("parental:");
  forgetSubjectNames();
});

describe("making an account restricted", () => {
  it("an admin sets the limit of an unrestricted account: the first PUT has version 0", async () => {
    const h: FamilyHome = { accounts: [{ id: CHILD, username: "sara" }] };
    open(h);
    expect(await screen.findByRole("heading", { level: 1 })).toHaveTextContent(
      "Make an account restricted: sara"
    );
    const form = screen.getByTestId("family-policy");
    await userEvent.selectOptions(within(form).getByLabelText(/Content limit/), "13");
    await userEvent.selectOptions(within(form).getByLabelText(/Unrated content/), "tier_9");
    await userEvent.click(within(form).getByLabelText("Only allowed items"));
    put.mockResolvedValue(
      policyAnswer({
        policy: { user_id: CHILD, max_tier: 13, unrated: "tier_9", allow_only: true, version: 1 },
      })
    );
    await userEvent.click(within(form).getByRole("button", { name: "Make an account restricted" }));
    expect(put).toHaveBeenCalledWith(`/api/users/${CHILD}/parental`, {
      max_tier: 13,
      unrated: "tier_9",
      allow_only: true,
      version: 0,
    });
    // the page is now the restricted account's, and the shell's list is read again
    expect(await screen.findByTestId("family-guardians")).toBeInTheDocument();
    expect(refresh).toHaveBeenCalled();
  });

  it("says why the home refused an admin's own kind of account", async () => {
    open({ accounts: [{ id: CHILD, username: "sara" }] });
    put.mockRejectedValue(problem(409, { code: "restricted_admin" }));
    await userEvent.click(
      await screen.findByRole("button", { name: "Make an account restricted" })
    );
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "An administrator cannot be restricted."
    );
  });

  it("anyone else is told the account is not theirs, with no form", async () => {
    open({}, memberUser);
    expect(
      await screen.findByText("This account is not restricted, or it is not one you look after.")
    ).toBeInTheDocument();
    expect(screen.queryByTestId("family-policy")).toBeNull();
    expect(put).not.toHaveBeenCalled();
  });
});

describe("the limit", () => {
  it("shows the tiers youngest first, then nothing unless allowed", async () => {
    open({ policy: policyAnswer() });
    const form = await screen.findByTestId("family-policy");
    const limit = within(form).getByLabelText(/Content limit/);
    expect(
      within(limit)
        .getAllByRole("option")
        .map((o) => o.textContent)
    ).toEqual(["All ages", "9+", "13+", "16+", "18+", "Nothing unless allowed"]);
    expect(limit).toHaveValue("9");
    expect(within(form).getByRole("button", { name: "Save" })).toBeDisabled();
  });

  it("a guardian changes it, sending the version that was read", async () => {
    open({ policy: policyAnswer({ access: "guardian" }) }, guardian);
    const form = await screen.findByTestId("family-policy");
    await userEvent.selectOptions(within(form).getByLabelText(/Content limit/), "-1");
    put.mockResolvedValue(
      policyAnswer({
        access: "guardian",
        policy: { user_id: CHILD, max_tier: -1, unrated: "hide", allow_only: false, version: 4 },
      })
    );
    await userEvent.click(within(form).getByRole("button", { name: "Save" }));
    expect(put).toHaveBeenCalledWith(`/api/users/${CHILD}/parental`, {
      max_tier: -1,
      unrated: "hide",
      allow_only: false,
      version: 3,
    });
    await waitFor(() => expect(within(form).getByRole("button", { name: "Save" })).toBeDisabled());
  });

  it("when someone else changed it first, reads the latest instead of overwriting", async () => {
    const h: FamilyHome = { policy: policyAnswer() };
    open(h);
    const form = await screen.findByTestId("family-policy");
    await userEvent.selectOptions(within(form).getByLabelText(/Content limit/), "16");
    put.mockRejectedValue(problem(409, { code: "stale" }));
    h.policy = policyAnswer({
      policy: { user_id: CHILD, max_tier: 18, unrated: "hide", allow_only: false, version: 9 },
    });
    await userEvent.click(within(form).getByRole("button", { name: "Save" }));
    expect(await screen.findByText("Someone else changed this. Showing the latest.")).toBeVisible();
    await waitFor(() => expect(within(form).getByLabelText(/Content limit/)).toHaveValue("18"));
  });

  it("a sign-in through Maktaba Cloud that may only play is told where to change it", async () => {
    open({ policy: policyAnswer() });
    const form = await screen.findByTestId("family-policy");
    await userEvent.selectOptions(within(form).getByLabelText(/Content limit/), "16");
    put.mockRejectedValue(problem(403, { type: "https://maktaba.tv/problems/cloud-session" }));
    await userEvent.click(within(form).getByRole("button", { name: "Save" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(/Connect at home/);
  });
});

// The home moves the policy's version on with every rule and guardian change. The
// page reads the account again after one, so the next save of the limit carries the
// version the home has now, and what is being chosen in the form stays.
describe("the limit after a rule or a guardian changed", () => {
  const versioned = (version: number, over: Record<string, unknown> = {}) =>
    policyAnswer({
      policy: { user_id: CHILD, max_tier: 9, unrated: "hide", allow_only: false, version },
      ...over,
    });
  const policyReads = () =>
    get.mock.calls.filter((c) => c[0] === `/api/users/${CHILD}/parental`).length;

  it("saves with the version the home has after a rule was added, keeping the choice", async () => {
    const h: FamilyHome = { policy: versioned(3), libraries: [{ id: "lib-2", name: "Films" }] };
    open(h);
    const form = await screen.findByTestId("family-policy");
    // chosen before the rule is added: the read that follows must not undo it
    await userEvent.selectOptions(within(form).getByLabelText(/Content limit/), "13");
    const box = screen.getByTestId("family-rules");
    const item = await within(box).findByLabelText("Item");
    await waitFor(() => expect(item).toBeEnabled());
    await userEvent.selectOptions(item, "lib-2");
    post.mockResolvedValue({
      id: "r9",
      effect: "allow",
      subject_kind: "library",
      subject_id: "lib-2",
    });
    const before = policyReads();
    h.policy = versioned(4);
    await userEvent.click(within(box).getByRole("button", { name: "Add" }));
    await waitFor(() => expect(policyReads()).toBeGreaterThan(before));
    await waitFor(() => expect(within(box).getByRole("button", { name: "Add" })).toBeDisabled());
    expect(within(form).getByLabelText(/Content limit/)).toHaveValue("13");

    put.mockResolvedValue(
      policyAnswer({
        policy: { user_id: CHILD, max_tier: 13, unrated: "hide", allow_only: false, version: 5 },
      })
    );
    await userEvent.click(within(form).getByRole("button", { name: "Save" }));
    expect(put).toHaveBeenCalledWith(`/api/users/${CHILD}/parental`, {
      max_tier: 13,
      unrated: "hide",
      allow_only: false,
      version: 4,
    });
    expect(await screen.findByText("Limit saved.")).toBeVisible();
    expect(screen.queryByText("Someone else changed this. Showing the latest.")).toBeNull();
  });

  it("saves with the version the home has after a rule was removed", async () => {
    const h: FamilyHome = {
      policy: versioned(3),
      rules: [
        { id: "r1", effect: "allow", subject_kind: "library", subject_id: "lib-1", created_at: "x" },
      ],
      libraries: [{ id: "lib-1", name: "Cartoons" }],
    };
    open(h);
    const form = await screen.findByTestId("family-policy");
    const allowed = await screen.findByTestId("family-rules-allow");
    await within(allowed).findByText("Cartoons", { selector: "bdi" });
    del.mockResolvedValue(undefined);
    const before = policyReads();
    h.policy = versioned(4);
    await userEvent.click(within(allowed).getByRole("button", { name: /Remove/ }));
    await waitFor(() => expect(policyReads()).toBeGreaterThan(before));
    await userEvent.selectOptions(within(form).getByLabelText(/Content limit/), "16");
    put.mockResolvedValue(versioned(5));
    await waitFor(async () => {
      put.mockClear();
      await userEvent.click(within(form).getByRole("button", { name: "Save" }));
      expect(put).toHaveBeenCalledWith(
        `/api/users/${CHILD}/parental`,
        expect.objectContaining({ max_tier: 16, version: 4 })
      );
    });
  });

  it("saves with the version the home has after a guardian was named", async () => {
    const h: FamilyHome = {
      policy: versioned(3),
      accounts: [
        { id: CHILD, username: "sara" },
        { id: "g2", username: "dad" },
      ],
    };
    open(h);
    const form = await screen.findByTestId("family-policy");
    await userEvent.selectOptions(within(form).getByLabelText(/Content limit/), "13");
    const box = await screen.findByTestId("family-guardians");
    await userEvent.selectOptions(within(box).getByLabelText("Name a guardian"), "g2");
    post.mockResolvedValue({ guardians: [{ id: "g2", username: "dad" }] });
    const before = policyReads();
    h.policy = versioned(4, { guardians: [{ id: "g2", username: "dad" }] });
    await userEvent.click(within(box).getByRole("button", { name: "Add" }));
    await waitFor(() => expect(policyReads()).toBeGreaterThan(before));
    expect(await within(box).findByText("dad", { selector: "bdi" })).toBeInTheDocument();
    // the choice made before the guardian was named is still there
    expect(within(form).getByLabelText(/Content limit/)).toHaveValue("13");
    put.mockResolvedValue(versioned(5));
    await waitFor(async () => {
      put.mockClear();
      await userEvent.click(within(form).getByRole("button", { name: "Save" }));
      expect(put).toHaveBeenCalledWith(
        `/api/users/${CHILD}/parental`,
        expect.objectContaining({ max_tier: 13, version: 4 })
      );
    });
  });

  it("a quiet re-read that says the same limit keeps what is being chosen", async () => {
    // the page was open before: its kept answer shows at once, and is read again
    const h: FamilyHome = { policy: versioned(3) };
    const first = open(h);
    await screen.findByTestId("family-policy");
    first.unmount();
    let land: (v: unknown) => void = () => {};
    get.mockImplementation((url: string) =>
      url === `/api/users/${CHILD}/parental`
        ? new Promise((resolve) => {
            land = resolve;
          })
        : familyGet(h)(url)
    );
    renderPage(
      <ParentalFixture value={{ children: [{ id: CHILD, username: "sara", maxTier: 9 }] }}>
        <ChildPage />
      </ParentalFixture>,
      { path: `/family/${CHILD}`, route: "/family/:userId", user: adminUser }
    );
    const form = await screen.findByTestId("family-policy");
    await userEvent.selectOptions(within(form).getByLabelText(/Content limit/), "16");
    // the re-read lands while the form is being changed
    land(versioned(3));
    await waitFor(() => expect(screen.getByTestId("family-policy")).toBeInTheDocument());
    await new Promise((r) => setTimeout(r, 20));
    expect(within(form).getByLabelText(/Content limit/)).toHaveValue("16");
    expect(within(form).getByRole("button", { name: "Save" })).toBeEnabled();
  });
});

describe("what only an admin gets", () => {
  it("a guardian sees who the guardians are, and no way to name one or lift", async () => {
    open(
      {
        policy: policyAnswer({
          access: "guardian",
          guardians: [{ id: guardian.id, username: "mum" }],
        }),
      },
      guardian
    );
    await screen.findByTestId("family-policy");
    expect(screen.getByText("mum")).toBeInTheDocument();
    expect(screen.queryByTestId("family-guardians")).toBeNull();
    expect(screen.queryByRole("button", { name: "Lift restriction" })).toBeNull();
    expect(get.mock.calls.map((c) => c[0])).not.toContain("/api/admin/library-acl");
  });

  it("an admin names a guardian and removes one", async () => {
    const h: FamilyHome = {
      policy: policyAnswer({ guardians: [{ id: "g1", username: "mum" }] }),
      accounts: [
        { id: CHILD, username: "sara" },
        { id: "g1", username: "mum" },
        { id: "g2", username: "dad" },
      ],
    };
    open(h);
    const box = await screen.findByTestId("family-guardians");
    const select = within(box).getByLabelText("Name a guardian");
    // not the child, not someone who is a guardian already
    expect(
      within(select)
        .getAllByRole("option")
        .map((o) => o.textContent)
    ).toEqual(["Choose an account", "dad"]);
    post.mockResolvedValue({
      guardians: [
        { id: "g2", username: "dad" },
        { id: "g1", username: "mum" },
      ],
    });
    await userEvent.selectOptions(select, "g2");
    // the home's own answer from here on (the page reads the account again)
    h.policy = policyAnswer({
      policy: { user_id: CHILD, max_tier: 9, unrated: "hide", allow_only: false, version: 4 },
      guardians: [
        { id: "g2", username: "dad" },
        { id: "g1", username: "mum" },
      ],
    });
    await userEvent.click(within(box).getByRole("button", { name: "Add" }));
    expect(post).toHaveBeenCalledWith(`/api/users/${CHILD}/parental/guardians`, {
      guardian_id: "g2",
    });
    expect(await within(box).findByText("dad", { selector: "bdi" })).toBeInTheDocument();
    del.mockResolvedValue(undefined);
    h.policy = policyAnswer({
      policy: { user_id: CHILD, max_tier: 9, unrated: "hide", allow_only: false, version: 5 },
      guardians: [{ id: "g2", username: "dad" }],
    });
    await userEvent.click(within(box).getByRole("button", { name: /Remove mum/ }));
    expect(del).toHaveBeenCalledWith(`/api/users/${CHILD}/parental/guardians/g1`);
    await waitFor(() => expect(within(box).queryByText("mum", { selector: "bdi" })).toBeNull());
  });

  it("says why a restricted account cannot be a guardian", async () => {
    open({ policy: policyAnswer(), accounts: [{ id: "g2", username: "omar" }] });
    const box = await screen.findByTestId("family-guardians");
    post.mockRejectedValue(problem(409, { code: "restricted_guardian" }));
    await userEvent.selectOptions(within(box).getByLabelText("Name a guardian"), "g2");
    await userEvent.click(within(box).getByRole("button", { name: "Add" }));
    expect(await within(box).findByRole("alert")).toHaveTextContent(
      /A restricted account cannot look after another account/
    );
  });

  it("lifting says what is removed and asks first", async () => {
    open({ policy: policyAnswer() });
    await userEvent.click(await screen.findByRole("button", { name: "Lift restriction" }));
    const dialog = await screen.findByRole("dialog");
    expect(dialog).toHaveTextContent("sara will see everything their libraries hold.");
    const removed = within(dialog).getByTestId("family-lift-removes");
    for (const what of [
      /Allowed and Blocked lists/,
      /request/,
      /activity/,
      /time rules/,
      /guardians/,
    ])
      expect(removed).toHaveTextContent(what);
    expect(del).not.toHaveBeenCalled();
    del.mockResolvedValue(undefined);
    await userEvent.click(within(dialog).getByRole("button", { name: "Lift restriction" }));
    expect(del).toHaveBeenCalledWith(`/api/users/${CHILD}/parental`);
    expect(await screen.findByText("the family page")).toBeInTheDocument();
    expect(refresh).toHaveBeenCalled();
  });

  it("Cancel lifts nothing", async () => {
    open({ policy: policyAnswer() });
    await userEvent.click(await screen.findByRole("button", { name: "Lift restriction" }));
    await userEvent.click(
      within(screen.getByRole("dialog")).getByRole("button", { name: "Cancel" })
    );
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(del).not.toHaveBeenCalled();
  });
});

describe("devices", () => {
  const signals = [
    {
      refresh_family_id: "fam-1",
      lower: 9,
      upper: 12,
      tier_limit: 9,
      pending_looser: { tier_limit: 13, observed_at: "2026-11-10T10:00:00Z" },
      observed_at: "2026-11-01T10:00:00Z",
    },
    { refresh_family_id: "fam-2", tier_limit: 13, observed_at: "2026-11-02T10:00:00Z" },
  ];

  it("shows each signal, and a looser statement that waits", async () => {
    open({ policy: policyAnswer({ access: "guardian", signals }) }, guardian);
    const rows = await screen.findAllByTestId("family-device");
    expect(rows).toHaveLength(2);
    expect(rows[0]).toHaveTextContent("Limit: 9+");
    expect(rows[0]).toHaveTextContent("age 9 to 12");
    expect(rows[0]).toHaveTextContent("Asks for a looser limit: 13+");
    expect(within(rows[0]).getByRole("button", { name: "Approve" })).toBeInTheDocument();
    expect(rows[1]).toHaveTextContent("Limit: 13+");
    expect(within(rows[1]).getByRole("button", { name: "Remove" })).toBeInTheDocument();
  });

  it("Approve answers the waiting statement and reads the account again", async () => {
    const h: FamilyHome = { policy: policyAnswer({ access: "guardian", signals }) };
    open(h, guardian);
    const rows = await screen.findAllByTestId("family-device");
    post.mockResolvedValue({ tier_limit: 13, removed: false });
    h.policy = policyAnswer({ access: "guardian", signals: [signals[1]] });
    await userEvent.click(within(rows[0]).getByRole("button", { name: "Approve" }));
    expect(post).toHaveBeenCalledWith(`/api/users/${CHILD}/parental/signals/fam-1/clear`);
    expect(await screen.findByText("The device's limit is now: 13+.")).toBeVisible();
    await waitFor(() => expect(screen.getAllByTestId("family-device")).toHaveLength(1));
  });

  it("a device limited by its own settings is told to answer from another", async () => {
    open({ policy: policyAnswer({ access: "guardian", signals }) }, guardian);
    const rows = await screen.findAllByTestId("family-device");
    post.mockRejectedValue(problem(403, { code: "device_limited" }));
    await userEvent.click(within(rows[1]).getByRole("button", { name: "Remove" }));
    expect(
      await within(screen.getByTestId("family-devices")).findByRole("alert")
    ).toHaveTextContent(/limited by its own settings/);
  });
});

describe("allowed and blocked", () => {
  const rules = [
    { id: "r1", effect: "allow", subject_kind: "library", subject_id: "lib-1", created_at: "x" },
    {
      id: "r2",
      effect: "block",
      subject_kind: "series",
      subject_id: "s-1",
      expires_at: "2099-06-15T12:00:00Z",
      created_at: "x",
    },
  ];
  const h = (): FamilyHome => ({
    policy: policyAnswer({ access: "guardian" }),
    rules,
    libraries: [
      { id: "lib-1", name: "Cartoons" },
      { id: "lib-2", name: "Films" },
    ],
    series: [{ id: "s-1", name: "Space Show" }],
    videos: [{ id: "v-1", title: "Moon Landing" }],
  });

  it("lists each rule under its list, by name, with when it ends", async () => {
    open(h(), guardian);
    const allowed = await screen.findByTestId("family-rules-allow");
    expect(await within(allowed).findByText("Cartoons", { selector: "bdi" })).toBeInTheDocument();
    expect(within(allowed).getByText("Library")).toBeInTheDocument();
    const blocked = screen.getByTestId("family-rules-block");
    expect(await within(blocked).findByText("Space Show", { selector: "bdi" })).toBeInTheDocument();
    expect(blocked).toHaveTextContent(/Until .*2099/);
  });

  it("adds a block on a library until today's end", async () => {
    open(h(), guardian);
    const box = await screen.findByTestId("family-rules");
    await within(box).findByTestId("family-rules-allow");
    await userEvent.selectOptions(within(box).getByLabelText("List"), "block");
    const item = within(box).getByLabelText("Item");
    await waitFor(() => expect(item).toBeEnabled());
    await userEvent.selectOptions(item, "lib-2");
    await userEvent.selectOptions(within(box).getByLabelText("For how long"), "today");
    post.mockResolvedValue({
      id: "r3",
      effect: "block",
      subject_kind: "library",
      subject_id: "lib-2",
      expires_at: "2099-06-16T01:00:00Z",
    });
    await userEvent.click(within(box).getByRole("button", { name: "Add" }));
    // "today" is the home's day: the home computes its end, this device sends no instant
    expect(post).toHaveBeenCalledWith(`/api/users/${CHILD}/parental/rules`, {
      effect: "block",
      subject_kind: "library",
      subject_id: "lib-2",
      until: "day_end",
    });
    expect(
      await within(screen.getByTestId("family-rules-block")).findByText("Films", {
        selector: "bdi",
      })
    ).toBeInTheDocument();
  });

  it("finds a video by its title and allows it with no end", async () => {
    open(h(), guardian);
    const box = await screen.findByTestId("family-rules");
    await within(box).findByTestId("family-rules-allow");
    await userEvent.selectOptions(within(box).getByLabelText("Kind"), "video");
    // nothing to choose until a title is typed
    expect(within(box).queryByLabelText("Item")).toBeNull();
    await userEvent.type(within(box).getByLabelText("Title"), "moon");
    const item = await within(box).findByLabelText("Item");
    await waitFor(() =>
      expect(within(item).getByRole("option", { name: "Moon Landing" })).toBeInTheDocument()
    );
    expect(get).toHaveBeenCalledWith("/api/videos?q=moon&limit=20");
    await userEvent.selectOptions(item, "v-1");
    post.mockResolvedValue({ id: "r4", effect: "allow", subject_kind: "video", subject_id: "v-1" });
    await userEvent.click(within(box).getByRole("button", { name: "Add" }));
    expect(post).toHaveBeenCalledWith(`/api/users/${CHILD}/parental/rules`, {
      effect: "allow",
      subject_kind: "video",
      subject_id: "v-1",
    });
  });

  it("says why a library the account cannot open cannot be allowed", async () => {
    open(h(), guardian);
    const box = await screen.findByTestId("family-rules");
    await within(box).findByTestId("family-rules-allow");
    const item = within(box).getByLabelText("Item");
    await waitFor(() => expect(item).toBeEnabled());
    await userEvent.selectOptions(item, "lib-2");
    post.mockRejectedValue(problem(422, { code: "no_library_access" }));
    await userEvent.click(within(box).getByRole("button", { name: "Add" }));
    expect(await within(box).findByRole("alert")).toHaveTextContent(/cannot open that library/);
  });

  it("removes a rule", async () => {
    open(h(), guardian);
    const allowed = await screen.findByTestId("family-rules-allow");
    await within(allowed).findByText("Cartoons", { selector: "bdi" });
    del.mockResolvedValue(undefined);
    await userEvent.click(within(allowed).getByRole("button", { name: /Remove/ }));
    expect(del).toHaveBeenCalledWith(`/api/users/${CHILD}/parental/rules/r1`);
    expect(await within(allowed).findByText("Nothing is allowed beyond the limit.")).toBeVisible();
  });
});

describe("in Arabic", () => {
  it("is right to left, with the tiers still youngest first", async () => {
    open({ policy: policyAnswer(), accounts: [{ id: CHILD, username: "sara" }] }, adminUser, "ar");
    const form = await screen.findByTestId("family-policy");
    expect(document.documentElement).toHaveAttribute("dir", "rtl");
    const limit = within(form).getByLabelText(/حدّ المحتوى/);
    expect(
      within(limit)
        .getAllByRole("option")
        .map((o) => o.textContent)
    ).toEqual([0, 9, 13, 16, 18, -1].map((tier) => (ar as Record<string, string>)[tierKey(tier)]));
    expect(screen.getByRole("heading", { name: "أولياء الأمر" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "المسموح والمحظور" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "رفع التقييد" })).toBeInTheDocument();
  });
});
