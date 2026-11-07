// Billing (Epic 50 story 50.19 §4–§6): this month's relayed use by quality, and Pro's
// pay-as-you-go with its monthly limit and extra streams.
//
//   GET /v1/billing/usage           { …, by_mode: { full, payg, capped, capped_slow } }
//   GET /v1/billing/payg            { enabled, cap_cents, spent_cents, price_per_gb_cents|null }
//   PATCH /v1/billing/payg          { enabled, cap_cents }: 422 cap_required, 409 needs_pro
//   PATCH /v1/billing/extra-streams   { count }: 202, the provider then confirms
import { useState, type FormEvent } from "react";
import { Button } from "@ds/components/Button/Button";
import { Card } from "@ds/components/Card/Card";
import { Input } from "@ds/components/Input/Input";
import { ProgressBar } from "@ds/components/ProgressBar/ProgressBar";
import { useToast } from "@ds/components/Toast/Toast";
import { api } from "../api";
import { useI18n } from "../i18n";
import { Banner, useBytes, useErrorMessage, useLoad, useMoney } from "../ui";
import type { Plan } from "./Billing";

export interface UsageByMode {
  full: number;
  payg: number;
  capped: number;
  capped_slow: number;
}

/** This month's relayed bytes by the quality they played at. */
export function UsageByModePanel({ by }: { by: UsageByMode }) {
  const { t } = useI18n();
  const bytes = useBytes();
  const rows: [string, number][] = [
    ["billing.usage.mode.full", by.full + by.payg],
    ["billing.usage.mode.capped", by.capped],
    ["billing.usage.mode.cappedSlow", by.capped_slow],
  ];
  if (rows.every(([, v]) => v === 0)) return null;
  return (
    <ul className="mkc-features" data-testid="usage-by-mode">
      {rows
        .filter(([, v]) => v > 0)
        .map(([k, v]) => (
          <li key={k}>
            {t(k)}: {bytes(v)}
          </li>
        ))}
    </ul>
  );
}

interface Payg {
  enabled: boolean;
  cap_cents: number;
  spent_cents: number;
  price_per_gb_cents: number | null;
}

/** Pro's pay-as-you-go: off unless turned on, with a monthly limit; and extra streams. */
export function ProExtras({ pro }: { pro: Plan }) {
  const { t, n } = useI18n();
  const money = useMoney();
  const message = useErrorMessage();
  const toast = useToast();
  const data = useLoad(() => api.get<Payg>("/v1/billing/payg"), "billing-payg");
  const [cap, setCap] = useState<string | null>(null);
  const [streams, setStreams] = useState("");
  const [busy, setBusy] = useState(false);
  const p = data.data;
  if (!p || p.price_per_gb_cents == null) return null;
  const capText = cap ?? (p.cap_cents > 0 ? String(p.cap_cents / 100) : "");

  async function save(enabled: boolean) {
    const cents = Math.round(Number(capText) * 100);
    if (enabled && (!Number.isFinite(cents) || cents <= 0)) {
      toast.show({ tone: "error", message: t("billing.payg.capRequired") });
      return;
    }
    setBusy(true);
    try {
      await api.patch("/v1/billing/payg", {
        enabled,
        cap_cents: Number.isFinite(cents) && cents > 0 ? cents : 0,
      });
      setCap(null);
      data.reload();
    } catch (err) {
      toast.show({ tone: "error", message: message(err) });
    } finally {
      setBusy(false);
    }
  }

  async function buyStreams(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    try {
      await api.patch("/v1/billing/extra-streams", { count: Number(streams) });
      setStreams("");
    } catch (err) {
      toast.show({ tone: "error", message: message(err) });
    } finally {
      setBusy(false);
    }
  }

  const pct = p.cap_cents > 0 ? Math.min(100, (p.spent_cents / p.cap_cents) * 100) : 0;
  return (
    <>
      <Card header={<h2>{t("billing.payg.title")}</h2>}>
        <p>{t("billing.payg.desc", { price: money(p.price_per_gb_cents) })}</p>
        {p.enabled && p.spent_cents >= p.cap_cents && (
          <Banner tone="warn">{t("payg.capReached")}</Banner>
        )}
        {p.enabled && p.spent_cents < p.cap_cents && pct >= 80 && (
          <Banner tone="info">{t("payg.nearCap")}</Banner>
        )}
        <Input
          label={t("billing.payg.cap")}
          inputMode="decimal"
          value={capText}
          disabled={busy}
          onChange={(e) => setCap(e.currentTarget.value)}
        />
        {p.enabled && (
          <>
            <p>
              {t("billing.payg.spent", { spent: money(p.spent_cents), cap: money(p.cap_cents) })}
            </p>
            <ProgressBar label={t("billing.payg.title")} value={pct} />
          </>
        )}
        <div className="mkc-row">
          <Button loading={busy} onClick={() => void save(true)}>
            {p.enabled ? t("billing.payg.save") : t("billing.payg.turnOn")}
          </Button>
          {p.enabled && (
            <Button variant="secondary" disabled={busy} onClick={() => void save(false)}>
              {t("billing.payg.turnOff")}
            </Button>
          )}
        </div>
      </Card>
      {pro.extra_streams &&
        pro.max_relay_streams != null &&
        pro.extra_stream_price_cents != null && (
          <Card header={<h2>{t("billing.extraStreams.title")}</h2>}>
            <p>
              {t("billing.extraStreams.desc", {
                streams: n(pro.max_relay_streams),
                price: money(pro.extra_stream_price_cents),
              })}
            </p>
            <form className="mkc-row" onSubmit={(e) => void buyStreams(e)}>
              <Input
                label={t("billing.extraStreams.title")}
                inputMode="numeric"
                value={streams}
                disabled={busy}
                onChange={(e) => setStreams(e.currentTarget.value)}
              />
              <Button type="submit" variant="secondary" disabled={busy || streams === ""}>
                {t("billing.payg.save")}
              </Button>
            </form>
          </Card>
        )}
    </>
  );
}
