// Maktaba Cloud web app: providers, the shell and the routes.
import type { ReactNode } from "react";
import { BrowserRouter, Link, Navigate, NavLink, Outlet, Route, Routes } from "react-router-dom";
import { useLocation } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { ToastProvider } from "@ds/components/Toast/Toast";
import { I18nProvider, useI18n } from "./i18n";
import { isServiceAdmin, SessionProvider, useSession } from "./session";
import { Loading } from "./ui";
import { SignIn } from "./pages/SignIn";
import { ForgotPassword, Register, ResetPassword, SignedIn, VerifyEmail } from "./pages/AuthFlows";
import { Account } from "./pages/Account";
import { Billing } from "./pages/Billing";
import { LinkHome } from "./pages/LinkHome";
import { AdminLayout } from "./pages/admin/AdminLayout";
import { AdminOverview } from "./pages/admin/Overview";
import { AdminAccounts } from "./pages/admin/Accounts";
import { AdminAccountDetail } from "./pages/admin/AccountDetail";
import { AdminServers } from "./pages/admin/Servers";
import { AdminAbuse } from "./pages/admin/Abuse";
import { AdminAudit } from "./pages/admin/Audit";
import { AdminUsage } from "./pages/admin/Usage";

export function Providers({ children }: { children: ReactNode }) {
  return (
    <I18nProvider>
      <ToastProvider>
        <SessionProvider>{children}</SessionProvider>
      </ToastProvider>
    </I18nProvider>
  );
}

function LanguageToggle() {
  const { t, locale, setLocale } = useI18n();
  const next = locale === "ar" ? "en" : "ar";
  return (
    <Button variant="ghost" size="sm" lang={next} onClick={() => setLocale(next)}>
      {t("shell.switchLanguage")}
    </Button>
  );
}

function Shell() {
  const { t } = useI18n();
  const { status, user, signOut } = useSession();
  const signedIn = status === "signed_in";
  return (
    <div className="mkc-shell">
      <header className="mkc-shell__header">
        <Link to={signedIn ? "/account" : "/sign-in"} className="mkc-shell__brand">
          {t("shell.brand")}
        </Link>
        {signedIn && (
          <nav className="mkc-shell__nav" aria-label={t("shell.nav")}>
            <NavLink to="/account" className="mkc-shell__link">
              {t("nav.account")}
            </NavLink>
            <NavLink to="/billing" className="mkc-shell__link">
              {t("nav.billing")}
            </NavLink>
            {isServiceAdmin(user) && (
              <NavLink to="/admin" className="mkc-shell__link">
                {t("nav.admin")}
              </NavLink>
            )}
          </nav>
        )}
        <div className="mkc-row">
          <LanguageToggle />
          {signedIn && (
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

function RequireAuth() {
  const { status } = useSession();
  const location = useLocation();
  if (status === "loading") return <Loading />;
  if (status === "signed_out") {
    return <Navigate to="/sign-in" replace state={{ from: location.pathname + location.search }} />;
  }
  return <Outlet />;
}

/** Sign-in and register are for signed-out visitors. */
function SignedOutOnly() {
  const { status } = useSession();
  if (status === "loading") return <Loading />;
  if (status === "signed_in") return <Navigate to="/account" replace />;
  return <Outlet />;
}

function Home() {
  const { status } = useSession();
  if (status === "loading") return <Loading />;
  return <Navigate to={status === "signed_in" ? "/account" : "/sign-in"} replace />;
}

export function AppRoutes() {
  return (
    <Routes>
      <Route element={<Shell />}>
        <Route index element={<Home />} />
        <Route element={<SignedOutOnly />}>
          <Route path="sign-in" element={<SignIn />} />
          <Route path="register" element={<Register />} />
        </Route>
        <Route path="verify-email" element={<VerifyEmail />} />
        <Route path="forgot-password" element={<ForgotPassword />} />
        <Route path="reset-password" element={<ResetPassword />} />
        <Route path="signed-in" element={<SignedIn />} />
        <Route element={<RequireAuth />}>
          <Route path="account" element={<Account />} />
          <Route path="billing" element={<Billing />} />
          {/* a home's Account page sends a person here to link their cloud account (42.7) */}
          <Route path="link-home" element={<LinkHome />} />
          <Route path="admin" element={<AdminLayout />}>
            <Route index element={<AdminOverview />} />
            <Route path="accounts" element={<AdminAccounts />} />
            <Route path="accounts/:id" element={<AdminAccountDetail />} />
            <Route path="servers" element={<AdminServers />} />
            <Route path="abuse" element={<AdminAbuse />} />
            <Route path="audit" element={<AdminAudit />} />
            <Route path="usage" element={<AdminUsage />} />
          </Route>
        </Route>
        <Route path="*" element={<Navigate to="/" replace />} />
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
