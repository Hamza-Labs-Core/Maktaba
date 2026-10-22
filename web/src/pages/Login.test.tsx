// Login: where it goes once signed in (Epic 35, design §10.5 — `?next=`).
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { I18nProvider } from "../lib/i18n";

const { get } = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock("../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../lib/api")>("../lib/api");
  return { ...actual, api: { ...actual.api, get } };
});
const { assignLocation } = vi.hoisted(() => ({ assignLocation: vi.fn() }));
vi.mock("../lib/navigation", async () => {
  const actual = await vi.importActual<typeof import("../lib/navigation")>("../lib/navigation");
  return { ...actual, assignLocation };
});
const { auth } = vi.hoisted(() => ({
  auth: {
    user: null as null | { id: string; username: string; is_admin: boolean },
    loading: false,
    login: vi.fn(),
  },
}));
vi.mock("../lib/auth", () => ({ useAuth: () => auth }));

import { Login } from "./Login";

function Where() {
  const loc = useLocation();
  return <p data-testid="where">{loc.pathname + loc.search}</p>;
}

function renderAt(url: string) {
  return render(
    <I18nProvider>
      <MemoryRouter initialEntries={[url]}>
        <Routes>
          <Route path="/login" element={<Login />} />
          <Route path="*" element={<Where />} />
        </Routes>
      </MemoryRouter>
    </I18nProvider>
  );
}

async function signIn() {
  await userEvent.type(screen.getByLabelText("Username"), "amal");
  await userEvent.type(screen.getByLabelText("Password"), "secret");
  await userEvent.click(screen.getByRole("button", { name: "Sign in" }));
}

const AUTHORIZE = "/api/cluster/sso/authorize?node=n1&challenge=abc&state=s&return=%2Flibrary";

describe("Login next", () => {
  beforeEach(() => {
    localStorage.clear();
    get.mockReset();
    get.mockRejectedValue(new Error("no cluster"));
    assignLocation.mockReset();
    auth.user = null;
    auth.login.mockReset();
    auth.login.mockResolvedValue(undefined);
  });

  it("continues the cluster sign-in with a full page load", async () => {
    renderAt(`/login?next=${encodeURIComponent(AUTHORIZE)}`);
    await signIn();
    expect(auth.login).toHaveBeenCalledWith("amal", "secret");
    expect(assignLocation).toHaveBeenCalledWith(AUTHORIZE);
  });

  it("continues at once when already signed in", () => {
    auth.user = { id: "u1", username: "amal", is_admin: false };
    renderAt(`/login?next=${encodeURIComponent(AUTHORIZE)}`);
    expect(assignLocation).toHaveBeenCalledTimes(1);
    expect(assignLocation).toHaveBeenCalledWith(AUTHORIZE);
  });

  it("opens another app path with the router", async () => {
    renderAt(`/login?next=${encodeURIComponent("/videos/42")}`);
    await signIn();
    expect(await screen.findByTestId("where")).toHaveTextContent("/videos/42");
    expect(assignLocation).not.toHaveBeenCalled();
  });

  it.each([
    "http://evil.example/api/cluster/sso/authorize",
    "//evil.example/api/cluster/sso/authorize",
    "/\\evil.example/api/cluster/sso/authorize",
    "javascript:alert(1)",
  ])("ignores %s and goes to the library", async (next) => {
    renderAt(`/login?next=${encodeURIComponent(next)}`);
    await signIn();
    expect(await screen.findByTestId("where")).toHaveTextContent("/library");
    expect(assignLocation).not.toHaveBeenCalled();
  });

  // Settings' "sign out everywhere" ended this machine's sessions only
  it("says the other sessions stay after a sign-out everywhere made here only", () => {
    render(
      <I18nProvider>
        <MemoryRouter initialEntries={[{ pathname: "/login", state: { signedOutHere: true } }]}>
          <Routes>
            <Route path="/login" element={<Login />} />
          </Routes>
        </MemoryRouter>
      </I18nProvider>
    );
    expect(screen.getByRole("note")).toHaveTextContent("You're signed out on this machine");
  });

  it("says nothing of it on an ordinary sign-in", () => {
    renderAt("/login");
    expect(screen.queryByText(/You're signed out on this machine/)).not.toBeInTheDocument();
  });
});
