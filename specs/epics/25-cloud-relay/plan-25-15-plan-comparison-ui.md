# Implementation Plan — Story 25.15 Plan comparison & subscription UI

> Companion to [story-25-15-plan-comparison-ui.md](story-25-15-plan-comparison-ui.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Pages | `web/src/pages/Pricing.tsx` (anonymous marketing), `web/src/pages/Upgrade.tsx` (in-app). |
| Components | `PlanCard`, `CurrentUsage`, `BillingBanner`, `UpgradeDialog`. |
| Data source | `GET /api/billing/plans` (price + display) + `GET /api/billing/subscription` (current) + `GET /api/me/usage` (this-month GB) + `GET /api/servers` (count). |
| Plan toggle | Monthly ↔ Yearly local state; recompute "save $X/year" badge. |
| Translation | `react-i18next`-driven; bilingual EN + AR; RTL via `<html dir="rtl">` when locale=ar. |
| iOS / Android in-app | Suppressed CTAs when UA is iOS (App Store rule); deep-link to web. |
| Accessibility | WCAG AA contrast; keyboard navigation; pattern + color for "Recommended" badge. |
| Out of scope | Trial periods (deferred). Promo-code UI (Stripe handles in Checkout). |

## 1. Files

```
web/src/pages/Pricing.tsx
web/src/pages/Upgrade.tsx
web/src/pages/BillingSuccess.tsx
web/src/pages/BillingCanceled.tsx
web/src/components/billing/PlanCard.tsx
web/src/components/billing/CurrentUsage.tsx
web/src/components/billing/BillingBanner.tsx
web/src/components/billing/UpgradeDialog.tsx
web/src/lib/billing.ts
web/src/i18n/en/billing.json
web/src/i18n/ar/billing.json
```

## 2. Data fetching

```ts
// web/src/lib/billing.ts
export async function fetchPlans(): Promise<PlanInfo[]> { ... }
export async function fetchSubscription(): Promise<SubscriptionState> { ... }
export async function fetchUsage(from?: string, to?: string): Promise<UsageDay[]> { ... }
export async function checkout(plan: string): Promise<{ url: string }> { ... }
export async function portal(): Promise<{ url: string }> { ... }
export async function upgradePreview(plan: string): Promise<UpgradePreview> { ... }
```

`/pricing` (anonymous) needs only `fetchPlans`. `/upgrade` needs all four. Use `react-query` for caching/staleness; SWR pattern.

## 3. Pricing page

```tsx
export function Pricing() {
    const { t } = useTranslation("billing");
    const { data: plans } = useQuery(["plans"], fetchPlans, { staleTime: 5*60*1000 });
    const [yearly, setYearly] = useState(false);
    return (
        <Layout dir={t("dir")}>
            <h1>{t("hero_title")}</h1>
            <p>{t("hero_subtitle")}</p>
            <Toggle checked={yearly} onChange={setYearly} labels={[t("monthly"), t("yearly")]} />
            <Grid>
                <PlanCard key="free"   info={freeInfo} highlighted={false} cta={t("get_started")} />
                <PlanCard info={planFor("pro", yearly, plans)}    badge={t("recommended")} highlighted cta={t("upgrade_to_pro")} />
                <PlanCard info={planFor("family", yearly, plans)} cta={t("upgrade_to_family")} />
            </Grid>
            <FAQ />
        </Layout>
    );
}
```

`PlanCard` props: `info, highlighted, badge?, cta, onCta?`. Anonymous version simply links CTA to `/auth/register?next=/upgrade?plan=pro_monthly`.

## 4. Upgrade page

```tsx
export function Upgrade() {
    const sub  = useQuery(["sub"], fetchSubscription);
    const usage= useQuery(["usage"], () => fetchUsage());
    const plans= useQuery(["plans"], fetchPlans);
    const ua   = navigator.userAgent;
    const onIOSApp = /Maktaba-iOS-App/.test(ua) || /iPhone|iPad/.test(ua);

    return (
        <Layout>
            {sub.data?.status === "past_due" && <BillingBanner kind="past_due" />}
            {sub.data?.status === "canceled" && <BillingBanner kind="canceled" />}
            <CurrentUsage usage={usage.data} sub={sub.data}/>
            <Grid>
                {(["free","pro","family"] as const).map(p => (
                  <PlanCard key={p} info={planFor(p)}
                    highlighted={sub.data?.plan?.startsWith(p)}
                    cta={ctaFor(p, sub.data, onIOSApp)}
                    onCta={()=>handleCta(p, sub.data, onIOSApp)} />
                ))}
            </Grid>
        </Layout>
    );
}

async function handleCta(plan, sub, onIOSApp) {
    if (onIOSApp) {
        window.open("https://maktaba.app/manage-on-web", "_blank");
        return;
    }
    if (sub.plan === "free") {
        const r = await checkout(plan + "_" + interval);
        window.location.href = r.url;
        return;
    }
    // Upgrading within paid tiers → portal + preview
    const preview = await upgradePreview(plan + "_" + interval);
    const ok = await dialogConfirm(<UpgradeDialog preview={preview} plan={plan}/>);
    if (!ok) return;
    const { url } = await portal();
    window.location.href = url;
}
```

## 5. Billing success / cancel

```tsx
export function BillingSuccess() {
    const params = new URLSearchParams(location.search);
    const sessionID = params.get("session_id");
    const [poll, setPoll] = useState({tries:0});
    useEffect(() => {
        const id = setInterval(async () => {
            const s = await fetchSubscription();
            if (s.status === "active") { clearInterval(id); navigate("/dashboard"); }
            setPoll(p => ({tries: p.tries+1}));
            if (poll.tries > 30) { clearInterval(id); navigate("/upgrade?reason=webhook_lag"); }
        }, 1000);
        return () => clearInterval(id);
    }, []);
    return <p>{t("activating")}</p>;
}
```

## 6. i18n strings (excerpt)

```jsonc
// en/billing.json
{
  "hero_title": "Your library, anywhere",
  "hero_subtitle": "Free LAN. Pro + Family for remote streaming.",
  "monthly": "Monthly", "yearly": "Yearly",
  "recommended": "Recommended",
  "upgrade_to_pro": "Upgrade to Pro",
  "upgrade_to_family": "Upgrade to Family",
  "dir": "ltr"
}
```

```jsonc
// ar/billing.json
{
  "hero_title": "مكتبتك في كل مكان",
  "hero_subtitle": "مجاناً على الشبكة المحلية. للوصول من أي مكان اشترك في Pro أو Family.",
  "monthly": "شهرياً", "yearly": "سنوياً",
  "recommended": "موصى به",
  "upgrade_to_pro": "ترقية إلى Pro",
  "upgrade_to_family": "ترقية إلى Family",
  "dir": "rtl"
}
```

## 7. Marketing-page caching

Pricing page is server-side rendered (or pre-rendered at build) and the resulting HTML is cached at Cloudflare 5 min. Prices come from the static JSON at build time and fall back to live JSON in case of cache-miss. Pre-warm cache on deploy via `curl -s -o /dev/null https://maktaba.app/pricing` post-deploy.

## 8. Accessibility

- Plan cards are `<article role="region" aria-labelledby="...">` with `data-recommended` attribute.
- Keyboard: tab cycles through cards in document order; Enter activates CTA; Esc closes dialogs.
- "Recommended" badge has both color + ★ glyph; text says "Recommended" to screen readers.
- Yearly savings updates `aria-live="polite"` so SR users hear "Save $20.88 per year" on toggle.
- Contrast: 4.5:1 minimum verified via lint rule (`eslint-plugin-jsx-a11y` + `axe` test).

## 9. Test plan

### 9.1 Snapshot

| Test | Pins |
|---|---|
| `PricingFreeUser.snap` | Three cards, Free unhighlighted. |
| `UpgradeProUser.snap` | Pro highlighted, "Switch to Family" CTA. |
| `UpgradePastDue.snap` | Red banner above cards. |
| `UpgradeIOSWeb.snap` | "Manage on web" CTA on iOS UA. |

### 9.2 Behaviour

| Test | Pins |
|---|---|
| `TestToggleMonthlyYearlyUpdatesPrices` | Yearly toggle changes price + savings line. |
| `TestUpgradeRedirectToStripe` | Click → `checkout()` → `window.location.href` set. |
| `TestUpgradePreviewDialog` | Switch Pro→Family shows prorated charge. |
| `TestPollUntilWebhookFires` | `/billing/success` polls 1Hz, lands at dashboard when status=active. |
| `TestApplyPromoCode` | Discount line visible (Stripe-side; UI shows code accepted). |

### 9.3 Accessibility / i18n

| Test | Pins |
|---|---|
| `TestKeyboardFocusOrder` | Tab cycles plan cards. |
| `TestHighContrast` | Axe scan: no AA violations. |
| `TestRTLArabicRender` | `<html dir="rtl">`; numbers display per locale. |
| `TestSavingsAriaLive` | Toggle yearly → aria-live announcement triggers. |

## 10. Edge cases — handling table

| Case | Behaviour | Pinned |
|---|---|---|
| Currency confusion | Display billed-in-USD hint when locale ≠ en-US. | UX note. |
| Mid-cycle plan change | Stripe handles proration; we show upcoming-invoice diff. | `TestUpgradePreviewDialog`. |
| Trial | Out for v1. | Spec. |
| Expired free tier | Free never expires. | Spec. |
| /pricing CF cache | 5min TTL; pre-warm on deploy. | Doc. |
| No PII in /pricing | Only static plan data. | Code review. |
| Server-rendered prices | Build-time JSON + request-time fallback. | Build pipeline. |
| Anti-fingerprinting | No third-party trackers. | Code review. |
| Webhook lag at success | Poll until 30s, then offer "try again". | `TestPollUntilWebhookFires`. |
| iOS app users see CTA | Suppressed; deep-link instead. | `UpgradeIOSWeb.snap`. |
| Past-due | Red banner; CTA "Update payment method" → portal. | `UpgradePastDue.snap`. |

## 11. Dependencies

- 25.13 (`/api/billing/checkout`, `/api/billing/portal`, `/api/billing/plans`, `/api/billing/upgrade-preview`).
- 25.14 (status reflects webhook updates).
- 25.5 (current usage GB).
- 25.16 (servers list — `GET /api/servers`).
- Existing web app shell from Epic 11.

## 12. Acceptance checklist

- [ ] `/pricing` renders anonymously; CDN-cached 5min.
- [ ] `/upgrade` shows current plan + usage + cards.
- [ ] Toggle monthly/yearly with announced savings.
- [ ] iOS in-app suppresses CTAs.
- [ ] Past-due banner present when status=past_due.
- [ ] Bilingual EN + AR with RTL.
- [ ] WCAG AA.
- [ ] Tests in §9 pass.
