// The Relay tab (story 31.R): the home's service door, which lets machines
// run the Plex stages without the Plex token. Its state, the switch (rules
// section "relay"), each extension's allow-list, and the recent relayed
// calls (who, what, how it went: never the token).
//
//   GET   /api/cluster/relay      the door's status and recent calls (admin)
//   PATCH /api/rules/relay        on / off
import { useCallback, useEffect, useState } from "react";
import { Badge } from "@ds/components/Badge/Badge";
import { Toggle } from "@ds/components/Choice/Toggle";
import { useToast } from "@ds/components/Toast/Toast";
import { clusterChannelApi, tsMs, type RelayStatus } from "../../lib/clusterApi";
import { bidiIsolate, useI18n } from "../../lib/i18n";
import { rulesApi, rulesError } from "../../lib/rulesApi";
import { useClusterFmt } from "./format";

export const RELAY_POLL_MS = 5_000;

export function RelayPanel() {
  const { t, n } = useI18n();
  const f = useClusterFmt();
  const toast = useToast();
  const [status, setStatus] = useState<RelayStatus | null>(null);
  const [error, setError] = useState(false);
  const [busy, setBusy] = useState(false);

  const load = useCallback(() => {
    clusterChannelApi
      .relay()
      .then((s) => {
        setStatus(s);
        setError(false);
      })
      .catch(() => setError(true));
  }, []);

  useEffect(() => {
    load();
    const id = setInterval(load, RELAY_POLL_MS);
    return () => clearInterval(id);
  }, [load]);

  const toggle = (on: boolean) => {
    setBusy(true);
    rulesApi
      .patch("relay", on ? {} : { enabled: false }, on ? ["enabled"] : [])
      .then(load)
      .catch((e: unknown) => toast.show({ tone: "error", message: rulesError(e).message }))
      .finally(() => setBusy(false));
  };

  const errorText = (code: string) => {
    const key = `cluster.relay.err.${code}`;
    const word = t(key);
    return word === key ? code : word;
  };

  if (!status) {
    return (
      <div className="mkt-mc-section">
        {error ? (
          <p className="mkt-mc-error" role="alert">
            {t("cluster.relay.loadError")}
          </p>
        ) : (
          <p className="mkt-mc-muted">{t("common.loading")}</p>
        )}
      </div>
    );
  }

  const [tone, label] = !status.available
    ? (["neutral", "cluster.relay.state.unavailable"] as const)
    : status.enabled
      ? (["success", "cluster.relay.state.on"] as const)
      : (["warn", "cluster.relay.state.off"] as const);
  const s = status.settings;

  return (
    <div className="mkt-mc-section">
      <section className="mkt-mc-sub" aria-labelledby="mkt-cl-relay-h">
        <div className="mkt-mc-row">
          <h2 id="mkt-cl-relay-h" className="mkt-mc-sub__title">
            {t("cluster.relay.title")}
          </h2>
          <Badge tone={tone} role="status">
            {t(label)}
          </Badge>
        </div>
        <p className="mkt-mc-muted">{t("cluster.relay.intro")}</p>
        {status.available && (
          <>
            <Toggle
              label={t("cluster.relay.switch")}
              checked={status.enabled}
              disabled={busy}
              onChange={(e) => toggle(e.currentTarget.checked)}
            />
            <p className="mkt-mc-muted">
              {t("cluster.relay.limits", {
                rpm: n(s.requests_per_minute),
                burst: n(s.burst),
                inflight: n(s.max_in_flight),
                mb: n(s.max_response_mb),
                sec: n(s.timeout_sec),
              })}
            </p>
          </>
        )}
        {error && (
          <p className="mkt-mc-error" role="alert">
            {t("cluster.relay.loadError")}
          </p>
        )}
      </section>

      {status.extensions.map((ext) => (
        <section className="mkt-mc-sub" key={ext.name} aria-label={ext.name}>
          <div className="mkt-mc-row">
            <h3 className="mkt-mc-sub__title">{bidiIsolate(ext.name)}</h3>
            <Badge tone={ext.configured && ext.enabled ? "success" : "warn"}>
              {ext.configured && ext.enabled
                ? t("cluster.relay.ready")
                : t("cluster.relay.notReady")}
            </Badge>
          </div>
          {ext.upstream && (
            <p className="mkt-mc-muted">
              {t("cluster.relay.upstream", { host: bidiIsolate(ext.upstream) })}
            </p>
          )}
          {ext.reason && <p className="mkt-mc-note">{ext.reason}</p>}
          <details>
            <summary>{t("cluster.relay.allow", { n: n(ext.allow.length) })}</summary>
            <ul className="mkt-cl-relay-allow">
              {ext.allow.map((a) => (
                <li key={`${a.method} ${a.path}`}>
                  <code dir="ltr">
                    {a.method} {a.path}
                  </code>
                </li>
              ))}
            </ul>
          </details>
        </section>
      ))}

      {status.home_only.length > 0 && (
        <section className="mkt-mc-sub" aria-labelledby="mkt-cl-relay-home-h">
          <h3 id="mkt-cl-relay-home-h" className="mkt-mc-sub__title">
            {t("cluster.relay.homeOnly")}
          </h3>
          <ul>
            {status.home_only.map((h) => (
              <li key={h.name}>
                <strong>{bidiIsolate(h.name)}</strong>: {h.reason}
              </li>
            ))}
          </ul>
        </section>
      )}

      <section className="mkt-mc-sub" aria-labelledby="mkt-cl-relay-calls-h">
        <h3 id="mkt-cl-relay-calls-h" className="mkt-mc-sub__title">
          {t("cluster.relay.calls")}
        </h3>
        {status.calls.length === 0 ? (
          <p className="mkt-mc-muted">{t("cluster.relay.noCalls")}</p>
        ) : (
          <div className="mkt-mc-table-wrap">
            <table className="mkt-mc-table">
              <caption className="mkt-visually-hidden">{t("cluster.relay.calls")}</caption>
              <thead>
                <tr>
                  <th scope="col">{t("cluster.relay.col.time")}</th>
                  <th scope="col">{t("cluster.relay.col.machine")}</th>
                  <th scope="col">{t("cluster.relay.col.call")}</th>
                  <th scope="col">{t("cluster.relay.col.result")}</th>
                  <th scope="col">{t("cluster.relay.col.size")}</th>
                  <th scope="col">{t("cluster.relay.col.ms")}</th>
                </tr>
              </thead>
              <tbody>
                {status.calls.map((c, i) => {
                  const at = tsMs(c.ts);
                  return (
                    <tr key={`${c.ts}-${i}`}>
                      <td>{Number.isFinite(at) ? f.time(at) : "–"}</td>
                      <th scope="row">
                        {bidiIsolate(
                          c.node_name || (c.kind === "home" ? t("cluster.relay.home") : c.ip || "–")
                        )}
                      </th>
                      <td>
                        <code dir="ltr">
                          {c.ext} {c.method} {c.path}
                        </code>
                      </td>
                      <td>
                        {c.error ? (
                          <Badge tone="warn">{errorText(c.error)}</Badge>
                        ) : (
                          <Badge tone={c.status >= 400 ? "warn" : "success"}>
                            {String(c.status)}
                          </Badge>
                        )}
                      </td>
                      <td>{f.bytes(c.bytes)}</td>
                      <td>{n(c.ms)}</td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </div>
  );
}
