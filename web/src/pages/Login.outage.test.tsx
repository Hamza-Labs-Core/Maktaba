// A node that cannot reach its home (PR #145): a reload keeps a signed-in
// user in the app (the node answers /api/auth/me from the home's last
// answer); signing in needs the home, so /login says so instead of a form
// that would fail, and once the home answers it probes the session again.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { I18nProvider } from "../lib/i18n";
import { AuthProvider, RequireAuth } from "../lib/auth";
import { HOME_STATE_HEADER, clearNodeUnavailable } from "../lib/api";
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

describe("sign-in while this machine cannot reach its home", () => {
  let homeBack = false;
  let signedIn = false;
  beforeEach(() => {
    localStorage.clear();
    clearNodeUnavailable();
    homeBack = false;
    signedIn = false;
    vi.spyOn(console, "error").mockImplementation(() => {});
    vi.spyOn(console, "warn").mockImplementation(() => {});
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input);
        if (url.endsWith("/api/cluster/self"))
          return answer(
            200,
            { role: "node", id: "n1", version: "1", sso: true, home: { reachable: homeBack } },
            false
          );
        if (url.endsWith("/api/auth/me")) {
          if (signedIn) return answer(200, { user: amal }, !homeBack);
          return answer(401, { title: "unauthorized", status: 401 }, !homeBack);
        }
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
    signedIn = true; // the node answers the probe from the home's last answer
    renderApp("/library");
    expect(await screen.findByText("the library")).toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent("This machine can't reach its home.");
    expect(screen.queryByLabelText("Username")).not.toBeInTheDocument();
  });

  it("says sign-in needs the home instead of a form, and goes on once it answers", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    renderApp("/login");
    expect(
      await screen.findByRole("heading", { name: "Signing in needs the home" })
    ).toBeInTheDocument();
    expect(screen.queryByLabelText("Username")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Sign in" })).not.toBeInTheDocument();

    // the home answers again, and the session the node could not vouch for holds there
    homeBack = true;
    signedIn = true;
    await act(async () => {
      await vi.advanceTimersByTimeAsync(HOME_OFFLINE_POLL_MS);
    });
    expect(await screen.findByText("the library")).toBeInTheDocument();
  });

  it("shows the form again once the home answers, when there is no session", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    renderApp("/login");
    expect(
      await screen.findByRole("heading", { name: "Signing in needs the home" })
    ).toBeInTheDocument();
    homeBack = true;
    await act(async () => {
      await vi.advanceTimersByTimeAsync(HOME_OFFLINE_POLL_MS);
    });
    expect(await screen.findByLabelText("Username")).toBeInTheDocument();
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
  });
});
