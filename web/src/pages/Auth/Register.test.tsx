// Register cases from web/test-cases/register.md (real AuthProvider, mocked lib/api).
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
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

const salma = { id: "u9", username: "salma", is_admin: false };

const field = (key: keyof typeof en) => screen.getByLabelText(en[key]);
const submitButton = () => screen.getByRole("button", { name: en["register.submit"] });

async function fill(
  over: Partial<Record<"username" | "email" | "password" | "confirm", string>> = {}
) {
  const v = {
    username: "salma",
    email: "salma@example.com",
    password: "longpass1",
    confirm: "longpass1",
    ...over,
  };
  const user = userEvent.setup();
  await screen.findByRole("heading", { name: en["register.title"] });
  if (v.username) await user.type(field("register.username"), v.username);
  if (v.email) await user.type(field("register.email"), v.email);
  if (v.password) await user.type(field("register.password"), v.password);
  if (v.confirm) await user.type(field("register.confirm"), v.confirm);
  return user;
}

async function submit(over?: Parameters<typeof fill>[0]) {
  const user = await fill(over);
  await user.click(submitButton());
  return user;
}

let errorSpy: ReturnType<typeof vi.spyOn>;
let warnSpy: ReturnType<typeof vi.spyOn>;

beforeEach(() => {
  get.mockReset();
  post.mockReset();
  get.mockImplementation(publicGet(null));
  post.mockResolvedValue({ user: salma });
  errorSpy = vi.spyOn(console, "error");
  warnSpy = vi.spyOn(console, "warn");
});

afterEach(() => {
  errorSpy.mockRestore();
  warnSpy.mockRestore();
});

describe("Register (test-cases/register.md)", () => {
  it("REG-01: creating an account signs in and goes to /library", async () => {
    renderAuth(["/register"]);
    await submit();
    expect(post).toHaveBeenCalledTimes(1);
    expect(post).toHaveBeenCalledWith("/api/auth/register", {
      username: "salma",
      email: "salma@example.com",
      password: "longpass1",
    });
    expect(await screen.findByTestId("where")).toHaveTextContent(/^\/library$/);
    expect(screen.getByText("signed in as salma")).toBeInTheDocument();
  });

  it("REG-02: submit stays disabled until every field is filled", async () => {
    renderAuth(["/register"]);
    const user = userEvent.setup();
    await screen.findByRole("heading", { name: en["register.title"] });
    expect(submitButton()).toBeDisabled();
    await user.type(field("register.username"), "salma");
    expect(submitButton()).toBeDisabled();
    await user.type(field("register.email"), "salma@example.com");
    expect(submitButton()).toBeDisabled();
    await user.type(field("register.email"), "{Enter}");
    await user.type(field("register.password"), "longpass1");
    expect(submitButton()).toBeEnabled();
    expect(post).not.toHaveBeenCalled();
  });

  it.each([["username"], ["email"]] as const)(
    "REG-03: a whitespace-only %s keeps submit disabled",
    async (which) => {
      renderAuth(["/register"]);
      const user = await fill({ [which]: "   " });
      expect(submitButton()).toBeDisabled();
      await user.type(field("register.confirm"), "{Enter}");
      expect(post).not.toHaveBeenCalled();
    }
  );

  it("REG-04: a password mismatch shows an alert and disables submit until fixed", async () => {
    renderAuth(["/register"]);
    const user = await fill({ confirm: "" });
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    await user.type(field("register.confirm"), "longpass2");
    expect(screen.getByRole("alert")).toHaveTextContent(en["register.mismatch"]);
    expect(submitButton()).toBeDisabled();
    await user.type(field("register.confirm"), "{Backspace}1");
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(submitButton()).toBeEnabled();
  });

  it("REG-05: a password shorter than 8 characters is not sent", async () => {
    renderAuth(["/register"]);
    await fill({ password: "short", confirm: "short" });
    expect(submitButton()).toBeDisabled();
    await userEvent.setup().type(field("register.confirm"), "{Enter}");
    expect(post).not.toHaveBeenCalled();
  });

  it("REG-07: registration closed (403) replaces the form", async () => {
    post.mockRejectedValue(
      problem(403, { type: "https://maktaba/problems/registration-closed", title: "Forbidden" })
    );
    renderAuth(["/register"]);
    await submit();
    expect(await screen.findByRole("alert")).toHaveTextContent(en["register.closed"]);
    expect(screen.getByRole("link", { name: en["register.toLogin"] })).toHaveAttribute(
      "href",
      "/login"
    );
    expect(screen.queryByLabelText(en["register.username"])).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: en["register.submit"] })).not.toBeInTheDocument();
  });

  it("REG-07: a 403 that is not registration-closed keeps the form", async () => {
    post.mockRejectedValue(problem(403, { detail: "csrf token missing" }));
    renderAuth(["/register"]);
    await submit();
    expect(await screen.findByRole("alert")).toHaveTextContent("csrf token missing");
    expect(field("register.username")).toBeInTheDocument();
  });

  it.each([["username-exists"], ["email-exists"]])(
    "REG-08: 409 %s shows the server detail and keeps the form",
    async (type) => {
      post.mockRejectedValue(problem(409, { type, title: "Conflict", detail: `${type} detail` }));
      renderAuth(["/register"]);
      await submit();
      expect(await screen.findByRole("alert")).toHaveTextContent(`${type} detail`);
      expect(field("register.username")).toHaveValue("salma");
      expect(submitButton()).toBeEnabled();
      expect(screen.queryByTestId("where")).not.toBeInTheDocument();
    }
  );

  it("REG-09: 409 without detail shows the generic conflict text", async () => {
    post.mockRejectedValue(problem(409, { type: "username-exists", title: "Conflict" }));
    renderAuth(["/register"]);
    await submit();
    expect(await screen.findByRole("alert")).toHaveTextContent(en["register.conflict"]);
  });

  it.each([
    ["with detail", { detail: "db down" }, "db down"],
    ["without detail", {}, en["common.error"]],
  ])("REG-10: 500 %s", async (_name, over, text) => {
    post.mockRejectedValue(problem(500, { title: "Internal", ...over }));
    renderAuth(["/register"]);
    await submit();
    expect(await screen.findByRole("alert")).toHaveTextContent(text);
    expect(field("register.username")).toHaveValue("salma");
  });

  it("REG-11: an HTML 502 body is not shown in the alert", async () => {
    post.mockRejectedValue(htmlError());
    renderAuth(["/register"]);
    await submit();
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent(en["common.error"]);
    expect(alert).not.toHaveTextContent(/html|Bad Gateway/);
  });

  it("REG-12: a JSON error without `type` still shows its detail", async () => {
    post.mockRejectedValue(problem(400, { type: undefined, detail: "username too short" }));
    renderAuth(["/register"]);
    await submit();
    expect(await screen.findByRole("alert")).toHaveTextContent("username too short");
    expect(submitButton()).toBeEnabled();
  });

  it("REG-13: a network failure shows the generic error", async () => {
    post.mockRejectedValue(new TypeError("Failed to fetch"));
    renderAuth(["/register"]);
    await submit();
    expect(await screen.findByRole("alert")).toHaveTextContent(en["common.error"]);
    expect(submitButton()).toBeEnabled();
    expect(field("register.email")).toHaveValue("salma@example.com");
  });

  it("REG-14: a 200 answer without user shows an error on /register", async () => {
    post.mockResolvedValue({});
    renderAuth(["/register"]);
    await submit();
    expect(await screen.findByRole("alert", {}, { timeout: 300 })).toBeInTheDocument();
    expect(field("register.username")).toBeInTheDocument();
  });

  it("REG-15: while submitting one request is sent and the button shows Loading", async () => {
    post.mockRejectedValueOnce(problem(500, { detail: "db down" }));
    renderAuth(["/register"]);
    const user = await submit();
    expect(await screen.findByRole("alert")).toHaveTextContent("db down");
    const held = deferred<unknown>();
    post.mockReturnValueOnce(held.promise);
    await user.click(submitButton());
    const busy = screen.getByRole("button", { name: en["common.loading"] });
    expect(busy).toBeDisabled();
    expect(screen.queryByText("db down")).not.toBeInTheDocument();
    await user.click(busy);
    expect(post).toHaveBeenCalledTimes(2);
    await act(async () => held.resolve({ user: salma }));
    expect(await screen.findByTestId("where")).toHaveTextContent("/library");
  });

  it("REG-16: already signed in, /register goes to /library without a render warning", async () => {
    get.mockImplementation(publicGet(amal));
    renderAuth(["/register"]);
    expect(await screen.findByTestId("where")).toHaveTextContent(/^\/library$/);
    expect(screen.queryByLabelText(en["register.username"])).not.toBeInTheDocument();
    const warnings = [...errorSpy.mock.calls, ...warnSpy.mock.calls].map((c) => String(c[0]));
    expect(warnings.filter((w) => /while rendering|navigate\(\)/.test(w))).toEqual([]);
  });

  it("REG-17: both sign-in links go to /login; Back returns to a fresh form", async () => {
    renderAuth(["/register"]);
    const user = userEvent.setup();
    await user.click(await screen.findByRole("link", { name: en["register.haveAccount"] }));
    expect(await screen.findByLabelText(en["login.username"])).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "go back" }));
    expect(await screen.findByRole("heading", { name: en["register.title"] })).toBeInTheDocument();

    post.mockRejectedValue(problem(403, { type: "registration-closed" }));
    await submit();
    await user.click(await screen.findByRole("link", { name: en["register.toLogin"] }));
    expect(await screen.findByLabelText(en["login.username"])).toBeInTheDocument();
  });

  it("REG-20: Arabic: right-to-left with translated labels and messages", async () => {
    localStorage.setItem("mkt:locale", "ar");
    post.mockRejectedValue(problem(403, { type: "registration-closed" }));
    renderAuth(["/register"]);
    const user = userEvent.setup();
    expect(await screen.findByRole("heading", { name: ar["register.title"] })).toBeInTheDocument();
    expect(document.documentElement).toHaveAttribute("dir", "rtl");
    await user.type(screen.getByLabelText(ar["register.username"]), "salma");
    await user.type(screen.getByLabelText(ar["register.email"]), "salma@example.com");
    await user.type(screen.getByLabelText(ar["register.password"]), "longpass1");
    await user.type(screen.getByLabelText(ar["register.confirm"]), "longpass2");
    expect(screen.getByRole("alert")).toHaveTextContent(ar["register.mismatch"]);
    await user.type(screen.getByLabelText(ar["register.confirm"]), "{Backspace}1");
    await user.click(screen.getByRole("button", { name: ar["register.submit"] }));
    expect(await screen.findByRole("alert")).toHaveTextContent(ar["register.closed"]);
  });

  it("REG-23: keyboard, labels and autocomplete", async () => {
    renderAuth(["/register"]);
    await screen.findByRole("heading", { name: en["register.title"] });
    expect(field("register.username")).toHaveFocus();
    expect(screen.getByRole("form", { name: en["register.submit"] })).toBeInTheDocument();
    expect(field("register.username")).toHaveAttribute("autocomplete", "username");
    expect(field("register.email")).toHaveAttribute("autocomplete", "email");
    expect(field("register.password")).toHaveAttribute("autocomplete", "new-password");
    expect(field("register.confirm")).toHaveAttribute("autocomplete", "new-password");
    const user = userEvent.setup();
    await user.type(field("register.username"), "salma");
    await user.tab();
    expect(field("register.email")).toHaveFocus();
    await user.keyboard("salma@example.com");
    await user.tab();
    expect(field("register.password")).toHaveFocus();
    await user.keyboard("longpass1");
    await user.tab();
    expect(field("register.confirm")).toHaveFocus();
    await user.keyboard("longpass1{Enter}");
    expect(post).toHaveBeenCalledTimes(1);
  });
});
