// Admin Channels, case by case from web/test-cases/admin-channels.md (CHN-*).
// Uses the shared page harness (RequireAuth + RouteErrorBoundary + a signed-in
// user), so unlike Channels.test.tsx it does not mock ../../lib/auth.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Route } from "react-router-dom";
import { ToastProvider } from "@ds/components/Toast/Toast";
import { renderPage, memberUser, quietCrash } from "../../test/pageHarness";

const { get, post, patch, del, upload } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  patch: vi.fn(),
  del: vi.fn(),
  upload: vi.fn(),
}));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return {
    ...actual,
    api: { ...actual.api, get, post, patch, delete: del },
    uploadWithProgress: upload,
  };
});

import { ApiError } from "../../lib/api";
import { Channels } from "./Channels";

const NOW = Date.now();
const iso = (h: number) => new Date(NOW + h * 3_600_000).toISOString();

function problem(status: number, title: string, detail?: string) {
  return new ApiError({ type: "about:blank", status, title, ...(detail ? { detail } : {}) });
}

type Obj = Record<string, unknown>;

const C1: Obj = {
  id: "c1",
  number: 1,
  name: "Quran",
  slug: "quran",
  category: "Religion",
  mode: "schedule",
  enabled: true,
  sort_order: 1,
  transition: "cut",
  mode_config: { slots: [{ start: "00:00", end: "24:00", source: { type: "filler" } }] },
  plan: { owned: true, edited: false },
  now_playing: { title: "Al-Baqara", start_at: iso(-0.5), end_at: iso(0.5), progress: 0.5 },
  guide_until: iso(48.5),
};
const C2: Obj = {
  ...C1,
  id: "c2",
  number: 2,
  name: "Kids",
  slug: "kids",
  category: "Family",
  plan: null,
  enabled: false,
};

// What each GET answers: a value, an Error (rejects) or a function of the URL.
type Answer = unknown;
let answers: Record<string, Answer> = {};

function answer(v: Answer, url: string): Promise<unknown> {
  if (v instanceof Error) return Promise.reject(v);
  if (typeof v === "function") return answer((v as (u: string) => unknown)(url), url);
  return Promise.resolve(v);
}

function routeGet(url: string): Promise<unknown> {
  const path = url.split("?")[0];
  if (path.startsWith("/api/channels/") && path.endsWith("/guide"))
    return answer(answers.guide, url);
  if (path in answers) return answer(answers[path], url);
  return Promise.reject(new Error(`unmocked GET ${url}`));
}

function defaults() {
  answers = {
    "/api/channels": { items: [C1, C2] },
    "/api/tv/info": { xmltv_url: "http://tv.local/xmltv.xml" },
    "/api/filler/pools": { items: [] },
    "/api/rules": { sections: [] },
    "/api/tv/cluster": new Error("no cluster"),
    "/api/channels/now": { channels: [] },
    "/api/channels/sources": { kind: "series", items: [] },
    guide: { channel: { id: "c1", number: 1, name: "Quran" }, blocks: [] },
  };
}

const listCalls = () => get.mock.calls.filter(([u]) => String(u).split("?")[0] === "/api/channels");

function renderChannels(opts: Partial<Parameters<typeof renderPage>[1]> = {}) {
  return renderPage(
    <ToastProvider>
      <Channels />
    </ToastProvider>,
    { path: "/admin/channels", ...opts }
  );
}

function rowOf(name: string): HTMLElement {
  return screen.getByText(name, { selector: "strong" }).closest("tr")!;
}

async function openEditor(name: string, button: RegExp = /^edit$/i) {
  const user = userEvent.setup();
  renderChannels();
  await screen.findByText(name, { selector: "strong" });
  await user.click(within(rowOf(name)).getByRole("button", { name: button }));
  const dialog = await screen.findByRole("dialog");
  return { user, dialog };
}

async function openNew() {
  const user = userEvent.setup();
  renderChannels();
  await user.click(await screen.findByRole("button", { name: /new channel/i }));
  const dialog = await screen.findByRole("dialog");
  return { user, dialog };
}

const saveButton = (dialog: HTMLElement) =>
  within(dialog).getByRole("button", { name: /^save( and stop following the plan)?$/i });

describe("Admin Channels (test-cases/admin-channels.md)", () => {
  beforeEach(() => {
    for (const f of [get, post, patch, del, upload]) f.mockReset();
    defaults();
    get.mockImplementation(routeGet);
  });
  afterEach(() => vi.restoreAllMocks());

  it("CHN-01: lists channels sorted by number with all columns", async () => {
    answers["/api/channels"] = { items: [C2, C1] };
    renderChannels();
    expect(await screen.findByRole("heading", { name: "Channels" })).toBeInTheDocument();
    const table = screen.getByRole("table", { name: "Channels" });
    const heads = within(table)
      .getAllByRole("columnheader")
      .map((h) => h.textContent);
    expect(heads.slice(1)).toEqual([
      "No.",
      "Name",
      "Category",
      "On now",
      "Guide ahead",
      "Enabled",
      "Actions",
    ]);
    const rows = within(table).getAllByRole("row").slice(1);
    expect(rows[0]).toHaveTextContent("Quran");
    expect(rows[1]).toHaveTextContent("Kids");
    const quran = rows[0];
    expect(within(quran).getByText("From plan")).toBeInTheDocument();
    expect(within(quran).getByText("Schedule")).toBeInTheDocument();
    expect(within(quran).getByText("Al-Baqara")).toBeInTheDocument();
    expect(within(quran).getByLabelText("50% played")).toBeInTheDocument();
    expect(within(quran).getByText("48 h")).toBeInTheDocument();
    const kids = rows[1];
    expect(within(kids).getByText("Made by hand")).toBeInTheDocument();
    expect(within(kids).getByRole("switch", { name: "Kids on or off" })).not.toBeChecked();
    for (const r of rows) {
      for (const b of [/^edit$/i, /^preview$/i, /^delete$/i]) {
        expect(within(r).getByRole("button", { name: b })).toBeInTheDocument();
      }
    }
    expect(screen.getByRole("button", { name: "New channel" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Filler pools" })).toHaveAttribute(
      "href",
      "/admin/filler"
    );
  });

  it.each([
    ["items: null", { items: null }],
    ["missing items key", {}],
  ])("CHN-04: %s shows the empty state and New channel starts at 1", async (_, body) => {
    answers["/api/channels"] = body;
    const user = userEvent.setup();
    renderChannels();
    expect(await screen.findByText("No channels yet")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).toBeNull();
    await user.click(screen.getByRole("button", { name: "New channel" }));
    const dialog = await screen.findByRole("dialog", { name: "New channel" });
    expect(within(dialog).getByLabelText("Channel number")).toHaveValue(1);
  });

  it("CHN-05: nullable channel fields render fallbacks, every editor tab opens", async () => {
    answers["/api/channels"] = {
      items: [
        {
          ...C1,
          now_playing: null,
          plan: null,
          category: null,
          guide_until: null,
          logo_url: null,
          logo_path: null,
          mode_config: null,
          art: null,
          draft: null,
        },
        {
          ...C2,
          now_playing: { title: "", start_at: iso(-1), end_at: iso(1), progress: null },
          guide_until: "not-a-date",
        },
      ],
    };
    const user = userEvent.setup();
    renderChannels();
    await screen.findByText("Quran", { selector: "strong" });
    const quran = rowOf("Quran");
    expect(quran.querySelector(".mkt-cb-logo--none")).toHaveTextContent("Q");
    expect(within(quran).getByText("Made by hand")).toBeInTheDocument();
    expect(within(quran).getByText("—")).toBeInTheDocument();
    expect(within(quran).getByText("Nothing scheduled")).toBeInTheDocument();
    expect(within(quran).getByText("0 h")).toHaveClass("mkt-cb-cover--low");
    const kids = rowOf("Kids");
    expect(within(kids).getByText("Filler / bumper")).toBeInTheDocument();
    expect(within(kids).getByText("0 h")).toBeInTheDocument();

    await user.click(within(quran).getByRole("button", { name: /^edit$/i }));
    const dialog = await screen.findByRole("dialog");
    for (const tab of ["Art", "Schedule", "Filler", "Live broadcasts", "Preview", "Basics"]) {
      await user.click(within(dialog).getByRole("tab", { name: new RegExp(`^${tab}`) }));
      if (tab === "Schedule") {
        expect(
          within(dialog).getByText("The schedule has no slots yet: add at least one.")
        ).toBeInTheDocument();
      }
      if (tab === "Art") {
        expect(within(dialog).getAllByText("No image").length).toBeGreaterThan(0);
        expect(within(dialog).queryByLabelText(/watermark width/i)).toBeNull();
      }
    }
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("CHN-06: a channel with name null renders and search does not throw", async () => {
    quietCrash();
    answers["/api/channels"] = { items: [{ ...C1, name: null, logo_url: null }] };
    const user = userEvent.setup();
    renderChannels();
    const table = await screen.findByRole("table", { name: "Channels" });
    expect(within(table).getByText("Al-Baqara")).toBeInTheDocument();
    await user.type(screen.getByLabelText("Search channels"), "q");
    expect(screen.queryByRole("alert")).toBeNull();
    expect(screen.getByText("Al-Baqara")).toBeInTheDocument();
  });

  it.each([
    ["500 problem", problem(500, "Internal error"), "Internal error"],
    ["network failure", new TypeError("Failed to fetch"), "Something went wrong."],
    ["403 problem", problem(403, "Forbidden"), "Forbidden"],
  ])("CHN-07: list load %s shows an error with Try again", async (_, err, text) => {
    answers["/api/channels"] = err;
    const user = userEvent.setup();
    renderChannels();
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent(text);
    expect(screen.queryByText("No channels yet")).toBeNull();
    answers["/api/channels"] = { items: [C1] };
    await user.click(within(alert).getByRole("button", { name: "Try again" }));
    expect(await screen.findByText("Quran", { selector: "strong" })).toBeInTheDocument();
  });

  it("CHN-09: a non-admin sees Admins only and no channel requests fire", async () => {
    renderChannels({ user: memberUser });
    expect(await screen.findByText("Admins only")).toBeInTheDocument();
    expect(
      screen.getByText("You need administrator access to view this page.")
    ).toBeInTheDocument();
    expect(get).not.toHaveBeenCalled();
  });

  it("CHN-10: a signed-out visitor is sent to /login with the page as from", () => {
    renderChannels({ user: null });
    expect(screen.getByText("login page (from /admin/channels)")).toBeInTheDocument();
    expect(get).not.toHaveBeenCalled();
  });

  it("CHN-11: search, category and origin filters with the no-match text", async () => {
    const C3 = {
      ...C1,
      id: "c3",
      number: 12,
      name: "News",
      category: "News",
      now_playing: null,
      plan: { owned: true, edited: true },
    };
    answers["/api/channels"] = { items: [C1, C2, C3] };
    const user = userEvent.setup();
    renderChannels();
    await screen.findByText("News", { selector: "strong" });
    const search = screen.getByLabelText("Search channels");
    const names = () =>
      screen
        .queryAllByText(/^(Quran|Kids|News)$/, { selector: "strong" })
        .map((e) => e.textContent);

    await user.type(search, "12");
    expect(names()).toEqual(["News"]);
    expect(
      screen.getByText("Showing 1 of 3 channels. Clear the filters to reorder.")
    ).toBeInTheDocument();
    const handle = screen.getByRole("button", { name: "Move News" });
    expect(handle).toBeDisabled();
    expect(handle).toHaveAttribute("title", "Clear the filters to reorder");
    await user.clear(search);
    await user.type(search, "baqara");
    expect(names()).toEqual(["Quran", "Kids"]); // both are on Al-Baqara now
    await user.clear(search);
    await user.type(search, "zzz");
    expect(screen.getByText("No channel matches these filters.")).toBeInTheDocument();
    await user.clear(search);

    const cat = screen.getByLabelText("Category");
    expect(
      within(cat)
        .getAllByRole("option")
        .map((o) => o.textContent)
    ).toEqual(["All categories", "Family", "News", "Religion"]);
    await user.selectOptions(cat, "Family");
    expect(names()).toEqual(["Kids"]);
    await user.selectOptions(cat, "");

    const origin = screen.getByLabelText("Source");
    await user.selectOptions(origin, "plan");
    expect(names()).toEqual(["Quran"]);
    await user.selectOptions(origin, "edited");
    expect(names()).toEqual(["News"]);
    await user.selectOptions(origin, "manual");
    expect(names()).toEqual(["Kids"]);
    await user.selectOptions(origin, "all");
    expect(screen.getByText(/drag a row by its handle/i)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Move News" })).toBeEnabled();
  });

  it("CHN-12: the on/off toggle is optimistic, warns for plan channels and toasts errors", async () => {
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(true);
    let release: (v: unknown) => void = () => {};
    patch.mockImplementationOnce(() => new Promise((r) => (release = r)));
    const user = userEvent.setup();
    renderChannels();
    await screen.findByText("Kids", { selector: "strong" });

    // 1) a manual channel: no confirm, flips at once, only `enabled` sent
    const kidsSwitch = screen.getByRole("switch", { name: "Kids on or off" });
    await user.click(kidsSwitch);
    expect(confirm).not.toHaveBeenCalled();
    expect(screen.getByRole("switch", { name: "Kids on or off" })).toBeChecked();
    expect(patch).toHaveBeenCalledWith("/api/channels/c2", { enabled: true });
    const before = listCalls().length;
    release({});
    await waitFor(() => expect(listCalls().length).toBe(before + 1));

    // 2) a plan channel: confirm first; Cancel sends nothing
    confirm.mockReturnValueOnce(false);
    patch.mockClear();
    await user.click(screen.getByRole("switch", { name: "Quran on or off" }));
    expect(confirm).toHaveBeenLastCalledWith(
      "Quran will stop following the lineup plan. Continue?"
    );
    expect(patch).not.toHaveBeenCalled();
    expect(screen.getByRole("switch", { name: "Quran on or off" })).toBeChecked();
    patch.mockResolvedValueOnce({});
    await user.click(screen.getByRole("switch", { name: "Quran on or off" }));
    expect(patch).toHaveBeenCalledWith("/api/channels/c1", { enabled: false });

    // 3) a failure toasts the problem title; the reload shows the server's value
    patch.mockRejectedValueOnce(problem(500, "Boom"));
    await waitFor(() =>
      expect(screen.getByRole("switch", { name: "Quran on or off" })).toBeChecked()
    );
    await user.click(screen.getByRole("switch", { name: "Quran on or off" }));
    expect(await screen.findByText("Boom")).toBeInTheDocument();
    await waitFor(() =>
      expect(screen.getByRole("switch", { name: "Quran on or off" })).toBeChecked()
    );
  });

  it("CHN-13: reorder from the keyboard and by drag; a reorder error toasts and reloads", async () => {
    const K = { ...C2, number: 1 };
    const D = { ...C2, id: "c3", number: 5, name: "Docs" };
    answers["/api/channels"] = { items: [K, D] };
    post.mockResolvedValue(undefined);
    const user = userEvent.setup();
    renderChannels();
    await screen.findByText("Docs", { selector: "strong" });

    screen.getByRole("button", { name: "Move Kids" }).focus();
    await user.keyboard("{ArrowUp}");
    expect(post).not.toHaveBeenCalled();
    await user.keyboard("{ArrowDown}");
    expect(post).toHaveBeenCalledWith("/api/channels/reorder", [
      { id: "c3", number: 1 },
      { id: "c2", number: 5 },
    ]);
    expect(screen.getByText("Kids moved to position 2.")).toBeInTheDocument();

    // drag Docs onto row 1 (the reload brought back the server order)
    post.mockClear();
    await screen.findByText("Docs", { selector: "strong" });
    const docsHandle = screen.getByRole("button", { name: "Move Docs" });
    const dataTransfer = { setData: vi.fn(), effectAllowed: "" };
    fireEvent.dragStart(docsHandle, { dataTransfer });
    const first = rowOf("Kids");
    fireEvent.dragOver(first, { dataTransfer });
    fireEvent.drop(first, { dataTransfer });
    expect(post).toHaveBeenCalledWith("/api/channels/reorder", [
      { id: "c3", number: 1 },
      { id: "c2", number: 5 },
    ]);

    post.mockRejectedValueOnce(problem(409, "Conflict"));
    const before = listCalls().length;
    await waitFor(() => expect(within(rowOf("Kids")).getByText("1")).toBeInTheDocument());
    screen.getByRole("button", { name: "Move Kids" }).focus();
    await user.keyboard("{ArrowDown}");
    expect(await screen.findByText("Conflict")).toBeInTheDocument();
    await waitFor(() => expect(listCalls().length).toBeGreaterThan(before));
  });

  it("CHN-14: delete asks first; Cancel sends nothing; OK deletes; an error toasts", async () => {
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(false);
    const user = userEvent.setup();
    renderChannels();
    await screen.findByText("Kids", { selector: "strong" });
    await user.click(within(rowOf("Kids")).getByRole("button", { name: /^delete$/i }));
    expect(confirm).toHaveBeenCalledWith('Delete channel "Kids"?');
    expect(del).not.toHaveBeenCalled();

    confirm.mockReturnValue(true);
    del.mockImplementationOnce(() => {
      answers["/api/channels"] = { items: [C1] };
      return Promise.resolve(undefined);
    });
    await user.click(within(rowOf("Kids")).getByRole("button", { name: /^delete$/i }));
    expect(del).toHaveBeenCalledWith("/api/channels/c2");
    await waitFor(() => expect(screen.queryByText("Kids", { selector: "strong" })).toBeNull());

    del.mockRejectedValueOnce(problem(404, "Not found"));
    await user.click(within(rowOf("Quran")).getByRole("button", { name: /^delete$/i }));
    expect(await screen.findByText("Not found")).toBeInTheDocument();
    expect(screen.getByText("Quran", { selector: "strong" })).toBeInTheDocument();
  });

  it("CHN-15: the new channel form blocks Save until name, number and a slot are fine", async () => {
    const { user, dialog } = await openNew();
    expect(dialog).toHaveAccessibleName("New channel");
    const number = within(dialog).getByLabelText("Channel number");
    expect(number).toHaveValue(3);
    expect(within(dialog).getByRole("switch", { name: "Enabled" })).not.toBeChecked();
    const name = within(dialog).getByLabelText("Name");
    expect(within(dialog).getByText("Name is required.")).toBeInTheDocument();
    await user.type(name, "   ");
    expect(within(dialog).getByText("Name is required.")).toBeInTheDocument();
    expect(within(dialog).getByRole("tab", { name: "Basics •" })).toBeInTheDocument();

    await user.clear(number);
    await user.type(number, "0");
    expect(within(dialog).getByText("Enter a channel number above 0.")).toBeInTheDocument();
    await user.clear(number);
    expect(within(dialog).getByText("Enter a channel number above 0.")).toBeInTheDocument();
    await user.type(number, "2");
    expect(within(dialog).getByText("Number already used by Kids.")).toBeInTheDocument();
    await user.clear(number);
    await user.type(number, "3");
    expect(within(dialog).queryByText(/number (above 0|already used)/i)).toBeNull();
    expect(saveButton(dialog)).toBeDisabled();

    await user.clear(name);
    await user.type(name, "Docs");
    expect(within(dialog).getByRole("tab", { name: "Basics" })).toBeInTheDocument();
    expect(within(dialog).getByRole("tab", { name: "Schedule •" })).toBeInTheDocument();
    // from another tab, the schedule's problem is said under the tabs
    expect(
      within(dialog).getByText("The schedule has no slots yet: add at least one.")
    ).toBeInTheDocument();
    expect(saveButton(dialog)).toBeDisabled();
    await user.click(within(dialog).getByRole("tab", { name: "Schedule •" }));
    expect(
      within(dialog).getByText("The schedule has no slots yet: add at least one.")
    ).toBeInTheDocument();
    await user.click(within(dialog).getByRole("button", { name: "Add slot" }));
    expect(within(dialog).getByRole("tab", { name: "Schedule" })).toBeInTheDocument();
    expect(saveButton(dialog)).toBeEnabled();
  });

  // Bug: a fractional channel number (2.5) is accepted client-side. Refusing it
  // needs a new i18n key (e.g. channels.err.numberWhole: "Enter a whole number.").
  it.fails("CHN-15: a fractional channel number is refused", async () => {
    const { user, dialog } = await openNew();
    await user.type(within(dialog).getByLabelText("Name"), "Docs");
    const number = within(dialog).getByLabelText("Channel number");
    await user.clear(number);
    await user.type(number, "2.5");
    expect(within(dialog).getByRole("tab", { name: "Basics •" })).toBeInTheDocument();
  });

  it("CHN-16: create sends only create fields, then uploads the pending art", async () => {
    post.mockImplementation((path: string) => {
      if (path === "/api/channels") {
        answers["/api/channels"] = {
          items: [C1, C2, { ...C2, id: "c9", number: 3, name: "Docs" }],
        };
        return Promise.resolve({ ...C2, id: "c9", number: 3, name: "Docs" });
      }
      return Promise.reject(new Error(`unmocked POST ${path}`));
    });
    upload.mockResolvedValueOnce({});
    const { user, dialog } = await openNew();
    await user.type(within(dialog).getByLabelText("Name"), "Docs");
    await user.click(within(dialog).getByRole("tab", { name: /^schedule/i }));
    await user.click(within(dialog).getByRole("button", { name: "Add slot" }));
    await user.click(within(dialog).getByRole("tab", { name: "Art" }));
    const png = new File([new Uint8Array(10_000)], "logo.png", { type: "image/png" });
    await user.upload(within(dialog).getByLabelText("Choose an image for Logo"), png);
    await user.click(saveButton(dialog));

    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    const body = post.mock.calls.find(([p]) => p === "/api/channels")![1] as Obj;
    expect(Object.keys(body).sort()).toEqual(
      ["category", "enabled", "mode", "mode_config", "name", "number", "transition"].filter(
        (k) => k !== "category" || body.category !== undefined
      )
    );
    expect(body).not.toHaveProperty("id");
    expect(body).not.toHaveProperty("art");
    expect(body).not.toHaveProperty("now_playing");
    expect(upload).toHaveBeenCalledWith(
      "/api/channels/c9/art/logo",
      expect.any(FormData),
      undefined
    );
    expect(await screen.findByText("Docs", { selector: "strong" })).toBeInTheDocument();
  });

  it("CHN-16: a failed art upload toasts, and the channel is still created", async () => {
    post.mockResolvedValue({ ...C2, id: "c9", number: 3, name: "Docs" });
    upload.mockRejectedValueOnce(problem(500, "Too big"));
    const { user, dialog } = await openNew();
    await user.type(within(dialog).getByLabelText("Name"), "Docs");
    await user.click(within(dialog).getByRole("tab", { name: /^schedule/i }));
    await user.click(within(dialog).getByRole("button", { name: "Add slot" }));
    await user.click(within(dialog).getByRole("tab", { name: "Art" }));
    const png = new File([new Uint8Array(10_000)], "logo.png", { type: "image/png" });
    await user.upload(within(dialog).getByLabelText("Choose an image for Logo"), png);
    await user.click(saveButton(dialog));
    expect(await screen.findByText("The Logo could not be uploaded: Too big")).toBeInTheDocument();
    expect(post).toHaveBeenCalledWith("/api/channels", expect.any(Object));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  });

  it("CHN-17: edit sends only the changed fields; Save is disabled while unchanged", async () => {
    answers["/api/channels"] = { items: [C2] };
    patch.mockResolvedValue({});
    const { user, dialog } = await openEditor("Kids");
    expect(dialog).toHaveAccessibleName("Edit Kids");
    expect(
      within(dialog).getByText(
        "Rule changes apply from the next program boundary, not immediately."
      )
    ).toBeInTheDocument();
    const save = saveButton(dialog);
    expect(save).toBeDisabled();
    const category = within(dialog).getByLabelText("Category");
    await user.clear(category);
    await user.type(category, "Cartoons");
    expect(save).toBeEnabled();
    await user.clear(category);
    await user.type(category, "Family");
    expect(save).toBeDisabled();
    await user.selectOptions(within(dialog).getByLabelText("Transition"), "crossfade");
    expect(save).toBeEnabled();
    const before = listCalls().length;
    await user.click(save);
    expect(patch).toHaveBeenCalledTimes(1);
    expect(patch).toHaveBeenCalledWith("/api/channels/c2", { transition: "crossfade" });
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(listCalls().length).toBe(before + 1);
  });

  it("CHN-18: plan banners, and Back to plan with confirm and error", async () => {
    const C3 = { ...C1, id: "c3", number: 3, name: "News", plan: { owned: true, edited: true } };
    answers["/api/channels"] = { items: [C1, C3] };
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(false);
    const { user, dialog } = await openEditor("Quran");
    expect(within(dialog).getByText(/^This channel follows the lineup plan/)).toBeInTheDocument();
    await user.type(within(dialog).getByLabelText("Name"), "!");
    expect(within(dialog).getByText(/^You changed this channel's settings/)).toBeInTheDocument();
    expect(
      within(dialog).getByRole("button", { name: "Save and stop following the plan" })
    ).toBeEnabled();
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));

    await user.click(within(rowOf("News")).getByRole("button", { name: /^edit$/i }));
    const d2 = await screen.findByRole("dialog");
    expect(within(d2).getByText(/^You edited this channel/)).toBeInTheDocument();
    await user.click(within(d2).getByRole("button", { name: "Back to plan" }));
    expect(post).not.toHaveBeenCalled();
    confirm.mockReturnValue(true);
    post.mockRejectedValueOnce(problem(500, "Down"));
    await user.click(within(d2).getByRole("button", { name: "Back to plan" }));
    expect(await screen.findByText("Down")).toBeInTheDocument();
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    post.mockResolvedValueOnce({ queued: true });
    await user.click(within(d2).getByRole("button", { name: "Back to plan" }));
    expect(post).toHaveBeenLastCalledWith("/api/channels/c3/follow-plan");
    expect(await screen.findByText(/^The channel follows the plan again/)).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  });

  it.each([
    ["409 stale", problem(409, "Conflict"), "Your copy is stale — reload and try again."],
    ["400 with detail", problem(400, "Bad Request", "number taken"), "number taken"],
    ["400 without detail", problem(400, "Bad Request"), "Bad Request"],
    ["network failure", new TypeError("Failed to fetch"), "Something went wrong."],
  ])("CHN-19: save error %s toasts and keeps the dialog", async (_, err, text) => {
    answers["/api/channels"] = { items: [C2] };
    patch.mockRejectedValueOnce(err);
    const { user, dialog } = await openEditor("Kids");
    const name = within(dialog).getByLabelText("Name");
    await user.clear(name);
    await user.type(name, "Kids 2");
    const before = listCalls().length;
    await user.click(saveButton(dialog));
    expect(await screen.findByText(text)).toBeInTheDocument();
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(within(dialog).getByLabelText("Name")).toHaveValue("Kids 2");
    await waitFor(() => expect(saveButton(dialog)).toBeEnabled());
    expect(listCalls().length).toBe(before);
  });

  it("CHN-20: without changes, Cancel, Escape and Close shut the dialog at once", async () => {
    const confirm = vi.spyOn(window, "confirm");
    const { user, dialog } = await openEditor("Kids");
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    expect(screen.queryByRole("dialog")).toBeNull();
    await user.click(within(rowOf("Kids")).getByRole("button", { name: /^edit$/i }));
    await screen.findByRole("dialog");
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("dialog")).toBeNull();
    await user.click(within(rowOf("Kids")).getByRole("button", { name: /^edit$/i }));
    await user.click(
      within(await screen.findByRole("dialog")).getByRole("button", { name: "Close" })
    );
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(confirm).not.toHaveBeenCalled();
  });

  // Bug: ChannelEditor passes onClose straight to Modal, so Escape / backdrop /
  // Close / Cancel drop every unsaved edit silently. Fix in ChannelEditor.tsx
  // needs a new i18n key for the prompt (e.g. channels.confirmDiscard).
  it.fails("CHN-20: with changes, Escape asks before discarding them", async () => {
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(false);
    const { user, dialog } = await openEditor("Kids");
    const name = within(dialog).getByLabelText("Name");
    await user.clear(name);
    await user.type(name, "Kids 2");
    await user.keyboard("{Escape}");
    expect(confirm).toHaveBeenCalled();
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(within(screen.getByRole("dialog")).getByLabelText("Name")).toHaveValue("Kids 2");
  });

  // Bug: design-system Tabs renders only the active panel, so section drafts
  // (watermark settings, strip, slot editor) are dropped on a tab switch.
  // Fix belongs in design-system/components/Tabs/Tabs.tsx (keep panels
  // mounted) or in ChannelEditor lifting the drafts.
  it.fails("CHN-21: an unapplied watermark width survives a tab switch", async () => {
    answers["/api/channels"] = {
      items: [
        {
          ...C1,
          art: {
            logo: { overridden: false },
            watermark: {
              url: "/w.png",
              overridden: true,
              position: "top-right",
              width_pct: 10,
              margin_pct: 2,
            },
            loading: { overridden: false },
          },
        },
      ],
    };
    const { user, dialog } = await openEditor("Quran");
    await user.click(within(dialog).getByRole("tab", { name: "Art" }));
    const width = within(dialog).getByLabelText(/watermark width/i);
    await user.clear(width);
    await user.type(width, "20");
    await user.click(within(dialog).getByRole("tab", { name: /^basics/i }));
    await user.click(within(dialog).getByRole("tab", { name: "Art" }));
    expect(within(dialog).getByLabelText(/watermark width/i)).toHaveValue(20);
  });

  it("CHN-22: slot problems block Save and warn from the other tabs; an overlap only warns", async () => {
    answers["/api/channels"] = {
      items: [
        {
          ...C2,
          mode_config: {
            slots: [
              { start: "08:00", end: "10:00", source: { type: "series" } },
              { start: "09:00", end: "11:00", source: { type: "filler" } },
            ],
          },
        },
      ],
    };
    answers["/api/channels/sources"] = {
      kind: "series",
      items: [{ kind: "series", id: "s1", name: "Omar", count: 30, duration_ms: 108_000_000 }],
    };
    const { user, dialog } = await openEditor("Kids");
    await user.type(within(dialog).getByLabelText("Name"), "!"); // make the form dirty
    expect(saveButton(dialog)).toBeDisabled();
    expect(within(dialog).getByText("A slot of the schedule needs fixing.")).toBeInTheDocument();
    await user.click(within(dialog).getByRole("tab", { name: "Schedule •" }));
    const blocks = within(dialog).getAllByRole("button", { name: /08:00–10:00/ });
    expect(blocks).toHaveLength(7);
    expect(blocks[0]).toHaveClass("mkt-cb-block--clash");
    await user.click(blocks[0]);
    const slot = within(dialog).getByRole("region", { name: "Slot" });
    const problems = within(slot).getByRole("alert");
    expect(problems).toHaveTextContent("Pick what the slot plays.");
    expect(problems).toHaveTextContent(
      "This slot overlaps another on the same day: the earlier one wins."
    );
    await user.click(await within(slot).findByRole("button", { name: /omar/i }));
    expect(within(dialog).getByRole("tab", { name: "Schedule" })).toBeInTheDocument();
    expect(saveButton(dialog)).toBeEnabled(); // the overlap alone does not block
    fireEvent.change(within(slot).getByLabelText("Ends"), { target: { value: "07:00" } });
    expect(within(slot).getByText("The slot must end after it starts.")).toBeInTheDocument();
    expect(saveButton(dialog)).toBeDisabled();
    expect(within(dialog).getByRole("tab", { name: "Schedule •" })).toBeInTheDocument();
    fireEvent.change(within(slot).getByLabelText("Ends"), { target: { value: "08:01" } });
    expect(within(slot).getByRole("button", { name: "Split in two" })).toBeDisabled();
    fireEvent.change(within(slot).getByLabelText("Ends"), { target: { value: "10:00" } });
    expect(saveButton(dialog)).toBeEnabled();
  });

  it.each([
    ["slots: null", { slots: null }],
    ["empty config", {}],
    ["ramadan: null", { slots: [], ramadan: null }],
    ["ramadan.slots: null", { slots: [], ramadan: { slots: null } }],
    ["slots as an object", { slots: {} }],
  ])("CHN-23: mode_config %s shows the empty grid and Add slot works", async (_, cfg) => {
    quietCrash();
    answers["/api/channels"] = { items: [{ ...C2, mode_config: cfg }] };
    const { user, dialog } = await openEditor("Kids");
    expect(screen.queryByRole("alert")).toBeNull();
    await user.click(within(dialog).getByRole("tab", { name: /^schedule/i }));
    expect(
      within(dialog).getByText("The schedule has no slots yet: add at least one.")
    ).toBeInTheDocument();
    const ramadan = (cfg as { ramadan?: unknown }).ramadan;
    if (ramadan) {
      await user.click(within(dialog).getByRole("tab", { name: "Ramadan" }));
      expect(
        within(dialog).getByText("The schedule has no slots yet: add at least one.")
      ).toBeInTheDocument();
    }
    await user.click(within(dialog).getByRole("button", { name: "Add slot" }));
    expect(within(dialog).getAllByRole("button", { name: /00:00–01:00/ })).toHaveLength(7);
    expect(screen.queryByRole("alert", { name: /error/i })).toBeNull();
  });

  it("CHN-23: a slot with days and source null is drawn on all seven days as filler", async () => {
    answers["/api/channels"] = {
      items: [
        {
          ...C2,
          mode_config: { slots: [{ start: "08:00", end: "09:00", days: null, source: null }] },
        },
      ],
    };
    const { user, dialog } = await openEditor("Kids");
    await user.click(within(dialog).getByRole("tab", { name: /^schedule/i }));
    const blocks = within(dialog).getAllByRole("button", { name: /08:00–09:00/ });
    expect(blocks).toHaveLength(7);
    expect(blocks[0].className).toMatch(/filler/);
  });

  it("CHN-24: marathon needs a series; shuffle and smart mix show their rule", async () => {
    answers["/api/channels/sources"] = {
      kind: "series",
      items: [{ kind: "series", id: "s1", name: "Omar", count: 30, duration_ms: 108_000_000 }],
    };
    const { user, dialog } = await openNew();
    await user.type(within(dialog).getByLabelText("Name"), "Omar TV");
    const mode = within(dialog).getByLabelText("Programming mode");
    await user.selectOptions(mode, "marathon");
    expect(
      within(dialog).getByText("Plays one series in order, back to back.")
    ).toBeInTheDocument();
    expect(saveButton(dialog)).toBeDisabled();
    await user.click(within(dialog).getByRole("tab", { name: "Schedule •" }));
    expect(within(dialog).getByText("Pick the series this channel plays.")).toBeInTheDocument();
    await user.click(await within(dialog).findByRole("button", { name: /omar/i }));
    expect(within(dialog).getByText("Omar", { selector: "strong" })).toBeInTheDocument();
    expect(saveButton(dialog)).toBeEnabled();
    await user.click(within(dialog).getByRole("tab", { name: "Basics" }));
    await user.selectOptions(within(dialog).getByLabelText("Programming mode"), "shuffle");
    expect(
      within(dialog).getByText("Plays the whole library in a fair shuffle.")
    ).toBeInTheDocument();
    await user.selectOptions(within(dialog).getByLabelText("Programming mode"), "smart_mix");
    await user.click(within(dialog).getByRole("tab", { name: /^schedule/i }));
    expect(within(dialog).getByLabelText("Daypart profile")).toBeInTheDocument();
  });

  it("CHN-25: filler lists show missing pools and shares; weight clamps; max gap blocks Save", async () => {
    answers["/api/filler/pools"] = { items: [{ id: "p1", name: "News clips", items: [] }] };
    answers["/api/channels"] = {
      items: [
        {
          ...C2,
          mode_config: {
            ...(C2.mode_config as Obj),
            filler: {
              lists: [
                { pool: "gone", weight: 50 },
                { pool: "p1", weight: 150 },
              ],
            },
          },
        },
      ],
    };
    const { user, dialog } = await openEditor("Kids");
    await user.click(within(dialog).getByRole("tab", { name: "Filler" }));
    const gone = within(dialog).getByTestId("filler-list-0");
    expect(within(gone).getByText("Pool not found")).toBeInTheDocument();
    expect(within(gone).getByText("25%")).toBeInTheDocument();
    const news = within(dialog).getByTestId("filler-list-1");
    expect(within(news).getByText("75%")).toBeInTheDocument();
    const weight = within(news).getByRole("spinbutton", { name: /news clips/i });
    await user.clear(weight);
    await user.type(weight, "5000");
    expect(weight).toHaveValue(1000);
    const gap = within(dialog).getByLabelText("Longest filler gap (minutes)");
    fireEvent.change(gap, { target: { value: "300" } });
    expect(within(dialog).getByText("Between 0 and 240 minutes.")).toBeInTheDocument();
    expect(saveButton(dialog)).toBeDisabled();
    expect(within(dialog).getByRole("tab", { name: "Filler •" })).toBeInTheDocument();
    fireEvent.change(gap, { target: { value: "-1" } });
    expect(saveButton(dialog)).toBeDisabled();
    fireEvent.change(gap, { target: { value: "30" } });
    expect(saveButton(dialog)).toBeEnabled();
  });

  it.each([
    ["items: null", { items: null }],
    ["missing items", {}],
    ["a 500", problem(500, "Down")],
  ])("CHN-26: filler pools %s shows No more pools to add", async (_, body) => {
    answers["/api/filler/pools"] = body;
    const { user, dialog } = await openEditor("Quran");
    await user.click(within(dialog).getByRole("tab", { name: "Filler" }));
    expect(within(dialog).getByText("No more pools to add.")).toBeInTheDocument();
    await user.click(within(dialog).getByRole("tab", { name: /^schedule/i }));
    await user.click(within(dialog).getAllByRole("button", { name: /00:00–24:00/ })[0]);
    const pool = within(dialog).getByLabelText("Filler pool for this slot");
    expect(within(pool).getAllByRole("option")).toHaveLength(1);
  });

  it("CHN-26: a pool with items null shows 0 clips; a nameless pool does not break the search", async () => {
    quietCrash();
    answers["/api/filler/pools"] = {
      items: [
        { id: "p1", name: "A", items: null },
        { id: "p2", name: null, items: [] },
      ],
    };
    const { user, dialog } = await openEditor("Quran");
    await user.click(within(dialog).getByRole("tab", { name: "Filler" }));
    expect(within(dialog).getByRole("button", { name: /^A/ })).toHaveTextContent("0 clips");
    await user.type(within(dialog).getByLabelText("Search filler pools"), "a");
    await waitFor(() =>
      expect(within(dialog).getAllByRole("button", { name: /clips/ })).toHaveLength(1)
    );
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it.each([
    ["items: null", { items: null }, "Nothing to pick from yet.", "Nothing matches “zz”."],
    ["missing items", {}, "Nothing to pick from yet.", "Nothing matches “zz”."],
    ["a 500", problem(500, "Down"), "Something went wrong.", "Something went wrong."],
    ["a network failure", new TypeError("x"), "Something went wrong.", "Something went wrong."],
  ])("CHN-26: source search %s", async (_, body, first, typed) => {
    answers["/api/channels/sources"] = body;
    const { user, dialog } = await openEditor("Quran");
    await user.click(within(dialog).getByRole("tab", { name: /^schedule/i }));
    await user.click(within(dialog).getAllByRole("button", { name: /00:00–24:00/ })[0]);
    const slot = within(dialog).getByRole("region", { name: "Slot" });
    await user.click(within(slot).getByRole("radio", { name: "Series" }));
    expect(await within(slot).findByText(first)).toBeInTheDocument();
    await user.type(within(slot).getByLabelText("Find a series"), "zz");
    expect(await within(slot).findByText(typed)).toBeInTheDocument();
  });

  it("CHN-26: a source hit with detail null renders", async () => {
    answers["/api/channels/sources"] = {
      items: [
        { kind: "series", id: "s1", name: "Omar", count: 1, duration_ms: 60_000, detail: null },
      ],
    };
    const { user, dialog } = await openEditor("Quran");
    await user.click(within(dialog).getByRole("tab", { name: /^schedule/i }));
    await user.click(within(dialog).getAllByRole("button", { name: /00:00–24:00/ })[0]);
    await user.click(within(dialog).getByRole("radio", { name: "Series" }));
    expect(await within(dialog).findByRole("button", { name: /omar/i })).toBeInTheDocument();
  });

  it("CHN-27: live rows validate, renumber with the channel, and a rules failure is shown", async () => {
    answers["/api/channels"] = { items: [C2] };
    answers["/api/rules"] = {
      sections: [
        {
          key: "tv",
          fields: [
            {
              name: "live",
              value: [
                { channel: 2, url: "https://x/live", title: "News" },
                { channel: 7, url: "https://y" },
              ],
            },
          ],
        },
      ],
    };
    patch.mockResolvedValue({});
    const { user, dialog } = await openEditor("Kids");
    await user.click(within(dialog).getByRole("tab", { name: /^live broadcasts/i }));
    expect(within(dialog).getAllByLabelText("Stream address")).toHaveLength(1); // only channel 2's
    await user.click(within(dialog).getByRole("button", { name: "Add live broadcast" }));
    const urls = within(dialog).getAllByLabelText("Stream address");
    expect(within(dialog).getByText("Enter the stream address.")).toBeInTheDocument();
    expect(within(dialog).getByRole("tab", { name: "Live broadcasts •" })).toBeInTheDocument();
    expect(saveButton(dialog)).toBeDisabled();
    await user.type(urls[1], "ftp://a");
    expect(
      within(dialog).getByText("Enter a full address, starting with https://")
    ).toBeInTheDocument();
    await user.clear(urls[1]);
    await user.type(urls[1], "https://z/live");
    expect(within(dialog).getByRole("tab", { name: "Live broadcasts" })).toBeInTheDocument();
    const minutes = within(dialog).getAllByLabelText("Length (minutes)")[1];
    fireEvent.change(minutes, { target: { value: "0" } });
    expect(within(dialog).getByText("Between 1 and 1440 minutes.")).toBeInTheDocument();
    expect(saveButton(dialog)).toBeDisabled();
    fireEvent.change(minutes, { target: { value: "10" } });

    await user.click(within(dialog).getByRole("tab", { name: "Basics" }));
    const number = within(dialog).getByLabelText("Channel number");
    await user.clear(number);
    await user.type(number, "3");
    await user.click(saveButton(dialog));
    await waitFor(() => expect(patch).toHaveBeenCalledTimes(2));
    expect(patch.mock.calls[0]).toEqual(["/api/channels/c2", { number: 3 }]);
    const [path, body] = patch.mock.calls[1] as [string, { set: { live: Obj[] } }];
    expect(path).toBe("/api/rules/tv");
    expect(body.set.live.map((r) => r.channel)).toEqual([3, 7, 3]);
    expect(body.set.live[1]).toEqual({ channel: 7, url: "https://y" });
  });

  it.each([
    ["a 500", problem(500, "Down")],
    ["sections: null", { sections: null }],
  ])("CHN-27: rules load %s shows Live broadcasts could not be loaded", async (_, body) => {
    answers["/api/rules"] = body;
    const { user, dialog } = await openEditor("Kids");
    await user.click(within(dialog).getByRole("tab", { name: /^live broadcasts/i }));
    expect(within(dialog).getByText("Live broadcasts could not be loaded.")).toBeInTheDocument();
    await user.click(within(dialog).getByRole("tab", { name: "Basics" }));
    expect(within(dialog).getByLabelText("Name")).toHaveValue("Kids");
  });

  it("CHN-27: an empty tv.live inherits the dizqueTV rows with a note", async () => {
    answers["/api/rules"] = {
      sections: [
        { key: "tv", fields: [{ name: "live", value: [] }] },
        { key: "dizquetv", fields: [{ name: "live", value: [{ channel: 2, url: "https://d" }] }] },
      ],
    };
    const { user, dialog } = await openEditor("Kids");
    await user.click(within(dialog).getByRole("tab", { name: /^live broadcasts/i }));
    expect(within(dialog).getByText(/^These come from the dizqueTV export/)).toBeInTheDocument();
    expect(within(dialog).getByLabelText("Stream address")).toHaveValue("https://d");
  });

  it("CHN-28: when the live rows fail after a create, the next Save retries only them", async () => {
    post.mockImplementation((path: string) => {
      if (path !== "/api/channels") return Promise.reject(new Error(path));
      answers["/api/channels"] = { items: [C1, C2, { ...C2, id: "c9", number: 3, name: "Docs" }] };
      return Promise.resolve({ ...C2, id: "c9", number: 3, name: "Docs" });
    });
    patch.mockRejectedValueOnce(problem(500, "Rules locked"));
    const { user, dialog } = await openNew();
    await user.type(within(dialog).getByLabelText("Name"), "Docs");
    await user.click(within(dialog).getByRole("tab", { name: /^schedule/i }));
    await user.click(within(dialog).getByRole("button", { name: "Add slot" }));
    await user.click(within(dialog).getByRole("tab", { name: /^live broadcasts/i }));
    await user.click(within(dialog).getByRole("button", { name: "Add live broadcast" }));
    await user.type(within(dialog).getByLabelText("Stream address"), "https://x/live");
    await user.click(saveButton(dialog));
    expect(
      await screen.findByText("The live broadcasts could not be saved: Rules locked")
    ).toBeInTheDocument();
    // the created channel shows in the list behind the dialog
    expect(await screen.findByText("Docs", { selector: "strong" })).toBeInTheDocument();
    expect(screen.getByRole("dialog")).toBeInTheDocument();

    patch.mockResolvedValueOnce({});
    await waitFor(() => expect(saveButton(dialog)).toBeEnabled());
    await user.click(saveButton(dialog));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(post.mock.calls.filter(([p]) => p === "/api/channels")).toHaveLength(1);
    expect(patch).toHaveBeenCalledTimes(2);
    expect(patch.mock.calls[1][0]).toBe("/api/rules/tv");
  });

  it("CHN-29: art errors show under the tile; watermark ranges block Apply", async () => {
    answers["/api/channels"] = {
      items: [
        {
          ...C1,
          art: {
            logo: { url: "/a.png", overridden: true },
            watermark: { position: "top-right", width_pct: 10, margin_pct: 2, overridden: false },
            loading: { overridden: false },
          },
        },
      ],
    };
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(true);
    upload.mockRejectedValueOnce(problem(413, "Payload Too Large", "max 5 MB"));
    del.mockRejectedValueOnce(problem(500, "Down"));
    patch.mockRejectedValueOnce(new TypeError("Failed to fetch"));
    const { user, dialog } = await openEditor("Quran");
    await user.click(within(dialog).getByRole("tab", { name: "Art" }));
    const logo = within(dialog).getByTestId("art-logo");
    const png = new File([new Uint8Array(1_000_000)], "logo.png", { type: "image/png" });
    await user.upload(within(logo).getByLabelText("Choose an image for Logo"), png);
    expect(await within(logo).findByText(/max 5 MB/)).toBeInTheDocument();
    expect(within(logo).queryByRole("progressbar")).toBeNull();
    await user.click(within(logo).getByRole("button", { name: /back to plan/i }));
    expect(await within(logo).findByText("Down")).toBeInTheDocument();
    confirm.mockRestore();

    const width = within(dialog).getByLabelText(/watermark width/i);
    const margin = within(dialog).getByLabelText(/margin from the edge/i);
    const apply = within(dialog).getByRole("button", { name: "Apply" });
    expect(apply).toBeDisabled();
    for (const v of ["0", "60"]) {
      fireEvent.change(width, { target: { value: v } });
      expect(within(dialog).getByText("Between 1 and 50.")).toBeInTheDocument();
      expect(apply).toBeDisabled();
    }
    fireEvent.change(width, { target: { value: "20" } });
    fireEvent.change(margin, { target: { value: "30" } });
    expect(within(dialog).getByText("Between 0 and 25.")).toBeInTheDocument();
    expect(apply).toBeDisabled();
    fireEvent.change(margin, { target: { value: "5" } });
    await user.click(apply);
    expect(await within(dialog).findByText("Something went wrong.")).toBeInTheDocument();

    const loading = within(dialog).getByTestId("art-loading");
    const svg = new File(["<svg/>"], "l.svg", { type: "image/svg+xml" });
    fireEvent.change(within(loading).getByLabelText("Choose an image for Loading screen"), {
      target: { files: [svg] },
    });
    expect(
      await within(loading).findByText("That file type is not accepted here.")
    ).toBeInTheDocument();
  });

  it.each([
    ["hosts null", { hosts: null, settings: { pins: null } }],
    ["settings null", { hosts: [{ name: "box1" }], settings: null }],
  ])("CHN-31: prefer host with %s does not crash", async (_, view) => {
    answers["/api/tv/cluster"] = view;
    const { dialog } = await openEditor("Quran");
    await waitFor(() => expect(get).toHaveBeenCalledWith("/api/tv/cluster"));
    const select = within(dialog).queryByLabelText("Prefer host");
    if (select) {
      expect(
        within(select)
          .getAllByRole("option")
          .map((o) => o.textContent)
      ).toEqual(["Automatic"]);
    }
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("CHN-31: a pinned gone host is listed; a failed pin toasts and re-enables the select", async () => {
    answers["/api/tv/cluster"] = {
      hosts: [{ name: "box1" }],
      settings: { pins: { quran: ["gone-host"] } },
    };
    patch.mockRejectedValueOnce(problem(500, "Down"));
    const { user, dialog } = await openEditor("Quran");
    const select = await within(dialog).findByLabelText("Prefer host");
    expect(
      within(select)
        .getAllByRole("option")
        .map((o) => o.textContent)
    ).toEqual(["Automatic", "box1", "gone-host"]);
    expect(select).toHaveValue("gone-host");
    await user.selectOptions(select, "box1");
    expect(await screen.findByText(/^Could not save the preferred host:/)).toBeInTheDocument();
    await waitFor(() => expect(within(dialog).getByLabelText("Prefer host")).toBeEnabled());
  });

  it("CHN-31: a new channel shows no prefer-host select", async () => {
    answers["/api/tv/cluster"] = { hosts: [{ name: "box1" }], settings: { pins: {} } };
    const { dialog } = await openNew();
    expect(within(dialog).queryByLabelText("Prefer host")).toBeNull();
  });

  it.each([
    ["blocks: []", { channel: { id: "c1" }, blocks: [] }, "No programs scheduled in this window."],
    [
      "blocks: null",
      { channel: { id: "c1" }, blocks: null },
      "No programs scheduled in this window.",
    ],
    ["a 500", problem(500, "Down"), "Something went wrong."],
  ])("CHN-32: preview with %s", async (_, guide, text) => {
    answers.guide = guide;
    const { dialog } = await openEditor("Quran", /^preview$/i);
    expect(within(dialog).getByRole("tab", { name: "Preview" })).toHaveAttribute(
      "aria-selected",
      "true"
    );
    expect(await within(dialog).findByText(text)).toBeInTheDocument();
  });

  it("CHN-32: filler blocks collapse into one row; regenerate confirms and toasts", async () => {
    answers.guide = {
      channel: { id: "c1" },
      blocks: [
        { kind: "filler", start: iso(0.1), stop: iso(0.2), title: "a" },
        { kind: "filler", start: iso(0.2), stop: iso(0.3), title: "b" },
        { kind: "program", start: iso(0.3), stop: iso(1), title: "Al-Imran" },
      ],
    };
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(false);
    const { user, dialog } = await openEditor("Quran", /^preview$/i);
    expect(await within(dialog).findByText("Al-Imran")).toBeInTheDocument();
    expect(within(dialog).getAllByText("Filler / bumper")).toHaveLength(1);
    expect(within(dialog).getAllByRole("heading", { level: 4 }).length).toBeGreaterThan(0);
    const regen = within(dialog).getByRole("button", { name: "Regenerate schedule" });
    await user.click(regen);
    expect(post).not.toHaveBeenCalled();
    confirm.mockReturnValue(true);
    post.mockRejectedValueOnce(problem(500, "Busy"));
    await user.click(regen);
    expect(await screen.findByText("Busy")).toBeInTheDocument();
    await waitFor(() => expect(regen).toBeEnabled());
    post.mockResolvedValueOnce({});
    await user.click(regen);
    expect(
      await screen.findByText("The schedule will be rebuilt within a few minutes.")
    ).toBeInTheDocument();
  });

  it("CHN-32: a new channel has no Preview tab", async () => {
    const { dialog } = await openNew();
    expect(within(dialog).queryByRole("tab", { name: "Preview" })).toBeNull();
  });

  it("CHN-33: Filler pools link and Back; the draft banner links to the change", async () => {
    answers["/api/channels"] = { items: [{ ...C1, draft: { change_id: "ch7", number: 4 } }] };
    const user = userEvent.setup();
    renderChannels({
      extraRoutes: (
        <>
          <Route path="/admin/filler" element={<p>filler page</p>} />
          <Route path="/admin/live-tv/changes/:id" element={<p>change page</p>} />
        </>
      ),
    });
    await user.click(await screen.findByRole("link", { name: "Filler pools" }));
    expect(screen.getByText("filler page")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "go back" }));
    await user.click(
      within(await screen.findByRole("row", { name: /quran/i })).getByRole("button", {
        name: /^edit$/i,
      })
    );
    const banner = await screen.findByTestId("tv-draft-banner");
    expect(banner).toHaveTextContent(/^Editing draft change #4:/);
    const link = within(banner).getByRole("link", { name: "Open change #4" });
    expect(link).toHaveAttribute("href", "/admin/live-tv/changes/ch7");
    await user.click(link);
    expect(screen.getByText("change page")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "go back" }));
    expect(await screen.findByRole("table", { name: "Channels" })).toBeInTheDocument();
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("CHN-34: a render crash shows the route error fallback; navigation still works", async () => {
    quietCrash();
    // an object where a string is drawn: "Objects are not valid as a React child"
    answers["/api/channels"] = { items: [{ ...C1, category: {} }] };
    const user = userEvent.setup();
    renderChannels();
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "This page hit an error and could not be shown."
    );
    expect(screen.getByRole("navigation", { name: "test shell" })).toBeInTheDocument();
    await user.click(screen.getByRole("link", { name: "go elsewhere" }));
    expect(screen.getByText("elsewhere")).toBeInTheDocument();
    answers["/api/channels"] = { items: [C1] };
    await user.click(screen.getByRole("button", { name: "go back" }));
    expect(await screen.findByText("Quran", { selector: "strong" })).toBeInTheDocument();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("CHN-38: names and roles for the list, dialog, tabs and grid", async () => {
    const user = userEvent.setup();
    renderChannels();
    await screen.findByText("Quran", { selector: "strong" });
    expect(screen.getByRole("columnheader", { name: "Order" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Move Quran" })).toBeInTheDocument();
    expect(screen.getByRole("switch", { name: "Quran on or off" })).toBeInTheDocument();
    const edit = within(rowOf("Quran")).getByRole("button", { name: /^edit$/i });
    edit.focus();
    await user.keyboard("{Enter}");
    const dialog = await screen.findByRole("dialog", { name: "Edit Quran" });
    expect(dialog).toHaveAttribute("aria-modal", "true");
    const basics = within(dialog).getByRole("tab", { name: "Basics" });
    basics.focus();
    await user.keyboard("{ArrowRight}");
    expect(within(dialog).getByRole("tab", { name: "Art" })).toHaveAttribute(
      "aria-selected",
      "true"
    );
    await user.keyboard("{ArrowRight}");
    const block = within(dialog).getAllByRole("button", { name: /00:00–24:00/ })[0];
    expect(block).toHaveAttribute("aria-pressed", "false");
    block.focus();
    await user.keyboard("{Enter}");
    expect(block).toHaveAttribute("aria-pressed", "true");
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(edit).toHaveFocus();
  });
});
