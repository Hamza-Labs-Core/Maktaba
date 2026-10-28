// Epic 47: run one of the library actions (Refresh from YouTube, Convert to MP4,
// Find duplicates) for one library, the selected ones, or every library. Each
// only queues work (the API enqueues; workers do it). A conversion or a
// duplicate search starts as a dry run: it writes the plan to the job and
// touches no file, so the first live run per library is one the owner read.
import { useEffect, useState } from "react";
import { Button } from "@ds/components/Button/Button";
import { Modal } from "@ds/components/Modal/Modal";
import { bidiIsolate, useI18n } from "../../lib/i18n";
import { queueApi } from "../../lib/queueApi";
import type { LibraryActionResponse } from "../../lib/queueApi";
import type { OfflineAction } from "./offline";
import { useAction } from "./useAction";

export interface OfflineActionTarget {
  action: OfflineAction;
  /** the libraries it is for; empty with all = every library */
  libraries: { id: string; name: string }[];
  all?: boolean;
}

export interface OfflineActionDialogProps {
  target: OfflineActionTarget | null;
  onClose: () => void;
  onDone?: () => void;
}

interface Tally {
  queued: number;
  merged: number;
  notSetUp: number;
}

function tally(rs: LibraryActionResponse[]): Tally {
  const out: Tally = { queued: 0, merged: 0, notSetUp: 0 };
  for (const r of rs) {
    out.queued += r.enqueued;
    out.merged += r.coalesced;
    out.notSetUp += r.skipped;
  }
  return out;
}

export function OfflineActionDialog({ target, onClose, onDone }: OfflineActionDialogProps) {
  const { t, n } = useI18n();
  const { busy, error, run, clear } = useAction();
  const [dryRun, setDryRun] = useState(true);
  const [fullListing, setFullListing] = useState(false);
  const [retryGone, setRetryGone] = useState(false);
  const [result, setResult] = useState<Tally | null>(null);

  useEffect(() => {
    setDryRun(true);
    setFullListing(false);
    setRetryGone(false);
    setResult(null);
    clear();
  }, [target, clear]);

  if (!target) return null;
  const { action } = target;
  const names = target.all
    ? t("queue.offline.allLibraries")
    : target.libraries.map((l) => bidiIsolate(l.name)).join(", ");

  const call = (id: string, all: boolean) => {
    switch (action) {
      case "convert":
        return queueApi.convertLibrary(id, { dry_run: dryRun, all: all || undefined });
      case "dedup":
        return queueApi.dedupScanLibrary(id, { dry_run: dryRun, all: all || undefined });
      case "youtube_refresh":
        return queueApi.refreshYouTube(id, {
          all: all || undefined,
          full_listing: fullListing || undefined,
          refresh: retryGone || undefined,
        });
    }
  };

  const submit = async () => {
    const res = await run("offline-action", async () => {
      if (target.all) return [await call("all", true)];
      const out: LibraryActionResponse[] = [];
      for (const l of target.libraries) out.push(await call(l.id, false));
      return out;
    });
    if (res) {
      setResult(tally(res));
      onDone?.();
    }
  };

  return (
    <Modal
      open
      onClose={onClose}
      title={t(`queue.offline.action.${action}`)}
      footer={
        result ? (
          <Button onClick={onClose}>{t("queue.retrigger.close")}</Button>
        ) : (
          <>
            <Button variant="ghost" onClick={onClose}>
              {t("queue.retrigger.cancel")}
            </Button>
            <Button loading={busy === "offline-action"} onClick={() => void submit()}>
              {action !== "youtube_refresh" && dryRun
                ? t("queue.offline.runDry")
                : t("queue.offline.run")}
            </Button>
          </>
        )
      }
    >
      {result ? (
        <div role="status" className="mkt-q-offline__done">
          <p>{t("queue.offline.result", { queued: n(result.queued), merged: n(result.merged) })}</p>
          {result.notSetUp > 0 && (
            <p className="mkt-q-banner mkt-q-banner--warn">
              {t("queue.offline.notSetUp", { n: n(result.notSetUp) })}
            </p>
          )}
          {action !== "youtube_refresh" && dryRun && (
            <p className="mkt-q-muted">{t("queue.offline.dryRunWhere")}</p>
          )}
        </div>
      ) : (
        <form className="mkt-q-offline" onSubmit={(e) => e.preventDefault()}>
          <p>{t(`queue.offline.explain.${action}`)}</p>
          <p>
            <strong>{t("queue.offline.for")}</strong> {names}
          </p>
          {action === "youtube_refresh" ? (
            <fieldset className="mkt-q-fieldset">
              <legend>{t("queue.offline.options")}</legend>
              <label className="mkt-q-check">
                <input
                  type="checkbox"
                  checked={fullListing}
                  onChange={(e) => setFullListing(e.target.checked)}
                />
                {t("queue.offline.fullListing")}
              </label>
              <label className="mkt-q-check">
                <input
                  type="checkbox"
                  checked={retryGone}
                  onChange={(e) => setRetryGone(e.target.checked)}
                />
                {t("queue.offline.retryGoneSources")}
              </label>
            </fieldset>
          ) : (
            <label className="mkt-q-check">
              <input
                type="checkbox"
                checked={dryRun}
                onChange={(e) => setDryRun(e.target.checked)}
              />
              {t("queue.offline.dryRun")}
            </label>
          )}
          {error && (
            <p role="alert" className="mkt-q-error">
              {error}
            </p>
          )}
        </form>
      )}
    </Modal>
  );
}
