import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { setAccessToken } from "../api";
import { json, ME, renderAt, signedOut, stubFetch, user } from "../test/harness";

const { navigateExternal } = vi.hoisted(() => ({ navigateExternal: vi.fn() }));
vi.mock("../nav", () => ({ navigateExternal }));

beforeEach(() => {
  setAccessToken(null);
  navigateExternal.mockReset();
});
afterEach(() => vi.unstubAllGlobals());

async function fillSignIn(email = "sam@example.com", password = "correct horse") {
  const u = userEvent.setup();
  await u.type(await screen.findByLabelText(/^Email/), email);
  await u.type(screen.getByLabelText(/^Password/), password);
  await u.click(screen.getByRole("button", { name: "Sign in" }));
  return u;
}

describe("sign in", () => {
  it("signs in and lands on the account page", async () => {
    const f = stubFetch({
      "POST /v1/auth/refresh": signedOut,
      "GET /v1/auth/providers": [200, { google: false, apple: false }],
      "POST /v1/auth/login": [200, { access_token: "tok", expires_in: 900, user: user() }],
      "GET /v1/account/me": [200, ME],
      "GET /v1/account/sessions": [200, { sessions: [] }],
    });
    renderAt("/sign-in");
    await fillSignIn();
    expect(await screen.findByRole("heading", { name: "Your account" })).toBeInTheDocument();
    expect(f.find("POST", "/v1/auth/login")[0].body).toEqual({
      email: "sam@example.com",
      password: "correct horse",
    });
    // the account call carries the token from the login
    expect(f.find("GET", "/v1/account/me")[0].headers.Authorization).toBe("Bearer tok");
  });

  it("offers a resend when the email is not verified", async () => {
    const f = stubFetch({
      "POST /v1/auth/refresh": signedOut,
      "GET /v1/auth/providers": [200, { google: false, apple: false }],
      "POST /v1/auth/login": [
        403,
        {
          error: "email_not_verified",
          message: "verify",
          resend_url: "/v1/auth/verify-email/resend",
        },
      ],
      "POST /v1/auth/verify-email/resend": [202],
    });
    renderAt("/sign-in");
    const u = await fillSignIn();
    expect(await screen.findByText(/Verify your email before signing in/)).toBeInTheDocument();
    await u.click(screen.getByRole("button", { name: "Resend the verification email" }));
    await waitFor(() => expect(f.find("POST", "/v1/auth/verify-email/resend")).toHaveLength(1));
    expect(f.find("POST", "/v1/auth/verify-email/resend")[0].body).toEqual({
      email: "sam@example.com",
    });
    expect(await screen.findByText(/a new verification email is on its way/)).toBeInTheDocument();
  });

  it("says the account is suspended", async () => {
    stubFetch({
      "POST /v1/auth/refresh": signedOut,
      "GET /v1/auth/providers": [200, { google: false, apple: false }],
      "POST /v1/auth/login": [403, { error: "account_suspended", message: "suspended" }],
    });
    renderAt("/sign-in");
    await fillSignIn();
    expect(await screen.findByText(/This account is suspended/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Resend/ })).not.toBeInTheDocument();
  });

  it("tells a locked-out user how long to wait (429)", async () => {
    stubFetch({
      "POST /v1/auth/refresh": signedOut,
      "GET /v1/auth/providers": [200, { google: false, apple: false }],
      "POST /v1/auth/login": () =>
        json(429, { error: "too_many_attempts", message: "locked" }, { "Retry-After": "1800" }),
    });
    renderAt("/sign-in");
    await fillSignIn();
    expect(
      await screen.findByText("Too many attempts. Try again in 30 minutes.")
    ).toBeInTheDocument();
  });

  it("shows only the enabled providers, which start the OAuth redirect", async () => {
    stubFetch({
      "POST /v1/auth/refresh": signedOut,
      "GET /v1/auth/providers": [200, { google: true, apple: false }],
    });
    renderAt("/sign-in");
    const google = await screen.findByRole("button", { name: "Continue with Google" });
    expect(screen.queryByRole("button", { name: "Continue with Apple" })).not.toBeInTheDocument();
    await userEvent.setup().click(google);
    expect(navigateExternal).toHaveBeenCalledWith("/v1/auth/oauth/google");
  });

  it("shows the OAuth error from the query string", async () => {
    stubFetch({
      "POST /v1/auth/refresh": signedOut,
      "GET /v1/auth/providers": [200, { google: true, apple: true }],
    });
    renderAt("/sign-in?error=state_mismatch");
    expect(await screen.findByText(/didn't work \(state_mismatch\)/)).toBeInTheDocument();
  });
});

describe("register", () => {
  it("shows the check-your-email state after the 202", async () => {
    const f = stubFetch({
      "POST /v1/auth/refresh": signedOut,
      "POST /v1/auth/register": [202, { status: "pending_verification" }],
    });
    renderAt("/register");
    const u = userEvent.setup();
    await u.type(await screen.findByLabelText(/^Email/), "new@example.com");
    const pw = screen.getByLabelText(/^Password/);
    await u.type(pw, "short");
    expect(screen.getByText("Use at least 10 characters.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Create account" })).toBeDisabled();
    await u.type(pw, "-but-long-now");
    await u.click(screen.getByRole("button", { name: "Create account" }));
    expect(await screen.findByRole("heading", { name: "Check your email" })).toBeInTheDocument();
    expect(screen.getByText(/new@example.com/)).toBeInTheDocument();
    expect(f.find("POST", "/v1/auth/register")[0].body).toEqual({
      email: "new@example.com",
      password: "short-but-long-now",
    });
  });

  it("shows a weak_password error from the server", async () => {
    stubFetch({
      "POST /v1/auth/refresh": signedOut,
      "POST /v1/auth/register": [400, { error: "weak_password", message: "breached" }],
    });
    renderAt("/register");
    const u = userEvent.setup();
    await u.type(await screen.findByLabelText(/^Email/), "new@example.com");
    await u.type(screen.getByLabelText(/^Password/), "password123");
    await u.click(screen.getByRole("button", { name: "Create account" }));
    expect(await screen.findByText(/too weak or too common/)).toBeInTheDocument();
  });
});

describe("verify email", () => {
  it("posts the token once", async () => {
    const f = stubFetch({
      "POST /v1/auth/refresh": signedOut,
      "POST /v1/auth/verify-email": [200, { status: "verified" }],
    });
    renderAt("/verify-email?token=abc");
    expect(await screen.findByText(/Your email is verified/)).toBeInTheDocument();
    expect(f.find("POST", "/v1/auth/verify-email")).toHaveLength(1);
    expect(f.find("POST", "/v1/auth/verify-email")[0].body).toEqual({ token: "abc" });
  });
});
