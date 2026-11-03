// Story 51.11 — what the home has on: all on until it answers, the last good
// answer kept on failure, If-None-Match on every refresh and on
// capabilities_changed, a viewer's app hides what is off and an admin's says so.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import type { ServerEvent } from "./ws";

const { hub, get } = vi.hoisted(() => ({
  hub: new Set<(ev: ServerEvent) => void>(),
  get: vi.fn(),
}));
vi.mock("./ws", () => ({
  subscribe: (channel: string, fn: (ev: ServerEvent) => void) => {
    if (channel !== "home") return () => {};
    hub.add(fn);
    return () => hub.delete(fn);
  },
}));
vi.mock("./api", async () => {
  const actual = await vi.importActual<typeof import("./api")>("./api");
  return { ...actual, api: { ...actual.api, get, post: vi.fn() } };
});

import {
  CapabilitiesClient,
  CapabilitiesProvider,
  Feature,
  fetchCapabilities,
  readCapabilities,
  useFeature,
  type CapabilitiesAnswer,
} from "./capabilities";
import { I18nProvider } from "./i18n";
import { ToastProvider } from "@ds/components/Toast/Toast";
import { ShortcutProvider } from "./keyboard/shortcuts";
import { AppShell } from "../components/AppShell";
import { FeatureRoute } from "../components/FeatureRoute";
import { TestAuth, adminUser, memberUser } from "../test/pageHarness";
import type { User } from "./auth";
import { axeViolations } from "../test/axe";
import en from "../i18n/en.json";
import ar from "../i18n/ar.json";

const ALL = {
  live_tv: "on",
  transcripts: "on",
  subtitles: "on",
  semantic_search: "on",
  chapters: "on",
  youtube: "on",
  plex: "on",
  remote_access: "on",
  recommendations: "on",
};

function viewerBody(off: Record<string, "off"> = {}, etag = "c-7-1") {
  return { etag, features: { ...ALL, ...off } };
}

function adminBody(off: Record<string, "off"> = {}, etag = "c-7-1") {
  const blocks: Record<string, unknown> = {};
  for (const k of Object.keys(off))
    blocks[k] = { components: ["livetv.slots"], reason: "switched_off" };
  return { ...viewerBody(off, etag), admin: { manage_components: true, off: blocks } };
}

const ok = (body: unknown, etag: string | null = null): CapabilitiesAnswer => ({
  status: "ok",
  body,
  etag,
});

describe("readCapabilities", () => {
  it("ignores a feature it does not know and treats a missing one as on", () => {
    const c = readCapabilities({ etag: "c-1-0", features: { live_tv: "off", teleport: "off" } });
    expect(c?.features.live_tv).toBe("off");
    expect(c?.features.transcripts).toBe("on");
    expect((c?.features as Record<string, string>).teleport).toBeUndefined();
    expect(c?.admin).toBeNull();
  });

  it("reads the admin block; without one the answer is a viewer's", () => {
    const c = readCapabilities(adminBody({ live_tv: "off" }));
    expect(c?.admin?.manageComponents).toBe(true);
    expect(c?.admin?.off.live_tv).toEqual({ components: ["livetv.slots"], reason: "switched_off" });
  });

  it("refuses what is not an answer", () => {
    expect(readCapabilities(null)).toBeNull();
    expect(readCapabilities({ etag: "x" })).toBeNull();
  });
});

describe("CapabilitiesClient", () => {
  let now = 0;
  const clock = () => now;
  beforeEach(() => {
    now = 1_000_000;
  });

  // T01
  it("is all on when the first load fails, and keeps the last good answer after", async () => {
    const load = vi
      .fn<(etag: string | null) => Promise<CapabilitiesAnswer>>()
      .mockRejectedValueOnce(new Error("down"))
      .mockResolvedValueOnce(ok(viewerBody({ live_tv: "off" })))
      .mockRejectedValueOnce(new Error("down again"));
    const c = new CapabilitiesClient(load, clock, true);
    await c.refresh(0);
    expect(c.snapshot().answered).toBe(false);
    expect(c.snapshot().features.live_tv).toBe("on");
    await c.refresh(0);
    expect(c.snapshot().features.live_tv).toBe("off");
    await c.refresh(0);
    expect(c.snapshot().features.live_tv).toBe("off");
  });

  it("sends the ETag; a 304 keeps the answer", async () => {
    const load = vi
      .fn<(etag: string | null) => Promise<CapabilitiesAnswer>>()
      .mockResolvedValueOnce(ok(viewerBody({ youtube: "off" }, "c-9-0")))
      .mockResolvedValueOnce({ status: "not_modified" });
    const c = new CapabilitiesClient(load, clock, true);
    await c.refresh(0);
    await c.refresh(0);
    expect(load).toHaveBeenNthCalledWith(1, null);
    expect(load).toHaveBeenNthCalledWith(2, "c-9-0");
    expect(c.snapshot().features.youtube).toBe("off");
  });

  it("asks again only after 5 minutes", async () => {
    const load = vi.fn(async () => ok(viewerBody()));
    const c = new CapabilitiesClient(load, clock, true);
    await c.refresh(0);
    await c.refresh();
    expect(load).toHaveBeenCalledTimes(1);
    now += 5 * 60_000;
    await c.refresh();
    expect(load).toHaveBeenCalledTimes(2);
  });

  it("an older home without the route (404) is all on", async () => {
    const c = new CapabilitiesClient(async () => ({ status: "absent" }), clock, true);
    await c.refresh(0);
    expect(c.snapshot().answered).toBe(true);
    expect(Object.values(c.snapshot().features).every((v) => v === "on")).toBe(true);
  });

  it("with the build switch off asks nothing and hides nothing", async () => {
    const load = vi.fn(async () => ok(viewerBody({ live_tv: "off" })));
    const c = new CapabilitiesClient(load, clock, false);
    await c.refresh(0);
    expect(load).not.toHaveBeenCalled();
    expect(c.snapshot().features.live_tv).toBe("on");
  });

  it("sign-out goes back to all on", async () => {
    const c = new CapabilitiesClient(async () => ok(viewerBody({ live_tv: "off" })), clock, true);
    await c.refresh(0);
    c.clear();
    expect(c.snapshot().features.live_tv).toBe("on");
  });
});

describe("fetchCapabilities", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("sends If-None-Match and reads a 304 as not modified", async () => {
    const fetchMock = vi.fn(async () => new Response(null, { status: 304 }));
    vi.stubGlobal("fetch", fetchMock);
    expect(await fetchCapabilities("c-3-1")).toEqual({ status: "not_modified" });
    const init = (fetchMock.mock.calls[0] as unknown as [string, RequestInit])[1];
    expect((init.headers as Record<string, string>)["If-None-Match"]).toBe('"c-3-1"');
  });

  it("reads the ETag header of a 200, and a 404 as absent", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(
        async () =>
          new Response(JSON.stringify(viewerBody()), {
            status: 200,
            headers: { "Content-Type": "application/json", ETag: '"c-4-1"' },
          })
      )
    );
    const a = await fetchCapabilities(null);
    expect(a.status === "ok" && a.etag).toBe("c-4-1");
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => new Response("", { status: 404 }))
    );
    expect(await fetchCapabilities(null)).toEqual({ status: "absent" });
  });

  it("throws on a server error, so the last good answer stays", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => new Response("", { status: 503 }))
    );
    await expect(fetchCapabilities(null)).rejects.toThrow();
  });
});

function Probe() {
  return <p data-testid="live">{useFeature("live_tv")}</p>;
}

function wrap(ui: React.ReactNode, user: User, client: CapabilitiesClient, path = "/") {
  return render(
    <I18nProvider>
      <ToastProvider>
        <TestAuth user={user}>
          <CapabilitiesProvider client={client}>
            <MemoryRouter initialEntries={[path]}>
              <ShortcutProvider>{ui}</ShortcutProvider>
            </MemoryRouter>
          </CapabilitiesProvider>
        </TestAuth>
      </ToastProvider>
    </I18nProvider>
  );
}

describe("CapabilitiesProvider", () => {
  beforeEach(() => {
    hub.clear();
    get.mockRejectedValue(Object.assign(new Error("401"), { status: 401 }));
  });

  // T04
  it("refetches with If-None-Match on capabilities_changed", async () => {
    const load = vi
      .fn<(etag: string | null) => Promise<CapabilitiesAnswer>>()
      .mockResolvedValueOnce(ok(viewerBody({}, "c-1-1")))
      .mockResolvedValueOnce(ok(viewerBody({ live_tv: "off" }, "c-2-1")));
    const client = new CapabilitiesClient(load, Date.now, true);
    wrap(<Probe />, memberUser, client);
    await waitFor(() => expect(load).toHaveBeenCalledTimes(1));
    expect(screen.getByTestId("live")).toHaveTextContent("on");
    // the same ETag again changes nothing
    act(() =>
      hub.forEach((fn) => fn({ type: "capabilities_changed", at: "", payload: { etag: "c-1-1" } }))
    );
    expect(load).toHaveBeenCalledTimes(1);
    act(() =>
      hub.forEach((fn) => fn({ type: "capabilities_changed", at: "", payload: { etag: "c-2-1" } }))
    );
    await waitFor(() => expect(screen.getByTestId("live")).toHaveTextContent("off"));
    expect(load).toHaveBeenNthCalledWith(2, "c-1-1");
  });

  // T02
  it("a viewer's shell has no Guide while Live TV is off", async () => {
    const client = new CapabilitiesClient(
      async () => ok(viewerBody({ live_tv: "off" })),
      Date.now,
      true
    );
    wrap(<AppShell />, memberUser, client);
    await waitFor(() =>
      expect(screen.queryAllByRole("link", { name: new RegExp(en["nav.guide"]) })).toHaveLength(0)
    );
    expect(
      screen.getAllByRole("link", { name: new RegExp(en["nav.search"]) }).length
    ).toBeGreaterThan(0);
  });

  it("the shell shows the Guide until the home answers, and to an admin", async () => {
    const never = new CapabilitiesClient(() => new Promise(() => {}), Date.now, true);
    const first = wrap(<AppShell />, memberUser, never);
    expect(
      screen.getAllByRole("link", { name: new RegExp(en["nav.guide"]) }).length
    ).toBeGreaterThan(0);
    first.unmount();
    const admin = new CapabilitiesClient(
      async () => ok(adminBody({ live_tv: "off" })),
      Date.now,
      true
    );
    wrap(<AppShell />, adminUser, admin);
    await act(async () => {});
    expect(
      screen.getAllByRole("link", { name: new RegExp(en["nav.guide"]) }).length
    ).toBeGreaterThan(0);
  });

  // T03
  it("an admin opening /guide reads the note with a link to Components", async () => {
    const client = new CapabilitiesClient(
      async () => ok(adminBody({ live_tv: "off" })),
      Date.now,
      true
    );
    wrap(
      <Routes>
        <Route
          path="/guide"
          element={
            <FeatureRoute id="live_tv">
              <p>the guide</p>
            </FeatureRoute>
          }
        />
      </Routes>,
      adminUser,
      client,
      "/guide"
    );
    expect(await screen.findByText("Live TV is off on this home.")).toBeInTheDocument();
    expect(screen.queryByText("the guide")).not.toBeInTheDocument();
    expect(screen.getByRole("link", { name: en["capabilities.turnOn"] })).toHaveAttribute(
      "href",
      "/settings/components"
    );
  });

  it("a viewer's link to /live/7 lands on Home with a toast", async () => {
    const client = new CapabilitiesClient(
      async () => ok(viewerBody({ live_tv: "off" })),
      Date.now,
      true
    );
    wrap(
      <Routes>
        <Route path="/" element={<p>home page</p>} />
        <Route
          path="/live/:number"
          element={
            <FeatureRoute id="live_tv">
              <p>the player</p>
            </FeatureRoute>
          }
        />
      </Routes>,
      memberUser,
      client,
      "/live/7"
    );
    expect(await screen.findByText("home page")).toBeInTheDocument();
    expect(await screen.findByText("Live TV isn't available on this home.")).toBeInTheDocument();
  });

  it("a page already open when the feature goes off stays", async () => {
    const load = vi
      .fn<(etag: string | null) => Promise<CapabilitiesAnswer>>()
      .mockResolvedValueOnce(ok(viewerBody({}, "c-1-1")))
      .mockResolvedValueOnce(ok(viewerBody({ live_tv: "off" }, "c-2-1")));
    const client = new CapabilitiesClient(load, Date.now, true);
    wrap(
      <Routes>
        <Route path="/" element={<p>home page</p>} />
        <Route
          path="/guide"
          element={
            <FeatureRoute id="live_tv">
              <p>the guide</p>
            </FeatureRoute>
          }
        />
      </Routes>,
      memberUser,
      client,
      "/guide"
    );
    await waitFor(() => expect(load).toHaveBeenCalledTimes(1));
    await act(async () => {
      await client.refresh(0);
    });
    expect(client.snapshot().features.live_tv).toBe("off");
    expect(screen.getByText("the guide")).toBeInTheDocument();
  });

  it("<Feature> hides for a viewer and explains for an admin", async () => {
    const viewer = new CapabilitiesClient(
      async () => ok(viewerBody({ youtube: "off" })),
      Date.now,
      true
    );
    const v = wrap(
      <Feature id="youtube">
        <p>yt</p>
      </Feature>,
      memberUser,
      viewer
    );
    await waitFor(() => expect(screen.queryByText("yt")).not.toBeInTheDocument());
    expect(screen.queryByRole("note")).not.toBeInTheDocument();
    v.unmount();
    const admin = new CapabilitiesClient(
      async () => ok(adminBody({ youtube: "off" })),
      Date.now,
      true
    );
    wrap(
      <Feature id="youtube">
        <p>yt</p>
      </Feature>,
      adminUser,
      admin
    );
    expect(await screen.findByText("YouTube is off on this home.")).toBeInTheDocument();
  });

  // T05
  it("the note in Arabic, with no axe violations", async () => {
    localStorage.setItem("mkt:locale", "ar");
    try {
      const client = new CapabilitiesClient(
        async () => ok(adminBody({ live_tv: "off" })),
        Date.now,
        true
      );
      const { container } = wrap(
        <Routes>
          <Route
            path="/guide"
            element={
              <FeatureRoute id="live_tv">
                <p>the guide</p>
              </FeatureRoute>
            }
          />
        </Routes>,
        adminUser,
        client,
        "/guide"
      );
      // the feature's own sentence: «البث المباشر» is masculine, so «متوقف»
      expect(await screen.findByText(ar["capabilities.off.live_tv"])).toBeInTheDocument();
      expect(ar["capabilities.off.live_tv"]).toContain("البث المباشر متوقف ");
      expect(screen.getByRole("link", { name: ar["capabilities.turnOn"] })).toBeInTheDocument();
      expect(await axeViolations(container)).toEqual([]);
    } finally {
      localStorage.removeItem("mkt:locale");
    }
  });
});
