// Story 11.6 — Settings page (account, sessions, theme, language).
//
// Phase 10 scaffolds three sub-views: profile/security, active sessions
// (Story 11.14), and appearance. PAT management (Story 11.13) follows
// once that surface lands.
import { useEffect, useState } from "react";
import { Link, Route, Routes, useNavigate } from "react-router-dom";
import { api, ApiError } from "../lib/api";
import { useAuth } from "../lib/auth";
import { useI18n } from "../lib/i18n";
import { resolveTheme, setTheme, type Theme } from "../lib/theme";

interface SessionRow {
  id: string;
  created_at: string;
  last_seen_at: string;
  ip?: string;
  user_agent?: string;
}

export function Settings() {
  const { t } = useI18n();
  return (
    <section className="mkt-page mkt-settings">
      <h1>{t("nav.settings")}</h1>
      <nav className="mkt-settings__nav" aria-label="Settings sections">
        <Link to="">Account</Link>
        <Link to="sessions">Sessions</Link>
        <Link to="appearance">Appearance</Link>
      </nav>
      <Routes>
        <Route index element={<AccountTab />} />
        <Route path="sessions" element={<SessionsTab />} />
        <Route path="appearance" element={<AppearanceTab />} />
      </Routes>
    </section>
  );
}

function AccountTab() {
  const { user, logoutAll } = useAuth();
  const nav = useNavigate();
  if (!user) return null;
  return (
    <div className="mkt-settings__panel">
      <p>
        Signed in as <strong>{user.username}</strong>
        {user.is_admin && " (admin)"}
      </p>
      <button
        type="button"
        className="mkt-btn mkt-btn--danger"
        onClick={async () => {
          await logoutAll();
          nav("/login", { replace: true });
        }}
      >
        Sign out everywhere
      </button>
    </div>
  );
}

function SessionsTab() {
  const { t } = useI18n();
  const [rows, setRows] = useState<SessionRow[] | null>(null);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    api
      .get<{ items: SessionRow[] }>("/api/users/me/sessions")
      .then((r) => setRows(r.items ?? []))
      .catch((e) => {
        if (e instanceof ApiError) setErr(e.problem.title);
        else setErr(t("common.error"));
        setRows([]);
      });
  }, [t]);

  if (err)
    return (
      <div className="mkt-alert" role="alert">
        {err}
      </div>
    );
  if (!rows) return <p>{t("common.loading")}</p>;
  if (rows.length === 0) return <p className="mkt-empty">{t("common.empty")}</p>;

  return (
    <div className="mkt-settings__panel">
      <table className="mkt-table">
        <thead>
          <tr>
            <th>Last seen</th>
            <th>IP</th>
            <th>User-Agent</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => (
            <tr key={r.id}>
              <td>{r.last_seen_at}</td>
              <td>{r.ip ?? "—"}</td>
              <td className="mkt-truncate">{r.user_agent ?? "—"}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function AppearanceTab() {
  const [theme, setLocalTheme] = useState<Theme>(resolveTheme);
  return (
    <div className="mkt-settings__panel">
      <fieldset>
        <legend>Theme</legend>
        {(["light", "dark"] as Theme[]).map((opt) => (
          <label key={opt} className="mkt-radio">
            <input
              type="radio"
              checked={theme === opt}
              onChange={() => {
                setTheme(opt);
                setLocalTheme(opt);
              }}
            />
            <span>{opt}</span>
          </label>
        ))}
      </fieldset>
    </div>
  );
}
