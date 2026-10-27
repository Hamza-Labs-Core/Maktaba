// Epic 42.5: where the remote edition opens, the home's status: the health
// banner (what is down and why), then the ways into processing, what needs
// attention, machines and the full health page. Watching stays in the nav.
// A non-admin is told the remote is for the home's admins, with the way to
// watch; the API refuses them the admin routes whatever this page shows.
import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { EmptyState } from "@ds/components/EmptyState/EmptyState";
import { useAuth } from "../lib/auth";
import { useI18n } from "../lib/i18n";
import { fetchStatus, type StatusReport } from "../lib/systemStatus";
import { StatusBanner } from "./Admin/StatusOverview";

const REFRESH_MS = 30_000;

const LINKS = [
  { to: "/queue", key: "nav.queue" },
  { to: "/queue/failures", key: "remote.attention" },
  { to: "/admin/machines", key: "nav.admin.machines" },
  { to: "/admin/health", key: "nav.admin.health" },
] as const;

export function RemoteHome() {
  const { user } = useAuth();
  const { t } = useI18n();
  if (!user?.is_admin) {
    return (
      <section className="mkt-page">
        <h1>{t("remote.title")}</h1>
        <EmptyState
          title={t("remote.notAdmin.title")}
          description={t("remote.notAdmin.desc")}
          action={
            <Link className="mkt-btn mkt-btn--primary" to="/library">
              {t("remote.notAdmin.watch")}
            </Link>
          }
        />
      </section>
    );
  }
  return <RemoteStatus />;
}

function RemoteStatus() {
  const { t } = useI18n();
  const [report, setReport] = useState<StatusReport | null>(null);
  const [read, setRead] = useState(false);

  useEffect(() => {
    let live = true;
    const load = () =>
      void fetchStatus().then((r) => {
        if (!live) return;
        setReport(r);
        setRead(true);
      });
    load();
    const id = setInterval(load, REFRESH_MS);
    return () => {
      live = false;
      clearInterval(id);
    };
  }, []);

  return (
    <section className="mkt-page mkt-remote-home">
      <h1>{t("remote.title")}</h1>
      <StatusBanner report={report} unreachable={read && !report} />
      <nav aria-label={t("remote.title")}>
        <ul className="mkt-more__list">
          {LINKS.map((l) => (
            <li key={l.to}>
              <Link className="mkt-more__link" to={l.to}>
                {t(l.key)}
              </Link>
            </li>
          ))}
        </ul>
      </nav>
    </section>
  );
}
