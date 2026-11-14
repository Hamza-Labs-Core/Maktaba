// Maktaba's operator console (Epic 50 stories 50.32, 50.33): its own bundle, served on
// admin.maktaba.tv only, so no admin code reaches a user's browser. It shares the cloud apps'
// design system, i18n provider and page pieces (web/cloud/src/shared); the user app has no route
// or link to it. Staff sign in with their own staff accounts (story 50.33), never a cloud
// account: email and password, then a code from an authenticator app or a recovery code, on a
// short session (10-minute token, 15 minutes idle, 8 hours at most) kept by session.tsx. The API
// stays the authority: every /v1/admin/* call checks the staff session, its MFA and its role.
import type { ReactNode } from "react";
import { BrowserRouter, Link, Outlet, Route, Routes } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { ToastProvider } from "@ds/components/Toast/Toast";
import { I18nProvider, useI18n } from "@cloud-shared/i18n";
import { Banner, Loading, Page } from "@cloud-shared/ui";
import { MESSAGES } from "./catalogs";
import { StaffSessionProvider, useStaff } from "./session";
import { AdminLayout } from "./pages/AdminLayout";
import { AdminOverview } from "./pages/Overview";
import { AdminAccounts } from "./pages/Accounts";
import { AdminAccountDetail } from "./pages/AccountDetail";
import { AdminServers } from "./pages/Servers";
import { AdminAbuse } from "./pages/Abuse";
import { AdminBroadcast } from "./pages/Broadcast";
import { AdminModeration } from "./pages/Moderation";
import { AdminAudit, StaffSignins } from "./pages/Audit";
import { AdminUsage } from "./pages/Usage";
import { AdminErrors, AdminLogs } from "./pages/Logs";
import { AdminReportDetail, AdminReports } from "./pages/Reports";
import { AdminSymbols } from "./pages/Symbols";
import { Enroll } from "./pages/Enroll";
import { StaffSessions } from "./pages/Sessions";
import { StaffSignIn } from "./pages/StaffSignIn";

export function Providers({ children }: { children: ReactNode }) {
  return (
    <I18nProvider messages={MESSAGES}>
      <ToastProvider>
        <StaffSessionProvider>{children}</StaffSessionProvider>
      </ToastProvider>
    </I18nProvider>
  );
}

function Shell() {
  const { t, locale, setLocale } = useI18n();
  const { status, staff, idleWarning, signOut } = useStaff();
  const next = locale === "ar" ? "en" : "ar";
  return (
    <div className="mkc-shell">
      <header className="mkc-shell__header">
        <Link to="/" className="mkc-shell__brand">
          {t("admin.title")}
        </Link>
        <div className="mkc-row">
          {status === "signed_in" && staff && (
            <span className="mkc-muted">{t("staff.account", { email: staff.email })}</span>
          )}
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
        {status === "signed_in" && idleWarning && (
          <Banner tone="warn">{t("staff.session.idleWarning")}</Banner>
        )}
        <Outlet />
      </main>
    </div>
  );
}

function RequireSignIn() {
  const { status } = useStaff();
  if (status === "loading") return <Loading />;
  if (status === "signed_out") return <StaffSignIn />;
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
            <Route path="broadcast" element={<AdminBroadcast />} />
            <Route path="moderation" element={<AdminModeration />} />
            <Route path="audit" element={<AdminAudit />} />
            <Route path="signins" element={<StaffSignins />} />
            <Route path="sessions" element={<StaffSessions />} />
            <Route path="logs" element={<AdminLogs />} />
            <Route path="errors" element={<AdminErrors />} />
            <Route path="usage" element={<AdminUsage />} />
            <Route path="reports" element={<AdminReports />} />
            <Route path="reports/:id" element={<AdminReportDetail />} />
            <Route path="symbols" element={<AdminSymbols />} />
          </Route>
        </Route>
        <Route path="enroll" element={<Enroll />} />
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
