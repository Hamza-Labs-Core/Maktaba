// The signed-in user's connected devices (Story 12.10 self-service).
//
// Real contracts:
//   GET    /api/me/devices      { items: [redactedDevice] }
//   DELETE /api/devices/{id}    (soft-revoke; owner-scoped)
//
// A card list rather than the admin table — this is the "my devices"
// surface. Removal is confirmed because it stops push delivery to that
// device. Shares the Device shape + platform icon with the admin view.
import { useEffect, useState } from "react";
import { Card } from "@ds/components/Card/Card";
import { Button } from "@ds/components/Button/Button";
import { Modal } from "@ds/components/Modal/Modal";
import { EmptyState } from "@ds/components/EmptyState/EmptyState";
import { ErrorState } from "@ds/components/ErrorState/ErrorState";
import { useToast } from "@ds/components/Toast/Toast";
import { api, ApiError } from "../../lib/api";
import { useI18n } from "../../lib/i18n";
import { platformIcon, type Device } from "../Cloud/Devices";

export function ConnectedDevices() {
  const { t, formatDate } = useI18n();
  const toast = useToast();
  const [devices, setDevices] = useState<Device[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [deleting, setDeleting] = useState<Device | null>(null);

  function load() {
    setError(null);
    api
      .get<{ items: Device[] }>("/api/me/devices")
      .then((r) => setDevices(r.items ?? []))
      .catch((e) => {
        setError(e instanceof ApiError ? e.problem.detail || e.problem.title : t("common.error"));
        setDevices([]);
      });
  }

  useEffect(load, []);

  async function remove(d: Device) {
    try {
      await api.delete(`/api/devices/${encodeURIComponent(d.id)}`);
      toast.show({ tone: "success", message: t("devices.removed") });
      setDeleting(null);
      load();
    } catch (e) {
      toast.show({
        tone: "error",
        message: e instanceof ApiError ? e.problem.detail || e.problem.title : t("common.error"),
      });
    }
  }

  return (
    <section className="mkt-page mkt-devices">
      <header className="mkt-page__header">
        <h1>{t("devices.title")}</h1>
      </header>

      {error ? (
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
      ) : devices === null ? (
        <p className="mkt-loading">{t("common.loading")}</p>
      ) : devices.length === 0 ? (
        <EmptyState title={t("devices.empty.title")} description={t("devices.empty.desc")} />
      ) : (
        <div className="mkt-device-cards">
          {devices.map((d) => (
            <Card key={d.id} className="mkt-device-card">
              <div className="mkt-device-card__head">
                <span className="mkt-device-card__icon" aria-hidden="true">
                  {platformIcon(d.platform)}
                </span>
                <div>
                  <strong>{t(`devices.platform.${d.platform}`)}</strong>
                  <div className="mkt-muted mkt-mono mkt-truncate">{d.bundle_id}</div>
                </div>
              </div>
              <dl className="mkt-device-card__meta">
                <div>
                  <dt className="mkt-muted">{t("devices.col.lastSeen")}</dt>
                  <dd>{formatDate(d.last_seen_at)}</dd>
                </div>
                {d.app_version && (
                  <div>
                    <dt className="mkt-muted">{t("devices.col.appVersion")}</dt>
                    <dd>{d.app_version}</dd>
                  </div>
                )}
              </dl>
              <Button variant="destructive" size="sm" onClick={() => setDeleting(d)}>
                {t("devices.remove")}
              </Button>
            </Card>
          ))}
        </div>
      )}

      {deleting && (
        <Modal
          open
          onClose={() => setDeleting(null)}
          dismissable={false}
          title={t("devices.remove.title")}
          footer={
            <>
              <Button variant="ghost" onClick={() => setDeleting(null)}>
                {t("common.cancel")}
              </Button>
              <Button variant="destructive" onClick={() => remove(deleting)}>
                {t("devices.remove")}
              </Button>
            </>
          }
        >
          <p>{t("devices.remove.confirm")}</p>
        </Modal>
      )}
    </section>
  );
}
