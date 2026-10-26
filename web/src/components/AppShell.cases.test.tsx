// App shell and More menu cases from web/test-cases/app-shell.md and
// web/test-cases/more-menu.md.
//
// The harness mounts the shell the way App.tsx does (AuthProvider with the
// real /api/auth/me probe, RequireAuth, AppShell, ShortcutProvider, the real
// Login page) with stub pages, so the shell itself is what is tested. A stub
// page throws while its path is in `crash.paths`.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {
  Link,
  MemoryRouter,
  Navigate,
  Route,
  Routes,
  useLocation,
  useNavigate,
} from "react-router-dom";
import { useEffect, type ComponentProps, type ReactNode } from "react";

const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock("../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../lib/api")>("../lib/api");
  return { ...actual, api: { ...actual.api, get, post } };
});
const { purge } = vi.hoisted(() => ({ purge: vi.fn() }));
vi.mock("../lib/sw", async () => {
  const actual = await vi.importActual<typeof import("../lib/sw")>("../lib/sw");
  return { ...actual, purgeApiCacheOnLogout: purge };
});
const { crash } = vi.hoisted(() => ({ crash: { paths: new Set<string>(), bell: false } }));
vi.mock("./NotificationsBell", async () => {
  const actual = await vi.importActual<typeof import("./NotificationsBell")>("./NotificationsBell");
  return {
    ...actual,
    NotificationsBell: () => {
      if (crash.bell) throw new Error("bell boom");
      return <actual.NotificationsBell />;
    },
  };
});
const { drop } = vi.hoisted(() => ({
  drop: { handler: null as null | ((f: { accepted: string[]; rejected: string[] }) => void) },
}));
vi.mock("../lib/desktop", async () => {
  const actual = await vi.importActual<typeof import("../lib/desktop")>("../lib/desktop");
  return {
    ...actual,
    registerMenuRouter: async () => () => {},
    registerDropHandler: async (cb: (f: { accepted: string[]; rejected: string[] }) => void) => {
      drop.handler = cb;
      return () => {};
    },
  };
});

import { AppShell, MoreMenu } from "./AppShell";
import { AdminGate } from "./AdminGate";
import { DesktopBridge } from "./DesktopBridge";
import { ApiError, api } from "../lib/api";
import { AuthProvider, RequireAuth, type User } from "../lib/auth";
import { I18nProvider } from "../lib/i18n";
import { ShortcutProvider } from "../lib/keyboard/shortcuts";
import { ToastProvider } from "@ds/components/Toast/Toast";
import { Login } from "../pages/Login";
import { NotFound } from "../pages/NotFound";
import en from "../i18n/en.json";
import ar from "../i18n/ar.json";

type InitialEntry = NonNullable<ComponentProps<typeof MemoryRouter>["initialEntries"]>[number];

const viewer: User = { id: "u2", username: "viewer", is_admin: false };
const root: User = { id: "u1", username: "root", is_admin: true };

const problem = (status: number) => new ApiError({ type: "about:blank", title: "Error", status });

// --- the stub app -----------------------------------------------------------

function Page() {
  const { pathname } = useLocation();
  if (crash.paths.has(pathname)) throw new Error(`boom at ${pathname}`);
  return <h1>page {pathname}</h1>;
}

/** /series loads its data, so a 401 there is a session that ran out. */
function SeriesPage() {
  useEffect(() => {
    void api.get("/api/series").catch(() => {});
  }, []);
  return <Page />;
}

function CrashGate({ children }: { children: ReactNode }) {
  const { pathname } = useLocation();
  if (crash.paths.has(pathname)) throw new Error(`boom at ${pathname}`);
  return <>{children}</>;
}

function TestBar() {
  const loc = useLocation();
  const nav = useNavigate();
  const from = (loc.state as { from?: string } | null)?.from ?? "";
  return (
    <div data-testid="testbar">
      <p data-testid="where">{loc.pathname + loc.search}</p>
      <p data-testid="from">{from}</p>
      <button type="button" onClick={() => nav(-1)}>
        test back
      </button>
      <button type="button" onClick={() => nav(1)}>
        test forward
      </button>
      <Link to="/videos/good">test good video</Link>
    </div>
  );
}

const PAGES = [
  "/library",
  "/search",
  "/guide",
  "/queue",
  "/transcription",
  "/settings/*",
  "/account",
  "/account/devices",
  "/account/sessions",
  "/account/tokens",
  "/account/history",
  "/billing",
  "/cloud/status",
  "/videos/:videoId",
  "/admin/library-acl",
  "/admin/channels",
  "/admin/filler",
  "/admin/analytics",
  "/admin/audit",
  "/admin/health",
  "/admin/machines",
  "/admin/updates",
  "/admin/devices",
  "/admin/extensions",
];

function renderShell(entries: InitialEntry[]) {
  return render(
    <I18nProvider>
      <ToastProvider>
        <AuthProvider>
          <MemoryRouter initialEntries={entries}>
            <ShortcutProvider>
              <Routes>
                <Route path="/login" element={<Login />} />
                <Route
                  element={
                    <RequireAuth>
                      <AppShell />
                    </RequireAuth>
                  }
                >
                  <Route index element={<Navigate to="/library" replace />} />
                  {PAGES.map((p) => (
                    <Route key={p} path={p} element={<Page />} />
                  ))}
                  <Route path="/series" element={<SeriesPage />} />
                  <Route
                    path="/more"
                    element={
                      <CrashGate>
                        <MoreMenu />
                      </CrashGate>
                    }
                  />
                  {["/admin/users", "/admin/live-tv", "/admin/logs"].map((p) => (
                    <Route
                      key={p}
                      path={p}
                      element={
                        <AdminGate>
                          <Page />
                        </AdminGate>
                      }
                    />
                  ))}
                  <Route path="*" element={<NotFound />} />
                </Route>
              </Routes>
              <TestBar />
            </ShortcutProvider>
          </MemoryRouter>
        </AuthProvider>
      </ToastProvider>
    </I18nProvider>
  );
}

// --- API answers ------------------------------------------------------------

let me: () => Promise<unknown>;
let notifications: () => Promise<unknown>;

function signedIn(u: User | Record<string, unknown>) {
  me = async () => ({ user: u });
}
function signedOut() {
  me = () => Promise.reject(problem(401));
}

beforeEach(() => {
  vi.spyOn(window, "scrollTo").mockImplementation(() => {});
  crash.paths.clear();
  crash.bell = false;
  drop.handler = null;
  get.mockReset();
  post.mockReset();
  purge.mockReset();
  purge.mockResolvedValue(undefined);
  signedIn(viewer);
  notifications = async () => ({ items: [], unread: 0 });
  get.mockImplementation((path: string) => {
    if (path === "/api/auth/me") return me();
    if (path.startsWith("/api/notifications")) return notifications();
    if (path === "/api/cluster/self") return Promise.reject(new Error("no cluster"));
    return Promise.resolve({});
  });
  post.mockImplementation((path: string) => {
    if (path === "/api/auth/login") return Promise.resolve({ user: viewer });
    return Promise.resolve(undefined);
  });
});

afterEach(() => {
  vi.useRealTimers();
  vi.mocked(window.scrollTo).mockRestore();
});

const where = () => screen.getByTestId("where");
const sidebar = () => screen.getAllByRole("navigation", { name: en["nav.primary"] })[0];
const tabbar = () => screen.getAllByRole("navigation", { name: en["nav.primary"] })[1];
const main = () => screen.getByRole("main");
/** a link's name: its aria-label, else its text without the aria-hidden icon */
function linkName(a: HTMLElement): string {
  const label = a.getAttribute("aria-label");
  if (label) return label;
  const copy = a.cloneNode(true) as HTMLElement;
  copy.querySelectorAll("[aria-hidden='true']").forEach((x) => x.remove());
  return (copy.textContent ?? "").trim();
}
const linkList = (el: HTMLElement) =>
  within(el)
    .getAllByRole("link")
    .map((a) => [linkName(a), a.getAttribute("href")]);

const USER_LINKS = [
  ["nav.library", "/library"],
  ["nav.series", "/series"],
  ["nav.guide", "/guide"],
  ["nav.search", "/search"],
  ["nav.account", "/account"],
  ["nav.devices", "/account/devices"],
  ["nav.linkDevice", "/link"],
  ["nav.sessions", "/account/sessions"],
  ["nav.tokens", "/account/tokens"],
  ["nav.history", "/account/history"],
  ["nav.billing", "/billing"],
  ["nav.cloudStatus", "/cloud/status"],
  ["nav.help", "/help"],
] as const;

// The account pages, billing, cloud status and help: everyone's half of the More page.
const MORE_LINKS = USER_LINKS.slice(4);

// Running the server is the admins' (the API refuses anyone else): these lead the
// Administration group and never show for a normal user.
const SERVER_LINKS = [
  ["nav.queue", "/queue"],
  ["nav.transcription", "/transcription"],
  ["nav.settings", "/settings"],
] as const;

const ADMIN_LINKS = [
  ...SERVER_LINKS,
  ["nav.admin.users", "/admin/users"],
  ["nav.admin.libraryAcl", "/admin/library-acl"],
  ["nav.admin.channels", "/admin/channels"],
  ["nav.admin.filler", "/admin/filler"],
  ["nav.admin.liveTv", "/admin/live-tv"],
  ["nav.admin.tvChanges", "/admin/live-tv/changes"],
  ["nav.admin.analytics", "/admin/analytics"],
  ["nav.admin.audit", "/admin/audit"],
  ["nav.admin.health", "/admin/health"],
  ["nav.admin.machines", "/admin/machines"],
  ["nav.admin.updates", "/admin/updates"],
  ["nav.admin.logs", "/admin/logs"],
  ["nav.admin.devices", "/admin/devices"],
  ["nav.admin.extensions", "/admin/extensions"],
] as const;

const named = (links: ReadonlyArray<readonly [string, string]>, cat: Record<string, string> = en) =>
  links.map(([k, href]) => [cat[k], href]);

describe("AppShell (test-cases/app-shell.md)", () => {
  it("SHL-02: an unknown route shows NotFound inside the shell", async () => {
    renderShell(["/does-not-exist/42"]);
    expect(await screen.findByRole("heading", { name: "404" })).toBeInTheDocument();
    expect(screen.getByText("This route doesn't exist.")).toBeInTheDocument();
    expect(screen.getByRole("banner")).toBeInTheDocument();
    expect(sidebar()).toBeInTheDocument();
    const back = screen.getByRole("link", { name: "Back to library" });
    expect(back).toHaveAttribute("href", "/library");
    await userEvent.setup().click(back);
    expect(await screen.findByRole("heading", { name: "page /library" })).toBeInTheDocument();
  });

  // Bug: NotFound.tsx has hardcoded English. Fix needs new i18n keys
  // (e.g. notFound.text / notFound.back) in i18n/en.json + ar.json.
  it.fails("SHL-02: NotFound is translated in Arabic", async () => {
    localStorage.setItem("mkt:locale", "ar");
    renderShell(["/does-not-exist"]);
    await screen.findByRole("heading", { name: "404" });
    expect(screen.queryByText("This route doesn't exist.")).not.toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "Back to library" })).not.toBeInTheDocument();
  });

  it("SHL-03: an unknown route while signed out goes to /login, then the 404", async () => {
    signedOut();
    renderShell(["/does-not-exist"]);
    const user = userEvent.setup();
    await user.type(await screen.findByLabelText(en["login.username"]), "viewer");
    expect(where()).toHaveTextContent(/^\/login$/);
    expect(screen.getByTestId("from")).toHaveTextContent("/does-not-exist");
    await user.type(screen.getByLabelText(en["login.password"]), "secret{Enter}");
    expect(await screen.findByRole("heading", { name: "404" })).toBeInTheDocument();
  });

  it.each([["/library"], ["/videos/42"], ["/admin/users"], ["/more"]])(
    "SHL-04: signed out, %s redirects to /login with from",
    async (path) => {
      signedOut();
      renderShell([path]);
      expect(await screen.findByLabelText(en["login.username"])).toBeInTheDocument();
      expect(where()).toHaveTextContent(/^\/login$/);
      expect(screen.getByTestId("from")).toHaveTextContent(path);
      expect(screen.queryByRole("banner")).not.toBeInTheDocument();
      expect(get.mock.calls.some(([p]) => String(p).startsWith("/api/notifications"))).toBe(false);
    }
  );

  it("SHL-04: signed out, from keeps the query of /search?q=x", async () => {
    signedOut();
    renderShell(["/search?q=x"]);
    await screen.findByLabelText(en["login.username"]);
    expect(screen.getByTestId("from")).toHaveTextContent(/^\/search\?q=x$/);
  });

  it("SHL-05: while auth loads a status shows, then the shell", async () => {
    let resolve!: (v: unknown) => void;
    me = () => new Promise((r) => (resolve = r));
    renderShell(["/library"]);
    expect(screen.getByRole("status")).toHaveTextContent(en["common.loading"]);
    expect(screen.queryByRole("navigation")).not.toBeInTheDocument();
    expect(where()).toHaveTextContent(/^\/library$/);
    await act(async () => resolve({ user: viewer }));
    expect(await screen.findByRole("heading", { name: "page /library" })).toBeInTheDocument();
  });

  it("SHL-05: the auth loading status is translated in Arabic", async () => {
    localStorage.setItem("mkt:locale", "ar");
    me = () => new Promise(() => {});
    renderShell(["/library"]);
    expect(screen.getByRole("status")).toHaveTextContent(ar["common.loading"]);
  });

  it.each([
    ["500", () => Promise.reject(problem(500)), true],
    ["network failure", () => Promise.reject(new TypeError("Failed to fetch")), true],
    ["200 without user", async () => ({}), false],
    ["401", () => Promise.reject(problem(401)), false],
  ])("SHL-06: /api/auth/me %s is treated as signed out", async (_name, answer, warns) => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    me = answer;
    renderShell(["/library"]);
    expect(await screen.findByLabelText(en["login.username"])).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    const probeWarned = warn.mock.calls.some(([m]) => m === "auth: /me probe failed");
    expect(probeWarned).toBe(warns);
    warn.mockRestore();
  });

  // Bug: nothing reacts to a 401 after start-up; the shell keeps the stale
  // user. Fix belongs in lib/api.ts + lib/auth.tsx (clear the user on 401).
  it.fails("SHL-07: a 401 mid-use sends the user to /login", async () => {
    renderShell(["/library"]);
    await screen.findByRole("heading", { name: "page /library" });
    get.mockImplementation((path: string) =>
      path === "/api/series" ? Promise.reject(problem(401)) : Promise.resolve({})
    );
    await userEvent.setup().click(within(sidebar()).getByRole("link", { name: en["nav.series"] }));
    await waitFor(() => expect(where()).toHaveTextContent(/^\/login$/), { timeout: 300 });
    expect(screen.getByTestId("from")).toHaveTextContent("/series");
  });

  it.each([
    ["is_admin false", viewer],
    ["is_admin missing", { id: "u3", username: "x" }],
  ])("SHL-08: a normal user (%s) sees no admin links", async (_name, u) => {
    signedIn(u);
    renderShell(["/library"]);
    await screen.findByRole("heading", { name: "page /library" });
    expect(linkList(sidebar())).toEqual(named(USER_LINKS));
    expect(screen.queryByText(en["nav.admin"])).not.toBeInTheDocument();
    // no server page (Processing, Transcription, Settings) anywhere: sidebar or tab bar
    for (const [k, href] of SERVER_LINKS) {
      expect(screen.queryByRole("link", { name: en[k] })).not.toBeInTheDocument();
      expect(document.querySelector(`a[href="${href}"]`)).toBeNull();
    }
    expect(linkList(tabbar()).map(([, href]) => href)).toEqual([
      "/library",
      "/series",
      "/guide",
      "/search",
      "/more",
    ]);
  });

  it("SHL-09: an admin sees the Administration group with every admin link", async () => {
    signedIn(root);
    renderShell(["/library"]);
    await screen.findByRole("heading", { name: "page /library" });
    expect(within(sidebar()).getByText(en["nav.admin"])).toBeInTheDocument();
    expect(linkList(sidebar())).toEqual([...named(USER_LINKS), ...named(ADMIN_LINKS)]);
    // Processing, Transcription and Settings open the group, before the other admin links
    const admin = linkList(sidebar()).slice(USER_LINKS.length, USER_LINKS.length + 3);
    expect(admin).toEqual(named(SERVER_LINKS));
    // the tab bar stays the same five for an admin
    expect(within(tabbar()).getAllByRole("link")).toHaveLength(5);
    // /admin/enrichment is routed but not in the nav (reported as a gap)
    expect(within(sidebar()).queryByRole("link", { name: /enrichment/i })).toBeNull();
  });

  it.each([["/admin/users"], ["/admin/live-tv"], ["/admin/logs"]])(
    "SHL-10: a non-admin deep link to %s shows Admins only",
    async (path) => {
      renderShell([path]);
      expect(await screen.findByText(en["common.forbidden.title"])).toBeInTheDocument();
      expect(screen.getByText(en["common.forbidden.desc"])).toBeInTheDocument();
      expect(screen.queryByRole("heading", { name: `page ${path}` })).not.toBeInTheDocument();
      expect(screen.getByRole("banner")).toBeInTheDocument();
      expect(within(sidebar()).queryByText(en["nav.admin"])).toBeNull();
    }
  );

  it("SHL-12: the header search submits to /search (trimmed, encoded)", async () => {
    renderShell(["/library"]);
    const user = userEvent.setup();
    const box = await screen.findByRole("searchbox", { name: en["nav.search"] });
    expect(box).toHaveAttribute("placeholder", en["search.placeholder"]);
    await user.type(box, " سورة البقرة & co{Enter}");
    expect(where()).toHaveTextContent(`/search?q=${encodeURIComponent("سورة البقرة & co")}`);
    await user.clear(box);
    await user.type(box, "{Enter}");
    expect(where()).toHaveTextContent(/^\/search$/);
  });

  it("SHL-13: sign out clears the session and Back does not show the old page", async () => {
    renderShell(["/series", "/library"]);
    const user = userEvent.setup();
    await screen.findByRole("heading", { name: "page /library" });
    // the name is the account link, not the sign-out button
    expect(screen.getByRole("link", { name: "viewer" })).toHaveAttribute("href", "/account");
    const signOut = screen.getByRole("button", { name: en["nav.signOut"] });
    expect(signOut).not.toHaveTextContent("viewer");
    await user.click(signOut);
    expect(await screen.findByLabelText(en["login.username"])).toBeInTheDocument();
    expect(post).toHaveBeenCalledWith("/api/auth/logout");
    expect(purge).toHaveBeenCalled();
    expect(where()).toHaveTextContent(/^\/login$/);
    await user.click(screen.getByRole("button", { name: "test back" }));
    expect(await screen.findByLabelText(en["login.username"])).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "page /series" })).not.toBeInTheDocument();
    expect(screen.getByTestId("from")).toHaveTextContent("/series");
  });

  it.each([
    ["500", () => Promise.reject(problem(500))],
    ["network failure", () => Promise.reject(new TypeError("Failed to fetch"))],
  ])("SHL-14: sign out still leaves when the server call fails (%s)", async (_n, answer) => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    post.mockImplementation(answer);
    renderShell(["/library"]);
    await screen.findByRole("heading", { name: "page /library" });
    await userEvent.setup().click(screen.getByRole("button", { name: en["nav.signOut"] }));
    expect(await screen.findByLabelText(en["login.username"])).toBeInTheDocument();
    expect(warn).toHaveBeenCalledWith("auth: logout call failed", expect.anything());
    warn.mockRestore();
  });

  it("SHL-14b: sign out refused with the home unreachable keeps the user and says so", async () => {
    post.mockRejectedValue(
      new ApiError({
        type: "https://maktaba.dev/problems/home-unreachable",
        title: "the home is not reachable",
        status: 503,
        code: "home_unreachable",
      })
    );
    renderShell(["/library"]);
    await screen.findByRole("heading", { name: "page /library" });
    await userEvent.setup().click(screen.getByRole("button", { name: en["nav.signOut"] }));
    expect(await screen.findByText(en["homeOffline.signOut"])).toBeInTheDocument();
    expect(where()).toHaveTextContent(/^\/library$/);
    expect(purge).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: en["nav.signOut"] })).toBeInTheDocument();
  });

  it("SHL-14c: a sign-out the home did not answer in time keeps the user and says so", async () => {
    post.mockRejectedValue(
      new ApiError({
        type: "https://maktaba.dev/problems/home-timeout",
        title: "the home is slow to answer",
        status: 504,
        code: "home_timeout",
      })
    );
    renderShell(["/library"]);
    await screen.findByRole("heading", { name: "page /library" });
    await userEvent.setup().click(screen.getByRole("button", { name: en["nav.signOut"] }));
    expect(await screen.findByText(en["homeOffline.signOut"])).toBeInTheDocument();
    expect(purge).not.toHaveBeenCalled();
    expect(screen.getByRole("link", { name: "viewer" })).toBeInTheDocument(); // still signed in
  });

  it("SHL-15: the theme button cycles light, dark, system", async () => {
    localStorage.setItem("mkt:theme", "light");
    renderShell(["/library"]);
    const user = userEvent.setup();
    const name = (k: string) => `${en["settings.theme"]}: ${en[k as keyof typeof en]}`;
    const btn = await screen.findByRole("button", { name: name("settings.theme.dark") });
    expect(btn).toHaveTextContent("☀");
    await user.click(btn);
    expect(document.documentElement.dataset.theme).toBe("dark");
    expect(localStorage.getItem("mkt:theme")).toBe("dark");
    expect(btn).toHaveAccessibleName(name("settings.theme.system"));
    await user.click(btn);
    expect(localStorage.getItem("mkt:theme")).toBe("system");
    expect(document.documentElement.dataset.theme).toBe("dark"); // OS not light
    expect(btn).toHaveAccessibleName(name("settings.theme.light"));
    await user.click(btn);
    expect(localStorage.getItem("mkt:theme")).toBe("light");
    expect(document.documentElement.dataset.theme).toBe("light");
  });

  it("SHL-16: the language button switches to Arabic and it survives a reload", async () => {
    const first = renderShell(["/library"]);
    const user = userEvent.setup();
    await user.click(
      await screen.findByRole("button", {
        name: `${en["settings.language"]}: ${en["settings.language.ar"]}`,
      })
    );
    expect(document.documentElement).toHaveAttribute("dir", "rtl");
    expect(document.documentElement).toHaveAttribute("lang", "ar");
    expect(localStorage.getItem("mkt:locale")).toBe("ar");
    const toggle = screen.getByRole("button", {
      name: `${ar["settings.language"]}: ${ar["settings.language.en"]}`,
    });
    expect(toggle).toHaveTextContent("EN");
    first.unmount();
    renderShell(["/library"]);
    await screen.findByRole("heading", { name: "page /library" });
    const nav = screen.getAllByRole("navigation", { name: ar["nav.primary"] })[0];
    for (const k of [
      "nav.library",
      "nav.series",
      "nav.guide",
      "nav.search",
      "nav.account",
    ] as const) {
      expect(within(nav).getByRole("link", { name: ar[k] })).toBeInTheDocument();
    }
  });

  const failover = {
    id: "n1",
    kind: "x",
    severity: "info",
    title_key: "notify.tv.slot.failover",
    link: "",
    audience: "all",
    created_at: "2026-10-01T10:00:00Z",
    read: false,
  };

  it.each([
    ["items: null", { items: null, unread: 0 }],
    ["no items", { unread: 0 }],
    ["unread: null", { items: [], unread: null }],
    ["{}", {}],
  ])("SHL-17: notifications with %s show the empty panel", async (_name, body) => {
    notifications = async () => body;
    renderShell(["/library"]);
    const user = userEvent.setup();
    await waitFor(() =>
      expect(get).toHaveBeenCalledWith(expect.stringMatching(/^\/api\/notifications/))
    );
    const bell = await screen.findByRole("button", { name: en["notify.bell"] });
    expect(screen.getByTestId("notifications-count")).toHaveClass("mkt-bell__count--none");
    expect(screen.getByTestId("notifications-count")).not.toHaveTextContent("NaN");
    await user.click(bell);
    expect(await screen.findByText(en["notify.empty"])).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("SHL-17: an item with null or missing params still shows its title", async () => {
    notifications = async () => ({
      items: [
        { ...failover, params: null },
        { ...failover, id: "n2" },
      ],
      unread: 2,
    });
    renderShell(["/library"]);
    await userEvent
      .setup()
      .click(await screen.findByRole("button", { name: /Notifications, 2 unread/ }));
    expect(await screen.findAllByText(en["notify.tv.slot.failover"])).toHaveLength(2);
    expect(screen.getByRole("heading", { name: "page /library" })).toBeInTheDocument();
  });

  it("SHL-18: a failed first load says so in the panel", async () => {
    notifications = () => Promise.reject(problem(500));
    renderShell(["/library"]);
    await userEvent.setup().click(await screen.findByRole("button", { name: en["notify.bell"] }));
    expect(await screen.findByText(en["notify.loadError"])).toBeInTheDocument();
  });

  it("SHL-18: a read answer without unread never shows NaN", async () => {
    notifications = async () => ({ items: [{ ...failover, params: {} }], unread: 1 });
    post.mockImplementation(async () => ({}));
    renderShell(["/library"]);
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: /1 unread/ }));
    notifications = () => new Promise(() => {}); // the next list read never lands
    await user.click(await screen.findByRole("button", { name: /hot standby/ }));
    await waitFor(() => expect(post).toHaveBeenCalledWith("/api/notifications/n1/read"));
    const count = screen.getByTestId("notifications-count");
    expect(count).not.toHaveTextContent("NaN");
    expect(screen.getByTestId("notifications-bell")).not.toHaveAccessibleName(/NaN/);
  });

  it("SHL-18: a failed read-all keeps the items read without an alert", async () => {
    notifications = async () => ({ items: [{ ...failover, params: {} }], unread: 1 });
    renderShell(["/library"]);
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: /1 unread/ }));
    notifications = () => new Promise(() => {});
    post.mockImplementation(() => Promise.reject(problem(500)));
    await user.click(await screen.findByRole("button", { name: en["notify.readAll"] }));
    await waitFor(() => expect(post).toHaveBeenCalledWith("/api/notifications/read-all"));
    expect(screen.getByRole("button", { name: en["notify.bell"] })).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("SHL-24: g-shortcuts navigate, / focuses search, typing is left alone", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    renderShell(["/series"]);
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    await screen.findByRole("heading", { name: "page /series" });
    for (const [key, path] of [
      ["l", "/library"],
      ["s", "/search"],
      ["q", "/queue"],
      ["c", "/settings"],
    ]) {
      await user.keyboard(`g${key}`);
      expect(where()).toHaveTextContent(new RegExp(`^${path}$`));
    }
    await user.keyboard("g");
    act(() => vi.advanceTimersByTime(1300));
    await user.keyboard("l");
    expect(where()).toHaveTextContent(/^\/settings$/);

    await user.keyboard("/");
    const box = screen.getByRole("searchbox", { name: en["nav.search"] });
    expect(box).toHaveFocus();
    await user.keyboard("gl");
    expect(box).toHaveValue("gl");
    expect(where()).toHaveTextContent(/^\/settings$/);

    box.blur();
    fireEvent.keyDown(window, { key: "g", isComposing: true });
    fireEvent.keyDown(window, { key: "l" });
    await user.keyboard("{Control>}g{/Control}l");
    expect(where()).toHaveTextContent(/^\/settings$/);
  });

  it("SHL-25: ? opens the shortcut help, Escape closes it, ignored while typing", async () => {
    renderShell(["/library"]);
    const user = userEvent.setup();
    await screen.findByRole("heading", { name: "page /library" });
    await user.keyboard("?");
    const dialog = await screen.findByRole("dialog", { name: en["shortcuts.title"] });
    for (const k of [
      "shortcuts.goLibrary",
      "shortcuts.goSearch",
      "shortcuts.goQueue",
      "shortcuts.goSettings",
      "shortcuts.focusSearch",
      "shortcuts.help",
      "shortcuts.close",
    ] as const) {
      expect(within(dialog).getByText(en[k])).toBeInTheDocument();
    }
    await user.tab();
    await user.tab();
    expect(dialog).toContainElement(document.activeElement as HTMLElement);
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    const box = screen.getByRole("searchbox", { name: en["nav.search"] });
    await user.click(box);
    await user.keyboard("?");
    expect(box).toHaveValue("?");
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("SHL-28: a page crash shows a fallback inside the shell", async () => {
    const spy = vi.spyOn(console, "error").mockImplementation(() => {});
    crash.paths.add("/search");
    renderShell(["/library"]);
    const user = userEvent.setup();
    await screen.findByRole("heading", { name: "page /library" });
    await user.click(within(sidebar()).getByRole("link", { name: en["nav.search"] }));
    expect(await within(main()).findByRole("alert")).toHaveTextContent(en["common.pageError"]);
    expect(screen.getByRole("banner")).toBeInTheDocument();
    expect(tabbar()).toBeInTheDocument();
    expect(spy.mock.calls.filter(([m]) => m === "[page] render failed")).toHaveLength(1);
    spy.mockRestore();
  });

  it("SHL-29: after a crash Back, nav links and g l recover without a reload", async () => {
    const spy = vi.spyOn(console, "error").mockImplementation(() => {});
    crash.paths.add("/search");
    renderShell(["/library"]);
    const user = userEvent.setup();
    const toSearch = async () => {
      await user.click(within(sidebar()).getByRole("link", { name: en["nav.search"] }));
      expect(await within(main()).findByRole("alert")).toBeInTheDocument();
    };
    await screen.findByRole("heading", { name: "page /library" });
    await toSearch();
    await user.click(screen.getByRole("button", { name: "test back" }));
    expect(await screen.findByRole("heading", { name: "page /library" })).toBeInTheDocument();
    await toSearch();
    await user.click(within(sidebar()).getByRole("link", { name: en["nav.series"] }));
    expect(await screen.findByRole("heading", { name: "page /series" })).toBeInTheDocument();
    await toSearch();
    (document.activeElement as HTMLElement | null)?.blur();
    await user.keyboard("gl");
    expect(await screen.findByRole("heading", { name: "page /library" })).toBeInTheDocument();
    spy.mockRestore();
  });

  it("SHL-30: the fallback resets on a new id of the same route, and on Back/Forward", async () => {
    const spy = vi.spyOn(console, "error").mockImplementation(() => {});
    crash.paths.add("/videos/bad");
    renderShell(["/videos/bad"]);
    const user = userEvent.setup();
    expect(await screen.findByRole("alert")).toBeInTheDocument();
    expect(main()).toContainElement(screen.getByRole("alert"));
    await user.click(screen.getByRole("link", { name: "test good video" }));
    expect(await screen.findByRole("heading", { name: "page /videos/good" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "test back" }));
    expect(await within(main()).findByRole("alert")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "test forward" }));
    expect(await screen.findByRole("heading", { name: "page /videos/good" })).toBeInTheDocument();
    spy.mockRestore();
  });

  it("SHL-31: a crash in the notifications bell does not blank the app", async () => {
    const spy = vi.spyOn(console, "error").mockImplementation(() => {});
    crash.bell = true;
    renderShell(["/library"]);
    expect(await screen.findByRole("heading", { name: "page /library" })).toBeInTheDocument();
    expect(sidebar()).toBeInTheDocument();
    expect(screen.getByRole("button", { name: en["nav.signOut"] })).toBeInTheDocument();
    spy.mockRestore();
  });

  it("SHL-34: landmarks, skip link and link names", async () => {
    renderShell(["/library"]);
    await screen.findByRole("heading", { name: "page /library" });
    expect(screen.getAllByRole("banner")).toHaveLength(1);
    expect(screen.getAllByRole("main")).toHaveLength(1);
    expect(screen.getAllByRole("search")).toHaveLength(1);
    expect(screen.getAllByRole("navigation")).toHaveLength(2);
    (document.activeElement as HTMLElement | null)?.blur();
    await userEvent.setup().tab();
    const skip = screen.getByRole("link", { name: en["nav.skipToContent"] });
    expect(skip).toHaveFocus();
    expect(skip).toHaveAttribute("href", "#mkt-main");
    expect(main()).toHaveAttribute("id", "mkt-main");
    expect(main()).toHaveAttribute("tabindex", "-1");
    for (const [k] of USER_LINKS) {
      const link = within(sidebar()).getByRole("link", { name: en[k] });
      expect(link.querySelector(".mkt-nav-link__icon")).toHaveAttribute("aria-hidden", "true");
    }
  });
});

describe("DesktopBridge drop overlay (test-cases/app-shell.md)", () => {
  function renderBridge() {
    return render(
      <MemoryRouter>
        <DesktopBridge />
      </MemoryRouter>
    );
  }

  it("SHL-22: a drop with rejected files shows the count and Dismiss clears it", async () => {
    renderBridge();
    await waitFor(() => expect(drop.handler).not.toBeNull());
    act(() => drop.handler!({ accepted: [], rejected: ["/x.txt"] }));
    expect(screen.getByText(/1 file\(s\) skipped \(unsupported\)/)).toBeInTheDocument();
    await userEvent.setup().click(screen.getByRole("button", { name: "Dismiss" }));
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
  });

  // Bug: after a clean drop (nothing rejected) the full-screen overlay never
  // closes: `drop` is only cleared by Dismiss, which needs a rejected file.
  // Fix belongs in DesktopBridge.tsx / DropOverlay.tsx (clear after the import).
  it.fails("SHL-22: the overlay closes after a clean drop", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const imported = vi.fn();
    window.addEventListener("maktaba:import-files", imported);
    renderBridge();
    await waitFor(() => expect(drop.handler).not.toBeNull());
    act(() => drop.handler!({ accepted: ["/v/a.mp4"], rejected: [] }));
    expect((imported.mock.calls[0][0] as CustomEvent).detail).toEqual({ paths: ["/v/a.mp4"] });
    act(() => vi.advanceTimersByTime(10_000));
    window.removeEventListener("maktaba:import-files", imported);
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
  });
});

describe("MoreMenu (test-cases/more-menu.md)", () => {
  const moreSection = async () => {
    await screen.findByRole("heading", { level: 1, name: en["nav.more"] });
    return main();
  };

  it("MOR-01: a normal user sees the account links, and no Processing, Transcription or Settings", async () => {
    renderShell(["/more"]);
    const m = await moreSection();
    const lists = within(m).getAllByRole("list");
    expect(linkList(lists[0])).toEqual(named(MORE_LINKS));
    for (const [k] of SERVER_LINKS) {
      expect(within(m).queryByRole("link", { name: en[k] })).not.toBeInTheDocument();
    }
    expect(within(m).getByRole("button", { name: en["nav.signOut"] })).toBeInTheDocument();
  });

  it("MOR-02: a normal user sees no Administration group", async () => {
    renderShell(["/more"]);
    const m = await moreSection();
    expect(within(m).queryByRole("heading", { name: en["nav.admin"] })).toBeNull();
    expect(within(m).getAllByRole("list")).toHaveLength(1);
    const hrefs = within(m)
      .getAllByRole("link")
      .map((a) => a.getAttribute("href") ?? "");
    expect(hrefs.filter((h) => h.startsWith("/admin/"))).toEqual([]);
  });

  it("MOR-03: an admin sees the Administration group with every admin link", async () => {
    signedIn(root);
    renderShell(["/more"]);
    const m = await moreSection();
    expect(within(m).getByRole("heading", { level: 2, name: en["nav.admin"] })).toBeInTheDocument();
    const lists = within(m).getAllByRole("list");
    expect(lists).toHaveLength(2);
    expect(linkList(lists[0])).toEqual(named(MORE_LINKS));
    expect(linkList(lists[1])).toEqual(named(ADMIN_LINKS));
    expect(linkList(lists[1]).slice(0, 3)).toEqual(named(SERVER_LINKS));
  });

  it.each([
    ["missing", { id: "u3", username: "x" }],
    ["null", { id: "u3", username: "x", is_admin: null }],
  ])("MOR-04: is_admin %s shows no admin group", async (_name, u) => {
    signedIn(u);
    renderShell(["/more"]);
    const m = await moreSection();
    expect(within(m).getAllByRole("list")).toHaveLength(1);
    expect(within(m).queryByText(en["nav.admin"])).toBeNull();
  });

  it("MOR-05: the More tab opens /more and is the only active link", async () => {
    renderShell(["/library"]);
    await screen.findByRole("heading", { name: "page /library" });
    const user = userEvent.setup();
    await user.click(within(tabbar()).getByRole("link", { name: en["nav.more"] }));
    await moreSection();
    expect(where()).toHaveTextContent(/^\/more$/);
    expect(within(tabbar()).getByRole("link", { name: en["nav.more"] })).toHaveAttribute(
      "aria-current",
      "page"
    );
    expect(within(tabbar()).getByRole("link", { name: en["nav.library"] })).not.toHaveAttribute(
      "aria-current"
    );
    expect(sidebar().querySelector("[aria-current]")).toBeNull();
  });

  it("MOR-06: each link opens its page and Back returns to /more", async () => {
    signedIn(root);
    renderShell(["/more"]);
    const user = userEvent.setup();
    for (const [k, path] of [
      ["nav.settings", "/settings"],
      ["nav.admin.users", "/admin/users"],
      ["nav.sessions", "/account/sessions"],
    ] as const) {
      const m = await moreSection();
      await user.click(within(m).getByRole("link", { name: en[k] }));
      expect(await screen.findByRole("heading", { name: `page ${path}` })).toBeInTheDocument();
      await user.click(screen.getByRole("button", { name: "test back" }));
    }
    expect(await moreSection()).toBeInTheDocument();
  });

  it("MOR-07: sign out goes to /login and Back does not return to /more", async () => {
    renderShell(["/library", "/more"]);
    const user = userEvent.setup();
    const m = await moreSection();
    await user.click(within(m).getByRole("button", { name: en["nav.signOut"] }));
    expect(await screen.findByLabelText(en["login.username"])).toBeInTheDocument();
    expect(post).toHaveBeenCalledWith("/api/auth/logout");
    expect(purge).toHaveBeenCalled();
    expect(where()).toHaveTextContent(/^\/login$/);
    await user.click(screen.getByRole("button", { name: "test back" }));
    expect(await screen.findByLabelText(en["login.username"])).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: en["nav.more"] })).not.toBeInTheDocument();
  });

  it.each([
    ["500", () => Promise.reject(problem(500))],
    ["network failure", () => Promise.reject(new TypeError("Failed to fetch"))],
  ])("MOR-08: sign out still leaves when the server call fails (%s)", async (_n, answer) => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    post.mockImplementation(answer);
    renderShell(["/more"]);
    const m = await moreSection();
    await userEvent.setup().click(within(m).getByRole("button", { name: en["nav.signOut"] }));
    expect(await screen.findByLabelText(en["login.username"])).toBeInTheDocument();
    expect(warn).toHaveBeenCalledWith("auth: logout call failed", expect.anything());
    warn.mockRestore();
  });

  it("MOR-08b: sign out refused with the home unreachable keeps the user and says so", async () => {
    post.mockRejectedValue(
      new ApiError({
        type: "https://maktaba.dev/problems/home-unreachable",
        title: "the home is not reachable",
        status: 503,
        code: "home_unreachable",
      })
    );
    renderShell(["/more"]);
    const m = await moreSection();
    await userEvent.setup().click(within(m).getByRole("button", { name: en["nav.signOut"] }));
    expect(await within(m).findByRole("alert")).toHaveTextContent(en["homeOffline.signOut"]);
    expect(where()).toHaveTextContent(/^\/more$/);
    expect(purge).not.toHaveBeenCalled();
  });

  it("MOR-10: a signed-out deep link to /more comes back after sign-in", async () => {
    signedOut();
    renderShell(["/more"]);
    const user = userEvent.setup();
    await user.type(await screen.findByLabelText(en["login.username"]), "viewer");
    expect(screen.getByTestId("from")).toHaveTextContent("/more");
    expect(screen.queryByRole("heading", { name: en["nav.more"] })).not.toBeInTheDocument();
    await user.type(screen.getByLabelText(en["login.password"]), "secret{Enter}");
    expect(await moreSection()).toBeInTheDocument();
    expect(where()).toHaveTextContent(/^\/more$/);
  });

  it("MOR-12: a crash on /more shows a fallback; Back and the Library tab recover", async () => {
    const spy = vi.spyOn(console, "error").mockImplementation(() => {});
    crash.paths.add("/more");
    renderShell(["/library"]);
    const user = userEvent.setup();
    const toMore = async () => {
      await user.click(within(tabbar()).getByRole("link", { name: en["nav.more"] }));
      expect(await within(main()).findByRole("alert")).toBeInTheDocument();
    };
    await screen.findByRole("heading", { name: "page /library" });
    await toMore();
    await user.click(screen.getByRole("button", { name: "test back" }));
    expect(await screen.findByRole("heading", { name: "page /library" })).toBeInTheDocument();
    await toMore();
    await user.click(within(tabbar()).getByRole("link", { name: en["nav.library"] }));
    expect(await screen.findByRole("heading", { name: "page /library" })).toBeInTheDocument();
    spy.mockRestore();
  });

  it("MOR-15: Arabic labels", async () => {
    localStorage.setItem("mkt:locale", "ar");
    signedIn(root);
    renderShell(["/more"]);
    expect(
      await screen.findByRole("heading", { level: 1, name: ar["nav.more"] })
    ).toBeInTheDocument();
    expect(document.documentElement).toHaveAttribute("dir", "rtl");
    const m = main();
    const lists = within(m).getAllByRole("list");
    expect(linkList(lists[0])).toEqual(named(MORE_LINKS, ar));
    expect(linkList(lists[1])).toEqual(named(ADMIN_LINKS, ar));
    expect(within(m).getByRole("heading", { level: 2, name: ar["nav.admin"] })).toBeInTheDocument();
    expect(within(m).getByRole("button", { name: ar["nav.signOut"] })).toBeInTheDocument();
  });

  it("MOR-17: headings, list semantics and keyboard", async () => {
    signedIn(root);
    renderShell(["/more"]);
    const m = await moreSection();
    expect(within(m).getAllByRole("heading", { level: 1 })).toHaveLength(1);
    expect(within(m).getByRole("heading", { level: 2, name: en["nav.admin"] })).toBeInTheDocument();
    const links = within(m).getAllByRole("link");
    expect(links).toHaveLength(MORE_LINKS.length + ADMIN_LINKS.length);
    for (const a of links) expect(a.closest("li")).not.toBeNull();
    expect(within(m).getAllByRole("listitem")).toHaveLength(links.length);
    expect(within(m).getByRole("link", { name: en["nav.sessions"] })).toBeInTheDocument();
    const user = userEvent.setup();
    links[0].focus();
    for (let i = 1; i < links.length; i++) {
      await user.tab();
      expect(links[i]).toHaveFocus();
    }
    await user.tab();
    const signOut = within(m).getByRole("button", { name: en["nav.signOut"] });
    expect(signOut).toHaveFocus();
    await user.keyboard("{Enter}");
    expect(await screen.findByLabelText(en["login.username"])).toBeInTheDocument();
  });
});
