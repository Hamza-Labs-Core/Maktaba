// "Maktaba is starting" (Epic 36, story 36.5/36.6): shown by the bundled
// web app in server mode until this Mac's server answers, then the
// shell loads the local web app in the window. Polls the shell's status
// (the menu-bar poller's last reading of the supervisor's /processes).
import { useEffect, useRef, useState } from "react";
import { useI18n } from "../../lib/i18n";
import type { ServerStatus, SetupApi } from "../../lib/desktopSetup";

export const STARTING_POLL_MS = 2000;

export function ServerStarting({ api }: { api: SetupApi }) {
  const { t } = useI18n();
  const [status, setStatus] = useState<ServerStatus | null>(null);
  const [error, setError] = useState<string | null>(null);
  const opened = useRef(false);

  useEffect(() => {
    let alive = true;
    const look = () =>
      api
        .serverStatus()
        .then((s) => {
          if (!alive) return;
          setStatus(s);
          if (s.kind === "running" && !opened.current) {
            opened.current = true;
            api.openLocalServer().catch((e) => {
              opened.current = false;
              if (alive) setError(String(e));
            });
          }
        })
        .catch(() => {});
    void look();
    const timer = setInterval(() => void look(), STARTING_POLL_MS);
    return () => {
      alive = false;
      clearInterval(timer);
    };
  }, [api]);

  let line = t("setup.starting.starting");
  if (status?.kind === "problem")
    line = t("setup.starting.problem", { roles: status.roles.join(", ") });
  else if (status?.kind === "not_answering")
    line = t("setup.starting.not_answering", { reason: status.reason });
  else if (status?.kind === "stopped" || status?.kind === "paused")
    line = t("setup.starting.stopped");
  else if (status?.kind === "running") line = t("setup.starting.opening");

  return (
    <main className="mkt-setup" aria-labelledby="mkt-starting-h">
      <section className="mkt-setup__card">
        <h1 id="mkt-starting-h" className="mkt-setup__title">
          {t("setup.starting.title")}
        </h1>
        <p role="status">{line}</p>
        {error && (
          <p className="mkt-setup__warn" role="alert">
            {error}
          </p>
        )}
        <p className="mkt-setup__note">{t("setup.starting.note")}</p>
      </section>
    </main>
  );
}
