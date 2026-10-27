// /cloud-sign-in (Epic 42.9): the edition the cloud serves on a home's relay address trades
// the cloud's one-use assertion for the home's own session, and the assertion leaves the
// address bar at once.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";

const { post } = vi.hoisted(() => ({ post: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, post } };
});
const auth = vi.hoisted(() => ({ refresh: vi.fn(), user: null as null | { id: string } }));
vi.mock("../../lib/auth", () => ({ useAuth: () => auth }));

import { ApiError } from "../../lib/api";
import { I18nProvider } from "../../lib/i18n";
import en from "../../i18n/en.json";
import { CloudSignIn } from "./CloudSignIn";

function open() {
  return render(
    <I18nProvider>
      <MemoryRouter initialEntries={["/cloud-sign-in"]}>
        <Routes>
          <Route path="/cloud-sign-in" element={<CloudSignIn />} />
          <Route path="/" element={<p>the app</p>} />
        </Routes>
      </MemoryRouter>
    </I18nProvider>
  );
}

beforeEach(() => {
  post.mockReset();
  auth.refresh.mockReset();
  auth.user = null;
});
afterEach(() => window.history.replaceState(null, "", "/"));

describe("signing in through Maktaba Cloud", () => {
  it("hands the assertion to the home, then opens the app", async () => {
    window.history.replaceState(null, "", "/cloud-sign-in#assertion=mkha1.p.s");
    post.mockResolvedValue({ user: { id: "u1" } });
    auth.refresh.mockImplementation(async () => {
      auth.user = { id: "u1" };
    });
    open();
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith("/api/auth/cloud", { assertion: "mkha1.p.s" })
    );
    expect(window.location.hash).toBe("");
    expect(await screen.findByText("the app")).toBeInTheDocument();
    expect(post).toHaveBeenCalledTimes(1);
  });

  it("says when the home refuses, and offers the password", async () => {
    window.history.replaceState(null, "", "/cloud-sign-in#assertion=used");
    post.mockRejectedValue(new ApiError({ type: "x", status: 401, title: "unauthorized" }));
    open();
    expect(await screen.findByText(en["cloudSignIn.refused"])).toBeInTheDocument();
    expect(screen.getByRole("button", { name: en["cloudSignIn.password"] })).toBeInTheDocument();
    expect(auth.refresh).not.toHaveBeenCalled();
  });

  it("asks nothing of the home without an assertion", async () => {
    open();
    expect(await screen.findByText(en["cloudSignIn.missing"])).toBeInTheDocument();
    expect(post).not.toHaveBeenCalled();
  });
});
