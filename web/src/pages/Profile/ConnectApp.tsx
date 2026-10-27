// "Connect an app" (Epic 42 story 42.2): a QR code a phone, tablet or TV app scans to
// add this home, and the home's addresses to type. Any signed-in user.
//
// Real contract (api/internal/handlers/discovery/home.go):
//   GET /api/discovery/home → { id, name, version, urls: [LAN…, relay], sign_in }
// The QR code is the app link maktaba://connect?id=<id>&n=<name>&u=<url>[&u=…] (the
// LAN addresses first), drawn here (lib/qr.ts), never by another service.
import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { Card } from "@ds/components/Card/Card";
import { Button } from "@ds/components/Button/Button";
import { ErrorState } from "@ds/components/ErrorState/ErrorState";
import { useToast } from "@ds/components/Toast/Toast";
import { api } from "../../lib/api";
import { bidiIsolate, useI18n } from "../../lib/i18n";
import { copyText } from "../../lib/clipboard";
import { QRCode } from "../../components/QRCode";
import { TitleRow } from "../../help/HelpLink";
import { errorText } from "../../lib/errorText";

export interface DiscoveryHome {
  id: string;
  name: string;
  version: string;
  urls: string[];
  sign_in: { password: boolean; code: boolean };
}

/** The app link a QR code carries: maktaba://connect?id=…&n=…&u=…[&u=…]. */
export function connectLink(home: Pick<DiscoveryHome, "id" | "name" | "urls">): string {
  const enc = encodeURIComponent;
  return (
    `maktaba://connect?id=${enc(home.id)}&n=${enc(home.name)}` +
    home.urls.map((u) => `&u=${enc(u)}`).join("")
  );
}

export function ConnectApp() {
  const { t } = useI18n();
  const toast = useToast();
  const [home, setHome] = useState<DiscoveryHome | null>(null);
  const [error, setError] = useState<string | null>(null);

  function load() {
    setError(null);
    api
      .get<DiscoveryHome>("/api/discovery/home")
      .then((h) => setHome({ ...h, urls: h.urls ?? [] }))
      .catch((e) => setError(errorText(e, t)));
  }

  useEffect(load, []);

  async function copy(text: string) {
    if (await copyText(text)) toast.show({ tone: "success", message: t("connect.copied") });
    else toast.show({ tone: "error", message: t("connect.copyFailed") });
  }

  const link = home ? connectLink(home) : "";

  return (
    <section className="mkt-page mkt-connect">
      <header className="mkt-page__header">
        <TitleRow>
          <h1>{t("connect.title")}</h1>
        </TitleRow>
      </header>

      {error ? (
        <ErrorState
          kind="server"
          title={t("common.error")}
          description={error}
          action={<Button onClick={load}>{t("common.retry")}</Button>}
        />
      ) : !home ? (
        <p className="mkt-muted" role="status">
          {t("common.loading")}
        </p>
      ) : (
        <Card>
          <p>{t("connect.intro")}</p>
          <div className="mkt-connect__body">
            {home.urls.length > 0 ? (
              <div className="mkt-connect__qr" data-testid="connect-qr" data-link={link}>
                <QRCode
                  value={link}
                  label={t("connect.qrLabel", { name: bidiIsolate(home.name) })}
                />
              </div>
            ) : null}
            <div className="mkt-connect__details">
              <p>
                <strong>{t("connect.home")}</strong> {bidiIsolate(home.name)}
              </p>
              <h2 className="mkt-connect__heading">{t("connect.addresses")}</h2>
              {home.urls.length === 0 ? (
                <p className="mkt-muted">{t("connect.noAddresses")}</p>
              ) : (
                <ul className="mkt-connect__urls">
                  {home.urls.map((u) => (
                    <li key={u}>
                      <span className="mkt-mono" dir="ltr">
                        {u}
                      </span>
                      <Button
                        variant="ghost"
                        size="sm"
                        onClick={() => void copy(u)}
                        aria-label={t("connect.copyAddress", { address: u })}
                      >
                        {t("connect.copy")}
                      </Button>
                    </li>
                  ))}
                </ul>
              )}
              {home.urls.length > 0 ? (
                <Button variant="secondary" onClick={() => void copy(link)}>
                  {t("connect.copyLink")}
                </Button>
              ) : null}
            </div>
          </div>
          {home.sign_in.code ? (
            <p className="mkt-muted">
              {t("connect.codeHint")} <Link to="/pair">{t("pair.title")}</Link>
            </p>
          ) : null}
        </Card>
      )}
    </section>
  );
}
