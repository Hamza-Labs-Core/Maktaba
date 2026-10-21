// Small presentational pieces of the item page: KindChips, AppearsIn and
// Timeline (history of job results). Kept together; each is exported.
import { Link } from "react-router-dom";
import { bidiIsolate, useI18n } from "../../lib/i18n";
import type { AppearsInEntry, StageInfo, TimelineEntry, WriteChange } from "../../lib/queueApi";
import { useFmt } from "./format";
import { jobHref } from "./RunningJobs";
import { HelpLink } from "../../help/HelpLink";
import { causeHelp } from "../../help/registry";

export function KindChips({ kinds }: { kinds: string[] }) {
  const { t } = useI18n();
  return (
    <ul className="mkt-q-kinds" aria-label={t("queue.item.kinds")}>
      {kinds.map((k) => (
        <li className="mkt-q-kind" key={k}>
          {t(`queue.kind.${k}`) === `queue.kind.${k}` ? k : t(`queue.kind.${k}`)}
        </li>
      ))}
    </ul>
  );
}

const EXTENSION_OF: Partial<Record<AppearsInEntry["target"], string>> = {
  plex_collection: "plex",
  dizquetv_show: "dizquetv",
  dizquetv_channel: "dizquetv",
};

/** Plex / dizqueTV rows are shown only while that extension is enabled. */
export function visibleAppears(
  entries: AppearsInEntry[],
  enabledExtensions: ReadonlySet<string>
): AppearsInEntry[] {
  return entries.filter((a) => {
    const ext = EXTENSION_OF[a.target];
    return !ext || enabledExtensions.has(ext);
  });
}

export function AppearsIn({ entries }: { entries: AppearsInEntry[] }) {
  const { t } = useI18n();
  if (entries.length === 0) return <p className="mkt-q-empty">{t("queue.item.appearsNone")}</p>;
  return (
    <ul className="mkt-q-appears">
      {entries.map((a) => (
        <li key={`${a.target}:${a.external_id}`}>
          <span className="mkt-q-chip">{t(`queue.appears.${a.target}`)}</span>{" "}
          <strong>{bidiIsolate(a.title)}</strong>
          {a.detail && <span className="mkt-q-muted"> · {a.detail}</span>}
        </li>
      ))}
    </ul>
  );
}

export type TimelineFilter = "all" | "changes" | "problems";

const CHANGE_OUTCOMES = new Set(["changed", "imported", "awaiting_review"]);

export function isProblemEntry(e: TimelineEntry): boolean {
  return (
    e.job.state === "failed" || e.job.outcome === "awaiting_review" || e.job.error_cause !== null
  );
}
export function isChangeEntry(e: TimelineEntry): boolean {
  return (
    (e.job.outcome !== null && CHANGE_OUTCOMES.has(e.job.outcome)) ||
    e.triggered.length > 0 ||
    (e.changes?.length ?? 0) > 0
  );
}
export function filterTimeline(entries: TimelineEntry[], f: TimelineFilter): TimelineEntry[] {
  if (f === "problems") return entries.filter(isProblemEntry);
  if (f === "changes") return entries.filter(isChangeEntry);
  return entries;
}

const CHANGE_GLYPH: Record<WriteChange["action"], string> = {
  added: "+",
  removed: "-",
  reordered: "~",
  created: "*",
  deleted: "x",
  updated: "~",
};

// What an automatic external write changed, e.g. "Added 3 episodes to
// Fiqh of worship: Lecture 22, Lecture 23, Lecture 24".
export function WriteChanges({ changes }: { changes: WriteChange[] }) {
  const { t, n } = useI18n();
  return (
    <ul className="mkt-q-changes" aria-label={t("queue.change.title")}>
      {changes.map((c, i) => (
        <li key={i} className={`mkt-q-change mkt-q-change--${c.action}`}>
          <span aria-hidden="true">{CHANGE_GLYPH[c.action]}</span>{" "}
          {t(`queue.change.${c.action}.${c.what}`, {
            n: n(c.count ?? c.items?.length ?? 0),
            target: bidiIsolate(c.target),
          })}
          {c.items && c.items.length > 0 && (
            <span className="mkt-q-muted">: {c.items.map((x) => bidiIsolate(x)).join(", ")}</span>
          )}
        </li>
      ))}
    </ul>
  );
}

export function Timeline({
  entries,
  stages,
}: {
  entries: TimelineEntry[];
  stages: Map<string, StageInfo>;
}) {
  const { t, formatDate } = useI18n();
  const fmt = useFmt();
  if (entries.length === 0) return <p className="mkt-q-empty">{t("queue.item.timelineNone")}</p>;
  return (
    <ol className="mkt-q-timeline">
      {entries.map((e) => {
        const j = e.job;
        const tone = isProblemEntry(e) ? "problem" : isChangeEntry(e) ? "change" : "plain";
        const result =
          j.state === "failed"
            ? t("queue.result.failed")
            : j.outcome
              ? t(`queue.outcome.${j.outcome}`)
              : t(`queue.jobstate.${j.state}`);
        return (
          <li className={`mkt-q-tl mkt-q-tl--${tone}`} key={j.id}>
            <div className="mkt-q-tl__head">
              <time dateTime={j.finished_at ?? j.created_at}>
                {formatDate(j.finished_at ?? j.created_at)}
              </time>
              <Link className="mkt-q-chip" to={`/queue/${encodeURIComponent(j.stage)}`}>
                {stages.get(j.stage)?.label ?? j.stage}
              </Link>
              <strong className="mkt-q-tl__result">
                <span aria-hidden="true">
                  {tone === "problem" ? "! " : tone === "change" ? "● " : "○ "}
                </span>
                {result}
              </strong>
              {e.duration_ms !== null && (
                <span className="mkt-q-muted">{fmt.ms(e.duration_ms)}</span>
              )}
              {j.subject_kind !== "catalog" && (
                <Link className="mkt-q-tl__subject" to={jobHref(j)}>
                  {bidiIsolate(j.subject_label)}
                </Link>
              )}
            </div>
            {e.changes && e.changes.length > 0 && <WriteChanges changes={e.changes} />}
            {j.error_cause && (
              <div className="mkt-q-tl__errrow">
                <p className="mkt-q-tl__err">{j.error_cause}</p>
                <HelpLink
                  to={causeHelp(j.error_cause)}
                  label={t("help.causeHelpNamed", { cause: j.error_cause })}
                />
              </div>
            )}
            {e.reasons.length > 0 && (
              <p className="mkt-q-muted">
                {t("queue.item.because")} {e.reasons.map((r) => `${r.why} (${r.by})`).join("; ")}
              </p>
            )}
            {e.triggered.length > 0 && (
              <p className="mkt-q-tl__triggered">
                <span aria-hidden="true">→ </span>
                {t("queue.item.requeued")}{" "}
                {e.triggered
                  .map(
                    (x) =>
                      `${stages.get(x.stage)?.label ?? x.stage}: ${bidiIsolate(x.subject_label)} (${t(`queue.enq.${x.outcome}`)})`
                  )
                  .join(", ")}
              </p>
            )}
          </li>
        );
      })}
    </ol>
  );
}
