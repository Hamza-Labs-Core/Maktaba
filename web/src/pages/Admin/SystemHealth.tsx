// System health dashboard (Story 21.4 aggregator + 7.20 version + perf
// admin surface).
//
// Real contracts:
//   GET /api/system/health  { status, services:{name:{status,checks?,reason?}},
//                             disk_free_bytes?, queue_depth?, transcribe_budget_usd_left? }
//   GET /api/system/version { version, build_sha, build_time, go_version, schema_revision }
//   GET /api/admin/caches   { caches: [name] }                 (admin)
//   GET /api/admin/perf/budgets  — the parsed budget bundle    (admin)
//
// NOTE: the budgets payload is YAML-tagged Go structs serialised by
// encoding/json, so its keys arrive PascalCase (Endpoints, Version);
// the nil case returns {"endpoints":[]}. We read both casings.
// The health probe reports free disk bytes only (no total), so there is
// no honest "% used" bar — we show the free figure as a stat instead.
// Auto-refreshes every 30s.
import { useCallback, useEffect, useState } from "react";
import { Card } from "@ds/components/Card/Card";
import { Badge } from "@ds/components/Badge/Badge";
import { useI18n } from "../../lib/i18n";
import { api } from "../../lib/api";
import { AdminGate } from "../../components/AdminGate";

interface ServiceSnapshot {
  status: string;
  checks?: Record<string, { status: string; reason?: string }>;
  reason?: string;
}
interface Health {
  status: string;
  services: Record<string, ServiceSnapshot>;
  disk_free_bytes?: number;
  queue_depth?: number;
  transcribe_budget_usd_left?: string;
}
interface Version {
  version: string;
  build_sha: string;
  build_time: string;
  go_version: string;
  schema_revision: number;
}

const REFRESH_MS = 30_000;

export function SystemHealth() {
  return (
    <AdminGate>
      <HealthInner />
    </AdminGate>
  );
}

function HealthInner() {
  const { t, n, formatDate } = useI18n();
  const [health, setHealth] = useState<Health | null>(null);
  const [healthErr, setHealthErr] = useState(false);
  const [version, setVersion] = useState<Version | null>(null);
  const [caches, setCaches] = useState<string[] | null>(null);
  const [budgets, setBudgets] = useState<Record<string, unknown> | null>(null);

  const refresh = useCallback(() => {
    api
      .get<Health>("/api/system/health")
      .then((h) => {
        setHealth(h);
        setHealthErr(false);
      })
      .catch(() => setHealthErr(true));
    api
      .get<Version>("/api/system/version")
      .then(setVersion)
      .catch(() => setVersion(null));
    api
      .get<{ caches: string[] }>("/api/admin/caches")
      .then((r) => setCaches(r.caches ?? []))
      .catch(() => setCaches(null));
    api
      .get<Record<string, unknown>>("/api/admin/perf/budgets")
      .then(setBudgets)
      .catch(() => setBudgets(null));
  }, []);

  useEffect(() => {
    refresh();
    const id = setInterval(refresh, REFRESH_MS);
    return () => clearInterval(id);
  }, [refresh]);

  return (
    <section className="mkt-page mkt-health">
      <header className="mkt-page__header">
        <h1>{t("health.title")}</h1>
        <span className="mkt-muted">{t("health.autoRefresh")}</span>
      </header>

      {/* Overall + key stats */}
      <div className="mkt-stage-cards">
        <Card className="mkt-stat">
          <strong>{t("health.overall")}</strong>
          {healthErr ? (
            <StatusBadge status="down" t={t} />
          ) : (
            <StatusBadge status={health?.status ?? "unknown"} t={t} />
          )}
        </Card>
        <Card className="mkt-stat">
          <strong>{t("health.disk")}</strong>
          <span>{health?.disk_free_bytes != null ? humanBytes(health.disk_free_bytes) : "—"}</span>
        </Card>
        <Card className="mkt-stat">
          <strong>{t("health.queue")}</strong>
          <span>{health?.queue_depth != null ? n(health.queue_depth) : "—"}</span>
        </Card>
        {health?.transcribe_budget_usd_left && (
          <Card className="mkt-stat">
            <strong>{t("health.budget")}</strong>
            <span>${health.transcribe_budget_usd_left}</span>
          </Card>
        )}
      </div>

      {/* Per-service cards */}
      <h2>{t("health.services")}</h2>
      {healthErr ? (
        <p className="mkt-muted">{t("health.section.error")}</p>
      ) : !health ? (
        <p className="mkt-loading">{t("common.loading")}</p>
      ) : (
        <div className="mkt-stage-cards">
          {Object.entries(health.services).map(([name, svc]) => (
            <Card key={name} className="mkt-service-card">
              <div className="mkt-service-card__head">
                <strong>{name}</strong>
                <StatusBadge status={svc.status} t={t} />
              </div>
              {svc.reason && <div className="mkt-muted">{svc.reason}</div>}
              {svc.checks &&
                Object.entries(svc.checks).map(([cn, c]) => (
                  <div key={cn} className="mkt-service-card__check">
                    <span className="mkt-muted">{cn}</span>
                    <StatusBadge status={c.status} t={t} small />
                  </div>
                ))}
            </Card>
          ))}
        </div>
      )}

      {/* Caches + budgets + version */}
      <div className="mkt-health__grid">
        <Card header={<strong>{t("health.caches")}</strong>}>
          {caches === null ? (
            <p className="mkt-muted">{t("health.section.error")}</p>
          ) : caches.length === 0 ? (
            <p className="mkt-muted">{t("health.caches.empty")}</p>
          ) : (
            <ul className="mkt-kv">
              {caches.map((c) => (
                <li key={c} className="mkt-mono">
                  {c}
                </li>
              ))}
            </ul>
          )}
        </Card>

        <Card header={<strong>{t("health.budgets")}</strong>}>
          {budgets === null ? (
            <p className="mkt-muted">{t("health.section.error")}</p>
          ) : (
            <ul className="mkt-kv">
              <li>
                <span className="mkt-muted">{t("health.budgets.version")}</span>
                <span>{n(budgetVersion(budgets))}</span>
              </li>
              <li>
                <span className="mkt-muted">{t("health.budgets.endpoints")}</span>
                <span>{n(endpointCount(budgets))}</span>
              </li>
            </ul>
          )}
        </Card>

        <Card header={<strong>{t("health.version")}</strong>}>
          {version === null ? (
            <p className="mkt-muted">{t("health.section.error")}</p>
          ) : (
            <ul className="mkt-kv">
              <li>
                <span className="mkt-muted">{t("health.version")}</span>
                <span className="mkt-mono">{version.version}</span>
              </li>
              <li>
                <span className="mkt-muted">{t("health.version.build")}</span>
                <span className="mkt-mono">{(version.build_sha || "").slice(0, 12) || "—"}</span>
              </li>
              <li>
                <span className="mkt-muted">{t("health.version.buildTime")}</span>
                <span>{version.build_time ? formatDate(version.build_time) : "—"}</span>
              </li>
              <li>
                <span className="mkt-muted">{t("health.version.go")}</span>
                <span className="mkt-mono">{version.go_version}</span>
              </li>
              <li>
                <span className="mkt-muted">{t("health.version.schema")}</span>
                <span>{n(version.schema_revision)}</span>
              </li>
            </ul>
          )}
        </Card>
      </div>
    </section>
  );
}

function StatusBadge({
  status,
  t,
  small,
}: {
  status: string;
  t: (k: string) => string;
  small?: boolean;
}) {
  const s = status.toLowerCase();
  const up = s === "ok" || s === "up" || s === "healthy" || s === "ready" || s === "pass";
  const down = s === "down" || s === "fail" || s === "error" || s === "unhealthy";
  const tone = up ? "success" : down ? "error" : "warn";
  const label = up
    ? t("health.status.up")
    : down
      ? t("health.status.down")
      : t("health.status.degraded");
  return (
    <Badge tone={tone} className={small ? "mkt-badge--sm" : undefined}>
      {label}
    </Badge>
  );
}

function humanBytes(bytes: number): string {
  if (bytes <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB", "PB"];
  const i = Math.min(units.length - 1, Math.floor(Math.log(bytes) / Math.log(1024)));
  return `${(bytes / Math.pow(1024, i)).toFixed(i === 0 ? 0 : 1)} ${units[i]}`;
}

// The budgets payload may arrive as {Version, Endpoints} (PascalCase Go
// fields) or the nil-case {endpoints: []}. Read defensively.
function budgetVersion(b: Record<string, unknown>): number {
  return Number(b.Version ?? b.version ?? 0) || 0;
}
function endpointCount(b: Record<string, unknown>): number {
  const eps = (b.Endpoints ?? b.endpoints) as unknown;
  if (Array.isArray(eps)) return eps.length;
  if (eps && typeof eps === "object") return Object.keys(eps).length;
  return 0;
}
