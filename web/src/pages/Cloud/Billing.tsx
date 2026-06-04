// License & entitlements (Epic 16 subscriptions surface).
//
// Real contracts:
//   GET    /api/entitlements    { tier, license_id?, seats?, features:{name:bool} }
//                               — readable by any authenticated principal
//   POST   /api/admin/license   body = full signed License JSON  (admin)
//                               — 204 on success, 400 invalid, 503 if the
//                                 build embeds no verifier key (disabled)
//   DELETE /api/admin/license   revert to free tier               (admin)
//
// There is NO GET /api/admin/license (keys are never echoed back), so the
// editor is a write-only paste box. The license editor is shown to admins
// only; the plan + entitlements cards are visible to everyone.
import { useEffect, useState } from "react";
import { Card } from "@ds/components/Card/Card";
import { Badge } from "@ds/components/Badge/Badge";
import { Button } from "@ds/components/Button/Button";
import { Textarea } from "@ds/components/Textarea/Textarea";
import { ErrorState } from "@ds/components/ErrorState/ErrorState";
import { useToast } from "@ds/components/Toast/Toast";
import { api, ApiError } from "../../lib/api";
import { useI18n } from "../../lib/i18n";
import { useAuth } from "../../lib/auth";

interface Entitlements {
  tier: string;
  license_id?: string;
  seats?: number;
  features: Record<string, boolean>;
}

export function Billing() {
  const { t, n } = useI18n();
  const { user } = useAuth();
  const toast = useToast();
  const [ent, setEnt] = useState<Entitlements | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [license, setLicense] = useState("");
  const [busy, setBusy] = useState(false);

  function load() {
    setError(null);
    api
      .get<Entitlements>("/api/entitlements")
      .then(setEnt)
      .catch((e) =>
        setError(e instanceof ApiError ? e.problem.detail || e.problem.title : t("common.error"))
      );
  }

  useEffect(load, []);

  async function applyLicense() {
    setBusy(true);
    try {
      // The endpoint expects the parsed License object; send the pasted
      // JSON verbatim so a malformed paste surfaces as a clean 400.
      const body = JSON.parse(license);
      await api.post("/api/admin/license", body);
      toast.show({ tone: "success", message: t("billing.license.applied") });
      setLicense("");
      load();
    } catch (e) {
      toast.show({ tone: "error", message: licenseError(e, t) });
    } finally {
      setBusy(false);
    }
  }

  async function revokeLicense() {
    setBusy(true);
    try {
      await api.delete("/api/admin/license");
      toast.show({ tone: "success", message: t("billing.license.revoked") });
      load();
    } catch (e) {
      toast.show({ tone: "error", message: licenseError(e, t) });
    } finally {
      setBusy(false);
    }
  }

  if (error) {
    return (
      <section className="mkt-page mkt-billing">
        <header className="mkt-page__header">
          <h1>{t("billing.title")}</h1>
        </header>
        <ErrorState
          kind="server"
          title={t("common.error")}
          description={error}
          action={
            <Button variant="secondary" onClick={load}>
              {t("common.retry")}
            </Button>
          }
        />
      </section>
    );
  }

  if (!ent) {
    return (
      <section className="mkt-page mkt-billing">
        <header className="mkt-page__header">
          <h1>{t("billing.title")}</h1>
        </header>
        <p className="mkt-loading">{t("common.loading")}</p>
      </section>
    );
  }

  const features = Object.entries(ent.features).sort(([a], [b]) => a.localeCompare(b));

  return (
    <section className="mkt-page mkt-billing">
      <header className="mkt-page__header">
        <h1>{t("billing.title")}</h1>
      </header>

      <Card className="mkt-plan-card" header={<strong>{t("billing.plan")}</strong>}>
        <div className="mkt-plan-card__tier">
          <Badge tone={ent.tier === "free" ? "neutral" : "accent"}>{ent.tier}</Badge>
        </div>
        <dl className="mkt-kv">
          {ent.seats != null && ent.seats > 0 && (
            <div>
              <dt className="mkt-muted">{t("billing.seats")}</dt>
              <dd>{n(ent.seats)}</dd>
            </div>
          )}
          {ent.license_id && (
            <div>
              <dt className="mkt-muted">{t("billing.licenseId")}</dt>
              <dd className="mkt-mono mkt-truncate">{ent.license_id}</dd>
            </div>
          )}
        </dl>
      </Card>

      <h2>{t("billing.entitlements")}</h2>
      <table className="mkt-table" aria-label={t("billing.entitlements")}>
        <thead>
          <tr>
            <th>{t("billing.feature")}</th>
            <th>{t("common.actions")}</th>
          </tr>
        </thead>
        <tbody>
          {features.map(([name, on]) => (
            <tr key={name}>
              <td className="mkt-mono">{name}</td>
              <td>
                <Badge tone={on ? "success" : "neutral"}>
                  {on ? t("billing.feature.on") : t("billing.feature.off")}
                </Badge>
              </td>
            </tr>
          ))}
        </tbody>
      </table>

      {user?.is_admin && (
        <>
          <h2>{t("billing.license")}</h2>
          <Card>
            <Textarea
              label={t("billing.license")}
              description={t("billing.license.desc")}
              placeholder={t("billing.license.placeholder")}
              value={license}
              onChange={(e) => setLicense(e.target.value)}
              rows={6}
              className="mkt-mono"
            />
            <div className="mkt-toolbar mkt-billing__actions">
              <Button onClick={applyLicense} loading={busy} disabled={!license.trim()}>
                {t("billing.license.apply")}
              </Button>
              <Button variant="destructive" onClick={revokeLicense} loading={busy}>
                {t("billing.license.revoke")}
              </Button>
            </div>
          </Card>
        </>
      )}
    </section>
  );
}

// licenseError maps the subscriptions endpoint's status codes onto
// localised copy: 503 means the build shipped no verifier key (feature
// off), 400 means the signed document was rejected.
function licenseError(e: unknown, t: (k: string) => string): string {
  if (e instanceof ApiError) {
    if (e.status === 503) return t("billing.license.disabled");
    if (e.status === 400) return e.problem.detail || t("billing.license.invalid");
    return e.problem.detail || e.problem.title;
  }
  if (e instanceof SyntaxError) return t("billing.license.invalid");
  return t("common.error");
}
