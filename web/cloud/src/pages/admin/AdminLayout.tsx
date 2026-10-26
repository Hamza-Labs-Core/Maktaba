// The operator console frame. The route renders for anyone signed in: the API
// is the authority (every /v1/admin/* call answers 403 admin_access_denied to a
// normal account), and each page shows "Access denied" on that 403.
import { NavLink, Outlet } from "react-router-dom";
import { useI18n } from "../../i18n";

const LINKS = [
  { to: "/admin", key: "admin.nav.overview", end: true },
  { to: "/admin/accounts", key: "admin.nav.accounts" },
  { to: "/admin/servers", key: "admin.nav.servers" },
  { to: "/admin/abuse", key: "admin.nav.abuse" },
  { to: "/admin/usage", key: "admin.nav.usage" },
  { to: "/admin/audit", key: "admin.nav.audit" },
];

export function AdminLayout() {
  const { t } = useI18n();
  return (
    <div className="mkc-admin">
      <nav className="mkc-subnav" aria-label={t("admin.nav.label")}>
        {LINKS.map((l) => (
          <NavLink key={l.to} to={l.to} end={l.end} className="mkc-subnav__link">
            {t(l.key)}
          </NavLink>
        ))}
      </nav>
      <Outlet />
    </div>
  );
}
