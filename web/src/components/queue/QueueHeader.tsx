// Overview header: overall state pill, live clock, Pause/Resume all and
// the primary "Queue work…" button (opens RetriggerDialog).
import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { useI18n } from "../../lib/i18n";
import { useFmt } from "./format";
import { TitleRow } from "../../help/HelpLink";

export type OverallState = "paused" | "attention" | "working" | "idle";

export function overallState(p: {
  pausedAll: boolean;
  issues: number;
  running: number;
}): OverallState {
  if (p.pausedAll) return "paused";
  if (p.issues > 0) return "attention";
  if (p.running > 0) return "working";
  return "idle";
}

const GLYPH: Record<OverallState, string> = {
  paused: "⏸",
  attention: "!",
  working: "▶",
  idle: "✓",
};

function LiveClock() {
  const fmt = useFmt();
  const [now, setNow] = useState(() => new Date());
  useEffect(() => {
    const id = setInterval(() => setNow(new Date()), 1000);
    return () => clearInterval(id);
  }, []);
  return (
    <time className="mkt-q-clock" dateTime={now.toISOString()}>
      {fmt.clock(now)}
    </time>
  );
}

export interface QueueHeaderProps {
  state: OverallState;
  pausedAll: boolean;
  busy?: boolean;
  onTogglePause: () => void;
  onQueueWork: () => void;
  /** Retry every "Needs attention" group that has a Retry button (left out: no button). */
  retryAll?: { count: number; busy: boolean; disabled?: boolean; onClick: () => void };
}

export function QueueHeader({
  state,
  pausedAll,
  busy,
  onTogglePause,
  onQueueWork,
  retryAll,
}: QueueHeaderProps) {
  const { t, n } = useI18n();
  return (
    <header className="mkt-q-header">
      <div className="mkt-q-header__title">
        <TitleRow>
          <h1>{t("queue.title")}</h1>
        </TitleRow>
        <span className={`mkt-q-pill mkt-q-pill--${state}`} role="status">
          <span aria-hidden="true">{GLYPH[state]}</span> {t(`queue.overall.${state}`)}
        </span>
        <LiveClock />
      </div>
      <div className="mkt-q-header__actions">
        <Link className="mkt-q-linkbtn" to="/queue/reports">
          {t("queue.header.reports")}
        </Link>
        <Link className="mkt-q-linkbtn" to="/queue/duplicates">
          {t("queue.header.duplicates")}
        </Link>
        <Link className="mkt-q-linkbtn" to="/queue/file-actions">
          {t("queue.header.fileActions")}
        </Link>
        <Link className="mkt-q-linkbtn" to="/admin/extensions">
          {t("queue.header.extensions")}
        </Link>
        {retryAll && (
          <Button
            variant="secondary"
            loading={retryAll.busy}
            disabled={retryAll.disabled}
            onClick={retryAll.onClick}
          >
            {t("queue.issues.retryAll", { n: n(retryAll.count) })}
          </Button>
        )}
        <Button variant="secondary" onClick={onTogglePause} disabled={busy}>
          {pausedAll ? t("queue.header.resumeAll") : t("queue.header.pauseAll")}
        </Button>
        <Button variant="primary" onClick={onQueueWork}>
          {t("queue.header.queueWork")}
        </Button>
      </div>
    </header>
  );
}
