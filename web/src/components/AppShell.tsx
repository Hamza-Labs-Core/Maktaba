// Top-level app shell: skip-link, header (brand + global search +
// theme/lang + user), side nav (desktop) / bottom tab bar (≤640px),
// content outlet. Lives inside <RequireAuth> so it never renders
// without a user. Stories 11.7 (responsive), 11.9 (search focus ref),
// 11.10 (update toast), 11.11 (skip-link + landmarks).
import { useEffect, useRef, useState, type FormEvent } from "react";
import { Outlet, NavLink, useNavigate } from "react-router-dom";
import { useToast } from "@ds/components/Toast/Toast";
import { useAuth } from "../lib/auth";
import { useI18n } from "../lib/i18n";
import { useShortcuts } from "../lib/keyboard/shortcuts";
import { ThemeToggle } from "./ThemeToggle";
import { LangToggle } from "./LangToggle";

const NAV = [
  { to: "/library", key: "nav.library", icon: "▦" },
  { to: "/search", key: "nav.search", icon: "⌕" },
  { to: "/queue", key: "nav.queue", icon: "⚙" },
  { to: "/settings", key: "nav.settings", icon: "☰" },
] as const;

export function AppShell() {
  const { user, logout } = useAuth();
  const { t } = useI18n();
  const { focusSearchRef } = useShortcuts();
  const toast = useToast();
  const nav = useNavigate();
  const [q, setQ] = useState("");
  const searchRef = useRef<HTMLInputElement | null>(null);

  useEffect(() => {
    focusSearchRef(searchRef.current);
    return () => focusSearchRef(null);
  }, [focusSearchRef]);

  useEffect(() => {
    const onUpdate = () =>
      toast.show({
        id: "sw-update",
        tone: "info",
        message: t("common.retry"),
        durationMs: 0,
      });
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
            <NavLink key={item.to} to={item.to} className="mkt-nav-link">
              <span className="mkt-nav-link__icon" aria-hidden="true">
                {item.icon}
              </span>
              <span className="mkt-nav-link__label">{t(item.key)}</span>
            </NavLink>
          ))}
        </nav>
        <main className="mkt-shell__main" role="main" id="mkt-main" tabIndex={-1}>
          <Outlet />
        </main>
      </div>
      <nav className="mkt-shell__tabbar" aria-label={t("nav.primary")}>
        {NAV.map((item) => (
          <NavLink key={item.to} to={item.to} className="mkt-tab">
            <span className="mkt-tab__icon" aria-hidden="true">
              {item.icon}
            </span>
            <span className="mkt-tab__label">{t(item.key)}</span>
          </NavLink>
        ))}
      </nav>
    </div>
  );
}
