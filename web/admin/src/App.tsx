// Maktaba's operator console (Epic 50 story 50.32): its own bundle, served on admin.maktaba.tv
// only, so no admin code reaches a user's browser. It shares the cloud apps' design system,
// API client, session and i18n provider (web/cloud/src/shared); the user app has no route or
// link to it. Until story 50.33's staff sign-in, staff sign in with their cloud account here and
// the API stays the authority: every /v1/admin/* call answers 403 to anyone else.
import { useState, type FormEvent, type ReactNode } from "react";
import { BrowserRouter, Link, Outlet, Route, Routes } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { Card } from "@ds/components/Card/Card";
import { Input } from "@ds/components/Input/Input";
import { ToastProvider } from "@ds/components/Toast/Toast";
import { I18nProvider, useI18n } from "@cloud-shared/i18n";
import { SessionProvider, useSession } from "@cloud-shared/session";
import { Banner, Loading, Page, useErrorMessage } from "@cloud-shared/ui";
import { MESSAGES } from "./catalogs";
import { AdminLayout } from "./pages/AdminLayout";
import { AdminOverview } from "./pages/Overview";
import { AdminAccounts } from "./pages/Accounts";
import { AdminAccountDetail } from "./pages/AccountDetail";
import { AdminServers } from "./pages/Servers";
import { AdminAbuse } from "./pages/Abuse";
import { AdminAudit } from "./pages/Audit";
import { AdminUsage } from "./pages/Usage";
import { AdminErrors, AdminLogs } from "./pages/Logs";
import { AdminReportDetail, AdminReports } from "./pages/Reports";

export function Providers({ children }: { children: ReactNode }) {
  return (
    <I18nProvider messages={MESSAGES}>
      <ToastProvider>
        <SessionProvider>{children}</SessionProvider>
      </ToastProvider>
    </I18nProvider>
  );
}

function Shell() {
  const { t, locale, setLocale } = useI18n();
  const { status, signOut } = useSession();
  const next = locale === "ar" ? "en" : "ar";
  return (
    <div className="mkc-shell">
      <header className="mkc-shell__header">
        <Link to="/" className="mkc-shell__brand">
          {t("admin.title")}
        </Link>
        <div className="mkc-row">
          <Button variant="ghost" size="sm" lang={next} onClick={() => setLocale(next)}>
            {t("shell.switchLanguage")}
          </Button>
          {status === "signed_in" && (
            <Button variant="secondary" size="sm" onClick={() => void signOut()}>
              {t("shell.signOut")}
            </Button>
          )}
        </div>
      </header>
      <main className="mkc-shell__main">
        <Outlet />
      </main>
    </div>
  );
}

function SignIn() {
  const { t } = useI18n();
  const { signIn } = useSession();
  const message = useErrorMessage();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await signIn(email.trim(), password);
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="mkc-auth">
      <Card header={<h1 className="mkc-auth__title">{t("admin.title")}</h1>}>
        {error && <Banner tone="error">{error}</Banner>}
        <form className="mkc-form" onSubmit={(e) => void submit(e)} noValidate>
          <Input
            label={t("field.email")}
            type="email"
            autoComplete="username"
            value={email}
            onChange={(e) => setEmail(e.currentTarget.value)}
          />
          <Input
            label={t("field.password")}
            type="password"
            autoComplete="current-password"
            value={password}
            onChange={(e) => setPassword(e.currentTarget.value)}
          />
          <Button type="submit" loading={busy}>
            {t("signIn.submit")}
          </Button>
        </form>
      </Card>
    </div>
  );
}

function RequireSignIn() {
  const { status } = useSession();
  if (status === "loading") return <Loading />;
  if (status === "signed_out") return <SignIn />;
  return <Outlet />;
}

function NotFound() {
  const { t } = useI18n();
  return (
    <Page title={t("admin.notFound")}>
      <p>
        <Link to="/">{t("admin.title")}</Link>
      </p>
    </Page>
  );
}

export function AppRoutes() {
  return (
    <Routes>
      <Route element={<Shell />}>
        <Route element={<RequireSignIn />}>
          <Route element={<AdminLayout />}>
            <Route index element={<AdminOverview />} />
            <Route path="accounts" element={<AdminAccounts />} />
            <Route path="accounts/:id" element={<AdminAccountDetail />} />
            <Route path="servers" element={<AdminServers />} />
            <Route path="abuse" element={<AdminAbuse />} />
            <Route path="audit" element={<AdminAudit />} />
            <Route path="logs" element={<AdminLogs />} />
            <Route path="errors" element={<AdminErrors />} />
            <Route path="usage" element={<AdminUsage />} />
            <Route path="reports" element={<AdminReports />} />
            <Route path="reports/:id" element={<AdminReportDetail />} />
          </Route>
        </Route>
        <Route path="*" element={<NotFound />} />
      </Route>
    </Routes>
  );
}

export function App() {
  return (
    <Providers>
      <BrowserRouter>
        <AppRoutes />
      </BrowserRouter>
    </Providers>
  );
}
