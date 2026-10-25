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
import { Settings, safeReleaseUrl } from "./Settings";

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
      { status: 500, statusText: "Internal Server Error" },
      { status: 403, statusText: "Forbidden" },
      { status: 401, statusText: "Unauthorized" },
    ];
    let i = 0;
    const { names } = stubDownload(async () => new Response("boom", answers[i++]));
    renderSettings("/settings");
    for (const a of answers) {
      await user.click(screen.getByRole("button", { name: "Collect diagnostics" }));
      expect(
        await screen.findByText(`Could not collect diagnostics: ${a.statusText}: boom`)
      ).toBeInTheDocument();
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
      "Could not collect diagnostics: Something went wrong."
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
    ["About", "/settings/about"],
  ];

  it("SAC-12: the tab nav lists all seven tabs with the right hrefs", () => {
    for (const path of ["/settings", "/settings/rules"]) {
      const { unmount } = renderSettings(path);
      const nav = screen.getByRole("navigation", { name: "Settings" });
      const links = within(nav).getAllByRole("link");
      expect(links.map((l) => [l.textContent, l.getAttribute("href")])).toEqual(TABS);
      unmount();
    }
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
        ["/api/me/"],
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
        "/settings/about",
        () => screen.findByText("Current version"),
        ["/api/system/", "/api/cluster/updates"],
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
    ).toEqual(["الحساب", "الجلسات", "المظهر", "الخصوصية", "النماذج", "القواعد", "حول"]);
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
    expect(screen.getByRole("alert")).toHaveTextContent("boom");
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
    expect(screen.getByRole("alert")).toHaveTextContent("boom");
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
      [
        problem(500, "Internal Server Error", { detail: "db down" }),
        "Internal Server Error: db down",
      ],
      [problem(403, "Forbidden"), "Forbidden"],
      [new TypeError("Failed to fetch"), "Something went wrong."],
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
    expect(screen.queryByRole("switch")).not.toBeInTheDocument();
    expect(screen.getByText("Loading…")).toBeInTheDocument();
  });

  it("SPR-07: a failed settings load says so instead of showing Tracking on", async () => {
    for (const err of [problem(500, "Internal Server Error"), new TypeError("Failed to fetch")]) {
      mockGets({ "/api/me/activity/settings": () => Promise.reject(err) });
      const { unmount } = renderSettings("/settings/privacy");
      expect(await screen.findByText("Something went wrong.")).toHaveAttribute("role", "alert");
      await screen.findByRole("table");
      expect(screen.queryByRole("switch")).not.toBeInTheDocument();
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
      [problem(404, "Not Found"), "Not Found"],
      [new TypeError("Failed to fetch"), "Something went wrong."],
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
    expect(await screen.findByRole("alert")).toHaveTextContent("Objects are not valid");
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

/** Fake timers that still let promises and userEvent run. */
function fakeClock() {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  return userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
}
const tick = (ms: number) => act(async () => void (await vi.advanceTimersByTimeAsync(ms)));

/** /api/system/version answers: the first is the page load, the rest are polls. */
function versionAnswers(...answers: unknown[]) {
  let i = 0;
  return () => {
    const a = answers[Math.min(i++, answers.length - 1)];
    return a instanceof Error ? Promise.reject(a) : a;
  };
}

describe("Settings about (test-cases/settings-about.md)", () => {
  it("SAB-01: shows version, commit, build date, channel and last checked", async () => {
    renderSettings("/settings/about", { user: memberUser });
    await waitFor(() => expect(metaValue("Current version")).toHaveTextContent("1.5.0"));
    expect(metaValue("Commit")).toHaveTextContent("abcdef1234");
    expect(metaValue("Commit")).not.toHaveTextContent("abcdef12345");
    expect(metaValue("Build date")).toHaveTextContent("2026-10-01");
    expect(metaValue("Update channel")).toHaveTextContent("Stable");
    const at = new Intl.DateTimeFormat("en", { dateStyle: "medium", timeStyle: "short" }).format(
      new Date(UP_TO_DATE.checked_at)
    );
    expect(await screen.findByText("Last checked")).toBeInTheDocument();
    expect(metaValue("Last checked")).toHaveTextContent(at);
    expect(screen.getByRole("button", { name: "Check now" })).toBeInTheDocument();
    expect(screen.getByText("You're up to date.")).toBeInTheDocument();
  });

  it("SAB-02: the version API fails: build version fallback", async () => {
    const user = userEvent.setup();
    const build = import.meta.env?.VITE_APP_VERSION ?? "dev";
    mockGets({
      "/api/system/version": () => Promise.reject(problem(500, "Internal Server Error")),
      "/api/system/updates": { ...UP_TO_DATE, channel: "beta" },
    });
    const { unmount } = renderSettings("/settings/about", { user: memberUser });
    expect(await screen.findByText("You're up to date.")).toBeInTheDocument();
    expect(metaValue("Current version")).toHaveTextContent(build);
    expect(metaValue("Update channel")).toHaveTextContent("Beta");
    expect(screen.queryByText("Commit")).not.toBeInTheDocument();
    expect(screen.queryByText("Build date")).not.toBeInTheDocument();
    unmount();

    mockGets({
      "/api/system/version": () => Promise.reject(new TypeError("Failed to fetch")),
      "/api/system/updates": (url: string) =>
        url.includes("refresh=true") ? UP_TO_DATE : Promise.reject(new TypeError("down")),
    });
    renderSettings("/settings/about", { user: memberUser });
    await waitFor(() => expect(get).toHaveBeenCalledWith("/api/system/updates"));
    expect(metaValue("Current version")).toHaveTextContent(build);
    expect(metaValue("Update channel")).toHaveTextContent("Stable");
    expect(screen.queryByText("You're up to date.")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Check now" }));
    expect(await screen.findByText("You're up to date.")).toBeInTheDocument();
  });

  it("SAB-03: missing or unknown optional version fields are hidden", async () => {
    for (const v of [
      { version: "1.5.0", commit: "unknown", build_date: "unknown" },
      { version: "1.5.0", commit: null, build_date: "" },
    ]) {
      mockGets({
        "/api/system/version": v,
        "/api/system/updates": { ...UP_TO_DATE, channel: "beta" },
      });
      const { unmount } = renderSettings("/settings/about", { user: memberUser });
      await waitFor(() => expect(metaValue("Update channel")).toHaveTextContent("Beta"));
      expect(screen.queryByText("Commit")).not.toBeInTheDocument();
      expect(screen.queryByText("Build date")).not.toBeInTheDocument();
      unmount();
    }
  });

  it("SAB-04: up to date / checks disabled messages", async () => {
    const cases: [object, string | null][] = [
      [UP_TO_DATE, "You're up to date."],
      [{ ...UP_TO_DATE, disabled: true }, "Automatic update checks are off."],
      [{ ...UP_TO_DATE, available: true }, null],
    ];
    for (const [upd, text] of cases) {
      mockGets({ "/api/system/updates": upd });
      const { unmount } = renderSettings("/settings/about", { user: memberUser });
      await waitFor(() => expect(screen.getByText("Last checked")).toBeInTheDocument());
      if (text) expect(screen.getByText(text)).toBeInTheDocument();
      if (text !== "You're up to date.")
        expect(screen.queryByText("You're up to date.")).not.toBeInTheDocument();
      if (!text) {
        expect(screen.queryByText("Automatic update checks are off.")).not.toBeInTheDocument();
        expect(screen.queryByRole("region", { name: "Updates" })).not.toBeInTheDocument();
      }
      unmount();
    }
  });

  it("SAB-05: the update available card with notes and the release link", async () => {
    mockGets({ "/api/system/updates": AVAILABLE });
    renderSettings("/settings/about", { user: memberUser });
    const card = await screen.findByRole("region", { name: "Updates" });
    expect(within(card).getByRole("heading", { level: 2 })).toHaveTextContent(
      "Update available: 1.6.0"
    );
    const summary = card.querySelector("details > summary")!;
    expect(summary).toHaveTextContent("Release notes");
    // Markdown: the two bullets are a list
    expect(Array.from(card.querySelectorAll("details li")).map((li) => li.textContent)).toEqual([
      "faster",
      "fixes",
    ]);
    const link = within(card).getByRole("link", { name: "View release" });
    expect(link).toHaveAttribute("href", AVAILABLE.release_url);
    expect(link).toHaveAttribute("target", "_blank");
    expect(link).toHaveAttribute("rel", "noreferrer");
  });

  it("SAB-06: release-note HTML is shown as text, never injected; a javascript: URL is not a link", async () => {
    const notes = "<img src=x onerror=alert(1)><b>bold</b>";
    mockGets({
      "/api/system/updates": {
        ...AVAILABLE,
        release_notes: notes,
        release_url: "javascript:alert(1)",
      },
    });
    renderSettings("/settings/about", { user: memberUser });
    const card = await screen.findByRole("region", { name: "Updates" });
    expect(card.querySelector(".mkt-about__notes-body")!.textContent).toBe(notes);
    expect(card.querySelector("img")).toBeNull();
    expect(card.querySelector("b")).toBeNull();
    expect(within(card).queryByRole("link")).not.toBeInTheDocument();
    expect(document.querySelector('a[href^="javascript:"]')).toBeNull();
  });

  it("SAB-06: only http(s) release URLs become links (javascript:, data:, relative junk do not)", async () => {
    for (const bad of [
      "javascript:alert(1)",
      " JavaScript:alert(1)",
      "data:text/html,<script>alert(1)</script>",
      "vbscript:x",
      "/releases/v1.6.0",
      "not a url",
      "",
    ]) {
      expect(safeReleaseUrl(bad)).toBeNull();
      mockGets({ "/api/system/updates": { ...AVAILABLE, release_url: bad } });
      const { unmount } = renderSettings("/settings/about", { user: memberUser });
      const card = await screen.findByRole("region", { name: "Updates" });
      expect(within(card).queryByRole("link", { name: "View release" })).not.toBeInTheDocument();
      unmount();
    }
    expect(safeReleaseUrl(42)).toBeNull();
    expect(safeReleaseUrl("http://example.com/r")).toBe("http://example.com/r");
    expect(safeReleaseUrl(AVAILABLE.release_url)).toBe(AVAILABLE.release_url);
  });

  it("SAB-07: Check now refreshes, shows busy, and toasts errors", async () => {
    const user = userEvent.setup();
    const refresh = vi.fn();
    mockGets({
      "/api/system/updates": (url: string) =>
        url.includes("refresh=true") ? refresh() : UP_TO_DATE,
    });
    renderSettings("/settings/about", { user: memberUser });
    await screen.findByText("You're up to date.");

    // (d) pending, then (a) an update appears
    let finish!: (v: unknown) => void;
    refresh.mockReturnValueOnce(new Promise((r) => (finish = r)));
    await user.click(screen.getByRole("button", { name: "Check now" }));
    expect(screen.getByRole("button", { name: "Checking…" })).toBeDisabled();
    await act(async () => finish({ ...AVAILABLE, checked_at: "2026-10-19T09:30:00Z" }));
    expect(await screen.findByRole("region", { name: "Updates" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Check now" })).toBeEnabled();
    expect(get).toHaveBeenCalledWith("/api/system/updates?refresh=true");

    // (b) a problem: toast, old status kept
    refresh.mockImplementationOnce(() =>
      Promise.reject(problem(500, "Internal Server Error", { detail: "github rate limited" }))
    );
    await user.click(screen.getByRole("button", { name: "Check now" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Internal Server Error: github rate limited"
    );
    expect(screen.getByRole("region", { name: "Updates" })).toBeInTheDocument();

    // (c) network
    refresh.mockImplementationOnce(() => Promise.reject(new TypeError("Failed to fetch")));
    await user.click(screen.getByRole("button", { name: "Check now" }));
    expect(await screen.findByText("Something went wrong.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Check now" })).toBeEnabled();
  });

  it("SAB-08: a non-admin sees no Update now and no automatic updates card", async () => {
    mockGets({ "/api/system/updates": AVAILABLE, "/api/cluster/updates": overview() });
    const { unmount } = renderSettings("/settings/about", { user: memberUser });
    const card = await screen.findByRole("region", { name: "Updates" });
    expect(within(card).getByRole("link", { name: "View release" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Update now" })).not.toBeInTheDocument();
    expect(screen.queryByText("Automatic updates")).not.toBeInTheDocument();
    expect(getPaths()).not.toContain("/api/cluster/updates");
    unmount();

    renderSettings("/settings/about", { user: adminUser });
    expect(await screen.findByRole("button", { name: "Update now" })).toBeInTheDocument();
    expect(await screen.findByRole("heading", { name: "Automatic updates" })).toBeInTheDocument();
  });

  it("SAB-09: admin Update now: downloading, restarting, done", async () => {
    const user = fakeClock();
    mockGets({
      "/api/system/updates": AVAILABLE,
      "/api/system/version": versionAnswers(
        VERSION,
        new Error("restarting"),
        { version: "1.5.0" },
        { version: "1.6.0" }
      ),
    });
    let posted!: (v: unknown) => void;
    post.mockReturnValue(new Promise((r) => (posted = r)));
    renderSettings("/settings/about", { user: adminUser });
    await user.click(await screen.findByRole("button", { name: "Update now" }));
    expect(post).toHaveBeenCalledTimes(1);
    expect(post).toHaveBeenCalledWith("/api/admin/system/update", { confirm: true });
    expect(screen.getByRole("button", { name: "Downloading…" })).toBeDisabled();
    expect(screen.getByRole("status")).toHaveTextContent("Downloading…");
    await act(async () => posted({}));
    expect(screen.getByRole("status")).toHaveTextContent("Restarting…");
    await tick(2000);
    await tick(2000);
    expect(screen.queryByText(/Updated\./)).not.toBeInTheDocument();
    await tick(2000);
    expect(await screen.findByText("Updated. Now running 1.6.0.")).toBeInTheDocument();
    expect(metaValue("Current version")).toHaveTextContent("1.6.0");
    expect(screen.queryByRole("button", { name: /Update now|Restarting/ })).not.toBeInTheDocument();
  });

  it("SAB-10: an update whose version never changes is reported as rolled back", async () => {
    const user = fakeClock();
    mockGets({ "/api/system/updates": AVAILABLE, "/api/system/version": VERSION });
    post.mockResolvedValue({});
    renderSettings("/settings/about", { user: adminUser });
    await user.click(await screen.findByRole("button", { name: "Update now" }));
    await tick(62_000);
    expect(await screen.findByText("Update failed and was rolled back.")).toBeInTheDocument();
    expect(screen.queryByText(/Updated\. Now running/)).not.toBeInTheDocument();
    expect(metaValue("Current version")).toHaveTextContent("1.5.0");
  });

  it("SAB-10: with no version at load, polling waits for a version other than the installed one", async () => {
    const user = fakeClock();
    mockGets({
      "/api/system/updates": AVAILABLE,
      "/api/system/version": versionAnswers(
        new Error("down"),
        { version: "1.5.0" },
        { version: "1.6.0" }
      ),
    });
    post.mockResolvedValue({});
    renderSettings("/settings/about", { user: adminUser });
    await user.click(await screen.findByRole("button", { name: "Update now" }));
    await tick(2000);
    expect(screen.queryByText(/Updated\./)).not.toBeInTheDocument();
    await tick(2000);
    expect(await screen.findByText("Updated. Now running 1.6.0.")).toBeInTheDocument();
  });

  it("SAB-11: a failed Update now (500 / 403 / network) can be tried again", async () => {
    const user = userEvent.setup();
    mockGets({ "/api/system/updates": AVAILABLE });
    renderSettings("/settings/about", { user: adminUser });
    const fails = [
      problem(500, "Internal Server Error"),
      problem(403, "Forbidden"),
      new TypeError("Failed to fetch"),
    ];
    for (const err of fails) {
      post.mockRejectedValueOnce(err);
      await user.click(await screen.findByRole("button", { name: "Update now" }));
      expect(await screen.findByText("Update failed.")).toHaveClass("mkt-error");
      expect(screen.getByRole("button", { name: "Update now" })).toBeEnabled();
    }
    expect(post).toHaveBeenCalledTimes(3);
  });

  it("SAB-12: a Docker / package install 409 shows the instruction, no polling", async () => {
    const user = userEvent.setup();
    mockGets({ "/api/system/updates": AVAILABLE });
    const runs: [Record<string, unknown>, string | null][] = [
      [
        { instructions: "docker compose pull && docker compose up -d" },
        "docker compose pull && docker compose up -d",
      ],
      [{ detail: "apt upgrade maktaba" }, "apt upgrade maktaba"],
      [{}, null],
    ];
    for (const [extra, hint] of runs) {
      get.mockClear();
      post.mockRejectedValueOnce(problem(409, "Conflict", extra));
      const { unmount } = renderSettings("/settings/about", { user: adminUser });
      await user.click(await screen.findByRole("button", { name: "Update now" }));
      if (hint) {
        expect(await screen.findByText("This is a Docker install. Run:")).toBeInTheDocument();
        expect(screen.getByText(hint).tagName).toBe("PRE");
        expect(screen.queryByRole("button", { name: "Update now" })).not.toBeInTheDocument();
        expect(screen.queryByText("Update failed.")).not.toBeInTheDocument();
      } else {
        expect(await screen.findByText("Update failed.")).toBeInTheDocument();
      }
      expect(getPaths().filter((p) => p === "/api/system/version")).toHaveLength(1);
      unmount();
    }
  });

  it("SAB-13: leaving the tab stops the polling", async () => {
    const user = fakeClock();
    mockGets({ "/api/system/updates": AVAILABLE, "/api/system/version": VERSION });
    post.mockResolvedValue({});
    renderSettings("/settings/about", { user: adminUser });
    await user.click(await screen.findByRole("button", { name: "Update now" }));
    await tick(4000);
    await user.click(screen.getByRole("link", { name: "Account" }));
    const polls = () => getPaths().filter((p) => p === "/api/system/version").length;
    const before = polls();
    await tick(60_000);
    expect(polls()).toBeLessThanOrEqual(before + 1);
    await user.click(screen.getByRole("link", { name: "About" }));
    expect(await screen.findByRole("button", { name: "Update now" })).toBeEnabled();
    expect(screen.queryByText("Restarting…")).not.toBeInTheDocument();
  });

  it("SAB-21: a signed-out deep link redirects to /login with no update calls", () => {
    renderSettings("/settings/about", { user: null });
    expect(screen.getByText("login page (from /settings/about)")).toBeInTheDocument();
    expect(get).not.toHaveBeenCalled();
  });

  it("SAB-22: a render crash shows the route error fallback; Back works", async () => {
    const user = userEvent.setup();
    quietCrash();
    mockGets({ "/api/system/updates": { ...AVAILABLE, release_notes: { md: "x" } } });
    renderSettings("/settings/models", { user: memberUser });
    await screen.findByRole("heading", { name: "Model Management" });
    await user.click(screen.getByRole("link", { name: "About" }));
    expect(await screen.findByRole("alert")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "go back" }));
    expect(await screen.findByRole("heading", { name: "Model Management" })).toBeInTheDocument();
    await user.click(screen.getByRole("link", { name: "go elsewhere" }));
    expect(screen.getByText("elsewhere")).toBeInTheDocument();
  });

  it("SAB-23: Arabic: RTL, Arabic strings, versions left as they are", async () => {
    localStorage.setItem("mkt:locale", "ar");
    mockGets({ "/api/system/updates": AVAILABLE, "/api/cluster/updates": overview() });
    renderSettings("/settings/about", { user: adminUser });
    expect(document.documentElement).toHaveAttribute("dir", "rtl");
    expect(await screen.findByText("يتوفر تحديث: 1.6.0")).toBeInTheDocument();
    expect(metaValue("الإصدار الحالي")).toHaveTextContent("1.5.0");
    expect(metaValue("قناة التحديث")).toHaveTextContent("مستقر");
    expect(screen.getByRole("button", { name: "تحقق الآن" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "حدّث الآن" })).toBeInTheDocument();
    expect(await screen.findByRole("heading", { name: "التحديثات التلقائية" })).toBeInTheDocument();
    const at = new Intl.DateTimeFormat("ar-EG", { dateStyle: "medium", timeStyle: "short" }).format(
      new Date(AVAILABLE.checked_at)
    );
    expect(screen.getByText(at)).toBeInTheDocument();
    expect(document.body.textContent).not.toMatch(/\bupdates?\.[a-z]+\./);
  });

  it("SAB-25: labelled regions, inputs and live phase texts", async () => {
    const user = userEvent.setup();
    mockGets({ "/api/system/updates": AVAILABLE, "/api/cluster/updates": overview() });
    post.mockReturnValue(new Promise(() => {}));
    renderSettings("/settings/about", { user: adminUser });
    expect(await screen.findByRole("region", { name: "Updates" })).toBeInTheDocument();
    const auto = await screen.findByRole("region", { name: "Automatic updates" });
    expect(
      within(auto).getByRole("switch", { name: /Install updates automatically/ })
    ).toBeInTheDocument();
    expect(within(auto).getByRole("combobox", { name: "Release channel" })).toBeInTheDocument();
    expect(within(auto).getAllByRole("textbox")).toHaveLength(3);
    for (const tb of within(auto).getAllByRole("textbox")) expect(tb).toHaveAccessibleName();
    await user.click(screen.getByRole("button", { name: "Update now" }));
    expect(screen.getByRole("status")).toHaveTextContent("Downloading…");
  });
});
