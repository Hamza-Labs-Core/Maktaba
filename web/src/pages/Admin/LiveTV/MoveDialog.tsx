// Move a channel that is on air to another host, or let the cluster pick
// ("Automatic"). Only hosts that are online and not draining are offered.
// Moving restarts the channel's encode, so the dialog says viewers are
// interrupted before anything is sent.
import { useState } from "react";
import { Select } from "@ds/components/Select/Select";
import { useI18n } from "../../../lib/i18n";
import {
  movableHosts,
  clusterErrText,
  tvClusterApi,
  type OnAirChannel,
  type StreamingHost,
} from "../../../lib/tvClusterApi";
import { ConfirmDialog } from "./ConfirmDialog";

const AUTO = "";

export function MoveDialog({
  channel,
  hosts,
  onClose,
  onMoved,
}: {
  channel: OnAirChannel;
  hosts: StreamingHost[];
  onClose: () => void;
  onMoved: () => void | Promise<void>;
}) {
  const { t } = useI18n();
  const [target, setTarget] = useState(AUTO);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const choices = movableHosts(hosts);

  async function move() {
    setBusy(true);
    setErr(null);
    try {
      await tvClusterApi.move(channel.channel_id, target || undefined);
      await onMoved();
      onClose();
    } catch (e) {
      setErr(clusterErrText(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <ConfirmDialog
      title={t("tv.cluster.move.title", { name: channel.name })}
      confirmLabel={t("tv.cluster.move.confirm")}
      busy={busy}
      onClose={onClose}
      onConfirm={move}
    >
      <Select
        label={t("tv.cluster.move.host")}
        value={target}
        onChange={(e) => setTarget(e.target.value)}
        options={[
          { value: AUTO, label: t("tv.cluster.automatic") },
          ...choices.map((h) => ({
            value: h.host_id,
            label:
              h.host_id === channel.host_id
                ? t("tv.cluster.move.current", { name: h.name })
                : h.name,
          })),
        ]}
      />
      {choices.length === 0 && <p className="mkt-livetv__hint">{t("tv.cluster.move.noHosts")}</p>}
      <p>{t("tv.cluster.interrupt")}</p>
      {err && <p role="alert">{err}</p>}
    </ConfirmDialog>
  );
}
