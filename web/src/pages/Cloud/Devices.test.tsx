// Admin devices cases from web/test-cases/admin-devices.md.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderPage, memberUser, quietCrash } from "../../test/pageHarness";
import { routeGet } from "../../test/queueHarness";
import { ToastProvider } from "@ds/components/Toast/Toast";

const { get, post, patch, del } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  patch: vi.fn(),
  del: vi.fn(),
}));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post, put: vi.fn(), patch, delete: del } };
});

import { ApiError } from "../../lib/api";
import { CloudDevices } from "./Devices";

const DEV = "/api/devices";
const CRASH = "This page hit an error and could not be shown.";

const d1 = {
  id: "d1",
  user_id: "u1",
  platform: "ios",
  bundle_id: "com.maktaba.app",
  app_version: "2.1.0",
  locale: "en",
  categories: ["job"],
  registered_at: "2026-09-01T10:00:00Z",
  last_seen_at: "2026-10-18T09:00:00Z",
};
const d2 = {
  id: "d2",
  user_id: "u1",
  platform: "android",
  bundle_id: "com.maktaba.android",
  registered_at: "2026-09-02T10:00:00Z",
  last_seen_at: "2026-10-17T09:00:00Z",
};
const d3 = {
  id: "d3",
  user_id: "u1",
  platform: "web",
  bundle_id: "web",
  registered_at: "2026-09-03T10:00:00Z",
  last_seen_at: "2026-10-16T09:00:00Z",
};

const open = (user?: typeof memberUser | null) =>
  renderPage(
    <ToastProvider>
      <CloudDevices />
    </ToastProvider>,
    { path: "/admin/devices", ...(user !== undefined ? { user } : {}) }
  );

const problem = (status: number, title: string, detail?: string) =>
  new ApiError({ type: "about:blank", title, status, detail });

const table = () => screen.findByRole("table", { name: "Devices" });
const bodyRows = () =>
  within(screen.getByRole("table", { name: "Devices" }))
    .getAllByRole("row")
    .slice(1);
const dialog = (name: string) => screen.findByRole("dialog", { name });

describe("CloudDevices (test-cases/admin-devices.md)", () => {
  beforeEach(() => {
    get.mockReset();
    post.mockReset();
    patch.mockReset();
    del.mockReset();
  });
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("DEV-01: lists devices in a table with all columns", async () => {
    routeGet(get, { [DEV]: { items: [d1, d2, d3] } });
    open();
    await table();
    expect(screen.getByRole("heading", { level: 1, name: "Devices" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Register device" })).toBeInTheDocument();
    expect(
      screen.getByText(
        "The device API is scoped to the signed-in account; this lists devices registered to you."
      )
    ).toBeInTheDocument();
    const heads = screen.getAllByRole("columnheader").map((h) => h.textContent);
    expect(heads).toEqual(["Platform", "App", "App version", "Last seen", "Registered", "Actions"]);
    const [r1, r2, r3] = bodyRows();
    expect(within(r1).getAllByRole("cell")[0]).toHaveTextContent("iOS");
    expect(within(r2).getAllByRole("cell")[0]).toHaveTextContent("🤖 Android");
    expect(within(r3).getAllByRole("cell")[0]).toHaveTextContent("🌐 Web");
    expect(within(r1).getByText("com.maktaba.app")).toBeInTheDocument();
    expect(within(r1).getByText("2.1.0")).toBeInTheDocument();
    expect(within(r2).getAllByRole("cell")[2]).toHaveTextContent("—");
    expect(within(r1).getAllByRole("cell")[3].textContent).toMatch(/2026/);
    for (const r of [r1, r2, r3]) {
      expect(within(r).getByRole("button", { name: "Edit" })).toBeInTheDocument();
      expect(within(r).getByRole("button", { name: "Remove" })).toBeInTheDocument();
    }
  });

  it("DEV-02: non-admin sees Admins only; no request", async () => {
    routeGet(get, { [DEV]: { items: [d1] } });
    open(memberUser);
    expect(await screen.findByText("Admins only")).toBeInTheDocument();
    expect(screen.queryByRole("table")).toBeNull();
    expect(screen.queryByRole("button", { name: "Register device" })).toBeNull();
    expect(get).not.toHaveBeenCalled();
  });

  it("DEV-03: signed-out user is sent to /login", async () => {
    open(null);
    expect(await screen.findByText("login page (from /admin/devices)")).toBeInTheDocument();
    expect(get).not.toHaveBeenCalled();
  });

  it("DEV-04: a table-shaped placeholder before the list arrives; Register already usable", async () => {
    const user = userEvent.setup();
    routeGet(get, { [DEV]: () => new Promise(() => {}) });
    open();
    // busy at once; the table's shape after 0.4 s; never a "Loading…" text
    const busy = await waitFor(() => document.querySelector('[aria-busy="true"]')!);
    await waitFor(() => expect(busy.querySelector("table.mkt-table thead")).not.toBeNull());
    expect(screen.queryByText("Loading…")).toBeNull();
    await user.click(screen.getByRole("button", { name: "Register device" }));
    expect(await dialog("Register device")).toBeInTheDocument();
  });

  it("DEV-05: items [] shows the empty state", async () => {
    routeGet(get, { [DEV]: { items: [] } });
    open();
    expect(await screen.findByText("No devices")).toBeInTheDocument();
    expect(
      screen.getByText("Register a device to receive push notifications.")
    ).toBeInTheDocument();
    expect(screen.queryByRole("table")).toBeNull();
  });

  it.each([
    ["items null", { items: null }],
    ["items missing", {}],
    ["body null", null],
    ["items not an array", { items: "oops" }],
  ])("DEV-06: %s shows the empty state, no crash", async (_, body) => {
    routeGet(get, { [DEV]: body });
    open();
    expect(await screen.findByText("No devices")).toBeInTheDocument();
    expect(screen.queryByText(CRASH)).toBeNull();
  });

  it("DEV-06: categories and locale null render; Edit opens with nothing ticked", async () => {
    const user = userEvent.setup();
    routeGet(get, { [DEV]: { items: [{ ...d1, categories: null, locale: null }] } });
    open();
    await table();
    await user.click(screen.getByRole("button", { name: "Edit" }));
    const dlg = await dialog("Edit device");
    for (const c of ["Jobs", "Library", "Subscription", "System"]) {
      expect(within(dlg).getByRole("checkbox", { name: c })).not.toBeChecked();
    }
    expect(within(dlg).getByLabelText("Locale")).toHaveValue("");
  });

  it("DEV-07: missing dates and an unknown platform render readably", async () => {
    routeGet(get, {
      [DEV]: { items: [{ id: "d9", user_id: "u1", platform: "windows", bundle_id: "" }] },
    });
    open();
    await table();
    const cells = within(bodyRows()[0]).getAllByRole("cell");
    expect(cells[0]).toHaveTextContent("📱 windows");
    expect(cells[0]).not.toHaveTextContent("devices.platform");
    expect(cells[2]).toHaveTextContent("—");
    expect(cells[3]).toHaveTextContent("—");
    expect(cells[4]).toHaveTextContent("—");
    expect(cells[3]).not.toHaveTextContent("undefined");
  });

  it.each([
    ["500 with detail", problem(500, "Internal Server Error", "db down"), "db down"],
    ["403 no detail", problem(403, "Forbidden"), "Forbidden"],
    ["network", new TypeError("Failed to fetch"), null],
    ["bad JSON", new SyntaxError("Unexpected end of JSON input"), null],
  ])("DEV-08: load fails (%s): error state and Try again", async (_, err, desc) => {
    routeGet(get, { [DEV]: () => Promise.reject(err) });
    open();
    expect(await screen.findByRole("button", { name: "Try again" })).toBeInTheDocument();
    const alert = screen.getByRole("alert");
    if (desc) expect(within(alert).getByText(desc)).toBeInTheDocument();
    else expect(within(alert).getAllByText("Something went wrong.")).toHaveLength(2);
    expect(screen.queryByRole("table")).toBeNull();
    expect(screen.queryByText("No devices")).toBeNull();
  });

  it("DEV-09: Try again shows the loading region, then the list", async () => {
    const user = userEvent.setup();
    let resolve: (v: unknown) => void = () => {};
    let n = 0;
    routeGet(get, {
      [DEV]: () =>
        n++ === 0
          ? Promise.reject(problem(500, "x", "db down"))
          : new Promise((r) => {
              resolve = r;
            }),
    });
    open();
    await user.click(await screen.findByRole("button", { name: "Try again" }));
    expect(document.querySelector('[aria-busy="true"]')).not.toBeNull();
    expect(screen.queryByText("No devices")).toBeNull();
    await act(async () => resolve({ items: [d1] }));
    await table();
    expect(bodyRows()).toHaveLength(1);
  });

  it("DEV-10: Register is disabled until token and bundle ID have text", async () => {
    const user = userEvent.setup();
    routeGet(get, { [DEV]: { items: [] } });
    open();
    await screen.findByText("No devices");
    await user.click(screen.getByRole("button", { name: "Register device" }));
    const dlg = await dialog("Register device");
    const platform = within(dlg).getByLabelText("Platform");
    expect(platform).toHaveValue("web");
    expect(
      within(platform)
        .getAllByRole("option")
        .map((o) => o.textContent)
    ).toEqual(["Web", "iOS", "Android"]);
    const submit = within(dlg).getByRole("button", { name: "Register device" });
    expect(submit).toBeDisabled();
    await user.type(within(dlg).getByLabelText(/Push token/), "tok");
    expect(submit).toBeDisabled();
    await user.type(within(dlg).getByLabelText(/Bundle ID/), " ");
    expect(submit).toBeDisabled();
    await user.type(within(dlg).getByLabelText(/Bundle ID/), "com.x");
    expect(submit).toBeEnabled();
    const group = within(dlg).getByRole("group", { name: "Notification categories" });
    for (const c of ["Jobs", "Library", "Subscription", "System"]) {
      expect(within(group).getByRole("checkbox", { name: c })).not.toBeChecked();
    }
  });

  it("DEV-11: register success posts a trimmed body, toasts, closes and reloads", async () => {
    const user = userEvent.setup();
    let n = 0;
    routeGet(get, { [DEV]: () => ({ items: n++ === 0 ? [] : [d1] }) });
    post.mockResolvedValue({});
    open();
    await screen.findByText("No devices");
    await user.click(screen.getByRole("button", { name: "Register device" }));
    const dlg = await dialog("Register device");
    await user.selectOptions(within(dlg).getByLabelText("Platform"), "ios");
    await user.type(within(dlg).getByLabelText(/Push token/), " abc123 ");
    await user.type(within(dlg).getByLabelText(/Bundle ID/), "com.maktaba.app");
    await user.click(within(dlg).getByRole("checkbox", { name: "Jobs" }));
    await user.click(within(dlg).getByRole("checkbox", { name: "System" }));
    await user.click(within(dlg).getByRole("button", { name: "Register device" }));
    expect(post).toHaveBeenCalledWith("/api/devices/register", {
      platform: "ios",
      token: "abc123",
      bundle_id: "com.maktaba.app",
      categories: ["job", "system"],
    });
    expect(await screen.findByText("Device registered.")).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    await table();
    expect(get).toHaveBeenCalledTimes(2);
  });

  it.each([
    [
      "ApiError",
      problem(400, "Bad Request", "token already registered"),
      "token already registered",
    ],
    ["network", new TypeError("Failed to fetch"), "Something went wrong."],
  ])("DEV-12: register failure (%s) keeps the dialog open", async (_, err, msg) => {
    const user = userEvent.setup();
    routeGet(get, { [DEV]: { items: [] } });
    post.mockRejectedValue(err);
    open();
    await screen.findByText("No devices");
    await user.click(screen.getByRole("button", { name: "Register device" }));
    const dlg = await dialog("Register device");
    await user.type(within(dlg).getByLabelText(/Push token/), "tok");
    await user.type(within(dlg).getByLabelText(/Bundle ID/), "com.x");
    await user.click(within(dlg).getByRole("button", { name: "Register device" }));
    expect(await screen.findByText(msg)).toBeInTheDocument();
    expect(screen.getByRole("dialog", { name: "Register device" })).toBeInTheDocument();
    expect(within(dlg).getByLabelText(/Push token/)).toHaveValue("tok");
    expect(within(dlg).getByRole("button", { name: "Register device" })).toBeEnabled();
    expect(get).toHaveBeenCalledTimes(1);
  });

  it("DEV-13: Enter submits only when valid, and only once while pending", async () => {
    const user = userEvent.setup();
    routeGet(get, { [DEV]: { items: [] } });
    post.mockImplementation(() => new Promise(() => {}));
    open();
    await screen.findByText("No devices");
    await user.click(screen.getByRole("button", { name: "Register device" }));
    const dlg = await dialog("Register device");
    await user.type(within(dlg).getByLabelText(/Push token/), "tok{Enter}");
    expect(post).not.toHaveBeenCalled();
    await user.type(within(dlg).getByLabelText(/Bundle ID/), "com.x{Enter}{Enter}");
    expect(post).toHaveBeenCalledTimes(1);
  });

  it("DEV-14: edit is pre-filled and PATCHes categories and trimmed locale", async () => {
    const user = userEvent.setup();
    routeGet(get, { [DEV]: { items: [d1] } });
    patch.mockResolvedValue({});
    open();
    await table();
    await user.click(screen.getByRole("button", { name: "Edit" }));
    const dlg = await dialog("Edit device");
    expect(within(dlg).getByRole("checkbox", { name: "Jobs" })).toBeChecked();
    const locale = within(dlg).getByLabelText("Locale");
    expect(locale).toHaveValue("en");
    await user.click(within(dlg).getByRole("checkbox", { name: "Jobs" }));
    await user.click(within(dlg).getByRole("checkbox", { name: "Library" }));
    await user.clear(locale);
    await user.type(locale, " ar ");
    await user.click(within(dlg).getByRole("button", { name: "Save" }));
    expect(patch).toHaveBeenCalledWith("/api/devices/d1", {
      categories: ["library"],
      locale: "ar",
    });
    expect(await screen.findByText("Device updated.")).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(get).toHaveBeenCalledTimes(2);
  });

  it("DEV-15: clearing the locale sends an empty locale so the server clears it", async () => {
    const user = userEvent.setup();
    routeGet(get, { [DEV]: { items: [d1] } });
    patch.mockResolvedValue({});
    open();
    await table();
    await user.click(screen.getByRole("button", { name: "Edit" }));
    const dlg = await dialog("Edit device");
    await user.clear(within(dlg).getByLabelText("Locale"));
    await user.click(within(dlg).getByRole("button", { name: "Save" }));
    expect(patch).toHaveBeenCalledWith("/api/devices/d1", { categories: ["job"], locale: "" });
    expect(JSON.parse(JSON.stringify(patch.mock.calls[0][1]))).toHaveProperty("locale", "");
  });

  it("DEV-16: edit failure shows an error toast and keeps the dialog", async () => {
    const user = userEvent.setup();
    routeGet(get, { [DEV]: { items: [d1] } });
    patch.mockRejectedValue(problem(404, "Not Found"));
    open();
    await table();
    await user.click(screen.getByRole("button", { name: "Edit" }));
    const dlg = await dialog("Edit device");
    await user.click(within(dlg).getByRole("button", { name: "Save" }));
    expect(await screen.findByText("Not Found")).toBeInTheDocument();
    expect(screen.getByRole("dialog", { name: "Edit device" })).toBeInTheDocument();
    expect(within(dlg).getByRole("button", { name: "Save" })).toBeEnabled();
  });

  it("DEV-17: remove asks first; backdrop does not close; Cancel sends nothing", async () => {
    const user = userEvent.setup();
    routeGet(get, { [DEV]: { items: [d1] } });
    open();
    await table();
    await user.click(screen.getByRole("button", { name: "Remove" }));
    const dlg = await dialog("Remove device");
    expect(
      within(dlg).getByText("Remove this device? It will stop receiving notifications.")
    ).toBeInTheDocument();
    const overlay = dlg.parentElement!;
    await user.pointer({ keys: "[MouseLeft]", target: overlay });
    expect(screen.getByRole("dialog", { name: "Remove device" })).toBeInTheDocument();
    await user.click(within(dlg).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(del).not.toHaveBeenCalled();
  });

  it("DEV-18: confirm sends one DELETE, toasts, closes and reloads", async () => {
    const user = userEvent.setup();
    let n = 0;
    routeGet(get, { [DEV]: () => ({ items: n++ === 0 ? [d1] : [] }) });
    let finish: () => void = () => {};
    del.mockImplementation(
      () =>
        new Promise<void>((r) => {
          finish = r;
        })
    );
    open();
    await table();
    await user.click(screen.getByRole("button", { name: "Remove" }));
    const dlg = await dialog("Remove device");
    const confirm = within(dlg).getByRole("button", { name: "Remove" });
    await user.click(confirm);
    await user.click(confirm);
    expect(del).toHaveBeenCalledTimes(1);
    expect(del).toHaveBeenCalledWith("/api/devices/d1");
    expect(confirm).toHaveAttribute("aria-busy", "true");
    await act(async () => finish());
    expect(await screen.findByText("Device removed.")).toBeInTheDocument();
    expect(await screen.findByText("No devices")).toBeInTheDocument();
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("DEV-19: remove failure keeps the dialog open with an error toast", async () => {
    const user = userEvent.setup();
    routeGet(get, { [DEV]: { items: [d1] } });
    del.mockRejectedValue(problem(500, "Internal Server Error", "boom"));
    open();
    await table();
    await user.click(screen.getByRole("button", { name: "Remove" }));
    const dlg = await dialog("Remove device");
    await user.click(within(dlg).getByRole("button", { name: "Remove" }));
    expect(await screen.findByText("boom")).toBeInTheDocument();
    expect(screen.getByRole("dialog", { name: "Remove device" })).toBeInTheDocument();
    // the dialog makes the page inert, so look past it for the row
    expect(screen.getByRole("cell", { name: "com.maktaba.app", hidden: true })).toBeInTheDocument();
  });

  it("DEV-20: ids with special characters are URL-encoded", async () => {
    const user = userEvent.setup();
    routeGet(get, { [DEV]: { items: [{ ...d1, id: "a/b?c#d" }] } });
    patch.mockResolvedValue({});
    del.mockResolvedValue(undefined);
    open();
    await table();
    await user.click(screen.getByRole("button", { name: "Edit" }));
    await user.click(within(await dialog("Edit device")).getByRole("button", { name: "Save" }));
    expect(patch.mock.calls[0][0]).toBe("/api/devices/a%2Fb%3Fc%23d");
    await table();
    await user.click(await screen.findByRole("button", { name: "Remove" }));
    await user.click(within(await dialog("Remove device")).getByRole("button", { name: "Remove" }));
    expect(del).toHaveBeenCalledWith("/api/devices/a%2Fb%3Fc%23d");
  });

  it("DEV-21: a render crash shows the route error fallback; Back works", async () => {
    const user = userEvent.setup();
    quietCrash();
    routeGet(get, { [DEV]: { items: [{ ...d1, bundle_id: {} }] } });
    open();
    expect(await screen.findByText(CRASH)).toBeInTheDocument();
    expect(screen.getByRole("navigation", { name: "test shell" })).toBeInTheDocument();
    await user.click(screen.getByRole("link", { name: "go elsewhere" }));
    expect(await screen.findByText("elsewhere")).toBeInTheDocument();
    routeGet(get, { [DEV]: { items: [d1] } });
    await user.click(screen.getByRole("button", { name: "go back" }));
    expect(await table()).toBeInTheDocument();
  });

  it("DEV-22: leaving with the Register dialog open unmounts it and restores scroll", async () => {
    const user = userEvent.setup();
    routeGet(get, { [DEV]: { items: [] } });
    open();
    await screen.findByText("No devices");
    await user.click(screen.getByRole("button", { name: "Register device" }));
    await dialog("Register device");
    expect(document.body.style.overflow).toBe("hidden");
    act(() => {
      (
        screen.getByRole("link", { name: "go elsewhere", hidden: true }) as HTMLAnchorElement
      ).click();
    });
    expect(await screen.findByText("elsewhere")).toBeInTheDocument();
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(document.body.style.overflow).toBe("");
  });

  it("DEV-23: Arabic: RTL with Arabic headings and platform names", async () => {
    localStorage.setItem("mkt:locale", "ar");
    routeGet(get, { [DEV]: { items: [d1, d3] } });
    open();
    expect(await screen.findByRole("heading", { level: 1, name: "الأجهزة" })).toBeInTheDocument();
    expect(document.documentElement.dir).toBe("rtl");
    expect(screen.getByText("com.maktaba.app")).toBeInTheDocument();
    expect(screen.getByRole("cell", { name: /الويب/ })).toBeInTheDocument();
  });

  it("DEV-26: dialogs are modal, close on Escape and return focus", async () => {
    const user = userEvent.setup();
    routeGet(get, { [DEV]: { items: [d1] } });
    open();
    await table();
    const reg = screen.getByRole("button", { name: "Register device" });
    await user.click(reg);
    const dlg = await dialog("Register device");
    expect(dlg).toHaveAttribute("aria-modal", "true");
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    await waitFor(() => expect(reg).toHaveFocus());
    const remove = screen.getByRole("button", { name: "Remove" });
    await user.click(remove);
    await dialog("Remove device");
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    await waitFor(() => expect(remove).toHaveFocus());
  });
});
