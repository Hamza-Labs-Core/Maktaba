// /account — profile, sign-in methods, password, sessions, export, deletion.
import { useEffect, useState } from "react";
import type { FormEvent } from "react";
import { useNavigate } from "react-router-dom";
import { Badge } from "@ds/components/Badge/Badge";
import { Button } from "@ds/components/Button/Button";
import { Card } from "@ds/components/Card/Card";
import { Input } from "@ds/components/Input/Input";
import { Modal } from "@ds/components/Modal/Modal";
import { Select } from "@ds/components/Select/Select";
import { useToast } from "@ds/components/Toast/Toast";
import { api, fetchApi, type CloudUser } from "../api";
import { useI18n, type Locale } from "../i18n";
import { useSession } from "../session";
import { Banner, LoadError, Loading, Page, passwordProblem, useErrorMessage, useLoad } from "../ui";

export interface Me extends CloudUser {
  locale: string;
  avatar_url?: string;
  created_at: string;
  identities: { provider: string; email: string; created_at: string }[];
  has_password: boolean;
  deletion?: { requested_at: string; purge_after: string } | null;
}

interface SessionRow {
  id: string;
  user_agent: string;
  ip: string;
  created_at: string;
  last_used_at: string;
  expires_at: string;
  current: boolean;
}

export function Account() {
  const { t } = useI18n();
  const me = useLoad(() => api.get<Me>("/v1/account/me"), "me");

  return (
    <Page title={t("account.title")}>
      {me.error != null ? (
        <LoadError error={me.error} onRetry={me.reload} />
      ) : !me.data ? (
        <Loading />
      ) : (
        <AccountBody me={me.data} reload={me.reload} />
      )}
    </Page>
  );
}

function AccountBody({ me, reload }: { me: Me; reload: () => void }) {
  return (
    <div className="mkc-stack">
      {me.deletion && <DeletionHold deletion={me.deletion} onCancelled={reload} />}
      <Profile me={me} onSaved={reload} />
      <Identities me={me} />
      {me.has_password && <ChangePassword />}
      <Sessions />
      <ExportData />
      {!me.deletion && <DeleteAccount hasPassword={me.has_password} />}
    </div>
  );
}

function DeletionHold({
  deletion,
  onCancelled,
}: {
  deletion: NonNullable<Me["deletion"]>;
  onCancelled: () => void;
}) {
  const { t, formatDate } = useI18n();
  const message = useErrorMessage();
  const toast = useToast();
  const [busy, setBusy] = useState(false);

  async function cancel() {
    setBusy(true);
    try {
      await api.post("/v1/account/deletion/cancel");
      toast.show({ tone: "success", message: t("account.deletion.cancelled") });
      onCancelled();
    } catch (err) {
      toast.show({ tone: "error", message: message(err) });
    } finally {
      setBusy(false);
    }
  }

  return (
    <Banner tone="warn">
      <p>{t("account.deletion.pending", { date: formatDate(deletion.purge_after) })}</p>
      <Button variant="secondary" size="sm" loading={busy} onClick={cancel}>
        {t("account.deletion.cancel")}
      </Button>
    </Banner>
  );
}

function Profile({ me, onSaved }: { me: Me; onSaved: () => void }) {
  const { t, setLocale } = useI18n();
  const message = useErrorMessage();
  const toast = useToast();
  const { updateUser } = useSession();
  const [name, setName] = useState(me.display_name ?? "");
  const [locale, setLoc] = useState(me.locale === "ar" ? "ar" : "en");
  const [busy, setBusy] = useState(false);
  const tooLong = [...name.trim()].length > 80;

  async function save(e: FormEvent) {
    e.preventDefault();
    if (tooLong) return;
    setBusy(true);
    try {
      const next = await api.patch<Me>("/v1/account/me", { display_name: name.trim(), locale });
      updateUser({ display_name: next?.display_name ?? name.trim() });
      setLocale(locale as Locale);
      toast.show({ tone: "success", message: t("account.profile.saved") });
      onSaved();
    } catch (err) {
      toast.show({ tone: "error", message: message(err) });
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card header={<h2>{t("account.profile.title")}</h2>}>
      <dl className="mkc-kv">
        <dt>{t("field.email")}</dt>
        <dd>
          {me.email}{" "}
          {me.email_verified ? (
            <Badge tone="success">{t("account.verified")}</Badge>
          ) : (
            <Badge tone="warn">{t("account.unverified")}</Badge>
          )}
        </dd>
        <dt>{t("account.plan")}</dt>
        <dd>
          <Badge tone="accent">{me.plan}</Badge>
        </dd>
      </dl>
      <form className="mkc-form" onSubmit={save} noValidate>
        <Input
          label={t("field.displayName")}
          value={name}
          maxLength={120}
          error={tooLong ? t("error.display_name_too_long") : undefined}
          description={t("account.profile.nameHint")}
          onChange={(e) => setName(e.target.value)}
        />
        <Select
          label={t("field.language")}
          value={locale}
          onChange={(e) => setLoc(e.target.value as "en" | "ar")}
          options={[
            { value: "en", label: "English" },
            { value: "ar", label: "العربية" },
          ]}
        />
        <Button type="submit" loading={busy} disabled={tooLong}>
          {t("common.save")}
        </Button>
      </form>
    </Card>
  );
}

function Identities({ me }: { me: Me }) {
  const { t, has, formatDate } = useI18n();
  const providerName = (p: string) => (has(`provider.${p}`) ? t(`provider.${p}`) : p);
  return (
    <Card header={<h2>{t("account.identities.title")}</h2>}>
      <ul className="mkc-list">
        {me.has_password && (
          <li>
            <strong>{t("provider.password")}</strong>
            <span className="mkc-muted">{me.email}</span>
          </li>
        )}
        {me.identities.map((id) => (
          <li key={`${id.provider}:${id.email}`}>
            <strong>{providerName(id.provider)}</strong>
            <span className="mkc-muted">
              {id.email} · {t("account.identities.linked", { date: formatDate(id.created_at) })}
            </span>
          </li>
        ))}
      </ul>
    </Card>
  );
}

function ChangePassword() {
  const { t } = useI18n();
  const message = useErrorMessage();
  const toast = useToast();
  const { forget } = useSession();
  const navigate = useNavigate();
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const problem = next ? passwordProblem(next) : null;

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (passwordProblem(next)) return;
    setBusy(true);
    setError(null);
    try {
      await api.post("/v1/account/password", { current_password: current, new_password: next });
      // every session, this one included, is revoked
      toast.show({ tone: "success", message: t("account.password.changed") });
      forget();
      navigate("/sign-in?notice=password_changed", { replace: true });
    } catch (err) {
      setError(err);
      setBusy(false);
    }
  }

  return (
    <Card header={<h2>{t("account.password.title")}</h2>}>
      {error != null && <Banner tone="error">{message(error)}</Banner>}
      <form className="mkc-form" onSubmit={submit} noValidate>
        <Input
          label={t("field.currentPassword")}
          type="password"
          autoComplete="current-password"
          value={current}
          onChange={(e) => setCurrent(e.target.value)}
        />
        <Input
          label={t("field.newPassword")}
          type="password"
          autoComplete="new-password"
          description={t("password.policy")}
          error={problem ? t(`password.${problem}`) : undefined}
          value={next}
          onChange={(e) => setNext(e.target.value)}
        />
        <Button type="submit" loading={busy} disabled={!current || passwordProblem(next) !== null}>
          {t("account.password.submit")}
        </Button>
      </form>
    </Card>
  );
}

function Sessions() {
  const { t, formatDate } = useI18n();
  const message = useErrorMessage();
  const toast = useToast();
  const { forget } = useSession();
  const navigate = useNavigate();
  const list = useLoad(
    () => api.get<{ sessions: SessionRow[] }>("/v1/account/sessions"),
    "sessions"
  );
  const [busy, setBusy] = useState<string | null>(null);

  async function revoke(id: string) {
    setBusy(id);
    try {
      await api.delete(`/v1/account/sessions/${encodeURIComponent(id)}`);
      toast.show({ tone: "success", message: t("account.sessions.revoked") });
      list.reload();
    } catch (err) {
      toast.show({ tone: "error", message: message(err) });
    } finally {
      setBusy(null);
    }
  }

  async function revokeAll() {
    setBusy("all");
    try {
      await api.post("/v1/account/sessions/revoke-all");
      forget();
      navigate("/sign-in?notice=signed_out_everywhere", { replace: true });
    } catch (err) {
      toast.show({ tone: "error", message: message(err) });
      setBusy(null);
    }
  }

  return (
    <Card header={<h2>{t("account.sessions.title")}</h2>}>
      {list.error != null ? (
        <LoadError error={list.error} onRetry={list.reload} />
      ) : !list.data ? (
        <Loading />
      ) : (
        <>
          <ul className="mkc-list">
            {list.data.sessions.map((s) => (
              <li key={s.id} className="mkc-list__row">
                <div>
                  <strong>{s.user_agent || t("account.sessions.unknownDevice")}</strong>{" "}
                  {s.current && <Badge tone="accent">{t("account.sessions.current")}</Badge>}
                  <div className="mkc-muted">
                    {t("account.sessions.meta", {
                      ip: s.ip,
                      last: formatDate(s.last_used_at),
                    })}
                  </div>
                </div>
                {!s.current && (
                  <Button
                    size="sm"
                    variant="secondary"
                    loading={busy === s.id}
                    onClick={() => revoke(s.id)}
                    aria-label={t("account.sessions.revokeOne", {
                      device: s.user_agent || s.ip,
                    })}
                  >
                    {t("account.sessions.revoke")}
                  </Button>
                )}
              </li>
            ))}
          </ul>
          <div className="mkc-row">
            <Button variant="destructive" loading={busy === "all"} onClick={revokeAll}>
              {t("account.sessions.revokeAll")}
            </Button>
          </div>
        </>
      )}
    </Card>
  );
}

function ExportData() {
  const { t } = useI18n();
  const message = useErrorMessage();
  const toast = useToast();
  const [busy, setBusy] = useState(false);

  async function download() {
    setBusy(true);
    try {
      const res = await fetchApi("GET", "/v1/account/export");
      const blob = await res.blob();
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = "maktaba-cloud-export.json";
      document.body.appendChild(a);
      a.click();
      a.remove();
      URL.revokeObjectURL(url);
    } catch (err) {
      toast.show({ tone: "error", message: message(err) });
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card header={<h2>{t("account.export.title")}</h2>}>
      <p className="mkc-muted">{t("account.export.body")}</p>
      <Button variant="secondary" loading={busy} onClick={download}>
        {t("account.export.download")}
      </Button>
    </Card>
  );
}

function DeleteAccount({ hasPassword }: { hasPassword: boolean }) {
  const { t } = useI18n();
  const message = useErrorMessage();
  const { forget } = useSession();
  const navigate = useNavigate();
  const [open, setOpen] = useState(false);
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);

  useEffect(() => {
    if (!open) {
      setPassword("");
      setError(null);
    }
  }, [open]);

  async function confirm() {
    setBusy(true);
    setError(null);
    try {
      await api.delete("/v1/account/me", hasPassword ? { password } : {});
      forget();
      navigate("/sign-in?notice=deletion_requested", { replace: true });
    } catch (err) {
      setError(err);
      setBusy(false);
    }
  }

  return (
    <Card header={<h2>{t("account.delete.title")}</h2>}>
      <p className="mkc-muted">{t("account.delete.body")}</p>
      <Button variant="destructive" onClick={() => setOpen(true)}>
        {t("account.delete.open")}
      </Button>
      <Modal
        open={open}
        onClose={() => setOpen(false)}
        dismissable={false}
        title={t("account.delete.confirmTitle")}
        footer={
          <div className="mkc-row">
            <Button variant="secondary" onClick={() => setOpen(false)}>
              {t("common.cancel")}
            </Button>
            <Button
              variant="destructive"
              loading={busy}
              disabled={hasPassword && !password}
              onClick={confirm}
            >
              {t("account.delete.confirm")}
            </Button>
          </div>
        }
      >
        <p>{t("account.delete.confirmBody")}</p>
        {error != null && <Banner tone="error">{message(error)}</Banner>}
        {hasPassword && (
          <Input
            label={t("field.password")}
            type="password"
            autoComplete="current-password"
            required
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
        )}
      </Modal>
    </Card>
  );
}
