// Settings → Subscription (Epic 16 stories 16.1–16.4): the home's plan,
// its users against the plan's seats, the license, and Maktaba Cloud.
//
//   GET    /api/admin/license          the admin view (never the key)
//   POST   /api/admin/license          apply a pasted signed license → the view
//   DELETE /api/admin/license          back to Free
//   POST   /api/admin/license/refresh  ask the license server now → the view
//   GET    /api/entitlements           a viewer's read-only plan
//
// The API enforces every paid gate and every admin rule; this page only
// shows them. The pasted license is write-only: no answer carries it. A
// return to the page shows the last plan at once and reads it again quietly
// (lib/usePageData).
import { useState } from "react";
import { Card } from "@ds/components/Card/Card";
import { Badge } from "@ds/components/Badge/Badge";
import { Button } from "@ds/components/Button/Button";
import { Skeleton } from "@ds/components/Skeleton/Skeleton";
import { Textarea } from "@ds/components/Textarea/Textarea";
import { useToast } from "@ds/components/Toast/Toast";
import { api, ApiError } from "../../lib/api";
import { useI18n } from "../../lib/i18n";
import { useAuth } from "../../lib/auth";
import { usePageData } from "../../lib/usePageData";
import { forgetAnswers } from "../../lib/pageCache";
import { LinesPlaceholder, Loading } from "../../components/Placeholders";

// the kept answers' keys (lib/pageCache)
const ENTITLEMENTS_KEY = "account:entitlements";
const LICENSE_KEY = "account:license";

/** The paid features, in the order Settings lists them. */
export const FEATURES = [
  "cloud_relay",
  "multi_user",
  "cloud_metadata_backup",
  "advanced_analytics",
  "federation",
] as const;

export type Tier = "free" | "home" | "pro";
export type Status = "none" | "active" | "grace" | "expired" | "revoked";

export interface LicenseView {
  available: boolean;
  tier: Tier;
  features: Record<string, boolean>;
  /** -1: unlimited. */
  seats_allowed: number;
  seats_used?: number;
  over_seats: boolean;
  status: Status;
  license?: {
    license_id: string;
    tier: Tier;
    seats: number;
    holder?: string;
    issued_at: string;
    expires_at: string;
    source: "cloud" | "offline";
  };
  message?: string;
  last_refresh_at?: string;
  last_refresh_error?: string;
  cloud: { claimed: boolean; server_id?: string; slug?: string; cloud_api?: string };
  manage_url?: string;
}

interface Entitlements {
  tier: Tier;
  seats_allowed?: number;
  features?: Record<string, boolean> | null;
}

export function SubscriptionSettings() {
  const { user } = useAuth();
  return user?.is_admin ? <AdminSubscription /> : <ViewerSubscription />;
}

function tierLabel(t: (k: string) => string, tier: string | undefined) {
  return t(`subscription.tier.${tier === "home" || tier === "pro" ? tier : "free"}`);
}

function seatsText(
  t: (k: string, v?: Record<string, string | number>) => string,
  allowed: number,
  used?: number
) {
  const limit = allowed < 0 ? t("subscription.seats.unlimited") : String(allowed);
  return used == null ? limit : t("subscription.seats.of", { used, limit });
}

function FeatureList({ features }: { features: Record<string, boolean> }) {
  const { t } = useI18n();
  return (
    <ul className="mkt-subscription__features" aria-label={t("subscription.features")}>
      {FEATURES.map((f) => (
        <li key={f}>
          <Badge tone={features[f] ? "success" : "neutral"}>
            {features[f] ? t("subscription.feature.on") : t("subscription.feature.off")}
          </Badge>{" "}
          {t(`subscription.feature.${f}`)}
        </li>
      ))}
    </ul>
  );
}

async function loadEntitlements(): Promise<Entitlements> {
  const e = await api.get<Entitlements>("/api/entitlements");
  if (!e || typeof e !== "object") throw new Error("not entitlements");
  return e;
}

async function loadLicense(): Promise<LicenseView> {
  const v = await api.get<LicenseView>("/api/admin/license");
  if (!v || typeof v !== "object") throw new Error("not a license view");
  return v;
}

/** The plan, users and license cards while the first read is slow. */
function AdminPlaceholder() {
  return (
    <div className="mkt-settings__panel mkt-subscription" aria-hidden="true">
      {[4, 2, 3].map((lines, i) => (
        <Card key={i} header={<Skeleton variant="text" className="mkt-ph-line" width="8rem" />}>
          <LinesPlaceholder lines={lines} />
        </Card>
      ))}
    </div>
  );
}

function ViewerSubscription() {
  const { t } = useI18n();
  const { data: ent, err, loading } = usePageData<Entitlements>(ENTITLEMENTS_KEY, loadEntitlements);
  if (err != null)
    return (
      <p className="mkt-alert" role="alert">
        {t("common.error")}
      </p>
    );
  if (!ent)
    return (
      <Loading loading={loading}>
        <div className="mkt-settings__panel mkt-subscription">
          <LinesPlaceholder lines={4} />
        </div>
      </Loading>
    );
  return (
    <div className="mkt-settings__panel mkt-subscription">
      <h2>{t("subscription.plan")}</h2>
      <p>
        <Badge tone={ent.tier === "free" ? "neutral" : "accent"}>{tierLabel(t, ent.tier)}</Badge>
      </p>
      <FeatureList features={ent.features ?? {}} />
      <p className="mkt-muted">{t("subscription.viewerHint")}</p>
    </div>
  );
}

function problemText(e: unknown, t: (k: string) => string): string {
  if (e instanceof ApiError) return e.problem.detail || e.problem.title;
  if (e instanceof SyntaxError) return t("subscription.license.notJson");
  return t("common.error");
}

function AdminSubscription() {
  const { t, formatDate: d } = useI18n();
  const toast = useToast();
  const {
    data: view,
    err,
    loading,
    reload,
    setData: setView,
  } = usePageData<LicenseView>(LICENSE_KEY, loadLicense);
  const failed = err != null ? problemText(err, t) : null;
  const [license, setLicense] = useState("");
  const [busy, setBusy] = useState<"apply" | "remove" | "check" | null>(null);

  // the plan changed: a viewer's kept plan is stale, and so is this one
  // until the new view is in
  function planChanged(v?: LicenseView | null) {
    forgetAnswers(ENTITLEMENTS_KEY, LICENSE_KEY);
    if (v && typeof v === "object") setView(v);
    else reload();
  }

  async function apply() {
    setBusy("apply");
    try {
      const v = await api.post<LicenseView>("/api/admin/license", JSON.parse(license.trim()));
      setLicense("");
      planChanged(v);
      toast.show({ tone: "success", message: t("subscription.license.applied") });
    } catch (e) {
      toast.show({ tone: "error", message: problemText(e, t) });
    } finally {
      setBusy(null);
    }
  }

  async function remove() {
    if (!window.confirm(t("subscription.license.removeConfirm"))) return;
    setBusy("remove");
    try {
      await api.delete("/api/admin/license");
      toast.show({ tone: "success", message: t("subscription.license.removed") });
      planChanged();
    } catch (e) {
      toast.show({ tone: "error", message: problemText(e, t) });
    } finally {
      setBusy(null);
    }
  }

  async function check() {
    setBusy("check");
    try {
      const v = await api.post<LicenseView>("/api/admin/license/refresh", {});
      if (v && typeof v === "object") planChanged(v);
    } catch (e) {
      toast.show({ tone: "error", message: problemText(e, t) });
    } finally {
      setBusy(null);
    }
  }

  if (failed)
    return (
      <div className="mkt-settings__panel">
        <p className="mkt-alert" role="alert">
          {failed}
        </p>
        <Button variant="secondary" onClick={reload}>
          {t("common.retry")}
        </Button>
      </div>
    );
  if (!view)
    return (
      <Loading loading={loading}>
        <AdminPlaceholder />
      </Loading>
    );

  const lic = view.license;
  const statusTone =
    view.status === "active"
      ? "success"
      : view.status === "grace"
        ? "warn"
        : view.status === "none"
          ? "neutral"
          : "error";
  return (
    <div className="mkt-settings__panel mkt-subscription">
      {view.message && (
        <p className="mkt-alert" role="alert" data-testid="subscription-message">
          {view.message}
        </p>
      )}

      <Card header={<strong>{t("subscription.plan")}</strong>}>
        <p>
          <Badge tone={view.tier === "free" ? "neutral" : "accent"}>
            {tierLabel(t, view.tier)}
          </Badge>{" "}
          {view.status !== "none" && (
            <Badge tone={statusTone}>{t(`subscription.status.${view.status}`)}</Badge>
          )}
        </p>
        <dl className="mkt-kv">
          <div>
            <dt className="mkt-muted">{t("subscription.users")}</dt>
            <dd data-testid="subscription-seats">
              {seatsText(t, view.seats_allowed, view.seats_used)}
            </dd>
          </div>
          {lic?.holder && (
            <div>
              <dt className="mkt-muted">{t("subscription.holder")}</dt>
              <dd>{lic.holder}</dd>
            </div>
          )}
          {lic && (
            <div>
              <dt className="mkt-muted">
                {lic.source === "cloud" ? t("subscription.renews") : t("subscription.expires")}
              </dt>
              <dd>{d(lic.expires_at)}</dd>
            </div>
          )}
          {lic && (
            <div>
              <dt className="mkt-muted">{t("subscription.licenseId")}</dt>
              <dd className="mkt-mono mkt-truncate">{lic.license_id}</dd>
            </div>
          )}
        </dl>
        <FeatureList features={view.features} />
        {view.tier === "free" && <p className="mkt-muted">{t("subscription.freeHint")}</p>}
      </Card>

      <Card header={<strong>{t("subscription.cloud")}</strong>}>
        {view.cloud.claimed ? (
          <p>
            {t("subscription.cloud.claimed", {
              slug: view.cloud.slug ?? view.cloud.server_id ?? "",
            })}
          </p>
        ) : (
          <p className="mkt-muted">{t("subscription.cloud.unclaimed")}</p>
        )}
        {view.last_refresh_at && (
          <p className="mkt-muted">
            {t("subscription.lastChecked", { when: d(view.last_refresh_at) })}
          </p>
        )}
        {view.last_refresh_error && (
          <p className="mkt-muted" data-testid="subscription-refresh-error">
            {t("subscription.lastError", { error: view.last_refresh_error })}
          </p>
        )}
        <div className="mkt-toolbar">
          {view.manage_url && (
            <a className="mkt-link-btn" href={view.manage_url} target="_blank" rel="noreferrer">
              {view.tier === "free" ? t("subscription.getPlan") : t("subscription.manage")}
            </a>
          )}
          {view.available && (
            <Button
              variant="secondary"
              onClick={check}
              loading={busy === "check"}
              disabled={busy !== null}
            >
              {t("subscription.checkNow")}
            </Button>
          )}
        </div>
      </Card>

      <Card header={<strong>{t("subscription.license")}</strong>}>
        {view.available ? (
          <>
            <Textarea
              label={t("subscription.license.paste")}
              description={t("subscription.license.desc")}
              value={license}
              onChange={(e) => setLicense(e.target.value)}
              rows={5}
              className="mkt-mono"
              autoComplete="off"
              spellCheck={false}
            />
            <div className="mkt-toolbar">
              <Button
                onClick={apply}
                loading={busy === "apply"}
                disabled={!license.trim() || busy !== null}
              >
                {t("subscription.license.apply")}
              </Button>
              {lic && (
                <Button
                  variant="destructive"
                  onClick={remove}
                  loading={busy === "remove"}
                  disabled={busy !== null}
                >
                  {t("subscription.license.remove")}
                </Button>
              )}
            </div>
          </>
        ) : (
          <p className="mkt-muted">{t("subscription.license.unavailable")}</p>
        )}
      </Card>
    </div>
  );
}
