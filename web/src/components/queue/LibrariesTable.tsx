// Libraries table: per-library strip of small bars (one per queue:
// done / running / waiting / failed), seasons, last change, Re-queue.
// Search filters by name; rows are multi-selectable for a bulk re-queue.
// Epic 47 (admins): each row's menu runs Refresh from YouTube, Convert to MP4
// and Find duplicates (pipeline order: download -> convert -> dedup) and opens
// its YouTube sources; the same three run for the selected rows. The YouTube,
// Not MP4 and Duplicates columns come from the offline summary.
import { useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { Menu } from "@ds/components/Menu/Menu";
import { bidiIsolate, useI18n } from "../../lib/i18n";
import type { LibraryQueueRow, OfflineSummary, StageInfo } from "../../lib/queueApi";
import { useFmt } from "./format";
import { OFFLINE_ACTIONS, type OfflineAction } from "./offline";

export interface LibrariesTableProps {
  rows: LibraryQueueRow[];
  stages: StageInfo[];
  onRequeue: (rows: LibraryQueueRow[]) => void;
  /** Epic 47 columns, by library id (admins; absent: the columns are not shown) */
  offline?: Record<string, OfflineSummary>;
  /** Epic 47 actions (admins only; absent: no menus) */
  onOffline?: (action: OfflineAction, rows: LibraryQueueRow[]) => void;
  onSources?: (row: LibraryQueueRow) => void;
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

/** The YouTube column: sources on; wanted / downloaded / failed; the back catalogue; last refresh. */
export function YouTubeCell({ s }: { s: OfflineSummary | undefined }) {
  const { t, n } = useI18n();
  const fmt = useFmt();
  if (!s || s.youtube.sources === 0) return <span className="mkt-q-muted">–</span>;
  const y = s.youtube;
  return (
    <div className="mkt-q-libs__yt">
      <div>
        {t("queue.libs.yt.on", { on: n(y.sources_on), total: n(y.sources) })}
        {y.downloading > 0 && <> · {t("queue.libs.yt.downloading", { n: n(y.downloading) })}</>}
      </div>
      <div className="mkt-q-muted">
        {t("queue.libs.yt.counts", {
          wanted: n(y.wanted),
          done: n(y.downloaded),
          failed: n(y.failed),
        })}
        {y.backfill > 0 && <> · {t("queue.libs.yt.backfill", { n: n(y.backfill) })}</>}
      </div>
      <div className="mkt-q-muted">{t("queue.libs.yt.refreshed", { time: fmt.ago(y.last_refresh) })}</div>
      {y.problems.length > 0 && (
        <div className="mkt-q-error">
          {t("queue.libs.yt.problems", { n: n(y.problems.length) })}
        </div>
      )}
    </div>
  );
}

export function LibrariesTable({
  rows,
  stages,
  onRequeue,
  offline,
  onOffline,
  onSources,
}: LibrariesTableProps) {
  const { t, n } = useI18n();
  const fmt = useFmt();
  const [query, setQuery] = useState("");
  const [picked, setPicked] = useState<Set<string>>(new Set());
  const label = useMemo(() => new Map(stages.map((s) => [s.stage, s.label])), [stages]);
  const filtered = rows.filter((r) => r.name.toLowerCase().includes(query.trim().toLowerCase()));
  const selectedRows = rows.filter((r) => picked.has(r.library_id));
  const showOffline = offline !== undefined;

  const toggle = (id: string) =>
    setPicked((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  const allShown = filtered.length > 0 && filtered.every((r) => picked.has(r.library_id));

  const rowItems = (r: LibraryQueueRow) => [
    ...OFFLINE_ACTIONS.map((a) => ({
      id: a,
      label: t(`queue.offline.action.${a}`),
      onSelect: () => onOffline?.(a, [r]),
    })),
    ...(onSources
      ? [{ id: "sources", label: t("queue.offline.sources"), onSelect: () => onSources(r) }]
      : []),
  ];

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
        {onOffline && selectedRows.length > 0 && (
          <Menu
            className="mkt-q-libs__bulk"
            trigger={t("queue.libs.withSelected", { n: n(selectedRows.length) })}
            items={OFFLINE_ACTIONS.map((a) => ({
              id: a,
              label: t(`queue.offline.action.${a}`),
              onSelect: () => onOffline(a, selectedRows),
            }))}
          />
        )}
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
                {showOffline && <th scope="col">{t("queue.libs.col.youtube")}</th>}
                {showOffline && (
                  <th scope="col" className="mkt-q-num">
                    {t("queue.libs.col.notMp4")}
                  </th>
                )}
                {showOffline && (
                  <th scope="col" className="mkt-q-num">
                    {t("queue.libs.col.duplicates")}
                  </th>
                )}
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
              {filtered.map((r) => {
                const o = offline?.[r.library_id];
                return (
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
                    {showOffline && (
                      <td>
                        <YouTubeCell s={o} />
                      </td>
                    )}
                    {showOffline && (
                      <td className="mkt-q-num">
                        {o ? n(o.not_mp4) : "–"}
                        {o && o.converting > 0 && (
                          <div className="mkt-q-muted">
                            {t("queue.libs.converting", { n: n(o.converting) })}
                          </div>
                        )}
                      </td>
                    )}
                    {showOffline && (
                      <td className="mkt-q-num">
                        {o && o.duplicates > 0 ? (
                          <Link
                            to={`/queue/duplicates?library=${encodeURIComponent(r.library_id)}`}
                            aria-label={t("queue.libs.duplicatesOf", {
                              n: n(o.duplicates),
                              name: r.name,
                            })}
                          >
                            {n(o.duplicates)}
                          </Link>
                        ) : o ? (
                          n(0)
                        ) : (
                          "–"
                        )}
                      </td>
                    )}
                    <td className="mkt-q-num">{n(r.seasons)}</td>
                    <td>{fmt.ago(r.last_activity_at)}</td>
                    <td>
                      <div className="mkt-q-libs__rowactions">
                        <Button size="sm" variant="ghost" onClick={() => onRequeue([r])}>
                          {t("queue.libs.requeue")}
                          <span className="mkt-visually-hidden"> {r.name}</span>
                        </Button>
                        {onOffline && (
                          <Menu
                            className="mkt-q-libs__menu"
                            trigger={
                              <>
                                <span aria-hidden="true">⋯</span>
                                <span className="mkt-visually-hidden">
                                  {t("queue.libs.actionsFor", { name: r.name })}
                                </span>
                              </>
                            }
                            items={rowItems(r)}
                          />
                        )}
                      </div>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}
