// /link-home — the cloud's half of linking a home account (Epic 42 story 42.7).
//
// A home user on their home's Account page asked to link their Maktaba Cloud account:
// the home made a nonce for them and sent the browser here (through
// GET /v1/homes/{id}/link) with ?server=&nonce=&return=. Signed in to the cloud (the
// route asks first), the person confirms; the page then asks for a link assertion
// (POST /v1/homes/{server}/assertion, purpose "link", the nonce) and goes back to the
// home with it in the address's fragment (#cloud-link=…), which no server sees. The
// home checks it and links its user. The assertion names this cloud account and the
// home, for one use, a minute, bound to that nonce: it links nothing for anyone else,
// and it gives no rights in the home by itself.
import { useState } from "react";
import { useSearchParams } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { Card } from "@ds/components/Card/Card";
import { api } from "../api";
import { useI18n } from "../i18n";
import { navigateExternal } from "../nav";
import { useSession } from "../session";
import { Banner, Page, useErrorMessage } from "../ui";

/** The home's page to go back to: an http(s) address with a host and no credentials. */
export function linkReturn(raw: string | null): URL | null {
  if (!raw) return null;
  try {
    const u = new URL(raw);
    if (
      (u.protocol !== "http:" && u.protocol !== "https:") ||
      !u.host ||
      u.username ||
      u.password
    ) {
      return null;
    }
    u.hash = "";
    return u;
  } catch {
    return null;
  }
}

/** The return address with the outcome in its fragment. */
function back(to: URL, value: string): string {
  const u = new URL(to);
  u.hash = `cloud-link=${encodeURIComponent(value)}`;
  return u.toString();
}

export function LinkHome() {
  const { t } = useI18n();
  const { user } = useSession();
  const errorMessage = useErrorMessage();
  const [params] = useSearchParams();
  const server = params.get("server") ?? "";
  const nonce = params.get("nonce") ?? "";
  const to = linkReturn(params.get("return"));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);

  if (!server || !nonce || !to) {
    return (
      <Page title={t("linkHome.title")}>
        <Banner tone="error">{t("linkHome.broken")}</Banner>
      </Page>
    );
  }

  async function link() {
    setBusy(true);
    setError(null);
    try {
      const r = await api.post<{ assertion: string }>(
        `/v1/homes/${encodeURIComponent(server)}/assertion`,
        { purpose: "link", nonce }
      );
      navigateExternal(back(to!, r.assertion));
    } catch (e) {
      setError(e);
      setBusy(false);
    }
  }

  return (
    <Page title={t("linkHome.title")}>
      <Card>
        <p>{t("linkHome.ask", { home: to.host, account: user?.email ?? "" })}</p>
        <p className="mkc-muted">{t("linkHome.rights")}</p>
        {error !== null && <Banner tone="error">{errorMessage(error)}</Banner>}
        <div className="mkc-row">
          <Button variant="primary" loading={busy} onClick={() => void link()}>
            {t("linkHome.link")}
          </Button>
          <Button
            variant="secondary"
            disabled={busy}
            onClick={() => navigateExternal(back(to, "cancelled"))}
          >
            {t("linkHome.cancel")}
          </Button>
        </div>
      </Card>
    </Page>
  );
}
