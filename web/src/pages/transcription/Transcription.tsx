// /transcription — live report of the whole-library transcription run:
// hours and videos done, speed, time left, the three stage counters,
// per-library progress and the latest open failures.
import { useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { ProgressBar } from "@ds/components/ProgressBar/ProgressBar";
import { bidiIsolate, useI18n } from "../../lib/i18n";
import {
  TRANSCRIPTION_STAGES,
  type TranscriptionLibrary,
  type TranscriptionSummary,
  type TranscriptionLive,
  type TranscriptionWindow,
} from "../../lib/transcriptionApi";
import { useTranscriptionSummary } from "../../lib/useTranscriptionSummary";
import { useFmt } from "../../components/queue/format";
import "../../styles/queue.css";
import "../../styles/transcription.css";

function pct(done: number, total: number): number {
  if (!(total > 0)) return 0;
  return Math.min(100, Math.max(0, (done / total) * 100));
}

function useHours() {
  const { t, n } = useI18n();
  return (h: number) =>
    t("transcription.unit.hours", {
      n: n(h, { maximumFractionDigits: h > 0 && h < 10 ? 1 : 0 }),
    });
}

export function Transcription() {
  const { t } = useI18n();
  const { summary, error, polling, reload } = useTranscriptionSummary();

  if (!summary) {
    return (
      <section className="mkt-page mkt-tr">
        <h1>{t("transcription.title")}</h1>
        {error ? (
          <div role="alert" className="mkt-q-banner">
            <p>{t("transcription.error.load")}</p>
            <Button variant="secondary" onClick={reload}>
              {t("common.retry")}
            </Button>
          </div>
        ) : (
          <p className="mkt-loading" role="status">
            {t("common.loading")}
          </p>
        )}
      </section>
    );
  }

  return (
    <section className="mkt-page mkt-tr" aria-labelledby="mkt-tr-h">
      <header className="mkt-tr__head">
        <h1 id="mkt-tr-h">{t("transcription.title")}</h1>
        <p className="mkt-tr__live" role="status">
          <span
            className={`mkt-tr__dot ${polling || error ? "mkt-tr__dot--off" : ""}`}
            aria-hidden="true"
          />
          {error
            ? t("transcription.stale")
            : polling
              ? t("transcription.polling")
              : t("transcription.live")}
        </p>
      </header>
      <Progress s={summary} />
      <div className="mkt-tr__grid2">
        <Speed s={summary} />
        <TimeLeft s={summary} />
      </div>
      <Stages s={summary} />
      <Libraries rows={summary.libraries} />
      <Failures s={summary} />
    </section>
  );
}

function Progress({ s }: { s: TranscriptionSummary }) {
  const { t, n } = useI18n();
  const hours = useHours();
  const tot = s.totals;
  const hp = pct(tot.hours_transcribed, tot.audio_hours);
  const vp = pct(tot.videos_transcribed, tot.videos);
  const pctText = (v: number) =>
    t("transcription.percent", { n: n(v, { maximumFractionDigits: 1 }) });
  return (
    <div className="mkt-tr__hero">
      <div className="mkt-tr-card mkt-tr-card--big">
        <h2 className="mkt-tr-card__title">{t("transcription.hours.title")}</h2>
        <p className="mkt-tr-big">
          <span className="mkt-tr-big__pct">{pctText(hp)}</span>
          <span className="mkt-tr-big__of">
            {t("transcription.of", {
              done: hours(tot.hours_transcribed),
              total: hours(tot.audio_hours),
            })}
          </span>
        </p>
        <ProgressBar value={hp} label={t("transcription.hours.aria")} tone="success" />
        <p className="mkt-tr-muted">
          {t("transcription.remaining", { h: hours(tot.hours_remaining) })}
        </p>
      </div>
      <div className="mkt-tr-card mkt-tr-card--big">
        <h2 className="mkt-tr-card__title">{t("transcription.videos.title")}</h2>
        <p className="mkt-tr-big">
          <span className="mkt-tr-big__pct">{pctText(vp)}</span>
          <span className="mkt-tr-big__of">
            {t("transcription.of", { done: n(tot.videos_transcribed), total: n(tot.videos) })}
          </span>
        </p>
        <ProgressBar value={vp} label={t("transcription.videos.aria")} />
        <p className="mkt-tr-muted">{t("transcription.segments", { n: n(tot.segments) })}</p>
      </div>
    </div>
  );
}

function SpeedRow({ label, w }: { label: string; w: TranscriptionWindow }) {
  const { t, n } = useI18n();
  const hours = useHours();
  return (
    <div className="mkt-tr-rate">
      <dt>{label}</dt>
      <dd>
        <span className="mkt-tr-rate__x">
          {t("transcription.rate.value", {
            n: n(w.realtime_factor, { maximumFractionDigits: 1 }),
          })}
        </span>
        <span className="mkt-tr-muted">
          {t("transcription.rate.detail", { h: hours(w.audio_hours), jobs: n(w.jobs) })}
          {w.avg_job_rtf !== null &&
            ` · ${t("transcription.rate.jobRtf", { n: n(w.avg_job_rtf, { maximumFractionDigits: 1 }) })}`}
        </span>
      </dd>
    </div>
  );
}

function LiveRow({ live }: { live: TranscriptionLive }) {
  const { t, n } = useI18n();
  const hours = useHours();
  return (
    <div className="mkt-tr-rate">
      <dt>{t("transcription.rate.live")}</dt>
      <dd>
        <span className="mkt-tr-rate__x">
          {t("transcription.rate.value", {
            n: n(live.realtime_factor, { maximumFractionDigits: 1 }),
          })}
        </span>
        <span className="mkt-tr-muted">
          {t("transcription.rate.liveDetail", {
            h: hours(live.audio_hours),
            workers: n(live.workers),
          })}
          {(live.lost_hours ?? 0) > 0 &&
            ` · ${t("transcription.rate.lost", { h: hours(live.lost_hours ?? 0) })}`}
        </span>
      </dd>
    </div>
  );
}

function Speed({ s }: { s: TranscriptionSummary }) {
  const { t } = useI18n();
  const live = s.rate.live;
  return (
    <div className="mkt-tr-card">
      <h2 className="mkt-tr-card__title">{t("transcription.rate.title")}</h2>
      <dl className="mkt-tr-rates">
        {live && live.workers > 0 && <LiveRow live={live} />}
        <SpeedRow label={t("transcription.rate.last10m")} w={s.rate.last_10m} />
        <SpeedRow label={t("transcription.rate.last1h")} w={s.rate.last_1h} />
      </dl>
    </div>
  );
}

function TimeLeft({ s }: { s: TranscriptionSummary }) {
  const { t, formatDate } = useI18n();
  const fmt = useFmt();
  const { seconds, at, basis } = s.eta;
  let body;
  if (seconds === null) {
    body = <p className="mkt-tr-muted">{t("transcription.eta.unknown")}</p>;
  } else if (seconds === 0) {
    body = <p className="mkt-tr-big__pct">{t("transcription.eta.done")}</p>;
  } else {
    body = (
      <>
        <p className="mkt-tr-big__pct">{fmt.dur(seconds)}</p>
        {at && <p>{t("transcription.eta.at", { t: formatDate(at) })}</p>}
        {basis && <p className="mkt-tr-muted">{t(`transcription.eta.basis.${basis}`)}</p>}
      </>
    );
  }
  return (
    <div className="mkt-tr-card">
      <h2 className="mkt-tr-card__title">{t("transcription.eta.title")}</h2>
      {body}
    </div>
  );
}

const STATES = ["pending", "running", "done", "failed"] as const;

function Stages({ s }: { s: TranscriptionSummary }) {
  const { t, n } = useI18n();
  const byStage = new Map(s.stages.map((c) => [c.stage, c]));
  return (
    <section className="mkt-tr-section" aria-labelledby="mkt-tr-stages-h">
      <h2 id="mkt-tr-stages-h" className="mkt-q-section-title">
        {t("transcription.stages.title")}
      </h2>
      <ul className="mkt-tr-stages">
        {TRANSCRIPTION_STAGES.map((stage) => {
          const c = byStage.get(stage) ?? { stage, pending: 0, running: 0, done: 0, failed: 0 };
          const name = t(`transcription.stage.${stage}`);
          return (
            <li key={stage} className="mkt-tr-card mkt-tr-stage">
              <h3 className="mkt-tr-card__title">
                <Link to={`/queue/${stage}`}>{name}</Link>
              </h3>
              <dl className="mkt-tr-counts">
                {STATES.map((st) => (
                  <div key={st} className={`mkt-tr-count mkt-tr-count--${st}`}>
                    <dt>{t(`transcription.state.${st}`)}</dt>
                    <dd>{n(c[st])}</dd>
                  </div>
                ))}
              </dl>
            </li>
          );
        })}
      </ul>
    </section>
  );
}

type SortKey = "name" | "videos" | "hours" | "progress";

function libValue(r: TranscriptionLibrary, k: SortKey): number | string {
  switch (k) {
    case "name":
      return r.name.toLocaleLowerCase();
    case "videos":
      return r.videos;
    case "hours":
      return r.hours;
    case "progress":
      return pct(r.hours_done, r.hours) || pct(r.videos_done, r.videos);
  }
}

function Libraries({ rows }: { rows: TranscriptionLibrary[] }) {
  const { t, n } = useI18n();
  const hours = useHours();
  const [sort, setSort] = useState<{ key: SortKey; dir: 1 | -1 }>({ key: "hours", dir: -1 });
  const sorted = useMemo(() => {
    const out = [...rows];
    out.sort((a, b) => {
      const x = libValue(a, sort.key);
      const y = libValue(b, sort.key);
      const c = typeof x === "string" ? x.localeCompare(y as string) : x - (y as number);
      return c * sort.dir || a.name.localeCompare(b.name);
    });
    return out;
  }, [rows, sort]);

  const cols: Array<{ key: SortKey; label: string; num?: boolean }> = [
    { key: "name", label: t("transcription.libs.col.library") },
    { key: "videos", label: t("transcription.libs.col.videos"), num: true },
    { key: "hours", label: t("transcription.libs.col.hours"), num: true },
    { key: "progress", label: t("transcription.libs.col.progress") },
  ];
  const by = (key: SortKey) =>
    setSort((p) =>
      p.key === key ? { key, dir: p.dir === 1 ? -1 : 1 } : { key, dir: key === "name" ? 1 : -1 }
    );

  return (
    <section className="mkt-tr-section" aria-labelledby="mkt-tr-libs-h">
      <div className="mkt-tr-section__bar">
        <h2 id="mkt-tr-libs-h" className="mkt-q-section-title">
          {t("transcription.libs.title")}
        </h2>
        <label className="mkt-tr-sort">
          <span>{t("transcription.libs.sort")}</span>
          <select
            value={`${sort.key}:${sort.dir}`}
            onChange={(e) => {
              const [key, dir] = e.target.value.split(":");
              setSort({ key: key as SortKey, dir: dir === "1" ? 1 : -1 });
            }}
          >
            {cols.flatMap((c) => [
              <option key={`${c.key}:-1`} value={`${c.key}:-1`}>
                {t("transcription.libs.sortDesc", { col: c.label })}
              </option>,
              <option key={`${c.key}:1`} value={`${c.key}:1`}>
                {t("transcription.libs.sortAsc", { col: c.label })}
              </option>,
            ])}
          </select>
        </label>
      </div>
      {sorted.length === 0 ? (
        <p className="mkt-tr-muted">{t("transcription.libs.none")}</p>
      ) : (
        <table className="mkt-q-table mkt-tr-libs">
          <thead>
            <tr>
              {cols.map((c) => (
                <th
                  key={c.key}
                  scope="col"
                  className={c.num ? "mkt-q-num" : undefined}
                  aria-sort={
                    sort.key === c.key ? (sort.dir === 1 ? "ascending" : "descending") : "none"
                  }
                >
                  <button type="button" className="mkt-tr-sortbtn" onClick={() => by(c.key)}>
                    {c.label}
                    <span aria-hidden="true">
                      {sort.key === c.key ? (sort.dir === 1 ? " ▲" : " ▼") : ""}
                    </span>
                  </button>
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {sorted.map((r) => {
              const p = pct(r.hours_done, r.hours) || pct(r.videos_done, r.videos);
              return (
                <tr key={r.library_id || r.name}>
                  <th scope="row">{bidiIsolate(r.name || t("transcription.libs.unnamed"))}</th>
                  <td className="mkt-q-num" data-label={cols[1].label}>
                    {t("transcription.of", { done: n(r.videos_done), total: n(r.videos) })}
                  </td>
                  <td className="mkt-q-num" data-label={cols[2].label}>
                    {t("transcription.of", { done: hours(r.hours_done), total: hours(r.hours) })}
                  </td>
                  <td data-label={cols[3].label}>
                    <span className="mkt-tr-libbar">
                      <ProgressBar
                        value={p}
                        label={t("transcription.libs.progressAria", { name: r.name })}
                        tone="success"
                      />
                      <span className="mkt-tr-libbar__pct">
                        {t("transcription.percent", { n: n(Math.floor(p)) })}
                      </span>
                    </span>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      )}
    </section>
  );
}

function Failures({ s }: { s: TranscriptionSummary }) {
  const { t, n } = useI18n();
  const fmt = useFmt();
  return (
    <section className="mkt-tr-section" aria-labelledby="mkt-tr-fail-h">
      <h2 id="mkt-tr-fail-h" className="mkt-q-section-title">
        {t("transcription.fail.title")}
      </h2>
      {s.failures.length === 0 ? (
        <p className="mkt-tr-muted">{t("transcription.fail.none")}</p>
      ) : (
        <ul className="mkt-tr-fails">
          {s.failures.map((f) => (
            <li key={f.job_id} className="mkt-tr-fail">
              <div className="mkt-tr-fail__head">
                <Link to={`/queue/items/video/${encodeURIComponent(f.video_id)}`}>
                  {bidiIsolate(f.title)}
                </Link>
                <span className="mkt-tr-chip">{t(`transcription.stage.${f.stage}`)}</span>
              </div>
              <p className="mkt-tr-fail__cause">
                <strong>{f.error_cause ?? t("transcription.fail.unknownCause")}</strong>
                {f.error && <span> · {f.error}</span>}
              </p>
              <p className="mkt-tr-muted">
                {[
                  f.library_name ? bidiIsolate(f.library_name) : null,
                  t("transcription.fail.attempts", { n: n(f.attempts) }),
                  fmt.ago(f.finished_at),
                ]
                  .filter(Boolean)
                  .join(" · ")}
              </p>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
