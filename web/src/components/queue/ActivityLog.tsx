// Live activity log: REST history (GET /api/activity) + `job.log` lines
// from the jobs SSE stream, with a queue filter and "problems only".
// Retains/render-caps the list so a chatty library cannot freeze the page.
import { useEffect, useMemo, useRef, useState } from "react";
import { Button } from "@ds/components/Button/Button";
import { bidiIsolate, useI18n } from "../../lib/i18n";
import { queueApi } from "../../lib/queueApi";
import type { Lane, LogLine, StageInfo } from "../../lib/queueApi";
import type { LiveLine } from "../../lib/useQueueStream";

const RENDER_CAP = 200;

type AnyLine = LogLine | LiveLine;

export interface ActivityLogProps {
  stages: StageInfo[];
  live: LiveLine[];
  /** Locks the log to one queue (queue page) and hides the queue filter. */
  fixedStage?: string;
  libraryId?: string;
  /** Pre-fetched lines for the unfiltered view (avoids a first request). */
  initial?: LogLine[];
}

const isProblem = (l: AnyLine) => l.level === "warn" || l.level === "error";

const timeOf = (iso: string) => {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? "" : d.toLocaleTimeString([], { hour12: false });
};

export function ActivityLog({ stages, live, fixedStage, libraryId, initial }: ActivityLogProps) {
  const { t } = useI18n();
  const [stage, setStage] = useState<string>(fixedStage ?? "");
  const [problems, setProblems] = useState(false);
  const [items, setItems] = useState<LogLine[] | null>(initial ?? null);
  const [next, setNext] = useState<string | null>(null);
  const [failed, setFailed] = useState(false);
  const [showAll, setShowAll] = useState(false);
  const [loadingOlder, setLoadingOlder] = useState(false);
  const skipInitial = useRef(initial !== undefined);

  const effectiveStage = fixedStage ?? stage;

  useEffect(() => {
    if (skipInitial.current && !problems && !stage) {
      skipInitial.current = false;
      return;
    }
    skipInitial.current = false;
    let alive = true;
    setItems(null);
    queueApi
      .activity({
        stage: effectiveStage || undefined,
        library_id: libraryId,
        level: problems ? "warn" : "info",
        limit: 100,
      })
      .then((r) => {
        if (!alive) return;
        setItems(r.items);
        setNext(r.next);
        setFailed(false);
      })
      .catch(() => {
        if (alive) {
          setItems([]);
          setFailed(true);
        }
      });
    return () => {
      alive = false;
    };
  }, [effectiveStage, libraryId, problems, stage]);

  const stageMeta = useMemo(() => new Map(stages.map((s) => [s.stage, s])), [stages]);

  const lines: AnyLine[] = useMemo(() => {
    const liveF = live.filter(
      (l) => (!effectiveStage || l.stage === effectiveStage) && (!problems || isProblem(l))
    );
    return [...liveF, ...(items ?? [])];
  }, [live, items, effectiveStage, problems]);

  const shown = showAll ? lines : lines.slice(0, RENDER_CAP);

  async function older() {
    if (!next) return;
    setLoadingOlder(true);
    try {
      const r = await queueApi.activity({
        stage: effectiveStage || undefined,
        library_id: libraryId,
        level: problems ? "warn" : "info",
        cursor: next,
        limit: 100,
      });
      setItems((prev) => [...(prev ?? []), ...r.items]);
      setNext(r.next);
    } catch {
      setFailed(true);
    } finally {
      setLoadingOlder(false);
    }
  }

  const laneOf = (s: string): Lane => stageMeta.get(s)?.lane ?? "media";

  return (
    <div className="mkt-q-log" role="group" aria-label={t("queue.log.title")}>
      <div className="mkt-q-log__filters">
        {!fixedStage && (
          <label className="mkt-q-field">
            <span>{t("queue.log.filterQueue")}</span>
            <select value={stage} onChange={(e) => setStage(e.target.value)}>
              <option value="">{t("queue.log.allQueues")}</option>
              {stages
                .filter((s) => !s.hidden)
                .map((s) => (
                  <option key={s.stage} value={s.stage}>
                    {s.label}
                  </option>
                ))}
            </select>
          </label>
        )}
        <label className="mkt-q-check">
          <input
            type="checkbox"
            checked={problems}
            onChange={(e) => setProblems(e.target.checked)}
          />
          <span>{t("queue.log.problemsOnly")}</span>
        </label>
      </div>
      {items === null && lines.length === 0 ? (
        <p className="mkt-q-empty">{t("common.loading")}</p>
      ) : lines.length === 0 ? (
        <p className="mkt-q-empty">{failed ? t("queue.log.error") : t("queue.log.empty")}</p>
      ) : (
        <ol className="mkt-q-log__list" aria-live="off">
          {shown.map((l) => (
            <li className={`mkt-q-logline mkt-q-logline--${l.level}`} key={l.id}>
              <time className="mkt-q-logline__time" dateTime={l.at}>
                {timeOf(l.at)}
              </time>
              <span className={`mkt-q-chip mkt-q-chip--lane-${laneOf(l.stage)}`}>
                {stageMeta.get(l.stage)?.label ?? l.stage}
              </span>
              {isProblem(l) && (
                <span className="mkt-q-logline__level">
                  <span aria-hidden="true">{l.level === "error" ? "✕" : "!"}</span>
                  <span className="mkt-visually-hidden">{t(`queue.level.${l.level}`)}</span>
                </span>
              )}
              <span className="mkt-q-logline__text">
                {l.subject_label && <strong>{bidiIsolate(l.subject_label)}</strong>} {l.message}
              </span>
            </li>
          ))}
        </ol>
      )}
      <div className="mkt-q-log__more">
        {lines.length > RENDER_CAP && !showAll && (
          <Button size="sm" variant="ghost" onClick={() => setShowAll(true)}>
            {t("queue.log.showAll")}
          </Button>
        )}
        {next && (
          <Button size="sm" variant="ghost" loading={loadingOlder} onClick={() => void older()}>
            {t("queue.log.older")}
          </Button>
        )}
      </div>
    </div>
  );
}
