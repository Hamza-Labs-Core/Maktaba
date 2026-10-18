// Which machines are running work: "This PC · NAS, detection, Plex" and
// "Mac · transcription", each online/offline with last seen
// (GET /api/workers). Shown right under the health tiles. A worker on a
// joined machine (Epic 31) is named after the machine; admins get a link
// to the Machines page.
import { Link } from "react-router-dom";
import { useOptionalAuth } from "../../lib/auth";
import { bidiIsolate, useI18n } from "../../lib/i18n";
import type { WorkerHost } from "../../lib/queueApi";
import { useFmt } from "./format";

export function WorkersStrip({ workers }: { workers: WorkerHost[] | null }) {
  const { t } = useI18n();
  const fmt = useFmt();
  const isAdmin = useOptionalAuth()?.user?.is_admin ?? false;
  if (workers === null) return null;
  return (
    <section className="mkt-q-workers" aria-label={t("queue.workers.title")}>
      <h2 className="mkt-q-section-title">{t("queue.workers.title")}</h2>
      {workers.length === 0 ? (
        <p className="mkt-q-empty">{t("queue.workers.none")}</p>
      ) : (
        <ul className="mkt-q-workers__list">
          {workers.map((w) => {
            const name = w.node_name || w.host;
            return (
              <li
                key={w.worker_id}
                className={`mkt-q-worker mkt-q-worker--${w.online ? "online" : "offline"}`}
              >
                <span className="mkt-q-worker__state">
                  <span aria-hidden="true">{w.online ? "●" : "○"}</span>{" "}
                  {w.online ? t("queue.workers.online") : t("queue.workers.offline")}
                </span>
                <strong>
                  {isAdmin && w.node_id ? (
                    <Link to="/admin/machines">{bidiIsolate(name)}</Link>
                  ) : (
                    bidiIsolate(name)
                  )}
                </strong>
                {w.node_name && w.host && w.host !== w.node_name && (
                  <span className="mkt-q-muted">({bidiIsolate(w.host)})</span>
                )}
                <span className="mkt-q-muted">· {w.serves_label}</span>
                <span className="mkt-q-muted">
                  · {t("queue.workers.lastSeen", { time: fmt.ago(w.last_seen_at) })}
                </span>
              </li>
            );
          })}
        </ul>
      )}
      {isAdmin && (
        <p className="mkt-q-muted">
          <Link to="/admin/machines">{t("queue.workers.manage")}</Link>
        </p>
      )}
    </section>
  );
}
