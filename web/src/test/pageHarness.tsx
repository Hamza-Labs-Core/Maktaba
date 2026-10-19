// Shared harness for the page tests written from web/test-cases/*.md.
//
// renderPage() mounts a page the way App.tsx does: inside RequireAuth and
// AppShell's RouteErrorBoundary, under a MemoryRouter, with a chosen user
// signed in (or none). The tiny shell around the page has a link to another
// route and a Back button, so the "page crash breaks Back" and "signed-out
// redirect" cases can be checked the same way on every page.
import type { ReactElement, ReactNode } from "react";
import { render } from "@testing-library/react";
import {
  Link,
  MemoryRouter,
  Outlet,
  Route,
  Routes,
  useLocation,
  useNavigate,
} from "react-router-dom";
import { vi } from "vitest";
import { I18nProvider } from "../lib/i18n";
import { AuthContext, RequireAuth, type AuthState, type User } from "../lib/auth";
import { RouteErrorBoundary } from "../components/RouteErrorBoundary";

export const adminUser: User = { id: "u-admin", username: "admin", is_admin: true };
export const memberUser: User = { id: "u-member", username: "member", is_admin: false };

/** An AuthState with the given user and no-op actions (override any field). */
export function authState(user: User | null, over: Partial<AuthState> = {}): AuthState {
  return {
    user,
    loading: false,
    login: vi.fn(async () => {}),
    register: vi.fn(async () => {}),
    logout: vi.fn(async () => {}),
    logoutAll: vi.fn(async () => {}),
    ...over,
  };
}

export function TestAuth({ user, children }: { user: User | null; children: ReactNode }) {
  return <AuthContext.Provider value={authState(user)}>{children}</AuthContext.Provider>;
}

function Shell() {
  const { pathname } = useLocation();
  const navigate = useNavigate();
  return (
    <>
      <nav aria-label="test shell">
        <Link to="/elsewhere">go elsewhere</Link>
        <button type="button" onClick={() => navigate(-1)}>
          go back
        </button>
      </nav>
      <RouteErrorBoundary resetKey={pathname}>
        <Outlet />
      </RouteErrorBoundary>
    </>
  );
}

function LoginProbe() {
  const { state } = useLocation();
  const from = (state as { from?: string } | null)?.from ?? "";
  return <p>login page (from {from})</p>;
}

export interface RenderPageOptions {
  /** the URL to open, e.g. "/admin/audit" or "/series/s-1" */
  path: string;
  /** the route pattern for the page, e.g. "/series/:id" (defaults to path) */
  route?: string;
  /** signed-in user; null means signed out. Defaults to adminUser. */
  user?: User | null;
  /** extra routes inside the shell, e.g. a detail page the page links to */
  extraRoutes?: ReactElement;
  /** history before `path` (lets Back leave the page) */
  history?: string[];
}

/**
 * Renders `ui` at `path` inside RequireAuth + RouteErrorBoundary.
 * - Signed out: RequireAuth sends you to "/login", shown as
 *   "login page (from <path>)".
 * - "/elsewhere" renders "elsewhere"; the "go elsewhere" link and
 *   "go back" button drive navigation.
 */
export function renderPage(ui: ReactElement, opts: RenderPageOptions) {
  const user = opts.user === undefined ? adminUser : opts.user;
  return render(
    <I18nProvider>
      <TestAuth user={user}>
        <MemoryRouter initialEntries={[...(opts.history ?? []), opts.path]}>
          <Routes>
            <Route path="/login" element={<LoginProbe />} />
            <Route
              element={
                <RequireAuth>
                  <Shell />
                </RequireAuth>
              }
            >
              <Route path={opts.route ?? opts.path.split("?")[0]} element={ui} />
              <Route path="/elsewhere" element={<p>elsewhere</p>} />
              {opts.extraRoutes}
            </Route>
          </Routes>
        </MemoryRouter>
      </TestAuth>
    </I18nProvider>
  );
}

/** Silences the expected console.error from React and the boundary in crash tests. */
export function quietCrash() {
  return vi.spyOn(console, "error").mockImplementation(() => {});
}
