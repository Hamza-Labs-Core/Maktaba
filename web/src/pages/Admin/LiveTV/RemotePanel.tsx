// Admin → Live TV → Remote (Epic 32 story 32.11 AC4/AC6): remote viewing at
// a glance — the Cloudflare integration, the live inputs, the pushes (with
// the viewers Maktaba counts and the ones Cloudflare counts), the remote
// sessions (user, device, channel), the month's minutes and cost and the
// last 20 remote events. Re-read every 5 s while the page is visible, so a
// failed push shows within 10 s.
//
//   GET /api/tv/remote   { integration, remote: {inputs, pushes, session_list, estimate, events, …} }
import { useCallback, useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { Card } from "@ds/components/Card/Card";
import { ApiError } from "../../../lib/api";
import { useI18n } from "../../../lib/i18n";
import {
  tvRemoteApi,
  type RemoteControllerStatus,
  type RemoteIntegration,
  type RemotePush,
} from "../../../lib/tvRemoteApi";
import { docLink } from "./docs";

export const REMOTE_POLL_MS = 5000;
/** Rows per table page (EC2). */
export const REMOTE_PAGE = 50;

const RUNNING = new Set(["requested", "connecting", "live", "stopping"]);

const since = (s?: string | null) => (s ? new Date(s).toLocaleString() : "—");

function channelLabel(p: { channel_number?: number; channel_name?: string; channel_id: string }) {
  if (p.channel_number) return `${p.channel_number} ${p.channel_name ?? ""}`.trim();
  return p.channel_id.slice(0, 8);
}

export function RemotePanel() {
  const { t } = useI18n();
  const [integration, setIntegration] = useState<RemoteIntegration | null>(null);
  const [st, setSt] = useState<RemoteControllerStatus | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [hidden, setHidden] = useState(false);
  const [pushPage, setPushPage] = useState(1);
  const [sessPage, setSessPage] = useState(1);
  const alive = useRef(true);

  const load = useCallback(async () => {
    try {
      const r = await tvRemoteApi.status();
      if (!alive.current) return;
      setIntegration(r.integration);
      setSt(r.remote ?? null);
      setError(null);
    } catch (e) {
      if (!alive.current) return;
      if (e instanceof ApiError && (e.status === 403 || e.status === 404)) {
        setHidden(true);
        return;
      }
      setError(e instanceof Error ? e.message : String(e));
    }
  }, []);

  useEffect(() => {
    alive.current = true;
    void load();
    let timer: number | null = null;
    const start = () => {
      if (timer === null) timer = window.setInterval(() => void load(), REMOTE_POLL_MS);
    };
    const stop = () => {
      if (timer !== null) window.clearInterval(timer);
      timer = null;
    };
    const onVis = () => {
      if (document.visibilityState === "visible") {
        void load();
        start();
      } else stop();
    };
    start();
    document.addEventListener("visibilitychange", onVis);
    return () => {
      alive.current = false;
      stop();
      document.removeEventListener("visibilitychange", onVis);
    };
  }, [load]);

  if (hidden) return null;

  const help = (
    <a className="mkt-livetv__help" href={docLink("cloudflare")} target="_blank" rel="noreferrer">
      {t("tv.remote.help")}
    </a>
  );

  if (!integration) {
    return (
      <Card className="mkt-livetv__card" data-testid="remote-panel">
        <h2>{t("tv.remote.title")}</h2>
        {error ? (
          <p className="mkt-livetv__hint">{t("tv.remote.loadError", { error })}</p>
        ) : (
          <p className="mkt-loading">{t("common.loading")}</p>
        )}
      </Card>
    );
  }

  const status = !integration.configured
    ? "none"
    : !integration.enabled
      ? "off"
      : st && !st.ready
        ? "problem"
        : "ready";
  const pushes = st?.pushes ?? [];
  const sessions = st?.session_list ?? [];
  const events = st?.events ?? [];
  const e = st?.estimate;

  const viewersCell = (p: RemotePush) => (
    <>
      {p.viewers}
      {" / "}
      <span title={t("tv.remote.cfViewersHint")}>
        {p.cf_viewers === null || p.cf_viewers === undefined ? "—" : p.cf_viewers}
      </span>
    </>
  );

  return (
    <Card className="mkt-livetv__card" data-testid="remote-panel">
      <h2>
        {t("tv.remote.title")} {help}
      </h2>
      {error && <p className="mkt-livetv__hint">{t("tv.cluster.staleView", { error })}</p>}
      <p data-testid="remote-status" className={status === "ready" ? "mkt-q-status--ok" : ""}>
        {t(`tv.remote.status.${status}`)}
        {status === "problem" && st?.reason ? ` — ${st.reason}` : ""}
        {status === "none" && (
          <>
            {" "}
            <Link to="/admin/extensions#cloudflare_stream">{t("tv.remote.setUp")}</Link>
          </>
        )}
      </p>
      {st?.unavailable && (
        <p role="alert" className="mkt-q-status--bad">
          {t("cfremote.unavailable", { reason: st.unavailable })}
        </p>
      )}

      {e && (
        <p data-testid="remote-month">
          {t("tv.remote.month", {
            month: e.month,
            minutes: Math.round(e.delivered_minutes + e.running_minutes),
            usd: e.total_usd.toFixed(2),
            budget: e.budget_usd.toFixed(2),
          })}
          {e.exhausted && <span className="mkt-q-status--bad"> · {t("cfremote.exhausted")}</span>}{" "}
          <a href={docLink("costs")} target="_blank" rel="noreferrer">
            {t("tv.remote.costsHelp")}
          </a>
        </p>
      )}

      {st?.budget_notice && st.budget_notice.level >= 80 && (
        <p role="alert" className="mkt-q-banner" data-testid="remote-budget-notice">
          {t(st.budget_notice.level >= 100 ? "tv.remote.budgetNotice100" : "tv.remote.budgetNotice80", {
            month: st.budget_notice.month,
            usd: (st.budget_notice.total_usd ?? e?.total_usd ?? 0).toFixed(2),
            budget: (st.budget_notice.budget_usd ?? e?.budget_usd ?? 0).toFixed(2),
          })}{" "}
          {st.budget_notice.level >= 100 &&
            t(
              st.budget_notice.action === "block_new"
                ? "tv.remote.budgetBlocks"
                : "tv.remote.budgetWarnOnly",
            )}
        </p>
      )}

      {st && st.inputs.length > 0 && (
        <table className="mkt-q-cfpushes" data-testid="remote-inputs">
          <caption>{t("tv.remote.inputs")}</caption>
          <thead>
            <tr>
              <th scope="col">{t("tv.remote.input")}</th>
              <th scope="col">{t("cfremote.state")}</th>
              <th scope="col">{t("cfremote.channel")}</th>
              <th scope="col">{t("tv.remote.since")}</th>
              <th scope="col">{t("tv.remote.cfStatus")}</th>
            </tr>
          </thead>
          <tbody>
            {st.inputs.map((i) => (
              <tr key={i.id}>
                <td>
                  <bdi>{i.name}</bdi>
                </td>
                <td>{t(`tv.remote.inputState.${i.state}`)}</td>
                <td>{i.channel_id ? <bdi>{i.channel_id.slice(0, 8)}</bdi> : "—"}</td>
                <td>{since(i.assigned_at)}</td>
                <td className={i.error ? "mkt-q-status--bad" : ""}>
                  {i.error || t("tv.remote.ok")}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      <table className="mkt-q-cfpushes" data-testid="remote-pushes">
        <caption>{t("tv.remote.pushes")}</caption>
        <thead>
          <tr>
            <th scope="col">{t("cfremote.channel")}</th>
            <th scope="col">{t("tv.remote.host")}</th>
            <th scope="col">{t("cfremote.state")}</th>
            <th scope="col">{t("tv.remote.since")}</th>
            <th scope="col">{t("tv.remote.viewersBoth")}</th>
          </tr>
        </thead>
        <tbody>
          {pushes.length === 0 && (
            <tr>
              <td colSpan={5} className="mkt-q-muted">
                {t("tv.remote.noPushes")}
              </td>
            </tr>
          )}
          {pushes.slice(0, pushPage * REMOTE_PAGE).map((p) => (
            <tr key={p.id} className={p.state === "failed" ? "mkt-q-status--bad" : ""}>
              <td>
                <bdi>{channelLabel(p)}</bdi>
              </td>
              <td>{p.host_id ? <bdi>{p.host_id.slice(0, 8)}</bdi> : "—"}</td>
              <td>
                {t(`cfremote.push.${p.state}`)}
                {p.state === "failed" && p.detail ? ` — ${p.detail}` : ""}
              </td>
              <td>{since(p.live_at ?? p.started_at)}</td>
              <td>{RUNNING.has(p.state) ? viewersCell(p) : "—"}</td>
            </tr>
          ))}
        </tbody>
      </table>
      {pushes.length > pushPage * REMOTE_PAGE && (
        <Button variant="ghost" onClick={() => setPushPage((n) => n + 1)}>
          {t("tv.remote.more")}
        </Button>
      )}

      <table className="mkt-q-cfpushes" data-testid="remote-sessions">
        <caption>{t("tv.remote.sessions", { n: st?.sessions ?? 0 })}</caption>
        <thead>
          <tr>
            <th scope="col">{t("tv.remote.user")}</th>
            <th scope="col">{t("tv.remote.device")}</th>
            <th scope="col">{t("cfremote.channel")}</th>
            <th scope="col">{t("tv.remote.since")}</th>
          </tr>
        </thead>
        <tbody>
          {sessions.length === 0 && (
            <tr>
              <td colSpan={4} className="mkt-q-muted">
                {t("tv.remote.noSessions")}
              </td>
            </tr>
          )}
          {sessions.slice(0, sessPage * REMOTE_PAGE).map((s) => (
            <tr key={s.id}>
              <td>
                <bdi>{s.user}</bdi>
              </td>
              <td>
                <bdi>{s.device || "—"}</bdi>
              </td>
              <td>
                <bdi>{channelLabel(s)}</bdi>
              </td>
              <td>{since(s.started_at)}</td>
            </tr>
          ))}
        </tbody>
      </table>
      {sessions.length > sessPage * REMOTE_PAGE && (
        <Button variant="ghost" onClick={() => setSessPage((n) => n + 1)}>
          {t("tv.remote.more")}
        </Button>
      )}

      <details className="mkt-livetv__events" data-testid="remote-events">
        <summary>{t("tv.remote.events", { n: events.length })}</summary>
        {events.length === 0 ? (
          <p className="mkt-q-muted">{t("tv.remote.noEvents")}</p>
        ) : (
          <ol>
            {[...events].reverse().map((ev, i) => (
              <li key={i} className={ev.level === "ERROR" ? "mkt-q-status--bad" : ""}>
                <time>{since(ev.at)}</time> <code>{ev.event}</code> {ev.message}
                {ev.code && <span className="mkt-q-muted"> · {ev.code}</span>}
              </li>
            ))}
          </ol>
        )}
      </details>
      <p className="mkt-livetv__hint">
        <a href={docLink("failures")} target="_blank" rel="noreferrer">
          {t("tv.remote.playbook")}
        </a>
      </p>
    </Card>
  );
}
