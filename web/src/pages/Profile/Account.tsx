// Account profile + password management (web-pages-batch2).
//
// Real contracts:
//   GET   /api/me                  { id, username, email, display_name, is_admin }
//   PATCH /api/me                  { display_name?, email? } → updated profile
//   POST  /api/me/change-password  { current_password, new_password } → 204
//
// Two independent sections: a profile form (display name + email) and a
// change-password form (current + new + confirm). Account deletion is
// intentionally absent — that is an admin-only action elsewhere. A return
// to the page shows the last profile at once and reads it again quietly
// (lib/usePageData).
import { type FormEvent, useState } from "react";
import { Card } from "@ds/components/Card/Card";
import { Button } from "@ds/components/Button/Button";
import { Input } from "@ds/components/Input/Input";
import { ErrorState } from "@ds/components/ErrorState/ErrorState";
import { Skeleton } from "@ds/components/Skeleton/Skeleton";
import { useToast } from "@ds/components/Toast/Toast";
import { api, ApiError } from "../../lib/api";
import { useI18n } from "../../lib/i18n";
import { TitleRow } from "../../help/HelpLink";
import { CloudLinkCard } from "./CloudLink";
import { errorText } from "../../lib/errorText";
import { usePageData } from "../../lib/usePageData";
import { forgetAnswers } from "../../lib/pageCache";
import { LinesPlaceholder, Loading } from "../../components/Placeholders";

interface Profile {
  id: string;
  username: string;
  email: string | null;
  display_name: string | null;
  is_admin: boolean;
}

function errMessage(e: unknown, t: (k: string) => string): string {
  return errorText(e, t);
}

async function loadProfile(): Promise<Profile> {
  const p = await api.get<Profile>("/api/me");
  // A misrouted proxy can answer 200 with an HTML page (the api
  // client hands back the raw string): treat anything that is not
  // a profile object as a failed load instead of rendering it.
  if (!p || typeof p !== "object") throw new Error("not a profile");
  return p;
}

/** The two forms' cards while the first read is slow: the grid's own classes. */
function AccountPlaceholder() {
  return (
    <div className="mkt-account__grid" aria-hidden="true">
      {[3, 4].map((lines) => (
        <Card key={lines} header={<Skeleton variant="text" className="mkt-ph-line" width="8rem" />}>
          <LinesPlaceholder lines={lines} />
        </Card>
      ))}
    </div>
  );
}

export function Account() {
  const { t } = useI18n();
  const {
    data: profile,
    err,
    loading,
    reload,
    setData: setProfile,
  } = usePageData<Profile>("account:profile", loadProfile);
  const error = err != null ? errMessage(err, t) : null;

  return (
    <section className="mkt-page mkt-account">
      <header className="mkt-page__header">
        <TitleRow>
          <h1>{t("account.title")}</h1>
        </TitleRow>
      </header>

      {error && profile === null ? (
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
      ) : profile === null ? (
        <Loading loading={loading}>
          <AccountPlaceholder />
        </Loading>
      ) : (
        <div className="mkt-account__grid">
          {error && (
            <div className="mkt-alert" role="alert">
              {error}
            </div>
          )}
          <ProfileForm
            profile={profile}
            onSaved={(p) => {
              // the admin's Users list shows names and emails too
              forgetAnswers("admin:users");
              setProfile(p);
            }}
          />
          <PasswordForm />
          <CloudLinkCard />
        </div>
      )}
    </section>
  );
}

function ProfileForm({ profile, onSaved }: { profile: Profile; onSaved: (p: Profile) => void }) {
  const { t } = useI18n();
  const toast = useToast();
  // null / missing fields start as "" so the inputs stay controlled.
  const savedName = profile.display_name ?? "";
  const savedEmail = profile.email ?? "";
  const [displayName, setDisplayName] = useState(savedName);
  const [email, setEmail] = useState(savedEmail);
  const [busy, setBusy] = useState(false);
  // the kept profile shows first: a quiet re-read that brings other values
  // updates the fields the user has not touched
  const [base, setBase] = useState({ name: savedName, email: savedEmail });
  if (base.name !== savedName || base.email !== savedEmail) {
    setBase({ name: savedName, email: savedEmail });
    if (displayName === base.name) setDisplayName(savedName);
    if (email === base.email) setEmail(savedEmail);
  }

  const dirty = displayName !== savedName || email !== savedEmail;

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
  // An empty confirm is not a match: only the native `required` stopped it.
  const valid = current !== "" && next.length >= 8 && confirm === next;

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
      // the home answers a wrong current password with 401 (not a sign-in that ended)
      const wrong = err instanceof ApiError && err.status === 401;
      toast.show({
        tone: "error",
        message: wrong ? t("account.wrongCurrent") : errMessage(err, t),
      });
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
