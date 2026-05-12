// Top-level app shell: header, side nav, content outlet. Lives inside
// <RequireAuth> so this component never renders without a user.
import { Outlet, NavLink, useNavigate } from "react-router-dom";
import { useAuth } from "../lib/auth";
import { useI18n } from "../lib/i18n";
import { ThemeToggle } from "./ThemeToggle";
import { LangToggle } from "./LangToggle";

export function AppShell() {
  const { user, logout } = useAuth();
  const { t } = useI18n();
  const nav = useNavigate();

  return (
    <div className="mkt-shell">
      <header className="mkt-shell__header" role="banner">
        <div className="mkt-shell__brand">{t("app.title")}</div>
        <div className="mkt-shell__actions">
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
              aria-label="Sign out"
            >
              {user.username}
            </button>
          )}
        </div>
      </header>
      <div className="mkt-shell__body">
        <nav className="mkt-shell__nav" aria-label="Primary">
          <NavLink to="/library" className="mkt-nav-link">
            {t("nav.library")}
          </NavLink>
          <NavLink to="/search" className="mkt-nav-link">
            {t("nav.search")}
          </NavLink>
          <NavLink to="/queue" className="mkt-nav-link">
            {t("nav.queue")}
          </NavLink>
          <NavLink to="/settings" className="mkt-nav-link">
            {t("nav.settings")}
          </NavLink>
        </nav>
        <main className="mkt-shell__main" role="main">
          <Outlet />
        </main>
      </div>
    </div>
  );
}
