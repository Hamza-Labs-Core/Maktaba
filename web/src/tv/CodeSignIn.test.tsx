// Epic 58 story 58.09 — signing a TV in by the home's code inside the launcher: the code
// large, the approve page and its QR code, waiting until approved, the tokens kept, a new
// code when one runs out, and the plain-HTTP line in English and Arabic.
import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { I18nProvider } from "../lib/i18n";
import en from "../i18n/en.json";
import ar from "../i18n/ar.json";
import { approvePage, CodeSignIn } from "./CodeSignIn";
import { TvSession } from "./session";

const E = en as Record<string, string>;
const A = ar as Record<string, string>;

const start = (code: string) => ({
  code,
  device_code: `dc-${code}`,
  expires_at: new Date(Date.now() + 10 * 60_000).toISOString(),
  verify_path: "/pair",
  verify_url: `https://home.example/pair?code=${code}`,
  interval: 1,
});
const json = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": status >= 400 ? "application/problem+json" : "application/json" },
  });

function renderSignIn(plain = false) {
  const session = new TvSession();
  const onSignedIn = vi.fn();
  render(
    <I18nProvider>
      <CodeSignIn session={session} deviceLabel="QE55Q80T" plain={plain} onSignedIn={onSignedIn} />
    </I18nProvider>
  );
  return { session, onSignedIn };
}

afterEach(() => {
  localStorage.clear();
  vi.unstubAllGlobals();
});

describe("CodeSignIn", () => {
  it("shows the code and the approve page, then keeps the tokens once approved", async () => {
    localStorage.setItem("mkt:locale", "en");
    const calls: [string, unknown][] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string, init: RequestInit) => {
        calls.push([url, JSON.parse(init.body as string)]);
        if (url === "/api/pairing/device") return json(200, start("ABCD-1234"));
        return json(200, { access_token: "a1", access_expires_in: 900, refresh_token: "r1" });
      })
    );
    const { session, onSignedIn } = renderSignIn();
    expect(await screen.findByTestId("tv-code")).toHaveTextContent("ABCD-1234");
    expect(screen.getByTestId("tv-code-signin")).toHaveAttribute("dir", "ltr");
    expect(screen.getByText("https://home.example/pair")).toBeInTheDocument();
    expect(screen.getByText(E["tv.code.waiting"]!)).toBeInTheDocument();
    expect(screen.queryByTestId("tv-plain")).toBeNull();
    await waitFor(() => expect(onSignedIn).toHaveBeenCalled(), { timeout: 3000 });
    expect(session.token()).toBe("a1");
    expect(calls[0]).toEqual(["/api/pairing/device", { device_label: "QE55Q80T" }]);
    expect(calls[1]).toEqual(["/api/pairing/device/token", { device_code: "dc-ABCD-1234" }]);
  });

  it("asks a new code when the old one ran out; plain HTTP said, in Arabic", async () => {
    localStorage.setItem("mkt:locale", "ar");
    let n = 0;
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string) => {
        if (url === "/api/pairing/device")
          return json(200, start(n++ === 0 ? "OLD-0001" : "NEW-0002"));
        return json(410, { title: "expired", status: 410 });
      })
    );
    renderSignIn(true);
    expect(await screen.findByTestId("tv-code")).toHaveTextContent("OLD-0001");
    expect(screen.getByTestId("tv-code-signin")).toHaveAttribute("dir", "rtl");
    expect(screen.getByRole("heading", { name: A["tv.code.title"] })).toBeInTheDocument();
    expect(screen.getByTestId("tv-plain")).toHaveTextContent(A["tv.plainHttp"]!);
    await waitFor(() => expect(screen.getByTestId("tv-code")).toHaveTextContent("NEW-0002"), {
      timeout: 3000,
    });
  });

  it("says when the home cannot give a code", async () => {
    localStorage.setItem("mkt:locale", "en");
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => json(500, { title: "x", status: 500 }))
    );
    renderSignIn();
    expect(await screen.findByRole("alert")).toHaveTextContent(E["tv.code.failed"]!);
  });

  it("names the approve page without the code", () => {
    expect(approvePage(start("X"), "https://o")).toBe("https://home.example/pair");
    expect(approvePage({ ...start("X"), verify_url: "" }, "https://o")).toBe("https://o/pair");
  });
});
