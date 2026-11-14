// Billing (Epic 50 story 50.20): Pro's guest sharing and the Broadcaster add-on, per home. Plans
// are per home, so each home the account owns gets its own panel: this month's viewer-hours
// against the allowance, the mode new viewers get, the viewer limit, the Broadcaster agreement,
// the add-on, and its pay-as-you-go with the owner's monthly limit.
//
//   GET   /v1/homes                         the account's homes (owned ones only here)
//   GET   /v1/billing/broadcast?server_id=  { tiers, state, payg, extras, agreement, enabled, purchasable }
//   POST  /v1/broadcast/agreement           { version }
//   PUT   /v1/billing/broadcast             { server_id, enabled }: 202, the provider then confirms
//   PATCH /v1/billing/broadcast/payg        { server_id, enabled, cap_cents }
import { useState } from "react";
import { Badge } from "@ds/components/Badge/Badge";
import { Button } from "@ds/components/Button/Button";
import { Card } from "@ds/components/Card/Card";
import { Input } from "@ds/components/Input/Input";
import { ProgressBar } from "@ds/components/ProgressBar/ProgressBar";
import { useToast } from "@ds/components/Toast/Toast";
import { api } from "../shared/api";
import { useI18n } from "../shared/i18n";
import { Banner, useErrorMessage, useLoad, useMoney } from "../shared/ui";

export interface BroadcastTier {
  id: "guest" | "broadcaster";
  max_channels: number;
  max_concurrent_viewers: number;
  viewer_hours_per_month: number;
  max_rungs: number;
  pay_as_you_go: boolean;
  /** null: the owner hasn't set it yet */
  price_cents: number | null;
  payg_price_per_100h_cents: number | null;
}

export interface BroadcastCatalog {
  guest: BroadcastTier;
  broadcaster: BroadcastTier;
}

interface PublisherState {
  plan: "" | "guest" | "broadcaster";
  mode: "full" | "lowest" | "wait" | "off";
  max_channels: number;
  max_concurrent_viewers: number;
  usage: { minutes_full: number; minutes_lowest: number; minutes_payg: number };
  allowance_minutes: number;
  payg: { on: boolean; cap_cents: number; spent_cents: number };
}

interface BroadcastView {
  server_id: string;
  state: PublisherState;
  payg: { enabled: boolean; cap_cents: number };
  agreement: { version: number; accepted: boolean };
  enabled: boolean;
  purchasable: boolean;
  tiers: BroadcastCatalog;
}

interface Home {
  server_id: string;
  name: string;
  owned: boolean;
}

/** The Broadcaster add-on in the plan comparison, and guest sharing's line for Pro. */
export function BroadcastOffer({ catalog }: { catalog: BroadcastCatalog }) {
  const { t, n } = useI18n();
  const money = useMoney();
  const b = catalog.broadcaster;
  const g = catalog.guest;
  return (
    <Card header={<h3>{t("billing.plan.broadcaster")}</h3>}>
      <p className="mkc-price">
        {b.price_cents == null
          ? t("billing.plan.priceTbd")
          : t("billing.plans.perMonth", { price: money(b.price_cents) })}
      </p>
      <p>
        {t("billing.plan.broadcaster.desc", {
          channels: n(b.max_channels),
          viewers: n(b.max_concurrent_viewers),
          hours: n(b.viewer_hours_per_month),
        })}
      </p>
      <ul className="mkc-features">
        <li>{t("billing.broadcast.addOnNeedsPro")}</li>
        <li>
          {t("billing.plan.pro.guestSharing", {
            viewers: n(g.max_concurrent_viewers),
            hours: n(g.viewer_hours_per_month),
          })}
        </li>
      </ul>
    </Card>
  );
}

/** Each owned home's broadcasting (Pro only: Free and Basic see that it needs Pro). */
export function BroadcastHomes({ pro }: { pro: boolean }) {
  const { t } = useI18n();
  const homes = useLoad(
    () => api.get<{ homes: Home[] }>("/v1/homes").then((r) => r.homes.filter((h) => h.owned)),
    "billing-broadcast-homes"
  );
  if (!pro) {
    return (
      <Card header={<h2>{t("billing.broadcast.section")}</h2>}>
        <p>{t("broadcast.needsPro")}</p>
      </Card>
    );
  }
  if (!homes.data || homes.data.length === 0) return null;
  return (
    <>
      {homes.data.map((h) => (
        <BroadcastHome key={h.server_id} home={h} />
      ))}
    </>
  );
}

function hours(minutes: number): number {
  return Math.floor(minutes / 60);
}

function BroadcastHome({ home }: { home: Home }) {
  const { t, n } = useI18n();
  const money = useMoney();
  const message = useErrorMessage();
  const toast = useToast();
  const data = useLoad(
    () =>
      api.get<BroadcastView>(
        `/v1/billing/broadcast?server_id=${encodeURIComponent(home.server_id)}`
      ),
    `billing-broadcast-${home.server_id}`
  );
  const [busy, setBusy] = useState(false);
  const [cap, setCap] = useState<string | null>(null);
  const v = data.data;
  if (!v) return null;
  const paygPrice = v.tiers?.broadcaster.payg_price_per_100h_cents ?? null;
  const st = v.state;
  const used = hours(st.usage.minutes_full);
  const allowance = hours(st.allowance_minutes);
  const pct = allowance > 0 ? Math.min(100, (used / allowance) * 100) : 0;
  const capText = cap ?? (v.payg.cap_cents > 0 ? String(v.payg.cap_cents / 100) : "");

  async function run(fn: () => Promise<unknown>) {
    setBusy(true);
    try {
      await fn();
      setCap(null);
      data.reload();
    } catch (err) {
      toast.show({ tone: "error", message: message(err) });
    } finally {
      setBusy(false);
    }
  }

  function savePayg(enabled: boolean) {
    const cents = Math.round(Number(capText) * 100);
    if (enabled && (!Number.isFinite(cents) || cents <= 0)) {
      toast.show({ tone: "error", message: t("billing.payg.capRequired") });
      return;
    }
    void run(() =>
      api.patch("/v1/billing/broadcast/payg", {
        server_id: home.server_id,
        enabled,
        cap_cents: Number.isFinite(cents) && cents > 0 ? cents : 0,
      })
    );
  }

  const planLabel =
    st.plan === "broadcaster"
      ? t("billing.plan.broadcaster")
      : st.plan === "guest"
        ? t("billing.broadcast.guest")
        : t("billing.broadcast.none");
  return (
    <Card
      header={
        <div className="mkc-row mkc-row--between">
          <h2>{t("billing.broadcast.title", { home: home.name })}</h2>
          <Badge tone="accent">{planLabel}</Badge>
        </div>
      }
    >
      {!v.enabled && <Banner tone="info">{t("billing.broadcast.notOpen")}</Banner>}
      {st.mode === "lowest" && <Banner tone="warn">{t("broadcast.mode.lowest")}</Banner>}
      {st.mode === "wait" && <Banner tone="warn">{t("broadcast.mode.wait")}</Banner>}
      <h3>{t("broadcast.usage.title")}</h3>
      <p>{t("broadcast.usage.hours", { used: n(used), allowance: n(allowance) })}</p>
      <ProgressBar label={t("broadcast.usage.title")} value={pct} />
      <ul className="mkc-features" data-testid="broadcast-limits">
        <li>{t("billing.broadcast.channels", { count: n(st.max_channels) })}</li>
        <li>{t("billing.broadcast.viewers", { count: n(st.max_concurrent_viewers) })}</li>
      </ul>

      {!v.agreement.accepted ? (
        <section aria-labelledby={`agreement-${home.server_id}`}>
          <h3 id={`agreement-${home.server_id}`}>{t("broadcast.agreement.title")}</h3>
          <p>{t("broadcast.agreement.body")}</p>
          <Button
            variant="secondary"
            loading={busy}
            onClick={() =>
              void run(() => api.post("/v1/broadcast/agreement", { version: v.agreement.version }))
            }
          >
            {t("billing.broadcast.accept")}
          </Button>
        </section>
      ) : st.plan === "guest" && v.purchasable ? (
        <Button
          loading={busy}
          onClick={() =>
            void run(() =>
              api.put("/v1/billing/broadcast", { server_id: home.server_id, enabled: true })
            )
          }
        >
          {t("billing.broadcast.add")}
        </Button>
      ) : null}

      {st.plan === "broadcaster" && (
        <>
          <h3>{t("billing.payg.title")}</h3>
          {paygPrice != null && (
            <p>{t("billing.broadcast.payg.desc", { price: money(paygPrice) })}</p>
          )}
          {st.payg.on && st.payg.cap_cents > 0 && (
            <p>
              {t("billing.payg.spent", {
                spent: money(st.payg.spent_cents),
                cap: money(st.payg.cap_cents),
              })}
            </p>
          )}
          <Input
            label={t("billing.payg.cap")}
            inputMode="decimal"
            value={capText}
            disabled={busy}
            onChange={(e) => setCap(e.currentTarget.value)}
          />
          <div className="mkc-row">
            <Button loading={busy} onClick={() => savePayg(true)}>
              {v.payg.enabled ? t("billing.payg.save") : t("billing.payg.turnOn")}
            </Button>
            {v.payg.enabled && (
              <Button variant="secondary" disabled={busy} onClick={() => savePayg(false)}>
                {t("billing.payg.turnOff")}
              </Button>
            )}
            <Button
              variant="secondary"
              disabled={busy}
              onClick={() =>
                void run(() =>
                  api.put("/v1/billing/broadcast", { server_id: home.server_id, enabled: false })
                )
              }
            >
              {t("billing.broadcast.remove")}
            </Button>
          </div>
        </>
      )}
    </Card>
  );
}
