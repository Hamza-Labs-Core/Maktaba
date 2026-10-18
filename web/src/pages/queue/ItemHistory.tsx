// /queue/items/:kind/:id — everything the queues did for one library,
// series, season or video: re-queue buttons per queue, seasons as bars,
// where it appears, and the job history timeline.
import { useCallback, useEffect, useMemo, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { bidiIsolate, useI18n } from "../../lib/i18n";
import { queueApi } from "../../lib/queueApi";
import { reportsApi } from "../../lib/reportsApi";
import type { ItemHistoryData, SubjectKind } from "../../lib/queueApi";
import { useJobStream, useQueuesSnapshot } from "../../lib/useQueueStream";
import {
  AppearsIn,
  KindChips,
  visibleAppears,
  Timeline,
  filterTimeline,
  type TimelineFilter,
} from "../../components/queue/ItemParts";
import { LibraryCollections } from "../../components/queue/LibraryCollections";
import { RetriggerDialog } from "../../components/queue/RetriggerDialog";
import { STATE_GLYPH, useFmt } from "../../components/queue/format";
import { useAction } from "../../components/queue/useAction";
import { useThrottledReload } from "../../components/queue/useThrottledReload";
import "../../styles/queue.css";

const FILTERS: TimelineFilter[] = ["all", "changes", "problems"];

const itemLink = (kind: string, id: string): string | null => {
  switch (kind) {
    case "video":
      return `/videos/${encodeURIComponent(id)}`;
    case "library":
      return `/library/${encodeURIComponent(id)}`;
    case "series":
      return `/series/${encodeURIComponent(id)}`;
    default:
      return null;
  }
};

export function ItemHistory() {
  const { kind = "", id = "" } = useParams<{ kind: string; id: string }>();
  const { t, n } = useI18n();
  const fmt = useFmt();
  const { snapshot } = useQueuesSnapshot();
  const stream = useJobStream({ subjectId: id });
  const action = useAction();

  const [data, setData] = useState<ItemHistoryData | null>(null);
  const [missing, setMissing] = useState(false);
  const [filter, setFilter] = useState<TimelineFilter>("all");
  const [status, setStatus] = useState<string | null>(null);
  const [dialog, setDialog] = useState(false);
  const [loadingMore, setLoadingMore] = useState(false);
  // a library has a review page once the review_pages queue has written it
  const [hasReview, setHasReview] = useState(false);
  // While unknown (or if the call fails) extension rows stay hidden.
  const [enabledExt, setEnabledExt] = useState<ReadonlySet<string>>(new Set());

  useEffect(() => {
    let alive = true;
    queueApi
      .extensions()
      .then(
        (r) =>
          alive && setEnabledExt(new Set(r.extensions.filter((e) => e.enabled).map((e) => e.name)))
      )
      .catch(() => {});
    return () => {
      alive = false;
    };
  }, []);

  useEffect(() => {
    if (kind !== "library") return;
    let alive = true;
    reportsApi
      .list()
      .then((r) => alive && setHasReview(r.reports.some((x) => x.library_id === id)))
      .catch(() => {});
    return () => {
      alive = false;
    };
  }, [kind, id]);

  const load = useCallback(() => {
    queueApi
      .history(kind, id)
      .then((d) => {
        setData(d);
        setMissing(false);
      })
      .catch(() => setMissing(true));
  }, [kind, id]);

  useEffect(() => {
    setData(null);
    setStatus(null);
    load();
  }, [load]);
  useThrottledReload(load, stream.finishedTick, 5_000);

  const stages = useMemo(
    () => new Map((snapshot?.stages ?? []).map((s) => [s.stage, s])),
    [snapshot?.stages]
  );

  if (missing && !data) {
    return (
      <section className="mkt-page mkt-q">
        <p role="alert" className="mkt-q-banner">
          {t("queue.item.notFound")}
        </p>
        <Link to="/queue">{t("queue.detail.back")}</Link>
      </section>
    );
  }
  if (!data) {
    return (
      <section className="mkt-page mkt-q">
        <p className="mkt-loading" role="status">
          {t("common.loading")}
        </p>
      </section>
    );
  }

  const subject = data.subject;
  const subjectKind = subject.kind as SubjectKind;
  const open = itemLink(subject.kind, subject.id);
  const maxEpisodes = Math.max(1, ...(data.seasons ?? []).map((s) => s.episode_count));
  const visible = filterTimeline(data.timeline, filter);

  const requeue = (stage: string | undefined, mode: "queue" | "force_cascade") =>
    void action
      .run(stage ?? "all", () =>
        queueApi.retrigger({
          items: [
            { subject_kind: subjectKind, subject_id: subject.id, ...(stage ? { stage } : {}) },
          ],
          mode,
        })
      )
      .then((r) => {
        if (!r) return;
        const inserted = r.results.filter((x) => x.outcome === "inserted").length;
        const merged = r.results.filter(
          (x) => x.outcome === "coalesced" || x.outcome === "rerun_requested"
        ).length;
        setStatus(
          t("queue.item.queued", {
            queued: n(inserted),
            merged: n(merged),
            skipped: n(r.results.length - inserted - merged),
          })
        );
        load();
      });

  async function more() {
    if (!data?.next) return;
    setLoadingMore(true);
    try {
      const next = await queueApi.history(kind, id, data.next);
      setData((prev) =>
        prev ? { ...prev, timeline: [...prev.timeline, ...next.timeline], next: next.next } : next
      );
    } catch {
      /* the button stays; the user can retry */
    } finally {
      setLoadingMore(false);
    }
  }

  return (
    <section className="mkt-page mkt-q">
      <nav aria-label={t("queue.detail.breadcrumb")} className="mkt-q-crumbs">
        <ol>
          <li>
            <Link to="/queue">{t("queue.title")}</Link>
          </li>
          {subject.library_id && subject.kind !== "library" && (
            <li>
              <Link to={`/queue/items/library/${encodeURIComponent(subject.library_id)}`}>
                {bidiIsolate(subject.library_label ?? subject.library_id)}
              </Link>
            </li>
          )}
          <li aria-current="page">{bidiIsolate(subject.label)}</li>
        </ol>
      </nav>

      <header className="mkt-q-detailhead">
        <div>
          <p className="mkt-q-eyebrow">{t(`queue.subject.${subject.kind}`)}</p>
          <h1>{bidiIsolate(subject.label)}</h1>
          <KindChips kinds={data.kinds} />
          {open && (
            <p>
              <Link to={open}>{t("queue.item.open")}</Link>
            </p>
          )}
          {hasReview && (
            <p>
              <Link to={`/queue/reports?open=${encodeURIComponent(`${subject.id}.html`)}`}>
                {t("queue.item.reviewPage")}
              </Link>
            </p>
          )}
        </div>
      </header>

      <section className="mkt-q-panel" aria-labelledby="mkt-q-rq-h">
        <h2 className="mkt-q-section-title" id="mkt-q-rq-h">
          {t("queue.item.requeue")}
        </h2>
        <ul className="mkt-q-requeue">
          {data.queues.map((q) => {
            const label = stages.get(q.stage)?.label ?? q.stage;
            const last = q.last_job;
            return (
              <li key={q.stage} className="mkt-q-requeue__row">
                <Button
                  size="sm"
                  variant="secondary"
                  disabled={!q.can_requeue}
                  loading={action.busy === q.stage}
                  onClick={() => requeue(q.stage, "queue")}
                >
                  {t("queue.item.requeueOne", { queue: label })}
                </Button>
                <span className="mkt-q-muted">
                  {last ? (
                    <>
                      <span aria-hidden="true">
                        {last.state === "failed" ? STATE_GLYPH.failing : STATE_GLYPH.done}
                      </span>{" "}
                      {last.outcome
                        ? t(`queue.outcome.${last.outcome}`)
                        : t(`queue.jobstate.${last.state}`)}{" "}
                      · {fmt.ago(last.finished_at ?? last.created_at)}
                    </>
                  ) : (
                    t("queue.item.neverRun")
                  )}
                </span>
              </li>
            );
          })}
        </ul>
        <div className="mkt-q-requeue__all">
          <Button
            variant="primary"
            loading={action.busy === "all"}
            onClick={() => requeue(undefined, "force_cascade")}
          >
            {t("queue.item.fromStart")}
          </Button>
          <Button variant="ghost" onClick={() => setDialog(true)}>
            {t("queue.item.chooseQueues")}
          </Button>
        </div>
        <p className="mkt-q-note">{t("queue.item.fromStartNote")}</p>
        {status && (
          <p role="status" className="mkt-q-note">
            {status}
          </p>
        )}
        {action.error && (
          <p role="alert" className="mkt-q-banner">
            {action.error}
          </p>
        )}
      </section>

      {data.seasons && data.seasons.length > 0 && (
        <section className="mkt-q-panel" aria-labelledby="mkt-q-seasons-h">
          <h2 className="mkt-q-section-title" id="mkt-q-seasons-h">
            {data.kinds.includes("ongoing") ? t("queue.item.years") : t("queue.item.seasons")}
          </h2>
          <ul className="mkt-q-seasons">
            {data.seasons.map((s) => (
              <li key={s.id} className="mkt-q-season">
                <Link to={`/queue/items/season/${encodeURIComponent(s.id)}`}>
                  {bidiIsolate(s.label)}
                </Link>
                <span
                  className="mkt-q-season__bar"
                  role="img"
                  aria-label={t("queue.item.episodes", { n: n(s.episode_count) })}
                >
                  <span
                    className="mkt-q-seg mkt-q-seg--done"
                    style={{ inlineSize: `${(s.episode_count / maxEpisodes) * 100}%` }}
                  />
                </span>
                <span className="mkt-q-season__n">
                  {t("queue.item.episodes", { n: n(s.episode_count) })}
                </span>
              </li>
            ))}
          </ul>
        </section>
      )}

      {subject.kind === "library" && <LibraryCollections libraryId={subject.id} />}

      <section className="mkt-q-panel" aria-labelledby="mkt-q-appears-h">
        <h2 className="mkt-q-section-title" id="mkt-q-appears-h">
          {t("queue.item.appears")}
        </h2>
        <AppearsIn entries={visibleAppears(data.appears_in, enabledExt)} />
      </section>

      <section className="mkt-q-panel" aria-labelledby="mkt-q-hist-h">
        <div className="mkt-q-panel__bar">
          <h2 className="mkt-q-section-title" id="mkt-q-hist-h">
            {t("queue.item.history")}
          </h2>
          <div className="mkt-q-segmented" role="group" aria-label={t("queue.item.filter")}>
            {FILTERS.map((f) => (
              <button
                key={f}
                type="button"
                className="mkt-q-segmented__btn"
                aria-pressed={filter === f}
                onClick={() => setFilter(f)}
              >
                {t(`queue.item.filter.${f}`)}
              </button>
            ))}
          </div>
        </div>
        <Timeline entries={visible} stages={stages} />
        {data.next && (
          <Button variant="ghost" loading={loadingMore} onClick={() => void more()}>
            {t("queue.log.older")}
          </Button>
        )}
      </section>

      <RetriggerDialog
        open={dialog}
        onClose={() => setDialog(false)}
        stages={snapshot?.stages ?? []}
        libraries={[]}
        initialSubjects={[{ kind: subjectKind, id: subject.id, label: subject.label }]}
        onDone={load}
      />
    </section>
  );
}
