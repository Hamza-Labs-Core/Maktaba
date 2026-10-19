// Billing & license page cases from web/test-cases/billing.md.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
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
import type { User } from "../../lib/auth";
import { ToastProvider } from "@ds/components/Toast/Toast";
import { renderPage, quietCrash, memberUser } from "../../test/pageHarness";
import { routeGet } from "../../test/queueHarness";
import ar from "../../i18n/ar.json";
import { Billing } from "./Billing";

const PRO = {
  tier: "pro",
  license_id: "lic_0123456789",
  seats: 5,
  features: { transcode_hw: true, cloud_relay: false, analytics: true },
};
const FREE = { tier: "free", features: {} };
const LICENSE =
  '{"license":{"license_id":"lic_0123456789","tier":"pro","seats":5},"signature":"abc"}';

function problem(status: number, title: string, detail?: string) {
  return new ApiError({ type: "about:blank", status, title, detail });
}

function deferred<T>() {
  let resolve!: (v: T) => void;
  const promise = new Promise<T>((r) => (resolve = r));
  return { promise, resolve };
}

function entSeq(...replies: unknown[]) {
  let i = 0;
  get.mockImplementation((url: string) => {
    if (url !== "/api/entitlements") return Promise.reject(new Error(`unmocked GET ${url}`));
    const r = replies[Math.min(i++, replies.length - 1)];
    return r instanceof Error ? Promise.reject(r) : Promise.resolve(r);
  });
}

function open(opts: { user?: User | null } = {}) {
  return renderPage(
    <ToastProvider>
      <Billing />
    </ToastProvider>,
    { path: "/billing", ...opts }
  );
}

/** Pastes text into the license box (typing JSON braces through userEvent is awkward). */
async function paste(text: string) {
  const box = await screen.findByLabelText("License key");
  fireEvent.change(box, { target: { value: text } });
  return box;
}

const toastWith = (role: "alert" | "status", text: string) =>
  waitFor(() =>
    expect(screen.getAllByRole(role).some((el) => el.textContent?.includes(text))).toBe(true)
  );

describe("Billing (test-cases/billing.md)", () => {
  beforeEach(() => {
    for (const f of [get, post, put, patch, del]) f.mockReset();
    routeGet(get, { "/api/entitlements": PRO });
  });

  it("BIL-01: paid plan shows tier, seats, license id and sorted features", async () => {
    open({ user: memberUser });
    const table = await screen.findByRole("table", { name: "Entitlements" });
    expect(
      screen.getByRole("heading", { level: 1, name: "Billing & license" })
    ).toBeInTheDocument();
    expect(screen.getByText("Current plan")).toBeInTheDocument();
    expect(screen.getByText("pro")).toBeInTheDocument();
    expect(screen.getByText("Seats").nextElementSibling).toHaveTextContent("5");
    expect(screen.getByText("lic_0123456789")).toHaveClass("mkt-mono");
    expect(screen.getByRole("heading", { level: 2, name: "Entitlements" })).toBeInTheDocument();
    const rows = within(table).getAllByRole("row").slice(1);
    expect(rows.map((r) => r.textContent)).toEqual([
      "analyticsEnabled",
      "cloud_relayDisabled",
      "transcode_hwEnabled",
    ]);
  });

  it("BIL-02: free tier with features {} renders without seats or license id", async () => {
    routeGet(get, { "/api/entitlements": FREE });
    open({ user: memberUser });
    expect(await screen.findByText("free")).toBeInTheDocument();
    expect(screen.queryByText("Seats")).toBeNull();
    expect(screen.queryByText("License ID")).toBeNull();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  // Desired: an empty-entitlements message instead of a header-only table.
  // Needs a new i18n key (e.g. billing.entitlements.empty) in src/lib/i18n.
  it.fails(
    "BIL-02: free tier with features {} shows an empty message, not a bare table",
    async () => {
      routeGet(get, { "/api/entitlements": FREE });
      open({ user: memberUser });
      await screen.findByText("free");
      const table = screen.queryByRole("table", { name: "Entitlements" });
      expect(table === null || within(table).getAllByRole("row").length > 1).toBe(true);
    }
  );

  it("BIL-03: non-admin sees no license editor", async () => {
    open({ user: memberUser });
    await screen.findByRole("table", { name: "Entitlements" });
    expect(screen.queryByRole("heading", { name: "License key" })).toBeNull();
    expect(screen.queryByRole("textbox")).toBeNull();
    expect(screen.queryByRole("button", { name: "Apply license" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Revoke license" })).toBeNull();
    expect(post).not.toHaveBeenCalled();
    expect(del).not.toHaveBeenCalled();
  });

  it("BIL-04: admin applies a valid license", async () => {
    entSeq(FREE, PRO);
    post.mockResolvedValue(undefined);
    open();
    expect(await screen.findByRole("heading", { name: "License key" })).toBeInTheDocument();
    const box = await paste(LICENSE);
    expect(box).toHaveAccessibleDescription(
      "Paste a signed license document (JSON) to upgrade this instance."
    );
    expect(box).toHaveAttribute("placeholder", '{ "license_id": "…", "tier": "pro", … }');
    await userEvent.setup().click(screen.getByRole("button", { name: "Apply license" }));

    await toastWith("status", "License applied.");
    expect(post).toHaveBeenCalledTimes(1);
    expect(post).toHaveBeenCalledWith("/api/admin/license", JSON.parse(LICENSE));
    expect(screen.getByLabelText("License key")).toHaveValue("");
    expect(await screen.findByText("pro")).toBeInTheDocument();
    expect(get).toHaveBeenCalledTimes(2);
  });

  it("BIL-05: pasted text that is not JSON sends nothing", async () => {
    open();
    await paste("not json {");
    await userEvent.setup().click(screen.getByRole("button", { name: "Apply license" }));
    await toastWith("alert", "The license document was rejected.");
    expect(post).not.toHaveBeenCalled();
    expect(screen.getByLabelText("License key")).toHaveValue("not json {");
  });

  it.each([
    ["with detail", problem(400, "Bad Request", "signature mismatch"), "signature mismatch"],
    ["without detail", problem(400, "Bad Request"), "The license document was rejected."],
  ])("BIL-06: server rejects the license (400 %s)", async (_, err, msg) => {
    post.mockRejectedValue(err);
    open();
    await paste(LICENSE);
    await userEvent.setup().click(screen.getByRole("button", { name: "Apply license" }));
    await toastWith("alert", msg);
    expect(screen.getByLabelText("License key")).toHaveValue(LICENSE);
    expect(get).toHaveBeenCalledTimes(1);
    expect(screen.getByRole("button", { name: "Apply license" })).toBeEnabled();
    expect(screen.getByRole("button", { name: "Revoke license" })).toBeEnabled();
  });

  it("BIL-07: 503 shows 'disabled in this build' for apply and revoke", async () => {
    const err = problem(503, "Service Unavailable", "no verifier key");
    post.mockRejectedValue(err);
    del.mockRejectedValue(err);
    open();
    await paste(LICENSE);
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Apply license" }));
    await toastWith("alert", "License management is disabled in this build.");
    await user.click(screen.getByRole("button", { name: "Revoke license" }));
    await waitFor(() => expect(del).toHaveBeenCalledTimes(1));
    expect(screen.queryByText("no verifier key")).toBeNull();
    expect(
      screen.getByRole("heading", { level: 1, name: "Billing & license" })
    ).toBeInTheDocument();
  });

  it.each([
    ["403", problem(403, "Forbidden", "admin only"), "admin only"],
    ["network failure", new TypeError("Failed to fetch"), "Something went wrong."],
  ])("BIL-08: apply / revoke %s shows a toast and keeps the plan", async (_, err, msg) => {
    post.mockRejectedValue(err);
    del.mockRejectedValue(err);
    open();
    await paste(LICENSE);
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Apply license" }));
    await toastWith("alert", msg);
    await user.click(screen.getByRole("button", { name: "Revoke license" }));
    await waitFor(() => expect(del).toHaveBeenCalledTimes(1));
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Revoke license" })).toBeEnabled()
    );
    expect(screen.getByText("pro")).toBeInTheDocument();
    expect(get).toHaveBeenCalledTimes(1);
  });

  it("BIL-09: admin revokes the license", async () => {
    entSeq(PRO, FREE);
    del.mockResolvedValue(undefined);
    open();
    await screen.findByText("pro");
    await userEvent.setup().click(screen.getByRole("button", { name: "Revoke license" }));
    await toastWith("status", "Reverted to free tier.");
    expect(del).toHaveBeenCalledTimes(1);
    expect(del).toHaveBeenCalledWith("/api/admin/license");
    expect(await screen.findByText("free")).toBeInTheDocument();
    expect(screen.queryByText("Seats")).toBeNull();
    expect(screen.queryByText("License ID")).toBeNull();
  });

  // Revoking downgrades the whole instance: it should ask first, and be
  // unavailable on the free tier. Needs confirm-dialog i18n keys
  // (billing.license.revoke.title / .confirm) in src/lib/i18n.
  it.fails("BIL-10: Revoke license asks for confirmation first", async () => {
    del.mockResolvedValue(undefined);
    open();
    await screen.findByText("pro");
    await userEvent.setup().click(screen.getByRole("button", { name: "Revoke license" }));
    expect(await screen.findByRole("dialog")).toBeInTheDocument();
    expect(del).not.toHaveBeenCalled();
  });

  it.fails("BIL-10: Revoke license is unavailable on the free tier", async () => {
    routeGet(get, { "/api/entitlements": FREE });
    open();
    await screen.findByText("free");
    const revoke = screen.queryByRole("button", { name: "Revoke license" });
    expect(revoke === null || revoke.hasAttribute("disabled")).toBe(true);
  });

  it("BIL-11: Apply is disabled for empty or whitespace input", async () => {
    open();
    const apply = await screen.findByRole("button", { name: "Apply license" });
    expect(apply).toBeDisabled();
    await paste("   \n\t  ");
    expect(apply).toBeDisabled();
    await userEvent.setup().click(apply);
    expect(post).not.toHaveBeenCalled();
  });

  it("BIL-12: one request per click; both buttons busy while pending", async () => {
    const pending = deferred<undefined>();
    post.mockReturnValue(pending.promise);
    open();
    await paste(LICENSE);
    const user = userEvent.setup();
    const apply = screen.getByRole("button", { name: "Apply license" });
    const revoke = screen.getByRole("button", { name: "Revoke license" });
    await user.click(apply);
    await user.click(apply);
    await user.click(revoke);
    expect(post).toHaveBeenCalledTimes(1);
    expect(del).not.toHaveBeenCalled();
    expect(apply).toHaveAttribute("aria-busy", "true");
    expect(revoke).toHaveAttribute("aria-busy", "true");
    expect(revoke).toBeDisabled();
    pending.resolve(undefined);
    await waitFor(() => expect(revoke).not.toHaveAttribute("aria-busy"));
  });

  it.each([
    ["features null", { tier: "free", features: null }],
    ["features missing", { tier: "free" }],
  ])("BIL-13: %s renders the plan and an empty entitlements section", async (_, body) => {
    routeGet(get, { "/api/entitlements": body });
    open();
    expect(await screen.findByText("free")).toBeInTheDocument();
    expect(screen.getByRole("heading", { level: 2, name: "Entitlements" })).toBeInTheDocument();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it.each([
    ["HTML body", "<!doctype html><html></html>"],
    ["null body", null],
  ])("BIL-13: %s shows the error state, not a crash or endless loading", async (_, body) => {
    routeGet(get, { "/api/entitlements": body });
    open();
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Something went wrong.");
    expect(alert).not.toHaveTextContent("This page hit an error");
    expect(screen.queryByText("Loading…")).toBeNull();
  });

  it.each([
    ["tier missing", { features: {} }, null],
    ["tier empty", { tier: "", features: {} }, null],
    ["tier enterprise", { tier: "enterprise", features: {} }, "enterprise"],
    ["seats 0", { tier: "pro", seats: 0, features: {} }, "pro"],
    ["seats null", { tier: "pro", seats: null, features: {} }, "pro"],
    ["license_id empty", { tier: "pro", license_id: "", features: {} }, "pro"],
  ])("BIL-14: %s does not crash", async (_, body, badge) => {
    routeGet(get, { "/api/entitlements": body });
    open();
    expect(await screen.findByText("Current plan")).toBeInTheDocument();
    if (badge) expect(screen.getByText(badge)).toBeInTheDocument();
    expect(screen.queryByText("Seats")).toBeNull();
    expect(screen.queryByText("License ID")).toBeNull();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it.each([
    ["500", problem(500, "Internal Server Error", "db down"), "db down"],
    ["404", problem(404, "Not Found"), "Not Found"],
    ["403", problem(403, "Forbidden", "auth required"), "auth required"],
    ["network failure", new TypeError("Failed to fetch"), "Something went wrong."],
    ["malformed JSON", new SyntaxError("Unexpected token"), "Something went wrong."],
  ])("BIL-16: load error (%s) and Retry", async (_, err, msg) => {
    entSeq(err, PRO);
    open();
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Something went wrong.");
    expect(alert).toHaveTextContent(msg);
    expect(
      screen.getByRole("heading", { level: 1, name: "Billing & license" })
    ).toBeInTheDocument();
    expect(screen.queryByLabelText("License key")).toBeNull();
    await userEvent.setup().click(within(alert).getByRole("button", { name: "Try again" }));
    expect(await screen.findByText("pro")).toBeInTheDocument();
  });

  it("BIL-17: 401 mid-session never leaves a blank page", async () => {
    entSeq(problem(401, "Unauthorized"));
    open();
    expect(await screen.findByRole("alert")).toHaveTextContent("Unauthorized");
  });

  // Desired: redirect to /login (shared 401 handling, src/lib/api.ts / auth.tsx).
  it.fails("BIL-17: 401 mid-session redirects to /login", async () => {
    entSeq(problem(401, "Unauthorized"));
    open();
    expect(await screen.findByText("login page (from /billing)")).toBeInTheDocument();
  });

  it("BIL-17: signed out deep link redirects to /login", async () => {
    open({ user: null });
    expect(await screen.findByText("login page (from /billing)")).toBeInTheDocument();
    expect(get).not.toHaveBeenCalled();
  });

  it("BIL-18: crash during render shows the route fallback; Back recovers", async () => {
    const quiet = quietCrash();
    routeGet(get, { "/api/entitlements": { tier: { x: 1 }, features: {} } });
    open();
    const user = userEvent.setup();
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "This page hit an error and could not be shown."
    );
    await user.click(screen.getByRole("link", { name: "go elsewhere" }));
    expect(await screen.findByText("elsewhere")).toBeInTheDocument();

    routeGet(get, { "/api/entitlements": PRO });
    await user.click(screen.getByRole("button", { name: "go back" }));
    expect(await screen.findByText("pro")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).toBeNull();
    quiet.mockRestore();
  });

  it("BIL-19: Back re-fetches entitlements and the license draft is not kept", async () => {
    open();
    await paste("draft text");
    const user = userEvent.setup();
    await user.click(screen.getByRole("link", { name: "go elsewhere" }));
    await user.click(await screen.findByRole("button", { name: "go back" }));
    expect(await screen.findByLabelText("License key")).toHaveValue("");
    expect(get).toHaveBeenCalledTimes(2);
  });

  it("BIL-21: Arabic / RTL", async () => {
    localStorage.setItem("mkt:locale", "ar");
    post.mockRejectedValue(problem(503, "Service Unavailable"));
    open();
    expect(
      await screen.findByRole("heading", { level: 1, name: ar["billing.title"] })
    ).toBeInTheDocument();
    expect(document.documentElement).toHaveAttribute("dir", "rtl");
    expect(screen.getByText(ar["billing.plan"])).toBeInTheDocument();
    expect(screen.getByText(ar["billing.seats"]).nextElementSibling).toHaveTextContent("٥");
    expect(screen.getByText(ar["billing.licenseId"])).toBeInTheDocument();
    expect(screen.getAllByText(ar["billing.feature.on"])).toHaveLength(2);
    expect(screen.getByText(ar["billing.feature.off"])).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText(ar["billing.license"]), { target: { value: LICENSE } });
    await userEvent
      .setup()
      .click(screen.getByRole("button", { name: ar["billing.license.apply"] }));
    await toastWith("alert", ar["billing.license.disabled"]);
  });

  it("BIL-24: textarea is labelled and described; toasts are live regions", async () => {
    post.mockResolvedValue(undefined);
    open();
    const box = await screen.findByLabelText("License key");
    expect(box.tagName).toBe("TEXTAREA");
    const describedBy = box.getAttribute("aria-describedby")!;
    expect(document.getElementById(describedBy)).toHaveTextContent(
      "Paste a signed license document (JSON) to upgrade this instance."
    );
    await paste(LICENSE);
    await userEvent.setup().click(screen.getByRole("button", { name: "Apply license" }));
    await toastWith("status", "License applied.");
  });
});
