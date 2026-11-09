// Story 50.33: the console's staff sign-in. T14: sign-in, the two-step check (an app code or a
// recovery code), enrolment, the re-auth dialog, the idle warning, a locked account, a refused
// network, and the idle and 8-hour messages, in English and Arabic (right to left). T15: every
// staff.* string of the story, word for word, in en.json and ar.json.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { api, setStaffToken } from "./api";
import { IDLE_END_MS, IDLE_WARN_MS } from "./session";
import { groupSecret } from "./pages/Enroll";
import en from "./i18n/en.json";
import ar from "./i18n/ar.json";
import { json, renderAt, staffSession, staffSignedOut, stubFetch } from "./test/harness";

beforeEach(() => setStaffToken(null));
afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  localStorage.clear();
});

// story 50.33 §9, word for word
const SPEC: [string, string, string][] = [
  ["staff.signIn.title", "Staff sign-in", "تسجيل دخول الفريق"],
  ["staff.signIn.submit", "Continue", "متابعة"],
  [
    "staff.signIn.failed",
    "Email or password is wrong.",
    "البريد الإلكتروني أو كلمة المرور غير صحيحة.",
  ],
  ["staff.mfa.title", "Two-step check", "التحقق بخطوتين"],
  ["staff.mfa.passkey", "Use your passkey", "استخدم مفتاح المرور"],
  [
    "staff.mfa.totp",
    "Enter the 6-digit code from your authenticator app",
    "أدخل الرمز المكوّن من 6 أرقام من تطبيق المصادقة",
  ],
  ["staff.mfa.recovery", "Use a recovery code", "استخدم رمز استرداد"],
  ["staff.mfa.failed", "That code didn't work. Try again.", "لم يعمل هذا الرمز. حاول مجددًا."],
  [
    "staff.mfa.required",
    "This account needs two-step sign-in set up first. Ask the owner for an enrolment link.",
    "يحتاج هذا الحساب إلى إعداد التحقق بخطوتين أولًا. اطلب رابط التسجيل من المالك.",
  ],
  ["staff.enroll.title", "Set up your staff account", "إعداد حساب الفريق"],
  ["staff.enroll.passkey", "Add a passkey", "أضف مفتاح مرور"],
  [
    "staff.enroll.totp",
    "Scan this code with your authenticator app",
    "امسح هذا الرمز بتطبيق المصادقة",
  ],
  [
    "staff.enroll.recoveryCodes",
    "Keep these recovery codes somewhere safe. Each one works once.",
    "احفظ رموز الاسترداد هذه في مكان آمن. كل رمز يعمل مرة واحدة.",
  ],
  [
    "staff.enroll.linkExpired",
    "This enrolment link has expired or was already used.",
    "انتهت صلاحية رابط التسجيل هذا أو سبق استخدامه.",
  ],
  ["staff.reauth.title", "Confirm it's you", "أكّد هويتك"],
  [
    "staff.reauth.body",
    "This action needs a fresh check with your passkey or code.",
    "يحتاج هذا الإجراء إلى تحقق جديد بمفتاح المرور أو الرمز.",
  ],
  [
    "staff.session.idleWarning",
    "You'll be signed out in 2 minutes unless you do something.",
    "سيتم تسجيل خروجك بعد دقيقتين ما لم تقم بأي إجراء.",
  ],
  [
    "staff.session.idle",
    "You were signed out after 15 minutes without activity.",
    "تم تسجيل خروجك بعد 15 دقيقة دون نشاط.",
  ],
  [
    "staff.session.capped",
    "Your session ended after 8 hours. Sign in again.",
    "انتهت جلستك بعد 8 ساعات. سجّل الدخول من جديد.",
  ],
  [
    "staff.locked",
    "Too many failed attempts. This account is locked for 30 minutes.",
    "محاولات فاشلة كثيرة. هذا الحساب مقفل لمدة 30 دقيقة.",
  ],
  [
    "staff.ipNotAllowed",
    "The console can't be used from this network.",
    "لا يمكن استخدام اللوحة من هذه الشبكة.",
  ],
  [
    "staff.forbidden",
    "Your staff account doesn't have this permission.",
    "لا يملك حساب الفريق الخاص بك هذه الصلاحية.",
  ],
  ["staff.sessions.title", "Signed-in devices", "الأجهزة المسجّل دخولها"],
  ["staff.sessions.signOutAll", "Sign out everywhere", "تسجيل الخروج من كل الأجهزة"],
  ["staff.audit.signins", "Staff sign-ins", "عمليات تسجيل دخول الفريق"],
];

const EN = en as Record<string, string>;
const AR = ar as Record<string, string>;

const LOGIN_OK: [number, unknown] = [200, { mfa_token: "m1", methods: ["totp", "recovery"] }];
const NO_SESSIONS: [number, unknown] = [200, { sessions: [] }];

async function typePassword(u: ReturnType<typeof userEvent.setup>) {
  await u.type(await screen.findByLabelText("Email"), "ops@example.com");
  await u.type(screen.getByLabelText("Password"), "correct horse battery");
  await u.click(screen.getByRole("button", { name: "Continue" }));
}

describe("T15: the staff strings", () => {
  it("has every staff.* string of the story in English and Arabic, word for word", () => {
    for (const [key, english, arabic] of SPEC) {
      expect([key, EN[key]]).toEqual([key, english]);
      expect([key, AR[key]]).toEqual([key, arabic]);
    }
  });

  it("has every staff.* key in both catalogs", () => {
    const staffKeys = (m: Record<string, string>) =>
      Object.keys(m)
        .filter((k) => k.startsWith("staff."))
        .sort();
    expect(staffKeys(AR)).toEqual(staffKeys(EN));
    for (const k of staffKeys(EN)) expect([k, AR[k].trim() !== ""]).toEqual([k, true]);
  });
});

describe("T14: staff sign-in", () => {
  it("signs in with a password then an app code, and keeps the token in memory only", async () => {
    const f = stubFetch({
      "POST /v1/staff/refresh": staffSignedOut,
      "POST /v1/staff/login": LOGIN_OK,
      "POST /v1/staff/mfa/totp": staffSession(),
      "GET /v1/staff/sessions": NO_SESSIONS,
    });
    renderAt("/sessions");
    const u = userEvent.setup();
    await typePassword(u);
    expect(await screen.findByRole("heading", { name: "Two-step check" })).toBeInTheDocument();
    expect(f.find("POST", "/v1/staff/login")[0].body).toEqual({
      email: "ops@example.com",
      password: "correct horse battery",
    });
    // the console's CSRF header on the sign-in too
    expect(f.find("POST", "/v1/staff/login")[0].headers["X-Maktaba-Staff"]).toBe("1");

    await u.type(
      screen.getByLabelText("Enter the 6-digit code from your authenticator app"),
      "123456"
    );
    await u.click(screen.getByRole("button", { name: "Continue" }));
    expect(await screen.findByRole("heading", { name: "Signed-in devices" })).toBeInTheDocument();
    expect(f.find("POST", "/v1/staff/mfa/totp")[0].body).toEqual({
      mfa_token: "m1",
      code: "123456",
    });
    expect(f.find("GET", "/v1/staff/sessions")[0].headers.Authorization).toBe("Bearer staff-tok");
    expect(screen.getByText("Signed in as ops@example.com")).toBeInTheDocument();
    expect(JSON.stringify({ ...localStorage })).not.toContain("staff-tok");
    // no customer route is called
    expect(f.calls.filter((c) => c.path.startsWith("/v1/auth/"))).toEqual([]);
  });

  it("shows the bot check when the console serves its site key, and sends its token", async () => {
    const reset = vi.fn();
    const render = vi.fn((_el: HTMLElement, opts: Record<string, unknown>) => {
      (opts.callback as (t: string) => void)("tok-1");
      return "w1";
    });
    (window as { turnstile?: unknown }).turnstile = { render, reset, remove: vi.fn() };
    try {
      const f = stubFetch({
        "POST /v1/staff/refresh": staffSignedOut,
        "GET /v1/staff/config": [200, { turnstile_site_key: "0x4AAA" }],
        "POST /v1/staff/login": [403, { error: "turnstile_failed" }],
      });
      renderAt("/");
      await waitFor(() => expect(render).toHaveBeenCalled());
      expect(render.mock.calls[0]![1]).toMatchObject({ sitekey: "0x4AAA" });
      const u = userEvent.setup();
      await typePassword(u);
      await waitFor(() => expect(f.find("POST", "/v1/staff/login")).toHaveLength(1));
      expect(f.find("POST", "/v1/staff/login")[0].body).toMatchObject({ turnstile_token: "tok-1" });
      // a token is good once: the widget is asked for a new one
      await waitFor(() => expect(reset).toHaveBeenCalledWith("w1"));
    } finally {
      delete (window as { turnstile?: unknown }).turnstile;
    }
  });

  it("switches to a recovery code", async () => {
    const f = stubFetch({
      "POST /v1/staff/refresh": staffSignedOut,
      "POST /v1/staff/login": LOGIN_OK,
      "POST /v1/staff/mfa/recovery": staffSession(),
      "GET /v1/staff/sessions": NO_SESSIONS,
    });
    renderAt("/sessions");
    const u = userEvent.setup();
    await typePassword(u);
    await u.click(await screen.findByRole("button", { name: "Use a recovery code" }));
    await u.type(screen.getByLabelText("Recovery code"), "abcd-efgh-ijkl");
    await u.click(screen.getByRole("button", { name: "Continue" }));
    expect(await screen.findByRole("heading", { name: "Signed-in devices" })).toBeInTheDocument();
    expect(f.find("POST", "/v1/staff/mfa/recovery")[0].body).toEqual({
      mfa_token: "m1",
      code: "abcd-efgh-ijkl",
    });
    expect(f.find("POST", "/v1/staff/mfa/totp")).toEqual([]);
  });

  it("says a wrong email or password, a wrong code, and starts again on a spent step", async () => {
    let logins = 0;
    stubFetch({
      "POST /v1/staff/refresh": staffSignedOut,
      "POST /v1/staff/login": () => {
        logins++;
        return logins === 1
          ? json(401, { error: "invalid_credentials" })
          : json(200, { mfa_token: "m1", methods: ["totp", "recovery"] });
      },
      "POST /v1/staff/mfa/totp": (_c, n) =>
        n === 1 ? json(401, { error: "mfa_failed" }) : json(401, { error: "mfa_token_invalid" }),
    });
    renderAt("/");
    const u = userEvent.setup();
    await typePassword(u);
    expect(await screen.findByText("Email or password is wrong.")).toBeInTheDocument();
    await u.type(screen.getByLabelText("Password"), "correct horse battery");
    await u.click(screen.getByRole("button", { name: "Continue" }));
    const code = await screen.findByLabelText("Enter the 6-digit code from your authenticator app");
    await u.type(code, "000000");
    await u.click(screen.getByRole("button", { name: "Continue" }));
    expect(await screen.findByText("That code didn't work. Try again.")).toBeInTheDocument();
    await u.click(screen.getByRole("button", { name: "Continue" }));
    // the step's token expired or took its attempts: back to the email and password
    expect(await screen.findByRole("heading", { name: "Staff sign-in" })).toBeInTheDocument();
    expect(
      screen.getByText("The sign-in step timed out. Enter your email and password again.")
    ).toBeInTheDocument();
  });

  it("says a locked account, a missing factor and a refused network", async () => {
    const answers = [
      json(423, { error: "account_locked" }, { "Retry-After": "1800" }),
      json(403, { error: "mfa_required" }),
      json(403, { error: "ip_not_allowed" }),
    ];
    stubFetch({
      "POST /v1/staff/refresh": staffSignedOut,
      "POST /v1/staff/login": (_c, n) => answers[n - 1],
    });
    renderAt("/");
    const u = userEvent.setup();
    await typePassword(u);
    expect(
      await screen.findByText("Too many failed attempts. This account is locked for 30 minutes.")
    ).toBeInTheDocument();
    await u.type(screen.getByLabelText("Password"), "correct horse battery");
    await u.click(screen.getByRole("button", { name: "Continue" }));
    expect(
      await screen.findByText(
        "This account needs two-step sign-in set up first. Ask the owner for an enrolment link."
      )
    ).toBeInTheDocument();
    await u.type(screen.getByLabelText("Password"), "correct horse battery");
    await u.click(screen.getByRole("button", { name: "Continue" }));
    expect(
      await screen.findByText("The console can't be used from this network.")
    ).toBeInTheDocument();
  });

  it("explains a session the server ended for idling", async () => {
    stubFetch({ "POST /v1/staff/refresh": [401, { error: "no_session", reason: "idle" }] });
    renderAt("/");
    expect(
      await screen.findByText("You were signed out after 15 minutes without activity.")
    ).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Staff sign-in" })).toBeInTheDocument();
  });

  it("explains a session an admin call found past its 8 hours", async () => {
    stubFetch({
      "POST /v1/staff/refresh": staffSession(),
      "GET /v1/admin/audit": [401, { error: "no_session", reason: "capped" }],
    });
    renderAt("/audit");
    expect(
      await screen.findByText("Your session ended after 8 hours. Sign in again.")
    ).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Staff sign-in" })).toBeInTheDocument();
  });

  it("refreshes an expired token once and sends the admin call again", async () => {
    const f = stubFetch({
      "POST /v1/staff/refresh": staffSession(),
      "GET /v1/admin/audit": (_c, n) =>
        n === 1 ? json(401, { error: "no_session" }) : json(200, { entries: [], total: 0 }),
    });
    renderAt("/audit");
    expect(await screen.findByText("No audit entries.")).toBeInTheDocument();
    expect(f.find("POST", "/v1/staff/refresh")).toHaveLength(2);
    expect(f.find("GET", "/v1/admin/audit")).toHaveLength(2);
  });

  it("is right to left in Arabic", async () => {
    localStorage.setItem("mkt:cloud:locale", "ar");
    stubFetch({
      "POST /v1/staff/refresh": staffSignedOut,
      "POST /v1/staff/login": LOGIN_OK,
    });
    renderAt("/");
    expect(await screen.findByRole("heading", { name: "تسجيل دخول الفريق" })).toBeInTheDocument();
    await waitFor(() => expect(document.documentElement.dir).toBe("rtl"));
    const u = userEvent.setup();
    await u.type(screen.getByLabelText("البريد الإلكتروني"), "ops@example.com");
    await u.type(screen.getByLabelText("كلمة المرور"), "correct horse battery");
    await u.click(screen.getByRole("button", { name: "متابعة" }));
    expect(await screen.findByRole("heading", { name: "التحقق بخطوتين" })).toBeInTheDocument();
    expect(
      screen.getByLabelText("أدخل الرمز المكوّن من 6 أرقام من تطبيق المصادقة")
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "استخدم رمز استرداد" })).toBeInTheDocument();
  });
});

describe("T14: the console's session", () => {
  it("asks for a fresh code on reauth_required, then sends the action again", async () => {
    let status = "active";
    const f = stubFetch({
      "POST /v1/staff/refresh": staffSession(),
      "GET /v1/admin/accounts/u2": () =>
        json(200, {
          account: {
            id: "u2",
            email: "bad@example.com",
            plan: "pro",
            status,
            role: "user",
            created_at: "2026-01-01T00:00:00Z",
            servers: 0,
          },
          subscription: { plan: "pro", status: "active" },
          servers: [],
          usage: { month: "2026-10", used_bytes: 5e9, limit_bytes: 1e11 },
          sessions_active: 1,
          abuse: [],
          audit: [],
        }),
      "POST /v1/admin/accounts/u2/suspend": (_c, n) => {
        if (n === 1) return json(401, { error: "reauth_required" });
        status = "suspended";
        return new Response(null, { status: 204 });
      },
      "POST /v1/staff/reauth/totp": () => {
        const [, body] = staffSession();
        return json(200, { ...(body as object), access_token: "staff-tok-2" });
      },
    });
    renderAt("/accounts/u2");
    const u = userEvent.setup();
    await u.click(await screen.findByRole("button", { name: "Suspend" }));
    const reason = await screen.findByRole("dialog");
    await u.type(within(reason).getByLabelText(/Reason/), "chargeback fraud");
    await u.click(within(reason).getByRole("button", { name: "Suspend" }));

    const check = await screen.findByRole("dialog", { name: "Confirm it's you" });
    expect(
      within(check).getByText("This action needs a fresh check with your passkey or code.")
    ).toBeInTheDocument();
    await u.type(
      within(check).getByLabelText("Enter the 6-digit code from your authenticator app"),
      "654321"
    );
    await u.click(within(check).getByRole("button", { name: "Confirm" }));

    expect(await screen.findByRole("button", { name: "Unsuspend" })).toBeInTheDocument();
    expect(f.find("POST", "/v1/staff/reauth/totp")[0].body).toEqual({ code: "654321" });
    const sent = f.find("POST", "/v1/admin/accounts/u2/suspend");
    expect(sent).toHaveLength(2);
    expect(sent[1].body).toEqual({ reason: "chargeback fraud" });
    expect(sent[1].headers.Authorization).toBe("Bearer staff-tok-2");
  });

  it("keeps the action refused when the check is cancelled", async () => {
    const f = stubFetch({
      "POST /v1/staff/refresh": staffSession(),
      "GET /v1/staff/sessions": NO_SESSIONS,
      "POST /v1/admin/audit/noop": [401, { error: "reauth_required" }],
    });
    // drive the client directly: the dialog's Cancel resolves the check as refused
    renderAt("/sessions");
    await screen.findByRole("heading", { name: "Signed-in devices" });
    const call = api.post("/v1/admin/audit/noop", {}).catch((e: unknown) => e);
    const check = await screen.findByRole("dialog", { name: "Confirm it's you" });
    await userEvent.setup().click(within(check).getByRole("button", { name: "Cancel" }));
    expect(await call).toMatchObject({ code: "reauth_required" });
    expect(f.find("POST", "/v1/admin/audit/noop")).toHaveLength(1);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("warns at 13 idle minutes, clears on an admin request, and ends at 15", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    let idle = false;
    stubFetch({
      "POST /v1/staff/refresh": () =>
        idle ? json(401, { error: "no_session", reason: "idle" }) : json(...staffSession()),
      "GET /v1/staff/sessions": NO_SESSIONS,
      "GET /v1/admin/audit": [200, { entries: [], total: 0 }],
    });
    renderAt("/sessions");
    await screen.findByRole("heading", { name: "Signed-in devices" });
    const warning = "You'll be signed out in 2 minutes unless you do something.";
    expect(screen.queryByText(warning)).not.toBeInTheDocument();

    await act(async () => {
      vi.advanceTimersByTime(IDLE_WARN_MS);
    });
    expect(screen.getByText(warning)).toBeInTheDocument();

    // any admin action restarts the clock
    const u = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    await u.click(screen.getByRole("link", { name: "Staff sign-ins" }));
    await screen.findByText("No audit entries.");
    await waitFor(() => expect(screen.queryByText(warning)).not.toBeInTheDocument());

    // then 15 minutes with none: the server ends the session and the sign-in says why
    idle = true;
    await act(async () => {
      vi.advanceTimersByTime(IDLE_END_MS);
    });
    expect(
      await screen.findByText("You were signed out after 15 minutes without activity.")
    ).toBeInTheDocument();
  });

  it("shows the frame's reason for a refused admin call", async () => {
    stubFetch({
      "POST /v1/staff/refresh": staffSession([]),
      "GET /v1/admin/audit": [403, { error: "admin_access_denied" }],
      "GET /v1/admin/servers": [403, { error: "ip_not_allowed" }],
    });
    renderAt("/audit");
    expect(
      await screen.findByText("Your staff account doesn't have this permission.")
    ).toBeInTheDocument();
    const u = userEvent.setup();
    await u.click(screen.getByRole("link", { name: "Servers" }));
    expect(
      await screen.findByText("The console can't be used from this network.")
    ).toBeInTheDocument();
    expect(
      screen.queryByText("Your staff account doesn't have this permission.")
    ).not.toBeInTheDocument();
  });

  it("lists the signed-in devices and signs out everywhere", async () => {
    const f = stubFetch({
      "POST /v1/staff/refresh": staffSession(),
      "GET /v1/staff/sessions": [
        200,
        {
          sessions: [
            {
              id: "a",
              signed_in_at: "2026-11-08T08:00:00Z",
              last_active_at: "2026-11-08T08:10:00Z",
              ip: "198.51.100.7",
              user_agent: "Firefox on macOS",
              current: true,
            },
            {
              id: "b",
              signed_in_at: "2026-11-07T08:00:00Z",
              last_active_at: "2026-11-07T09:00:00Z",
              ip: "198.51.100.8",
              user_agent: "Safari on iPad",
              current: false,
            },
          ],
        },
      ],
      "POST /v1/staff/sessions/revoke-all": () => new Response(null, { status: 204 }),
    });
    renderAt("/sessions");
    expect(await screen.findByText("Safari on iPad")).toBeInTheDocument();
    expect(screen.getByText(/This device/)).toBeInTheDocument();
    const u = userEvent.setup();
    await u.click(screen.getByRole("button", { name: "Sign out everywhere" }));
    const dialog = await screen.findByRole("dialog");
    await u.click(within(dialog).getByRole("button", { name: "Sign out everywhere" }));
    expect(await screen.findByRole("heading", { name: "Staff sign-in" })).toBeInTheDocument();
    expect(f.find("POST", "/v1/staff/sessions/revoke-all")).toHaveLength(1);
  });

  it("shows the staff sign-ins: the audit log filtered to them", async () => {
    const f = stubFetch({
      "POST /v1/staff/refresh": staffSession(),
      "GET /v1/admin/audit": [
        200,
        {
          entries: [
            {
              id: "1",
              actor_kind: "staff",
              actor_email: "ops@example.com",
              action: "staff.signin.mfa_ok",
              detail: null,
              ip: "198.51.100.7",
              created_at: "2026-11-08T08:00:00Z",
            },
          ],
          total: 1,
        },
      ],
    });
    renderAt("/signins");
    expect(await screen.findByRole("heading", { name: "Staff sign-ins" })).toBeInTheDocument();
    expect(await screen.findByText("staff.signin.mfa_ok")).toBeInTheDocument();
    const q = new URLSearchParams(f.find("GET", "/v1/admin/audit")[0].path.split("?")[1]);
    expect(q.get("actor_kind")).toBe("staff");
    expect(q.get("action")).toBe("staff.signin.");
  });
});

describe("T14: enrolment", () => {
  const STARTED = {
    email: "ops@example.com",
    totp_secret: "JBSWY3DPEHPK3PXPJBSWY3DP",
    totp_uri: "otpauth://totp/Maktaba%20staff:ops@example.com?secret=JBSWY3DPEHPK3PXPJBSWY3DP",
  };
  const CODES = Array.from({ length: 10 }, (_, i) => `code-${i + 1}`);

  it("groups the key for typing", () => {
    expect(groupSecret("JBSWY3DPEHPK3PXP")).toBe("JBSW Y3DP EHPK 3PXP");
    expect(groupSecret("ABCDEF")).toBe("ABCD EF");
  });

  it("starts from the fragment's token, sets the password and code, and shows 10 codes once", async () => {
    const f = stubFetch({
      "POST /v1/staff/refresh": staffSignedOut,
      "POST /v1/staff/enroll/start": [200, STARTED],
      "POST /v1/staff/enroll/finish": [200, { recovery_codes: CODES }],
    });
    renderAt("/enroll#tok-abc");
    expect(
      await screen.findByRole("heading", { name: "Set up your staff account" })
    ).toBeInTheDocument();
    expect(await screen.findByText(/Setting up ops@example.com/)).toBeInTheDocument();
    expect(screen.getByText("Scan this code with your authenticator app")).toBeInTheDocument();
    expect(screen.getByTestId("totp-secret")).toHaveTextContent("JBSW Y3DP EHPK 3PXP JBSW Y3DP");
    expect(screen.getByRole("link", { name: "Open in your authenticator app" })).toHaveAttribute(
      "href",
      STARTED.totp_uri
    );
    expect(f.find("POST", "/v1/staff/enroll/start")[0].body).toEqual({ token: "tok-abc" });

    const u = userEvent.setup();
    // the API's rule, checked first: 14 characters at least
    await u.type(screen.getByLabelText("Password (14 to 256 characters)"), "too short");
    await u.type(screen.getByLabelText("Code from the app"), "123456");
    await u.click(screen.getByRole("button", { name: "Finish setup" }));
    expect(await screen.findByText("Use at least 14 characters.")).toBeInTheDocument();
    expect(f.find("POST", "/v1/staff/enroll/finish")).toEqual([]);

    await u.type(screen.getByLabelText("Password (14 to 256 characters)"), " but longer now");
    await u.click(screen.getByRole("button", { name: "Finish setup" }));
    expect(
      await screen.findByText("Keep these recovery codes somewhere safe. Each one works once.")
    ).toBeInTheDocument();
    const list = screen.getByRole("list", { name: "Recovery code" });
    expect(within(list).getAllByRole("listitem")).toHaveLength(10);
    expect(within(list).getByText("code-10")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Go to sign-in" })).toHaveAttribute("href", "/");
    expect(f.find("POST", "/v1/staff/enroll/finish")[0].body).toEqual({
      token: "tok-abc",
      password: "too short but longer now",
      code: "123456",
    });
    // the token goes only in the POST bodies, never in a URL
    expect(f.calls.filter((c) => c.path.includes("tok-abc"))).toEqual([]);
  });

  it("says an expired or used link", async () => {
    stubFetch({
      "POST /v1/staff/refresh": staffSignedOut,
      "POST /v1/staff/enroll/start": [410, { error: "enrolment_link_expired" }],
    });
    renderAt("/enroll#old");
    expect(
      await screen.findByText("This enrolment link has expired or was already used.")
    ).toBeInTheDocument();
  });

  it("says a link with no token is no good, without calling the API", async () => {
    const f = stubFetch({ "POST /v1/staff/refresh": staffSignedOut });
    renderAt("/enroll");
    expect(
      await screen.findByText("This enrolment link has expired or was already used.")
    ).toBeInTheDocument();
    expect(f.find("POST", "/v1/staff/enroll/start")).toEqual([]);
  });

  it("says a refused password or code", async () => {
    stubFetch({
      "POST /v1/staff/refresh": staffSignedOut,
      "POST /v1/staff/enroll/start": [200, STARTED],
      "POST /v1/staff/enroll/finish": (_c, n) =>
        n === 1 ? json(400, { error: "password_breached" }) : json(400, { error: "mfa_failed" }),
    });
    renderAt("/enroll#tok");
    const u = userEvent.setup();
    await u.type(
      await screen.findByLabelText("Password (14 to 256 characters)"),
      "password1234567"
    );
    await u.type(screen.getByLabelText("Code from the app"), "123456");
    await u.click(screen.getByRole("button", { name: "Finish setup" }));
    expect(
      await screen.findByText("That password has appeared in a data breach. Choose another.")
    ).toBeInTheDocument();
    await u.click(screen.getByRole("button", { name: "Finish setup" }));
    expect(await screen.findByText("That code didn't work. Try again.")).toBeInTheDocument();
  });

  it("is right to left in Arabic", async () => {
    localStorage.setItem("mkt:cloud:locale", "ar");
    stubFetch({
      "POST /v1/staff/refresh": staffSignedOut,
      "POST /v1/staff/enroll/start": [200, STARTED],
      "POST /v1/staff/enroll/finish": [200, { recovery_codes: CODES }],
    });
    renderAt("/enroll#tok");
    expect(await screen.findByRole("heading", { name: "إعداد حساب الفريق" })).toBeInTheDocument();
    await waitFor(() => expect(document.documentElement.dir).toBe("rtl"));
    expect(await screen.findByText("امسح هذا الرمز بتطبيق المصادقة")).toBeInTheDocument();
    // the key stays left to right inside the Arabic page
    expect(screen.getByTestId("totp-secret")).toHaveAttribute("dir", "ltr");
    const u = userEvent.setup();
    await u.type(
      screen.getByLabelText("كلمة المرور (من 14 إلى 256 حرفًا)"),
      "a long enough password"
    );
    await u.type(screen.getByLabelText("الرمز من التطبيق"), "123456");
    await u.click(screen.getByRole("button", { name: "إنهاء الإعداد" }));
    expect(
      await screen.findByText("احفظ رموز الاسترداد هذه في مكان آمن. كل رمز يعمل مرة واحدة.")
    ).toBeInTheDocument();
  });
});
