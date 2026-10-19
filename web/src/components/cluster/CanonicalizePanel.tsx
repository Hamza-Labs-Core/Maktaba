// Story 31.3 — the home converts its stored media paths to canonical share
// paths (smb://server/share/...) by itself. This shows where that stands on the
// Machines page (the home card): what it waits for, the run in progress, the
// last conversion, the servers and Plex path map entries it added. The admin
// can ask for a check now, revert the last conversion (automatic conversion
// then holds) and resume it. The home's pipeline takes a request within 30 s.
import { useState } from "react";
import { Button } from "@ds/components/Button/Button";
import { useToast } from "@ds/components/Toast/Toast";
import { ApiError } from "../../lib/api";
import { clusterApi, type CanonAction, type CanonStatus } from "../../lib/clusterApi";
import { useI18n } from "../../lib/i18n";
import { useClusterFmt } from "./format";

const WORKING = new Set(["backing_up", "pausing", "applying", "verifying", "reverting"]);

export function CanonicalizePanel({
  status,
  onChanged,
}: {
  status: CanonStatus | null;
  onChanged: () => void;
}) {
  const { t, n } = useI18n();
  const cf = useClusterFmt();
  const toast = useToast();
  const [busy, setBusy] = useState(false);
  const [confirmRevert, setConfirmRevert] = useState(false);
  const st = status?.state ?? {};
  const phase = st.phase ?? "never";
  const working = WORKING.has(phase);
  const run = st.run ?? null;
  const time = (iso?: string | null) => (iso ? cf.time(Date.parse(iso)) : "");
  const reasonText = (code: string, detail?: string) => {
    const key = `machines.canon.reason.${code}`;
    const word = t(key, { detail: detail ?? "" });
    return word === key ? `${code}${detail ? `: ${detail}` : ""}` : word;
  };

  const ask = (action: CanonAction) => {
    setBusy(true);
    clusterApi
      .canonicalize(action)
      .then(() => {
        setConfirmRevert(false);
        onChanged();
      })
      .catch((e: unknown) =>
        toast.show({
          tone: "error",
          message: e instanceof ApiError ? e.problem.detail || e.problem.title : t("common.error"),
        })
      )
      .finally(() => setBusy(false));
  };

  return (
    <section className="mkt-mc-sub" aria-label={t("machines.canon.title")}>
      <h4 className="mkt-mc-sub__title">{t("machines.canon.title")}</h4>
      <p className="mkt-mc-line" role="status">
        {t(`machines.canon.phase.${phase}`)}
        {st.checked_at && !working && (
          <span className="mkt-mc-muted">
            {" "}
            {t("machines.canon.checkedAt", { time: time(st.checked_at) })}
          </span>
        )}
      </p>
      {run && (run.total ?? 0) > 0 && (
        <p className="mkt-mc-line">
          <progress
            max={run.total ?? 0}
            value={run.done ?? 0}
            aria-label={t("machines.canon.progressLabel")}
          />{" "}
          {t("machines.canon.progress", { done: n(run.done ?? 0), total: n(run.total ?? 0) })}
        </p>
      )}
      {st.problems && st.problems.length > 0 && (
        <ul className="mkt-mc-map" aria-label={t("machines.canon.problems")}>
          {st.problems.map((p, i) => (
            <li key={`${p.code}-${i}`}>{reasonText(p.code, p.detail)}</li>
          ))}
        </ul>
      )}
      {st.last && (
        <p className="mkt-mc-muted">
          {t("machines.canon.last", {
            n: n(st.last.changes),
            time: time(st.last.finished_at),
          })}
        </p>
      )}
      {st.plan && st.plan.collisions > 0 && (
        <p className="mkt-mc-muted">
          {t("machines.canon.collisions", { n: n(st.plan.collisions) })}
        </p>
      )}
      {st.reverted && (
        <p className="mkt-mc-muted">
          {t("machines.canon.reverted", { n: n(st.reverted.rows), time: time(st.reverted.at) })}
        </p>
      )}
      {st.servers_added && st.servers_added.length > 0 && (
        <p className="mkt-mc-muted">
          {t("machines.canon.serversAdded")}{" "}
          {st.servers_added.map((s) => (
            <code key={s.name} dir="ltr">
              {`smb://${s.name}/ (${(s.addresses ?? []).join(", ")}) `}
            </code>
          ))}
        </p>
      )}
      {st.plex_added && st.plex_added.length > 0 && (
        <p className="mkt-mc-muted">
          {t("machines.canon.plexAdded")}{" "}
          {st.plex_added.map((e) => (
            <code key={e} dir="ltr">
              {`${e} `}
            </code>
          ))}
        </p>
      )}
      {status?.request && (
        <p className="mkt-mc-muted">
          {t("machines.canon.requested", { action: t(`machines.canon.action.${status.request}`) })}
        </p>
      )}
      <div className="mkt-mc-actions">
        <Button size="sm" variant="secondary" disabled={busy || working} onClick={() => ask("run")}>
          {t("machines.canon.run")}
        </Button>
        {st.held ? (
          <Button size="sm" disabled={busy || working} onClick={() => ask("resume")}>
            {t("machines.canon.resume")}
          </Button>
        ) : (
          st.last &&
          (confirmRevert ? (
            <>
              <span className="mkt-mc-muted">
                {t("machines.canon.revertConfirm", { n: n(st.last.changes) })}
              </span>
              <Button
                size="sm"
                variant="destructive"
                disabled={busy || working}
                onClick={() => ask("revert")}
              >
                {t("machines.canon.revertYes")}
              </Button>
              <Button size="sm" variant="ghost" onClick={() => setConfirmRevert(false)}>
                {t("common.cancel")}
              </Button>
            </>
          ) : (
            <Button
              size="sm"
              variant="ghost"
              disabled={busy || working}
              onClick={() => setConfirmRevert(true)}
            >
              {t("machines.canon.revert")}
            </Button>
          ))
        )}
      </div>
    </section>
  );
}
