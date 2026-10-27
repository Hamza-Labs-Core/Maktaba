// "Maktaba is starting" (Epic 36 story 36.5/36.6, Epic 38 story 38.6): shown
// by the bundled web app in server mode until this machine's server answers,
// then the shell loads the local web app in the window. Polls the shell's
// status (the menu-bar / tray poller's last reading of the supervisor's
// /processes). Once the server's processes run, the shell opens the web app
// only when its page and api answer (open_local_server); until then this
// screen stays, and after PAGE_PATIENCE_MS it says so, with a retry: the
// window never shows a page that cannot load.
import { useCallback, useEffect, useRef, useState } from "react";
import { Button } from "@ds/components/Button/Button";
import { ProgressBar } from "@ds/components/ProgressBar/ProgressBar";
import { useI18n } from "../../lib/i18n";
import {
  formatBytes,
  NO_ANSWER,
  type Platform,
  type ServerStatus,
  type SetupApi,
} from "../../lib/desktopSetup";
import { platformWords, usePlatform } from "./platform";
import { startingDetail } from "./startingDetail";

export const STARTING_POLL_MS = 2000;
/** How long the server's page may not answer, once its processes run, before the screen says so. */
export const PAGE_PATIENCE_MS = 30_000;
/** What the shell says while the server runs but its page does not answer yet (shell.rs). */
export const PAGE_NOT_ANSWERING = "page_not_answering";

/** The backup before an update: what it has written and what it will likely come to
 * (the newest finished dump's size; 0 when the server knows none), in MB. */
export function dumpProgress(
  status: ServerStatus | null
): { mb: number; expected: number; percent: number | null } | null {
  if (status?.kind !== "starting" || status.detail_code !== "db_dumping") return null;
  const num = (v: unknown) => (typeof v === "number" && Number.isFinite(v) && v > 0 ? v : 0);
  const mb = num(status.detail_params?.mb);
  const expected = num(status.detail_params?.expected_mb);
  // a dump may outgrow the last one: the bar stops short of full until it is done
  const percent = expected > 0 ? Math.min(99, (mb / expected) * 100) : null;
  return { mb, expected, percent };
}

export function ServerStarting({ api, platform }: { api: SetupApi; platform?: Platform }) {
  const { t } = useI18n();
  const asked = usePlatform(api);
  const words = platformWords(t, platform ?? asked);
  const [status, setStatus] = useState<ServerStatus | null>(null);
  const [openFailed, setOpenFailed] = useState(false);
  // the server runs but its page has not answered for PAGE_PATIENCE_MS
  const [pageSilent, setPageSilent] = useState(false);
  const opened = useRef(false);
  const alive = useRef(true);
  // since when the server's page has not answered (null: it has not been asked yet)
  const silentSince = useRef<number | null>(null);

  // Ask the shell to open the local web app; while its page does not answer the
  // next poll asks again (or the retry, at once).
  const open = useCallback(() => {
    if (opened.current) return;
    opened.current = true;
    api
      .openLocalServer()
      .then(() => {
        if (!alive.current) return;
        setPageSilent(false);
        setOpenFailed(false);
      })
      .catch((e) => {
        opened.current = false;
        if (!alive.current) return;
        const said = String(e);
        if (said.includes(PAGE_NOT_ANSWERING)) {
          if (silentSince.current === null) silentSince.current = Date.now();
          setPageSilent(Date.now() - silentSince.current >= PAGE_PATIENCE_MS);
          return;
        }
        // the shell's own words go to the console; the screen says it in this language
        console.error("[setup] opening the local server failed", said.slice(0, 300));
        setOpenFailed(true);
      });
  }, [api]);

  useEffect(() => {
    alive.current = true;
    const look = () =>
      api
        .serverStatus()
        .then((s) => {
          if (!alive.current) return;
          setStatus(s);
          if (s.kind === "running") return open();
          // not running (any more): the page's wait starts again when it runs
          silentSince.current = null;
          setPageSilent(false);
        })
        .catch(() => {});
    void look();
    const timer = setInterval(() => void look(), STARTING_POLL_MS);
    return () => {
      alive.current = false;
      clearInterval(timer);
    };
  }, [api, open]);

  let line = t("setup.starting.starting");
  // a line of server.log, in the server's own words: shown as it is, apart from the
  // sentence of this language
  let logLine: string | null = null;
  if (status?.kind === "problem")
    line = t("setup.starting.problem", { roles: status.roles.join(", "), ...words });
  else if (status?.kind === "not_answering") {
    line = t("setup.starting.not_answering", words);
    if (status.reason && status.reason !== NO_ANSWER) logLine = status.reason;
  } else if (status?.kind === "stopped" || status?.kind === "paused")
    line = t("setup.starting.stopped", words);
  else if (status?.kind === "running" && pageSilent)
    line = t("setup.starting.page_not_answering", words);
  else if (status?.kind === "running") line = t("setup.starting.opening");
  // what the server waits on, in this language when the app has words for its
  // code (else the line above says it generically); the programs' own words
  // and the server's English go under Details
  const waits = status?.kind === "starting" || status?.kind === "problem";
  const waitsOn = waits
    ? startingDetail(t, status.detail_code, status.detail_params, words)
    : { text: "", apart: [] };
  const english = waits ? status.detail?.trim() : undefined;
  const dump = dumpProgress(status);
  const MB = 2 ** 20;
  const dumpText = !dump
    ? ""
    : dump.expected > 0
      ? t("setup.starting.progress.dump", {
          done: formatBytes(dump.mb * MB),
          total: formatBytes(dump.expected * MB),
        })
      : t("setup.starting.progress.dump_so_far", { done: formatBytes(dump.mb * MB) });
  // the server is on its way: a bar, measured while the backup before an update is
  // taken, moving otherwise
  const working =
    status === null || status.kind === "starting" || (status.kind === "running" && !pageSilent);

  return (
    <main className="mkt-setup mkt-setup--starting">
      <section
        className="mkt-setup__card"
        role="dialog"
        aria-modal="true"
        aria-labelledby="mkt-starting-h"
      >
        <h1 id="mkt-starting-h" className="mkt-setup__title">
          {t("setup.starting.title")}
        </h1>
        <p role="status">{line}</p>
        {waitsOn.text && <p className="mkt-setup__note">{waitsOn.text}</p>}
        {working && (
          <div className="mkt-setup__progress">
            <ProgressBar
              label={t("setup.starting.title")}
              value={dump?.percent ?? 0}
              indeterminate={dump?.percent == null}
            />
            {dumpText && (
              <p className="mkt-setup__note" aria-live="polite">
                {dumpText}
              </p>
            )}
          </div>
        )}
        {(english || waitsOn.apart.length > 0) && (
          <details className="mkt-setup__details">
            <summary>{t("setup.details")}</summary>
            {waitsOn.apart.map(([label, value]) => (
              <p key={label}>
                {label} <code dir="ltr">{value}</code>
              </p>
            ))}
            {english && <pre dir="ltr">{english}</pre>}
          </details>
        )}
        {logLine && (
          <p className="mkt-setup__note">
            {t("setup.starting.log_says")} <code dir="ltr">{logLine}</code>
          </p>
        )}
        {status?.kind === "running" && pageSilent && (
          <p>
            <Button type="button" onClick={open}>
              {t("common.retry")}
            </Button>
          </p>
        )}
        {openFailed && (
          <p className="mkt-setup__warn" role="alert">
            {t("setup.starting.openFailed", words)}
          </p>
        )}
        <p className="mkt-setup__note">{t("setup.starting.note", words)}</p>
      </section>
    </main>
  );
}
