// Settings → Storage & discovery: direct access from the internet (Epic 50, story 50.15).
import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { get, post, patch } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), patch: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post, patch } };
});

import { ApiError } from "../../lib/api";
import { DirectAccessSection, directStateKey, type DirectStatus } from "./DirectAccessSection";
import { renderPage } from "../../test/pageHarness";
import { routeGet } from "../../test/queueHarness";

const rules = (enabled: boolean, mapPort = true, manualPort = 0) => ({
  sections: [
    {
      key: "direct",
      title: "Direct access",
      what: "",
      fields: [
        { name: "enabled", value: enabled },
        { name: "map_port", value: mapPort },
        { name: "manual_port", value: manualPort },
      ],
    },
  ],
});

const status = (over: Partial<DirectStatus> = {}): DirectStatus => ({
  enabled: true,
  state: "on",
  port: 32443,
  internal: 8444,
  listening: true,
  online: true,
  ...over,
});

beforeEach(() => {
  get.mockReset();
  post.mockReset();
  patch.mockReset();
});

describe("direct access", () => {
  it("says the outside check's result in the story's words", async () => {
    routeGet(get, {
      "/api/direct": status({ check: { result: "ok", port: 32443, at: "2026-11-07T10:00:00Z" } }),
      "/api/rules": rules(true),
    });
    renderPage(<DirectAccessSection pollMs={60_000} />, { path: "/settings/storage" });
    expect(
      await screen.findByText(/Reachable from the internet at port 32,443\. Checked/)
    ).toBeInTheDocument();
    expect(screen.getByRole("switch", { name: /Let apps reach this home directly/ })).toBeChecked();
  });

  it("names CGNAT, a closed port, a timeout and another home", () => {
    expect(directStateKey(status({ state: "cgnat" }))?.key).toBe("direct.state.cgnat");
    expect(directStateKey(status({ state: "mapping_failed" }))).toEqual({
      key: "direct.state.closed",
      port: 32443,
    });
    expect(directStateKey(status({ check: { result: "timeout", port: 9, at: "" } }))?.key).toBe(
      "direct.state.timeout"
    );
    expect(directStateKey(status({ check: { result: "wrong_home", port: 9, at: "" } }))?.key).toBe(
      "direct.state.wrongHome"
    );
    expect(directStateKey(status({ enabled: false, state: "off" }))).toBeNull();
    expect(directStateKey(status()))?.toEqual({ key: "direct.state.checking" });
  });

  it("turns it on through rules.direct, and Check now asks the cloud", async () => {
    let on = false;
    routeGet(get, {
      "/api/direct": () => status({ enabled: on }),
      "/api/rules": () => rules(on),
    });
    patch.mockImplementation(() => {
      on = true;
      return Promise.resolve({ section: "direct", changed: ["enabled"] });
    });
    post.mockResolvedValue(undefined);
    renderPage(<DirectAccessSection pollMs={60_000} />, { path: "/settings/storage" });
    const u = userEvent.setup();
    await u.click(await screen.findByRole("switch", { name: /Let apps reach this home directly/ }));
    expect(patch).toHaveBeenCalledWith("/api/rules/direct", { set: { enabled: true }, reset: [] });
    await u.click(await screen.findByRole("button", { name: "Check now" }));
    expect(post).toHaveBeenCalledWith("/api/direct/check", {});
  });

  it("takes a port forwarded by hand", async () => {
    routeGet(get, { "/api/direct": status(), "/api/rules": rules(true, false, 0) });
    patch.mockResolvedValue({ section: "direct", changed: ["manual_port"] });
    renderPage(<DirectAccessSection pollMs={60_000} />, { path: "/settings/storage" });
    const u = userEvent.setup();
    await u.type(await screen.findByLabelText("Public port"), "32443");
    await u.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() =>
      expect(patch).toHaveBeenCalledWith("/api/rules/direct", {
        set: { manual_port: 32443 },
        reset: [],
      })
    );
  });

  it("is hidden on a home without direct access (404)", async () => {
    get.mockImplementation((url: string) =>
      url.startsWith("/api/direct")
        ? Promise.reject(new ApiError({ type: "about:blank", title: "not found", status: 404 }))
        : Promise.resolve(rules(false))
    );
    renderPage(<DirectAccessSection pollMs={60_000} />, { path: "/settings/storage" });
    await waitFor(() => expect(get).toHaveBeenCalled());
    await waitFor(() => expect(screen.queryByTestId("direct-access")).not.toBeInTheDocument());
  });
});
