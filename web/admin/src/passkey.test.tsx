// Story 50.33 part 2: passkeys in the console. The browser helpers turn the API's JSON options
// into navigator.credentials calls and the answers back into JSON; sign-in offers the passkey
// first when the account has one, enrolment can finish with a passkey and no app code, and the
// re-check dialog asks for the passkey of a passkey-only account. In English and Arabic.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { setStaffToken } from "./api";
import {
  createPasskey,
  fromB64url,
  getPasskey,
  PasskeyCancelled,
  passkeysSupported,
  toB64url,
} from "./passkey";
import { firstMode } from "./pages/StaffSignIn";
import { json, renderAt, staffSession, staffSignedOut, stubFetch } from "./test/harness";

const bytes = (...b: number[]) => new Uint8Array(b).buffer;

/** A browser with passkeys: navigator.credentials answers with fixed bytes. */
function stubPasskeys(opts: { refuse?: boolean } = {}) {
  vi.stubGlobal("PublicKeyCredential", class {});
  const create = vi.fn(async () => {
    if (opts.refuse) throw new DOMException("closed", "NotAllowedError");
    return {
      id: "AQID",
      rawId: bytes(1, 2, 3),
      type: "public-key",
      authenticatorAttachment: "platform",
      getClientExtensionResults: () => ({}),
      response: {
        clientDataJSON: bytes(4),
        attestationObject: bytes(5, 6),
        getTransports: () => ["internal"],
      },
    };
  });
  const get = vi.fn(async () => {
    if (opts.refuse) throw new DOMException("closed", "NotAllowedError");
    return {
      id: "AQID",
      rawId: bytes(1, 2, 3),
      type: "public-key",
      authenticatorAttachment: "platform",
      getClientExtensionResults: () => ({}),
      response: {
        clientDataJSON: bytes(7),
        authenticatorData: bytes(8),
        signature: bytes(9),
        userHandle: bytes(10),
      },
    };
  });
  Object.defineProperty(navigator, "credentials", {
    value: { create, get },
    configurable: true,
  });
  return { create, get };
}

const ASSERT_OPTS = {
  publicKey: {
    challenge: "AAEC",
    rpId: "admin.maktaba.tv",
    userVerification: "required",
    allowCredentials: [{ id: "AQID", type: "public-key" }],
  },
};
const ANSWER = {
  id: "AQID",
  rawId: "AQID",
  type: "public-key",
  authenticatorAttachment: "platform",
  clientExtensionResults: {},
  response: { clientDataJSON: "Bw", authenticatorData: "CA", signature: "CQ", userHandle: "Cg" },
};

beforeEach(() => setStaffToken(null));
afterEach(() => {
  vi.unstubAllGlobals();
  Reflect.deleteProperty(navigator, "credentials");
  localStorage.clear();
});

describe("the browser helpers", () => {
  it("round-trips base64url, padding and URL-safe letters included", () => {
    for (const b of [[], [0], [251, 255], [1, 2, 3, 4, 5]]) {
      const s = toB64url(new Uint8Array(b));
      expect(s).not.toMatch(/[+/=]/);
      expect([...new Uint8Array(fromB64url(s))]).toEqual(b);
    }
    expect(toB64url(new Uint8Array([251, 255]))).toBe("-_8");
  });

  it("knows a browser without passkeys", () => {
    expect(passkeysSupported()).toBe(false);
    stubPasskeys();
    expect(passkeysSupported()).toBe(true);
  });

  it("turns the options into bytes and the answers into JSON", async () => {
    const { create, get } = stubPasskeys();
    const made = await createPasskey({
      publicKey: {
        challenge: "AAEC",
        rp: { id: "admin.maktaba.tv", name: "Maktaba console" },
        user: { id: "dXNlcg", name: "ops@example.com", displayName: "ops@example.com" },
        excludeCredentials: [{ id: "AQID", type: "public-key" }],
      },
    });
    const sent = (create.mock.calls[0] as unknown as [CredentialCreationOptions])[0].publicKey!;
    expect([...new Uint8Array(sent.challenge as ArrayBuffer)]).toEqual([0, 1, 2]);
    expect(new TextDecoder().decode(sent.user.id as ArrayBuffer)).toBe("user");
    expect([...new Uint8Array(sent.excludeCredentials![0].id as ArrayBuffer)]).toEqual([1, 2, 3]);
    expect(made).toEqual({
      id: "AQID",
      rawId: "AQID",
      type: "public-key",
      authenticatorAttachment: "platform",
      clientExtensionResults: {},
      response: { clientDataJSON: "BA", attestationObject: "BQY", transports: ["internal"] },
    });
    expect(await getPasskey(ASSERT_OPTS)).toEqual(ANSWER);
    const asked = (get.mock.calls[0] as unknown as [CredentialRequestOptions])[0].publicKey!;
    expect(asked.userVerification).toBe("required");
    expect([...new Uint8Array(asked.allowCredentials![0].id as ArrayBuffer)]).toEqual([1, 2, 3]);
  });

  it("tells a closed prompt apart", async () => {
    stubPasskeys({ refuse: true });
    await expect(getPasskey(ASSERT_OPTS)).rejects.toBeInstanceOf(PasskeyCancelled);
  });

  it("offers the passkey first when there is one and the browser has passkeys", () => {
    expect(firstMode(["passkey", "totp", "recovery"], true)).toBe("passkey");
    expect(firstMode(["passkey", "totp", "recovery"], false)).toBe("totp");
    expect(firstMode(["passkey", "recovery"], false)).toBe("recovery");
    expect(firstMode(["totp", "recovery"], true)).toBe("totp");
  });
});

async function signInTo(u: ReturnType<typeof userEvent.setup>, continueLabel = "Continue") {
  await u.type(await screen.findByLabelText(/Email|البريد الإلكتروني/), "ops@example.com");
  await u.type(screen.getByLabelText(/Password|كلمة المرور/), "correct horse battery");
  await u.click(screen.getByRole("button", { name: continueLabel }));
}

describe("sign-in with a passkey", () => {
  const LOGIN: [number, unknown] = [
    200,
    { mfa_token: "m1", methods: ["passkey", "totp", "recovery"] },
  ];

  it("asks for the passkey first and signs in with its answer", async () => {
    stubPasskeys();
    const f = stubFetch({
      "POST /v1/staff/refresh": staffSignedOut,
      "POST /v1/staff/login": LOGIN,
      "POST /v1/staff/mfa/passkey/begin": [200, ASSERT_OPTS],
      "POST /v1/staff/mfa/passkey/finish": staffSession(),
      "GET /v1/staff/sessions": [200, { sessions: [] }],
    });
    renderAt("/sessions");
    const u = userEvent.setup();
    await signInTo(u);
    await u.click(await screen.findByRole("button", { name: "Use your passkey" }));
    expect(await screen.findByRole("heading", { name: "Signed-in devices" })).toBeInTheDocument();
    expect(f.find("POST", "/v1/staff/mfa/passkey/begin")[0].body).toEqual({ mfa_token: "m1" });
    expect(f.find("POST", "/v1/staff/mfa/passkey/finish")[0].body).toEqual({
      mfa_token: "m1",
      credential: ANSWER,
    });
  });

  it("says a closed prompt and offers the other ways", async () => {
    stubPasskeys({ refuse: true });
    stubFetch({
      "POST /v1/staff/refresh": staffSignedOut,
      "POST /v1/staff/login": LOGIN,
      "POST /v1/staff/mfa/passkey/begin": [200, ASSERT_OPTS],
    });
    renderAt("/");
    const u = userEvent.setup();
    await signInTo(u);
    await u.click(await screen.findByRole("button", { name: "Use your passkey" }));
    expect(
      await screen.findByText("The passkey check didn't finish. Try again, or use another way.")
    ).toBeInTheDocument();
    await u.click(screen.getByRole("button", { name: "Use your authenticator app instead" }));
    expect(
      screen.getByLabelText("Enter the 6-digit code from your authenticator app")
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Use your passkey" })).toBeInTheDocument();
  });

  it("says a wrong passkey", async () => {
    stubPasskeys();
    stubFetch({
      "POST /v1/staff/refresh": staffSignedOut,
      "POST /v1/staff/login": LOGIN,
      "POST /v1/staff/mfa/passkey/begin": [200, ASSERT_OPTS],
      "POST /v1/staff/mfa/passkey/finish": [401, { error: "mfa_failed" }],
    });
    renderAt("/");
    const u = userEvent.setup();
    await signInTo(u);
    await u.click(await screen.findByRole("button", { name: "Use your passkey" }));
    expect(await screen.findByText("That code didn't work. Try again.")).toBeInTheDocument();
  });

  it("falls back to the app's code in a browser without passkeys", async () => {
    stubFetch({ "POST /v1/staff/refresh": staffSignedOut, "POST /v1/staff/login": LOGIN });
    renderAt("/");
    const u = userEvent.setup();
    await signInTo(u);
    expect(
      await screen.findByLabelText("Enter the 6-digit code from your authenticator app")
    ).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Use your passkey" })).toBeNull();
  });

  it("is in Arabic, right to left", async () => {
    localStorage.setItem("mkt:cloud:locale", "ar");
    stubPasskeys();
    stubFetch({ "POST /v1/staff/refresh": staffSignedOut, "POST /v1/staff/login": LOGIN });
    renderAt("/");
    const u = userEvent.setup();
    await signInTo(u, "متابعة");
    expect(await screen.findByRole("button", { name: "استخدم مفتاح المرور" })).toBeInTheDocument();
    await waitFor(() => expect(document.documentElement.dir).toBe("rtl"));
    expect(
      screen.getByRole("button", { name: "استخدم تطبيق المصادقة بدلًا من ذلك" })
    ).toBeInTheDocument();
  });
});

describe("enrolment with a passkey", () => {
  const STARTED = {
    email: "ops@example.com",
    totp_secret: "JBSWY3DPEHPK3PXP",
    totp_uri: "otpauth://totp/x",
  };
  const CREATE_OPTS = {
    publicKey: {
      challenge: "AAEC",
      rp: { id: "admin.maktaba.tv", name: "Maktaba console" },
      user: { id: "dXNlcg", name: "ops@example.com", displayName: "ops@example.com" },
    },
  };

  it("adds a passkey and finishes with no app code", async () => {
    stubPasskeys();
    const f = stubFetch({
      "POST /v1/staff/refresh": staffSignedOut,
      "POST /v1/staff/enroll/start": [200, STARTED],
      "POST /v1/staff/enroll/passkey/begin": [200, CREATE_OPTS],
      "POST /v1/staff/enroll/passkey/finish": [200, { name: "Passkey", passkeys: 1 }],
      "POST /v1/staff/enroll/finish": [200, { recovery_codes: ["c1"] }],
    });
    renderAt("/enroll#tok-abc");
    const u = userEvent.setup();
    expect(
      await screen.findByText(/Add a passkey, the authenticator app below/)
    ).toBeInTheDocument();
    // with no factor yet, the setup can't finish
    await u.type(
      screen.getByLabelText("Password (14 to 256 characters)"),
      "a long enough password"
    );
    expect(screen.getByRole("button", { name: "Finish setup" })).toBeDisabled();
    await u.click(screen.getByRole("button", { name: "Add a passkey" }));
    expect(
      await screen.findByText(
        "Passkey added (1 in all). You can add the authenticator app too, or finish now."
      )
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Add another passkey" })).toBeInTheDocument();
    expect(
      screen.getByLabelText("Code from the app (optional with a passkey)")
    ).toBeInTheDocument();
    await u.click(screen.getByRole("button", { name: "Finish setup" }));
    expect(await screen.findByText("Your staff account is ready.")).toBeInTheDocument();
    expect(f.find("POST", "/v1/staff/enroll/passkey/begin")[0].body).toEqual({ token: "tok-abc" });
    expect(f.find("POST", "/v1/staff/enroll/passkey/finish")[0].body).toEqual({
      token: "tok-abc",
      credential: {
        id: "AQID",
        rawId: "AQID",
        type: "public-key",
        authenticatorAttachment: "platform",
        clientExtensionResults: {},
        response: { clientDataJSON: "BA", attestationObject: "BQY", transports: ["internal"] },
      },
    });
    expect(f.find("POST", "/v1/staff/enroll/finish")[0].body).toEqual({
      token: "tok-abc",
      password: "a long enough password",
      code: "",
    });
  });

  it("says a passkey already added, and a browser without passkeys", async () => {
    stubPasskeys();
    stubFetch({
      "POST /v1/staff/refresh": staffSignedOut,
      "POST /v1/staff/enroll/start": [200, STARTED],
      "POST /v1/staff/enroll/passkey/begin": [200, CREATE_OPTS],
      "POST /v1/staff/enroll/passkey/finish": [409, { error: "passkey_exists" }],
    });
    const view = renderAt("/enroll#tok-abc");
    const u = userEvent.setup();
    await u.click(await screen.findByRole("button", { name: "Add a passkey" }));
    expect(await screen.findByText("This passkey is already added.")).toBeInTheDocument();
    view.unmount();

    vi.unstubAllGlobals();
    Reflect.deleteProperty(navigator, "credentials");
    stubFetch({
      "POST /v1/staff/refresh": staffSignedOut,
      "POST /v1/staff/enroll/start": [200, STARTED],
    });
    renderAt("/enroll#tok-xyz");
    expect(
      await screen.findByText("This browser can't use passkeys. Use the authenticator app instead.")
    ).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Add a passkey" })).toBeNull();
  });

  it("is in Arabic", async () => {
    localStorage.setItem("mkt:cloud:locale", "ar");
    stubPasskeys();
    stubFetch({
      "POST /v1/staff/refresh": staffSignedOut,
      "POST /v1/staff/enroll/start": [200, STARTED],
    });
    renderAt("/enroll#tok-abc");
    expect(await screen.findByRole("button", { name: "أضف مفتاح مرور" })).toBeInTheDocument();
    expect(
      screen.getByText(/أضف مفتاح مرور أو تطبيق المصادقة أدناه أو كليهما/)
    ).toBeInTheDocument();
  });
});

describe("the re-check with a passkey", () => {
  it("asks a passkey-only account for its passkey, then sends the action again", async () => {
    stubPasskeys();
    let status = "active";
    const f = stubFetch({
      "POST /v1/staff/refresh": staffSession(),
      "GET /v1/staff/me": [200, { methods: ["passkey", "recovery"] }],
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
      "POST /v1/staff/reauth/passkey/begin": [200, ASSERT_OPTS],
      "POST /v1/staff/reauth/passkey/finish": () => {
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
    const key = await within(check).findByRole("button", { name: "Use your passkey" });
    // a passkey-only account has no app code to type
    expect(
      within(check).queryByLabelText("Enter the 6-digit code from your authenticator app")
    ).toBeNull();
    await u.click(key);
    expect(await screen.findByRole("button", { name: "Unsuspend" })).toBeInTheDocument();
    expect(f.find("POST", "/v1/staff/reauth/passkey/finish")[0].body).toEqual({
      credential: ANSWER,
    });
    const sent = f.find("POST", "/v1/admin/accounts/u2/suspend");
    expect(sent).toHaveLength(2);
    expect(sent[1].headers.Authorization).toBe("Bearer staff-tok-2");
  });
});
