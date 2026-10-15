// Libraries table: per-library strip of small bars (one per queue:
// done / running / waiting / failed), seasons, last change, Re-queue.
// Search filters by name; rows are multi-selectable for a bulk re-queue.
import { useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { bidiIsolate, useI18n } from "../../lib/i18n";
import type { LibraryQueueRow, StageInfo } from "../../lib/queueApi";
import { useFmt } from "./format";

export interface LibrariesTableProps {
  rows: LibraryQueueRow[];
  stages: StageInfo[];
  onRequeue: (rows: LibraryQueueRow[]) => void;
}

function Bar({ label, s }: { label: string; s: LibraryQueueRow["strip"][number] }) {
  const { t, n } = useI18n();
  const total = Math.max(s.total, s.done + s.running + s.pending + s.failed, 1);
  const pct = (v: number) => `${(Math.min(v, total) / total) * 100}%`;
  const text = t("queue.libs.barAria", {
    label,
    done: n(s.done),
    total: n(s.total),
    running: n(s.running),
    pending: n(s.pending),
    failed: n(s.failed),
  });
  return (
    <li className="mkt-q-strip__item">
      <span className="mkt-q-strip__label" aria-hidden="true">
        {label}
      </span>
      <span className="mkt-q-strip__bar" role="img" aria-label={text} title={text}>
        <span className="mkt-q-seg mkt-q-seg--done" style={{ inlineSize: pct(s.done) }} />
        <span className="mkt-q-seg mkt-q-seg--running" style={{ inlineSize: pct(s.running) }} />
        <span className="mkt-q-seg mkt-q-seg--pending" style={{ inlineSize: pct(s.pending) }} />
        <span className="mkt-q-seg mkt-q-seg--failed" style={{ inlineSize: pct(s.failed) }} />
      </span>
    </li>
  );
}

export function LibrariesTable({ rows, stages, onRequeue }: LibrariesTableProps) {
  const { t, n } = useI18n();
  const fmt = useFmt();
  const [query, setQuery] = useState("");
  const [picked, setPicked] = useState<Set<string>>(new Set());
  const label = useMemo(() => new Map(stages.map((s) => [s.stage, s.label])), [stages]);
  const filtered = rows.filter((r) => r.name.toLowerCase().includes(query.trim().toLowerCase()));
  const selectedRows = rows.filter((r) => picked.has(r.library_id));

  const toggle = (id: string) =>
    setPicked((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  const allShown = filtered.length > 0 && filtered.every((r) => picked.has(r.library_id));

  return (
    <section className="mkt-q-libs" aria-labelledby="mkt-q-libs-h">
      <div className="mkt-q-libs__bar">
        <h2 id="mkt-q-libs-h" className="mkt-q-section-title">
          {t("queue.libs.title")}
        </h2>
        <label className="mkt-q-field mkt-q-field--search">
          <span className="mkt-visually-hidden">{t("queue.libs.search")}</span>
          <input
            type="search"
            value={query}
            placeholder={t("queue.libs.search")}
            onChange={(e) => setQuery(e.target.value)}
          />
        </label>
        <Button
          size="sm"
          variant="secondary"
          disabled={selectedRows.length === 0}
          onClick={() => onRequeue(selectedRows)}
        >
          {t("queue.libs.requeueSelected", { n: n(selectedRows.length) })}
        </Button>
      </div>
      {filtered.length === 0 ? (
        <p className="mkt-q-empty">{t("queue.libs.none")}</p>
      ) : (
        <div className="mkt-q-tablewrap">
          <table className="mkt-q-table">
            <thead>
              <tr>
                <th scope="col">
                  <input
                    type="checkbox"
                    aria-label={t("queue.libs.selectAll")}
                    checked={allShown}
                    onChange={() =>
                      setPicked(allShown ? new Set() : new Set(filtered.map((r) => r.library_id)))
                    }
                  />
                </th>
                <th scope="col">{t("queue.libs.col.library")}</th>
                <th scope="col">{t("queue.libs.col.queues")}</th>
                <th scope="col" className="mkt-q-num">
                  {t("queue.libs.col.seasons")}
                </th>
                <th scope="col">{t("queue.libs.col.changed")}</th>
                <th scope="col">
                  <span className="mkt-visually-hidden">{t("queue.libs.col.actions")}</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {filtered.map((r) => (
                <tr key={r.library_id}>
                  <td>
                    <input
                      type="checkbox"
                      aria-label={t("queue.libs.select", { name: r.name })}
                      checked={picked.has(r.library_id)}
                      onChange={() => toggle(r.library_id)}
                    />
                  </td>
                  <th scope="row">
                    <Link to={`/queue/items/library/${encodeURIComponent(r.library_id)}`}>
                      {bidiIsolate(r.name)}
                    </Link>
                    <span className="mkt-q-muted">
                      {" "}
                      {t("queue.libs.videos", { n: n(r.videos) })}
                    </span>
                  </th>
                  <td>
                    <ul className="mkt-q-strip">
                      {r.strip.map((s) => (
                        <Bar key={s.stage} s={s} label={label.get(s.stage) ?? s.stage} />
                      ))}
                    </ul>
                  </td>
                  <td className="mkt-q-num">{n(r.seasons)}</td>
                  <td>{fmt.ago(r.last_activity_at)}</td>
                  <td>
                    <Button size="sm" variant="ghost" onClick={() => onRequeue([r])}>
                      {t("queue.libs.requeue")}
                      <span className="mkt-visually-hidden"> {r.name}</span>
                    </Button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}
