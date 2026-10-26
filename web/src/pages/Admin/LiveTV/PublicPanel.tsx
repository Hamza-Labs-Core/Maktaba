// Admin → Live TV → Public site (Epic 34, first slice): the site's switch
// and address, its secrets (created here and shown once, for the Worker),
// the publisher's last post, and every channel with its public flag. Making
// a channel public asks for the rights confirmation each time (34.1 AC1,
// AC2); the API refuses without it, so this dialog is a convenience, not
// the rule.
//
//   GET /api/tv/public · PUT /api/tv/public · PUT /api/tv/public/channels/{id}
//   POST /api/tv/public/site-secrets
import { useCallback, useEffect, useRef, useState } from "react";
import { Button } from "@ds/components/Button/Button";
import { Card } from "@ds/components/Card/Card";
import { Input } from "@ds/components/Input/Input";
import { Modal } from "@ds/components/Modal/Modal";
import { Toggle } from "@ds/components/Choice/Toggle";
import { CopyField } from "../../../components/CopyField";
import { ApiError } from "../../../lib/api";
import { useI18n } from "../../../lib/i18n";
import {
  tvPublicApi,
  type PublicChannel,
  type PublicView,
  type SiteSecrets,
} from "../../../lib/tvPublicApi";
import { simulcastErrCode } from "../../../lib/tvSimulcastApi";

const when = (s?: string | null) => (s ? new Date(s).toLocaleString() : "—");

function errText(e: unknown): string {
  if (e instanceof ApiError) return e.problem.detail || e.message;
  return e instanceof Error ? e.message : String(e);
}

export function PublicPanel() {
  const { t } = useI18n();
  const [view, setView] = useState<PublicView | null>(null);
  const [hidden, setHidden] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [enabled, setEnabled] = useState(false);
  const [url, setUrl] = useState("");
  const [busy, setBusy] = useState(false);
  const [rights, setRights] = useState<PublicChannel | null>(null);
  const [slug, setSlug] = useState("");
  const [note, setNote] = useState("");
  const [secrets, setSecrets] = useState<SiteSecrets | null>(null);
  const [askSecrets, setAskSecrets] = useState(false);
  const alive = useRef(true);

  const apply = useCallback((v: PublicView) => {
    setView(v);
    setEnabled(!!v.settings?.enabled);
    setUrl(v.settings?.site_url ?? "");
  }, []);

  const load = useCallback(async () => {
    try {
      const v = await tvPublicApi.status();
      if (!alive.current) return;
      apply(v);
      setError(null);
    } catch (e) {
      if (!alive.current) return;
      if (e instanceof ApiError && (e.status === 403 || e.status === 404 || e.status === 503)) {
        setHidden(true);
        return;
      }
      setError(errText(e));
    }
  }, [apply]);

  useEffect(() => {
    alive.current = true;
    void load();
    return () => {
      alive.current = false;
    };
  }, [load]);

  if (hidden) return null;

  async function saveSettings() {
    setBusy(true);
    setError(null);
    try {
      apply(await tvPublicApi.save({ enabled, site_url: url.trim() }));
    } catch (e) {
      setError(errText(e));
    } finally {
      setBusy(false);
    }
  }

  async function setPublic(c: PublicChannel, on: boolean, confirmed = false) {
    setBusy(true);
    setError(null);
    try {
      await tvPublicApi.setChannel(
        c.channel_id,
        on
          ? {
              public: true,
              slug: confirmed ? slug.trim() : c.slug,
              rights_note: confirmed ? note : c.rights_note,
              rights_confirmed: confirmed,
            }
          : { public: false }
      );
      setRights(null);
      await load();
    } catch (e) {
      if (simulcastErrCode(e) === "rights_confirmation_required") {
        setSlug(c.slug);
        setNote(c.rights_note);
        setRights(c);
      } else {
        setError(errText(e));
      }
    } finally {
      setBusy(false);
    }
  }

  async function createSecrets() {
    setBusy(true);
    setError(null);
    try {
      setSecrets(await tvPublicApi.newSecrets());
      setAskSecrets(false);
      await load();
    } catch (e) {
      setError(errText(e));
    } finally {
      setBusy(false);
    }
  }

  const channels = view?.channels ?? [];
  const pub = view?.publisher;

  return (
    <Card className="mkt-livetv__card">
      <h2>{t("tvpublic.title")}</h2>
      <p className="mkt-livetv__hint">{t("tvpublic.intro")}</p>
      {error && (
        <p role="alert" className="mkt-q-status--bad">
          {error}
        </p>
      )}
      <div className="mkt-livetv__row">
        <Toggle
          label={t("tvpublic.enabled")}
          checked={enabled}
          onChange={(e) => setEnabled(e.currentTarget.checked)}
        />
        <Input
          label={t("tvpublic.siteUrl")}
          description={t("tvpublic.siteUrlHint")}
          value={url}
          placeholder="https://maktaba-tv-site.example.workers.dev"
          onChange={(e) => setUrl(e.currentTarget.value)}
        />
        <Button disabled={busy} onClick={() => void saveSettings()}>
          {t("common.save")}
        </Button>
      </div>
      <p className="mkt-livetv__hint">
        {view?.secrets_set ? t("tvpublic.secretsSet") : t("tvpublic.secretsMissing")}{" "}
        <Button variant="secondary" disabled={busy} onClick={() => setAskSecrets(true)}>
          {view?.secrets_set ? t("tvpublic.secretsRenew") : t("tvpublic.secretsCreate")}
        </Button>
      </p>
      <p className="mkt-livetv__hint" data-testid="tvpublic-publisher">
        {t("tvpublic.lastPost", { at: when(pub?.last_ok_at) })}
        {pub?.last_error ? ` — ${pub.last_error}` : ""}
      </p>
      <table className="mkt-livetv__table">
        <thead>
          <tr>
            <th scope="col">{t("tvpublic.channel")}</th>
            <th scope="col">{t("tvpublic.slug")}</th>
            <th scope="col">{t("tvpublic.public")}</th>
            <th scope="col">{t("tvpublic.onAir")}</th>
            <th scope="col">{t("tvpublic.rightsBy")}</th>
          </tr>
        </thead>
        <tbody>
          {channels.map((c) => (
            <tr key={c.channel_id}>
              <td>
                {c.number} {c.name}
              </td>
              <td>
                <code>/watch/{c.slug}</code>
              </td>
              <td>
                <Toggle
                  aria-label={t("tvpublic.publicFor", { name: c.name })}
                  checked={c.public}
                  disabled={busy}
                  onChange={(e) => void setPublic(c, e.currentTarget.checked)}
                />
              </td>
              <td>{c.public ? (c.on_air ? t("tvpublic.yes") : t("tvpublic.no")) : "—"}</td>
              <td>
                {c.rights_confirmed_at
                  ? t("tvpublic.rightsAt", { at: when(c.rights_confirmed_at) })
                  : "—"}
              </td>
            </tr>
          ))}
        </tbody>
      </table>

      {rights && (
        <Modal
          open
          onClose={() => setRights(null)}
          title={t("tvpublic.rightsTitle")}
          footer={
            <div className="mkt-livetv__actions">
              <Button variant="secondary" onClick={() => setRights(null)}>
                {t("common.cancel")}
              </Button>
              <Button
                disabled={busy || !slug.trim()}
                onClick={() => void setPublic(rights, true, true)}
              >
                {t("tvpublic.rightsConfirm")}
              </Button>
            </div>
          }
        >
          <p>{t("tvpublic.rightsBody", { name: rights.name })}</p>
          <Input
            label={t("tvpublic.slug")}
            value={slug}
            onChange={(e) => setSlug(e.currentTarget.value)}
          />
          <Input
            label={t("tvpublic.rightsNote")}
            value={note}
            onChange={(e) => setNote(e.currentTarget.value)}
          />
          <p className="mkt-q-muted">{t("simulcast.rightsAudit")}</p>
        </Modal>
      )}

      {askSecrets && (
        <Modal
          open
          onClose={() => setAskSecrets(false)}
          title={t("tvpublic.secretsTitle")}
          footer={
            <div className="mkt-livetv__actions">
              <Button variant="secondary" onClick={() => setAskSecrets(false)}>
                {t("common.cancel")}
              </Button>
              <Button disabled={busy} onClick={() => void createSecrets()}>
                {t("tvpublic.secretsCreate")}
              </Button>
            </div>
          }
        >
          <p>{t("tvpublic.secretsWarn")}</p>
        </Modal>
      )}

      {secrets && (
        <Modal
          open
          onClose={() => setSecrets(null)}
          title={t("tvpublic.secretsTitle")}
          footer={<Button onClick={() => setSecrets(null)}>{t("tvpublic.secretsDone")}</Button>}
        >
          <p>{t("tvpublic.secretsOnce")}</p>
          <CopyField label="HOME_HMAC" value={secrets.home_hmac} />
          <CopyField label="STREAM_KEY_ID" value={secrets.signing_key_id} />
          <CopyField label="STREAM_KEY_JWK" value={secrets.signing_key_jwk} />
        </Modal>
      )}
    </Card>
  );
}
