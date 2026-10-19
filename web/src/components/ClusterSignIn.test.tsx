// Epic 35, story 35.9 TC7: the cluster sign-in prompt on a node's Login page.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "../lib/i18n";
import type { ClusterSelf } from "../lib/clusterApi";

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

import { ClusterSignIn } from "./ClusterSignIn";

function node(over: Partial<ClusterSelf> = {}): ClusterSelf {
  return {
    role: "node",
    id: "n1",
    name: "Studio Mac",
    version: "1.5.0",
    schema_rev: 134,
    sso: true,
    home: { name: "Home NAS", web_url: "http://home.local:8780", reachable: true },
    served_at: new Date().toISOString(),
    ...over,
  };
}

function renderPrompt(returnTo?: string) {
  return render(
    <I18nProvider>
      <ClusterSignIn returnTo={returnTo} />
    </I18nProvider>
  );
}

describe("ClusterSignIn", () => {
  beforeEach(() => {
    localStorage.clear();
    get.mockReset();
    assignLocation.mockReset();
  });

  it("offers to continue with the home and goes there with the return path", async () => {
    get.mockResolvedValue(node());
    renderPrompt("/videos/42?t=10");
    const button = await screen.findByRole("button", { name: /Continue with .*Home NAS/ });
    expect(get).toHaveBeenCalledWith("/api/cluster/self");
    expect(
      screen.getByText(/You are on .*Studio Mac.*; your account lives on .*Home NAS/)
    ).toBeInTheDocument();
    await userEvent.click(button);
    expect(assignLocation).toHaveBeenCalledWith(
      `/api/cluster/sso/start?return=${encodeURIComponent("/videos/42?t=10")}`
    );
  });

  it("returns to /library by default and never to another site", async () => {
    get.mockResolvedValue(node());
    renderPrompt("//evil.example/x");
    await userEvent.click(await screen.findByRole("button", { name: /Continue with/ }));
    expect(assignLocation).toHaveBeenCalledWith("/api/cluster/sso/start?return=%2Flibrary");
  });

  it("names the home generically when it has no name", async () => {
    get.mockResolvedValue(node({ home: { web_url: "http://home.local", reachable: true } }));
    renderPrompt();
    expect(
      await screen.findByRole("button", { name: "Continue with the home" })
    ).toBeInTheDocument();
  });

  it.each([
    ["the home", node({ role: "home" })],
    ["a node without cluster sign-in", node({ sso: false })],
    ["a node that does not know its home's address", node({ home: { reachable: true } })],
  ])("shows nothing on %s", async (_, self) => {
    get.mockResolvedValue(self);
    const { container } = renderPrompt();
    await waitFor(() => expect(get).toHaveBeenCalled());
    await Promise.resolve();
    expect(container).toBeEmptyDOMElement();
  });

  it("shows nothing when /api/cluster/self cannot be read", async () => {
    get.mockRejectedValue(new Error("network"));
    const { container } = renderPrompt();
    await waitFor(() => expect(get).toHaveBeenCalled());
    expect(container).toBeEmptyDOMElement();
  });

  it("speaks Arabic", async () => {
    localStorage.setItem("mkt:locale", "ar");
    get.mockResolvedValue(node());
    renderPrompt();
    expect(
      await screen.findByRole("button", { name: /المتابعة عبر .*Home NAS/ })
    ).toBeInTheDocument();
    expect(
      screen.getByText(/أنت على .*Studio Mac.*؛ حسابك موجود على .*Home NAS/)
    ).toBeInTheDocument();
    expect(screen.getByText("أو سجّل الدخول هنا")).toBeInTheDocument();
    expect(document.documentElement.dir).toBe("rtl");
  });
});
