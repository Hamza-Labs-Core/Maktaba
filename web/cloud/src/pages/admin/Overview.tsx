// /admin — fleet and revenue at a glance.
import { Card } from "@ds/components/Card/Card";
import { api } from "../../api";
import { useI18n } from "../../i18n";
import { LoadError, Loading, Page, useLoad, useMoney } from "../../ui";

interface Fleet {
  users_total: number;
  users_suspended: number;
  servers_total: number;
  servers_online: number;
  servers_stale: number;
  tunnels_live?: number;
}

interface Revenue {
  currency: string;
  mrr_cents: number;
  arr_cents: number;
  active_paid: number;
  past_due: number;
  cancelling: number;
  breakdown: { plan: string; users: number; active: number; mrr_cents: number }[];
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <Card className="mkc-stat">
      <div className="mkc-stat__value">{value}</div>
      <div className="mkc-muted">{label}</div>
    </Card>
  );
}

export function AdminOverview() {
  const { t, n } = useI18n();
  const money = useMoney();
  const data = useLoad(
    () => Promise.all([api.get<Fleet>("/v1/admin/fleet"), api.get<Revenue>("/v1/admin/revenue")]),
    "overview"
  );

  if (data.error != null) {
    return (
      <Page title={t("admin.overview.title")}>
        <LoadError error={data.error} onRetry={data.reload} />
      </Page>
    );
  }
  if (!data.data) {
    return (
      <Page title={t("admin.overview.title")}>
        <Loading />
      </Page>
    );
  }
  const [fleet, rev] = data.data;
  return (
    <Page title={t("admin.overview.title")}>
      <h2>{t("admin.overview.fleet")}</h2>
      <div className="mkc-stats">
        <Stat label={t("admin.fleet.users")} value={n(fleet.users_total)} />
        <Stat label={t("admin.fleet.suspended")} value={n(fleet.users_suspended)} />
        <Stat label={t("admin.fleet.servers")} value={n(fleet.servers_total)} />
        <Stat label={t("admin.fleet.online")} value={n(fleet.servers_online)} />
        <Stat label={t("admin.fleet.stale")} value={n(fleet.servers_stale)} />
        {fleet.tunnels_live != null && (
          <Stat label={t("admin.fleet.tunnels")} value={n(fleet.tunnels_live)} />
        )}
      </div>
      <h2>{t("admin.overview.revenue")}</h2>
      <div className="mkc-stats">
        <Stat label={t("admin.revenue.mrr")} value={money(rev.mrr_cents, rev.currency)} />
        <Stat label={t("admin.revenue.arr")} value={money(rev.arr_cents, rev.currency)} />
        <Stat label={t("admin.revenue.activePaid")} value={n(rev.active_paid)} />
        <Stat label={t("admin.revenue.pastDue")} value={n(rev.past_due)} />
        <Stat label={t("admin.revenue.cancelling")} value={n(rev.cancelling)} />
      </div>
      <table className="mkc-table">
        <caption className="mkc-visually-hidden">{t("admin.revenue.breakdown")}</caption>
        <thead>
          <tr>
            <th scope="col">{t("admin.col.plan")}</th>
            <th scope="col">{t("admin.revenue.users")}</th>
            <th scope="col">{t("admin.revenue.active")}</th>
            <th scope="col">{t("admin.revenue.mrr")}</th>
          </tr>
        </thead>
        <tbody>
          {rev.breakdown.map((b) => (
            <tr key={b.plan}>
              <td>{b.plan}</td>
              <td>{n(b.users)}</td>
              <td>{n(b.active)}</td>
              <td>{money(b.mrr_cents, rev.currency)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </Page>
  );
}
