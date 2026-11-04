// Epic 51 story 51.11 §2: the big-screen UI keeps the same gate as the web. With Live TV
// off on the home, a viewer has no Live TV tab, no channel row on Home and nothing failed
// in its place; with it on, both are there.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
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

const { get, post, del } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), del: vi.fn() }));
vi.mock("../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../lib/api")>("../lib/api");
  return { ...actual, api: { ...actual.api, get, post, delete: del } };
});

import BigScreen from "./BigScreen";
import { resetFocusMemory } from "./focus/FocusProvider";

const E = en as Record<string, string>;

const card = (id: string) => ({
  id,
  title: `Video ${id}`,
  filename: `${id}.mp4`,
  library_id: "L1",
  duration_sec: 600,
  state: "ready",
});

const HOME = {
  continue_watching: [],
  recently_added: [card("v2")],
  recently_watched: [],
  pinned: [],
};

const NOW = {
  server_time: "2026-10-27T10:00:00Z",
  channels: [
    {
      channel: { id: "c1", number: 7, name: "Quran TV", mode: "shuffle", enabled: true },
      current: {
        channel_id: "c1",
        seq: 0,
        kind: "program",
        title: "Tafsir live",
        start: "2026-10-27T09:30:00Z",
        stop: "2026-10-27T10:30:00Z",
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

function serve(now: unknown) {
  get.mockImplementation((url: string) => {
    if (url === "/api/home") return Promise.resolve(HOME);
    if (url === "/api/channels/now")
      return now instanceof Error ? Promise.reject(now) : Promise.resolve(now);
    return Promise.reject(new Error(`unmocked GET ${url}`));
  });
}

function renderBig(client: CapabilitiesClient) {
  return render(
    <I18nProvider>
      <TestAuth user={memberUser}>
        <CapabilitiesProvider client={client}>
          <MemoryRouter initialEntries={["/big"]}>
            <Routes>
              <Route path="/big/*" element={<BigScreen />} />
            </Routes>
          </MemoryRouter>
        </CapabilitiesProvider>
      </TestAuth>
    </I18nProvider>
  );
}

const tabs = () => screen.getByRole("navigation", { name: E["big.tabs"] });

beforeEach(() => {
  get.mockReset();
  post.mockReset();
  del.mockReset().mockResolvedValue(undefined);
  resetFocusMemory();
  localStorage.clear();
  sessionStorage.clear();
  localStorage.setItem("mkt:locale", "en");
});

describe("big screen and Live TV (story 51.11)", () => {
  it("with Live TV on, a viewer has the Live TV tab and the channel row", async () => {
    serve(NOW);
    renderBig(caps("on"));
    expect(await screen.findByText("Quran TV")).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: E["won.title"] })).toBeInTheDocument();
    expect(within(tabs()).getByRole("button", { name: E["big.tab.live"] })).toBeInTheDocument();
  });

  it("with Live TV off, a viewer has no Live TV tab and no channel row", async () => {
    serve(NOW);
    const client = caps("off");
    renderBig(client);
    await screen.findByTestId("big-hero");
    await waitFor(() => expect(client.snapshot().answered).toBe(true));
    await waitFor(() =>
      expect(within(tabs()).queryByRole("button", { name: E["big.tab.live"] })).toBeNull()
    );
    expect(screen.queryByRole("heading", { name: E["won.title"] })).toBeNull();
    expect(screen.queryByText("Quran TV")).toBeNull();
    expect(within(tabs()).getByRole("button", { name: E["big.tab.home"] })).toBeInTheDocument();
  });

  it("with Live TV off, the channels read's refusal shows nothing failed", async () => {
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
    renderBig(client);
    await screen.findByTestId("big-hero");
    await waitFor(() => expect(client.snapshot().answered).toBe(true));
    expect(screen.queryByRole("heading", { name: E["won.title"] })).toBeNull();
    expect(screen.queryByRole("alert")).toBeNull();
    expect(screen.queryByRole("button", { name: E["common.retry"] })).toBeNull();
  });
});
