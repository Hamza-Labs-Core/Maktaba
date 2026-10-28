// Settings page cases from web/test-cases/settings-account.md, settings-sessions.md,
// settings-appearance.md, settings-privacy.md and settings-about.md (the Models and
// Rules tabs have their own files next to their sections).
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState, type ReactElement, type ReactNode } from "react";
import { MemoryRouter, Route, Routes, useNavigate } from "react-router-dom";

const { get, post, put, patch, del, request } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  put: vi.fn(),
  patch: vi.fn(),
  del: vi.fn(),
  request: vi.fn(),
}));
vi.mock("../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../lib/api")>("../lib/api");
  return { ...actual, request, api: { ...actual.api, get, post, put, patch, delete: del } };
});

// Switches used by the crash cases: make one i18n key or readMode() throw.
const flags = vi.hoisted(() => ({ throwKey: null as string | null, throwReadMode: false }));
vi.mock("../lib/i18n", async () => {
  const actual = await vi.importActual<typeof import("../lib/i18n")>("../lib/i18n");
  return {
    ...actual,
    useI18n: () => {
      const v = actual.useI18n();
      const bad = flags.throwKey;
      if (!bad) return v;
      return {
        ...v,
        t: (k: string, vars?: Record<string, string | number>) => {
          if (k === bad) throw new Error("boom");
          return v.t(k, vars);
        },
      };
    },
  };
});
vi.mock("../lib/theme", async () => {
  const actual = await vi.importActual<typeof import("../lib/theme")>("../lib/theme");
  return {
    ...actual,
    readMode: () => {
      if (flags.throwReadMode) throw new Error("boom");
      return actual.readMode();
    },
  };
});

import { ToastProvider } from "@ds/components/Toast/Toast";
import { ApiError, type Problem } from "../lib/api";
import { AuthContext, AuthProvider, RequireAuth, type User } from "../lib/auth";
import { I18nProvider } from "../lib/i18n";
import { watchSystemTheme } from "../lib/theme";
import { adminUser, authState, memberUser, quietCrash, renderPage } from "../test/pageHarness";
import { routeGet } from "../test/queueHarness";
import { Settings } from "./Settings";
import en from "../i18n/en.json";

// ─── data ────────────────────────────────────────────────────────────────

const VERSION = {
  version: "1.5.0",
  commit: "abcdef1234567890",
  build_date: "2026-10-01",
  channel: "stable",
};
const UP_TO_DATE = {
  available: false,
  current_version: "1.5.0",
  channel: "stable",
  checked_at: "2026-10-19T08:00:00Z",
};
const AVAILABLE = {
  available: true,
  current_version: "1.5.0",
  latest_version: "1.6.0",
  channel: "stable",
  release_url: "https://github.com/Hamza-Labs-Core/Maktaba/releases/tag/v1.6.0",
  release_notes: "- faster\n- fixes",
  checked_at: "2026-10-19T08:00:00Z",
};
const HISTORY = {
  items: [
    {
      video_id: "v1",
      title: "Tafsir 1",
      duration_sec: 3600,
      times_watched: 2,
      total_watch_sec: 5400,
      best_percent: 87.4,
      last_watched_at: "2026-10-18T20:00:00Z",
      position_sec: 120,
    },
    {
      video_id: "v2",
      title: "Fiqh 2",
      duration_sec: 600,
      times_watched: 1,
      total_watch_sec: 300,
      best_percent: 50,
      last_watched_at: "2026-10-17T20:00:00Z",
      position_sec: 0,
    },
  ],
  limit: 20,
  offset: 0,
};

function overview(over: Record<string, unknown> = {}) {
  return {
    current_version: "1.5.0",
    watermark: "1.5.0",
    policy: {
      auto_install: true,
      channel: "stable",
      window: "03:00-05:00",
      window_overrun_minutes: 120,
      min_release_age_hours: 24,
      pinned_version: "",
      deferred_until: null,
      skipped_versions: [],
      node_failure_policy: "hold_platform",
      keep_versions: 3,
    },
    auto_install_available: true,
    plan: {
      target: { version: "1.6.0", tag: "v1.6.0", channel: "stable", verify_state: "verified" },
      held: false,
      stepping_stone: false,
      auto_install: true,
      due: false,
      next_run_at: "2026-10-22T03:00:00Z",
    },
    catalog: [],
    check_stale: false,
    time_zone: "Europe/London",
    ...over,
  };
}

function problem(status: number, title: string, extra: Record<string, unknown> = {}) {
  return new ApiError({ type: "about:blank", title, status, ...extra } as Problem);
}

type Table = Parameters<typeof routeGet>[1];
function mockGets(over: Table = {}) {
  routeGet(get, {
    "/api/system/version": VERSION,
    "/api/system/updates": UP_TO_DATE,
    "/api/me/activity/settings": { track_enabled: true },
    "/api/me/activity": { items: [], limit: 10, offset: 0 },
    "/api/me/history": () => structuredClone(HISTORY),
    "/api/models": [],
    "/api/rules": { sections: [] },
    // an admin's Privacy tab also reads the server's usage-data switch (story 16.5)
    "/api/telemetry/server": {
      sharing: false,
      enabled: true,
      pending: 0,
      oldest_pending: null,
      last_sent_at: null,
      last_error: "",
      preview: {},
    },
    ...over,
  });
}

const getPaths = () => get.mock.calls.map((c) => String(c[0]));

function renderSettings(
  path: string,
  opts: { user?: User | null; history?: string[]; extraRoutes?: ReactElement } = {}
) {
  return renderPage(
    <ToastProvider>
      <Settings />
    </ToastProvider>,
    { path, route: "/settings/*", ...opts }
  );
}

/** The <dd> shown for a <dt> label in the About meta list. */
function metaValue(label: string) {
  return screen.getByText(label, { selector: "dt" }).nextElementSibling as HTMLElement;
}

function stubMatchMedia(prefersLight: boolean) {
  const listeners = new Set<() => void>();
  const mq = {
    matches: prefersLight,
    media: "(prefers-color-scheme: light)",
    addEventListener: (_: string, f: () => void) => listeners.add(f),
    removeEventListener: (_: string, f: () => void) => listeners.delete(f),
  };
  vi.stubGlobal(
    "matchMedia",
    vi.fn(() => mq)
  );
  return {
    flip(light: boolean) {
      mq.matches = light;
      listeners.forEach((f) => f());
    },
  };
}

beforeEach(() => {
  for (const f of [get, post, put, patch, del, request]) f.mockReset();
  mockGets();
  localStorage.clear();
  delete document.documentElement.dataset.theme;
  delete document.documentElement.dataset.density;
  flags.throwKey = null;
  flags.throwReadMode = false;
});

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

// ─── Account tab and tab nav ─────────────────────────────────────────────

// A signed-in session whose logoutAll really signs out, and a /login page with Back.
function LoginProbe() {
  const nav = useNavigate();
  return (
    <>
      <p>login page</p>
      <button type="button" onClick={() => nav(-1)}>
        go back
      </button>
    </>
  );
}
function StatefulAuth({ onLogoutAll, children }: { onLogoutAll: () => void; children: ReactNode }) {
  const [user, setUser] = useState<User | null>(memberUser);
  const value = authState(user, {
    logoutAll: async () => {
      onLogoutAll();
      setUser(null);
      return "everywhere" as const;
    },
  });
  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}
function renderSignOutFlow(onLogoutAll: () => void) {
  return render(
    <I18nProvider>
      <ToastProvider>
        <StatefulAuth onLogoutAll={onLogoutAll}>
          <MemoryRouter initialEntries={["/library", "/settings"]}>
            <Routes>
              <Route path="/login" element={<LoginProbe />} />
              <Route
                path="/library"
                element={
                  <RequireAuth>
                    <p>library page</p>
                  </RequireAuth>
                }
              />
              <Route
                path="/settings/*"
                element={
                  <RequireAuth>
                    <Settings />
                  </RequireAuth>
                }
              />
            </Routes>
          </MemoryRouter>
        </StatefulAuth>
      </ToastProvider>
    </I18nProvider>
  );
}

describe("Settings account (test-cases/settings-account.md)", () => {
  it("SAC-01: account tab shows the signed-in username", () => {
    renderSettings("/settings", { user: { id: "u1", username: "maha", is_admin: false } });
    expect(screen.getByRole("heading", { level: 1, name: "Settings" })).toBeInTheDocument();
    const line = screen.getByText("maha").closest("p")!;
    expect(line).toHaveTextContent("Signed in as maha");
    expect(line).not.toHaveTextContent("(admin)");
    expect(screen.getByText("maha").tagName).toBe("STRONG");
    expect(screen.getByRole("button", { name: "Sign out everywhere" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Manage tokens" })).toHaveAttribute(
      "href",
      "/account/tokens"
    );
    expect(screen.getByRole("heading", { level: 2, name: "Diagnostics" })).toBeInTheDocument();
    expect(screen.getByText(/Collect a diagnostics bundle/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Collect diagnostics" })).toBeInTheDocument();
    // renaming the home is an admin's (the API refuses anyone else)
    expect(screen.queryByRole("heading", { name: "Home name" })).not.toBeInTheDocument();
  });

  it("SAC-02: an admin sees (admin) after the name", () => {
    renderSettings("/settings", { user: { id: "u1", username: "root", is_admin: true } });
    expect(screen.getByText("root").closest("p")).toHaveTextContent("Signed in as root (admin)");
    expect(screen.getByRole("heading", { level: 2, name: "Home name" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Sign out everywhere" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Collect diagnostics" })).toBeInTheDocument();
  });

  it("SAC-03: a signed-out visit redirects to /login with no Settings calls", () => {
    for (const path of ["/settings", "/settings/privacy", "/settings/rules"]) {
      const { unmount } = renderSettings(path, { user: null });
      expect(screen.getByText(`login page (from ${path})`)).toBeInTheDocument();
      expect(screen.queryByRole("heading", { name: "Settings" })).not.toBeInTheDocument();
      unmount();
    }
    expect(get).not.toHaveBeenCalled();
  });

  it("SAC-04: Sign out everywhere calls logout-all and goes to /login", async () => {
    const user = userEvent.setup();
    const onLogoutAll = vi.fn();
    renderSignOutFlow(onLogoutAll);
    await user.click(screen.getByRole("button", { name: "Sign out everywhere" }));
    expect(await screen.findByText("login page")).toBeInTheDocument();
    expect(onLogoutAll).toHaveBeenCalledTimes(1);
  });

  // Bug: lib/auth.tsx logoutAll() only console.warns when POST /api/auth/logout-all fails,
  // then signs out locally as if it worked. Fix belongs in lib/auth.tsx (shared).
  it.fails(
    "SAC-05: a failed logout-all tells the user other devices may still be signed in",
    async () => {
      const user = userEvent.setup();
      vi.spyOn(console, "warn").mockImplementation(() => {});
      get.mockImplementation((url: string) =>
        url === "/api/auth/me"
          ? Promise.resolve({ user: memberUser })
          : Promise.reject(new Error(`unmocked GET ${url}`))
      );
      post.mockRejectedValue(problem(500, "Internal Server Error"));
      render(
        <I18nProvider>
          <ToastProvider>
            <AuthProvider>
              <MemoryRouter initialEntries={["/settings"]}>
                <Routes>
                  <Route path="/login" element={<p>login page</p>} />
                  <Route
                    path="/settings/*"
                    element={
                      <RequireAuth>
                        <Settings />
                      </RequireAuth>
                    }
                  />
                </Routes>
              </MemoryRouter>
            </AuthProvider>
          </ToastProvider>
        </I18nProvider>
      );
      await user.click(await screen.findByRole("button", { name: "Sign out everywhere" }));
      await screen.findByText("login page");
      expect(post).toHaveBeenCalledWith("/api/auth/logout-all");
      expect(screen.getByRole("alert")).toBeInTheDocument();
    }
  );

  it("SAC-06: Back after Sign out everywhere does not reopen Settings", async () => {
    const user = userEvent.setup();
    renderSignOutFlow(() => {});
    await user.click(screen.getByRole("button", { name: "Sign out everywhere" }));
    await screen.findByText("login page");
    await user.click(screen.getByRole("button", { name: "go back" }));
    // /library is behind RequireAuth: it sends the signed-out user to /login again
    expect(await screen.findByText("login page")).toBeInTheDocument();
    expect(screen.queryByText("library page")).not.toBeInTheDocument();
    expect(screen.queryByText(/Signed in as/)).not.toBeInTheDocument();
  });

  function stubDownload(response: () => Promise<Response>) {
    const fetchMock = vi.fn(response);
    vi.stubGlobal("fetch", fetchMock);
    Object.assign(URL, { createObjectURL: vi.fn(() => "blob:diag"), revokeObjectURL: vi.fn() });
    const names: string[] = [];
    vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (
      this: HTMLAnchorElement
    ) {
      names.push(this.download);
    });
    return { fetchMock, names };
  }

  it("SAC-07: Collect diagnostics downloads the bundle", async () => {
    const user = userEvent.setup();
    let disposition: string | null = 'attachment; filename="diag-u1.tar.gz"';
    const { fetchMock, names } = stubDownload(async () => {
      const headers = new Headers();
      if (disposition) headers.set("Content-Disposition", disposition);
      return new Response(new Blob(["bundle"]), { status: 200, headers });
    });
    renderSettings("/settings");
    await user.click(screen.getByRole("button", { name: "Collect diagnostics" }));
    expect(await screen.findByRole("status")).toHaveTextContent("Diagnostics bundle downloaded.");
    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toMatch(/\/api\/diagnostics\/export$/);
    expect(init.credentials).toBe("include");
    expect(names).toEqual(["diag-u1.tar.gz"]);
    expect(document.querySelector("a[download]")).toBeNull(); // the anchor is removed

    disposition = null;
    await user.click(screen.getByRole("button", { name: "Collect diagnostics" }));
    await waitFor(() => expect(names).toEqual(["diag-u1.tar.gz", "maktaba-diagnostics.tar.gz"]));
  });

  it("SAC-08: the diagnostics button is busy while collecting", async () => {
    const user = userEvent.setup();
    let finish!: (r: Response) => void;
    const { fetchMock } = stubDownload(() => new Promise<Response>((r) => (finish = r)));
    renderSettings("/settings");
    await user.click(screen.getByRole("button", { name: "Collect diagnostics" }));
    const busy = screen.getByRole("button", { name: "Collecting…" });
    expect(busy).toBeDisabled();
    await user.click(busy);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    await act(async () => finish(new Response(new Blob(["x"]), { status: 200 })));
    expect(await screen.findByRole("button", { name: "Collect diagnostics" })).toBeEnabled();
  });

  it("SAC-09: a diagnostics error toast on 500 / 403 / 401", async () => {
    const user = userEvent.setup();
    const errors = vi.spyOn(console, "error").mockImplementation(() => {});
    const answers = [
      { status: 500, statusText: "Internal Server Error", kind: "server" },
      { status: 403, statusText: "Forbidden", kind: "noAccess" },
      { status: 401, statusText: "Unauthorized", kind: "signedOut" },
    ];
    let i = 0;
    const { names } = stubDownload(async () => new Response("boom", answers[i++]));
    renderSettings("/settings");
    for (const a of answers) {
      await user.click(screen.getByRole("button", { name: "Collect diagnostics" }));
      // the kind's sentence, never the server's status text or body
      expect(
        await screen.findByText(
          `${en["settings.diagnostics.error"]}: ${en[`error.kind.${a.kind}` as keyof typeof en]}`
        )
      ).toBeInTheDocument();
      expect(screen.queryByText(/boom/)).not.toBeInTheDocument();
      expect(screen.getByRole("button", { name: "Collect diagnostics" })).toBeEnabled();
    }
    expect(screen.getAllByRole("alert")).toHaveLength(3);
    expect(names).toEqual([]);
    expect(errors).toHaveBeenCalledWith(
      expect.stringMatching(/^\[api\] GET \/api\/diagnostics\/export failed \(500\)/)
    );
  });

  it("SAC-10: a diagnostics error toast on network failure", async () => {
    const user = userEvent.setup();
    vi.spyOn(console, "error").mockImplementation(() => {});
    stubDownload(() => Promise.reject(new TypeError("Failed to fetch")));
    renderSettings("/settings");
    await user.click(screen.getByRole("button", { name: "Collect diagnostics" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      `${en["settings.diagnostics.error"]}: ${en["error.kind.unreachable"]}`
    );
    expect(screen.getByRole("button", { name: "Collect diagnostics" })).toBeEnabled();
  });

  const TABS: [string, string][] = [
    ["Account", "/settings"],
    ["Sessions", "/settings/sessions"],
    ["Appearance", "/settings/appearance"],
    ["Privacy", "/settings/privacy"],
    ["Models", "/settings/models"],
    ["Rules", "/settings/rules"],
    ["Advanced", "/settings/advanced"],
    ["Storage & discovery", "/settings/storage"],
    ["Subscription", "/settings/subscription"],
    ["About", "/settings/about"],
  ];

  it("SAC-12: the tab nav lists all ten tabs with the right hrefs", () => {
    for (const path of ["/settings", "/settings/rules"]) {
      const { unmount } = renderSettings(path);
      const nav = screen.getByRole("navigation", { name: "Settings" });
      const links = within(nav).getAllByRole("link");
      expect(links.map((l) => [l.textContent, l.getAttribute("href")])).toEqual(TABS);
      unmount();
    }
  });

  it("SAC-12b: Storage & discovery is an admin's tab only", () => {
    renderSettings("/settings", { user: memberUser });
    const nav = screen.getByRole("navigation", { name: "Settings" });
    const links = within(nav).getAllByRole("link");
    expect(links.map((l) => l.getAttribute("href"))).toEqual(
      TABS.map(([, href]) => href).filter((h) => h !== "/settings/storage")
    );
  });

  it("SAC-13: a deep link to each sub-route renders that tab and only its calls", async () => {
    const cases: [string, () => Promise<unknown>, string[]][] = [
      // an admin's Account tab reads the home's name
      ["/settings", () => screen.findByText(/Signed in as/), ["/api/auth/me"]],
      ["/settings/", () => screen.findByText(/Signed in as/), ["/api/auth/me"]],
      ["/settings/sessions", () => screen.findByRole("link", { name: "Manage sessions" }), []],
      ["/settings/appearance", () => screen.findByRole("radiogroup", { name: "Theme" }), []],
      [
        "/settings/privacy",
        () => screen.findByRole("heading", { name: "Activity tracking" }),
        ["/api/me/", "/api/telemetry/server"],
      ],
      [
        "/settings/models",
        () => screen.findByRole("heading", { name: "Model Management" }),
        ["/api/models"],
      ],
      [
        "/settings/rules",
        () => screen.findByRole("heading", { level: 2, name: "Rules" }),
        ["/api/rules"],
      ],
      [
        "/settings/advanced",
        () => screen.findByRole("heading", { level: 2, name: "Experiments" }),
        ["/api/me/cohorts"],
      ],
      [
        "/settings/about",
        () => screen.findByText("Current version"),
        ["/api/system/", "/api/cluster/updates", "/api/auth/me", "/api/entitlements"],
      ],
    ];
    for (const [path, find, prefixes] of cases) {
      get.mockClear();
      const { unmount } = renderSettings(path);
      await find();
      // every tab also reads the update status, for the badge on About
      await waitFor(() => expect(getPaths()).toContain("/api/system/updates"));
      if (prefixes.length > 0) {
        await waitFor(() =>
          expect(getPaths().some((p) => prefixes.some((x) => p.startsWith(x)))).toBe(true)
        );
      }
      for (const p of getPaths()) {
        expect(p === "/api/system/updates" || prefixes.some((x) => p.startsWith(x))).toBe(true);
      }
      unmount();
    }
  });

  it("SAC-14: clicking a tab link switches the panel; the h1 and nav stay", async () => {
    const user = userEvent.setup();
    renderSettings("/settings");
    const h1 = screen.getByRole("heading", { level: 1 });
    await user.click(screen.getByRole("link", { name: "Appearance" }));
    expect(screen.getByRole("radiogroup", { name: "Theme" })).toBeInTheDocument();
    expect(screen.queryByText(/Signed in as/)).not.toBeInTheDocument();
    await user.click(screen.getByRole("link", { name: "About" }));
    expect(await screen.findByText("Current version")).toBeInTheDocument();
    expect(screen.queryByRole("radiogroup", { name: "Theme" })).not.toBeInTheDocument();
    await user.click(screen.getByRole("link", { name: "Account" }));
    expect(screen.getByText(/Signed in as/)).toBeInTheDocument();
    expect(screen.queryByText("Current version")).not.toBeInTheDocument();
    expect(screen.getByRole("heading", { level: 1 })).toBe(h1);
  });

  it("SAC-15: the active tab is marked with aria-current", async () => {
    renderSettings("/settings/privacy");
    const nav = screen.getByRole("navigation", { name: "Settings" });
    expect(within(nav).getByRole("link", { name: "Privacy" })).toHaveAttribute(
      "aria-current",
      "page"
    );
    expect(within(nav).getByRole("link", { name: "Account" })).not.toHaveAttribute("aria-current");
    expect(nav.querySelectorAll('[aria-current="page"]')).toHaveLength(1);
    await screen.findByRole("table");
  });

  it("SAC-16: an unknown sub-route opens the Account tab, nav still works", async () => {
    const user = userEvent.setup();
    for (const path of ["/settings/nope", "/settings/rules/extra"]) {
      const { unmount } = renderSettings(path);
      expect(await screen.findByText(/Signed in as/)).toBeInTheDocument();
      unmount();
    }
    renderSettings("/settings/nope");
    await user.click(await screen.findByRole("link", { name: "Appearance" }));
    expect(screen.getByRole("radiogroup", { name: "Theme" })).toBeInTheDocument();
  });

  it("SAC-17: Back moves between tabs with each panel fully rendered", async () => {
    const user = userEvent.setup();
    renderSettings("/settings");
    await user.click(screen.getByRole("link", { name: "Appearance" }));
    await user.click(screen.getByRole("link", { name: "Privacy" }));
    await screen.findByRole("table");
    await user.click(screen.getByRole("button", { name: "go back" }));
    expect(screen.getByRole("radiogroup", { name: "Theme" })).toBeInTheDocument();
    expect(screen.queryByRole("table")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "go back" }));
    expect(screen.getByText(/Signed in as/)).toBeInTheDocument();
    expect(screen.queryByRole("radiogroup", { name: "Theme" })).not.toBeInTheDocument();
  });

  it("SAC-18: a render crash shows the route error fallback; Back still works", async () => {
    const user = userEvent.setup();
    quietCrash();
    const bad = { id: "u1", username: { bad: true } as unknown as string, is_admin: false };
    renderSettings("/settings", { user: bad, history: ["/elsewhere"] });
    expect(screen.getByRole("alert")).toHaveTextContent("This page hit an error");
    expect(screen.getByRole("navigation", { name: "test shell" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "go back" }));
    expect(screen.getByText("elsewhere")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    await user.click(screen.getByRole("link", { name: "go elsewhere" }));
    expect(screen.getByText("elsewhere")).toBeInTheDocument();
  });

  it("SAC-20: Arabic: RTL layout and Arabic strings", () => {
    localStorage.setItem("mkt:locale", "ar");
    renderSettings("/settings", { user: { id: "u1", username: "مها", is_admin: false } });
    expect(document.documentElement).toHaveAttribute("dir", "rtl");
    expect(document.documentElement).toHaveAttribute("lang", "ar");
    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("الإعدادات");
    const nav = screen.getByRole("navigation", { name: "الإعدادات" });
    expect(
      within(nav)
        .getAllByRole("link")
        .map((l) => l.textContent)
    ).toEqual([
      "الحساب",
      "الجلسات",
      "المظهر",
      "الخصوصية",
      "النماذج",
      "القواعد",
      "متقدم",
      "الاشتراك",
      "حول",
    ]);
    expect(screen.getByText("مها").closest("p")).toHaveTextContent("تسجيل الدخول باسم مها");
    expect(screen.getByRole("button", { name: "تسجيل الخروج من كل الأجهزة" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "جمع التشخيص" })).toBeInTheDocument();
    expect(document.body.textContent).not.toMatch(/settings\.[a-z]/);
  });
});

// ─── Sessions tab ───────────────────────────────────────────────────────

describe("Settings sessions (test-cases/settings-sessions.md)", () => {
  it("SSE-01: shows the sessions panel text", () => {
    for (const user of [memberUser, adminUser]) {
      const { unmount } = renderSettings("/settings/sessions", { user });
      expect(screen.getByRole("heading", { level: 1, name: "Settings" })).toBeInTheDocument();
      expect(screen.getByRole("navigation", { name: "Settings" })).toBeInTheDocument();
      expect(
        screen.getByText(
          "See every device and browser signed in to your account, and sign one out."
        )
      ).toHaveClass("mkt-muted");
      unmount();
    }
  });

  it("SSE-03: the tab points to the real sessions page", () => {
    renderSettings("/settings/sessions");
    expect(document.querySelector('a[href="/account/sessions"]')).not.toBeNull();
  });

  it("SSE-04: a signed-out deep link redirects to /login", () => {
    renderSettings("/settings/sessions", { user: null });
    expect(screen.getByText("login page (from /settings/sessions)")).toBeInTheDocument();
    expect(screen.queryByText(/See every device/)).not.toBeInTheDocument();
  });

  it("SSE-05: deep link and Back to and from the tab", async () => {
    const user = userEvent.setup();
    renderSettings("/settings");
    await user.click(screen.getByRole("link", { name: "Sessions" }));
    expect(screen.getByText(/See every device/)).toBeInTheDocument();
    await user.click(screen.getByRole("link", { name: "Appearance" }));
    expect(screen.queryByText(/See every device/)).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "go back" }));
    expect(screen.getByText(/See every device/)).toBeInTheDocument();
  });

  it("SSE-06: a render crash shows the route error fallback; Back works", async () => {
    const user = userEvent.setup();
    quietCrash();
    flags.throwKey = "settings.sessions.hint";
    renderSettings("/settings");
    await user.click(screen.getByRole("link", { name: "Sessions" }));
    expect(screen.getByRole("alert")).toHaveTextContent(en["common.pageError"]);
    await user.click(screen.getByRole("button", { name: "go back" }));
    expect(screen.getByText(/Signed in as/)).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    await user.click(screen.getByRole("link", { name: "go elsewhere" }));
    expect(screen.getByText("elsewhere")).toBeInTheDocument();
  });
});

// ─── Appearance tab ─────────────────────────────────────────────────────

function checkedIn(group: string) {
  return within(screen.getByRole("radiogroup", { name: group }))
    .getAllByRole("radio")
    .filter((r) => (r as HTMLInputElement).checked)
    .map((r) => r.closest("label")!.textContent);
}

describe("Settings appearance (test-cases/settings-appearance.md)", () => {
  it("SAP-01: renders the theme, language and density groups", () => {
    renderSettings("/settings/appearance");
    const names = (group: string) =>
      within(screen.getByRole("radiogroup", { name: group }))
        .getAllByRole("radio")
        .map((r) => r.closest("label")!.textContent);
    expect(names("Theme")).toEqual(["Light", "Dark", "System"]);
    expect(names("Language")).toEqual(["English", "العربية"]);
    expect(names("Density")).toEqual(["Comfortable", "Compact"]);
  });

  it("SAP-02: the checked radios reflect the stored values", () => {
    localStorage.setItem("mkt:theme", "dark");
    localStorage.setItem("mkt:locale", "en");
    localStorage.setItem("mkt:density", "compact");
    renderSettings("/settings/appearance");
    expect(checkedIn("Theme")).toEqual(["Dark"]);
    expect(checkedIn("Language")).toEqual(["English"]);
    expect(checkedIn("Density")).toEqual(["Compact"]);
  });

  it("SAP-03: nothing stored: System, the browser language, Comfortable", () => {
    const { unmount } = renderSettings("/settings/appearance");
    expect(checkedIn("Theme")).toEqual(["System"]);
    expect(checkedIn("Language")).toEqual(["English"]);
    expect(checkedIn("Density")).toEqual(["Comfortable"]);
    unmount();

    vi.spyOn(navigator, "language", "get").mockReturnValue("ar-EG");
    renderSettings("/settings/appearance");
    expect(checkedIn("اللغة")).toEqual(["العربية"]);
    expect(document.documentElement).toHaveAttribute("dir", "rtl");
  });

  it("SAP-04: corrupt stored values fall back", () => {
    localStorage.setItem("mkt:theme", "purple");
    localStorage.setItem("mkt:locale", "fr");
    localStorage.setItem("mkt:density", "huge");
    renderSettings("/settings/appearance");
    expect(checkedIn("Theme")).toEqual(["System"]);
    expect(checkedIn("Language")).toEqual(["English"]);
    expect(checkedIn("Density")).toEqual(["Comfortable"]);
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("SAP-05: choosing Light / Dark sets data-theme and storage at once", async () => {
    const user = userEvent.setup();
    stubMatchMedia(false); // OS dark
    renderSettings("/settings/appearance");
    await user.click(screen.getByRole("radio", { name: "Light" }));
    expect(document.documentElement.dataset.theme).toBe("light");
    expect(localStorage.getItem("mkt:theme")).toBe("light");
    expect(checkedIn("Theme")).toEqual(["Light"]);
    await user.click(screen.getByRole("radio", { name: "Dark" }));
    expect(document.documentElement.dataset.theme).toBe("dark");
    expect(localStorage.getItem("mkt:theme")).toBe("dark");
  });

  it("SAP-06: System follows the OS, including a live OS switch", async () => {
    const user = userEvent.setup();
    localStorage.setItem("mkt:theme", "dark");
    const os = stubMatchMedia(true);
    const stop = watchSystemTheme(); // what App's ThemeSync subscribes
    renderSettings("/settings/appearance");
    await user.click(screen.getByRole("radio", { name: "System" }));
    expect(localStorage.getItem("mkt:theme")).toBe("system");
    expect(document.documentElement.dataset.theme).toBe("light");
    act(() => os.flip(false));
    expect(document.documentElement.dataset.theme).toBe("dark");
    act(() => os.flip(true));
    expect(document.documentElement.dataset.theme).toBe("light");
    expect(checkedIn("Theme")).toEqual(["System"]);
    stop();
  });

  it("SAP-07: an explicit Dark ignores a live OS switch", async () => {
    const user = userEvent.setup();
    const os = stubMatchMedia(true);
    const stop = watchSystemTheme();
    renderSettings("/settings/appearance");
    await user.click(screen.getByRole("radio", { name: "Dark" }));
    act(() => os.flip(true));
    act(() => os.flip(false));
    act(() => os.flip(true));
    expect(document.documentElement.dataset.theme).toBe("dark");
    expect(checkedIn("Theme")).toEqual(["Dark"]);
    stop();
  });

  it("SAP-09: switching to Arabic: RTL, Arabic strings, kept after a reload", async () => {
    const user = userEvent.setup();
    const { unmount } = renderSettings("/settings/appearance");
    await user.click(screen.getByRole("radio", { name: "العربية" }));
    expect(document.documentElement).toHaveAttribute("dir", "rtl");
    expect(document.documentElement).toHaveAttribute("lang", "ar");
    expect(localStorage.getItem("mkt:locale")).toBe("ar");
    for (const g of ["السمة", "اللغة", "الكثافة"])
      expect(screen.getByRole("radiogroup", { name: g })).toBeInTheDocument();
    for (const r of ["فاتح", "داكن", "النظام"])
      expect(screen.getByRole("radio", { name: r })).toBeInTheDocument();
    expect(checkedIn("اللغة")).toEqual(["العربية"]);
    unmount();
    renderSettings("/settings/appearance"); // the "reload"
    expect(document.documentElement).toHaveAttribute("dir", "rtl");
    expect(checkedIn("اللغة")).toEqual(["العربية"]);
  });

  it("SAP-10: switching back to English keeps the other choices", async () => {
    const user = userEvent.setup();
    localStorage.setItem("mkt:locale", "ar");
    localStorage.setItem("mkt:theme", "light");
    localStorage.setItem("mkt:density", "compact");
    renderSettings("/settings/appearance");
    await user.click(screen.getByRole("radio", { name: "English" }));
    expect(document.documentElement).toHaveAttribute("dir", "ltr");
    expect(document.documentElement).toHaveAttribute("lang", "en");
    expect(localStorage.getItem("mkt:locale")).toBe("en");
    expect(checkedIn("Theme")).toEqual(["Light"]);
    expect(checkedIn("Density")).toEqual(["Compact"]);
  });

  it("SAP-11: density Compact sets data-density and persists", async () => {
    const user = userEvent.setup();
    renderSettings("/settings/appearance");
    expect(document.documentElement.dataset.density).toBe("comfortable");
    expect(localStorage.getItem("mkt:density")).toBe("comfortable");
    await user.click(screen.getByRole("radio", { name: "Compact" }));
    expect(document.documentElement.dataset.density).toBe("compact");
    expect(localStorage.getItem("mkt:density")).toBe("compact");
    await user.click(screen.getByRole("radio", { name: "Comfortable" }));
    expect(document.documentElement.dataset.density).toBe("comfortable");
  });

  // Bug: applyDensity only runs inside AppearanceTab, so a stored density is not applied
  // after a reload until the tab opens. Fix belongs at app boot (App.tsx / theme-boot.js).
  it.fails("SAP-12: a stored density applies on load, before the tab opens", () => {
    localStorage.setItem("mkt:density", "compact");
    renderSettings("/settings");
    expect(document.documentElement.dataset.density).toBe("compact");
  });

  it("SAP-13: storage blocked: the choices still apply this session", async () => {
    const user = userEvent.setup();
    const blocked = () => {
      throw new DOMException("blocked", "SecurityError");
    };
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(blocked);
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(blocked);
    renderSettings("/settings/appearance");
    await user.click(screen.getByRole("radio", { name: "Light" }));
    await user.click(screen.getByRole("radio", { name: "Compact" }));
    await user.click(screen.getByRole("radio", { name: "العربية" }));
    expect(document.documentElement.dataset.theme).toBe("light");
    expect(document.documentElement.dataset.density).toBe("compact");
    expect(document.documentElement).toHaveAttribute("dir", "rtl");
    expect(checkedIn("السمة")).toEqual(["فاتح"]);
    expect(checkedIn("اللغة")).toEqual(["العربية"]);
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("SAP-15: a signed-out deep link redirects to /login", () => {
    renderSettings("/settings/appearance", { user: null });
    expect(screen.getByText("login page (from /settings/appearance)")).toBeInTheDocument();
    expect(screen.queryByRole("radiogroup")).not.toBeInTheDocument();
  });

  it("SAP-16: a render crash shows the route error fallback; Back works", async () => {
    const user = userEvent.setup();
    quietCrash();
    flags.throwReadMode = true;
    renderSettings("/settings");
    await user.click(screen.getByRole("link", { name: "Appearance" }));
    expect(screen.getByRole("alert")).toHaveTextContent(en["common.pageError"]);
    await user.click(screen.getByRole("button", { name: "go back" }));
    expect(screen.getByText(/Signed in as/)).toBeInTheDocument();
    await user.click(screen.getByRole("link", { name: "Privacy" }));
    expect(await screen.findByRole("heading", { name: "Activity tracking" })).toBeInTheDocument();
  });

  it("SAP-17: arrow keys move within a radio group", async () => {
    const user = userEvent.setup();
    localStorage.setItem("mkt:theme", "light");
    renderSettings("/settings/appearance");
    const radios = within(screen.getByRole("radiogroup", { name: "Theme" })).getAllByRole("radio");
    expect(radios.map((r) => r.getAttribute("name"))).toEqual(["theme", "theme", "theme"]);
    radios[0].focus();
    await user.keyboard("{ArrowDown}");
    expect(checkedIn("Theme")).toEqual(["Dark"]);
    expect(document.documentElement.dataset.theme).toBe("dark");
    await user.keyboard("{ArrowDown}");
    expect(checkedIn("Theme")).toEqual(["System"]);
  });

  it("SAP-18: the radio groups and radios are labelled", () => {
    renderSettings("/settings/appearance");
    for (const g of ["Theme", "Language", "Density"]) {
      const group = screen.getByRole("radiogroup", { name: g });
      for (const r of within(group).getAllByRole("radio"))
        expect(r).toHaveAccessibleName(r.closest("label")!.textContent!);
    }
  });
});

// ─── Privacy tab ────────────────────────────────────────────────────────

function historyWith(items: unknown) {
  mockGets({ "/api/me/history": () => ({ items, limit: 20, offset: 0 }) });
}

describe("Settings privacy (test-cases/settings-privacy.md)", () => {
  it("SPR-01: shows the tracking switch and the watch history table", async () => {
    renderSettings("/settings/privacy");
    expect(screen.getByRole("heading", { name: "Activity tracking" })).toBeInTheDocument();
    expect(screen.getByText(/When off, your plays are not recorded/)).toBeInTheDocument();
    expect(await screen.findByRole("switch", { name: "Tracking on" })).toBeChecked();
    expect(screen.getByRole("heading", { name: "Watch history" })).toBeInTheDocument();
    const table = await screen.findByRole("table", { name: "Watch history" });
    const rows = within(table).getAllByRole("row");
    expect(
      within(rows[0])
        .getAllByRole("columnheader")
        .map((h) => h.textContent)
    ).toEqual(["Video", "Progress", "Watched", ""]);
    const cells = (r: HTMLElement) =>
      within(r)
        .getAllByRole("cell")
        .map((c) => c.textContent);
    expect(cells(rows[1])).toEqual(["Tafsir 1", "87%", "1h 30m", "Remove"]);
    expect(cells(rows[2])).toEqual(["Fiqh 2", "50%", "5m", "Remove"]);
    expect(getPaths()).toContain("/api/me/history?limit=20");
  });

  it("SPR-02: tracking off shows Tracking paused", async () => {
    mockGets({ "/api/me/activity/settings": { track_enabled: false } });
    renderSettings("/settings/privacy");
    expect(await screen.findByRole("switch", { name: "Tracking paused" })).not.toBeChecked();
  });

  it("SPR-03: turning tracking off saves and toasts; the server answer wins", async () => {
    const user = userEvent.setup();
    request.mockResolvedValueOnce({ track_enabled: false });
    renderSettings("/settings/privacy");
    await user.click(await screen.findByRole("switch", { name: "Tracking on" }));
    expect(request).toHaveBeenCalledTimes(1);
    expect(request).toHaveBeenCalledWith("/api/me/activity/settings", {
      method: "PUT",
      body: { track_enabled: false },
    });
    expect(await screen.findByRole("switch", { name: "Tracking paused" })).not.toBeChecked();
    expect(screen.getByRole("status")).toHaveTextContent("Privacy settings saved.");

    request.mockResolvedValueOnce({ track_enabled: false }); // asked for on, server keeps it off
    await user.click(screen.getByRole("switch", { name: "Tracking paused" }));
    await waitFor(() => expect(request).toHaveBeenCalledTimes(2));
    expect(await screen.findByRole("switch", { name: "Tracking paused" })).not.toBeChecked();
  });

  it("SPR-04: the switch is disabled while saving; no double request", async () => {
    const user = userEvent.setup();
    let finish!: (v: unknown) => void;
    request.mockReturnValue(new Promise((r) => (finish = r)));
    renderSettings("/settings/privacy");
    const sw = await screen.findByRole("switch", { name: "Tracking on" });
    await user.click(sw);
    await user.click(sw);
    expect(sw).toBeDisabled();
    expect(request).toHaveBeenCalledTimes(1);
    await act(async () => finish({ track_enabled: false }));
    expect(screen.getByRole("switch", { name: "Tracking paused" })).toBeEnabled();
  });

  it("SPR-05: a failed save (500 / 403 / network) toasts and keeps the old value", async () => {
    const user = userEvent.setup();
    const fails: [unknown, string][] = [
      [problem(500, "Internal Server Error", { detail: "db down" }), en["error.kind.server"]],
      [problem(403, "Forbidden"), en["error.kind.noAccess"]],
      [new TypeError("Failed to fetch"), en["error.kind.unreachable"]],
    ];
    renderSettings("/settings/privacy");
    for (const [err, text] of fails) {
      request.mockRejectedValueOnce(err);
      await user.click(await screen.findByRole("switch", { name: "Tracking on" }));
      expect(await screen.findByText(text)).toBeInTheDocument();
      const sw = screen.getByRole("switch", { name: "Tracking on" });
      expect(sw).toBeChecked();
      expect(sw).toBeEnabled();
    }
    expect(screen.getAllByRole("alert")).toHaveLength(3);
  });

  it("SPR-06: loading states before the data arrives", async () => {
    get.mockImplementation(() => new Promise(() => {}));
    renderSettings("/settings/privacy");
    expect(screen.getByRole("heading", { name: "Activity tracking" })).toBeInTheDocument();
    expect(screen.queryByRole("switch", { name: /^Tracking/ })).not.toBeInTheDocument();
    expect(screen.getByText("Loading…")).toBeInTheDocument();
  });

  it("SPR-07: a failed settings load says so instead of showing Tracking on", async () => {
    for (const err of [problem(500, "Internal Server Error"), new TypeError("Failed to fetch")]) {
      mockGets({ "/api/me/activity/settings": () => Promise.reject(err) });
      const { unmount } = renderSettings("/settings/privacy");
      expect(await screen.findByText("Something went wrong.")).toHaveAttribute("role", "alert");
      await screen.findByRole("table");
      expect(screen.queryByRole("switch", { name: /^Tracking/ })).not.toBeInTheDocument();
      expect(request).not.toHaveBeenCalled();
      unmount();
    }
  });

  it("SPR-08: an empty history shows the empty text", async () => {
    historyWith([]);
    renderSettings("/settings/privacy");
    expect(await screen.findByText("Nothing here yet.")).toBeInTheDocument();
    expect(screen.queryByRole("table")).not.toBeInTheDocument();
    expect(await screen.findByRole("switch", { name: "Tracking on" })).toBeEnabled();
  });

  it("SPR-09: items null shows the empty text, not Loading forever", async () => {
    historyWith(null);
    renderSettings("/settings/privacy");
    expect(await screen.findByText("Nothing here yet.")).toBeInTheDocument();
    expect(screen.queryByText("Loading…")).not.toBeInTheDocument();
  });

  it("SPR-10: items missing (or a bare array) shows the empty text, no crash", async () => {
    for (const body of [{}, { limit: 20 }, []]) {
      mockGets({ "/api/me/history": () => body });
      const { unmount } = renderSettings("/settings/privacy");
      expect(await screen.findByText("Nothing here yet.")).toBeInTheDocument();
      expect(screen.queryByRole("alert")).not.toBeInTheDocument();
      unmount();
    }
  });

  it("SPR-11: a failed history load shows an error, not an empty history", async () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    for (const err of [
      problem(500, "Internal Server Error"),
      problem(401, "Unauthorized"),
      new TypeError("Failed to fetch"),
      new SyntaxError("Unexpected token"),
    ]) {
      mockGets({ "/api/me/history": () => Promise.reject(err) });
      const { unmount } = renderSettings("/settings/privacy");
      expect(await screen.findByText("Something went wrong.")).toHaveAttribute("role", "alert");
      expect(screen.queryByText("Nothing here yet.")).not.toBeInTheDocument();
      unmount();
    }
  });

  it("SPR-12: missing or null optional fields in a row render without NaN", async () => {
    historyWith([
      { video_id: "v3", title: "", best_percent: null, total_watch_sec: null },
      { video_id: "v4" },
    ]);
    renderSettings("/settings/privacy");
    const table = await screen.findByRole("table");
    const rows = within(table).getAllByRole("row").slice(1);
    const cells = (r: HTMLElement) =>
      within(r)
        .getAllByRole("cell")
        .map((c) => c.textContent);
    expect(cells(rows[0])).toEqual(["v3", "0%", "0m", "Remove"]);
    expect(cells(rows[1])).toEqual(["v4", "0%", "0m", "Remove"]);
    expect(table.textContent).not.toMatch(/NaN/);
    expect(within(rows[0]).getByRole("link", { name: "v3" })).toHaveAttribute("href", "/videos/v3");
  });

  it("SPR-13: Remove deletes the item and drops its row", async () => {
    const user = userEvent.setup();
    del.mockResolvedValue(undefined);
    renderSettings("/settings/privacy");
    const row = (await screen.findByRole("link", { name: "Tafsir 1" })).closest("tr")!;
    await user.click(within(row).getByRole("button", { name: "Remove" }));
    expect(del).toHaveBeenCalledTimes(1);
    expect(del).toHaveBeenCalledWith("/api/me/history/v1");
    await waitFor(() => expect(screen.queryByText("Tafsir 1")).not.toBeInTheDocument());
    expect(screen.getByText("Fiqh 2")).toBeInTheDocument();
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
  });

  it("SPR-14: a failed Remove keeps the row and toasts", async () => {
    const user = userEvent.setup();
    renderSettings("/settings/privacy");
    const row = (await screen.findByRole("link", { name: "Tafsir 1" })).closest("tr")!;
    for (const [err, text] of [
      [problem(404, "Not Found"), en["error.kind.notFound"]],
      [new TypeError("Failed to fetch"), en["error.kind.unreachable"]],
    ] as [unknown, string][]) {
      del.mockRejectedValueOnce(err);
      await user.click(within(row).getByRole("button", { name: "Remove" }));
      expect(await screen.findByText(text)).toBeInTheDocument();
      expect(screen.getByText("Tafsir 1")).toBeInTheDocument();
    }
  });

  it("SPR-16: a title links to its video (id encoded); Back returns and refetches", async () => {
    const user = userEvent.setup();
    historyWith([...HISTORY.items, { ...HISTORY.items[1], video_id: "a/b?c", title: "Odd" }]);
    renderSettings("/settings/privacy", {
      extraRoutes: <Route path="/videos/:id" element={<p>video page</p>} />,
    });
    expect(await screen.findByRole("link", { name: "Odd" })).toHaveAttribute(
      "href",
      "/videos/a%2Fb%3Fc"
    );
    await user.click(screen.getByRole("link", { name: "Tafsir 1" }));
    expect(screen.getByText("video page")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "go back" }));
    expect(await screen.findByRole("table")).toBeInTheDocument();
    expect(getPaths().filter((p) => p.startsWith("/api/me/history"))).toHaveLength(2);
  });

  it("SPR-17: a signed-out deep link redirects to /login", () => {
    renderSettings("/settings/privacy", { user: null });
    expect(screen.getByText("login page (from /settings/privacy)")).toBeInTheDocument();
    expect(get).not.toHaveBeenCalled();
  });

  it("SPR-18: a render crash shows the route error fallback; Back works", async () => {
    const user = userEvent.setup();
    quietCrash();
    historyWith([{ ...HISTORY.items[0], title: { bad: true } }]);
    renderSettings("/settings");
    await user.click(screen.getByRole("link", { name: "Privacy" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(en["common.pageError"]);
    await user.click(screen.getByRole("button", { name: "go back" }));
    expect(screen.getByText(/Signed in as/)).toBeInTheDocument();
    mockGets();
    await user.click(screen.getByRole("link", { name: "Privacy" }));
    expect(await screen.findByRole("table")).toBeInTheDocument();
    await user.click(screen.getByRole("link", { name: "Appearance" }));
    expect(screen.getByRole("radiogroup", { name: "Theme" })).toBeInTheDocument();
  });

  it("SPR-19: Arabic: RTL, Arabic strings, Arabic titles", async () => {
    localStorage.setItem("mkt:locale", "ar");
    historyWith([{ ...HISTORY.items[0], title: "تفسير سورة البقرة" }]);
    renderSettings("/settings/privacy");
    expect(document.documentElement).toHaveAttribute("dir", "rtl");
    expect(screen.getByRole("heading", { name: "تتبّع النشاط" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "سجل المشاهدة" })).toBeInTheDocument();
    expect(await screen.findByRole("switch", { name: "التتبّع مُفعّل" })).toBeInTheDocument();
    const table = await screen.findByRole("table");
    for (const h of ["الفيديو", "التقدم", "تمت المشاهدة"])
      expect(within(table).getByRole("columnheader", { name: h })).toBeInTheDocument();
    expect(within(table).getByRole("button", { name: "إزالة" })).toBeInTheDocument();
    const title = within(table).getByRole("link", { name: "تفسير سورة البقرة" });
    expect(title.closest("td")).toHaveAttribute("dir", "auto");
  });

  it("SPR-21: Space on the switch saves; each Remove is a button", async () => {
    const user = userEvent.setup();
    request.mockResolvedValue({ track_enabled: false });
    renderSettings("/settings/privacy");
    const sw = await screen.findByRole("switch", { name: "Tracking on" });
    sw.focus();
    await user.keyboard(" ");
    await waitFor(() => expect(request).toHaveBeenCalledTimes(1));
    expect(await screen.findAllByRole("button", { name: "Remove" })).toHaveLength(2);
  });

  // a11y gap: every Remove is just "Remove"; wanted "Remove <title>" (needs an i18n key).
  it.fails("SPR-21: each Remove button names its row", async () => {
    renderSettings("/settings/privacy");
    await screen.findByRole("table");
    expect(screen.getByRole("button", { name: /Remove Tafsir 1/ })).toBeInTheDocument();
  });
});

// ─── About tab ──────────────────────────────────────────────────────────

// About says what the app is (story 28.1, reworked): name, tagline, version and build,
// the home and this machine, the plan, links and credits. Updates get one line, from the
// same source and in the same words as the Updates page, and a link there.
describe("Settings about (test-cases/settings-about.md)", () => {
  const aboutGets = (over: Table = {}) =>
    mockGets({
      "/api/system/version": { ...VERSION, version: "v1.5.0", build_date: "1793147514" },
      "/api/auth/me": { home_name: "Studio" },
      "/api/entitlements": { tier: "pro" },
      "/api/cluster/updates": overview(),
      ...over,
    });

  it("SAB-01: what the app is: name, tagline, version, build, channel, machine, plan, app", async () => {
    aboutGets();
    renderSettings("/settings/about", { user: memberUser });
    expect(await screen.findByRole("heading", { level: 2, name: "Maktaba" })).toBeInTheDocument();
    expect(screen.getByText(/Your home's video library/)).toBeInTheDocument();
    // the version as the Updates page writes it: no "v"
    await waitFor(() => expect(screen.getByTestId("about-version")).toHaveTextContent(/^1\.5\.0$/));
    expect(screen.getByTestId("about-commit")).toHaveTextContent(/^abcdef1234$/);
    // a build date in Unix seconds reads as a date, never the raw number
    expect(screen.getByTestId("about-built")).not.toHaveTextContent("1793147514");
    expect(screen.getByTestId("about-built")).toHaveTextContent("2026");
    expect(metaValue("Update channel")).toHaveTextContent("Stable");
    expect(await screen.findByTestId("about-machine")).toHaveTextContent("Studio · the home");
    expect(await screen.findByTestId("about-plan")).toHaveTextContent("Pro");
    expect(metaValue("App")).toHaveTextContent("Web app");
    const links = within(screen.getByRole("region", { name: "Links" }));
    expect(links.getByRole("link", { name: "Project website" })).toHaveAttribute(
      "href",
      "https://github.com/Hamza-Labs-Core/Maktaba"
    );
    expect(links.getByRole("link", { name: "Help" })).toHaveAttribute("href", "/help");
    expect(links.getByRole("link", { name: "Privacy" })).toHaveAttribute(
      "href",
      "/settings/privacy"
    );
    expect(screen.getByRole("heading", { name: "Acknowledgements" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "PostgreSQL" })).toBeInTheDocument();
  });

  it("SAB-02: the version API fails: the build's own version", async () => {
    const build = (import.meta.env?.VITE_APP_VERSION ?? "dev").replace(/^v(?=\d)/, "");
    aboutGets({
      "/api/system/version": () => Promise.reject(problem(500, "Internal Server Error")),
    });
    renderSettings("/settings/about", { user: memberUser });
    expect(await screen.findByTestId("about-version")).toHaveTextContent(build);
    expect(screen.queryByTestId("about-commit")).toBeNull();
  });

  it("SAB-04: a viewer gets one update line from the status, no Updates link, no update controls", async () => {
    aboutGets();
    const { unmount } = renderSettings("/settings/about", { user: memberUser });
    const line = await screen.findByTestId("about-update-status");
    expect(line).toHaveTextContent("up to date");
    expect(screen.queryByTestId("about-updates-link")).toBeNull();
    expect(
      screen.queryByRole("button", { name: /Update now|Check now|Save update settings/ })
    ).toBeNull();
    expect(getPaths()).not.toContain("/api/cluster/updates");
    unmount();

    aboutGets({ "/api/system/updates": AVAILABLE });
    renderSettings("/settings/about", { user: memberUser });
    const avail = await screen.findByTestId("about-update-status");
    await waitFor(() => expect(avail).toHaveTextContent("update available"));
    expect(avail).toHaveTextContent("1.6.0");
  });

  it("SAB-05: an admin's line is the Updates page's: same status, same version, a link there", async () => {
    aboutGets();
    renderSettings("/settings/about");
    const line = await screen.findByTestId("about-update-status");
    await waitFor(() => expect(line).toHaveTextContent("update available"));
    expect(line).toHaveTextContent("1.6.0");
    expect(within(line).getByRole("link", { name: "Open Updates" })).toHaveAttribute(
      "href",
      "/admin/updates"
    );
    // everything else about updates is on the Updates page only
    expect(screen.queryByText("Automatic updates")).toBeNull();
    expect(screen.queryByRole("button", { name: /Update now|Check now/ })).toBeNull();
  });

  it("SAB-05: on a joined machine, the line is that machine's: behind, and the release it catches up to", async () => {
    aboutGets({
      "/api/system/version": { ...VERSION, role: "node", node_id: "pc" },
      "/api/cluster/updates": overview({
        current_version: "1.6.0",
        watermark: "1.6.0",
        plan: { held: false, stepping_stone: false, auto_install: true, due: false },
        machines: [
          { id: "home", name: "home", role: "home", version: "1.6.0", state: "up_to_date" },
          {
            id: "pc",
            name: "windows-pc",
            role: "node",
            version: "1.5.0",
            state: "behind",
            next: { version: "1.6.0", reason: "catch_up_window", at: "2026-10-22T07:00:00Z" },
          },
        ],
      }),
    });
    renderSettings("/settings/about");
    const line = await screen.findByTestId("about-update-status");
    await waitFor(() => expect(line).toHaveTextContent("behind"));
    expect(line).toHaveTextContent("1.6.0");
    expect(await screen.findByTestId("about-machine")).toHaveTextContent("A machine of Studio");
  });

  it("SAB-07: Copy version info copies the version, build and machine", async () => {
    aboutGets();
    const writeText = vi.fn().mockResolvedValue(undefined);
    const user = userEvent.setup();
    // after userEvent's setup, which puts its own clipboard in place
    Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });
    vi.stubGlobal("isSecureContext", true);
    renderSettings("/settings/about");
    await screen.findByTestId("about-commit");
    await user.click(screen.getByRole("button", { name: "Copy version info" }));
    await waitFor(() => expect(writeText).toHaveBeenCalled());
    const text = String(writeText.mock.calls[0]?.[0]);
    expect(text).toContain("Maktaba: 1.5.0");
    expect(text).toContain("Commit: abcdef1234567890");
    expect(text).toContain("Machine: home");
    expect(await screen.findByText("Version info copied.")).toBeInTheDocument();
  });

  it("SAB-21: a signed-out deep link redirects to /login with no update calls", () => {
    renderSettings("/settings/about", { user: null });
    expect(getPaths().filter((p) => p.includes("updates"))).toEqual([]);
  });

  it("SAB-23: Arabic: RTL, Arabic strings, versions left as they are", async () => {
    aboutGets();
    localStorage.setItem("mkt:locale", "ar");
    renderSettings("/settings/about");
    expect(await screen.findByRole("heading", { level: 2, name: "مكتبة" })).toBeInTheDocument();
    expect(document.documentElement.dir).toBe("rtl");
    const line = await screen.findByTestId("about-update-status");
    await waitFor(() => expect(line).toHaveTextContent("يتوفر تحديث"));
    expect(within(line).getByText("1.6.0")).toHaveAttribute("dir", "ltr");
    expect(screen.getByTestId("about-version")).toHaveAttribute("dir", "ltr");
    expect(screen.getByRole("heading", { name: "شكر وتقدير" })).toBeInTheDocument();
  });
});
