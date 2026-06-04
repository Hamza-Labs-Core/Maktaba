// Maktaba Cloud relay status (web-pages-batch2).
//
// There is no dedicated cloud-relay API surface in this build, so this
// page reports honestly off what the server already exposes:
//   GET /api/system/health   { status, services:{name:{status,checks?,reason?}} }
//   GET /api/system/version  { version, build_sha, ... }
//
// The cloud relay, when configured, registers itself as a peer service
// in the health aggregator (a service whose name contains "cloud" or
// "relay"). When no such service is present we show setup instructions
// rather than fabricating a connected state (same no-false-green
// convention as Admin/Users + Settings).
import { useCallback, useEffect, useState } from "react";
import { Card } from "@ds/components/Card/Card";
import { Badge } from "@ds/components/Badge/Badge";
import { EmptyState } from "@ds/components/EmptyState/EmptyState";
import { useI18n } from "../../lib/i18n";
import { api } from "../../lib/api";

interface ServiceSnapshot {
  status: string;
  checks?: Record<string, { status: string; reason?: string }>;
  reason?: string;
}
interface Health {
  status: string;
  services: Record<string, ServiceSnapshot>;
}
interface Version {
  version: string;
  build_sha: string;
}

const REFRESH_MS = 30_000;

function findCloudService(
  services: Record<string, ServiceSnapshot>
): [string, ServiceSnapshot] | null {
  for (const [name, svc] of Object.entries(services)) {
    if (/cloud|relay|tunnel/i.test(name)) return [name, svc];
  }
  return null;
}

export function ServerStatus() {
  const { t } = useI18n();
  const [health, setHealth] = useState<Health | null>(null);
  const [version, setVersion] = useState<Version | null>(null);
  const [healthErr, setHealthErr] = useState(false);

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
  }, []);

  useEffect(() => {
    refresh();
    const id = setInterval(refresh, REFRESH_MS);
    return () => clearInterval(id);
  }, [refresh]);

  const cloud = health ? findCloudService(health.services) : null;
  const configured = cloud !== null;

  return (
    <section className="mkt-page mkt-cloud-status">
      <header className="mkt-page__header">
        <h1>{t("cloud.status.title")}</h1>
        <span className="mkt-muted">{t("health.autoRefresh")}</span>
      </header>

      {healthErr ? (
        <Card className="mkt-stat">
          <strong>{t("cloud.status.tunnel")}</strong>
          <Badge tone="error">{t("health.status.down")}</Badge>
          <p className="mkt-muted">{t("cloud.status.unreachable")}</p>
        </Card>
      ) : health === null ? (
        <p className="mkt-loading">{t("common.loading")}</p>
      ) : !configured ? (
        <>
          <EmptyState
            title={t("cloud.status.notConfigured.title")}
            description={t("cloud.status.notConfigured.desc")}
          />
          <Card header={<strong>{t("cloud.status.setup.title")}</strong>}>
            <ol className="mkt-cloud-setup">
              <li>{t("cloud.status.setup.step1")}</li>
              <li>{t("cloud.status.setup.step2")}</li>
              <li>{t("cloud.status.setup.step3")}</li>
            </ol>
          </Card>
        </>
      ) : (
        <ConnectedView name={cloud[0]} svc={cloud[1]} version={version} />
      )}
    </section>
  );
}

function ConnectedView({
  name,
  svc,
  version,
}: {
  name: string;
  svc: ServiceSnapshot;
  version: Version | null;
}) {
  const { t } = useI18n();
  const s = svc.status.toLowerCase();
  const up = s === "ok" || s === "up" || s === "healthy" || s === "ready";
  const heartbeat = svc.checks?.heartbeat?.reason ?? svc.reason ?? null;

  return (
    <div className="mkt-stage-cards">
      <Card className="mkt-stat">
        <strong>{t("cloud.status.tunnel")}</strong>
        <Badge tone={up ? "success" : "error"}>
          {up ? t("cloud.status.connected") : t("cloud.status.disconnected")}
        </Badge>
      </Card>
      <Card className="mkt-stat">
        <strong>{t("cloud.status.serverId")}</strong>
        <span className="mkt-mono">{version?.build_sha?.slice(0, 12) || name}</span>
      </Card>
      <Card className="mkt-stat">
        <strong>{t("cloud.status.heartbeat")}</strong>
        <span>{heartbeat || t("cloud.status.noData")}</span>
      </Card>
      <Card className="mkt-stat">
        <strong>{t("cloud.status.bandwidth")}</strong>
        <span className="mkt-muted">{t("cloud.status.noData")}</span>
      </Card>
    </div>
  );
}
