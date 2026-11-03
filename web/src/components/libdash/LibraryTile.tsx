// One library on the dashboard: its name (to the library's own admin page),
// overall progress as a gauge, done / running / waiting / failed, the last
// two weeks' work as a small chart, and badges for what needs an admin:
// failures, YouTube on or off (one click opens its sources), files left to
// convert and duplicates to review.
import { Link } from "react-router-dom";
import { Badge } from "@ds/components/Badge/Badge";
import { Button } from "@ds/components/Button/Button";
import { Checkbox } from "@ds/components/Choice/Checkbox";
import { bidiIsolate, useI18n } from "../../lib/i18n";
import { RingGauge } from "../queue/RingGauge";
import { useFmt, type QueueVisualState } from "../queue/format";
import { ActivityChart, StatusBar, useStatusText } from "./charts";
import type { LibraryHealth } from "./health";

/**
 * The progress ring's colour: always the done colour, so its percentage reads
 * as work done; failures show in the red "N failed" line and the tile's edge,
 * never in the ring. Only a library with nothing to do has the off ring.
 */
export function ringState(h: Pick<LibraryHealth, "total">): QueueVisualState {
  return h.total > 0 ? "done" : "off";
}

export function libraryPath(id: string, section?: string): string {
  return `/admin/libraries/${encodeURIComponent(id)}${section ? `#${section}` : ""}`;
}

export interface LibraryTileProps {
  h: LibraryHealth;
  activity?: { days: string[]; done: number[]; failed: number[] };
  selected?: boolean;
  onSelect?: (on: boolean) => void;
  onSources?: () => void;
}

export function LibraryTile({ h, activity, selected, onSelect, onSources }: LibraryTileProps) {
  const { t, n } = useI18n();
  const fmt = useFmt();
  const text = useStatusText();
  const headingId = `mkt-ld-tile-${h.id}`;
  const counts = {
    done: h.done,
    total: h.total,
    running: h.running,
    waiting: h.waiting,
    failed: h.failed,
  };
  const recent = activity ? activity.done.reduce((a, b) => a + b, 0) : 0;
  return (
    <li className="mkt-ld-tile" data-state={h.state} aria-labelledby={headingId}>
      <div className="mkt-ld-tile__head">
        {onSelect && (
          <Checkbox
            className="mkt-ld-tile__pick"
            checked={!!selected}
            onChange={(e) => onSelect(e.target.checked)}
            label={
              <span className="mkt-visually-hidden">{t("libdash.select", { name: h.name })}</span>
            }
          />
        )}
        <h2 id={headingId} className="mkt-ld-tile__name">
          <Link to={libraryPath(h.id)}>{bidiIsolate(h.name)}</Link>
        </h2>
      </div>
      <p className="mkt-ld-tile__meta">
        {t("libdash.meta", { videos: n(h.row.videos), seasons: n(h.row.seasons) })}
      </p>

      <div className="mkt-ld-tile__body">
        <RingGauge
          done={h.done}
          total={Math.max(h.total, 1)}
          state={ringState(h)}
          size={64}
          centre={`${n(h.pct)}%`}
          label={t("libdash.gauge", { pct: n(h.pct), name: h.name })}
        />
        <div className="mkt-ld-tile__state">
          <p className={`mkt-ld-state mkt-ld-state--${h.state}`}>
            <span className="mkt-ld-state__dot" aria-hidden="true" />
            {h.state === "failing"
              ? t("libdash.state.failing", { n: n(h.failed) })
              : t(`libdash.state.${h.state}`)}
          </p>
          <StatusBar c={counts} label={text(counts)} />
          <p className="mkt-ld-tile__counts">
            {h.failingStages > 0 && (
              <span>{t("libdash.n.failingQueues", { n: n(h.failingStages) })}</span>
            )}
            {h.running > 0 && <span>{t("libdash.n.running", { n: n(h.running) })}</span>}
            {h.waiting > 0 && <span>{t("libdash.n.waiting", { n: n(h.waiting) })}</span>}
            {h.running === 0 && h.waiting === 0 && <span>{t("libdash.n.nothingQueued")}</span>}
          </p>
        </div>
      </div>

      {activity && (
        <div className="mkt-ld-tile__trend">
          <ActivityChart compact name={h.name} {...activity} />
          <span className="mkt-ld-tile__trendlabel">
            {t("libdash.trend", { days: n(activity.days.length), n: n(recent) })}
          </span>
        </div>
      )}

      {(h.youtube !== null && h.youtube !== "none") || h.toConvert > 0 || h.duplicates > 0 ? (
        <ul className="mkt-ld-tile__flags" aria-label={t("libdash.flags", { name: h.name })}>
          {h.youtube !== null && h.youtube !== "none" && (
            <li>
              {onSources ? (
                <Button
                  size="sm"
                  variant="ghost"
                  className={`mkt-ld-yt mkt-ld-yt--${h.youtube}`}
                  onClick={onSources}
                  aria-label={t(`libdash.yt.aria.${h.youtube}`, {
                    name: h.name,
                    on: n(h.sourcesOn),
                    total: n(h.sources),
                  })}
                >
                  <span className="mkt-ld-yt__dot" aria-hidden="true" />
                  {t(`libdash.yt.${h.youtube}`, { on: n(h.sourcesOn), total: n(h.sources) })}
                </Button>
              ) : (
                <Badge
                  tone={
                    h.youtube === "problem" ? "error" : h.youtube === "on" ? "accent" : "neutral"
                  }
                >
                  {t(`libdash.yt.${h.youtube}`, { on: n(h.sourcesOn), total: n(h.sources) })}
                </Badge>
              )}
            </li>
          )}
          {h.toConvert > 0 && (
            <li>
              <Link className="mkt-ld-flag mkt-ld-flag--warn" to={libraryPath(h.id, "convert")}>
                {t("libdash.convert", { n: n(h.toConvert) })}
                {h.converting > 0 && ` (${t("libdash.converting", { n: n(h.converting) })})`}
              </Link>
            </li>
          )}
          {h.duplicates > 0 && (
            <li>
              <Link
                className="mkt-ld-flag"
                to={`/queue/duplicates?library=${encodeURIComponent(h.id)}`}
              >
                {t("libdash.duplicates", { n: n(h.duplicates) })}
              </Link>
            </li>
          )}
        </ul>
      ) : null}

      <p className="mkt-ld-tile__foot">
        {t("libdash.changed", { time: fmt.ago(h.row.last_activity_at) })}
      </p>
    </li>
  );
}
