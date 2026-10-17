// Which machines are running work: "This PC · NAS, detection, Plex" and
// "Mac · transcription", each online/offline with last seen
// (GET /api/workers). Shown right under the health tiles.
import { bidiIsolate, useI18n } from "../../lib/i18n";
import type { WorkerHost } from "../../lib/queueApi";
import { useFmt } from "./format";

export function WorkersStrip({ workers }: { workers: WorkerHost[] | null }) {
  const { t } = useI18n();
  const fmt = useFmt();
  if (workers === null) return null;
  return (
    <section className="mkt-q-workers" aria-label={t("queue.workers.title")}>
      <h2 className="mkt-q-section-title">{t("queue.workers.title")}</h2>
      {workers.length === 0 ? (
        <p className="mkt-q-empty">{t("queue.workers.none")}</p>
      ) : (
        <ul className="mkt-q-workers__list">
          {workers.map((w) => (
            <li
              key={w.worker_id}
              className={`mkt-q-worker mkt-q-worker--${w.online ? "online" : "offline"}`}
            >
              <span className="mkt-q-worker__state">
                <span aria-hidden="true">{w.online ? "●" : "○"}</span>{" "}
                {w.online ? t("queue.workers.online") : t("queue.workers.offline")}
              </span>
              <strong>{bidiIsolate(w.host)}</strong>
              <span className="mkt-q-muted">· {w.serves_label}</span>
              <span className="mkt-q-muted">
                · {t("queue.workers.lastSeen", { time: fmt.ago(w.last_seen_at) })}
              </span>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
