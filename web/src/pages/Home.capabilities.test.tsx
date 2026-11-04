// Epic 51 story 51.11 §1: with Live TV off on the home, a viewer's Home has no channel
// row ("What's On Now") and nothing failed in its place; with it on, the row is there.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { ToastProvider } from "@ds/components/Toast/Toast";
import { ApiError } from "../lib/api";
import { I18nProvider } from "../lib/i18n";
import {
  CapabilitiesClient,
  CapabilitiesProvider,
  KNOWN_FEATURES,
  type CapabilitiesAnswer,
} from "../lib/capabilities";
import { TestAuth, memberUser } from "../test/pageHarness";
import en from "../i18n/en.json";

const { get } = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock("../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../lib/api")>("../lib/api");
  return { ...actual, api: { ...actual.api, get } };
});

import { Home } from "./Home";

const HOME = {
  continue_watching: [],
  recently_added: [
    {
      id: "a1",
      title: "Video a1",
      filename: "a1.mp4",
      library_id: "L1",
      duration_sec: 60,
      state: "ready",
    },
  ],
  recently_watched: [],
  pinned: [],
};

const NOW = {
  server_time: "2026-10-26T10:00:00Z",
  channels: [
    {
      channel: { id: "c1", number: 1, name: "Quran TV", mode: "shuffle", enabled: true },
      current: {
        channel_id: "c1",
        seq: 0,
        kind: "program",
        title: "Tafsir",
        start: "2026-10-26T09:30:00Z",
        stop: "2026-10-26T10:30:00Z",
      },
    },
  ],
};

function caps(liveTv: "on" | "off"): CapabilitiesClient {
  const features = Object.fromEntries(KNOWN_FEATURES.map((f) => [f, "on"]));
  features.live_tv = liveTv;
  const load = vi.fn(
    async (): Promise<CapabilitiesAnswer> => ({ status: "ok", body: { features }, etag: null })
  );
  return new CapabilitiesClient(load, () => Date.now(), true);
}

function renderHome(client: CapabilitiesClient) {
  return render(
    <I18nProvider>
      <ToastProvider>
        <TestAuth user={memberUser}>
          <CapabilitiesProvider client={client}>
            <MemoryRouter initialEntries={["/"]}>
              <Home />
            </MemoryRouter>
          </CapabilitiesProvider>
        </TestAuth>
      </ToastProvider>
    </I18nProvider>
  );
}

function serve(now: unknown) {
  get.mockImplementation((url: string) => {
    if (url === "/api/home") return Promise.resolve(HOME);
    if (url.startsWith("/api/channels/now"))
      return now instanceof Error ? Promise.reject(now) : Promise.resolve(now);
    return Promise.reject(new Error(`unmocked GET ${url}`));
  });
}

describe("Home and Live TV (story 51.11)", () => {
  beforeEach(() => {
    get.mockReset();
    localStorage.setItem("mkt:locale", "en");
  });

  it("with Live TV on, a viewer sees the channel row", async () => {
    serve(NOW);
    renderHome(caps("on"));
    expect(await screen.findByRole("region", { name: en["won.title"] })).toBeInTheDocument();
    expect(screen.getByText("Quran TV")).toBeInTheDocument();
  });

  it("with Live TV off, a viewer has no channel row, even while the channels read answers", async () => {
    serve(NOW);
    const client = caps("off");
    renderHome(client);
    await screen.findByText("Video a1");
    await waitFor(() => expect(client.snapshot().answered).toBe(true));
    await waitFor(() => expect(screen.queryByRole("region", { name: en["won.title"] })).toBeNull());
    expect(screen.queryByText("Quran TV")).toBeNull();
  });

  it("with Live TV off, nothing failed shows where the row would be", async () => {
    serve(
      new ApiError({
        type: "https://maktaba.tv/problems/component-off",
        title: "component off",
        status: 409,
        code: "component_off",
        component: "livetv",
        feature: "live_tv",
      })
    );
    const client = caps("off");
    renderHome(client);
    await screen.findByText("Video a1");
    await waitFor(() => expect(client.snapshot().answered).toBe(true));
    expect(screen.queryByRole("region", { name: en["won.title"] })).toBeNull();
    expect(screen.queryByRole("alert")).toBeNull();
    expect(screen.queryByRole("button", { name: en["common.retry"] })).toBeNull();
  });
});
