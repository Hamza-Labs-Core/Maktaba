// "Preview channels": plan the channels with the settings as saved now and
// look at the result without writing a plan or a channel (the stand-alone
// control centre's Preview channels). The run happens in the pipeline
// (gRPC PlanPreview); this panel starts it, polls its state while it runs and
// links to the page it wrote (Reports). Used on the Reports page and next to
// Settings → Rules → Channels, where a changed setting is previewed.
import { useCallback, useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { ApiError } from "../../lib/api";
import { bidiIsolate, useI18n } from "../../lib/i18n";
import { PREVIEW_PAGE, reportsApi } from "../../lib/reportsApi";
import type { PlanPreviewStatus, PreviewKind } from "../../lib/reportsApi";

import "../../styles/queue.css";

export interface PlanPreviewPanelProps {
  /** Only an admin starts a preview. */
  canRun: boolean;
  /** Settings → Rules: just the preview, with a note about unsaved edits. */
  compact?: boolean;
  /** Rule edits not saved yet: the preview does not include them. */
  unsaved?: number;
  /** Called when a run finishes (the Reports page reloads its list). */
  onFinished?: (status: PlanPreviewStatus) => void;
  pollMs?: number;
}

const NAMES_SHOWN = 6;

function names(list: string[] | undefined): string {
  if (!list?.length) return "";
  const shown = list.slice(0, NAMES_SHOWN).map(bidiIsolate).join(", ");
  return list.length > NAMES_SHOWN ? `${shown} …` : shown;
}

export function PlanPreviewPanel({
  canRun,
  compact = false,
  unsaved = 0,
  onFinished,
  pollMs = 2000,
}: PlanPreviewPanelProps) {
  const { t, n } = useI18n();
  const [status, setStatus] = useState<PlanPreviewStatus | null>(null);
  const [offline, setOffline] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [starting, setStarting] = useState<PreviewKind | null>(null);
  const wasRunning = useRef(false);
  const finished = useRef(onFinished);
  finished.current = onFinished;

  const load = useCallback(async () => {
    try {
      const s = await reportsApi.previewStatus();
      setStatus(s);
      setOffline(false);
      if (wasRunning.current && !s.running) finished.current?.(s);
      wasRunning.current = s.running;
    } catch {
      setOffline(true);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const running = status?.running ?? false;
  useEffect(() => {
    if (!running) return;
    const id = setInterval(() => void load(), pollMs);
    return () => clearInterval(id);
  }, [running, load, pollMs]);

  async function start(kind: PreviewKind) {
    setError(null);
    setStarting(kind);
    try {
      await reportsApi.startPreview(kind);
      wasRunning.current = true;
      await load();
    } catch (e) {
      setError(
        e instanceof ApiError && e.status === 409
          ? t("queue.preview.busy")
          : e instanceof Error
            ? e.message
            : String(e)
      );
    } finally {
      setStarting(null);
    }
  }

  const summary = status?.summary;
  const changes = summary?.changes;
  const ready = status?.ready.preview ?? false;
  const failed = !running && status?.error;
  const lastPhase = status?.log[status.log.length - 1];

  return (
    <section className="mkt-q-preview" aria-labelledby="mkt-q-preview-h">
      <h2 className="mkt-q-section-title" id="mkt-q-preview-h">
        {t("queue.preview.title")}
      </h2>
      <p className="mkt-q-muted">{t("queue.preview.intro")}</p>
      {unsaved > 0 && (
        <p className="mkt-q-note" role="status">
          {t("queue.preview.unsaved", { n: n(unsaved) })}
        </p>
      )}
      {canRun && (
        <div className="mkt-q-preview__actions">
          <Button
            variant="primary"
            loading={starting === "preview"}
            disabled={running}
            onClick={() => void start("preview")}
          >
            {t("queue.preview.run")}
          </Button>
          {!compact && (
            <Button
              variant="secondary"
              loading={starting === "stored"}
              disabled={running}
              onClick={() => void start("stored")}
            >
              {t("queue.preview.stored")}
            </Button>
          )}
        </div>
      )}
      {offline && (
        <p role="alert" className="mkt-q-error">
          {t("queue.preview.offline")}
        </p>
      )}
      {error && (
        <p role="alert" className="mkt-q-error">
          {error}
        </p>
      )}
      {running && (
        <p role="status" className="mkt-q-preview__phase">
          <span aria-hidden="true">▶</span>{" "}
          {t("queue.preview.running", { phase: status?.phase || lastPhase || "…" })}
        </p>
      )}
      {failed && (
        <p role="alert" className="mkt-q-error">
          {t("queue.preview.failed", { message: status?.error ?? "" })}
        </p>
      )}
      {!running && !failed && summary?.channels !== undefined && (
        <div className="mkt-q-preview__result" role="status">
          <p>
            {t("queue.preview.result", {
              channels: n(summary.channels),
              lanes: n(summary.lanes ?? 0),
            })}
          </p>
          {changes && (
            <ul className="mkt-q-preview__changes">
              <li>
                {t("queue.preview.added", { n: n(changes.added) })}
                {names(summary.added) && (
                  <span className="mkt-q-muted"> · {names(summary.added)}</span>
                )}
              </li>
              <li>
                {t("queue.preview.removed", { n: n(changes.removed) })}
                {names(summary.removed) && (
                  <span className="mkt-q-muted"> · {names(summary.removed)}</span>
                )}
              </li>
              <li>
                {t("queue.preview.changed", { n: n(changes.changed) })}
                {names(summary.changed) && (
                  <span className="mkt-q-muted"> · {names(summary.changed)}</span>
                )}
              </li>
            </ul>
          )}
        </div>
      )}
      {ready && !running && (
        <p>
          <Link to={`/queue/reports?open=${encodeURIComponent(PREVIEW_PAGE)}`}>
            {t("queue.preview.open")}
          </Link>
        </p>
      )}
      {!compact && status && status.log.length > 0 && (
        <details className="mkt-q-preview__log">
          <summary>{t("queue.preview.log")}</summary>
          <pre dir="auto">{status.log.join("\n")}</pre>
        </details>
      )}
    </section>
  );
}
