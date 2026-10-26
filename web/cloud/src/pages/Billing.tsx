// /billing — the plans, the account's subscription, Stripe Checkout / portal,
// and this month's relay usage.
import { useState } from "react";
import { useSearchParams } from "react-router-dom";
import { Badge } from "@ds/components/Badge/Badge";
import { Button } from "@ds/components/Button/Button";
import { Card } from "@ds/components/Card/Card";
import { ProgressBar } from "@ds/components/ProgressBar/ProgressBar";
import { useToast } from "@ds/components/Toast/Toast";
import { api } from "../api";
import { useI18n } from "../i18n";
import { navigateExternal } from "../nav";
import {
  Banner,
  LoadError,
  Loading,
  Page,
  useBytes,
  useErrorMessage,
  useLoad,
  useMoney,
} from "../ui";

export interface Plan {
  id: string;
  name: string;
  price_cents: number;
  bandwidth_bytes_per_month: number;
  max_servers: number;
  max_concurrent_streams: number;
  relay_qos: string;
  includes_transcoding: boolean;
  family_seats: number;
  purchasable: boolean;
  /** The plan the license gives this account's homes: "home", "pro" or "" (Epic 16). */
  home_tier?: string;
}

export interface BillingMe {
  plan: string;
  status: string;
  interval?: string;
  current_period_end?: string;
  cancel_at_period_end: boolean;
  has_customer: boolean;
}

export interface Usage {
  month: string;
  plan: string;
  limit_bytes: number;
  used_bytes: number;
  servers: { id: string; name: string; bytes_in: number; bytes_out: number }[];
}

interface BillingData {
  plans: Plan[];
  me: BillingMe;
  usage: Usage | null;
}

async function loadBilling(): Promise<BillingData> {
  const [plans, me, usage] = await Promise.all([
    api.get<{ plans: Plan[] }>("/v1/billing/plans", { auth: false }),
    api.get<BillingMe>("/v1/billing/me"),
    // usage is a side panel: the page still works without it
    api.get<Usage>("/v1/billing/usage").catch(() => null),
  ]);
  return { plans: plans?.plans ?? [], me, usage };
}

export function Billing() {
  const { t } = useI18n();
  const [params] = useSearchParams();
  const data = useLoad(loadBilling, "billing");
  const outcome = params.get("billing");

  return (
    <Page title={t("billing.title")}>
      {outcome === "ok" && <Banner tone="success">{t("billing.returned.ok")}</Banner>}
      {outcome === "cancel" && <Banner tone="info">{t("billing.returned.cancel")}</Banner>}
      {data.error != null ? (
        <LoadError error={data.error} onRetry={data.reload} />
      ) : !data.data ? (
        <Loading />
      ) : (
        <BillingBody data={data.data} />
      )}
    </Page>
  );
}

function statusTone(status: string): "success" | "warn" | "error" | "neutral" {
  if (status === "active" || status === "trialing") return "success";
  if (status === "past_due" || status === "incomplete") return "warn";
  if (status === "unpaid" || status === "canceled") return "error";
  return "neutral";
}

function BillingBody({ data }: { data: BillingData }) {
  const { t, has, formatDate } = useI18n();
  const message = useErrorMessage();
  const toast = useToast();
  const [busy, setBusy] = useState<string | null>(null);
  const { me, plans, usage } = data;
  const planName = (id: string) => plans.find((p) => p.id === id)?.name ?? id;

  async function upgrade(plan: string) {
    setBusy(plan);
    try {
      const r = await api.post<{ checkout_url: string; session_id: string }>(
        "/v1/billing/checkout",
        { plan }
      );
      navigateExternal(r.checkout_url);
    } catch (err) {
      toast.show({ tone: "error", message: message(err) });
      setBusy(null);
    }
  }

  async function portal() {
    setBusy("portal");
    try {
      const r = await api.post<{ portal_url: string }>("/v1/billing/portal");
      navigateExternal(r.portal_url);
    } catch (err) {
      toast.show({ tone: "error", message: message(err) });
      setBusy(null);
    }
  }

  return (
    <div className="mkc-stack">
      <Card header={<h2>{t("billing.current.title")}</h2>}>
        <dl className="mkc-kv">
          <dt>{t("billing.current.plan")}</dt>
          <dd>
            <Badge tone="accent">{planName(me.plan)}</Badge>
          </dd>
          <dt>{t("billing.current.status")}</dt>
          <dd>
            <Badge tone={statusTone(me.status)}>
              {has(`billing.status.${me.status}`) ? t(`billing.status.${me.status}`) : me.status}
            </Badge>
          </dd>
          {me.interval && (
            <>
              <dt>{t("billing.current.interval")}</dt>
              <dd>
                {has(`billing.interval.${me.interval}`)
                  ? t(`billing.interval.${me.interval}`)
                  : me.interval}
              </dd>
            </>
          )}
          {me.current_period_end && (
            <>
              <dt>
                {me.cancel_at_period_end
                  ? t("billing.current.endsOn")
                  : t("billing.current.renews")}
              </dt>
              <dd>{formatDate(me.current_period_end)}</dd>
            </>
          )}
        </dl>
        {me.cancel_at_period_end && <Banner tone="warn">{t("billing.current.cancelling")}</Banner>}
        {me.has_customer && (
          <Button variant="secondary" loading={busy === "portal"} onClick={portal}>
            {t("billing.manage")}
          </Button>
        )}
      </Card>

      <h2>{t("billing.plans.title")}</h2>
      <div className="mkc-grid">
        {plans.map((p) => (
          <PlanCard
            key={p.id}
            plan={p}
            current={p.id === me.plan}
            busy={busy === p.id}
            disabled={busy !== null}
            onUpgrade={() => upgrade(p.id)}
          />
        ))}
      </div>

      {usage && <UsagePanel usage={usage} />}
    </div>
  );
}

function PlanCard({
  plan,
  current,
  busy,
  disabled,
  onUpgrade,
}: {
  plan: Plan;
  current: boolean;
  busy: boolean;
  disabled: boolean;
  onUpgrade: () => void;
}) {
  const { t, n } = useI18n();
  const bytes = useBytes();
  const money = useMoney();
  return (
    <Card
      header={
        <div className="mkc-row mkc-row--between">
          <h3>{plan.name}</h3>
          {current && <Badge tone="success">{t("billing.plans.current")}</Badge>}
        </div>
      }
      footer={
        !current && plan.purchasable ? (
          <Button
            loading={busy}
            disabled={disabled}
            onClick={onUpgrade}
            aria-label={t("billing.plans.upgradeTo", { plan: plan.name })}
          >
            {t("billing.plans.upgrade")}
          </Button>
        ) : undefined
      }
    >
      <p className="mkc-price">
        {plan.price_cents > 0
          ? t("billing.plans.perMonth", { price: money(plan.price_cents) })
          : t("billing.plans.free")}
      </p>
      <ul className="mkc-features">
        <li>
          {plan.bandwidth_bytes_per_month > 0
            ? t("billing.plans.bandwidth", { amount: bytes(plan.bandwidth_bytes_per_month) })
            : t("billing.plans.bandwidthUnlimited")}
        </li>
        <li>{t("billing.plans.servers", { count: n(plan.max_servers) })}</li>
        <li>{t("billing.plans.streams", { count: n(plan.max_concurrent_streams) })}</li>
        {plan.includes_transcoding && <li>{t("billing.plans.transcoding")}</li>}
        {plan.family_seats > 0 && (
          <li>{t("billing.plans.seats", { count: n(plan.family_seats) })}</li>
        )}
        {(plan.home_tier === "home" || plan.home_tier === "pro") && (
          <li>{t(`billing.plans.homeTier.${plan.home_tier}`)}</li>
        )}
      </ul>
    </Card>
  );
}

function UsagePanel({ usage }: { usage: Usage }) {
  const { t } = useI18n();
  const bytes = useBytes();
  const limited = usage.limit_bytes > 0;
  const pct = limited ? (usage.used_bytes / usage.limit_bytes) * 100 : 0;
  return (
    <Card header={<h2>{t("billing.usage.title", { month: usage.month })}</h2>}>
      <p>
        {limited
          ? t("billing.usage.ofLimit", {
              used: bytes(usage.used_bytes),
              limit: bytes(usage.limit_bytes),
            })
          : t("billing.usage.unlimited", { used: bytes(usage.used_bytes) })}
      </p>
      {limited && <ProgressBar label={t("billing.usage.bar")} value={pct} />}
      {usage.servers.length > 0 && (
        <table className="mkc-table">
          <thead>
            <tr>
              <th scope="col">{t("billing.usage.server")}</th>
              <th scope="col">{t("billing.usage.in")}</th>
              <th scope="col">{t("billing.usage.out")}</th>
            </tr>
          </thead>
          <tbody>
            {usage.servers.map((s) => (
              <tr key={s.id}>
                <td>{s.name}</td>
                <td>{bytes(s.bytes_in)}</td>
                <td>{bytes(s.bytes_out)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </Card>
  );
}
