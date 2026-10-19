// The per-process commands (design-agent.md §3 `cmd`): drain / undrain,
// reload the queues it pulls from, change its log level, disconnect it.
// Each sends over the hub (REST when the socket is down) and the result
// comes back as a toast.
import { useState } from "react";
import { Button } from "@ds/components/Button/Button";
import { Modal } from "@ds/components/Modal/Modal";
import { Select } from "@ds/components/Select/Select";
import { useToast } from "@ds/components/Toast/Toast";
import { ApiError } from "../../lib/api";
import { LOG_LEVELS, type ProcessCommand } from "../../lib/clusterApi";
import { runCommand, type LiveProcess } from "../../lib/clusterStream";
import { bidiIsolate, useI18n } from "../../lib/i18n";
import { processName } from "./names";

export function ProcessActions({
  process,
  onOpenLogs,
}: {
  process: LiveProcess;
  onOpenLogs: () => void;
}) {
  const { t } = useI18n();
  const toast = useToast();
  const [busy, setBusy] = useState(false);
  const [confirm, setConfirm] = useState(false);
  const [level, setLevel] = useState("");
  const name = processName(process);
  const off = !process.online;

  const run = (cmd: ProcessCommand, args: Record<string, unknown> = {}) => {
    setBusy(true);
    runCommand(process.session, cmd, args)
      .then((r) => {
        const label = t(`cluster.cmd.${cmd}`);
        toast.show({
          tone: r.ok ? "success" : "error",
          message: r.ok
            ? t("cluster.cmd.ok", { cmd: label, detail: r.detail ?? "" }).trim()
            : t("cluster.cmd.failed", { cmd: label, detail: r.detail ?? "" }).trim(),
        });
      })
      .catch((e: unknown) =>
        toast.show({
          tone: "error",
          message: t("cluster.cmd.failed", {
            cmd: t(`cluster.cmd.${cmd}`),
            detail: e instanceof ApiError ? e.problem.detail || e.problem.title : "",
          }).trim(),
        })
      )
      .finally(() => setBusy(false));
  };

  return (
    <div className="mkt-mc-actions mkt-cl-actions">
      <Button size="sm" variant="secondary" disabled={busy || off} onClick={() => run("drain")}>
        {t("cluster.cmd.drain")}
      </Button>
      <Button size="sm" variant="secondary" disabled={busy || off} onClick={() => run("undrain")}>
        {t("cluster.cmd.undrain")}
      </Button>
      <Button
        size="sm"
        variant="secondary"
        disabled={busy || off}
        onClick={() => run("reload_stages")}
      >
        {t("cluster.cmd.reload_stages")}
      </Button>
      <Select
        className="mkt-cl-level"
        aria-label={t("cluster.cmd.levelFor", { name })}
        value={level}
        disabled={busy || off}
        options={[
          { value: "", label: t("cluster.cmd.levelPick") },
          ...LOG_LEVELS.map((l) => ({ value: l, label: t(`cluster.level.${l}`) })),
        ]}
        onChange={(e) => {
          const v = e.currentTarget.value;
          setLevel(v);
          if (v) run("log_level", { level: v });
        }}
      />
      <Button size="sm" variant="ghost" onClick={onOpenLogs}>
        {t("cluster.logs.open")}
      </Button>
      <Button
        size="sm"
        variant="destructive"
        disabled={busy || off}
        onClick={() => setConfirm(true)}
      >
        {t("cluster.cmd.disconnect")}
      </Button>
      {confirm && (
        <Modal
          open
          dismissable={false}
          onClose={() => setConfirm(false)}
          title={t("cluster.cmd.disconnectTitle", { name: bidiIsolate(name) })}
          footer={
            <>
              <Button variant="secondary" onClick={() => setConfirm(false)}>
                {t("common.cancel")}
              </Button>
              <Button
                variant="destructive"
                onClick={() => {
                  setConfirm(false);
                  run("disconnect");
                }}
              >
                {t("cluster.cmd.disconnect")}
              </Button>
            </>
          }
        >
          <p>{t("cluster.cmd.disconnectBody")}</p>
        </Modal>
      )}
    </div>
  );
}
