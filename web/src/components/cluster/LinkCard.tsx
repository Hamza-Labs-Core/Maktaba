// The cluster link (Epic 35 story 35.16) on the Machines page: how the machines reach the
// home (prefer, require, off), how many held their link for a day (the home requires it
// by itself once all of them did), each machine's link and certificate, and the cluster
// CA's fingerprint with its certificate to download (to install it for browser HTTPS on
// 8443). The link's settings are on the rules page (Machines, Cluster link). While the
// link is required, a machine whose processes do not present their certificate to the
// home's database yet (an older release) is said: its database login keeps its password.
import { useI18n } from "../../lib/i18n";
import type { ClusterLink, ClusterNode } from "../../lib/clusterApi";
import { useFmt } from "../queue/format";

/** A SHA-256 fingerprint in pairs, as a browser's certificate viewer shows it. */
export function fingerprint(hex: string): string {
  return (hex.toUpperCase().match(/.{1,2}/g) ?? []).join(":");
}

export function LinkCard({ link, nodes }: { link: ClusterLink; nodes: ClusterNode[] }) {
  const { t } = useI18n();
  const f = useFmt();
  const names = new Map(nodes.map((n) => [n.id, n.name]));
  const rows = Object.entries(link.nodes ?? {});
  return (
    <section className="mkt-mc-card mkt-mc-link" aria-labelledby="mkt-mc-link-h">
      <h2 id="mkt-mc-link-h" className="mkt-mc-card__eyebrow">
        {t("machines.link.title")}
      </h2>
      <p>
        {t(`machines.link.mode.${link.mode}`)}
        {link.mode === "prefer" && link.auto_require ? " " + t("machines.link.auto") : ""}
      </p>
      {link.machines > 0 && (
        <p className="mkt-mc-muted">
          {t("machines.link.ready", { ready: link.ready, total: link.machines })}
        </p>
      )}
      {rows.length > 0 && (
        <ul className="mkt-mc-list mkt-mc-link__nodes">
          {rows.map(([id, n]) => {
            const left = n.cert_not_after
              ? (Date.parse(n.cert_not_after) - Date.now()) / 1000
              : null;
            return (
              <li key={id}>
                <span>{names.get(id) ?? id.slice(0, 8)}</span>{" "}
                <span className="mkt-mc-muted">
                  {n.connected ? t("machines.link.connected") : t("machines.link.offline")}
                  {" · "}
                  {!n.has_cert
                    ? t("machines.link.noCert")
                    : left !== null && left > 0
                      ? t("machines.link.certLeft", { time: f.dur(left) })
                      : t("machines.link.certExpired")}
                  {n.clock_skew_seconds
                    ? " · " +
                      t("machines.link.clockSkew", { time: f.dur(Math.abs(n.clock_skew_seconds)) })
                    : ""}
                  {n.blocks ? " · " + t(`machines.link.block.${n.blocks}`) : ""}
                  {link.mode === "require" && n.has_cert && !n.db_cert
                    ? " · " + t("machines.link.dbPassword")
                    : ""}
                </span>
              </li>
            );
          })}
        </ul>
      )}
      <p className="mkt-mc-muted">
        {t("machines.link.ca")} <code dir="ltr">{fingerprint(link.ca_sha256)}</code>
      </p>
      <p>
        <a href={link.ca_url} download="maktaba-cluster-ca.crt">
          {t("machines.link.download")}
        </a>
      </p>
    </section>
  );
}
