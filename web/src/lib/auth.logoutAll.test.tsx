// "Sign out everywhere" on a machine that could end only its own sessions of the person
// (they signed in on it only while its home was away): logoutAll says "here".
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { I18nProvider } from "./i18n";

const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock("./api", async () => {
  const actual = await vi.importActual<typeof import("./api")>("./api");
  return { ...actual, api: { ...actual.api, get, post } };
});
vi.mock("./sw", () => ({ purgeApiCacheOnLogout: vi.fn().mockResolvedValue(undefined) }));

import { AuthProvider, useAuth } from "./auth";

function SignOutEverywhere() {
  const { user, logoutAll } = useAuth();
  const [scope, setScope] = useState("");
  return (
    <>
      <p>{user ? "signed in" : "signed out"}</p>
      <p data-testid="scope">{scope}</p>
      <button type="button" onClick={async () => setScope(await logoutAll())}>
        everywhere
      </button>
    </>
  );
}

function renderIt() {
  return render(
    <I18nProvider>
      <AuthProvider>
        <SignOutEverywhere />
      </AuthProvider>
    </I18nProvider>
  );
}

describe("logoutAll", () => {
  beforeEach(() => {
    get.mockReset();
    post.mockReset();
    get.mockResolvedValue({ user: { id: "u1", username: "amal", is_admin: false } });
  });

  it("says when only this machine's sessions ended", async () => {
    post.mockResolvedValue({ signed_out: "here", code: "signed_out_here_only" });
    renderIt();
    expect(await screen.findByText("signed in")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "everywhere" }));
    expect(await screen.findByText("signed out")).toBeInTheDocument();
    expect(screen.getByTestId("scope")).toHaveTextContent("here");
    expect(post).toHaveBeenCalledWith("/api/auth/logout-all");
  });

  it("says when the home's sessions could not be checked", async () => {
    post.mockResolvedValue({ signed_out: "here", code: "signed_out_home_unchecked" });
    renderIt();
    expect(await screen.findByText("signed in")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "everywhere" }));
    expect(await screen.findByText("signed out")).toBeInTheDocument();
    expect(screen.getByTestId("scope")).toHaveTextContent("unchecked");
  });

  it("is everywhere on the home's answer (no body)", async () => {
    post.mockResolvedValue(undefined);
    renderIt();
    expect(await screen.findByText("signed in")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "everywhere" }));
    expect(await screen.findByText("signed out")).toBeInTheDocument();
    expect(screen.getByTestId("scope")).toHaveTextContent("everywhere");
  });
});
