// Cases that need the real App route tree (web/test-cases/app-shell.md SHL-01
// and the "crash on a public page" cases of the auth pages). App uses a
// BrowserRouter, so the URL is set with history.pushState before rendering.
import { describe, it, expect, vi, beforeAll, beforeEach, afterEach } from "vitest";
import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock("./lib/api", async () => {
  const actual = await vi.importActual<typeof import("./lib/api")>("./lib/api");
  return { ...actual, api: { ...actual.api, get, post } };
});
// crash switch: a t() key (one page's title or label) throws
const { crash } = vi.hoisted(() => ({ crash: { keys: new Set<string>() } }));
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

// Each lazy server page these cases open transforms on its first visit: one-time work
// that, under load, ran out a findBy's wait (#697). Done once here, in a hook with its
// own budget.
beforeAll(async () => {
  // one at a time: modules evaluated at once could each see another half-made
  await import("./pages/queue/LibraryDashboard");
  await import("./pages/Admin/LibraryAdmin");
  await import("./pages/Admin/Errors");
  await import("./pages/Admin/LogSettings");
}, 60_000);

beforeEach(() => {
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
  it("SHL-01: / is Home (Epic 45), and only Home is current there", async () => {
    signedIn = true;
    window.history.replaceState({}, "", "/elsewhere-before");
    open("/");
    expect(
      await screen.findAllByRole("link", { name: en["nav.home"], current: "page" })
    ).toHaveLength(2); // sidebar + tab bar
    expect(await screen.findByRole("heading", { level: 1, name: en["home.title"] })).toBeVisible();
    expect(window.location.pathname).toBe("/");
    expect(
      screen.queryAllByRole("link", { name: en["nav.libraries"], current: "page" })
    ).toHaveLength(0);
    await browserBack();
    expect(window.location.pathname).toBe("/elsewhere-before");
  });

  // the library dashboard and a library's admin page are routed (lazily, with
  // the server pages); a member gets the admins-only message, never a 404
  it("routes /queue/libraries and /admin/libraries/:id: admins see them, members are refused", async () => {
    signedIn = true;
    amal.is_admin = true;
    try {
      const { unmount } = open("/queue/libraries");
      expect(
        await screen.findByRole(
          "heading",
          { level: 1, name: en["libdash.title"] },
          { timeout: 3000 }
        )
      ).toBeInTheDocument();
      unmount();
      const page = open("/admin/libraries/lib-1");
      // the generic answers list no libraries, so this one is not found (not a 404 page)
      expect(
        await screen.findByText(en["libpage.notFound.title"], {}, { timeout: 3000 })
      ).toBeInTheDocument();
      page.unmount();
    } finally {
      amal.is_admin = false;
    }
    for (const path of ["/queue/libraries", "/admin/libraries/lib-1"]) {
      const { unmount } = open(path);
      expect(
        await screen.findByText(en["common.forbidden.desc"], {}, { timeout: 3000 })
      ).toBeInTheDocument();
      unmount();
    }
  });
});

describe("Epic 53 story 53.15 routes", () => {
  it("routes /account/report and /account/reports for anyone signed in", async () => {
    signedIn = true;
    const a = open("/account/report");
    expect(
      await screen.findByRole("heading", { level: 1, name: en["report.title"] }, { timeout: 3000 })
    ).toBeInTheDocument();
    a.unmount();
    const b = open("/account/reports");
    expect(
      await screen.findByRole("heading", { level: 1, name: en["report.mine"] }, { timeout: 3000 })
    ).toBeInTheDocument();
    b.unmount();
  });
});

describe("Epic 53 story 53.11 routes", () => {
  it("routes /admin/errors and /admin/logs/settings: admins see them, members are refused", async () => {
    signedIn = true;
    amal.is_admin = true;
    try {
      const a = open("/admin/errors");
      expect(
        await screen.findByRole(
          "heading",
          { level: 1, name: en["errors.title"] },
          { timeout: 3000 }
        )
      ).toBeInTheDocument();
      a.unmount();
      const b = open("/admin/logs/settings");
      // the generic answer is no settings document: the page says it could not read them
      expect(
        await screen.findByText(en["logs.settings.loadError"], {}, { timeout: 3000 })
      ).toBeInTheDocument();
      b.unmount();
    } finally {
      amal.is_admin = false;
    }
    for (const path of [
      "/admin/errors",
      "/admin/errors/ab12",
      "/admin/logs/settings",
      "/admin/dashboards",
      "/admin/reports",
      "/admin/reports/1",
    ]) {
      const { unmount } = open(path);
      expect(
        await screen.findByText(en["common.forbidden.desc"], {}, { timeout: 3000 })
      ).toBeInTheDocument();
      unmount();
    }
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
    crash.keys.clear();
    await browserBack();
    expect(await screen.findByRole("heading", { name: backHeading })).toBeInTheDocument();
  }

  const quiet = () => vi.spyOn(console, "error").mockImplementation(() => {});

  it("LGN-23: a crash on /login shows a fallback; Back recovers", async () => {
    quiet();
    open("/register");
    await screen.findByRole("heading", { name: en["register.title"] });
    crash.keys.add("login.username"); // the Login page's label (not the register page's)
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

describe("the log ring (Epic 53 story 53.01)", () => {
  it("keeps each route change by its pattern, never the URL", async () => {
    const { clearLogLines, logLines } = await import("./lib/errorLog");
    clearLogLines();
    open("/videos/01J9ZK2ABC/watch?t=secret");
    await screen.findAllByText(/./);
    const routes = logLines()
      .filter((l) => l.msg === "route_changed")
      .map((l) => l.attrs?.route);
    expect(routes).toContain("/videos/:id/watch");
    expect(JSON.stringify(logLines())).not.toContain("01J9ZK2ABC");
    expect(JSON.stringify(logLines())).not.toContain("secret");
  });
});
