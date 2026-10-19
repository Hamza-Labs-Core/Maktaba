// The relay door on the Machines tab (story 31.R): is the home's service door
// on, how many calls it relayed or refused, and the last few calls.
//
// Built to hold still like the cards around it: the card, its badge, its
// three counters and its five call rows are always there (a missing value
// reads "–"); rows are fixed height with one-line cells; the newest call is
// on top and values change in place; a poll that brings nothing new does
// not re-render. Numbers are tabular.
//
//   GET /api/cluster/relay   every 5 s while the tab is shown
import { memo, useEffect, useRef, useState } from "react";
import { Badge } from "@ds/components/Badge/Badge";
import { clusterChannelApi, tsMs, type RelayCall, type RelayStatus } from "../../lib/clusterApi";
import { bidiIsolate, useI18n } from "../../lib/i18n";
import { useClusterFmt } from "./format";

export const RELAY_CARD_POLL_MS = 5_000;
export const RELAY_CARD_ROWS = 5;

type Summary = {
  state: "loading" | "unavailable" | "on" | "off" | "error";
  calls: number | null;
  refused: number | null;
  failed: number | null;
  recent: RelayCall[];
};

/**
 * The relay status with every list present: the API may send null (or
 * leave out) extensions, home_only, calls, totals, an extension's allow
 * list, or the settings.
 */
export function relayStatusOf(s: RelayStatus): RelayStatus {
  const list = <T,>(v: T[] | null | undefined): T[] => (Array.isArray(v) ? v : []);
  return {
    ...s,
    extensions: list(s.extensions).map((e) => ({ ...e, allow: list(e.allow) })),
    home_only: list(s.home_only),
    calls: list(s.calls),
    totals: list(s.totals),
  };
}

function summarise(raw: RelayStatus): Summary {
  const s = relayStatusOf(raw);
  const sum = (k: "calls" | "refused" | "failed") => s.totals.reduce((a, x) => a + (x[k] ?? 0), 0);
  return {
    state: !s.available ? "unavailable" : s.enabled ? "on" : "off",
    calls: sum("calls"),
    refused: sum("refused"),
    failed: sum("failed"),
    recent: s.calls.slice(0, RELAY_CARD_ROWS),
  };
}

const EMPTY: Summary = { state: "loading", calls: null, refused: null, failed: null, recent: [] };

export function RelayDoorCard() {
  const [summary, setSummary] = useState<Summary>(EMPTY);
  const last = useRef("");

  useEffect(() => {
    let alive = true;
    const load = () => {
      clusterChannelApi
        .relay()
        .then((s) => {
          if (!alive) return;
          const next = summarise(s);
          const key = JSON.stringify(next);
          if (key === last.current) return; // nothing new: no re-render
          last.current = key;
          setSummary(next);
        })
        .catch(() => {
          if (!alive) return;
          setSummary((cur) => (cur.state === "error" ? cur : { ...cur, state: "error" }));
          last.current = "";
        });
    };
    load();
    const id = setInterval(load, RELAY_CARD_POLL_MS);
    return () => {
      alive = false;
      clearInterval(id);
    };
  }, []);

  return <RelayDoorView summary={summary} />;
}

const TONE = {
  loading: "neutral",
  unavailable: "neutral",
  on: "success",
  off: "warn",
  error: "warn",
} as const;

const RelayDoorView = memo(function RelayDoorView({ summary }: { summary: Summary }) {
  const { t, n } = useI18n();
  const f = useClusterFmt();
  const num = (v: number | null) => (v === null ? "–" : n(v));
  const errorText = (code: string) => {
    const key = `cluster.relay.err.${code}`;
    const word = t(key);
    return word === key ? code : word;
  };
  const rows: (RelayCall | null)[] = Array.from(
    { length: RELAY_CARD_ROWS },
    (_, i) => summary.recent[i] ?? null
  );
  return (
    <section className="mkt-mc-section mkt-cl-door" aria-labelledby="mkt-cl-door-h">
      <div className="mkt-mc-row">
        <h2 id="mkt-cl-door-h" className="mkt-mc-section__title">
          {t("cluster.relay.title")}
        </h2>
        <Badge tone={TONE[summary.state]} role="status">
          {t(`cluster.relay.card.${summary.state}`)}
        </Badge>
      </div>
      <dl className="mkt-cl-door__counts">
        <div>
          <dt>{t("cluster.relay.card.calls")}</dt>
          <dd>{num(summary.calls)}</dd>
        </div>
        <div>
          <dt>{t("cluster.relay.card.refused")}</dt>
          <dd>{num(summary.refused)}</dd>
        </div>
        <div>
          <dt>{t("cluster.relay.card.failed")}</dt>
          <dd>{num(summary.failed)}</dd>
        </div>
      </dl>
      <table className="mkt-mc-table mkt-cl-door__calls">
        <caption className="mkt-visually-hidden">{t("cluster.relay.calls")}</caption>
        <thead>
          <tr>
            <th scope="col">{t("cluster.relay.col.time")}</th>
            <th scope="col">{t("cluster.relay.col.machine")}</th>
            <th scope="col">{t("cluster.relay.col.call")}</th>
            <th scope="col">{t("cluster.relay.col.result")}</th>
            <th scope="col">{t("cluster.relay.col.ms")}</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((c, i) => {
            const at = c ? tsMs(c.ts) : NaN;
            const who = c
              ? c.node_name || (c.kind === "home" ? t("cluster.relay.home") : c.ip || "–")
              : "–";
            const result = !c ? "–" : c.error ? errorText(c.error) : String(c.status);
            return (
              // slots, not calls: row i is always row i, its values change in place
              <tr key={i} data-empty={c ? undefined : "true"}>
                <td>{Number.isFinite(at) ? f.time(at) : "–"}</td>
                <td title={who}>{bidiIsolate(who)}</td>
                <td title={c ? `${c.method} ${c.path}` : undefined}>
                  <code dir="ltr">{c ? `${c.method} ${c.path}` : "–"}</code>
                </td>
                <td className={c && (c.error || c.status >= 400) ? "mkt-cl-door__bad" : undefined}>
                  {result}
                </td>
                <td>{c ? n(c.ms) : "–"}</td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </section>
  );
});
