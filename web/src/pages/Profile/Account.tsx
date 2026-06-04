// Account profile + password management (web-pages-batch2).
//
// Real contracts:
//   GET   /api/me                  { id, username, email, display_name, is_admin }
//   PATCH /api/me                  { display_name?, email? } → updated profile
//   POST  /api/me/change-password  { current_password, new_password } → 204
//
// Two independent sections: a profile form (display name + email) and a
// change-password form (current + new + confirm). Account deletion is
// intentionally absent — that is an admin-only action elsewhere.
import { type FormEvent, useEffect, useState } from "react";
import { Card } from "@ds/components/Card/Card";
import { Button } from "@ds/components/Button/Button";
import { Input } from "@ds/components/Input/Input";
import { ErrorState } from "@ds/components/ErrorState/ErrorState";
import { useToast } from "@ds/components/Toast/Toast";
import { api, ApiError } from "../../lib/api";
import { useI18n } from "../../lib/i18n";

interface Profile {
  id: string;
  username: string;
  email: string;
  display_name: string;
  is_admin: boolean;
}

function errMessage(e: unknown, t: (k: string) => string): string {
  if (e instanceof ApiError) return e.problem.detail || e.problem.title;
  return t("common.error");
}

export function Account() {
  const { t } = useI18n();
  const [profile, setProfile] = useState<Profile | null>(null);
  const [error, setError] = useState<string | null>(null);

  function load() {
    setError(null);
    api
      .get<Profile>("/api/me")
      .then(setProfile)
      .catch((e) => setError(errMessage(e, t)));
  }

  useEffect(load, []);

  return (
    <section className="mkt-page mkt-account">
      <header className="mkt-page__header">
        <h1>{t("account.title")}</h1>
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
      ) : profile === null ? (
        <p className="mkt-loading">{t("common.loading")}</p>
      ) : (
        <div className="mkt-account__grid">
          <ProfileForm profile={profile} onSaved={setProfile} />
          <PasswordForm />
        </div>
      )}
    </section>
  );
}

function ProfileForm({
  profile,
  onSaved,
}: {
  profile: Profile;
  onSaved: (p: Profile) => void;
}) {
  const { t } = useI18n();
  const toast = useToast();
  const [displayName, setDisplayName] = useState(profile.display_name);
  const [email, setEmail] = useState(profile.email);
  const [busy, setBusy] = useState(false);

  const dirty = displayName !== profile.display_name || email !== profile.email;

  async function save(e: FormEvent) {
    e.preventDefault();
    if (!dirty) return;
    setBusy(true);
    try {
      const updated = await api.patch<Profile>("/api/me", {
        display_name: displayName,
        email,
      });
      onSaved(updated);
      toast.show({ tone: "success", message: t("account.saved") });
    } catch (err) {
      toast.show({ tone: "error", message: errMessage(err, t) });
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card header={<strong>{t("account.profile")}</strong>}>
      <form className="mkt-form" onSubmit={save}>
        <Input label={t("account.username")} value={profile.username} disabled readOnly />
        <Input
          label={t("account.displayName")}
          value={displayName}
          onChange={(e) => setDisplayName(e.target.value)}
          autoComplete="name"
        />
        <Input
          type="email"
          label={t("register.email")}
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          autoComplete="email"
        />
        <Button type="submit" loading={busy} disabled={!dirty}>
          {t("common.save")}
        </Button>
      </form>
    </Card>
  );
}

function PasswordForm() {
  const { t } = useI18n();
  const toast = useToast();
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [confirm, setConfirm] = useState("");
  const [busy, setBusy] = useState(false);

  const mismatch = confirm !== "" && next !== confirm;
  const valid = current !== "" && next.length >= 8 && !mismatch;

  async function save(e: FormEvent) {
    e.preventDefault();
    if (!valid) return;
    setBusy(true);
    try {
      await api.post("/api/me/change-password", {
        current_password: current,
        new_password: next,
      });
      setCurrent("");
      setNext("");
      setConfirm("");
      toast.show({ tone: "success", message: t("account.passwordChanged") });
    } catch (err) {
      toast.show({ tone: "error", message: errMessage(err, t) });
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card header={<strong>{t("account.changePassword")}</strong>}>
      <form className="mkt-form" onSubmit={save}>
        <Input
          type="password"
          label={t("account.currentPassword")}
          value={current}
          onChange={(e) => setCurrent(e.target.value)}
          autoComplete="current-password"
          required
        />
        <Input
          type="password"
          label={t("account.newPassword")}
          value={next}
          onChange={(e) => setNext(e.target.value)}
          autoComplete="new-password"
          minLength={8}
          required
        />
        <Input
          type="password"
          label={t("register.confirm")}
          value={confirm}
          onChange={(e) => setConfirm(e.target.value)}
          autoComplete="new-password"
          error={mismatch ? t("register.mismatch") : undefined}
          required
        />
        <Button type="submit" loading={busy} disabled={!valid}>
          {t("account.changePassword")}
        </Button>
      </form>
    </Card>
  );
}
