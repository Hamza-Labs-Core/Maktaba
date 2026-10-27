// /homes — the homes this cloud account reaches (Epic 42 story 42.9), and their player and
// remote served by the cloud: Watch opens https://<home>.<relay>/play/, Manage /manage/.
//
// Each opens signed in: the page asks for a one-use sign-in assertion for that home
// (POST /v1/homes/{id}/assertion) and goes to the edition's /cloud-sign-in with it in the
// fragment, which no server sees; the edition hands it to the home (POST /api/auth/cloud),
// which signs its linked user in on that address. The home decides everything: a home not
// linked to this account gives nothing (owning it is no account in it), and Manage is
// refused there unless the person is an admin of the home whose own link allows managing.
import { useState } from "react";
import { Badge } from "@ds/components/Badge/Badge";
import { Button } from "@ds/components/Button/Button";
import { Card } from "@ds/components/Card/Card";
import { api } from "../api";
import { useI18n } from "../i18n";
import { navigateExternal } from "../nav";
import { Banner, LoadError, Loading, Page, useErrorMessage, useLoad } from "../ui";

export interface CloudHome {
  server_id: string;
  name: string;
  slug: string;
  relay_url: string;
  direct_url?: string;
  online: boolean;
  linked: boolean;
  owned: boolean;
}

type Edition = "play" | "manage";

/** A home's edition's sign-in page, with the assertion in the fragment. */
export function editionUrl(home: CloudHome, edition: Edition, assertion: string): string {
  const u = new URL(home.relay_url);
  if (u.protocol !== "https:" && u.protocol !== "http:") throw new Error("not a home address");
  u.pathname = `${u.pathname.replace(/\/+$/, "")}/${edition}/cloud-sign-in`;
  u.search = "";
  u.hash = `assertion=${encodeURIComponent(assertion)}`;
  return u.toString();
}

export function Homes() {
  const { t } = useI18n();
  const errorMessage = useErrorMessage();
  const homes = useLoad(() => api.get<{ homes: CloudHome[] }>("/v1/homes"), "homes");
  const [opening, setOpening] = useState<string | null>(null);
  const [error, setError] = useState<unknown>(null);

  async function open(home: CloudHome, edition: Edition) {
    setOpening(`${home.server_id}/${edition}`);
    setError(null);
    try {
      const r = await api.post<{ assertion: string }>(
        `/v1/homes/${encodeURIComponent(home.server_id)}/assertion`,
        { purpose: "signin" }
      );
      navigateExternal(editionUrl(home, edition, r.assertion));
    } catch (e) {
      setError(e);
      setOpening(null);
    }
  }

  return (
    <Page title={t("homes.title")}>
      <p className="mkc-muted">{t("homes.intro")}</p>
      {error !== null && <Banner tone="error">{errorMessage(error)}</Banner>}
      {homes.loading && <Loading />}
      {homes.error !== null && homes.error !== undefined && (
        <LoadError error={homes.error} onRetry={homes.reload} />
      )}
      {homes.data && homes.data.homes.length === 0 && <p>{t("homes.none")}</p>}
      {homes.data &&
        homes.data.homes.map((h) => (
          <Card key={h.server_id}>
            <div className="mkc-row">
              <h2>
                <bdi>{h.name || h.slug}</bdi>
              </h2>
              <Badge tone={h.online ? "success" : "neutral"}>
                {h.online ? t("homes.online") : t("homes.offline")}
              </Badge>
              {h.owned && <Badge tone="accent">{t("homes.owned")}</Badge>}
            </div>
            {h.linked ? (
              <div className="mkc-row">
                <Button
                  variant="primary"
                  loading={opening === `${h.server_id}/play`}
                  disabled={opening !== null}
                  onClick={() => void open(h, "play")}
                >
                  {t("homes.watch")}
                </Button>
                <Button
                  variant="secondary"
                  loading={opening === `${h.server_id}/manage`}
                  disabled={opening !== null}
                  onClick={() => void open(h, "manage")}
                >
                  {t("homes.manage")}
                </Button>
              </div>
            ) : (
              <p className="mkc-muted">
                {t("homes.notLinked")}{" "}
                <a href={new URL("/account", h.relay_url).toString()}>{t("homes.openHome")}</a>
              </p>
            )}
          </Card>
        ))}
      <p className="mkc-muted">{t("homes.rights")}</p>
    </Page>
  );
}
