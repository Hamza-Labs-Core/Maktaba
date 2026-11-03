// Every queue of one library as a bar with its counts: the queues with work
// left or failures first (most failures, then most left), the finished ones
// folded behind a button. Each row names its stage and links to the queue.
import { useState } from "react";
import { Link } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { bidiIsolate, useI18n } from "../../lib/i18n";
import type { LibraryQueueRow, StageInfo } from "../../lib/queueApi";
import { StatusBar, StatusLegend, useStatusText } from "./charts";

type Cell = LibraryQueueRow["strip"][number];

export function stageNeedsWork(c: Cell): boolean {
  return c.failed > 0 || c.running > 0 || c.pending > 0 || Math.min(c.done, c.total) < c.total;
}

export function StageChart({ row, stages }: { row: LibraryQueueRow; stages: StageInfo[] }) {
  const { t, n } = useI18n();
  const text = useStatusText();
  const [showAll, setShowAll] = useState(false);
  const label = new Map(stages.map((s) => [s.stage, s.label]));
  const strip = row.strip ?? [];
  const open = strip
    .filter(stageNeedsWork)
    .sort(
      (a, b) =>
        b.failed - a.failed ||
        b.total - Math.min(b.done, b.total) - (a.total - Math.min(a.done, a.total)) ||
        0
    );
  const finished = strip.filter((c) => !stageNeedsWork(c));
  const shown = showAll ? [...open, ...finished] : open;

  return (
    <div className="mkt-ld-stages">
      <StatusLegend />
      {shown.length === 0 ? (
        <p className="mkt-muted">{t("libdash.stages.allDone", { n: n(finished.length) })}</p>
      ) : (
        <ul className="mkt-ld-stages__list">
          {shown.map((c) => {
            const name = label.get(c.stage) ?? c.stage;
            const counts = {
              done: c.done,
              total: c.total,
              running: c.running,
              waiting: c.pending,
              failed: c.failed,
            };
            return (
              <li key={c.stage} className="mkt-ld-stage" data-failing={c.failed > 0 || undefined}>
                <Link className="mkt-ld-stage__name" to={`/queue/${encodeURIComponent(c.stage)}`}>
                  {bidiIsolate(name)}
                </Link>
                <StatusBar c={counts} label={`${name}: ${text(counts)}`} />
                <span className="mkt-ld-stage__nums" aria-hidden="true">
                  {t("libdash.stages.of", {
                    done: n(Math.min(c.done, c.total)),
                    total: n(c.total),
                  })}
                  {c.running > 0 && <span>{t("libdash.n.running", { n: n(c.running) })}</span>}
                  {c.pending > 0 && <span>{t("libdash.n.waiting", { n: n(c.pending) })}</span>}
                  {c.failed > 0 && (
                    <span className="mkt-ld-stage__failed">
                      {t("libdash.n.failed", { n: n(c.failed) })}
                    </span>
                  )}
                </span>
              </li>
            );
          })}
        </ul>
      )}
      {finished.length > 0 && open.length > 0 && (
        <Button
          size="sm"
          variant="ghost"
          aria-expanded={showAll}
          onClick={() => setShowAll((v) => !v)}
        >
          {showAll
            ? t("libdash.stages.hideDone")
            : t("libdash.stages.showDone", { n: n(finished.length) })}
        </Button>
      )}
    </div>
  );
}
