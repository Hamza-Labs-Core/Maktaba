// Story 31.R — the relay door on the Machines tab: always the same card,
// counters and five rows; values change in place.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, render, screen, within } from "@testing-library/react";
import { I18nProvider } from "../../lib/i18n";
import type { RelayCall, RelayStatus } from "../../lib/clusterApi";
import { axeViolations } from "../../test/axe";

const { get } = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get } };
});

import { RELAY_CARD_POLL_MS, RelayDoorCard } from "./RelayDoorCard";

const call = (path: string, extra: Partial<RelayCall> = {}): RelayCall => ({
  ts: "2026-10-19T10:00:00Z",
  kind: "node",
  node_id: "n1",
  node_name: "Studio Mac",
  ext: "plex",
  method: "GET",
  path,
  status: 200,
  bytes: 10,
  ms: 12,
  ...extra,
});

const status = (calls: RelayCall[], enabled = true): RelayStatus => ({
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
  extensions: [],
  home_only: [],
  calls,
  totals: [{ caller: "node:n1", calls: calls.length, refused: 1, failed: 0, bytes: 0 }],
});

const renderCard = () =>
  render(
    <I18nProvider>
      <RelayDoorCard />
    </I18nProvider>
  );

const bodyRows = () => within(screen.getByRole("table")).getAllByRole("row").slice(1);

describe("Relay door card", () => {
  beforeEach(() => {
    localStorage.clear();
    get.mockReset();
  });
  afterEach(() => vi.useRealTimers());

  it("has the same layout before, with and after data, updating in place", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    let resolve: (s: RelayStatus) => void = () => {};
    get.mockReturnValueOnce(new Promise<RelayStatus>((r) => (resolve = r)));
    renderCard();
    // loading: the card, its counters and five empty rows are already there
    expect(screen.getByRole("status")).toHaveTextContent("Checking…");
    expect(bodyRows()).toHaveLength(5);
    const firstRow = bodyRows()[0];
    expect(firstRow).toHaveTextContent("–");

    await act(async () => resolve(status([call("/:/prefs", { error: "not_allowed", status: 0 })])));
    expect(screen.getByRole("status")).toHaveTextContent("On");
    expect(bodyRows()).toHaveLength(5);
    expect(bodyRows()[0]).toBe(firstRow); // the same element, new values
    expect(firstRow).toHaveTextContent("Not allowed");
    expect(firstRow).toHaveTextContent("Studio Mac");

    get.mockResolvedValue(status([call("/library/sections"), call("/:/prefs")], false));
    await act(async () => {
      vi.advanceTimersByTime(RELAY_CARD_POLL_MS);
    });
    expect(await screen.findByText("Off")).toBeInTheDocument();
    expect(bodyRows()).toHaveLength(5);
    expect(bodyRows()[0]).toBe(firstRow);
    expect(firstRow).toHaveTextContent("GET /library/sections");
    const counts = screen.getByText("Calls").parentElement as HTMLElement;
    expect(counts).toHaveTextContent("2");
  });

  it("keeps its layout when the status cannot be read", async () => {
    get.mockRejectedValue(new Error("down"));
    renderCard();
    expect(await screen.findByText("Status unknown")).toBeInTheDocument();
    expect(bodyRows()).toHaveLength(5);
  });

  it("renders in Arabic without axe violations", async () => {
    localStorage.setItem("mkt:locale", "ar");
    get.mockResolvedValue(status([call("/identity")]));
    const { container } = renderCard();
    expect(await screen.findByText("يعمل")).toBeInTheDocument();
    expect(screen.getByText("الطلبات")).toBeInTheDocument();
    expect(await axeViolations(container)).toEqual([]);
  });
});
