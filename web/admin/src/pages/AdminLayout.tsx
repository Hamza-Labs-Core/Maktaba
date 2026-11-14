// The operator console frame. The route renders for any signed-in staff account: the API is the
// authority (every /v1/admin/* call answers 403 admin_access_denied to an account without the
// route's role), each page shows "Access denied" on that 403, and the frame says why: the
// account lacks the permission, or the network isn't on the console's allow-list.
import { useEffect } from "react";
import { NavLink, Outlet, useLocation } from "react-router-dom";
import { useI18n } from "@cloud-shared/i18n";
import { Banner } from "@cloud-shared/ui";
import { useStaff } from "../session";

const DENIAL_KEYS: Record<string, string> = {
  admin_access_denied: "staff.forbidden",
  ip_not_allowed: "staff.ipNotAllowed",
  mfa_required: "staff.mfa.required",
};

const LINKS = [
  { to: "/", key: "admin.nav.overview", end: true },
  { to: "/accounts", key: "admin.nav.accounts" },
  { to: "/servers", key: "admin.nav.servers" },
  { to: "/abuse", key: "admin.nav.abuse" },
  { to: "/moderation", key: "admin.nav.moderation" },
  { to: "/broadcast", key: "admin.nav.broadcast" },
  { to: "/usage", key: "admin.nav.usage" },
  { to: "/reports", key: "admin.nav.reports" },
  { to: "/symbols", key: "admin.nav.symbols" },
  { to: "/errors", key: "admin.nav.errors" },
  { to: "/logs", key: "admin.nav.logs" },
  { to: "/audit", key: "admin.nav.audit" },
  { to: "/signins", key: "staff.audit.signins" },
  { to: "/sessions", key: "staff.sessions.title" },
];

export function AdminLayout() {
  const { t } = useI18n();
  const { denied, clearDenied } = useStaff();
  const { pathname } = useLocation();
  // a refusal belongs to the page that got it
  useEffect(() => clearDenied(), [pathname, clearDenied]);
  const deniedKey = denied ? DENIAL_KEYS[denied] : undefined;
  return (
    <div className="mkc-admin">
      <nav className="mkc-subnav" aria-label={t("admin.nav.label")}>
        {LINKS.map((l) => (
          <NavLink key={l.to} to={l.to} end={l.end} className="mkc-subnav__link">
            {t(l.key)}
          </NavLink>
        ))}
      </nav>
      {deniedKey && <Banner tone="error">{t(deniedKey)}</Banner>}
      <Outlet />
    </div>
  );
}
