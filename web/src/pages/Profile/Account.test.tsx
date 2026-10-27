// Account page cases from web/test-cases/account.md.
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
import en from "../../i18n/en.json";
import { Account } from "./Account";

const PROFILE = {
  id: "u1",
  username: "mona",
  email: "mona@example.com",
  display_name: "Mona",
  is_admin: false,
};

function problem(status: number, title: string, detail?: string) {
  return new ApiError({ type: "about:blank", status, title, detail });
}

function deferred<T>() {
  let resolve!: (v: T) => void;
  const promise = new Promise<T>((r) => (resolve = r));
  return { promise, resolve };
}

function open(opts: { user?: null } = {}) {
  return renderPage(
    <ToastProvider>
      <Account />
    </ToastProvider>,
    { path: "/account", ...opts }
  );
}

const meCalls = () => get.mock.calls.filter(([u]) => u === "/api/me").length;

async function fillPasswords(cur: string, next: string, confirm: string) {
  const user = userEvent.setup();
  await user.type(await screen.findByLabelText(/^Current password/), cur);
  await user.type(screen.getByLabelText(/^New password/), next);
  if (confirm) await user.type(screen.getByLabelText(/^Confirm password/), confirm);
  return user;
}

describe("Account (test-cases/account.md)", () => {
  beforeEach(() => {
    for (const f of [get, post, put, patch, del]) f.mockReset();
    routeGet(get, { "/api/me": PROFILE });
  });

  it("ACC-01: profile loads and fills both forms", async () => {
    open();
    expect(await screen.findByLabelText("Display name")).toHaveValue("Mona");
    expect(screen.getByRole("heading", { level: 1, name: "Account" })).toBeInTheDocument();
    expect(screen.getByText("Profile")).toBeInTheDocument();
    const username = screen.getByLabelText("Username");
    expect(username).toHaveValue("mona");
    expect(username).toBeDisabled();
    const email = screen.getByLabelText("Email");
    expect(email).toHaveValue("mona@example.com");
    expect(email).toHaveAttribute("type", "email");
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
    for (const label of ["Current password", "New password", "Confirm password"]) {
      const input = screen.getByLabelText(new RegExp(`^${label}`));
      expect(input).toHaveAttribute("type", "password");
      expect(input).toHaveValue("");
    }
    expect(screen.getByRole("button", { name: "Change password" })).toBeDisabled();
    expect(meCalls()).toBe(1);
  });

  it("ACC-02: save display name and email", async () => {
    patch.mockResolvedValue({ ...PROFILE, display_name: "Mona D", email: "m2@example.com" });
    open();
    const user = userEvent.setup();
    const name = await screen.findByLabelText("Display name");
    await user.clear(name);
    await user.type(name, "Mona D");
    const email = screen.getByLabelText("Email");
    await user.clear(email);
    await user.type(email, "m2@example.com");
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(await screen.findByRole("status")).toHaveTextContent("Profile updated.");
    expect(patch).toHaveBeenCalledTimes(1);
    expect(patch).toHaveBeenCalledWith("/api/me", {
      display_name: "Mona D",
      email: "m2@example.com",
    });
    expect(name).toHaveValue("Mona D");
    expect(email).toHaveValue("m2@example.com");
    await waitFor(() => expect(screen.getByRole("button", { name: "Save" })).toBeDisabled());
  });

  it("ACC-03: Save is disabled until something changes", async () => {
    open();
    const user = userEvent.setup();
    const name = await screen.findByLabelText("Display name");
    const save = screen.getByRole("button", { name: "Save" });
    expect(save).toBeDisabled();
    await user.type(name, "x");
    expect(save).toBeEnabled();
    await user.type(name, "{Backspace}");
    expect(save).toBeDisabled();
    await user.type(name, "{Enter}");
    expect(patch).not.toHaveBeenCalled();
  });

  it.each([
    ["409 with detail", problem(409, "Conflict", "email already in use"), "email already in use"],
    ["400 without detail", problem(400, "Bad Request"), en["error.kind.server"]],
    ["network failure", new TypeError("Failed to fetch"), en["error.kind.unreachable"]],
    ["500 text body", problem(500, "Internal Server Error", "boom"), en["error.kind.server"]],
  ])(
    "ACC-04: profile save error (%s) shows an error toast and keeps the edits",
    async (_, err, msg) => {
      patch.mockRejectedValue(err);
      open();
      const user = userEvent.setup();
      const email = await screen.findByLabelText("Email");
      await user.clear(email);
      await user.type(email, "taken@example.com");
      await user.click(screen.getByRole("button", { name: "Save" }));

      const alert = await screen.findByRole("alert");
      expect(alert).toHaveTextContent(msg);
      // never the server's 5xx text or its title
      expect(alert).not.toHaveTextContent("boom");
      expect(alert).not.toHaveTextContent("Bad Request");
      expect(email).toHaveValue("taken@example.com");
      expect(screen.getByRole("button", { name: "Save" })).toBeEnabled();
      expect(screen.queryByRole("status")).toBeNull();
    }
  );

  it("ACC-05: change password happy path", async () => {
    post.mockResolvedValue(undefined);
    open();
    const user = await fillPasswords("oldpass12", "newpass12", "newpass12");
    await user.click(screen.getByRole("button", { name: "Change password" }));

    expect(await screen.findByRole("status")).toHaveTextContent("Password changed.");
    expect(post).toHaveBeenCalledTimes(1);
    expect(post).toHaveBeenCalledWith("/api/me/change-password", {
      current_password: "oldpass12",
      new_password: "newpass12",
    });
    for (const label of ["Current password", "New password", "Confirm password"]) {
      expect(screen.getByLabelText(new RegExp(`^${label}`))).toHaveValue("");
    }
    expect(screen.getByRole("button", { name: "Change password" })).toBeDisabled();
    expect(screen.queryByText(/login page/)).toBeNull();
  });

  it("ACC-06: wrong current password says so (the home's 401, not an ended sign-in)", async () => {
    post.mockRejectedValue(problem(401, "invalid credentials", "current password is incorrect"));
    open();
    const user = await fillPasswords("wrongpass", "newpass12", "newpass12");
    await user.click(screen.getByRole("button", { name: "Change password" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(en["account.wrongCurrent"]);
    expect(screen.getByLabelText(/^Current password/)).toHaveValue("wrongpass");
    expect(screen.getByLabelText(/^New password/)).toHaveValue("newpass12");
    expect(screen.getByLabelText(/^Confirm password/)).toHaveValue("newpass12");
    expect(screen.getByRole("button", { name: "Change password" })).toBeEnabled();
    expect(screen.queryByText(/login page/)).toBeNull();
  });

  it("ACC-07: new password shorter than 8 keeps the button disabled", async () => {
    open();
    const user = await fillPasswords("oldpass12", "short7x", "short7x");
    const button = screen.getByRole("button", { name: "Change password" });
    expect(button).toBeDisabled();
    await user.click(button);
    expect(post).not.toHaveBeenCalled();

    for (const label of ["New password", "Confirm password"]) {
      await user.clear(screen.getByLabelText(new RegExp(`^${label}`)));
      await user.type(screen.getByLabelText(new RegExp(`^${label}`)), "exactly8");
    }
    expect(button).toBeEnabled();
  });

  it("ACC-08: confirm mismatch shows 'Passwords do not match.'", async () => {
    open();
    const user = await fillPasswords("oldpass12", "newpass12", "newpass13");
    const confirm = screen.getByLabelText(/^Confirm password/);
    const msg = screen.getByText("Passwords do not match.");
    expect(confirm).toHaveAttribute("aria-invalid", "true");
    expect(confirm.getAttribute("aria-describedby")).toContain(msg.id);
    expect(screen.getByRole("button", { name: "Change password" })).toBeDisabled();

    await user.clear(confirm);
    await user.type(confirm, "newpass12");
    expect(screen.queryByText("Passwords do not match.")).toBeNull();
    expect(screen.getByRole("button", { name: "Change password" })).toBeEnabled();
  });

  it("ACC-09: empty confirm field must not allow submit", async () => {
    open();
    const user = await fillPasswords("oldpass12", "newpass12", "");
    const button = screen.getByRole("button", { name: "Change password" });
    expect(button).toBeDisabled();
    // Bypass native `required` validation: submit the form directly.
    const form = button.closest("form")!;
    form.noValidate = true;
    await user.type(screen.getByLabelText(/^New password/), "{Enter}");
    form.requestSubmit();
    expect(post).not.toHaveBeenCalled();
  });

  it("ACC-11: /api/me 500 shows ErrorState and Retry works", async () => {
    get.mockRejectedValueOnce(problem(500, "Internal Server Error", "db down"));
    open();
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Something went wrong.");
    expect(alert).toHaveTextContent(en["error.kind.server"]);
    expect(alert).not.toHaveTextContent("db down");
    await userEvent.setup().click(within(alert).getByRole("button", { name: "Try again" }));
    expect(await screen.findByLabelText("Display name")).toHaveValue("Mona");
    expect(meCalls()).toBe(2);
  });

  it("ACC-11: /api/me 404 says it is gone, not the problem title", async () => {
    get.mockRejectedValueOnce(problem(404, "Not Found"));
    open();
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent(en["error.kind.notFound"]);
    expect(alert).not.toHaveTextContent("Not Found");
  });

  it.each([
    ["network failure", () => Promise.reject(new TypeError("Failed to fetch"))],
    ["malformed JSON", () => Promise.reject(new SyntaxError("Unexpected token"))],
    ["HTML body", () => Promise.resolve("<html><body>app</body></html>")],
  ])("ACC-12: /api/me %s shows ErrorState with Retry", async (_, reply) => {
    get.mockImplementationOnce(reply);
    open();
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Something went wrong.");
    expect(within(alert).getByRole("button", { name: "Try again" })).toBeInTheDocument();
    expect(screen.queryByLabelText("Display name")).toBeNull();
  });

  it("ACC-13: /api/me 401 mid-session never leaves a blank page", async () => {
    get.mockRejectedValue(problem(401, "Unauthorized"));
    open();
    expect(await screen.findByRole("alert")).toHaveTextContent(en["error.kind.signedOut"]);
    expect(screen.getByRole("heading", { name: "Account" })).toBeInTheDocument();
  });

  // Desired: a 401 sends the user to sign in. Needs a shared 401 handler
  // (src/lib/api.ts / src/lib/auth.tsx), not a page change.
  it.fails("ACC-13: /api/me 401 mid-session redirects to /login", async () => {
    get.mockRejectedValue(problem(401, "Unauthorized"));
    open();
    expect(await screen.findByText("login page (from /account)")).toBeInTheDocument();
  });

  it.each([
    ["display_name null", { display_name: null }],
    ["email null", { email: null }],
    ["both keys missing", { display_name: undefined, email: undefined }],
    ["both empty", { display_name: "", email: "" }],
  ])("ACC-14: %s renders empty inputs and saves typed values", async (_, over) => {
    const errors = vi.spyOn(console, "error").mockImplementation(() => {});
    routeGet(get, { "/api/me": { ...PROFILE, ...over } });
    patch.mockImplementation((_u: string, body: object) =>
      Promise.resolve({ ...PROFILE, ...body })
    );
    open();
    const user = userEvent.setup();
    const name = await screen.findByLabelText("Display name");
    const save = screen.getByRole("button", { name: "Save" });
    expect(save).toBeDisabled();
    if ("display_name" in over) expect(name).toHaveValue("");
    if ("email" in over) expect(screen.getByLabelText("Email")).toHaveValue("");

    await user.clear(name);
    await user.type(name, "Typed");
    expect(save).toBeEnabled();
    await user.click(save);
    await waitFor(() => expect(patch).toHaveBeenCalledTimes(1));
    expect(patch.mock.calls[0][1]).toMatchObject({ display_name: "Typed" });
    expect(screen.queryByRole("alert")).toBeNull();
    const uncontrolled = errors.mock.calls.some((c) => String(c[0]).includes("uncontrolled"));
    errors.mockRestore();
    expect(uncontrolled).toBe(false);
  });

  it("ACC-15: signed out deep link redirects to /login", async () => {
    open({ user: null });
    expect(await screen.findByText("login page (from /account)")).toBeInTheDocument();
    expect(get).not.toHaveBeenCalled();
  });

  it("ACC-16: username is read-only and not sent", async () => {
    patch.mockResolvedValue({ ...PROFILE, display_name: "New" });
    open();
    const user = userEvent.setup();
    const username = await screen.findByLabelText("Username");
    expect(username).toBeDisabled();
    expect(username).toHaveAttribute("readonly");
    await user.type(username, "hacker");
    expect(username).toHaveValue("mona");

    const name = screen.getByLabelText("Display name");
    await user.clear(name);
    await user.type(name, "New");
    await user.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(patch).toHaveBeenCalledTimes(1));
    expect(Object.keys(patch.mock.calls[0][1] as object).sort()).toEqual(["display_name", "email"]);
  });

  it("ACC-17: double submit sends one request per form", async () => {
    const saving = deferred<unknown>();
    const changing = deferred<unknown>();
    patch.mockReturnValue(saving.promise);
    post.mockReturnValue(changing.promise);
    open();
    const user = userEvent.setup();
    const name = await screen.findByLabelText("Display name");
    await user.type(name, "!");
    const save = screen.getByRole("button", { name: "Save" });
    await user.click(save);
    await user.click(save);
    expect(save).toHaveAttribute("aria-busy", "true");
    expect(save).toBeDisabled();
    expect(patch).toHaveBeenCalledTimes(1);

    await fillPasswords("oldpass12", "newpass12", "newpass12");
    const change = screen.getByRole("button", { name: "Change password" });
    await user.click(change);
    await user.click(change);
    expect(change).toHaveAttribute("aria-busy", "true");
    expect(post).toHaveBeenCalledTimes(1);

    saving.resolve({ ...PROFILE, display_name: "Mona!" });
    changing.resolve(undefined);
    await waitFor(() => expect(change).not.toHaveAttribute("aria-busy"));
    expect(save).not.toHaveAttribute("aria-busy");
  });

  it("ACC-19: crash during render shows the route fallback; Back recovers", async () => {
    const quiet = quietCrash();
    const broken = {
      ...PROFILE,
      get display_name(): string {
        throw new Error("profile getter exploded");
      },
    };
    routeGet(get, { "/api/me": broken });
    open();
    const user = userEvent.setup();
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("This page hit an error and could not be shown.");
    await user.click(screen.getByRole("link", { name: "go elsewhere" }));
    expect(await screen.findByText("elsewhere")).toBeInTheDocument();

    routeGet(get, { "/api/me": PROFILE });
    await user.click(screen.getByRole("button", { name: "go back" }));
    expect(await screen.findByLabelText("Display name")).toHaveValue("Mona");
    expect(screen.queryByRole("alert")).toBeNull();
    quiet.mockRestore();
  });

  it("ACC-20: Back and Forward re-mount the page with a fresh GET", async () => {
    open();
    const user = userEvent.setup();
    await screen.findByLabelText("Display name");
    await user.click(screen.getByRole("link", { name: "go elsewhere" }));
    expect(await screen.findByText("elsewhere")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "go back" }));
    expect(await screen.findByLabelText("Display name")).toHaveValue("Mona");
    expect(meCalls()).toBe(2);
  });

  it("ACC-21: Arabic / RTL", async () => {
    localStorage.setItem("mkt:locale", "ar");
    open();
    expect(
      await screen.findByRole("heading", { level: 1, name: ar["account.title"] })
    ).toBeInTheDocument();
    expect(document.documentElement).toHaveAttribute("dir", "rtl");
    expect(document.documentElement).toHaveAttribute("lang", "ar");
    for (const key of [
      "account.username",
      "account.displayName",
      "register.email",
      "account.currentPassword",
      "account.newPassword",
      "register.confirm",
    ] as const) {
      expect(screen.getByLabelText(new RegExp(`^${ar[key]}`))).toBeInTheDocument();
    }
    const user = userEvent.setup();
    await user.type(
      screen.getByLabelText(new RegExp(`^${ar["account.newPassword"]}`)),
      "newpass12"
    );
    await user.type(screen.getByLabelText(new RegExp(`^${ar["register.confirm"]}`)), "newpass13");
    expect(screen.getByText(ar["register.mismatch"])).toBeInTheDocument();
  });

  it("ACC-24: labels, autocomplete and Enter submit", async () => {
    post.mockResolvedValue(undefined);
    open();
    expect(await screen.findByLabelText(/^Current password/)).toHaveAttribute(
      "autocomplete",
      "current-password"
    );
    expect(screen.getByLabelText(/^New password/)).toHaveAttribute("autocomplete", "new-password");
    expect(screen.getByLabelText(/^Confirm password/)).toHaveAttribute(
      "autocomplete",
      "new-password"
    );
    const user = await fillPasswords("oldpass12", "newpass12", "newpass12");
    await user.type(screen.getByLabelText(/^Confirm password/), "{Enter}");
    await waitFor(() => expect(post).toHaveBeenCalledTimes(1));
    expect(await screen.findByRole("status")).toHaveTextContent("Password changed.");
  });
});
