// Story 50.30 T07: the sign-in page's answers to the edge's and the bot check's refusals, in
// English and in Arabic (right to left), and the Turnstile widget's token going with the form.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { setAccessToken } from "../shared/api";
import { ME, renderAt, signedOut, stubFetch, user } from "../test/harness";

beforeEach(() => setAccessToken(null));
afterEach(() => {
  vi.unstubAllGlobals();
  localStorage.removeItem("mkt:cloud:locale");
  delete (window as { turnstile?: unknown }).turnstile;
});

async function fillSignIn(button = "Sign in") {
  const u = userEvent.setup();
  await u.type(await screen.findByLabelText(/^(Email|البريد)/), "sam@example.com");
  await u.type(screen.getByLabelText(/^(Password|كلمة المرور)/), "correct horse");
  await u.click(screen.getByRole("button", { name: button }));
  return u;
}

const answers: [string, number, Record<string, unknown>, string, string][] = [
  [
    "the bot check failed",
    403,
    { error: "turnstile_failed" },
    "We couldn't check that you're a person. Reload the page and try again.",
    "تعذّر التحقق من أنك إنسان. أعد تحميل الصفحة وحاول مجددًا.",
  ],
  [
    "the edge's rate limit",
    429,
    {
      error: "rate_limited",
      message: "Too many requests. Wait a moment and try again.",
      message_ar: "طلبات كثيرة. انتظر قليلًا ثم حاول مجددًا.",
    },
    "Too many requests. Wait a moment and try again.",
    "طلبات كثيرة. انتظر قليلًا ثم حاول مجددًا.",
  ],
  [
    "the bot check unavailable",
    503,
    { error: "turnstile_unavailable" },
    "Sign-in is briefly unavailable. Try again in a minute.",
    "تسجيل الدخول غير متاح مؤقتًا. حاول مجددًا بعد دقيقة.",
  ],
];

describe("the sign-in page's refusals", () => {
  for (const [name, status, body, en] of answers) {
    it(`${name}: in English`, async () => {
      stubFetch({
        "POST /v1/auth/refresh": signedOut,
        "GET /v1/auth/providers": [200, { google: false, apple: false }],
        "POST /v1/auth/login": [status, body],
      });
      renderAt("/sign-in");
      await fillSignIn();
      expect(await screen.findByText(en)).toBeInTheDocument();
    });
  }

  for (const [name, status, body, , ar] of answers) {
    it(`${name}: in Arabic, right to left`, async () => {
      localStorage.setItem("mkt:cloud:locale", "ar");
      stubFetch({
        "POST /v1/auth/refresh": signedOut,
        "GET /v1/auth/providers": [200, { google: false, apple: false }],
        "POST /v1/auth/login": [status, body],
      });
      renderAt("/sign-in");
      const submit = await screen.findByRole("button", { name: /دخول|تسجيل الدخول/ });
      await fillSignIn(submit.textContent ?? "");
      expect(await screen.findByText(ar)).toBeInTheDocument();
      expect(document.documentElement.getAttribute("dir")).toBe("rtl");
    });
  }
});

describe("the Turnstile widget", () => {
  it("renders with the cloud's site key; its token goes with the sign-in, and a refusal asks for a fresh one", async () => {
    const reset = vi.fn();
    const render = vi.fn((_el: HTMLElement, opts: Record<string, unknown>) => {
      (opts.callback as (t: string) => void)("tok-1");
      return "w1";
    });
    (window as { turnstile?: unknown }).turnstile = { render, reset, remove: vi.fn() };
    const f = stubFetch({
      "POST /v1/auth/refresh": signedOut,
      "GET /v1/auth/providers": [
        200,
        { google: false, apple: false, turnstile_site_key: "0x4AAA" },
      ],
      "POST /v1/auth/login": [403, { error: "turnstile_failed" }],
    });
    renderAt("/sign-in");
    await waitFor(() => expect(render).toHaveBeenCalled());
    expect(render.mock.calls[0]![1]).toMatchObject({ sitekey: "0x4AAA" });
    expect(screen.getByRole("group", { name: "Check that you're a person" })).toBeInTheDocument();
    await fillSignIn();
    await waitFor(() => expect(f.find("POST", "/v1/auth/login")).toHaveLength(1));
    expect(f.find("POST", "/v1/auth/login")[0].body).toMatchObject({ turnstile_token: "tok-1" });
    await waitFor(() => expect(reset).toHaveBeenCalledWith("w1"));
  });

  it("without a site key: no widget, and no token in the request", async () => {
    const f = stubFetch({
      "POST /v1/auth/refresh": signedOut,
      "GET /v1/auth/providers": [200, { google: false, apple: false }],
      "POST /v1/auth/login": [200, { access_token: "tok", expires_in: 900, user: user() }],
      "GET /v1/account/me": [200, ME],
      "GET /v1/account/sessions": [200, { sessions: [] }],
    });
    renderAt("/sign-in");
    await fillSignIn();
    await waitFor(() => expect(f.find("POST", "/v1/auth/login")).toHaveLength(1));
    expect(f.find("POST", "/v1/auth/login")[0].body).toEqual({
      email: "sam@example.com",
      password: "correct horse",
    });
    expect(screen.queryByRole("group", { name: "Check that you're a person" })).toBeNull();
  });
});
