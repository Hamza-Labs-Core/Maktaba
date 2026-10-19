// Cases that need the real App route tree (web/test-cases/app-shell.md SHL-01
// and the "crash on a public page" cases of the auth pages). App uses a
// BrowserRouter, so the URL is set with history.pushState before rendering.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock("./lib/api", async () => {
  const actual = await vi.importActual<typeof import("./lib/api")>("./lib/api");
  return { ...actual, api: { ...actual.api, get, post } };
});
// crash switches: ClusterSignIn (only on /login) and a t() key per page title
const { crash } = vi.hoisted(() => ({ crash: { cluster: false, keys: new Set<string>() } }));
vi.mock("./components/ClusterSignIn", async () => {
  const actual = await vi.importActual<typeof import("./components/ClusterSignIn")>(
    "./components/ClusterSignIn"
  );
  return {
    ...actual,
    ClusterSignIn: (props: { returnTo?: string }) => {
      if (crash.cluster) throw new Error("cluster boom");
      return <actual.ClusterSignIn {...props} />;
    },
  };
});
vi.mock("./lib/i18n", async () => {
  const actual = await vi.importActual<typeof import("./lib/i18n")>("./lib/i18n");
  return {
    ...actual,
    useI18n: () => {
      const i18n = actual.useI18n();
      return {
        ...i18n,
        t: (key: string, vars?: Record<string, string | number>) => {
          if (crash.keys.has(key)) throw new Error(`boom on ${key}`);
          return i18n.t(key, vars);
        },
      };
    },
  };
});

import { App } from "./App";
import { ApiError } from "./lib/api";
import en from "./i18n/en.json";

const amal = { id: "u1", username: "amal", is_admin: false };
let signedIn = false;

function open(path: string) {
  window.history.pushState({}, "", path);
  return render(<App />);
}

async function browserBack() {
  await act(async () => {
    window.history.back();
    await new Promise((r) => setTimeout(r, 30));
  });
}

beforeEach(() => {
  crash.cluster = false;
  crash.keys.clear();
  signedIn = false;
  vi.spyOn(window, "scrollTo").mockImplementation(() => {});
  get.mockReset();
  post.mockReset();
  get.mockImplementation((path: string) => {
    if (path === "/api/auth/me") {
      return signedIn
        ? Promise.resolve({ user: amal })
        : Promise.reject(new ApiError({ type: "about:blank", title: "", status: 401 }));
    }
    if (path === "/api/cluster/self") return Promise.reject(new Error("no cluster"));
    if (path.startsWith("/api/notifications")) return Promise.resolve({ items: [], unread: 0 });
    return Promise.resolve({ items: [], libraries: [], next_cursor: null });
  });
  post.mockResolvedValue({});
});

afterEach(() => {
  vi.restoreAllMocks();
  window.history.replaceState({}, "", "/");
});

describe("App routes (test-cases/app-shell.md)", () => {
  it("SHL-01: / redirects to /library and Back does not land on / again", async () => {
    signedIn = true;
    window.history.replaceState({}, "", "/elsewhere-before");
    open("/");
    expect(
      await screen.findAllByRole("link", { name: en["nav.library"], current: "page" })
    ).toHaveLength(2); // sidebar + tab bar
    expect(window.location.pathname).toBe("/library");
    await browserBack();
    expect(window.location.pathname).toBe("/elsewhere-before");
  });
});

// The public pages sit outside AppShell, so App.tsx gives them their own
// RouteErrorBoundary: a render crash there shows the fallback, and Back
// brings the app back.
describe("Public page crash (login / register test cases)", () => {
  async function expectFallbackThenBack(backHeading: string) {
    expect(await screen.findByRole("alert", {}, { timeout: 300 })).toHaveTextContent(
      en["common.pageError"]
    );
    crash.cluster = false;
    crash.keys.clear();
    await browserBack();
    expect(await screen.findByRole("heading", { name: backHeading })).toBeInTheDocument();
  }

  const quiet = () => vi.spyOn(console, "error").mockImplementation(() => {});

  it("LGN-23: a crash on /login shows a fallback; Back recovers", async () => {
    quiet();
    open("/register");
    await screen.findByRole("heading", { name: en["register.title"] });
    crash.cluster = true;
    await act(async () => {
      window.history.pushState({}, "", "/login");
      window.dispatchEvent(new PopStateEvent("popstate"));
    });
    await expectFallbackThenBack(en["register.title"]);
  });

  it("REG-19: a crash on /register shows a fallback; Back recovers", async () => {
    quiet();
    open("/login");
    const user = userEvent.setup();
    crash.keys.add("register.title");
    await user.click(await screen.findByRole("link", { name: en["login.register"] }));
    await expectFallbackThenBack(en["app.title"]);
  });

  it("no self-service reset: /forgot-password and /reset-password are gone", async () => {
    open("/forgot-password");
    expect(await screen.findByRole("heading", { name: en["app.title"] })).toBeInTheDocument();
    expect(screen.queryByText("Reset your password")).not.toBeInTheDocument();
  });
});
