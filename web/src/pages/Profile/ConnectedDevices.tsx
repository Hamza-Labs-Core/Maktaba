// The signed-in user's connected devices (Story 12.10 self-service).
//
// Real contracts:
//   GET    /api/me/devices      { items: [redactedDevice] }
//   DELETE /api/devices/{id}    (soft-revoke; owner-scoped)
//
// A card list rather than the admin table — this is the "my devices"
// surface. Removal is confirmed because it stops push delivery to that
// device. Shares the Device shape + platform icon with the admin view. A
// return to the page shows the last list at once and reads it again quietly
// (lib/usePageData).
import { useState } from "react";
import { Link } from "react-router-dom";
import { Card } from "@ds/components/Card/Card";
import { Button } from "@ds/components/Button/Button";
import { Modal } from "@ds/components/Modal/Modal";
import { EmptyState } from "@ds/components/EmptyState/EmptyState";
import { ErrorState } from "@ds/components/ErrorState/ErrorState";
import { useToast } from "@ds/components/Toast/Toast";
import { api } from "../../lib/api";
import { useI18n } from "../../lib/i18n";
import { platformIcon } from "../../lib/platformIcon";
import type { Device } from "../Cloud/Devices";
import { TitleRow } from "../../help/HelpLink";
import { errorText } from "../../lib/errorText";
import { usePageData } from "../../lib/usePageData";
import { forgetAnswers } from "../../lib/pageCache";
import { Skeleton } from "@ds/components/Skeleton/Skeleton";
import { Loading } from "../../components/Placeholders";

function loadDevices() {
  return api.get<{ items: Device[] }>("/api/me/devices").then((r) => r.items ?? []);
}

/** The device cards while the first read is slow: the cards' own classes. */
function DeviceCardsPlaceholder({ cards = 3 }: { cards?: number }) {
  return (
    <div className="mkt-device-cards" aria-hidden="true">
      {Array.from({ length: cards }, (_, i) => (
        <Card key={i} className="mkt-device-card">
          <div className="mkt-device-card__head">
            <Skeleton variant="circle" width={28} height={28} />
            <div>
              <Skeleton variant="text" className="mkt-ph-line" width="6rem" />
              <Skeleton variant="text" className="mkt-ph-line" width="10rem" />
            </div>
          </div>
          <Skeleton variant="text" className="mkt-ph-line" width="60%" />
          <Skeleton variant="rect" className="mkt-ph-button" width="6rem" />
        </Card>
      ))}
    </div>
  );
}

export function ConnectedDevices() {
  const { t, formatDate } = useI18n();
  const toast = useToast();
  const {
    data: devices,
    err,
    loading,
    reload,
    setData: setDevices,
  } = usePageData<Device[]>("account:devices", loadDevices);
  const error = err != null ? errorText(err, t) : null;
  const [deleting, setDeleting] = useState<Device | null>(null);
  const [removing, setRemoving] = useState(false);

  // A platform the catalog doesn't know shows its raw value (or a dash),
  // never the i18n key.
  function platformLabel(platform: string | null | undefined): string {
    if (!platform) return "—";
    const key = `devices.platform.${platform}`;
    const label = t(key);
    return label === key ? platform : label;
  }

  async function remove(d: Device) {
    if (removing) return;
    setRemoving(true);
    try {
      await api.delete(`/api/devices/${encodeURIComponent(d.id)}`);
      toast.show({ tone: "success", message: t("devices.removed") });
      setDeleting(null);
      // gone from the page and its kept answer at once, then read again;
      // Cloud → Devices lists the same devices
      forgetAnswers("cloud:devices");
      setDevices((cur) => (cur ? cur.filter((x) => x.id !== d.id) : cur));
      reload();
    } catch (e) {
      toast.show({
        tone: "error",
        message: errorText(e, t),
      });
    } finally {
      setRemoving(false);
    }
  }

  return (
    <section className="mkt-page mkt-devices">
      <header className="mkt-page__header">
        <TitleRow>
          <h1>{t("devices.title")}</h1>
        </TitleRow>
        <Link to="/pair" className="mkt-btn">
          {t("devices.signInTv")}
        </Link>
      </header>

      {error ? (
        <ErrorState
          kind="server"
          title={t("common.error")}
          description={error}
          action={
            <Button variant="secondary" onClick={reload}>
              {t("common.retry")}
            </Button>
          }
        />
      ) : devices === null ? (
        <Loading loading={loading}>
          <DeviceCardsPlaceholder />
        </Loading>
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
                  <strong>{platformLabel(d.platform)}</strong>
                  <div className="mkt-muted mkt-mono mkt-truncate">{d.bundle_id}</div>
                </div>
              </div>
              <dl className="mkt-device-card__meta">
                <div>
                  <dt className="mkt-muted">{t("devices.col.lastSeen")}</dt>
                  <dd>{d.last_seen_at ? formatDate(d.last_seen_at) : t("common.never")}</dd>
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
              <Button variant="destructive" loading={removing} onClick={() => remove(deleting)}>
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
