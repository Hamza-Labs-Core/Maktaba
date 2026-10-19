// Test harness for the public auth pages (login, register, forgot and reset
// password). They sit outside AppShell and RequireAuth in App.tsx, so this
// mounts them the same way: the real AuthProvider (its /api/auth/me probe and
// login/register/logout go through the mocked lib/api), the four public
// routes, and one protected route that stands in for the signed-in app.
//
// The test file mocks "../lib/api" (or "../../lib/api") itself; this file only
// builds the tree and the canned API answers.
import type { ComponentProps, ReactElement } from "react";
import { render } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation, useNavigate } from "react-router-dom";
import { I18nProvider } from "../../lib/i18n";
import { ApiError } from "../../lib/api";
import { AuthProvider, RequireAuth, useAuth, type User } from "../../lib/auth";
import { Login } from "../Login";
import { Register } from "./Register";
import { ForgotPassword } from "./ForgotPassword";
import { ResetPassword } from "./ResetPassword";

type InitialEntry = NonNullable<ComponentProps<typeof MemoryRouter>["initialEntries"]>[number];

export const amal: User = { id: "u1", username: "amal", is_admin: false };

/** An ApiError the way lib/api builds one from a problem+json answer. */
export function problem(status: number, over: Record<string, unknown> = {}): ApiError {
  return new ApiError({ type: "about:blank", title: "Error", status, ...over } as never);
}

/** The ApiError lib/api builds for a non-JSON error body (an HTML 502). */
/** What lib/api throws for a proxy's HTML 502 page (the HTML is not kept). */
export function htmlError(): ApiError {
  return new ApiError({ type: "about:blank", title: "Bad Gateway", status: 502 });
}

/**
 * A GET mock for the calls the public pages make: /api/auth/me answers the
 * signed-in user (401 when null) and /api/cluster/self fails (no cluster).
 */
export function publicGet(me: User | null, over: Record<string, () => Promise<unknown>> = {}) {
  return (path: string): Promise<unknown> => {
    for (const [prefix, answer] of Object.entries(over)) {
      if (path.startsWith(prefix)) return answer();
    }
    if (path === "/api/auth/me") {
      return me ? Promise.resolve({ user: me }) : Promise.reject(problem(401));
    }
    return Promise.reject(new Error(`no mock for GET ${path}`));
  };
}

/** The signed-in app: shows where it is and who is signed in. */
function App() {
  const loc = useLocation();
  const { user } = useAuth();
  return (
    <>
      <p data-testid="where">{loc.pathname + loc.search}</p>
      <p>signed in as {user?.username}</p>
    </>
  );
}

function BackButton() {
  const nav = useNavigate();
  return (
    <button type="button" onClick={() => nav(-1)}>
      go back
    </button>
  );
}

/** Renders the public routes at the last entry (earlier entries are history). */
export function renderAuth(entries: InitialEntry[], extra?: ReactElement) {
  return render(
    <I18nProvider>
      <AuthProvider>
        <MemoryRouter initialEntries={entries}>
          <Routes>
            <Route path="/login" element={<Login />} />
            <Route path="/register" element={<Register />} />
            <Route path="/forgot-password" element={<ForgotPassword />} />
            <Route path="/reset-password" element={<ResetPassword />} />
            <Route
              path="*"
              element={
                <RequireAuth>
                  <App />
                </RequireAuth>
              }
            />
          </Routes>
          <BackButton />
          {extra}
        </MemoryRouter>
      </AuthProvider>
    </I18nProvider>
  );
}

/** A promise with its resolve/reject handles (to hold a request pending). */
export function deferred<T>() {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}
