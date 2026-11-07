// Story 54.15 T06: the console, /link and the account pages in English and Arabic: every
// string in both, right to left in Arabic, the dark theme, and the words the ACs name.
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "../src/App";
import { WORDS } from "../src/i18n";

const ME = {
  id: "a_1",
  email: "dev@example.com",
  email_verified: true,
  display_name: "Dev",
  locale: "en",
  roles: ["buyer", "developer"],
  factors: [{ id: "f_1", kind: "totp", name: "Authenticator", added: "2026-11-01T00:00:00Z", last_used: null }],
  sessions: [{ id: "s1", created: "2026-11-01T00:00:00Z", last_seen: "2026-11-06T00:00:00Z", device: "Firefox on Linux", current: true }],
};

function server(routes: Record<string, (body: unknown) => [number, unknown]>) {
  return vi.fn(async (path: string, init?: RequestInit) => {
    const key = `${init?.method ?? "GET"} ${path}`;
    const h = routes[key];
    const [status, body] = h ? h(init?.body ? JSON.parse(String(init.body)) : undefined) : [404, { error: "not_found" }];
    return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
  });
}

const DEV = {
  developer: { agreement_version: 1, status: "active" },
  agreement_current: 1,
  keys: [],
  prefixes: [],
  plugins: [],
  submissions: [],
};

beforeEach(() => {
  // a storage of its own (Node 22+ has a global localStorage of its own that shadows jsdom's)
  const m = new Map<string, string>();
  vi.stubGlobal("localStorage", {
    getItem: (k: string) => m.get(k) ?? null,
    setItem: (k: string, v: string) => void m.set(k, v),
    removeItem: (k: string) => void m.delete(k),
    clear: () => m.clear(),
  });
  delete document.documentElement.dataset.theme;
});
afterEach(cleanup);

describe("the console's words", () => {
  it("every key has English and Arabic, and none is empty", () => {
    expect(Object.keys(WORDS.ar).sort()).toEqual(Object.keys(WORDS.en).sort());
    for (const [k, v] of Object.entries(WORDS.ar)) expect(v.trim(), k).not.toBe("");
  });

  it("the AC's words: two-step verification and Confirm it's you", () => {
    expect(WORDS.ar["error.mfa_required"]).toBe("يلزم التحقق بخطوتين");
    expect(WORDS.ar["reauth.title"]).toBe("أكّد أنّك أنت");
    expect(WORDS.en["reauth.title"]).toBe("Confirm it's you");
    expect(WORDS.ar["signup.check"]).toBe("تحقّق من بريدك الإلكتروني لإكمال التسجيل");
  });
});

describe("pages", () => {
  it("signed out: the sign-in page; Arabic turns the page right to left", async () => {
    vi.stubGlobal("fetch", server({ "GET /v1/me": () => [401, { error: "signed_out" }] }));
    render(<App path="/" />);
    expect(await screen.findByRole("heading", { name: WORDS.en["signin.title"] })).toBeTruthy();
    expect(document.documentElement.dir).toBe("ltr");
    await act(async () => fireEvent.click(screen.getByRole("button", { name: "العربية" })));
    expect(await screen.findByRole("heading", { name: WORDS.ar["signin.title"] })).toBeTruthy();
    expect(document.documentElement.dir).toBe("rtl");
    expect(document.documentElement.lang).toBe("ar");
  });

  for (const lang of ["en", "ar"] as const) {
    it(`${lang}: the account, developer and link pages, in the dark theme`, async () => {
      localStorage.setItem("mks:lang", lang);
      vi.stubGlobal(
        "fetch",
        server({ "GET /v1/me": () => [200, ME], "GET /v1/homes": () => [200, { homes: [] }], "GET /v1/developers/me": () => [200, DEV] }),
      );
      const { unmount } = render(<App path="/account" />);
      expect(await screen.findByRole("heading", { name: WORDS[lang]["account.title"] })).toBeTruthy();
      expect(screen.getByRole("button", { name: WORDS[lang]["account.signout.everywhere"] })).toBeTruthy();
      await act(async () => fireEvent.click(screen.getByRole("button", { name: WORDS[lang]["theme.toggle"] })));
      expect(document.documentElement.dataset.theme).toBe("dark");
      unmount();
      render(<App path="/developers" />);
      expect(await screen.findByRole("heading", { name: WORDS[lang]["dev.title"] })).toBeTruthy();
      expect(screen.getByText(WORDS[lang]["dev.keys"])).toBeTruthy();
      cleanup();
      render(<App path="/link" />);
      expect(await screen.findByRole("heading", { name: WORDS[lang]["link.title"] })).toBeTruthy();
      expect(document.documentElement.dir).toBe(lang === "ar" ? "rtl" : "ltr");
    });
  }

  it("an action answered 401 reauth_required asks to confirm it's you, then runs again", async () => {
    let confirmed = false;
    vi.stubGlobal(
      "fetch",
      server({
        "GET /v1/me": () => [200, ME],
        "GET /v1/homes": () => [200, { homes: [] }],
        "POST /v1/mfa/totp/begin": () =>
          confirmed ? [200, { setup_id: "c1", secret: "ABC", otpauth: "otpauth://x" }] : [401, { error: "reauth_required" }],
        "POST /v1/reauth": () => ((confirmed = true), [200, { status: "confirmed" }]),
      }),
    );
    render(<App path="/account" />);
    const add = await screen.findByRole("button", { name: WORDS.en["account.totp.add"] });
    await act(async () => fireEvent.click(add));
    expect(await screen.findByRole("dialog", { name: WORDS.en["reauth.title"] })).toBeTruthy();
    fireEvent.change(screen.getByLabelText(WORDS.en["reauth.code"]), { target: { value: "123456" } });
    await act(async () => fireEvent.click(screen.getByRole("button", { name: WORDS.en["reauth.confirm"] })));
    expect(await screen.findByText("ABC")).toBeTruthy();
  });
});
