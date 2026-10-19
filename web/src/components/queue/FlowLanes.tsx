// The queue flow: Sources chip row, then the Episodes, Seasons &
// libraries and Channels lanes of QueueCards joined by FlowArrows, and a
// collapsed "Media processing" lane for the legacy stages (Q1).
// Hidden stages (e.g. fp_gate) fold into the card before them.
import { useState } from "react";
import { Link } from "react-router-dom";
import { useI18n } from "../../lib/i18n";
import type {
  Extension,
  Flow,
  Lane,
  Pool,
  QueueSummary,
  QueuesSnapshot,
  StageInfo,
} from "../../lib/queueApi";
import { FlowArrow } from "./FlowArrow";
import { QueueCard, type QueueLiveJob } from "./QueueCard";
import { STATE_GLYPH, queueVisualState } from "./format";

const EMPTY_SUMMARY = (stage: string): QueueSummary => ({
  stage,
  counts: {
    pending: 0,
    ready: 0,
    running: 0,
    paused: 0,
    failed_open: 0,
    done_24h: 0,
    skipped_24h: 0,
    awaiting_review: 0,
  },
  wave: { since: null, done: 0, total: 0 },
  rate_per_min: 0,
  eta_sec: null,
  oldest_waiting_sec: null,
  waiting_reason: "idle",
  running_jobs: [],
});

const byOrder = (a: StageInfo, b: StageInfo) =>
  a.lane_order - b.lane_order || a.stage.localeCompare(b.stage);

export type FlowLanesProps = Pick<QueuesSnapshot, "stages" | "queues" | "pools" | "flows"> & {
  /** each queue's jobs running now with their machine, by stage */
  live?: ReadonlyMap<string, QueueLiveJob[]>;
  extensions?: Extension[] | null;
};

type ViewLane = Lane | "extensions";
/** Extension-owned queues form their own lane, whatever lane the graph says. */
const viewLane = (s: StageInfo): ViewLane => (s.extension ? "extensions" : s.lane);

export function FlowLanes({ stages, queues, pools, flows, extensions, live }: FlowLanesProps) {
  const { t, n } = useI18n();
  const summaryOf = (s: string) => queues.find((q) => q.stage === s) ?? EMPTY_SUMMARY(s);
  const poolOf = (p: string): Pool | undefined => pools.find((x) => x.pool === p);
  const laneStages = (lane: ViewLane) => stages.filter((s) => viewLane(s) === lane).sort(byOrder);
  const extLabel = (name?: string | null) =>
    name ? (extensions?.find((e) => e.name === name)?.label ?? name) : undefined;

  const flowBetween = (a: string, b: string, fl: Flow[]) =>
    fl.find((f) => f.from === a && f.to === b)?.per_min ?? null;

  const renderLane = (lane: ViewLane, compact = false) => {
    const all = laneStages(lane);
    const visible = all.filter((s) => !s.hidden);
    const hidden = all.filter((s) => s.hidden);
    const subStepsFor = (s: StageInfo) =>
      hidden
        .filter((h) => {
          const host = [...visible].reverse().find((v) => v.lane_order <= h.lane_order);
          return (host ?? visible[0])?.stage === s.stage;
        })
        .map((h) => ({ info: h, summary: summaryOf(h.stage) }));
    return (
      <div className="mkt-q-lane__cards">
        {visible.map((s, i) => (
          <div className="mkt-q-lane__item" key={s.stage}>
            {i > 0 && (
              <FlowArrow
                perMin={flowBetween(visible[i - 1].stage, s.stage, flows)}
                fromLabel={visible[i - 1].label}
                toLabel={s.label}
              />
            )}
            <QueueCard
              info={s}
              summary={summaryOf(s.stage)}
              pool={poolOf(s.pool)}
              subSteps={subStepsFor(s)}
              live={live?.get(s.stage)}
              compact={compact}
              extensionLabel={extLabel(s.extension)}
            />
          </div>
        ))}
      </div>
    );
  };

  const sources = laneStages("sources");
  const media = laneStages("media");
  // The Media lane is open by default when it has work; a manual toggle wins after that.
  const mediaHasWork = media.some((s) => {
    const c = summaryOf(s.stage).counts;
    return c.pending + c.running + c.failed_open > 0;
  });
  const [mediaOpen, setMediaOpen] = useState<boolean | null>(null);
  const lanes: ViewLane[] = ["episodes", "speech", "seasons", "channels", "extensions"];

  return (
    <div className="mkt-q-flow">
      {sources.length > 0 && (
        <section className="mkt-q-lane" aria-labelledby="mkt-q-lane-sources">
          <h2 className="mkt-q-lane__title" id="mkt-q-lane-sources">
            {t("queue.lane.sources")}
          </h2>
          <ul className="mkt-q-sources">
            {sources.map((s) => {
              const sum = summaryOf(s.stage);
              const st = queueVisualState(s, sum);
              return (
                <li key={s.stage}>
                  <Link
                    className={`mkt-q-source mkt-q-source--${st}`}
                    to={`/queue/${encodeURIComponent(s.stage)}`}
                  >
                    <span aria-hidden="true">{STATE_GLYPH[st]}</span> {s.label}
                    <span className="mkt-q-source__n">
                      {t("queue.card.doneOf", {
                        done: n(sum.wave.done),
                        total: n(Math.max(sum.wave.total, sum.wave.done)),
                      })}
                    </span>
                    <span className="mkt-visually-hidden"> {t(`queue.state.${st}`)}</span>
                  </Link>
                </li>
              );
            })}
          </ul>
        </section>
      )}
      {lanes.map((lane) =>
        laneStages(lane).length === 0 ? null : (
          <section className="mkt-q-lane" key={lane} aria-labelledby={`mkt-q-lane-${lane}`}>
            <h2 className="mkt-q-lane__title" id={`mkt-q-lane-${lane}`}>
              {t(`queue.lane.${lane}`)}
            </h2>
            {renderLane(lane)}
          </section>
        )
      )}
      {media.length > 0 && (
        <details
          className="mkt-q-lane mkt-q-lane--media"
          open={mediaOpen ?? mediaHasWork}
          onToggle={(e) => setMediaOpen((e.currentTarget as HTMLDetailsElement).open)}
        >
          <summary className="mkt-q-lane__title">{t("queue.lane.media")}</summary>
          {renderLane("media", true)}
        </details>
      )}
    </div>
  );
}
