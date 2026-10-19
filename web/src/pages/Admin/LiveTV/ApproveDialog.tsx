// Approve, Schedule and Swap now (story 33.11 AC4, TC3): when the change goes on the air
// (default: as soon as possible - each channel switches at its next programme boundary),
// the swap mode, the expected switch time of every changed channel, the second-admin
// rule and the parameter-change acknowledgement when verification asks for it.
import { useEffect, useMemo, useState } from "react";
import { Button } from "@ds/components/Button/Button";
import { Modal } from "@ds/components/Modal/Modal";
import { Checkbox } from "@ds/components/Choice/Checkbox";
import { Radio } from "@ds/components/Choice/Radio";
import { useI18n } from "../../../lib/i18n";
import {
  needsParamAck,
  tvChangesApi,
  type Boundaries,
  type StagingConfig,
  type TvChangeDetail,
} from "../../../lib/tvChanges";
import { Clock, actionError } from "./changeBits";

export type SwapDialogMode = "approve" | "schedule" | "promote";

/** A Date as the value of an <input type="datetime-local"> (local time). */
export function localInput(d: Date): string {
  const p = (x: number) => String(x).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}T${p(d.getHours())}:${p(d.getMinutes())}`;
}

export function ApproveDialog({
  mode,
  change,
  cfg,
  meId,
  onClose,
  onDone,
}: {
  mode: SwapDialogMode;
  change: TvChangeDetail;
  cfg: StagingConfig | null;
  meId: string | undefined;
  onClose: () => void;
  onDone: (c: TvChangeDetail) => void;
}) {
  const { t, n } = useI18n();
  const [when, setWhen] = useState<"asap" | "at">(
    change.swap_at && mode !== "promote" ? "at" : "asap"
  );
  const [at, setAt] = useState(() =>
    localInput(change.swap_at ? new Date(change.swap_at) : new Date(Date.now() + 60 * 60 * 1000))
  );
  const [wait, setWait] = useState<"boundary" | "immediate">(
    change.swap_wait ?? cfg?.swap_wait ?? "boundary"
  );
  const [ack, setAck] = useState(false);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [bounds, setBounds] = useState<Boundaries | null>(null);
  const boundsChannels = bounds?.channels ?? [];

  const ackNeeded = mode !== "schedule" && needsParamAck(change);
  const ownChange = !!cfg?.require_second_admin && !!meId && change.created_by === meId;
  const blocked = mode === "approve" && ownChange;
  const swapAtIso = useMemo(() => {
    if (when !== "at") return null;
    const d = new Date(at);
    return Number.isNaN(d.getTime()) ? null : d.toISOString();
  }, [when, at]);
  const atInvalid = when === "at" && (!swapAtIso || new Date(swapAtIso).getTime() < Date.now());

  // the expected per-channel switch times for the chosen moment and mode
  useEffect(() => {
    let alive = true;
    const id = window.setTimeout(() => {
      tvChangesApi
        .boundaries(change.id, swapAtIso ?? undefined, wait)
        .then((b) => alive && setBounds(b))
        .catch(() => alive && setBounds(null));
    }, 150);
    return () => {
      alive = false;
      window.clearTimeout(id);
    };
  }, [change.id, swapAtIso, wait]);

  async function submit() {
    setBusy(true);
    setErr(null);
    try {
      let out: TvChangeDetail;
      if (mode === "promote") {
        if ((change.swap_wait ?? cfg?.swap_wait) !== wait) {
          await tvChangesApi.schedule(change.id, {
            swap_at: change.swap_at ?? null,
            swap_wait: wait,
          });
        }
        out = await tvChangesApi.promote(change.id);
      } else if (mode === "schedule") {
        out = await tvChangesApi.schedule(change.id, { swap_at: swapAtIso, swap_wait: wait });
      } else {
        if ((change.swap_wait ?? cfg?.swap_wait) !== wait) {
          await tvChangesApi.schedule(change.id, {
            swap_at: change.swap_at ?? null,
            swap_wait: wait,
          });
        }
        out = await tvChangesApi.approve(change.id, {
          swap_at: swapAtIso,
          ...(ackNeeded ? { ack_param_change: ack } : {}),
        });
      }
      onDone(out);
    } catch (e) {
      setErr(actionError(t, e));
    } finally {
      setBusy(false);
    }
  }

  const title = t(`tvchanges.dialog.${mode}.title`, { n: n(change.number) });
  const confirm = t(`tvchanges.dialog.${mode}.confirm`);
  return (
    <Modal
      open
      onClose={onClose}
      title={title}
      dismissable={mode !== "promote"}
      className="mkt-tvx-dialog"
      footer={
        <div className="mkt-livetv__actions">
          <Button variant="secondary" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button
            variant={mode === "promote" ? "destructive" : "primary"}
            disabled={busy || blocked || (ackNeeded && !ack) || atInvalid}
            onClick={() => void submit()}
            data-testid="dialog-confirm"
          >
            {confirm}
          </Button>
        </div>
      }
    >
      <p>{t(`tvchanges.dialog.${mode}.intro`)}</p>
      {mode !== "promote" && (
        <fieldset className="mkt-tvx-fieldset">
          <legend>{t("tvchanges.dialog.when")}</legend>
          <Radio
            name="swap-when"
            checked={when === "asap"}
            onChange={() => setWhen("asap")}
            label={t("tvchanges.dialog.asap")}
          />
          <Radio
            name="swap-when"
            checked={when === "at"}
            onChange={() => setWhen("at")}
            label={t("tvchanges.dialog.at")}
          />
          {when === "at" && (
            <label className="mkt-tvx-at">
              <span>{t("tvchanges.dialog.atLabel")}</span>
              <input
                type="datetime-local"
                value={at}
                onChange={(e) => setAt(e.target.value)}
                aria-invalid={atInvalid}
              />
              {atInvalid && <span className="mkt-tvx-error">{t("tvchanges.dialog.atPast")}</span>}
            </label>
          )}
        </fieldset>
      )}
      <fieldset className="mkt-tvx-fieldset">
        <legend>{t("tvchanges.dialog.mode")}</legend>
        <Radio
          name="swap-wait"
          checked={wait === "boundary"}
          onChange={() => setWait("boundary")}
          label={t("tvchanges.swapWait.boundary")}
        />
        <Radio
          name="swap-wait"
          checked={wait === "immediate"}
          onChange={() => setWait("immediate")}
          label={t("tvchanges.swapWait.immediate")}
        />
      </fieldset>

      <div className="mkt-tvx-bounds" data-testid="boundaries">
        <h3>{t("tvchanges.dialog.switches")}</h3>
        {!bounds ? (
          <div className="mkt-tvx-skeleton mkt-tvx-skeleton--short" aria-hidden="true" />
        ) : boundsChannels.length === 0 ? (
          <p className="mkt-tvx-muted">{t("tvchanges.dialog.noSwitches")}</p>
        ) : (
          <>
            <table className="mkt-tvx-kv">
              <thead>
                <tr>
                  <th scope="col">{t("tvchanges.checks.channel")}</th>
                  <th scope="col">{t("tvchanges.dialog.onAir")}</th>
                  <th scope="col">{t("tvchanges.dialog.switchAt")}</th>
                </tr>
              </thead>
              <tbody>
                {boundsChannels.map((b) => (
                  <tr key={b.channel_id}>
                    <td>{t("tvchanges.lineup.channel", { n: n(b.number), name: b.name })}</td>
                    <td dir="auto">{b.on_air || "—"}</td>
                    <td>
                      <Clock at={b.switch_at} />
                      {b.capped && (
                        <span className="mkt-tvx-muted">
                          {" "}
                          {t("tvchanges.dialog.capped", { n: n(bounds.boundary_max_wait_minutes) })}
                        </span>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
            {bounds.last_switch_at && (
              <p className="mkt-tvx-hint">
                {t("tvchanges.dialog.lastSwitch")} <Clock at={bounds.last_switch_at} />
              </p>
            )}
          </>
        )}
      </div>

      {ackNeeded && (
        <div className="mkt-tvx-banner mkt-tvx-banner--warn">
          <Checkbox
            checked={ack}
            onChange={(e) => setAck(e.target.checked)}
            label={t("tvchanges.dialog.ack")}
            data-testid="ack-param"
          />
        </div>
      )}
      {mode === "approve" && cfg?.require_second_admin && (
        <p className={ownChange ? "mkt-tvx-error" : "mkt-tvx-hint"} data-testid="second-admin">
          {ownChange ? t("tvchanges.err.secondAdmin") : t("tvchanges.dialog.secondAdminOk")}
        </p>
      )}
      {err && (
        <p role="alert" className="mkt-tvx-error">
          {err}
        </p>
      )}
    </Modal>
  );
}
