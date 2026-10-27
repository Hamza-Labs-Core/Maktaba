// Account → Linked accounts (Epic 42.7): this home account and a Maktaba Cloud account.
//
//   GET    /api/me/cloud-link          { available, server_id, cloud_api, link }
//   POST   /api/me/cloud-link/start    { nonce, server_id, cloud_api, expires_at }
//   POST   /api/me/cloud-link          { assertion } → the link (scope play)
//   PATCH  /api/me/cloud-link          { scope: play | manage } (manage: an admin's own)
//   DELETE /api/me/cloud-link
//
// Linking: the home makes a nonce for this user, and the browser goes to the cloud
// ({cloud_api}/v1/homes/{server_id}/link, which sends it to the cloud web's confirm
// page). Signed in to the cloud and confirmed, the cloud sends the browser back here
// with a link assertion in the fragment (#cloud-link=…), which this card hands to the
// home. The home checks every rule (the nonce is this user's, one use; a sign-in
// through the cloud cannot link or set a scope: 403); this card only shows what it may.
import { useEffect, useRef, useState } from "react";
import { Card } from "@ds/components/Card/Card";
import { Button } from "@ds/components/Button/Button";
import { Badge } from "@ds/components/Badge/Badge";
import { Modal } from "@ds/components/Modal/Modal";
import { useToast } from "@ds/components/Toast/Toast";
import { api, ApiError } from "../../lib/api";
import { useAuth } from "../../lib/auth";
import { useI18n } from "../../lib/i18n";

export interface CloudLinkInfo {
  user_id: string;
  cloud_user_id: string;
  cloud_label?: string;
  scope: "play" | "manage";
  created_at: string;
}

interface CloudLinkState {
  available: boolean;
  server_id?: string;
  cloud_api?: string;
  link: CloudLinkInfo | null;
}

/** Where the cloud sends the browser back: this page, without its query or fragment. */
function returnAddress(): string {
  const u = new URL(window.location.href);
  u.search = "";
  u.hash = "";
  return u.toString();
}

/** The cloud's answer in this page's fragment (and takes it out of the address). */
function takeFragment(): string | null {
  const m = /(?:^#|&)cloud-link=([^&]*)/.exec(window.location.hash);
  if (!m) return null;
  window.history.replaceState(window.history.state, "", returnAddress());
  return decodeURIComponent(m[1]);
}

// The browser leaves for the cloud: one seam, so tests can see where.
export const leave = { to: (url: string) => window.location.assign(url) };

export function CloudLinkCard() {
  const { t } = useI18n();
  const toast = useToast();
  const { user } = useAuth();
  const [state, setState] = useState<CloudLinkState | null>(null);
  const [hidden, setHidden] = useState(false);
  const [busy, setBusy] = useState(false);
  const [confirmUnlink, setConfirmUnlink] = useState(false);
  const handled = useRef(false);
  const viaCloud = user?.cloud_scope !== undefined;

  function problem(e: unknown): string {
    if (e instanceof ApiError) {
      if (e.status === 409) return t("cloudLink.taken");
      if (e.status === 422) return t("cloudLink.expired");
      if (e.problem.type?.endsWith("/cloud-session")) return t("cloudLink.onHome");
      return e.problem.detail || e.problem.title;
    }
    return t("common.error");
  }

  useEffect(() => {
    let live = true;
    async function load() {
      try {
        const s = await api.get<CloudLinkState>("/api/me/cloud-link");
        if (live) setState(s);
      } catch {
        // no linked accounts on this home (an older home, a machine's copy): no card
        if (live) setHidden(true);
        return;
      }
      // back from the cloud: finish the link it confirmed (once, even under StrictMode)
      const answer = handled.current ? null : takeFragment();
      handled.current = true;
      if (answer === null) return;
      if (answer === "cancelled") {
        toast.show({ tone: "info", message: t("cloudLink.cancelled") });
        return;
      }
      setBusy(true);
      try {
        const link = await api.post<CloudLinkInfo>("/api/me/cloud-link", { assertion: answer });
        if (live) setState((s) => (s ? { ...s, link } : s));
        toast.show({ tone: "success", message: t("cloudLink.linked") });
      } catch (e) {
        toast.show({ tone: "error", message: problem(e) });
      } finally {
        if (live) setBusy(false);
      }
    }
    void load();
    return () => {
      live = false;
    };
    // load once per page
  }, []);

  if (hidden || state === null) return null;

  async function start() {
    setBusy(true);
    try {
      const s = await api.post<{ nonce: string; server_id: string; cloud_api: string }>(
        "/api/me/cloud-link/start",
        {}
      );
      const to = new URL(
        `${s.cloud_api.replace(/\/+$/, "")}/v1/homes/${encodeURIComponent(s.server_id)}/link`
      );
      to.searchParams.set("nonce", s.nonce);
      to.searchParams.set("return", returnAddress());
      leave.to(to.toString());
    } catch (e) {
      toast.show({ tone: "error", message: problem(e) });
      setBusy(false);
    }
  }

  async function setScope(scope: "play" | "manage") {
    setBusy(true);
    try {
      const link = await api.patch<CloudLinkInfo>("/api/me/cloud-link", { scope });
      setState((s) => (s ? { ...s, link } : s));
      toast.show({ tone: "success", message: t("cloudLink.scopeSaved") });
    } catch (e) {
      toast.show({ tone: "error", message: problem(e) });
    } finally {
      setBusy(false);
    }
  }

  async function unlink() {
    setBusy(true);
    try {
      await api.delete("/api/me/cloud-link");
      setState((s) => (s ? { ...s, link: null } : s));
      setConfirmUnlink(false);
      toast.show({ tone: "success", message: t("cloudLink.unlinked") });
    } catch (e) {
      toast.show({ tone: "error", message: problem(e) });
    } finally {
      setBusy(false);
    }
  }

  const link = state.link;
  return (
    <Card header={<strong>{t("cloudLink.title")}</strong>}>
      <div className="mkt-cloud-link" aria-busy={busy}>
        {!state.available ? (
          <p className="mkt-muted">{t("cloudLink.unavailable")}</p>
        ) : link === null ? (
          <>
            <p>{t("cloudLink.intro")}</p>
            {viaCloud ? (
              <p className="mkt-muted">{t("cloudLink.onHome")}</p>
            ) : (
              <Button onClick={() => void start()} loading={busy}>
                {t("cloudLink.link")}
              </Button>
            )}
          </>
        ) : (
          <>
            <p>
              {t("cloudLink.linkedTo")} <bdi>{link.cloud_label || link.cloud_user_id}</bdi>{" "}
              <Badge tone={link.scope === "manage" ? "warn" : "neutral"}>
                {t(`cloudLink.scope.${link.scope}`)}
              </Badge>
            </p>
            <p className="mkt-muted">{t(`cloudLink.scopeHint.${link.scope}`)}</p>
            {/* manage is an admin's choice for their own link, made on the home */}
            {user?.is_admin && !viaCloud && (
              <Button
                variant="secondary"
                disabled={busy}
                onClick={() => void setScope(link.scope === "manage" ? "play" : "manage")}
              >
                {link.scope === "manage" ? t("cloudLink.toPlay") : t("cloudLink.toManage")}
              </Button>
            )}{" "}
            <Button variant="destructive" disabled={busy} onClick={() => setConfirmUnlink(true)}>
              {t("cloudLink.unlink")}
            </Button>
          </>
        )}
      </div>
      <Modal
        open={confirmUnlink}
        onClose={() => setConfirmUnlink(false)}
        title={t("cloudLink.unlinkTitle")}
        footer={
          <>
            <Button variant="secondary" onClick={() => setConfirmUnlink(false)}>
              {t("common.cancel")}
            </Button>
            <Button variant="destructive" loading={busy} onClick={() => void unlink()}>
              {t("cloudLink.unlink")}
            </Button>
          </>
        }
      >
        <p>{t("cloudLink.unlinkBody")}</p>
      </Modal>
    </Card>
  );
}
