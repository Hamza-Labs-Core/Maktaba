// Top-level app shell: skip-link, header (brand + global search +
// theme/lang + user), side nav (desktop) / bottom tab bar (≤640px),
// content outlet. Lives inside <RequireAuth> so it never renders
// without a user. Stories 11.7 (responsive), 11.9 (search focus ref),
// 11.10 (update toast), 11.11 (skip-link + landmarks).
import { Component, useEffect, useRef, useState, type FormEvent, type ReactNode } from "react";
import { Outlet, NavLink, useLocation, useNavigate, useNavigationType } from "react-router-dom";
import { RouteErrorBoundary } from "./RouteErrorBoundary";
import { useToast } from "@ds/components/Toast/Toast";
import { useAuth } from "../lib/auth";
import { useI18n } from "../lib/i18n";
import { useShortcuts } from "../lib/keyboard/shortcuts";
import { applyServiceWorkerUpdate, type SwUpdateDetail } from "../lib/pwa";
import { ThemeToggle } from "./ThemeToggle";
import { LangToggle } from "./LangToggle";
import { NotificationsBell } from "./NotificationsBell";
import { updateAvailable, useUpdateStatus } from "../lib/updates";

// Where the update badge sits (Story 28.6): Settings in the sidebar and
// the More menu, and More in the phone tab bar (Settings lives behind it).
const BADGED: ReadonlySet<string> = new Set(["/settings", "/more"]);

// UpdateDot is the badge: a dot over the nav icon, positioned absolutely
// so it never moves the layout; the badged link is described by
// UPDATE_DESC ("Update available"), its name stays the same.
const UPDATE_DESC = "mkt-shell-update-badge";
function UpdateDot() {
  return <span className="mkt-update-dot" aria-hidden="true" data-testid="update-badge" />;
}

const NAV = [
  { to: "/library", key: "nav.library", icon: "▦" },
  { to: "/series", key: "nav.series", icon: "▤" },
  { to: "/guide", key: "nav.guide", icon: "▥" },
  { to: "/search", key: "nav.search", icon: "⌕" },
  { to: "/queue", key: "nav.queue", icon: "⚙" },
  { to: "/transcription", key: "nav.transcription", icon: "✎" },
  { to: "/settings", key: "nav.settings", icon: "☰" },
] as const;

// Primary pages that live behind "More" on the phone (sidebar on wide
// screens): the tab bar has room for five tabs and "More".
const MORE_ONLY: ReadonlySet<string> = new Set(["/settings", "/transcription"]);

// The phone tab bar has room for five tabs and "More": Settings,
// Transcription, the account pages and the admin pages live behind More
// (/more).
const TABS = [
  ...NAV.filter((item) => !MORE_ONLY.has(item.to)),
  { to: "/more", key: "nav.more", icon: "⋯" },
] as const;

// Secondary nav is sidebar-only (kept out of the compact bottom tab bar);
// these surfaces are reachable by every authenticated user.
const SECONDARY = [
  { to: "/account", key: "nav.account", icon: "◐" },
  { to: "/account/devices", key: "nav.devices", icon: "▢" },
  { to: "/account/sessions", key: "nav.sessions", icon: "⧉" },
  { to: "/account/tokens", key: "nav.tokens", icon: "⚿" },
  { to: "/account/history", key: "nav.history", icon: "◷" },
  { to: "/billing", key: "nav.billing", icon: "◈" },
  { to: "/cloud/status", key: "nav.cloudStatus", icon: "☁" },
  { to: "/help", key: "nav.help", icon: "?" },
] as const;

// Admin nav is sidebar-only and rendered only for admins. The pages
// re-check the principal server-side, so this is purely a UX affordance.
const ADMIN = [
  { to: "/admin/users", key: "nav.admin.users", icon: "◉" },
  { to: "/admin/library-acl", key: "nav.admin.libraryAcl", icon: "▦" },
  { to: "/admin/channels", key: "nav.admin.channels", icon: "▥" },
  { to: "/admin/filler", key: "nav.admin.filler", icon: "◫" },
  { to: "/admin/live-tv", key: "nav.admin.liveTv", icon: "▶" },
  { to: "/admin/analytics", key: "nav.admin.analytics", icon: "▰" },
  { to: "/admin/audit", key: "nav.admin.audit", icon: "❑" },
  { to: "/admin/health", key: "nav.admin.health", icon: "♥" },
  { to: "/admin/machines", key: "nav.admin.machines", icon: "⌘" },
  { to: "/admin/logs", key: "nav.admin.logs", icon: "≣" },
  { to: "/admin/devices", key: "nav.admin.devices", icon: "▤" },
  { to: "/admin/extensions", key: "nav.admin.extensions", icon: "⧉" },
] as const;

// A header part that throws while rendering drops out on its own instead of
// taking the whole app down (the route boundary only wraps the page).
class QuietBoundary extends Component<{ children: ReactNode }, { failed: boolean }> {
  state = { failed: false };

  static getDerivedStateFromError() {
    return { failed: true };
  }

  componentDidCatch(error: Error) {
    console.error("[shell] header part failed", error);
  }

  render() {
    return this.state.failed ? null : this.props.children;
  }
}

// A new page opens at its top; Back / Forward (POP) leave the browser's own
// restoration alone, so returning to a long list keeps its place.
function useScrollTopOnNavigate(): void {
  const { pathname } = useLocation();
  const type = useNavigationType();
  useEffect(() => {
    if (type === "POP") return;
    window.scrollTo(0, 0);
    document.getElementById("mkt-main")?.scrollTo?.(0, 0);
  }, [pathname, type]);
}

export function AppShell() {
  useScrollTopOnNavigate();
  const { pathname } = useLocation();
  const { user, logout } = useAuth();
  const { t } = useI18n();
  const { focusSearchRef } = useShortcuts();
  const toast = useToast();
  const nav = useNavigate();
  const [q, setQ] = useState("");
  const searchRef = useRef<HTMLInputElement | null>(null);
  const hasUpdate = updateAvailable(useUpdateStatus());
  const badged = (to: string) => hasUpdate && BADGED.has(to);

  useEffect(() => {
    focusSearchRef(searchRef.current);
    return () => focusSearchRef(null);
  }, [focusSearchRef]);

  useEffect(() => {
    const onUpdate = (e: Event) => {
      const reg = (e as CustomEvent<SwUpdateDetail>).detail?.registration;
      toast.show({
        id: "sw-update",
        tone: "info",
        durationMs: 0,
        message: (
          <span className="mkt-sw-update">
            <span>{t("pwa.updateAvailable")}</span>
            <button
              type="button"
              className="mkt-btn mkt-btn--ghost"
              onClick={() => {
                if (reg) applyServiceWorkerUpdate(reg);
                else window.location.reload();
              }}
            >
              {t("pwa.reload")}
            </button>
          </span>
        ),
      });
    };
    window.addEventListener("mkt:sw-update", onUpdate);
    return () => window.removeEventListener("mkt:sw-update", onUpdate);
  }, [toast, t]);

  function onSearch(e: FormEvent) {
    e.preventDefault();
    const term = q.trim();
    nav(term ? `/search?q=${encodeURIComponent(term)}` : "/search");
  }

  return (
    <div className="mkt-shell">
      <a href="#mkt-main" className="mkt-skip-link">
        {t("nav.skipToContent")}
      </a>
      <header className="mkt-shell__header" role="banner">
        <div className="mkt-shell__brand">{t("app.title")}</div>
        <form className="mkt-shell__search" role="search" onSubmit={onSearch}>
          <input
            ref={searchRef}
            type="search"
            value={q}
            onChange={(e) => setQ(e.target.value)}
            placeholder={t("search.placeholder")}
            aria-label={t("nav.search")}
          />
        </form>
        <div className="mkt-shell__actions">
          {user && (
            <QuietBoundary>
              <NotificationsBell />
            </QuietBoundary>
          )}
          <ThemeToggle />
          <LangToggle />
          {user && (
            <button
              type="button"
              className="mkt-btn mkt-btn--ghost"
              onClick={async () => {
                await logout();
                nav("/login", { replace: true });
              }}
              aria-label={t("nav.signOut")}
            >
              {user.username}
            </button>
          )}
        </div>
      </header>
      <div className="mkt-shell__body">
        <nav className="mkt-shell__nav" aria-label={t("nav.primary")}>
          {NAV.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              className="mkt-nav-link"
              title={t(item.key)}
              aria-label={t(item.key)}
              aria-describedby={badged(item.to) ? UPDATE_DESC : undefined}
            >
              <span className="mkt-nav-link__icon" aria-hidden="true">
                {item.icon}
                {badged(item.to) && <UpdateDot />}
              </span>
              <span className="mkt-nav-link__label">{t(item.key)}</span>
            </NavLink>
          ))}
          {SECONDARY.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              className="mkt-nav-link"
              title={t(item.key)}
              aria-label={t(item.key)}
            >
              <span className="mkt-nav-link__icon" aria-hidden="true">
                {item.icon}
              </span>
              <span className="mkt-nav-link__label">{t(item.key)}</span>
            </NavLink>
          ))}
          {user?.is_admin && (
            <>
              <span className="mkt-nav-group" aria-hidden="true">
                {t("nav.admin")}
              </span>
              {ADMIN.map((item) => (
                <NavLink
                  key={item.to}
                  to={item.to}
                  className="mkt-nav-link"
                  title={t(item.key)}
                  aria-label={t(item.key)}
                >
                  <span className="mkt-nav-link__icon" aria-hidden="true">
                    {item.icon}
                  </span>
                  <span className="mkt-nav-link__label">{t(item.key)}</span>
                </NavLink>
              ))}
            </>
          )}
        </nav>
        <main className="mkt-shell__main" role="main" id="mkt-main" tabIndex={-1}>
          <RouteErrorBoundary resetKey={pathname}>
            <Outlet />
          </RouteErrorBoundary>
        </main>
      </div>
      {hasUpdate && (
        <span id={UPDATE_DESC} hidden>
          {t("update.badge")}
        </span>
      )}
      <nav className="mkt-shell__tabbar" aria-label={t("nav.primary")}>
        {TABS.map((item) => (
          <NavLink
            key={item.to}
            to={item.to}
            className="mkt-tab"
            aria-describedby={badged(item.to) ? UPDATE_DESC : undefined}
          >
            <span className="mkt-tab__icon" aria-hidden="true">
              {item.icon}
              {badged(item.to) && <UpdateDot />}
            </span>
            <span className="mkt-tab__label">{t(item.key)}</span>
          </NavLink>
        ))}
      </nav>
    </div>
  );
}

// The phone's way to everything the tab bar has no room for: Settings, the
// account pages, the admin pages (admins only) and signing out. On a wide
// screen the sidebar already lists them.
export function MoreMenu() {
  const { user, logout } = useAuth();
  const { t } = useI18n();
  const nav = useNavigate();
  const hasUpdate = updateAvailable(useUpdateStatus());
  const groups: Array<{
    title?: string;
    items: ReadonlyArray<{ to: string; key: string; icon: string }>;
  }> = [{ items: [...NAV.filter((item) => MORE_ONLY.has(item.to)), ...SECONDARY] }];
  if (user?.is_admin) groups.push({ title: t("nav.admin"), items: ADMIN });
  return (
    <section className="mkt-page mkt-more">
      {/* the menu lists Help itself, so no "?" beside the title */}
      <h1>{t("nav.more")}</h1>
      {hasUpdate && (
        <span id="mkt-more-update-badge" hidden>
          {t("update.badge")}
        </span>
      )}
      {groups.map((g, i) => (
        <div key={i} className="mkt-more__group">
          {g.title && <h2>{g.title}</h2>}
          <ul className="mkt-more__list">
            {g.items.map((item) => (
              <li key={item.to}>
                <NavLink
                  to={item.to}
                  className="mkt-more__link"
                  aria-describedby={
                    hasUpdate && item.to === "/settings" ? "mkt-more-update-badge" : undefined
                  }
                >
                  <span className="mkt-nav-link__icon" aria-hidden="true">
                    {item.icon}
                    {hasUpdate && item.to === "/settings" && <UpdateDot />}
                  </span>
                  {t(item.key)}
                </NavLink>
              </li>
            ))}
          </ul>
        </div>
      ))}
      <button
        type="button"
        className="mkt-btn mkt-btn--ghost mkt-more__signout"
        onClick={async () => {
          await logout();
          nav("/login", { replace: true });
        }}
      >
        {t("nav.signOut")}
      </button>
    </section>
  );
}
