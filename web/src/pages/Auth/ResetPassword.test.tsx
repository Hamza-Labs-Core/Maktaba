// Reset-password cases from web/test-cases/reset-password.md.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { act, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post } };
});

import { amal, deferred, htmlError, problem, publicGet, renderAuth } from "./authTestHarness";
import en from "../../i18n/en.json";
import ar from "../../i18n/ar.json";

const newPassword = () => screen.getByLabelText(en["reset.newPassword"]);
const confirmPassword = () => screen.getByLabelText(en["register.confirm"]);
const submitButton = () => screen.getByRole("button", { name: en["reset.submit"] });

async function fill(password = "newpass12", confirm = password) {
  const user = userEvent.setup();
  await screen.findByRole("heading", { name: en["reset.title"] });
  if (password) await user.type(newPassword(), password);
  if (confirm) await user.type(confirmPassword(), confirm);
  return user;
}

async function submit(password?: string) {
  const user = await fill(password);
  await user.click(submitButton());
  return user;
}

const resetCalls = () => post.mock.calls.filter(([path]) => path === "/api/auth/reset-password");

beforeEach(() => {
  get.mockReset();
  post.mockReset();
  get.mockImplementation(publicGet(null));
  post.mockResolvedValue(undefined); // 204
});

describe("ResetPassword (test-cases/reset-password.md)", () => {
  it("RPW-01: a valid token resets and lands on /login with the banner", async () => {
    renderAuth(["/reset-password?token=tok123"]);
    await submit();
    expect(resetCalls()).toEqual([
      ["/api/auth/reset-password", { token: "tok123", password: "newpass12" }],
    ]);
    expect(await screen.findByRole("status")).toHaveTextContent(en["reset.success"]);
    expect(screen.getByLabelText(en["login.username"])).toBeInTheDocument();
  });

  it("RPW-02: a missing token shows the no-token alert and the request link", async () => {
    renderAuth(["/reset-password"]);
    expect(await screen.findByRole("alert")).toHaveTextContent(en["reset.noToken"]);
    expect(screen.getByRole("link", { name: en["reset.request"] })).toHaveAttribute(
      "href",
      "/forgot-password"
    );
    expect(screen.queryByLabelText(en["reset.newPassword"])).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: en["reset.submit"] })).not.toBeInTheDocument();
    expect(post).not.toHaveBeenCalled();
  });

  it.each([["?token="], ["?other=1"], ["?token=%20%20"]])(
    "RPW-03: %s behaves like a missing token",
    async (search) => {
      renderAuth([`/reset-password${search}`]);
      expect(await screen.findByRole("alert")).toHaveTextContent(en["reset.noToken"]);
      expect(screen.queryByLabelText(en["reset.newPassword"])).not.toBeInTheDocument();
    }
  );

  it("RPW-04: an expired token (400 with detail) shows the server detail", async () => {
    post.mockRejectedValue(problem(400, { title: "Bad Request", detail: "reset token expired" }));
    renderAuth(["/reset-password?token=tok123"]);
    await submit();
    expect(await screen.findByRole("alert")).toHaveTextContent("reset token expired");
    expect(newPassword()).toHaveValue("newpass12");
    expect(confirmPassword()).toHaveValue("newpass12");
    expect(submitButton()).toBeEnabled();
    expect(screen.queryByLabelText(en["login.username"])).not.toBeInTheDocument();
  });

  it("RPW-05: a used token (400 without detail) shows the invalid-link text", async () => {
    post.mockRejectedValue(problem(400, { title: "Bad Request" }));
    renderAuth(["/reset-password?token=tok123"]);
    await submit();
    expect(await screen.findByRole("alert")).toHaveTextContent(en["reset.invalid"]);
  });

  it("RPW-06: after a token error the user can request a new link", async () => {
    post.mockRejectedValue(problem(400, { title: "Bad Request" }));
    renderAuth(["/reset-password?token=tok123"]);
    const user = await submit();
    await screen.findByRole("alert");
    const link = screen.getByRole("link", { name: en["reset.request"] });
    expect(link).toHaveAttribute("href", "/forgot-password");
    await user.click(link);
    expect(await screen.findByRole("heading", { name: en["forgot.title"] })).toBeInTheDocument();
  });

  it("RPW-07: a password shorter than 8 keeps submit disabled", async () => {
    renderAuth(["/reset-password?token=tok123"]);
    const user = await fill("1234567");
    expect(submitButton()).toBeDisabled();
    await user.type(confirmPassword(), "{Enter}");
    expect(post).not.toHaveBeenCalled();
    await user.type(newPassword(), "8");
    await user.type(confirmPassword(), "8");
    expect(submitButton()).toBeEnabled();
  });

  it("RPW-08: a mismatch shows an alert until fixed", async () => {
    renderAuth(["/reset-password?token=tok123"]);
    const user = await fill("newpass12", "newpass13");
    expect(screen.getByRole("alert")).toHaveTextContent(en["register.mismatch"]);
    expect(submitButton()).toBeDisabled();
    await user.type(confirmPassword(), "{Backspace}2");
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(submitButton()).toBeEnabled();
  });

  it("RPW-09: a 500 is not reported as a bad link", async () => {
    post.mockRejectedValue(problem(500, { title: "Internal" }));
    renderAuth(["/reset-password?token=tok123"]);
    await submit();
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent(en["common.error"]);
    expect(alert).not.toHaveTextContent(en["reset.invalid"]);
  });

  it("RPW-10: an HTML 502 body is not shown in the alert", async () => {
    post.mockRejectedValue(htmlError());
    renderAuth(["/reset-password?token=tok123"]);
    await submit();
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent(en["common.error"]);
    expect(alert).not.toHaveTextContent(/html|Bad Gateway/);
  });

  it("RPW-11: a network failure shows the generic error", async () => {
    post.mockRejectedValue(new TypeError("Failed to fetch"));
    renderAuth(["/reset-password?token=tok123"]);
    await submit();
    expect(await screen.findByRole("alert")).toHaveTextContent(en["common.error"]);
    expect(submitButton()).toBeEnabled();
    expect(newPassword()).toHaveValue("newpass12");
  });

  it("RPW-12: while submitting one request is sent and the old error clears", async () => {
    post.mockRejectedValueOnce(new TypeError("Failed to fetch"));
    renderAuth(["/reset-password?token=tok123"]);
    const user = await submit();
    await screen.findByRole("alert");
    const held = deferred<undefined>();
    post.mockReturnValueOnce(held.promise);
    await user.click(submitButton());
    const busy = screen.getByRole("button", { name: en["common.loading"] });
    expect(busy).toBeDisabled();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    await user.click(busy);
    expect(resetCalls()).toHaveLength(2);
    await act(async () => held.resolve(undefined));
    expect(await screen.findByText(en["reset.success"])).toBeInTheDocument();
  });

  it.each([
    ["?token=a%2Bb%2Fc%3D", "a+b/c="],
    // a raw "+" reads as a space (URLSearchParams): the server must mint
    // URL-safe tokens or the e-mail link must encode "+"
    ["?token=a+b", "a b"],
  ])("RPW-13: token %s is sent as %s", async (search, token) => {
    renderAuth([`/reset-password${search}`]);
    await submit();
    expect(resetCalls()[0][1]).toEqual({ token, password: "newpass12" });
  });

  it("RPW-14: Back after a successful reset does not reopen the form", async () => {
    renderAuth(["/forgot-password", "/reset-password?token=tok123"]);
    const user = await submit();
    await screen.findByText(en["reset.success"]);
    await user.click(screen.getByRole("button", { name: "go back" }));
    expect(await screen.findByRole("heading", { name: en["forgot.title"] })).toBeInTheDocument();
  });

  it("RPW-15: a reset while signed in clears the user and shows the banner", async () => {
    get.mockImplementation(publicGet(amal));
    post.mockImplementation((path: string) =>
      path === "/api/auth/logout" ? Promise.reject(problem(401)) : Promise.resolve(undefined)
    );
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    renderAuth(["/reset-password?token=tok123"]);
    await submit();
    expect(await screen.findByRole("status")).toHaveTextContent(en["reset.success"]);
    expect(screen.getByLabelText(en["login.username"])).toBeInTheDocument();
    expect(screen.queryByTestId("where")).not.toBeInTheDocument();
    warn.mockRestore();
  });

  it("RPW-17: Arabic: right-to-left with translated text", async () => {
    localStorage.setItem("mkt:locale", "ar");
    const first = renderAuth(["/reset-password"]);
    expect(await screen.findByRole("heading", { name: ar["reset.title"] })).toBeInTheDocument();
    expect(document.documentElement).toHaveAttribute("dir", "rtl");
    expect(screen.getByRole("alert")).toHaveTextContent(ar["reset.noToken"]);
    expect(screen.getByRole("link", { name: ar["reset.request"] })).toBeInTheDocument();
    first.unmount();

    post.mockRejectedValueOnce(problem(400));
    renderAuth(["/reset-password?token=tok123"]);
    const user = userEvent.setup();
    await user.type(await screen.findByLabelText(ar["reset.newPassword"]), "newpass12");
    await user.type(screen.getByLabelText(ar["register.confirm"]), "newpass12");
    await user.click(screen.getByRole("button", { name: ar["reset.submit"] }));
    expect(await screen.findByRole("alert")).toHaveTextContent(ar["reset.invalid"]);
    await user.click(screen.getByRole("button", { name: ar["reset.submit"] }));
    expect(await screen.findByRole("status")).toHaveTextContent(ar["reset.success"]);
  });

  it("RPW-20: keyboard and labels", async () => {
    renderAuth(["/reset-password?token=tok123"]);
    await screen.findByRole("heading", { name: en["reset.title"] });
    expect(newPassword()).toHaveFocus();
    expect(newPassword()).toHaveAttribute("autocomplete", "new-password");
    expect(confirmPassword()).toHaveAttribute("autocomplete", "new-password");
    expect(screen.getByRole("form", { name: en["reset.submit"] })).toBeInTheDocument();
    const user = userEvent.setup();
    await user.keyboard("newpass12");
    await user.tab();
    expect(confirmPassword()).toHaveFocus();
    await user.keyboard("newpass12{Enter}");
    expect(resetCalls()).toHaveLength(1);
  });
});
