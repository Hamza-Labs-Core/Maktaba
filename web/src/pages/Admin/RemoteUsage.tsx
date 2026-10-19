// Remote viewing's live numbers (Epic 32 stories 32.7, 32.8, 32.10): the
// month's minutes and cost estimate against the budget, what each limit is
// doing ("2 of 22 remote channels in use", the stored minutes against the
// account's storage), the broadcasts on air with a Stop each, and the
// signing key's panic button (every remote playback link stops working).
import { useCallback, useEffect, useState } from "react";
import { Button } from "@ds/components/Button/Button";
import { useI18n } from "../../lib/i18n";
import { tvRemoteApi } from "../../lib/tvRemoteApi";
import type { RemoteControllerStatus } from "../../lib/tvRemoteApi";
import { useAction } from "../../components/queue/useAction";

const RUNNING = new Set(["requested", "connecting", "live", "stopping"]);

export function RemoteUsage({ admin = true }: { admin?: boolean }) {
  const { t } = useI18n();
  const action = useAction();
  const [st, setSt] = useState<RemoteControllerStatus | null>(null);
  const [note, setNote] = useState<string | null>(null);

  const load = useCallback(() => {
    tvRemoteApi
      .status()
      .then((r) => setSt(r.remote ?? null))
      .catch(() => setSt(null));
  }, []);
  useEffect(load, [load]);

  if (!st) return null;
  const e = st.estimate;
  const g = st.storage;
  const pct = e.budget_usd > 0 ? Math.round(e.percent) : null;
  const running = st.pushes.filter((p) => RUNNING.has(p.state));

  const rotate = () => {
    if (!window.confirm(t("cfremote.rotateConfirm"))) return;
    void action
      .run("rotate", () => tvRemoteApi.rotateKey())
      .then((r) => {
        if (!r) return;
        setNote(r.old_deleted ? t("cfremote.rotated") : (r.error ?? t("cfremote.rotated")));
        load();
      });
  };
  const stop = (id: string) =>
    void action.run("stop:" + id, () => tvRemoteApi.stopPush(id)).then(load);

  return (
    <section className="mkt-q-cfusage" aria-label={t("cfremote.usage")}>
      <h4>{t("cfremote.usage")}</h4>
      {st.unavailable && (
        <p role="alert" className="mkt-q-banner">
          {t("cfremote.unavailable", { reason: st.unavailable })}
        </p>
      )}
      <ul>
        <li>
          {t("cfremote.estimate", {
            month: e.month,
            minutes: Math.round(e.delivered_minutes + e.running_minutes),
            usd: e.total_usd.toFixed(2),
          })}{" "}
          {pct !== null ? (
            <span
              className={
                pct >= 100 ? "mkt-q-status--bad" : pct >= 80 ? "mkt-q-status--off" : "mkt-q-muted"
              }
            >
              {t("cfremote.ofBudget", { pct, budget: e.budget_usd.toFixed(2) })}
            </span>
          ) : (
            <span className="mkt-q-muted">{t("cfremote.noBudget")}</span>
          )}
          {e.source === "sessions" && (
            <span className="mkt-q-muted"> · {t("cfremote.fromSessions")}</span>
          )}
        </li>
        {e.exhausted && (
          <li role="alert" className="mkt-q-status--bad">
            {t("cfremote.exhausted")}
          </li>
        )}
        <li>
          {t("cfremote.channels", { n: st.limits.channels_in_use, max: st.limits.max_channels })}
        </li>
        <li>
          {t("cfremote.stored", {
            stored: Math.round(g.stored_minutes + g.other_minutes),
            cap: Math.round(g.capacity_minutes),
            reserved: Math.round(
              g.running_reserved_minutes + g.pending_recording_minutes + g.other_minutes
            ),
          })}
          {!g.fits && <span className="mkt-q-status--bad"> · {t("cfremote.storageFull")}</span>}
        </li>
        <li>{t("cfremote.peak", { minutes: Math.round(e.peak_recording_minutes) })}</li>
        <li className="mkt-q-muted">
          {t("cfremote.upload", { mbps: st.limits.upload_mbps.toFixed(1) })}
        </li>
        <li className="mkt-q-muted">{t("cfremote.noAudioOnly")}</li>
        <li className="mkt-q-muted">{t("cfremote.utcMonth")}</li>
      </ul>
      {running.length > 0 && (
        <table className="mkt-q-cfpushes">
          <caption>{t("cfremote.onAir")}</caption>
          <thead>
            <tr>
              <th scope="col">{t("cfremote.channel")}</th>
              <th scope="col">{t("cfremote.state")}</th>
              <th scope="col">{t("cfremote.viewers")}</th>
              {admin && <th scope="col" />}
            </tr>
          </thead>
          <tbody>
            {running.map((p) => (
              <tr key={p.id}>
                <td>
                  <bdi>{p.channel_id.slice(0, 8)}</bdi>
                  {p.host_id && <span className="mkt-q-muted"> · {p.host_id.slice(0, 8)}</span>}
                </td>
                <td>{t(`cfremote.push.${p.state}`)}</td>
                <td>{p.viewers}</td>
                {admin && (
                  <td>
                    <Button
                      variant="secondary"
                      loading={action.busy === "stop:" + p.id}
                      disabled={p.state === "stopping"}
                      onClick={() => stop(p.id)}
                    >
                      {t("cfremote.stop")}
                    </Button>
                  </td>
                )}
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {admin && (
        <div className="mkt-q-extcard__actions">
          <Button variant="secondary" loading={action.busy === "rotate"} onClick={rotate}>
            {t("cfremote.rotate")}
          </Button>
          <span className="mkt-q-muted">{t("cfremote.rotateHelp")}</span>
        </div>
      )}
      {note && (
        <p role="status" className="mkt-q-muted">
          {note}
        </p>
      )}
      {action.error && (
        <p role="alert" className="mkt-q-banner">
          {action.error}
        </p>
      )}
    </section>
  );
}
