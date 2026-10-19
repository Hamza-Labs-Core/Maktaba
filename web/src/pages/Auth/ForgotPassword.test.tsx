// Forgot-password cases from web/test-cases/forgot-password.md.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { act, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post } };
});

import { deferred, problem, publicGet, renderAuth } from "./authTestHarness";
import en from "../../i18n/en.json";
import ar from "../../i18n/ar.json";

const email = () => screen.getByLabelText(en["register.email"]);
const sendButton = () => screen.getByRole("button", { name: en["forgot.submit"] });

async function send(address = "amal@example.com") {
  const user = userEvent.setup();
  await screen.findByRole("heading", { name: en["forgot.title"] });
  await user.type(email(), address);
  await user.click(sendButton());
  return user;
}

beforeEach(() => {
  get.mockReset();
  post.mockReset();
  get.mockImplementation(publicGet(null));
  post.mockResolvedValue({ ok: true });
});

describe("ForgotPassword (test-cases/forgot-password.md)", () => {
  it("FPW-01: sending the link shows the neutral confirmation", async () => {
    renderAuth(["/forgot-password"]);
    expect(await screen.findByText(en["forgot.intro"])).toBeInTheDocument();
    await send();
    expect(post).toHaveBeenCalledTimes(1);
    expect(post).toHaveBeenCalledWith("/api/auth/forgot-password", { email: "amal@example.com" });
    expect(await screen.findByText(en["forgot.sent"])).toBeInTheDocument();
    expect(screen.queryByLabelText(en["register.email"])).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: en["forgot.submit"] })).not.toBeInTheDocument();
    expect(screen.getByRole("link", { name: en["register.toLogin"] })).toHaveAttribute(
      "href",
      "/login"
    );
  });

  it("FPW-02: a known and an unknown address get the same screen", async () => {
    const { container, unmount } = renderAuth(["/forgot-password"]);
    await send("known@example.com");
    await screen.findByText(en["forgot.sent"]);
    const known = container.innerHTML;
    unmount();

    post.mockRejectedValue(problem(404));
    const second = renderAuth(["/forgot-password"]);
    await send("nobody@example.com");
    await screen.findByText(en["forgot.sent"]);
    expect(second.container.innerHTML).toBe(known);
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("FPW-03: submit is disabled while the email is empty or spaces", async () => {
    renderAuth(["/forgot-password"]);
    const user = userEvent.setup();
    await screen.findByRole("heading", { name: en["forgot.title"] });
    expect(sendButton()).toBeDisabled();
    await user.type(email(), "{Enter}");
    await user.type(email(), "   ");
    expect(sendButton()).toBeDisabled();
    await user.clear(email());
    await user.type(email(), "amal@example.com");
    expect(sendButton()).toBeEnabled();
    expect(post).not.toHaveBeenCalled();
  });

  it.each([
    ["500", problem(500)],
    ["429", problem(429)],
    ["403", problem(403)],
  ])("FPW-05: a %s answer still shows the neutral confirmation", async (_name, err) => {
    post.mockRejectedValue(err);
    renderAuth(["/forgot-password"]);
    await send();
    expect(await screen.findByText(en["forgot.sent"])).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("FPW-06: a network failure still shows the neutral confirmation", async () => {
    post.mockRejectedValue(new TypeError("Failed to fetch"));
    renderAuth(["/forgot-password"]);
    await send();
    expect(await screen.findByText(en["forgot.sent"])).toBeInTheDocument();
  });

  it("FPW-07: while sending the button shows Loading and one request is sent", async () => {
    const held = deferred<unknown>();
    post.mockReturnValue(held.promise);
    renderAuth(["/forgot-password"]);
    const user = await send();
    const busy = screen.getByRole("button", { name: en["common.loading"] });
    expect(busy).toBeDisabled();
    await user.click(busy);
    expect(post).toHaveBeenCalledTimes(1);
    expect(screen.queryByText(en["forgot.sent"])).not.toBeInTheDocument();
    await act(async () => held.resolve({ ok: true }));
    expect(await screen.findByText(en["forgot.sent"])).toBeInTheDocument();
  });

  it("FPW-08: Back to sign in works before and after sending", async () => {
    renderAuth(["/forgot-password"]);
    const user = userEvent.setup();
    const link = await screen.findByRole("link", { name: en["register.toLogin"] });
    expect(link).toHaveAttribute("href", "/login");
    await user.click(link);
    expect(await screen.findByLabelText(en["login.username"])).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "go back" }));
    await send();
    const after = await screen.findByRole("link", { name: en["register.toLogin"] });
    expect(after).toHaveAttribute("href", "/login");
    await user.click(after);
    expect(await screen.findByLabelText(en["login.username"])).toBeInTheDocument();
  });

  it("FPW-11: the confirmation is announced (a status live region)", async () => {
    renderAuth(["/forgot-password"]);
    await send();
    expect(await screen.findByRole("status")).toHaveTextContent(en["forgot.sent"]);
  });

  it("FPW-13: Arabic: right-to-left with translated text", async () => {
    localStorage.setItem("mkt:locale", "ar");
    renderAuth(["/forgot-password"]);
    const user = userEvent.setup();
    expect(await screen.findByRole("heading", { name: ar["forgot.title"] })).toBeInTheDocument();
    expect(document.documentElement).toHaveAttribute("dir", "rtl");
    await user.type(screen.getByLabelText(ar["register.email"]), "amal@example.com");
    await user.click(screen.getByRole("button", { name: ar["forgot.submit"] }));
    expect(await screen.findByText(ar["forgot.sent"])).toBeInTheDocument();
    expect(screen.getByRole("link", { name: ar["register.toLogin"] })).toBeInTheDocument();
  });

  it("FPW-16: keyboard and labels", async () => {
    renderAuth(["/forgot-password"]);
    await screen.findByRole("heading", { name: en["forgot.title"] });
    expect(email()).toHaveFocus();
    expect(email()).toHaveAttribute("autocomplete", "email");
    expect(screen.getByRole("form", { name: en["forgot.submit"] })).toBeInTheDocument();
    const user = userEvent.setup();
    await user.tab();
    // the disabled button is skipped
    expect(screen.getByRole("link", { name: en["register.toLogin"] })).toHaveFocus();
    await user.click(email());
    await user.type(email(), "amal@example.com");
    await user.tab();
    expect(sendButton()).toHaveFocus();
    await user.tab();
    expect(screen.getByRole("link", { name: en["register.toLogin"] })).toHaveFocus();
    await user.type(email(), "{Enter}");
    expect(post).toHaveBeenCalledTimes(1);
  });
});
