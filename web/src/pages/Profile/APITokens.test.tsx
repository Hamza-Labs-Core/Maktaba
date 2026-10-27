// API tokens page cases from web/test-cases/account-tokens.md.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
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
import en from "../../i18n/en.json";
import { APITokens } from "./APITokens";

const T1 = {
  id: "t1",
  name: "CLI laptop",
  prefix: "ab12cd",
  scopes: [],
  last_used_at: null,
  expires_at: null,
  created_at: "2026-10-01T09:00:00Z",
  revoked_at: null,
};
const T2 = {
  id: "t2",
  name: "CI",
  prefix: "ef34gh",
  scopes: ["read", "write"],
  last_used_at: "2026-10-18T07:00:00Z",
  expires_at: "2027-01-01T00:00:00Z",
  created_at: "2026-09-01T09:00:00Z",
  revoked_at: null,
};
const T3 = {
  id: "t3",
  name: "Old",
  prefix: "zz99yy",
  scopes: ["read"],
  last_used_at: null,
  expires_at: null,
  created_at: "2026-01-01T09:00:00Z",
  revoked_at: "2026-02-01T09:00:00Z",
};
const SECRET = "mkt_pat_ab12cd_SECRETVALUE";
const T4 = {
  id: "t4",
  name: "Scripts",
  prefix: "ab12cd",
  scopes: ["read"],
  last_used_at: null,
  expires_at: "2026-11-18T09:00:00Z",
  created_at: "2026-10-19T09:00:00Z",
  revoked_at: null,
};

function problem(status: number, title: string, detail?: string) {
  return new ApiError({ type: "about:blank", status, title, detail });
}

/** A copy of `obj` without `keys` (a field missing from the JSON). */
function omit(obj: Record<string, unknown>, ...keys: string[]) {
  return Object.fromEntries(Object.entries(obj).filter(([k]) => !keys.includes(k)));
}

function deferred<T>() {
  let resolve!: (v: T) => void;
  const promise = new Promise<T>((r) => (resolve = r));
  return { promise, resolve };
}

function tokensSeq(...replies: unknown[]) {
  let i = 0;
  get.mockImplementation((url: string) => {
    if (url !== "/api/me/tokens") return Promise.reject(new Error(`unmocked GET ${url}`));
    const r = replies[Math.min(i++, replies.length - 1)];
    return r instanceof Error ? Promise.reject(r) : Promise.resolve(r);
  });
}

function open(opts: { user?: null } = {}) {
  return renderPage(
    <ToastProvider>
      <APITokens />
    </ToastProvider>,
    { path: "/account/tokens", ...opts }
  );
}

const row = (text: string) => screen.getByText(text).closest("tr") as HTMLElement;

/** Clipboard stub; call after userEvent.setup(), which installs its own. */
function stubClipboard(writeText: () => Promise<void>) {
  const fn = vi.fn(writeText);
  Object.defineProperty(navigator, "clipboard", {
    configurable: true,
    value: { writeText: fn },
  });
  return fn;
}

async function openCreate() {
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: "Create token" }));
  const dialog = await screen.findByRole("dialog", { name: "Create API token" });
  return { user, dialog };
}

async function createScripts() {
  const { user, dialog } = await openCreate();
  await user.type(within(dialog).getByLabelText(/^Name/), "Scripts");
  await user.click(within(dialog).getByLabelText("Read"));
  await user.selectOptions(within(dialog).getByLabelText("Expiry"), "30");
  await user.click(within(dialog).getByRole("button", { name: "Create token" }));
  return { user, dialog };
}

describe("APITokens (test-cases/account-tokens.md)", () => {
  beforeEach(() => {
    for (const f of [get, post, put, patch, del]) f.mockReset();
    routeGet(get, { "/api/me/tokens": { items: [T1, T2, T3] } });
  });
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("ATK-01: token table renders (active and revoked)", async () => {
    open();
    const table = await screen.findByRole("table", { name: "API tokens" });
    expect(screen.getByRole("heading", { level: 1, name: "API tokens" })).toBeInTheDocument();
    expect(
      screen.getByText("Personal access tokens authenticate scripts and the CLI on your behalf.")
    ).toBeInTheDocument();
    expect(
      within(table)
        .getAllByRole("columnheader")
        .map((h) => h.textContent)
    ).toEqual(["Name", "Prefix", "Scopes", "Last used", "Expires", "Actions"]);
    const r1 = row("CLI laptop");
    expect(within(r1).getByText("ab12cd…")).toBeInTheDocument();
    expect(within(r1).getByText("Full access")).toBeInTheDocument();
    expect(within(r1).getAllByText("Never")).toHaveLength(2);
    expect(within(r1).getByRole("button", { name: "Revoke" })).toBeInTheDocument();
    const r2 = row("CI");
    expect(within(r2).getByText("read, write")).toBeInTheDocument();
    expect(within(r2).getByRole("button", { name: "Revoke" })).toBeInTheDocument();
    const r3 = row("Old");
    expect(r3).toHaveClass("mkt-row--muted");
    expect(within(r3).getByText("Revoked")).toBeInTheDocument();
    expect(within(r3).queryByRole("button")).toBeNull();
  });

  it("ATK-02: create token sends the body and shows the secret once", async () => {
    tokensSeq({ items: [T1, T2, T3] }, { items: [T4, T1, T2, T3] });
    post.mockResolvedValue({ token: SECRET, pat: T4 });
    open();
    await createScripts();

    const reveal = await screen.findByRole("dialog", { name: "Copy your new token" });
    expect(post).toHaveBeenCalledTimes(1);
    expect(post).toHaveBeenCalledWith("/api/me/tokens", {
      name: "Scripts",
      scopes: ["read"],
      expires_in_days: 30,
    });
    expect(within(reveal).getByRole("alert")).toHaveTextContent(
      "This is the only time the token is shown. Copy it now and store it safely."
    );
    expect(within(reveal).getByText(SECRET).tagName).toBe("CODE");
    expect(within(reveal).getByRole("button", { name: "Copy" })).toBeInTheDocument();
    expect(within(reveal).getAllByRole("button", { name: "Close" }).length).toBeGreaterThan(0);
    expect(screen.queryByRole("dialog", { name: "Create API token" })).toBeNull();
    expect(screen.getByRole("status", { hidden: true })).toHaveTextContent("Token created.");
    await waitFor(() => expect(get).toHaveBeenCalledTimes(2));
    expect(screen.getByText("Scripts", { ignore: "[role=dialog] *" })).toBeInTheDocument();
  });

  it("ATK-03: after Close the secret is gone from the page and storage", async () => {
    tokensSeq({ items: [T1] }, { items: [T4, T1] });
    post.mockResolvedValue({ token: SECRET, pat: T4 });
    open();
    const { user } = await createScripts();
    const reveal = await screen.findByRole("dialog", { name: "Copy your new token" });
    const close = within(reveal).getAllByRole("button", { name: "Close" });
    await user.click(close[close.length - 1]);

    expect(screen.queryByRole("dialog")).toBeNull();
    expect(document.body).not.toHaveTextContent(SECRET);
    expect(JSON.stringify({ ...localStorage })).not.toContain(SECRET);
    expect(JSON.stringify({ ...sessionStorage })).not.toContain(SECRET);
    await user.click(screen.getByRole("link", { name: "go elsewhere" }));
    await user.click(await screen.findByRole("button", { name: "go back" }));
    await screen.findByRole("table");
    expect(document.body).not.toHaveTextContent(SECRET);
  });

  it("ATK-04: Copy on a secure origin", async () => {
    post.mockResolvedValue({ token: SECRET, pat: T4 });
    open();
    const { user } = await createScripts();
    const reveal = await screen.findByRole("dialog", { name: "Copy your new token" });
    const writeText = stubClipboard(() => Promise.resolve());
    await user.click(within(reveal).getByRole("button", { name: "Copy" }));

    expect(writeText).toHaveBeenCalledTimes(1);
    expect(writeText).toHaveBeenCalledWith(SECRET);
    expect(within(reveal).getByRole("button", { name: "Copied!" })).toBeInTheDocument();
    await waitFor(() =>
      expect(
        screen.getAllByRole("status", { hidden: true }).some((s) => s.textContent === "Copied!")
      ).toBe(true)
    );
    expect(screen.getByRole("dialog", { name: "Copy your new token" })).toBeInTheDocument();
  });

  it("ATK-05: Copy on plain http (no navigator.clipboard) falls back to execCommand", async () => {
    post.mockResolvedValue({ token: SECRET, pat: T4 });
    open();
    const { user } = await createScripts();
    const reveal = await screen.findByRole("dialog", { name: "Copy your new token" });
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: undefined });
    vi.stubGlobal("isSecureContext", false);
    const execCommand = vi.fn(() => true);
    Object.defineProperty(document, "execCommand", { configurable: true, value: execCommand });
    await user.click(within(reveal).getByRole("button", { name: "Copy" }));

    expect(execCommand).toHaveBeenCalledWith("copy");
    expect(within(reveal).getByRole("button", { name: "Copied!" })).toBeInTheDocument();
  });

  it("ATK-06: Copy rejected by the browser shows the copy-failed toast", async () => {
    post.mockResolvedValue({ token: SECRET, pat: T4 });
    open();
    const { user } = await createScripts();
    const reveal = await screen.findByRole("dialog", { name: "Copy your new token" });
    stubClipboard(() => Promise.reject(new DOMException("denied", "NotAllowedError")));
    Object.defineProperty(document, "execCommand", { configurable: true, value: () => false });
    await user.click(within(reveal).getByRole("button", { name: "Copy" }));

    await waitFor(() =>
      expect(
        screen
          .getAllByRole("alert", { hidden: true })
          .some((a) => a.textContent?.includes("Couldn't copy to clipboard."))
      ).toBe(true)
    );
    expect(within(reveal).getByRole("button", { name: "Copy" })).toBeInTheDocument();
    expect(within(reveal).getByText(SECRET)).toBeInTheDocument();
  });

  it("ATK-07: backdrop does not close the reveal dialog; Escape and X do", async () => {
    post.mockResolvedValue({ token: SECRET, pat: T4 });
    open();
    const { user } = await createScripts();
    let reveal = await screen.findByRole("dialog", { name: "Copy your new token" });
    await user.pointer({ keys: "[MouseLeft]", target: reveal.parentElement! });
    expect(screen.getByRole("dialog", { name: "Copy your new token" })).toBeInTheDocument();
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(document.body).not.toHaveTextContent(SECRET);

    await createScripts();
    reveal = await screen.findByRole("dialog", { name: "Copy your new token" });
    await user.click(within(reveal).getAllByRole("button", { name: "Close" })[0]);
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("ATK-08: name validation (empty, spaces) and untrimmed submit", async () => {
    tokensSeq({ items: [] }, { items: [{ ...T1, name: "CLI" }] });
    post.mockResolvedValue({ token: SECRET, pat: T1 });
    open();
    const { user, dialog } = await openCreate();
    const submit = within(dialog).getByRole("button", { name: "Create token" });
    const name = within(dialog).getByLabelText(/^Name/);
    expect(submit).toBeDisabled();
    await user.type(name, "{Enter}");
    await user.type(name, "   ");
    expect(submit).toBeDisabled();
    await user.type(name, "{Enter}");
    expect(post).not.toHaveBeenCalled();

    await user.clear(name);
    await user.type(name, "  CLI  ");
    expect(submit).toBeEnabled();
    await user.click(submit);
    await waitFor(() => expect(post).toHaveBeenCalledTimes(1));
    expect(post.mock.calls[0][1]).toMatchObject({ name: "  CLI  " });
    expect(await screen.findByText("CLI", { ignore: "[role=dialog] *" })).toBeInTheDocument();
  });

  it("ATK-09: scopes and expiry choices are sent correctly", async () => {
    post.mockResolvedValue({ token: SECRET, pat: T4 });
    open();
    let { user, dialog } = await openCreate();
    expect(
      within(dialog).getByText("Leave all unchecked to inherit your full permissions.")
    ).toBeInTheDocument();
    const expiry = within(dialog).getByLabelText("Expiry") as HTMLSelectElement;
    expect(Array.from(expiry.options).map((o) => o.textContent)).toEqual([
      "Never",
      "30 days",
      "90 days",
      "365 days",
    ]);
    expect(expiry).toHaveValue("0");
    await user.type(within(dialog).getByLabelText(/^Name/), "one");
    await user.click(within(dialog).getByRole("button", { name: "Create token" }));
    await waitFor(() => expect(post).toHaveBeenCalledTimes(1));
    expect(post.mock.calls[0][1]).toEqual({ name: "one", scopes: [], expires_in_days: 0 });

    const reveal = await screen.findByRole("dialog", { name: "Copy your new token" });
    await user.click(within(reveal).getAllByRole("button", { name: "Close" })[0]);
    ({ user, dialog } = await openCreate());
    await user.type(within(dialog).getByLabelText(/^Name/), "two");
    for (const s of ["Read", "Write", "Admin", "Write"]) {
      await user.click(within(dialog).getByLabelText(s));
    }
    await user.selectOptions(within(dialog).getByLabelText("Expiry"), "365");
    await user.click(within(dialog).getByRole("button", { name: "Create token" }));
    await waitFor(() => expect(post).toHaveBeenCalledTimes(2));
    expect(post.mock.calls[1][1]).toEqual({
      name: "two",
      scopes: ["read", "admin"],
      expires_in_days: 365,
    });
  });

  it.each([
    ["400 with detail", problem(400, "Bad Request", "name required"), "name required"],
    [
      "403 with detail",
      problem(403, "Forbidden", "admin scope not allowed"),
      en["error.kind.noAccess"],
    ],
    ["500 without detail", problem(500, "Internal Server Error"), en["error.kind.server"]],
    ["network failure", new TypeError("Failed to fetch"), en["error.kind.unreachable"]],
  ])("ATK-10: create fails (%s): toast, dialog keeps its inputs", async (_, err, msg) => {
    post.mockRejectedValue(err);
    open();
    await createScripts();
    await waitFor(() =>
      expect(
        screen.getAllByRole("alert", { hidden: true }).some((a) => a.textContent?.includes(msg))
      ).toBe(true)
    );
    const dialog = screen.getByRole("dialog", { name: "Create API token" });
    expect(within(dialog).getByLabelText(/^Name/)).toHaveValue("Scripts");
    expect(within(dialog).getByLabelText("Read")).toBeChecked();
    expect(within(dialog).getByLabelText("Expiry")).toHaveValue("30");
    expect(within(dialog).getByRole("button", { name: "Create token" })).not.toHaveAttribute(
      "aria-busy"
    );
    expect(screen.queryByRole("dialog", { name: "Copy your new token" })).toBeNull();
    expect(get).toHaveBeenCalledTimes(1);
  });

  it.each([
    ["no token key", { pat: T4 }],
    ["empty token", { token: "" }],
    ["204 empty body", undefined],
  ])("ATK-11: create response without a token (%s) is reported", async (_, reply) => {
    post.mockResolvedValue(reply);
    open();
    await createScripts();
    expect(await screen.findByRole("alert")).toHaveTextContent("Something went wrong.");
    expect(screen.queryByRole("status")).toBeNull();
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("ATK-12: double click and double Enter each send one POST", async () => {
    const pending = deferred<unknown>();
    post.mockReturnValue(pending.promise);
    open();
    const { user, dialog } = await openCreate();
    const name = within(dialog).getByLabelText(/^Name/);
    await user.type(name, "Scripts");
    const submit = within(dialog).getByRole("button", { name: "Create token" });
    await user.click(submit);
    await user.click(submit);
    expect(submit).toHaveAttribute("aria-busy", "true");
    expect(submit).toBeDisabled();
    await user.type(name, "{Enter}{Enter}");
    expect(post).toHaveBeenCalledTimes(1);
    pending.resolve({ token: SECRET, pat: T4 });
    expect(await screen.findByRole("dialog", { name: "Copy your new token" })).toBeInTheDocument();
  });

  it("ATK-14: revoke through the confirm dialog", async () => {
    tokensSeq(
      { items: [T1, T2, T3] },
      { items: [T1, { ...T2, revoked_at: "2026-10-19T09:00:00Z" }, T3] }
    );
    del.mockResolvedValue(undefined);
    open();
    await screen.findByRole("table");
    const user = userEvent.setup();
    await user.click(within(row("CI")).getByRole("button", { name: "Revoke" }));
    let dialog = await screen.findByRole("dialog", { name: "Revoke token" });
    expect(dialog).toHaveTextContent("Revoke “CI”? Any client using it will stop working.");
    await user.pointer({ keys: "[MouseLeft]", target: dialog.parentElement! });
    expect(screen.getByRole("dialog", { name: "Revoke token" })).toBeInTheDocument();
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    expect(del).not.toHaveBeenCalled();

    await user.click(within(row("CI")).getByRole("button", { name: "Revoke" }));
    dialog = await screen.findByRole("dialog", { name: "Revoke token" });
    await user.click(within(dialog).getByRole("button", { name: "Revoke" }));
    expect(await screen.findByRole("status")).toHaveTextContent("Token revoked.");
    expect(del).toHaveBeenCalledTimes(1);
    expect(del).toHaveBeenCalledWith("/api/me/tokens/t2");
    expect(screen.queryByRole("dialog")).toBeNull();
    await waitFor(() => expect(row("CI")).toHaveClass("mkt-row--muted"));
    expect(within(row("CI")).getByText("Revoked")).toBeInTheDocument();
    expect(within(row("CI")).queryByRole("button")).toBeNull();
  });

  it.each([
    ["404 with detail", problem(404, "Not Found", "token not found"), en["error.kind.notFound"]],
    ["network failure", new TypeError("Failed to fetch"), en["error.kind.unreachable"]],
  ])("ATK-15: revoke fails (%s): toast, dialog stays", async (_, err, msg) => {
    del.mockRejectedValue(err);
    open();
    await screen.findByRole("table");
    const user = userEvent.setup();
    await user.click(within(row("CI")).getByRole("button", { name: "Revoke" }));
    const dialog = await screen.findByRole("dialog", { name: "Revoke token" });
    await user.click(within(dialog).getByRole("button", { name: "Revoke" }));
    expect(await screen.findByRole("alert", { hidden: true })).toHaveTextContent(msg);
    expect(screen.getByRole("dialog", { name: "Revoke token" })).toBeInTheDocument();
    expect(row("CI")).not.toHaveClass("mkt-row--muted");
  });

  it("ATK-15: double click on the dialog's Revoke sends one DELETE", async () => {
    const pending = deferred<undefined>();
    del.mockReturnValue(pending.promise);
    open();
    await screen.findByRole("table");
    const user = userEvent.setup();
    await user.click(within(row("CI")).getByRole("button", { name: "Revoke" }));
    const dialog = await screen.findByRole("dialog", { name: "Revoke token" });
    const confirm = within(dialog).getByRole("button", { name: "Revoke" });
    await user.click(confirm);
    await user.click(confirm);
    expect(confirm).toHaveAttribute("aria-busy", "true");
    expect(del).toHaveBeenCalledTimes(1);
    pending.resolve(undefined);
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  });

  it("ATK-16: items [] shows the empty state; Create still works", async () => {
    routeGet(get, { "/api/me/tokens": { items: [] } });
    open();
    expect(await screen.findByText("No API tokens")).toBeInTheDocument();
    expect(
      screen.getByText("Create a token to access the API from scripts or the CLI.")
    ).toBeInTheDocument();
    await openCreate();
  });

  it.each([
    ["items null", { items: null }],
    ["items missing", {}],
    ["HTML body", "<!doctype html><html></html>"],
  ])("ATK-17: %s shows the empty state", async (_, body) => {
    routeGet(get, { "/api/me/tokens": body });
    open();
    expect(await screen.findByText("No API tokens")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("ATK-17: a null body shows the error state with Retry", async () => {
    routeGet(get, { "/api/me/tokens": null });
    open();
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Something went wrong.");
    expect(within(alert).getByRole("button", { name: "Try again" })).toBeInTheDocument();
  });

  it("ATK-18: scopes null / missing shows Full access", async () => {
    const noScopes = omit(T1, "scopes");
    routeGet(get, {
      "/api/me/tokens": {
        items: [
          { ...T1, id: "a", name: "null-scopes", scopes: null },
          { ...noScopes, id: "b", name: "no-scopes" },
        ],
      },
    });
    open();
    await screen.findByRole("table");
    expect(within(row("null-scopes")).getByText("Full access")).toBeInTheDocument();
    expect(within(row("no-scopes")).getByText("Full access")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("ATK-19: null dates, empty name, odd prefix, missing revoked_at", async () => {
    const noDates = omit(T1, "last_used_at", "expires_at");
    const noRevoked = omit(T1, "revoked_at");
    routeGet(get, {
      "/api/me/tokens": {
        items: [
          { ...T1, id: "a", name: "" },
          { ...T1, id: "b", name: "empty-prefix", prefix: "" },
          { ...noDates, id: "c", name: "no-dates" },
          { ...noRevoked, id: "d", name: "no-revoked" },
        ],
      },
    });
    open();
    await screen.findByRole("table");
    expect(screen.getAllByRole("row")).toHaveLength(5);
    expect(within(row("empty-prefix")).getByText("…")).toBeInTheDocument();
    expect(within(row("no-dates")).getAllByText("Never")).toHaveLength(2);
    const active = row("no-revoked");
    expect(active).not.toHaveClass("mkt-row--muted");
    expect(within(active).getByRole("button", { name: "Revoke" })).toBeInTheDocument();

    const user = userEvent.setup();
    const emptyRow = screen.getAllByRole("row")[1];
    await user.click(within(emptyRow).getByRole("button", { name: "Revoke" }));
    expect(await screen.findByRole("dialog", { name: "Revoke token" })).toHaveTextContent(
      "Revoke “”?"
    );
  });

  it.each([
    ["500 with detail", problem(500, "Internal Server Error", "db down"), en["error.kind.server"]],
    ["404", problem(404, "Not Found"), en["error.kind.notFound"]],
    ["network failure", new TypeError("Failed to fetch"), en["error.kind.unreachable"]],
    ["malformed JSON", new SyntaxError("Unexpected token"), en["error.kind.server"]],
  ])("ATK-21: list load error (%s) and Retry", async (_, err, msg) => {
    tokensSeq(err, { items: [T1] });
    open();
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent(msg);
    expect(alert).not.toHaveTextContent(/db down|Not Found|Unexpected token/);
    await userEvent.setup().click(within(alert).getByRole("button", { name: "Try again" }));
    expect(await screen.findByRole("table")).toBeInTheDocument();
  });

  it("ATK-22: 401 mid-session never leaves a blank page", async () => {
    tokensSeq(problem(401, "Unauthorized"));
    open();
    expect(await screen.findByRole("alert")).toHaveTextContent(en["error.kind.signedOut"]);
  });

  // Desired: redirect to /login (shared 401 handling, src/lib/api.ts / auth.tsx).
  it.fails("ATK-22: 401 mid-session redirects to /login", async () => {
    tokensSeq(problem(401, "Unauthorized"));
    open();
    expect(await screen.findByText("login page (from /account/tokens)")).toBeInTheDocument();
  });

  it("ATK-22: signed out deep link redirects to /login", async () => {
    open({ user: null });
    expect(await screen.findByText("login page (from /account/tokens)")).toBeInTheDocument();
    expect(get).not.toHaveBeenCalled();
  });

  it("ATK-23: a token name with markup is shown as text", async () => {
    const evil = "<img src=x onerror=alert(1)>";
    routeGet(get, { "/api/me/tokens": { items: [{ ...T1, name: evil }] } });
    open();
    await screen.findByRole("table");
    expect(screen.getByText(evil)).toBeInTheDocument();
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Revoke" }));
    expect(await screen.findByRole("dialog")).toHaveTextContent(evil);
    expect(document.querySelector("img")).toBeNull();
  });

  it("ATK-24: crash during render shows the route fallback; Back recovers", async () => {
    const quiet = quietCrash();
    routeGet(get, { "/api/me/tokens": { items: [{ ...T1, name: { x: 1 } }] } });
    open();
    const user = userEvent.setup();
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "This page hit an error and could not be shown."
    );
    await user.click(screen.getByRole("link", { name: "go elsewhere" }));
    expect(await screen.findByText("elsewhere")).toBeInTheDocument();

    routeGet(get, { "/api/me/tokens": { items: [T1] } });
    await user.click(screen.getByRole("button", { name: "go back" }));
    expect(await screen.findByRole("table")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).toBeNull();
    quiet.mockRestore();
  });

  it("ATK-25: leaving with a dialog open removes it and leaves no inert background", async () => {
    post.mockResolvedValue({ token: SECRET, pat: T4 });
    open();
    const { user } = await openCreate();
    await user.click(screen.getByRole("link", { name: "go elsewhere", hidden: true }));
    expect(await screen.findByText("elsewhere")).toBeInTheDocument();
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(document.body.style.overflow).toBe("");
    expect(document.querySelector("[inert]")).toBeNull();
    await user.click(screen.getByRole("button", { name: "go back" }));
    await screen.findByRole("table");

    await createScripts();
    await screen.findByRole("dialog", { name: "Copy your new token" });
    await user.click(screen.getByRole("link", { name: "go elsewhere", hidden: true }));
    expect(await screen.findByText("elsewhere")).toBeInTheDocument();
    expect(document.querySelector("[inert]")).toBeNull();
    await user.click(screen.getByRole("button", { name: "go back" }));
    await screen.findByRole("table");
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(document.body).not.toHaveTextContent(SECRET);
  });

  it("ATK-26: Arabic / RTL", async () => {
    localStorage.setItem("mkt:locale", "ar");
    open();
    expect(
      await screen.findByRole("heading", { level: 1, name: ar["tokens.title"] })
    ).toBeInTheDocument();
    expect(document.documentElement).toHaveAttribute("dir", "rtl");
    const table = screen.getByRole("table");
    expect(within(table).getByText(ar["tokens.col.prefix"])).toBeInTheDocument();
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: ar["tokens.create"] }));
    const dialog = await screen.findByRole("dialog", { name: ar["tokens.create.title"] });
    expect(within(dialog).getByLabelText(ar["tokens.scope.read"])).toBeInTheDocument();
  });

  // ar.json: tokens.revoke and common.cancel are both "إلغاء", so the two
  // revoke-dialog buttons read the same. Fix belongs in src/i18n/ar.json.
  it.fails("ATK-26: Arabic revoke dialog buttons have different labels", async () => {
    localStorage.setItem("mkt:locale", "ar");
    open();
    await screen.findByRole("table");
    const user = userEvent.setup();
    await user.click(within(row("CI")).getByRole("button", { name: ar["tokens.revoke"] }));
    const dialog = await screen.findByRole("dialog");
    const labels = within(dialog)
      .getAllByRole("button")
      .filter((b) => b.getAttribute("aria-label") !== "Close")
      .map((b) => b.textContent);
    expect(new Set(labels).size).toBe(labels.length);
  });

  it("ATK-29: dialogs trap focus; scopes are a fieldset; warning is an alert", async () => {
    post.mockResolvedValue({ token: SECRET, pat: T4 });
    open();
    const { user, dialog } = await openCreate();
    expect(dialog.contains(document.activeElement)).toBe(true);
    const group = within(dialog).getByRole("group", { name: "Scopes" });
    expect(group.tagName).toBe("FIELDSET");
    expect(within(group).getAllByRole("checkbox")).toHaveLength(3);
    await user.type(within(dialog).getByLabelText(/^Name/), "kb");
    await user.click(within(dialog).getByRole("button", { name: "Create token" }));
    const reveal = await screen.findByRole("dialog", { name: "Copy your new token" });
    expect(within(reveal).getByRole("alert")).toBeInTheDocument();
    const close = within(reveal).getAllByRole("button", { name: "Close" });
    await user.click(close[close.length - 1]);
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  });
});
