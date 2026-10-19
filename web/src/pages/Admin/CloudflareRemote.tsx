// The Cloudflare Stream card's status panel (Epic 32 story 32.6): what the
// last "Test connection" found (one line per check), whether remote viewing
// is on (it can only be turned on after a passed test from the last 24 h),
// and "Remove integration" (deletes Maktaba's live inputs and signing key at
// Cloudflare, then the stored token).
import { useCallback, useEffect, useState } from "react";
import { Button } from "@ds/components/Button/Button";
import { lookupMessage, useI18n } from "../../lib/i18n";
import { tvRemoteApi } from "../../lib/tvRemoteApi";
import type { RemoteIntegration, RemoteTestLine, RemoteTestResult } from "../../lib/tvRemoteApi";
import { useAction } from "../../components/queue/useAction";
import { RemoteUsage } from "./RemoteUsage";

const LINE_KEYS = [
  "token",
  "stream",
  "storage",
  "analytics",
  "signing_key",
  "customer_domain",
  "push",
] as const;

/**
 * One check of Test connection: ok / failed / warning / not checked, the
 * reason, and the check's error code. `ext` names the extension whose
 * labels to use (`ext.check.<ext>.<key>`); Cloudflare Stream keeps its
 * `cfstream.line.*` labels. An unknown key shows as is.
 */
export function RemoteTestLines({
  result,
  ext = "cloudflare_stream",
}: {
  result: RemoteTestResult;
  ext?: string;
}) {
  const { t, locale } = useI18n();
  const label = (l: RemoteTestLine) => {
    if (ext === "cloudflare_stream") {
      return (LINE_KEYS as readonly string[]).includes(l.key) ? t(`cfstream.line.${l.key}`) : l.key;
    }
    return lookupMessage(locale, `ext.check.${ext}.${l.key}`) ?? l.key;
  };
  const state = (l: RemoteTestLine) =>
    l.skipped ? "skipped" : l.ok ? "ok" : l.optional ? "warn" : "bad";
  return (
    <ul className="mkt-q-cflines" aria-label={t("cfstream.lines")}>
      {result.lines.map((l) => {
        const s = state(l);
        return (
          <li
            key={l.key}
            className={`mkt-q-status--${s === "warn" ? "off" : s === "skipped" ? "off" : s}`}
          >
            <span aria-hidden="true">{s === "ok" ? "✓" : s === "bad" ? "✕" : "–"}</span>{" "}
            <strong>{label(l)}</strong>{" "}
            <span className="mkt-q-muted">({t(`cfstream.state.${s}`)})</span>
            {": "}
            <span dir="auto">{l.detail}</span>
            {l.code && !l.ok && !l.skipped && (
              <>
                {" "}
                <code className="mkt-q-muted" dir="ltr">
                  {l.code}
                </code>
              </>
            )}
          </li>
        );
      })}
    </ul>
  );
}

export function CloudflareRemote({ refresh }: { refresh: number }) {
  const { t } = useI18n();
  const action = useAction();
  const [st, setSt] = useState<RemoteIntegration | null>(null);
  const [failed, setFailed] = useState(false);
  const [removed, setRemoved] = useState<string | null>(null);

  const load = useCallback(() => {
    tvRemoteApi
      .status()
      .then((r) => {
        setSt(r.integration);
        setFailed(false);
      })
      .catch(() => setFailed(true));
  }, []);
  useEffect(load, [load, refresh]);

  const toggle = (enabled: boolean) =>
    void action
      .run("enable", () => tvRemoteApi.setEnabled(enabled))
      .then((r) => r && setSt(r.integration));

  const remove = () => {
    if (!window.confirm(t("cfstream.removeConfirm"))) return;
    void action
      .run("remove", () => tvRemoteApi.remove())
      .then((r) => {
        if (!r) return;
        setRemoved(
          r.errors.length
            ? t("cfstream.removedWithErrors", { errors: r.errors.join("; ") })
            : t("cfstream.removed", { n: r.deleted_inputs.length })
        );
        load();
      });
  };

  if (failed && !st) {
    return (
      <p role="alert" className="mkt-q-banner">
        {t("cfstream.error")}
      </p>
    );
  }
  if (!st) {
    return (
      <p className="mkt-loading" role="status">
        {t("common.loading")}
      </p>
    );
  }
  const last = st.last_test;
  return (
    <div className="mkt-q-cfstatus" aria-label={t("cfstream.status")} role="group">
      <p>
        <span className={`mkt-q-status--${st.enabled ? "ok" : "off"}`}>
          {st.enabled ? t("cfstream.remoteOn") : t("cfstream.remoteOff")}
        </span>
        {st.customer_domain && (
          <span className="mkt-q-muted">
            {" · "}
            <bdi>{st.customer_domain}</bdi>
          </span>
        )}
        {st.signing_key_set && <span className="mkt-q-muted"> · {t("cfstream.keySet")}</span>}
      </p>
      {st.token_problem && (
        <p role="alert" className="mkt-q-banner">
          {st.token_problem}
        </p>
      )}
      {last ? (
        <>
          <p className={last.ok ? "mkt-q-status--ok" : "mkt-q-status--bad"}>
            {t(last.ok ? "cfstream.lastOk" : "cfstream.lastBad", {
              at: new Date(last.checked_at).toLocaleString(),
            })}
          </p>
          <RemoteTestLines result={last} />
        </>
      ) : (
        <p className="mkt-q-muted">{t("cfstream.neverTested")}</p>
      )}
      <div className="mkt-q-extcard__actions">
        <label className="mkt-q-check">
          <input
            type="checkbox"
            role="switch"
            checked={st.enabled}
            disabled={action.busy === "enable" || (!st.enabled && !st.can_enable)}
            onChange={(e) => toggle(e.target.checked)}
          />
          <span>{t("cfstream.remoteSwitch")}</span>
        </label>
        {!st.enabled && !st.can_enable && (
          <span className="mkt-q-muted">{t("cfstream.needsTest")}</span>
        )}
        {(st.token_set || st.account_id) && (
          <Button variant="secondary" loading={action.busy === "remove"} onClick={remove}>
            {t("cfstream.remove")}
          </Button>
        )}
      </div>
      {st.configured && <RemoteUsage key={refresh} />}
      {removed && (
        <p role="status" className="mkt-q-muted">
          {removed}
        </p>
      )}
      {action.error && (
        <p role="alert" className="mkt-q-banner">
          {action.error}
        </p>
      )}
    </div>
  );
}
