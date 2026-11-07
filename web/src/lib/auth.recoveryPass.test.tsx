// Epic 53 story 53.04: a signed-in admin asks for a recovery pass at sign-in and every
// 12 hours; a viewer never does.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, render, screen } from "@testing-library/react";
import { I18nProvider } from "./i18n";

const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock("./api", async () => {
  const actual = await vi.importActual<typeof import("./api")>("./api");
  return { ...actual, api: { ...actual.api, get, post } };
});
vi.mock("./sw", () => ({ purgeApiCacheOnLogout: vi.fn().mockResolvedValue(undefined) }));

import { AuthProvider, RECOVERY_PASS_EVERY_MS, useAuth } from "./auth";

function Who() {
  const { user } = useAuth();
  return <p>{user ? "signed in" : "signed out"}</p>;
}

function renderIt() {
  return render(
    <I18nProvider>
      <AuthProvider>
        <Who />
      </AuthProvider>
    </I18nProvider>
  );
}

const passCalls = () => post.mock.calls.filter((c) => c[0] === "/api/auth/recovery-pass").length;

describe("the recovery pass", () => {
  beforeEach(() => {
    get.mockReset();
    post.mockReset();
    post.mockResolvedValue({});
  });
  afterEach(() => vi.useRealTimers());

  it("an admin asks at sign-in and every 12 hours", async () => {
    get.mockResolvedValue({ user: { id: "u1", username: "amal", is_admin: true } });
    vi.useFakeTimers({ shouldAdvanceTime: true });
    renderIt();
    expect(await screen.findByText("signed in")).toBeInTheDocument();
    // the ask runs in a passive effect, then a microtask: flush both before counting
    await act(async () => {});
    expect(passCalls()).toBe(1);
    await act(async () => {
      vi.advanceTimersByTime(RECOVERY_PASS_EVERY_MS);
    });
    expect(passCalls()).toBe(2);
  });

  it("a viewer never asks", async () => {
    get.mockResolvedValue({ user: { id: "u2", username: "sara", is_admin: false } });
    renderIt();
    expect(await screen.findByText("signed in")).toBeInTheDocument();
    await act(async () => {});
    expect(passCalls()).toBe(0);
  });
});
