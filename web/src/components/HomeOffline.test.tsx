// Epic 37 — the notice a node shows while its home is away: on a standby home (for its
// admin) it says this machine can take over, and links to where.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { I18nProvider } from "../lib/i18n";

const { get } = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock("../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../lib/api")>("../lib/api");
  return { ...actual, api: { ...actual.api, get } };
});

import { HomeOffline } from "./HomeOffline";

const answer = (emergency: unknown) => (path: string) =>
  path === "/api/cluster/emergency"
    ? emergency instanceof Error
      ? Promise.reject(emergency)
      : Promise.resolve(emergency)
    : Promise.resolve({ role: "node", home: { reachable: false }, copy: { state: "behind" } });

describe("The home-away notice", () => {
  beforeEach(() => {
    localStorage.clear();
    get.mockReset();
  });

  it("tells a standby's admin it can take over, with a link to the Machines page", async () => {
    get.mockImplementation(answer({ standby: true, ready: true, home: "a" }));
    render(
      <I18nProvider>
        <HomeOffline onBack={() => {}} />
      </I18nProvider>
    );
    const link = await screen.findByRole("link", { name: /can take over as the home/ });
    expect(link).toHaveAttribute("href", "/admin/machines");
  });

  it("says nothing of it on another machine, or to someone who is not its admin", async () => {
    get.mockImplementation(answer(new Error("403")));
    render(
      <I18nProvider>
        <HomeOffline onBack={() => {}} />
      </I18nProvider>
    );
    expect(await screen.findByText(/can't reach its home/)).toBeInTheDocument();
    expect(screen.queryByRole("link")).not.toBeInTheDocument();
  });
});
