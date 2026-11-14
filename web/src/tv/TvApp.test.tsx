// Epic 58 story 58.08 — the TV build's gate: signed out, a TV-sized page sends
// the person to the home's sign-in, which brings them back to /tv/.
import { afterEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { I18nProvider } from "../lib/i18n";
import { TestAuth } from "../test/pageHarness";
import en from "../i18n/en.json";
import ar from "../i18n/ar.json";
import { installLauncherMode, launcherClient, launcherMode, TV_SIGN_IN, TvGate } from "./TvApp";
import { TvSession } from "./session";
import { bearerAuth } from "../lib/api";

const E = en as Record<string, string>;
const A = ar as Record<string, string>;

function renderGate(session: TvSession | null = null) {
  return render(
    <I18nProvider>
      <TestAuth user={null}>
        <MemoryRouter>
          <TvGate session={session} />
        </MemoryRouter>
      </TestAuth>
    </I18nProvider>
  );
}

afterEach(() => {
  installLauncherMode(null);
  localStorage.clear();
  vi.unstubAllGlobals();
});

describe("TvGate", () => {
  it("signed out: the sign-in page, its only button focused, going to the home's sign-in", () => {
    localStorage.setItem("mkt:locale", "en");
    const assign = vi.fn();
    vi.stubGlobal("location", { ...window.location, assign });
    renderGate();
    expect(screen.getByTestId("tv-signin")).toHaveAttribute("dir", "ltr");
    expect(screen.getByRole("heading", { name: E["tv.signIn.title"] })).toBeInTheDocument();
    const go = screen.getByRole("button", { name: E["tv.signIn.go"] });
    expect(document.activeElement).toBe(go);
    fireEvent.click(go);
    expect(assign).toHaveBeenCalledWith(TV_SIGN_IN);
    expect(TV_SIGN_IN).toBe("/login?next=%2Ftv%2F");
  });

  it("in Arabic, right to left", () => {
    localStorage.setItem("mkt:locale", "ar");
    renderGate();
    expect(screen.getByTestId("tv-signin")).toHaveAttribute("dir", "rtl");
    expect(screen.getByRole("heading", { name: A["tv.signIn.title"] })).toBeInTheDocument();
    expect(screen.getByText(A["tv.signIn.body"]!)).toBeInTheDocument();
  });
});

// Epic 58 story 58.09: /tv/ in the Samsung or LG launcher
describe("the launcher mode", () => {
  const loc = (href: string) => new URL(href) as unknown as Location;
  const framed = () =>
    Object.defineProperty(window, "parent", { value: {} as Window, configurable: true });
  afterEach(() => Object.defineProperty(window, "parent", { value: window, configurable: true }));

  it("only with ?bridge=1 inside a frame", () => {
    expect(launcherMode(loc("https://h.example/tv/?bridge=1"))).toBeNull();
    framed();
    expect(launcherMode(loc("https://h.example/tv/"))).toBeNull();
    expect(launcherMode(loc("https://h.example/tv/?bridge=1&lang=ar"))).toEqual({
      bundled: false,
      base: null,
      plain: false,
      lang: "ar",
    });
    expect(launcherMode(loc("http://10.0.0.7:8088/tv/?bridge=1&lang=xx"))).toMatchObject({
      plain: true,
      lang: null,
    });
  });

  it("bundled: the home's address from ?home=, only http(s)", () => {
    framed();
    expect(
      launcherMode(loc("file:///app/ui/index.html?bridge=1&home=http%3A%2F%2F10.0.0.7%3A8088%2F"))
    ).toEqual({ bundled: true, base: "http://10.0.0.7:8088", plain: true, lang: null });
    expect(
      launcherMode(loc("file:///app/ui/index.html?bridge=1&home=javascript%3Aalert(1)"))
    ).toMatchObject({ bundled: false, base: null });
  });

  it("signs in by bearer: the session is the API client's", () => {
    const s = installLauncherMode({ bundled: false, base: null, plain: false, lang: null });
    expect(s).toBeInstanceOf(TvSession);
    expect(bearerAuth()).toBe(s);
    installLauncherMode(null);
    expect(bearerAuth()).toBeNull();
  });

  it("names the TV app in the client header", () => {
    expect(launcherClient("1.2.3", "tizen")).toBe("tv-web/1.2.3 (tizen; tv)");
    expect(launcherClient("", "webos")).toBe("tv-web/dev (webos; tv)");
  });

  it("signed out in the launcher: the code sign-in, never the password page", async () => {
    localStorage.setItem("mkt:locale", "en");
    vi.stubGlobal(
      "fetch",
      vi.fn(
        async () =>
          new Response("{}", { status: 500, headers: { "Content-Type": "application/json" } })
      )
    );
    renderGate(new TvSession());
    expect(await screen.findByTestId("tv-code-signin")).toBeInTheDocument();
    expect(screen.queryByTestId("tv-signin")).toBeNull();
  });
});
