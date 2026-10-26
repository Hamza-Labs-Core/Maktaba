// A node that cannot reach its home (Epic 37): a reload keeps a signed-in user in
// the app (the node answers /api/auth/me from its copy of the home's database);
// a sign-in is checked on the node too, and holds on that machine only, which
// /login says. A node that cannot check one (no copy) refuses it: /login then
// says sign-in waits for the home, and once the home answers it probes the
// session again. Back with the home, a change made on such a sign-in asks for a
// sign-in with the home.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { I18nProvider } from "../lib/i18n";
import { AuthProvider, RequireAuth } from "../lib/auth";
import { HOME_STATE_HEADER, api, clearNodeUnavailable } from "../lib/api";
import { HOME_OFFLINE_POLL_MS } from "../components/HomeOffline";
import { NodeGuard } from "../components/NodeGuard";
import { Login } from "./Login";

const amal = { id: "u1", username: "amal", is_admin: false };

function answer(status: number, body: unknown, offline: boolean): Response {
  const headers: Record<string, string> = { "Content-Type": "application/json" };
  if (offline) headers[HOME_STATE_HEADER] = "unreachable";
  return new Response(JSON.stringify(body), { status, headers });
}

function renderApp(at: string) {
  return render(
    <I18nProvider>
      <NodeGuard>
        <AuthProvider>
          <MemoryRouter initialEntries={[at]}>
            <Routes>
              <Route path="/login" element={<Login />} />
              <Route
                path="/library"
                element={
                  <RequireAuth>
                    <p>the library</p>
                  </RequireAuth>
                }
              />
            </Routes>
          </MemoryRouter>
        </AuthProvider>
      </NodeGuard>
    </I18nProvider>
  );
}

async function signIn() {
  fireEvent.change(await screen.findByLabelText("Username"), { target: { value: "amal" } });
  fireEvent.change(screen.getByLabelText("Password"), { target: { value: "pw" } });
  fireEvent.click(screen.getByRole("button", { name: "Sign in" }));
}

describe("sign-in while this machine cannot reach its home", () => {
  let homeBack = false;
  let signedIn = false;
  // whether this machine can check a sign-in (it has a current copy)
  let canCheck = true;
  // the account must change its password (an admin reset it), which waits for the home
  let mustChange = false;
  beforeEach(() => {
    localStorage.clear();
    clearNodeUnavailable();
    homeBack = false;
    signedIn = false;
    canCheck = true;
    mustChange = false;
    vi.spyOn(console, "error").mockImplementation(() => {});
    vi.spyOn(console, "warn").mockImplementation(() => {});
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        if (url.endsWith("/api/cluster/self"))
          return answer(
            200,
            {
              role: "node",
              id: "n1",
              version: "1",
              home: { reachable: homeBack },
              copy: { state: "following", applied_at: new Date().toISOString() },
            },
            false
          );
        if (url.endsWith("/api/auth/login") && init?.method === "POST") {
          if (homeBack) return answer(200, { user: amal }, false);
          if (!canCheck)
            return answer(
              503,
              { title: "the home is not reachable", status: 503, code: "home_unreachable" },
              true
            );
          if (mustChange)
            return answer(
              503,
              {
                title: "the password change waits for the home",
                status: 503,
                code: "password_change_waits",
              },
              true
            );
          signedIn = true;
          return answer(200, { session: "node", user: amal }, true);
        }
        if (url.endsWith("/api/auth/me")) {
          if (signedIn)
            return answer(200, { user: amal, session: homeBack ? "home" : "node" }, !homeBack);
          return answer(401, { title: "unauthorized", status: 401 }, !homeBack);
        }
        if (url.endsWith("/api/videos/v1/rating"))
          return answer(
            401,
            { title: "sign in again", status: 401, code: "signin_required" },
            false
          );
        return answer(404, { title: "not found", status: 404 }, false);
      })
    );
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    clearNodeUnavailable();
  });

  it("a reload keeps a signed-in user in the app, under the notice", async () => {
    signedIn = true; // the node answers the probe from its copy
    renderApp("/library");
    expect(await screen.findByText("the library")).toBeInTheDocument();
    const notice = screen.getByRole("status");
    expect(notice).toHaveTextContent("This machine can't reach its home.");
    expect(await screen.findByText(/Its copy was last updated/)).toBeInTheDocument();
    expect(screen.queryByLabelText("Username")).not.toBeInTheDocument();
  });

  it("signs in on this machine, which the page says holds here only", async () => {
    renderApp("/library");
    expect(await screen.findByText(/your sign-in is checked here/)).toBeInTheDocument();
    await signIn();
    expect(await screen.findByText("the library")).toBeInTheDocument();
  });

  it("says sign-in waits for the home when this machine cannot check it, and goes on once it answers", async () => {
    canCheck = false;
    vi.useFakeTimers({ shouldAdvanceTime: true });
    renderApp("/library");
    await signIn();
    expect(
      await screen.findByRole("heading", { name: "Signing in needs the home" })
    ).toBeInTheDocument();
    expect(screen.queryByLabelText("Username")).not.toBeInTheDocument();

    // the home answers again: the form comes back, and a sign-in goes to the home
    homeBack = true;
    await act(async () => {
      await vi.advanceTimersByTimeAsync(HOME_OFFLINE_POLL_MS);
    });
    expect(await screen.findByLabelText("Username")).toBeInTheDocument();
    expect(screen.queryByText(/your sign-in is checked here/)).not.toBeInTheDocument();
    await signIn();
    expect(await screen.findByText("the library")).toBeInTheDocument();
  });

  it("says a password that must change waits for the home", async () => {
    mustChange = true;
    renderApp("/library");
    await signIn();
    expect(
      await screen.findByText(/must change its password, which needs the home/)
    ).toBeInTheDocument();
    expect(screen.getByLabelText("Username")).toBeInTheDocument();
  });

  it("asks for a sign-in with the home when a sign-in made here meets a change", async () => {
    signedIn = true;
    homeBack = true;
    renderApp("/library");
    expect(await screen.findByText("the library")).toBeInTheDocument();
    await act(async () => {
      await api.post("/api/videos/v1/rating", { rating: 5 }).catch(() => {});
    });
    expect(await screen.findByText(/Sign in again/)).toBeInTheDocument();
    expect(screen.getByLabelText("Username")).toBeInTheDocument();
  });
});
