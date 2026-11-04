// "Import from Tartube" on Manage libraries (slot 0175): every library's YouTube sources
// from Tartube's records at once.
//
//   GET  /api/queues/youtube-tartube   the last import (state, report) and the file to offer
//   POST /api/queues/youtube-tartube   {path}: queue the import (stage youtube_tartube)
//
// Each channel or playlist Tartube downloads into a folder that is a library's root
// becomes a source of that library, off; a source a library has keeps its mode. The
// report says what was added, what was there, what was skipped and why, and the Tartube
// entries no library has yet ("not in Maktaba yet"): no library is made.
import { useCallback, useEffect, useRef, useState } from "react";
import { Modal } from "@ds/components/Modal/Modal";
import { Button } from "@ds/components/Button/Button";
import { Input } from "@ds/components/Input/Input";
import { queueApi } from "../../../lib/queueApi";
import type { TartubeImport, TartubeRow } from "../../../lib/queueApi";
import { bidiIsolate, useI18n } from "../../../lib/i18n";
import { problemMessage } from "../../../lib/libraryAdminApi";

const ENDED = new Set(["done", "failed", "cancelled", "superseded"]);
const REASONS = new Set(["no_url", "not_youtube", "search", "unrecognised", "unresolved"]);
/** how often a running import is read again (ms); a variable for the tests */
export const tartubePoll = { ms: 2000 };

function Rows({ title, rows, total }: { title: string; rows: TartubeRow[]; total: number }) {
  const { t, n } = useI18n();
  if (total === 0) return null;
  return (
    <details className="mkt-libadmin__tartube-list">
      <summary>
        {title} ({n(total)})
      </summary>
      <ul>
        {rows.map((r, i) => (
          <li key={`${r.library_id ?? ""}-${r.name}-${i}`}>
            <bdi>{r.name}</bdi>
            {r.library && r.library !== r.name && (
              <> → {t("libraryAdmin.tartube.library", { name: bidiIsolate(r.library) })}</>
            )}
            {r.reason && (
              <span className="mkt-muted">
                {" "}
                · {REASONS.has(r.reason) ? t(`libraryAdmin.tartube.reason.${r.reason}`) : r.reason}
              </span>
            )}
            {r.url && (
              <bdi
                dir="ltr"
                className="mkt-muted mkt-libadmin__small mkt-libadmin__path mkt-libadmin__local"
              >
                {r.url}
              </bdi>
            )}
          </li>
        ))}
      </ul>
      {rows.length < total && (
        <p className="mkt-muted mkt-libadmin__small">
          {t("libraryAdmin.tartube.more", { n: n(total - rows.length) })}
        </p>
      )}
    </details>
  );
}

export function TartubeImportDialog({
  onClose,
  onDone,
}: {
  onClose: () => void;
  /** an import finished: the cards' sources changed */
  onDone: () => void;
}) {
  const { t, n, formatDate } = useI18n();
  const [last, setLast] = useState<TartubeImport | null>(null);
  const [path, setPath] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const touched = useRef(false);
  const seenJob = useRef<string | null>(null);

  const read = useCallback(async () => {
    try {
      const r = await queueApi.tartubeSources();
      setLast(r);
      if (!touched.current) setPath(r.path ?? r.suggested_path ?? "");
      return r;
    } catch (e) {
      setError(problemMessage(e, t("common.error")));
      return null;
    }
  }, [t]);

  useEffect(() => {
    void read();
  }, [read]);

  const running = last?.state != null && !ENDED.has(last.state);
  useEffect(() => {
    if (!running) return;
    const id = window.setInterval(() => void read(), tartubePoll.ms);
    return () => window.clearInterval(id);
  }, [running, read]);

  // the import this dialog queued has ended: the cards are stale
  useEffect(() => {
    if (last?.job_id && last.job_id === seenJob.current && last.state && ENDED.has(last.state)) {
      seenJob.current = null;
      onDone();
    }
  }, [last, onDone]);

  async function start() {
    setBusy(true);
    setError(null);
    try {
      const r = await queueApi.importTartubeSources({ path: path.trim() });
      seenJob.current = r?.job_id != null ? String(r.job_id) : null;
      await read();
    } catch (e) {
      setError(problemMessage(e, t("common.error")));
    } finally {
      setBusy(false);
    }
  }

  const rep = last?.report ?? null;
  return (
    <Modal
      open
      onClose={onClose}
      title={t("libraryAdmin.tartube.title")}
      footer={
        <>
          <Button type="button" variant="ghost" onClick={onClose}>
            {t("queue.retrigger.close")}
          </Button>
          <Button
            type="button"
            loading={busy}
            disabled={!path.trim() || running}
            onClick={() => void start()}
          >
            {t("libraryAdmin.tartube.run")}
          </Button>
        </>
      }
    >
      <p className="mkt-muted">{t("libraryAdmin.tartube.help")}</p>
      <Input
        label={t("libraryAdmin.tartube.path")}
        value={path}
        dir="ltr"
        autoComplete="off"
        spellCheck={false}
        placeholder="/Volumes/…/Youtube/tartube.db"
        onChange={(e) => {
          touched.current = true;
          setPath(e.target.value);
        }}
      />
      {error && (
        <p role="alert" className="mkt-libadmin__problem">
          {error}
        </p>
      )}
      {last?.state && (
        <p role="status" className="mkt-libadmin__small">
          {running
            ? t("libraryAdmin.tartube.running")
            : last.state === "failed"
              ? t("libraryAdmin.tartube.failed", { error: last.error ?? "" })
              : t("libraryAdmin.tartube.last", {
                  when: formatDate(last.finished_at ?? last.created_at ?? ""),
                })}
        </p>
      )}
      {rep && !running && (
        <div className="mkt-libadmin__tartube">
          <p>
            {t("libraryAdmin.tartube.counts", {
              created: n(rep.counts.created),
              existing: n(rep.counts.existing),
              skipped: n(rep.counts.skipped),
              missing: n(rep.counts.not_in_maktaba),
              seeded: n(rep.counts.seeded),
            })}
          </p>
          <Rows
            title={t("libraryAdmin.tartube.created")}
            rows={rep.created}
            total={rep.counts.created}
          />
          <Rows
            title={t("libraryAdmin.tartube.existing")}
            rows={rep.existing}
            total={rep.counts.existing}
          />
          <Rows
            title={t("libraryAdmin.tartube.skipped")}
            rows={rep.skipped}
            total={rep.counts.skipped}
          />
          <Rows
            title={t("libraryAdmin.tartube.missing")}
            rows={rep.not_in_maktaba}
            total={rep.counts.not_in_maktaba}
          />
          {rep.counts.not_in_maktaba > 0 && (
            <p className="mkt-muted mkt-libadmin__small">{t("libraryAdmin.tartube.missingHelp")}</p>
          )}
        </div>
      )}
    </Modal>
  );
}
