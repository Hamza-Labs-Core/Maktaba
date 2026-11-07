// Epic 53 story 53.06: the strip shows the highest line, "N more" for the rest, Details
// for admins; closed, it comes back when the state changes; in Arabic, Arabic.
import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";
import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { authState } = vi.hoisted(() => ({
  authState: { user: null as null | { is_admin: boolean } },
}));
vi.mock("../lib/auth", () => ({ useAuth: () => authState }));

import { I18nProvider } from "../lib/i18n";
import { setHomeStatus, type HomeStatus } from "../lib/homeStatus";
import { setCloudStatus, type CloudSummary } from "../lib/cloudStatus";
import { StatusStrip } from "./StatusStrip";

const base: HomeStatus = {
  v: 1,
  at: "2026-11-06T10:00:00Z",
  mode: "normal",
  overall: "ok",
  components: {
    database: "ok",
    api: "ok",
    streaming: "ok",
    processing: "ok",
    search: "ok",
    logs: "off",
  },
  features_down: [],
};

beforeEach(() => {
  vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response("", { status: 404 }));
  authState.user = { is_admin: false };
});
afterEach(() => {
  setHomeStatus(null);
  setCloudStatus(null);
  vi.restoreAllMocks();
});

function show(s: HomeStatus | null) {
  setHomeStatus(s);
  return render(
    <I18nProvider>
      <StatusStrip />
    </I18nProvider>
  );
}

describe("StatusStrip", () => {
  it("shows nothing when all is well", () => {
    show(base);
    expect(screen.queryByTestId("status-strip")).toBeNull();
  });

  it("says playback is down, with one more line behind a tap", async () => {
    show({
      ...base,
      overall: "degraded",
      components: { ...base.components, streaming: "down", search: "down" },
    });
    expect(screen.getByText("Playback and Live TV are unavailable right now.")).toBeInTheDocument();
    expect(screen.queryByText("Search is unavailable right now.")).toBeNull();
    await userEvent.click(screen.getByRole("button", { name: "1 more" }));
    expect(screen.getByText("Search is unavailable right now.")).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "Details" })).toBeNull();
  });

  it("gives an admin Details to the recovery page when the API is down", () => {
    authState.user = { is_admin: true };
    show({ ...base, overall: "down", components: { ...base.components, api: "restarting" } });
    expect(screen.getByText(/Maktaba is restarting/)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Details" })).toHaveAttribute("href", "/recovery");
  });

  it("closed, comes back when the state changes", async () => {
    show({ ...base, mode: "safe" });
    await userEvent.click(screen.getByRole("button", { name: "Close" }));
    expect(screen.queryByTestId("status-strip")).toBeNull();
    act(() =>
      setHomeStatus({ ...base, mode: "safe", components: { ...base.components, search: "down" } })
    );
    expect(screen.getByTestId("status-strip")).toBeInTheDocument();
  });

  it("is Arabic in Arabic", () => {
    localStorage.setItem("mkt:locale", "ar");
    show({ ...base, overall: "degraded", components: { ...base.components, streaming: "down" } });
    const strip = screen.getByTestId("status-strip");
    localStorage.removeItem("mkt:locale");
    expect(strip.textContent).toMatch(/التشغيل والبث المباشر غير متاحين الآن/);
  });

  // Epic 50 story 50.11: the cloud's line, only through the cloud
  const relayDown: CloudSummary = {
    updated_at: "2026-11-03T10:15:00Z",
    overall: "outage",
    components: { relay: "outage", api: "operational", push: "operational" },
    incident: {
      id: "2026-11-03-relay-down",
      state: "investigating",
      components: ["relay"],
      title: { en: "Remote access is down", ar: "الوصول عن بُعد متوقف" },
      url: "https://status.maktaba.tv/i/2026-11-03-relay-down",
    },
    maintenance: [],
  };
  function showCloud(home: HomeStatus | null, cloud: CloudSummary | null, viaCloud: boolean) {
    setHomeStatus(home);
    setCloudStatus(cloud);
    return render(
      <I18nProvider>
        <StatusStrip viaCloud={viaCloud} />
      </I18nProvider>
    );
  }

  it("through the cloud, says remote access is down, linking to the status page", () => {
    showCloud(base, relayDown, true);
    expect(screen.getByText(/Remote access is down. We're working on it/)).toBeInTheDocument();
    const link = screen.getByRole("link", { name: "Details" });
    expect(link).toHaveAttribute("href", "https://status.maktaba.tv/i/2026-11-03-relay-down");
    expect(link).toHaveAttribute("rel", "noopener noreferrer");
  });

  it("on the home's own network, shows nothing for the cloud", () => {
    showCloud(base, relayDown, false);
    expect(screen.queryByTestId("status-strip")).toBeNull();
  });

  it("puts the home's own trouble first, the cloud's behind a tap", async () => {
    showCloud(
      { ...base, overall: "degraded", components: { ...base.components, streaming: "down" } },
      relayDown,
      true
    );
    expect(screen.getByText("Playback and Live TV are unavailable right now.")).toBeInTheDocument();
    expect(screen.queryByText(/Remote access is down/)).toBeNull();
    await userEvent.click(screen.getByRole("button", { name: "1 more" }));
    expect(screen.getByText(/Remote access is down/)).toBeInTheDocument();
  });

  it("shows the cloud's line before the home's status answered, and none for notifications", () => {
    showCloud(null, relayDown, true);
    expect(screen.getByText(/Remote access is down/)).toBeInTheDocument();
    act(() =>
      setCloudStatus({
        ...relayDown,
        incident: null,
        components: { relay: "operational", api: "operational", push: "outage" },
      })
    );
    expect(screen.queryByTestId("status-strip")).toBeNull();
  });

  it("the cloud's line in Arabic", () => {
    localStorage.setItem("mkt:locale", "ar");
    showCloud(base, relayDown, true);
    const strip = screen.getByTestId("status-strip");
    localStorage.removeItem("mkt:locale");
    expect(strip.textContent).toMatch(/الوصول عن بُعد متوقف، ونعمل على إصلاحه/);
  });

  // Epic 50 story 50.13: a request waiting for the home behind the relay
  it("says it is reconnecting while a request waits for the home", async () => {
    const { rideOutReconnect } = await import("../lib/reconnect");
    show(base);
    expect(screen.queryByTestId("status-strip")).toBeNull();
    let release!: () => void;
    const gate = new Promise<void>((r) => (release = r));
    const answers = [
      new Response(JSON.stringify({ error: "home_reconnecting" }), { status: 503 }),
      new Response("{}", { status: 200 }),
    ];
    const done = rideOutReconnect(
      async () => answers.shift() as Response,
      () => gate
    );
    expect(await screen.findByText("Reconnecting to your home…")).toBeInTheDocument();
    release();
    await act(async () => {
      await done;
    });
    expect(screen.queryByText("Reconnecting to your home…")).toBeNull();
  });
});
