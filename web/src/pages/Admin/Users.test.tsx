import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { get, post, put, patch, del, badgeCrash } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  put: vi.fn(),
  patch: vi.fn(),
  del: vi.fn(),
  badgeCrash: { on: false },
}));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post, put, patch, delete: del } };
});
// USR-19: lets a test make the page throw while rendering.
vi.mock("@ds/components/Badge/Badge", async () => {
  const actual = await vi.importActual<typeof import("@ds/components/Badge/Badge")>(
    "@ds/components/Badge/Badge"
  );
  return {
    ...actual,
    Badge: (props: Parameters<typeof actual.Badge>[0]) => {
      if (badgeCrash.on) throw new Error("badge boom");
      return actual.Badge(props);
    },
  };
});

import { ToastProvider } from "@ds/components/Toast/Toast";
import { ApiError } from "../../lib/api";
import { AdminUsers } from "./Users";
import { renderPage, memberUser, quietCrash, adminUser } from "../../test/pageHarness";
import type { User } from "../../lib/auth";

const KEY = "mkt:admin:knownUsers";
const alice = { id: "u1", username: "alice", is_admin: true };
const bob = { id: "u2", username: "bob", is_admin: false };

function setRoster(v: unknown) {
  localStorage.setItem(KEY, typeof v === "string" ? v : JSON.stringify(v));
}
function roster() {
  return JSON.parse(localStorage.getItem(KEY) ?? "[]") as unknown[];
}
function open(user: User | null = adminUser) {
  return renderPage(
    <ToastProvider>
      <AdminUsers />
    </ToastProvider>,
    { path: "/admin/users", user }
  );
}
function problem(status: number, title: string, detail?: string) {
  return new ApiError({ type: "about:blank", title, status, detail });
}
const lastAdmin = () => problem(409, "conflict", "operation would leave the system with no admin");

function row(name: string) {
  // hidden: true — the page sits behind an open modal in some steps
  return screen.getByRole("cell", { name, hidden: true }).closest("tr")!;
}

describe("AdminUsers (test-cases/admin-users.md)", () => {
  beforeEach(() => {
    for (const f of [get, post, put, patch, del]) f.mockReset();
    // the page's only read: a user's cloud link (42.7); by default a home without linked accounts
    get.mockRejectedValue(problem(404, "not linked to Maktaba Cloud"));
    badgeCrash.on = false;
  });
  afterEach(() => vi.restoreAllMocks());

  it("USR-01: empty roster shows the empty state and no table", () => {
    open();
    expect(screen.getByRole("heading", { level: 1, name: "User management" })).toBeInTheDocument();
    expect(screen.getByText(/The server has no user-listing endpoint/)).toBeInTheDocument();
    expect(screen.getByText("No tracked users")).toBeInTheDocument();
    expect(screen.getByText("Create a user to start managing accounts here.")).toBeInTheDocument();
    expect(screen.queryByRole("table")).toBeNull();
    expect(screen.getByRole("button", { name: "Add user" })).toBeInTheDocument();
    expect(get).not.toHaveBeenCalled();
  });

  it("USR-02: roster loads from localStorage", () => {
    setRoster([alice, bob]);
    open();
    const table = screen.getByRole("table", { name: "User management" });
    const headers = within(table)
      .getAllByRole("columnheader")
      .map((h) => h.textContent);
    expect(headers).toEqual(["Username", "Role", "ID", "Actions"]);
    expect(within(row("alice")).getByText("Administrator")).toBeInTheDocument();
    expect(within(row("bob")).getByText("User")).toBeInTheDocument();
    expect(within(row("bob")).getByText("u2")).toBeInTheDocument();
    for (const name of ["Edit", "Unlock", "Revoke access", "Delete"]) {
      expect(within(row("bob")).getByRole("button", { name })).toBeInTheDocument();
    }
  });

  it("USR-03: create user adds a row and persists it", async () => {
    let resolve!: (v: unknown) => void;
    post.mockReturnValue(new Promise((r) => (resolve = r)));
    const view = open();
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Add user" }));
    const dialog = screen.getByRole("dialog", { name: "Create user" });
    await user.type(within(dialog).getByLabelText(/Username/), "carol");
    await user.type(within(dialog).getByLabelText(/^Password/), "s3cret-pass");
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    expect(post).toHaveBeenCalledTimes(1);
    expect(post).toHaveBeenCalledWith("/api/users", {
      username: "carol",
      password: "s3cret-pass",
      is_admin: false,
    });
    expect(within(dialog).getByRole("button", { name: "Save" })).toHaveAttribute("aria-busy");
    resolve({ id: "u9", username: "carol", is_admin: false });
    expect(await screen.findByText("User created.")).toBeInTheDocument();
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(within(row("carol")).getByText("u9")).toBeInTheDocument();
    expect(roster()).toEqual([{ id: "u9", username: "carol", is_admin: false }]);
    // "reload": a fresh mount still shows the row
    view.unmount();
    open();
    expect(screen.getByRole("cell", { name: "carol" })).toBeInTheDocument();
  });

  it("USR-03: pressing Enter in the form submits exactly once", async () => {
    post.mockResolvedValue({ id: "u9", username: "carol", is_admin: false });
    open();
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Add user" }));
    const dialog = screen.getByRole("dialog", { name: "Create user" });
    await user.type(within(dialog).getByLabelText(/Username/), "carol");
    await user.type(within(dialog).getByLabelText(/^Password/), "s3cret-pass{Enter}");
    await screen.findByText("User created.");
    expect(post).toHaveBeenCalledTimes(1);
  });

  it("USR-04: Save is disabled until username and password are filled; edit makes password optional", async () => {
    setRoster([bob]);
    open();
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Add user" }));
    const dialog = screen.getByRole("dialog", { name: "Create user" });
    const save = within(dialog).getByRole("button", { name: "Save" });
    const name = within(dialog).getByLabelText(/Username/);
    const pass = within(dialog).getByLabelText(/^Password/);
    expect(name).toBeRequired();
    expect(pass).toBeRequired();
    expect(save).toBeDisabled();
    await user.type(name, "dave");
    expect(save).toBeDisabled();
    await user.clear(name);
    await user.type(name, "   ");
    await user.type(pass, "pw");
    expect(save).toBeDisabled();
    await user.type(name, "{Enter}");
    expect(post).not.toHaveBeenCalled();
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));

    await user.click(within(row("bob")).getByRole("button", { name: "Edit" }));
    const edit = screen.getByRole("dialog", { name: "Edit user" });
    const newPass = within(edit).getByLabelText(/New password/);
    expect(newPass).not.toBeRequired();
    expect(within(edit).getByText("optional")).toBeInTheDocument();
  });

  it("USR-05: create fails with 409 username exists", async () => {
    setRoster([alice]);
    post.mockRejectedValue(problem(409, "conflict", "username already exists"));
    open();
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Add user" }));
    const dialog = screen.getByRole("dialog", { name: "Create user" });
    await user.type(within(dialog).getByLabelText(/Username/), "alice");
    await user.type(within(dialog).getByLabelText(/^Password/), "pw");
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    expect(await screen.findByText("username already exists")).toBeInTheDocument();
    expect(within(dialog).getByLabelText(/Username/)).toHaveValue("alice");
    expect(within(dialog).getByRole("button", { name: "Save" })).toBeEnabled();
    expect(roster()).toEqual([alice]);
  });

  it.each([
    ["network error", new TypeError("Failed to fetch"), "Something went wrong."],
    ["text/plain 500", problem(500, "Internal Server Error", "boom"), "boom"],
    ["problem without detail", problem(500, "internal"), "internal"],
  ])("USR-06: create and edit fail with %s", async (_n, err, text) => {
    setRoster([bob]);
    post.mockRejectedValue(err);
    patch.mockRejectedValue(err);
    open();
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Add user" }));
    const dialog = screen.getByRole("dialog", { name: "Create user" });
    await user.type(within(dialog).getByLabelText(/Username/), "x");
    await user.type(within(dialog).getByLabelText(/^Password/), "pw");
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    expect(await screen.findByText(text)).toBeInTheDocument();
    expect(within(dialog).getByRole("button", { name: "Save" })).toBeEnabled();
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    expect(roster()).toEqual([bob]);

    await user.click(within(row("bob")).getByRole("button", { name: "Edit" }));
    const edit = screen.getByRole("dialog", { name: "Edit user" });
    await user.type(within(edit).getByLabelText(/Username/), "2");
    await user.click(within(edit).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(screen.getAllByText(text).length).toBeGreaterThanOrEqual(1));
    expect(within(edit).getByRole("button", { name: "Save" })).toBeEnabled();
    expect(roster()).toEqual([bob]);
  });

  it("USR-07: edit sends only changed fields, and a no-op edit sends nothing", async () => {
    setRoster([bob]);
    patch.mockImplementation((_p: string, body: Record<string, unknown>) =>
      Promise.resolve({ ...bob, username: "robert", ...body })
    );
    open();
    const user = userEvent.setup();
    await user.click(within(row("bob")).getByRole("button", { name: "Edit" }));
    let dialog = screen.getByRole("dialog", { name: "Edit user" });
    expect(within(dialog).getByLabelText(/Username/)).toHaveValue("bob");
    expect(within(dialog).getByLabelText(/New password/)).toHaveValue("");
    expect(within(dialog).getByRole("checkbox", { name: "Administrator" })).not.toBeChecked();
    await user.clear(within(dialog).getByLabelText(/Username/));
    await user.type(within(dialog).getByLabelText(/Username/), "robert");
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    expect(patch).toHaveBeenLastCalledWith("/api/users/u2", { username: "robert" });
    expect(await screen.findByText("User updated.")).toBeInTheDocument();
    expect(screen.getAllByRole("row")).toHaveLength(2); // header + one row, not duplicated
    expect(roster()).toEqual([{ id: "u2", username: "robert", is_admin: false }]);

    await user.click(within(row("robert")).getByRole("button", { name: "Edit" }));
    dialog = screen.getByRole("dialog", { name: "Edit user" });
    await user.type(within(dialog).getByLabelText(/New password/), "n3w");
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    expect(patch).toHaveBeenLastCalledWith("/api/users/u2", { password: "n3w" });
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());

    await user.click(within(row("robert")).getByRole("button", { name: "Edit" }));
    dialog = screen.getByRole("dialog", { name: "Edit user" });
    expect(within(dialog).getByRole("button", { name: "Save" })).toBeDisabled();
    expect(patch).toHaveBeenCalledTimes(2);
  });

  it("USR-08: promote and demote flip the badge; create as admin sends is_admin true", async () => {
    setRoster([alice, bob]);
    patch.mockImplementation((p: string, body: { is_admin: boolean }) =>
      Promise.resolve({ ...(p.endsWith("u1") ? alice : bob), ...body })
    );
    post.mockResolvedValue({ id: "u3", username: "cat", is_admin: true });
    open();
    const user = userEvent.setup();
    await user.click(within(row("bob")).getByRole("button", { name: "Edit" }));
    let dialog = screen.getByRole("dialog", { name: "Edit user" });
    await user.click(within(dialog).getByRole("checkbox", { name: "Administrator" }));
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    expect(patch).toHaveBeenLastCalledWith("/api/users/u2", { is_admin: true });
    await waitFor(() => expect(within(row("bob")).getByText("Administrator")).toBeInTheDocument());

    await user.click(within(row("alice")).getByRole("button", { name: "Edit" }));
    dialog = screen.getByRole("dialog", { name: "Edit user" });
    await user.click(within(dialog).getByRole("checkbox", { name: "Administrator" }));
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    expect(patch).toHaveBeenLastCalledWith("/api/users/u1", { is_admin: false });
    await waitFor(() => expect(within(row("alice")).getByText("User")).toBeInTheDocument());

    await user.click(screen.getByRole("button", { name: "Add user" }));
    dialog = screen.getByRole("dialog", { name: "Create user" });
    await user.type(within(dialog).getByLabelText(/Username/), "cat");
    await user.type(within(dialog).getByLabelText(/^Password/), "pw");
    await user.click(within(dialog).getByRole("checkbox", { name: "Administrator" }));
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    expect(post).toHaveBeenCalledWith("/api/users", {
      username: "cat",
      password: "pw",
      is_admin: true,
    });
  });

  it("USR-09: last-admin protection on demote keeps the dialog and the roster", async () => {
    setRoster([alice]);
    patch.mockRejectedValue(lastAdmin());
    open();
    const user = userEvent.setup();
    await user.click(within(row("alice")).getByRole("button", { name: "Edit" }));
    const dialog = screen.getByRole("dialog", { name: "Edit user" });
    await user.click(within(dialog).getByRole("checkbox", { name: "Administrator" }));
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    expect(
      await screen.findByText("operation would leave the system with no admin")
    ).toBeInTheDocument();
    expect(screen.getByRole("dialog", { name: "Edit user" })).toBeInTheDocument();
    expect(within(row("alice")).getByText("Administrator")).toBeInTheDocument();
    expect(roster()).toEqual([alice]);
  });

  it("USR-10: last-admin protection on delete keeps the row", async () => {
    setRoster([alice]);
    del.mockRejectedValue(lastAdmin());
    open();
    const user = userEvent.setup();
    await user.click(within(row("alice")).getByRole("button", { name: "Delete" }));
    const dialog = screen.getByRole("dialog", { name: "Delete user" });
    expect(dialog).toHaveTextContent("Delete user “alice”? This cannot be undone.");
    await user.click(within(dialog).getByRole("button", { name: "Delete" }));
    expect(
      await screen.findByText("operation would leave the system with no admin")
    ).toBeInTheDocument();
    expect(within(dialog).getByRole("button", { name: "Delete" })).toBeEnabled();
    expect(screen.getByRole("cell", { name: "alice", hidden: true })).toBeInTheDocument();
    expect(roster()).toEqual([alice]);
  });

  // Needs a way to refresh the signed-in user (src/lib/auth.tsx has none)
  // plus a warning string in i18n; see the report.
  it.fails("USR-11: demoting yourself drops you to the Admins only state", async () => {
    const me = { id: "u-admin", username: "admin", is_admin: true };
    setRoster([me, alice]);
    patch.mockResolvedValue({ ...me, is_admin: false });
    open();
    const user = userEvent.setup();
    await user.click(within(row("admin")).getByRole("button", { name: "Edit" }));
    const dialog = screen.getByRole("dialog", { name: "Edit user" });
    await user.click(within(dialog).getByRole("checkbox", { name: "Administrator" }));
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    expect(await screen.findByText("Admins only", {}, { timeout: 300 })).toBeInTheDocument();
  });

  it.fails("USR-12: deleting yourself signs you out", async () => {
    const me = { id: "u-admin", username: "admin", is_admin: true };
    setRoster([me, alice]);
    del.mockResolvedValue(undefined);
    open();
    const user = userEvent.setup();
    await user.click(within(row("admin")).getByRole("button", { name: "Delete" }));
    const dialog = screen.getByRole("dialog", { name: "Delete user" });
    await user.click(within(dialog).getByRole("button", { name: "Delete" }));
    expect(
      await screen.findByText("login page (from /admin/users)", {}, { timeout: 300 })
    ).toBeInTheDocument();
  });

  it("USR-13: delete — cancel, backdrop, confirm, and 404", async () => {
    setRoster([bob]);
    open();
    const user = userEvent.setup();
    // 1. Cancel
    await user.click(within(row("bob")).getByRole("button", { name: "Delete" }));
    await user.click(
      within(screen.getByRole("dialog", { name: "Delete user" })).getByRole("button", {
        name: "Cancel",
      })
    );
    expect(screen.queryByRole("dialog")).toBeNull();
    // 2. Backdrop does not close
    await user.click(within(row("bob")).getByRole("button", { name: "Delete" }));
    const dialog = screen.getByRole("dialog", { name: "Delete user" });
    await user.pointer({ keys: "[MouseLeft]", target: dialog.parentElement! });
    expect(screen.getByRole("dialog", { name: "Delete user" })).toBeInTheDocument();
    expect(del).not.toHaveBeenCalled();
    // 4. 404 keeps the row
    del.mockRejectedValueOnce(problem(404, "not found", "user not found"));
    await user.click(within(dialog).getByRole("button", { name: "Delete" }));
    expect(await screen.findByText("user not found")).toBeInTheDocument();
    expect(screen.getByRole("cell", { name: "bob", hidden: true })).toBeInTheDocument();
    // 3. Confirm
    del.mockResolvedValueOnce(undefined);
    await user.click(within(dialog).getByRole("button", { name: "Delete" }));
    expect(await screen.findByText("User deleted.")).toBeInTheDocument();
    expect(del).toHaveBeenLastCalledWith("/api/users/u2");
    expect(del).toHaveBeenCalledTimes(2);
    expect(screen.queryByRole("table")).toBeNull();
    expect(screen.getByText("No tracked users")).toBeInTheDocument();
    expect(roster()).toEqual([]);
  });

  it("USR-14: unlock — success, 404, and the id is URL-encoded", async () => {
    setRoster([bob, { id: "a/b", username: "slash", is_admin: false }]);
    post.mockResolvedValueOnce(undefined);
    open();
    const user = userEvent.setup();
    await user.click(within(row("bob")).getByRole("button", { name: "Unlock" }));
    expect(post).toHaveBeenLastCalledWith("/api/users/u2/unlock");
    expect(await screen.findByText("Account unlocked.")).toBeInTheDocument();
    post.mockRejectedValueOnce(problem(404, "not found", "user not found"));
    await user.click(within(row("slash")).getByRole("button", { name: "Unlock" }));
    expect(post).toHaveBeenLastCalledWith("/api/users/a%2Fb/unlock");
    expect(await screen.findByText("user not found")).toBeInTheDocument();
  });

  it("USR-15: revoke a session and a token family", async () => {
    setRoster([bob]);
    let resolve!: (v: unknown) => void;
    del.mockReturnValueOnce(new Promise((r) => (resolve = r)));
    open();
    const user = userEvent.setup();
    await user.click(within(row("bob")).getByRole("button", { name: "Revoke access" }));
    const dialog = screen.getByRole("dialog", { name: "Revoke sessions & tokens" });
    expect(dialog).toHaveTextContent(/no per-user session list endpoint/);
    const revokeSession = within(dialog).getByRole("button", { name: "Revoke session" });
    const revokeFamily = within(dialog).getByRole("button", { name: "Revoke token family" });
    expect(revokeSession).toBeDisabled();
    expect(revokeFamily).toBeDisabled();
    await user.type(within(dialog).getByLabelText("Session ID"), "   ");
    expect(revokeSession).toBeDisabled();
    await user.clear(within(dialog).getByLabelText("Session ID"));
    await user.type(within(dialog).getByLabelText("Session ID"), "s-1");
    await user.click(revokeSession);
    expect(del).toHaveBeenLastCalledWith("/api/users/u2/sessions/s-1");
    expect(revokeSession).toHaveAttribute("aria-busy");
    expect(revokeFamily).toHaveAttribute("aria-busy");
    resolve(undefined);
    expect(await screen.findByText("Revoked.")).toBeInTheDocument();

    del.mockRejectedValueOnce(problem(404, "not found", "no such family"));
    await user.type(within(dialog).getByLabelText("Refresh-token family ID"), "f/1");
    await user.click(revokeFamily);
    expect(del).toHaveBeenLastCalledWith("/api/users/u2/refresh-tokens/f%2F1");
    expect(await screen.findByText("no such family")).toBeInTheDocument();
    expect(screen.getByRole("dialog", { name: "Revoke sessions & tokens" })).toBeInTheDocument();
  });

  it("USR-42.7: shows the user's Maktaba Cloud link and unlinks it", async () => {
    setRoster([bob]);
    get.mockImplementation((path: string) =>
      path === "/api/users/u2/cloud-link"
        ? Promise.resolve({
            link: {
              user_id: "u2",
              cloud_user_id: "c-9",
              cloud_label: "bob@cloud.example",
              scope: "play",
            },
          })
        : Promise.reject(new Error(`unexpected GET ${path}`))
    );
    del.mockResolvedValueOnce(undefined);
    open();
    const user = userEvent.setup();
    await user.click(within(row("bob")).getByRole("button", { name: "Revoke access" }));
    const dialog = screen.getByRole("dialog", { name: "Revoke sessions & tokens" });
    expect(await within(dialog).findByText("bob@cloud.example")).toBeInTheDocument();
    await user.click(within(dialog).getByRole("button", { name: "Unlink" }));
    expect(del).toHaveBeenLastCalledWith("/api/users/u2/cloud-link");
    expect(
      await within(dialog).findByText("Not linked to a Maktaba Cloud account.")
    ).toBeInTheDocument();
  });

  it("USR-42.7: no link section on a home without linked accounts", async () => {
    setRoster([bob]);
    get.mockRejectedValue(problem(404, "not linked to Maktaba Cloud"));
    open();
    const user = userEvent.setup();
    await user.click(within(row("bob")).getByRole("button", { name: "Revoke access" }));
    const dialog = screen.getByRole("dialog", { name: "Revoke sessions & tokens" });
    await waitFor(() => expect(get).toHaveBeenCalledWith("/api/users/u2/cloud-link"));
    expect(within(dialog).queryByText("Linked accounts")).not.toBeInTheDocument();
  });

  // Wanted: a 401 sends you to /login; today only a toast "unauthorized"
  // is shown. The fix belongs in src/lib/api.ts / src/lib/auth.tsx.
  it.fails("USR-16: a 401 during a mutation redirects to /login", async () => {
    setRoster([bob]);
    post.mockRejectedValue(problem(401, "unauthorized"));
    open();
    const user = userEvent.setup();
    await user.click(within(row("bob")).getByRole("button", { name: "Unlock" }));
    await screen.findByText("unauthorized");
    expect(screen.getByText("login page (from /admin/users)")).toBeInTheDocument();
  });

  it.each([
    ["null", "null"],
    ["{}", "{}"],
    ["a string", '"text"'],
    ["not json", "not json"],
    ["[null]", "[null]"],
    ["a number", "42"],
  ])("USR-17: a corrupt roster (%s) shows the empty state", (_n, raw) => {
    quietCrash();
    setRoster(raw);
    open();
    expect(screen.queryByRole("alert")).toBeNull();
    expect(screen.getByText("No tracked users")).toBeInTheDocument();
  });

  it("USR-17: roster entries without id are dropped; missing is_admin shows User", () => {
    setRoster([{ username: "x" }, null, { id: "u5", username: "eve" }]);
    open();
    expect(screen.queryByRole("alert")).toBeNull();
    expect(screen.queryByRole("cell", { name: "x" })).toBeNull();
    expect(within(row("eve")).getByText("User")).toBeInTheDocument();
  });

  it.each([
    ["204 empty", undefined],
    ["an HTML body", "<html>"],
  ])("USR-18: create response %s shows a clear error and no bogus row", async (_n, body) => {
    post.mockResolvedValue(body);
    open();
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Add user" }));
    const dialog = screen.getByRole("dialog", { name: "Create user" });
    await user.type(within(dialog).getByLabelText(/Username/), "carol");
    await user.type(within(dialog).getByLabelText(/^Password/), "pw");
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    expect(await screen.findByText("Something went wrong.")).toBeInTheDocument();
    expect(screen.queryByText("User created.")).toBeNull();
    expect(screen.queryByRole("table")).toBeNull();
  });

  it("USR-19: a page crash shows the route error fallback and Back works", async () => {
    quietCrash();
    setRoster([bob]);
    badgeCrash.on = true;
    open();
    expect(screen.getByRole("alert")).toBeInTheDocument();
    const user = userEvent.setup();
    await user.click(screen.getByRole("link", { name: "go elsewhere" }));
    expect(screen.getByText("elsewhere")).toBeInTheDocument();
    badgeCrash.on = false;
    await user.click(screen.getByRole("button", { name: "go back" }));
    expect(await screen.findByRole("table", { name: "User management" })).toBeInTheDocument();
  });

  it("USR-20: non-admin sees Admins only and no table", () => {
    setRoster([bob]);
    open(memberUser);
    expect(screen.getByText("Admins only")).toBeInTheDocument();
    expect(
      screen.getByText("You need administrator access to view this page.")
    ).toBeInTheDocument();
    expect(screen.queryByRole("table")).toBeNull();
    expect(screen.queryByRole("button", { name: "Add user" })).toBeNull();
    for (const f of [get, post, put, patch, del]) expect(f).not.toHaveBeenCalled();
  });

  it("USR-21: signed-out user is redirected to /login", () => {
    open(null);
    expect(screen.getByText("login page (from /admin/users)")).toBeInTheDocument();
  });

  it("USR-22: Back with the create dialog open leaves the page and unlocks scroll", async () => {
    renderPage(
      <ToastProvider>
        <AdminUsers />
      </ToastProvider>,
      { path: "/admin/users", history: ["/elsewhere"] }
    );
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Add user" }));
    expect(screen.getByRole("dialog", { name: "Create user" })).toBeInTheDocument();
    expect(document.body.style.overflow).toBe("hidden");
    await user.click(screen.getByRole("button", { name: "go back", hidden: true }));
    expect(screen.getByText("elsewhere")).toBeInTheDocument();
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(document.body.style.overflow).toBe("");
  });

  it("USR-27: dialogs are modal, labelled, close on Escape and return focus; toasts announced", async () => {
    setRoster([bob]);
    post.mockResolvedValueOnce(undefined).mockRejectedValueOnce(problem(404, "not found", "gone"));
    open();
    const user = userEvent.setup();
    const add = screen.getByRole("button", { name: "Add user" });
    await user.click(add);
    const dialog = screen.getByRole("dialog", { name: "Create user" });
    expect(dialog).toHaveAttribute("aria-modal", "true");
    expect(dialog.contains(document.activeElement)).toBe(true);
    expect(within(dialog).getByLabelText(/Username/)).toBeInTheDocument();
    expect(within(dialog).getByLabelText(/^Password/)).toBeInTheDocument();
    expect(within(dialog).getByRole("checkbox", { name: "Administrator" })).toBeInTheDocument();
    expect(within(dialog).getByRole("button", { name: "Close" })).toBeInTheDocument();
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("dialog")).toBeNull();
    await waitFor(() => expect(add).toHaveFocus());

    await user.click(within(row("bob")).getByRole("button", { name: "Delete" }));
    expect(screen.getByRole("dialog", { name: "Delete user" })).toBeInTheDocument();
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("dialog")).toBeNull();

    await user.click(within(row("bob")).getByRole("button", { name: "Unlock" }));
    expect(await screen.findByRole("status")).toHaveTextContent("Account unlocked.");
    await user.click(within(row("bob")).getByRole("button", { name: "Unlock" }));
    const err = await screen.findByText("gone");
    expect(err.closest("[role=alert]")).toHaveAttribute("aria-live", "assertive");
  });
});
