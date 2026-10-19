// Admin → Live TV → On air (story 32.5): every channel that is encoding now,
// on which host, with which encoder and how many people watch it (HLS,
// HDHomeRun tuners, remote), with Move and Stop. Both interrupt viewers, so
// both ask first. A runtime row with no live placement is marked "stale".
import { useState } from "react";
import { Badge } from "@ds/components/Badge/Badge";
import { Button } from "@ds/components/Button/Button";
import { Card } from "@ds/components/Card/Card";
import { useI18n } from "../../../lib/i18n";
import {
  clusterErrText,
  tvClusterApi,
  type OnAirChannel,
  type OnAirState,
  type TvClusterView,
} from "../../../lib/tvClusterApi";
import { ConfirmDialog } from "./ConfirmDialog";
import { MoveDialog } from "./MoveDialog";

const STATE_TONE: Record<OnAirState, "success" | "warn" | "neutral" | "accent"> = {
  live: "success",
  placing: "accent",
  moving: "warn",
  runtime: "neutral",
};

export function OnAir({
  view,
  onChanged,
}: {
  view: TvClusterView;
  onChanged: () => void | Promise<void>;
}) {
  const { t, n, formatDate } = useI18n();
  const [moving, setMoving] = useState<OnAirChannel | null>(null);
  const [stopping, setStopping] = useState<OnAirChannel | null>(null);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);
  const rows = [...view.on_air].sort((a, b) => a.number - b.number);

  async function stop(ch: OnAirChannel) {
    setBusy(true);
    setMsg(null);
    try {
      await tvClusterApi.stop(ch.channel_id);
      setStopping(null);
      setMsg({ ok: true, text: t("tv.cluster.onAir.stopped", { name: ch.name }) });
      await onChanged();
    } catch (e) {
      setMsg({ ok: false, text: clusterErrText(e) });
      setStopping(null);
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card className="mkt-livetv__card mkt-tvc">
      <h2>{t("tv.cluster.onAir.title")}</h2>
      {rows.length === 0 ? (
        <p className="mkt-livetv__hint">{t("tv.cluster.onAir.none")}</p>
      ) : (
        <div className="mkt-tvc-table-wrap">
          <table className="mkt-table mkt-tvc-table" aria-label={t("tv.cluster.onAir.title")}>
            <thead>
              <tr>
                <th scope="col">{t("tv.cluster.onAir.number")}</th>
                <th scope="col">{t("tv.cluster.onAir.name")}</th>
                <th scope="col">{t("tv.cluster.onAir.host")}</th>
                <th scope="col">{t("tv.cluster.onAir.state")}</th>
                <th scope="col">{t("tv.cluster.onAir.encoder")}</th>
                <th scope="col">{t("tv.cluster.onAir.hls")}</th>
                <th scope="col">{t("tv.cluster.onAir.tuners")}</th>
                <th scope="col">{t("tv.cluster.onAir.remote")}</th>
                <th scope="col">{t("tv.cluster.onAir.since")}</th>
                <th scope="col">{t("tv.cluster.onAir.restarts")}</th>
                <th scope="col">{t("tv.cluster.onAir.nvencFallbacks")}</th>
                <th scope="col">{t("tv.cluster.onAir.push")}</th>
                <th scope="col">{t("tv.cluster.onAir.actions")}</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((c) => (
                <tr key={c.channel_id}>
                  <td>{n(c.number, { useGrouping: false })}</td>
                  <td>{c.name}</td>
                  <td>{c.host_name || "—"}</td>
                  <td>
                    <Badge tone={STATE_TONE[c.state] ?? "neutral"}>
                      {t(`tv.cluster.onAir.stateName.${c.state}`)}
                    </Badge>
                    {c.stale && (
                      <>
                        {" "}
                        <Badge tone="error">{t("tv.cluster.onAir.stale")}</Badge>
                      </>
                    )}
                  </td>
                  <td>{c.encoder || "—"}</td>
                  <td>{n(c.viewers.hls)}</td>
                  <td>{n(c.viewers.tuners)}</td>
                  <td>{n(c.viewers.remote)}</td>
                  <td>{c.since ? formatDate(c.since) : "—"}</td>
                  <td>{n(c.restarts)}</td>
                  <td>{n(c.nvenc_fallbacks)}</td>
                  <td>{c.push ? c.push.state : "—"}</td>
                  <td>
                    <div className="mkt-tvc-row-actions">
                      <Button
                        size="sm"
                        variant="secondary"
                        disabled={busy}
                        aria-label={t("tv.cluster.onAir.moveNamed", { name: c.name })}
                        onClick={() => setMoving(c)}
                      >
                        {t("tv.cluster.onAir.move")}
                      </Button>
                      <Button
                        size="sm"
                        variant="secondary"
                        disabled={busy}
                        aria-label={t("tv.cluster.onAir.stopNamed", { name: c.name })}
                        onClick={() => setStopping(c)}
                      >
                        {t("tv.cluster.onAir.stop")}
                      </Button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {msg && (
        <p
          role={msg.ok ? "status" : "alert"}
          className={msg.ok ? "mkt-q-status--ok" : "mkt-q-status--bad"}
        >
          {msg.text}
        </p>
      )}

      {moving && (
        <MoveDialog
          channel={moving}
          hosts={view.hosts}
          onClose={() => setMoving(null)}
          onMoved={async () => {
            setMsg({ ok: true, text: t("tv.cluster.onAir.moved", { name: moving.name }) });
            await onChanged();
          }}
        />
      )}
      {stopping && (
        <ConfirmDialog
          title={t("tv.cluster.stop.title", { name: stopping.name })}
          confirmLabel={t("tv.cluster.stop.confirm")}
          busy={busy}
          onClose={() => setStopping(null)}
          onConfirm={() => stop(stopping)}
        >
          <p>
            {t("tv.cluster.interrupt")} {t("tv.cluster.stop.body")}
          </p>
        </ConfirmDialog>
      )}
    </Card>
  );
}
