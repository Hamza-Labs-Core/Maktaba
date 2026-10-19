// Story 31.R — the Relay tab on /admin/machines: the home's service door,
// its switch (rules section "relay") and the recent relayed calls.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "../../lib/i18n";
import { ToastProvider } from "@ds/components/Toast/Toast";
import type { RelayStatus } from "../../lib/clusterApi";
import { axeViolations } from "../../test/axe";

const { get, patch } = vi.hoisted(() => ({ get: vi.fn(), patch: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, patch } };
});

import { RelayPanel } from "./RelayPanel";

const status = (enabled = true): RelayStatus => ({
  available: true,
  enabled,
  settings: {
    enabled,
    requests_per_minute: 600,
    burst: 60,
    max_in_flight: 4,
    max_response_mb: 256,
    timeout_sec: 120,
  },
  extensions: [
    {
      name: "plex",
      configured: true,
      enabled: true,
      upstream: "192.168.1.10:32400",
      allow: [
        { method: "GET", path: "/library/sections", use: "sections" },
        { method: "DELETE", path: "/library/metadata/{key} (collections only)", use: "x" },
      ],
    },
  ],
  home_only: [{ name: "cloudflare_stream", reason: "admin operations" }],
  calls: [
    {
      ts: "2026-10-19T10:00:02Z",
      kind: "node",
      node_id: "n1",
      node_name: "Studio Mac",
      ext: "plex",
      method: "GET",
      path: "/:/prefs",
      status: 0,
      bytes: 0,
      ms: 1,
      error: "not_allowed",
    },
    {
      ts: "2026-10-19T10:00:01Z",
      kind: "node",
      node_id: "n1",
      node_name: "Studio Mac",
      ext: "plex",
      method: "GET",
      path: "/library/sections",
      status: 200,
      bytes: 2048,
      ms: 35,
    },
  ],
  totals: [{ caller: "node:n1", calls: 2, refused: 1, failed: 0, bytes: 2048 }],
});

function renderPanel() {
  return render(
    <I18nProvider>
      <ToastProvider>
        <RelayPanel />
      </ToastProvider>
    </I18nProvider>
  );
}

describe("Relay panel", () => {
  beforeEach(() => {
    localStorage.clear();
    get.mockReset();
    patch.mockReset();
  });
  afterEach(() => vi.useRealTimers());

  it("shows the door, its allow-list and the recent calls", async () => {
    get.mockResolvedValue(status());
    renderPanel();
    expect(await screen.findByText("On")).toBeInTheDocument();
    expect(get).toHaveBeenCalledWith("/api/cluster/relay");
    expect(screen.getByText(/600 calls a minute/)).toBeInTheDocument();
    expect(screen.getByText("Ready")).toBeInTheDocument();
    expect(screen.getByText(/192\.168\.1\.10:32400/)).toBeInTheDocument();
    expect(screen.getByText("Allowed calls (2)")).toBeInTheDocument();
    expect(screen.getByText(/cloudflare_stream/)).toBeInTheDocument();
    const table = screen.getByRole("table");
    const refused = within(table).getByRole("row", { name: /\/:\/prefs/ });
    expect(refused).toHaveTextContent("Not allowed");
    expect(within(table).getByRole("row", { name: /\/library\/sections/ })).toHaveTextContent(
      "200"
    );
    expect(refused).toHaveTextContent("Studio Mac");
  });

  it("switches the relay off and on through the rules", async () => {
    get.mockResolvedValueOnce(status(true)).mockResolvedValue(status(false));
    patch.mockResolvedValue({ section: "relay", changed: ["enabled"] });
    renderPanel();
    const sw = await screen.findByRole("switch", { name: "Relay extension calls for machines" });
    await userEvent.click(sw);
    expect(patch).toHaveBeenCalledWith("/api/rules/relay", { set: { enabled: false }, reset: [] });
    expect(await screen.findByText("Off: Plex stages run on the home only")).toBeInTheDocument();
    await userEvent.click(
      screen.getByRole("switch", { name: "Relay extension calls for machines" })
    );
    expect(patch).toHaveBeenLastCalledWith("/api/rules/relay", { set: {}, reset: ["enabled"] });
  });

  it("says when the home has no door", async () => {
    get.mockResolvedValue({ ...status(false), available: false, extensions: [], calls: [] });
    renderPanel();
    expect(await screen.findByText("Not available on this home")).toBeInTheDocument();
    expect(screen.queryByRole("switch")).toBeNull();
    expect(screen.getByText("No relayed calls since the home started.")).toBeInTheDocument();
  });

  it("renders in Arabic", async () => {
    localStorage.setItem("mkt:locale", "ar");
    get.mockResolvedValue(status());
    renderPanel();
    expect(await screen.findByText("وسيط الإضافات")).toBeInTheDocument();
    expect(screen.getByText("يعمل")).toBeInTheDocument();
    expect(screen.getByText("غير مسموح")).toBeInTheDocument();
  });

  it("has no axe violations", async () => {
    get.mockResolvedValue(status());
    const { container } = renderPanel();
    await screen.findByText("On");
    expect(await axeViolations(container)).toEqual([]);
  });
});
