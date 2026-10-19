// "My devices" page cases from web/test-cases/account-devices.md.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { get, post, put, patch, del } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  put: vi.fn(),
  patch: vi.fn(),
  del: vi.fn(),
}));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post, put, patch, delete: del } };
});

import { ApiError } from "../../lib/api";
import { ToastProvider } from "@ds/components/Toast/Toast";
import { renderPage, quietCrash } from "../../test/pageHarness";
import { routeGet } from "../../test/queueHarness";
import ar from "../../i18n/ar.json";
import { ConnectedDevices } from "./ConnectedDevices";

const IOS = {
  id: "d1",
  user_id: "u1",
  platform: "ios",
  bundle_id: "app.maktaba.ios",
  app_version: "1.4.0",
  registered_at: "2026-09-01T10:00:00Z",
  last_seen_at: "2026-10-18T08:30:00Z",
};
const ANDROID = {
  id: "d2",
  user_id: "u1",
  platform: "android",
  bundle_id: "app.maktaba.android",
  registered_at: "2026-09-02T10:00:00Z",
  last_seen_at: "2026-10-17T08:30:00Z",
};

function problem(status: number, title: string, detail?: string) {
  return new ApiError({ type: "about:blank", status, title, detail });
}

function deferred<T>() {
  let resolve!: (v: T) => void;
  const promise = new Promise<T>((r) => (resolve = r));
  return { promise, resolve };
}

/** GET /api/me/devices answers with each value in turn (the last one repeats). */
function devicesSeq(...replies: unknown[]) {
  let i = 0;
  get.mockImplementation((url: string) => {
    if (url !== "/api/me/devices") return Promise.reject(new Error(`unmocked GET ${url}`));
    const r = replies[Math.min(i++, replies.length - 1)];
    return r instanceof Error ? Promise.reject(r) : Promise.resolve(r);
  });
}

function open(opts: { user?: null } = {}) {
  return renderPage(
    <ToastProvider>
      <ConnectedDevices />
    </ToastProvider>,
    { path: "/account/devices", ...opts }
  );
}

const card = (text: string) => screen.getByText(text).closest(".mkt-device-card") as HTMLElement;

async function openRemoveDialog(platform = "iOS") {
  const user = userEvent.setup();
  await screen.findByText(platform);
  await user.click(within(card(platform)).getByRole("button", { name: "Remove" }));
  const dialog = await screen.findByRole("dialog", { name: "Remove device" });
  return { user, dialog };
}

describe("ConnectedDevices (test-cases/account-devices.md)", () => {
  beforeEach(() => {
    for (const f of [get, post, put, patch, del]) f.mockReset();
    routeGet(get, { "/api/me/devices": { items: [IOS, ANDROID] } });
  });

  it("ADV-01: device cards render", async () => {
    open();
    expect(await screen.findByText("iOS")).toBeInTheDocument();
    expect(screen.getByRole("heading", { level: 1, name: "Devices" })).toBeInTheDocument();
    const ios = card("iOS");
    const android = card("Android");
    expect(ios.compareDocumentPosition(android) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(within(ios).getByText("app.maktaba.ios")).toBeInTheDocument();
    expect(within(ios).getByText("Last seen")).toBeInTheDocument();
    expect(within(ios).getByText("App version")).toBeInTheDocument();
    expect(within(ios).getByText("1.4.0")).toBeInTheDocument();
    expect(within(ios).getByRole("button", { name: "Remove" })).toBeInTheDocument();
    expect(within(android).queryByText("App version")).toBeNull();
    expect(ios.querySelector(".mkt-device-card__icon")).toHaveAttribute("aria-hidden", "true");
  });

  it("ADV-02: remove a device through the confirm dialog", async () => {
    devicesSeq({ items: [IOS, ANDROID] }, { items: [ANDROID] });
    del.mockResolvedValue(undefined);
    open();
    const { user, dialog } = await openRemoveDialog();
    expect(dialog).toHaveTextContent("Remove this device? It will stop receiving notifications.");
    expect(within(dialog).getByRole("button", { name: "Cancel" })).toBeInTheDocument();
    await user.click(within(dialog).getByRole("button", { name: "Remove" }));

    expect(await screen.findByRole("status")).toHaveTextContent("Device removed.");
    expect(del).toHaveBeenCalledTimes(1);
    expect(del).toHaveBeenCalledWith("/api/devices/d1");
    expect(screen.queryByRole("dialog")).toBeNull();
    await waitFor(() => expect(screen.queryByText("iOS")).toBeNull());
    expect(screen.getByText("Android")).toBeInTheDocument();
  });

  it("ADV-03: Cancel, Escape and the X close the dialog; the backdrop does not", async () => {
    open();
    const { user, dialog } = await openRemoveDialog();
    const opener = within(card("iOS")).getByRole("button", { name: "Remove", hidden: true });
    expect(dialog).toHaveAttribute("aria-modal", "true");
    expect(dialog.contains(document.activeElement)).toBe(true);
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    expect(screen.queryByRole("dialog")).toBeNull();
    await waitFor(() => expect(opener).toHaveFocus());

    await openRemoveDialog();
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("dialog")).toBeNull();

    const third = await openRemoveDialog();
    await user.click(within(third.dialog).getByRole("button", { name: "Close" }));
    expect(screen.queryByRole("dialog")).toBeNull();

    const fourth = await openRemoveDialog();
    await user.pointer({ keys: "[MouseLeft]", target: fourth.dialog.parentElement! });
    expect(screen.getByRole("dialog", { name: "Remove device" })).toBeInTheDocument();
    expect(del).not.toHaveBeenCalled();
  });

  it.each([
    ["404 with detail", problem(404, "Not Found", "device not found"), "device not found"],
    ["403 without detail", problem(403, "Forbidden"), "Forbidden"],
    ["500 without detail", problem(500, "Internal Server Error"), "Internal Server Error"],
    ["network failure", new TypeError("Failed to fetch"), "Something went wrong."],
  ])("ADV-04: remove fails (%s): error toast, dialog stays, device stays", async (_, err, msg) => {
    del.mockRejectedValue(err);
    open();
    const { user, dialog } = await openRemoveDialog();
    await user.click(within(dialog).getByRole("button", { name: "Remove" }));

    expect(await screen.findByRole("alert", { hidden: true })).toHaveTextContent(msg);
    expect(screen.getByRole("dialog", { name: "Remove device" })).toBeInTheDocument();
    expect(screen.getByText("iOS")).toBeInTheDocument();
    expect(get).toHaveBeenCalledTimes(1);
  });

  it("ADV-05: double click on confirm Remove sends one DELETE", async () => {
    const pending = deferred<undefined>();
    del.mockReturnValue(pending.promise);
    open();
    const { user, dialog } = await openRemoveDialog();
    const confirm = within(dialog).getByRole("button", { name: "Remove" });
    await user.click(confirm);
    await user.click(confirm);
    expect(confirm).toHaveAttribute("aria-busy", "true");
    expect(del).toHaveBeenCalledTimes(1);
    pending.resolve(undefined);
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  });

  it("ADV-06: removing the last device shows the empty state", async () => {
    devicesSeq({ items: [IOS] }, { items: [] });
    del.mockResolvedValue(undefined);
    open();
    const { user, dialog } = await openRemoveDialog();
    await user.click(within(dialog).getByRole("button", { name: "Remove" }));
    expect(await screen.findByText("No devices")).toBeInTheDocument();
    expect(
      screen.getByText("Register a device to receive push notifications.")
    ).toBeInTheDocument();
    expect(screen.queryByText("iOS")).toBeNull();
  });

  it("ADV-07: items [] shows the empty state", async () => {
    routeGet(get, { "/api/me/devices": { items: [] } });
    open();
    expect(await screen.findByText("No devices")).toBeInTheDocument();
    expect(
      screen.getByText("Register a device to receive push notifications.")
    ).toBeInTheDocument();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it.each([
    ["items null", { items: null }],
    ["items missing", {}],
    ["HTML body", "<!doctype html><html></html>"],
  ])("ADV-08: %s shows the empty state", async (_, body) => {
    routeGet(get, { "/api/me/devices": body });
    open();
    expect(await screen.findByText("No devices")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("ADV-08: a null body shows the error state with Retry", async () => {
    routeGet(get, { "/api/me/devices": null });
    open();
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Something went wrong.");
    expect(within(alert).getByRole("button", { name: "Try again" })).toBeInTheDocument();
  });

  it("ADV-09: null / missing optional device fields", async () => {
    const base = { ...ANDROID, platform: "web" };
    routeGet(get, {
      "/api/me/devices": {
        items: [
          { ...base, id: "a", app_version: null },
          { ...base, id: "b", app_version: "" },
          { ...base, id: "c", bundle_id: null },
          { ...base, id: "d", last_seen_at: null },
          { ...base, id: "e", last_seen_at: undefined },
          { ...base, id: "f", categories: null },
        ],
      },
    });
    del.mockResolvedValue(undefined);
    open();
    const cards = await waitFor(() => {
      const found = document.querySelectorAll<HTMLElement>(".mkt-device-card");
      expect(found).toHaveLength(6);
      return found;
    });
    expect(screen.queryByText("App version")).toBeNull();
    expect(cards[3]).toHaveTextContent("Never");
    expect(cards[4]).toHaveTextContent("Never");
    for (const c of cards) {
      expect(c).not.toHaveTextContent("1970");
      expect(c).not.toHaveTextContent("undefined");
    }
    const user = userEvent.setup();
    await user.click(within(cards[4]).getByRole("button", { name: "Remove" }));
    await user.click(
      within(await screen.findByRole("dialog")).getByRole("button", { name: "Remove" })
    );
    await waitFor(() => expect(del).toHaveBeenCalledWith("/api/devices/e"));
  });

  it("ADV-10: unknown platform shows a readable label, not the i18n key", async () => {
    routeGet(get, {
      "/api/me/devices": {
        items: [
          { ...ANDROID, id: "w", platform: "windows" },
          { ...ANDROID, id: "e", platform: "" },
          { ...ANDROID, id: "n", platform: null },
        ],
      },
    });
    open();
    expect(await screen.findByText("windows")).toBeInTheDocument();
    expect(screen.queryByText(/devices\.platform\./)).toBeNull();
    expect(document.querySelectorAll(".mkt-device-card")).toHaveLength(3);
  });

  it("ADV-11: an id with special characters is URL-encoded", async () => {
    routeGet(get, { "/api/me/devices": { items: [{ ...IOS, id: "a/b c?#" }] } });
    del.mockResolvedValue(undefined);
    open();
    const { user, dialog } = await openRemoveDialog();
    await user.click(within(dialog).getByRole("button", { name: "Remove" }));
    await waitFor(() => expect(del).toHaveBeenCalledWith("/api/devices/a%2Fb%20c%3F%23"));
  });

  it.each([
    ["500 with detail", problem(500, "Internal Server Error", "db down"), "db down"],
    ["404", problem(404, "Not Found"), "Not Found"],
    ["network failure", new TypeError("Failed to fetch"), "Something went wrong."],
    ["malformed JSON", new SyntaxError("Unexpected token"), "Something went wrong."],
  ])("ADV-13: list load error (%s) and Retry", async (_, err, msg) => {
    devicesSeq(err, { items: [IOS, ANDROID] });
    open();
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Something went wrong.");
    expect(alert).toHaveTextContent(msg);
    expect(screen.queryByText("No devices")).toBeNull();
    await userEvent.setup().click(within(alert).getByRole("button", { name: "Try again" }));
    expect(await screen.findByText("iOS")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("ADV-14: 401 mid-session never leaves a blank page", async () => {
    devicesSeq(problem(401, "Unauthorized"));
    open();
    expect(await screen.findByRole("alert")).toHaveTextContent("Unauthorized");
    expect(screen.getByRole("heading", { name: "Devices" })).toBeInTheDocument();
  });

  // Desired: a 401 sends the user to sign in (shared 401 handling in
  // src/lib/api.ts / src/lib/auth.tsx).
  it.fails("ADV-14: 401 mid-session redirects to /login", async () => {
    devicesSeq(problem(401, "Unauthorized"));
    open();
    expect(await screen.findByText("login page (from /account/devices)")).toBeInTheDocument();
  });

  it("ADV-15: signed out deep link redirects to /login", async () => {
    open({ user: null });
    expect(await screen.findByText("login page (from /account/devices)")).toBeInTheDocument();
    expect(get).not.toHaveBeenCalled();
  });

  it("ADV-16: crash during render shows the route fallback; Back recovers", async () => {
    const quiet = quietCrash();
    routeGet(get, {
      "/api/me/devices": { items: [{ ...IOS, bundle_id: { x: 1 } }] },
    });
    open();
    const user = userEvent.setup();
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "This page hit an error and could not be shown."
    );
    await user.click(screen.getByRole("link", { name: "go elsewhere" }));
    expect(await screen.findByText("elsewhere")).toBeInTheDocument();

    routeGet(get, { "/api/me/devices": { items: [IOS] } });
    await user.click(screen.getByRole("button", { name: "go back" }));
    expect(await screen.findByText("iOS")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).toBeNull();
    quiet.mockRestore();
  });

  it("ADV-17: leaving with the dialog open removes it and unlocks scroll", async () => {
    open();
    const { user } = await openRemoveDialog();
    expect(document.body.style.overflow).toBe("hidden");
    // The background is inert while the dialog is open; navigate via the
    // shell link the way browser Back would, outside the dialog.
    await user.click(screen.getByRole("link", { name: "go elsewhere", hidden: true }));
    expect(await screen.findByText("elsewhere")).toBeInTheDocument();
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(document.body.style.overflow).toBe("");
    await user.click(screen.getByRole("button", { name: "go back" }));
    expect(await screen.findByText("iOS")).toBeInTheDocument();
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("ADV-18: Arabic / RTL", async () => {
    localStorage.setItem("mkt:locale", "ar");
    open();
    expect(
      await screen.findByRole("heading", { level: 1, name: ar["devices.title"] })
    ).toBeInTheDocument();
    expect(document.documentElement).toHaveAttribute("dir", "rtl");
    expect(screen.getAllByText(ar["devices.col.lastSeen"])).toHaveLength(2);
    expect(screen.getByText(ar["devices.col.appVersion"])).toBeInTheDocument();
    const user = userEvent.setup();
    await user.click(screen.getAllByRole("button", { name: ar["devices.remove"] })[0]);
    const dialog = await screen.findByRole("dialog", { name: ar["devices.remove.title"] });
    expect(dialog).toHaveTextContent(ar["devices.remove.confirm"]);
    expect(within(dialog).getByRole("button", { name: ar["common.cancel"] })).toBeInTheDocument();
  });

  it("ADV-21: dialog is labelled by its title; toasts use a live region", async () => {
    del.mockResolvedValue(undefined);
    open();
    const { user, dialog } = await openRemoveDialog();
    const titleId = dialog.getAttribute("aria-labelledby")!;
    expect(document.getElementById(titleId)).toHaveTextContent("Remove device");
    await user.click(within(dialog).getByRole("button", { name: "Remove" }));
    expect(await screen.findByRole("status")).toHaveAttribute("aria-live", "polite");
  });

  // Minor a11y bug in ConnectedDevices.tsx: every "Remove" has the same name.
  // Needs an i18n key (e.g. devices.removeNamed) for a translated label.
  it.todo("ADV-21: each Remove button names its device");
});
