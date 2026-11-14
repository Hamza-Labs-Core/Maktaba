// Epic 58 story 58.09's second part — /tv/ signed in by a cloud code's grant the launcher
// hands over (maktaba:signIn): redeemed at the home's token route in its own origin, the
// tokens kept by the TV's session; a refused grant falls back to the home's own code, and the
// bundled copy (which cannot start the home's code) sends the person to the launcher's code.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { I18nProvider } from "../lib/i18n";
import { TestAuth } from "../test/pageHarness";
import en from "../i18n/en.json";
import ar from "../i18n/ar.json";
import { installLauncherMode, TvGate } from "./TvApp";
import { LauncherContext, type LauncherMode } from "./launcher";
import { TvSession } from "./session";
import { resetBridgeForTest, startBridge, type UiMessage } from "../bigscreen/input/bridge";

const E = en as Record<string, string>;
const A = ar as Record<string, string>;
const GRANT = "Gr4nt_" + "x".repeat(37);

let parent: Window;
let frame: HTMLIFrameElement;
let sent: UiMessage[];

const hello = (re: number, signIn: boolean) => ({
  type: "maktaba:hello",
  re,
  bridge_level: 1,
  platform: "webos",
  model: "OLED55CX",
  year: 2020,
  engine: "Chromium 79",
  version: "1.0.0",
  device_caps: {
    containers: ["mp4"],
    video: [{ codec: "h264" }],
    hdr: [],
    audio: ["aac"],
    passthrough: [],
    hls_segments: ["ts"],
  },
  keys: [],
  messages: ["openHomePicker"],
  ...(signIn ? { sign_in: true } : {}),
});

function fromParent(data: unknown, origin = "null") {
  window.dispatchEvent(new MessageEvent("message", { data, origin, source: parent }));
}

/** The launcher answered hello (announcing a grant or not), then sent the grant. */
async function launcher(signIn: boolean) {
  const started = startBridge(1000);
  const ask = sent.find((m) => m.type === "maktaba:hello") as Extract<
    UiMessage,
    { type: "maktaba:hello" }
  >;
  fromParent(hello(ask.id, signIn));
  await started;
  if (signIn) fromParent({ type: "maktaba:signIn", grant: GRANT });
}

function renderGate(mode: LauncherMode, session: TvSession) {
  return render(
    <I18nProvider>
      <LauncherContext.Provider value={mode}>
        <TestAuth user={null}>
          <MemoryRouter>
            <TvGate session={session} />
          </MemoryRouter>
        </TestAuth>
      </LauncherContext.Provider>
    </I18nProvider>
  );
}

const MODE: LauncherMode = { bundled: false, base: null, plain: false, lang: null };
const BUNDLED: LauncherMode = {
  bundled: true,
  base: "https://10-0-0-7.h1.direct-a.example:8443",
  plain: false,
  lang: null,
};

beforeEach(() => {
  resetBridgeForTest();
  localStorage.clear();
  localStorage.setItem("mkt:locale", "en");
  frame = document.createElement("iframe");
  document.body.appendChild(frame);
  parent = frame.contentWindow!;
  sent = [];
  vi.spyOn(parent, "postMessage").mockImplementation((m: unknown) => {
    sent.push(m as UiMessage);
  });
  Object.defineProperty(window, "parent", { value: parent, configurable: true });
});

afterEach(() => {
  Object.defineProperty(window, "parent", { value: window, configurable: true });
  frame.remove();
  resetBridgeForTest();
  installLauncherMode(null);
  localStorage.clear();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

function api(token: (body: Record<string, unknown>) => Response) {
  const calls: { url: string; body: Record<string, unknown> }[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init?: RequestInit) => {
      const body = init?.body ? (JSON.parse(String(init.body)) as Record<string, unknown>) : {};
      calls.push({ url: String(url), body });
      if (String(url).endsWith("/api/pairing/device/token")) return token(body);
      if (String(url).endsWith("/api/pairing/device"))
        return new Response(
          JSON.stringify({
            code: "KQ7M-4R2P",
            device_code: "dc",
            expires_at: new Date(Date.now() + 600_000).toISOString(),
            verify_path: "/pair",
            verify_url: "",
            interval: 60,
          }),
          { status: 200, headers: { "Content-Type": "application/json" } }
        );
      return new Response("{}", { status: 404, headers: { "Content-Type": "application/json" } });
    })
  );
  return calls;
}

const tokens = () =>
  new Response(
    JSON.stringify({
      access_token: "a1",
      access_expires_in: 900,
      refresh_token: "r1",
      refresh_expires_in: 2592000,
    }),
    { status: 200, headers: { "Content-Type": "application/json" } }
  );

describe("signing in with the launcher's grant", () => {
  it("redeems it at the home's token route, keeps the tokens, and never starts a home code", async () => {
    const calls = api(() => tokens());
    await launcher(true);
    const session = installLauncherMode(MODE)!;
    renderGate(MODE, session);
    expect(screen.getByTestId("tv-grant-signin")).toHaveTextContent(E["tv.code.signingIn"]!);
    await waitFor(() => expect(session.signedIn()).toBe(true));
    // the first call is the redeem: no home code was asked for before it
    expect(calls[0]!.url).toMatch(/\/api\/pairing\/device\/token$/);
    expect(calls[0]!.body).toEqual({ device_code: GRANT, device_label: "OLED55CX" });
    // the grant is nowhere a page or a later run could read it
    expect(JSON.stringify(localStorage)).not.toContain(GRANT);
    expect(calls.every((c) => !c.url.includes(GRANT))).toBe(true);
  });

  it("a refused grant: the home's own code, saying the approval could not be used", async () => {
    api(
      () =>
        new Response(JSON.stringify({ title: "Gone", status: 410 }), {
          status: 410,
          headers: { "Content-Type": "application/problem+json" },
        })
    );
    await launcher(true);
    const session = installLauncherMode(MODE)!;
    renderGate(MODE, session);
    expect(await screen.findByTestId("tv-code-notice")).toHaveTextContent(
      E["tv.code.grantFailed"]!
    );
    expect(await screen.findByTestId("tv-code")).toHaveTextContent("KQ7M-4R2P");
    expect(session.signedIn()).toBe(false);
  });

  it("no grant announced: the home's own code at once", async () => {
    const calls = api(() => tokens());
    await launcher(false);
    const session = installLauncherMode(MODE)!;
    renderGate(MODE, session);
    expect(await screen.findByTestId("tv-code-signin")).toBeInTheDocument();
    expect(calls.some((c) => c.url.endsWith("/api/pairing/device/token"))).toBe(false);
  });

  it("bundled: redeems the grant at the home's address", async () => {
    const calls = api(() => tokens());
    await launcher(true);
    const session = installLauncherMode(BUNDLED)!;
    renderGate(BUNDLED, session);
    await waitFor(() => expect(session.signedIn()).toBe(true));
    expect(calls[0]!.url).toBe(`${BUNDLED.base}/api/pairing/device/token`);
  });

  it("bundled with no grant: back to the launcher's code, in Arabic right to left", async () => {
    localStorage.setItem("mkt:locale", "ar");
    const calls = api(() => tokens());
    await launcher(false);
    const session = installLauncherMode(BUNDLED)!;
    renderGate(BUNDLED, session);
    const page = await screen.findByTestId("tv-bundled-signin");
    expect(page).toHaveAttribute("dir", "rtl");
    expect(page).toHaveTextContent(A["tv.code.bundled"]!);
    // the bundled copy never asks the home for a code (its origin is not answered there)
    expect(calls.some((c) => c.url.endsWith("/api/pairing/device"))).toBe(false);
    const go = screen.getByTestId("tv-bundled-go");
    expect(document.activeElement).toBe(go);
    fireEvent.click(go);
    expect(sent.map((m) => m.type)).toContain("maktaba:openHomePicker");
  });
});
