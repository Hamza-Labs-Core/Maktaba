// Login: where it goes once signed in (`?next=`).
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

describe("Login next", () => {
  beforeEach(() => {
    localStorage.clear();
    get.mockReset();
    get.mockRejectedValue(new Error("nothing on this page asks the server"));
    auth.user = null;
    auth.login.mockReset();
    auth.login.mockResolvedValue(undefined);
  });

  it("opens the next app path with the router", async () => {
    renderAt(`/login?next=${encodeURIComponent("/videos/42")}`);
    await signIn();
    expect(auth.login).toHaveBeenCalledWith("amal", "secret");
    expect(await screen.findByTestId("where")).toHaveTextContent("/videos/42");
  });

  it("goes on at once when already signed in", async () => {
    auth.user = { id: "u1", username: "amal", is_admin: false };
    renderAt(`/login?next=${encodeURIComponent("/videos/42")}`);
    expect(await screen.findByTestId("where")).toHaveTextContent("/videos/42");
  });

  it.each([
    "http://evil.example/library",
    "//evil.example/library",
    "/\\evil.example/library",
    "javascript:alert(1)",
  ])("ignores %s and goes to Home", async (next) => {
    renderAt(`/login?next=${encodeURIComponent(next)}`);
    await signIn();
    expect(await screen.findByTestId("where")).toHaveTextContent(/^\/$/);
  });

  // Settings' "sign out everywhere" ended this machine's sessions only
  it("says the other sessions stay after a sign-out everywhere made here only", () => {
    render(
      <I18nProvider>
        <MemoryRouter initialEntries={[{ pathname: "/login", state: { signedOut: "here" } }]}>
          <Routes>
            <Route path="/login" element={<Login />} />
          </Routes>
        </MemoryRouter>
      </I18nProvider>
    );
    expect(screen.getByRole("note")).toHaveTextContent("You're signed out on this machine");
  });

  it("says the other sessions were not checked when the machine could not tell", () => {
    render(
      <I18nProvider>
        <MemoryRouter initialEntries={[{ pathname: "/login", state: { signedOut: "unchecked" } }]}>
          <Routes>
            <Route path="/login" element={<Login />} />
          </Routes>
        </MemoryRouter>
      </I18nProvider>
    );
    expect(screen.getByRole("note")).toHaveTextContent("It couldn't check your sessions elsewhere");
  });

  it("says nothing of it on an ordinary sign-in", () => {
    renderAt("/login");
    expect(screen.queryByText(/You're signed out on this machine/)).not.toBeInTheDocument();
  });
});
