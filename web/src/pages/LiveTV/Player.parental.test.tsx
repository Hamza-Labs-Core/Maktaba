// Epic 57 stories 57.10 and 57.15: the live channel player for a sign-in under a
// parental limit. It asks the home whether what the channel plays now may be
// watched before it tunes (GET /api/channels/{id}/access); while the answer is no
// it shows "Not available right now" and tunes nothing. A restricted account whose
// time is over gets the time's-up screen. An account with no limit asks nothing.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Routes, Route, useNavigate } from "react-router-dom";

const { get, post, del } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), del: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post, delete: del } };
});

import { Player } from "./Player";
import { I18nProvider } from "../../lib/i18n";
import { ParentalFixture, type MyParental } from "../../lib/parental";

const realCanPlayType = HTMLMediaElement.prototype.canPlayType;
const realPlay = HTMLMediaElement.prototype.play;
const realPause = HTMLMediaElement.prototype.pause;
const realLoad = HTMLMediaElement.prototype.load;

const lineup = {
  items: [
    { id: "c1", number: 1, name: "Movies", mode: "shuffle", enabled: true, sort_order: 0 },
    { id: "c2", number: 2, name: "Kids", mode: "shuffle", enabled: true, sort_order: 1 },
  ],
};
const lanInfo = { remote: { available: false, reason: "off", via_relay: false } };

let access: Record<string, unknown> | Error = { restricted: true, allowed: true, until: null };
let myTime: Record<string, unknown> = { allowed: true, limited: false, remaining_seconds: null };

function mockGet() {
  get.mockImplementation((url: string) => {
    if (url.includes("/access")) {
      return access instanceof Error ? Promise.reject(access) : Promise.resolve(access);
    }
    if (url.includes("/api/me/parental/time")) return Promise.resolve(myTime);
    if (url.includes("/api/tv/info")) return Promise.resolve(lanInfo);
    if (url.includes("/now"))
      return Promise.resolve({ server_time: new Date().toISOString(), channels: [] });
    return Promise.resolve(lineup);
  });
}

/** The address changing under the open player, as the browser's Back or a link does. */
function Address() {
  const nav = useNavigate();
  return (
    <button type="button" onClick={() => nav("/live/2")}>
      to channel 2
    </button>
  );
}

function renderPlayer(parental: Partial<MyParental>, locale: "en" | "ar" = "en") {
  localStorage.setItem("mkt:locale", locale);
  return render(
    <MemoryRouter initialEntries={["/live/1"]}>
      <I18nProvider>
        <ParentalFixture value={parental}>
          <Address />
          <Routes>
            <Route path="/live/:number" element={<Player />} />
            <Route path="/guide" element={<div>GUIDE</div>} />
          </Routes>
        </ParentalFixture>
      </I18nProvider>
    </MemoryRouter>
  );
}

const active = () => screen.getByTestId("live-video") as HTMLVideoElement;
const asked = (part: string) => get.mock.calls.filter((c) => String(c[0]).includes(part));

beforeEach(() => {
  get.mockReset();
  post.mockReset();
  del.mockReset();
  localStorage.clear();
  access = { restricted: true, allowed: true, until: null };
  myTime = { allowed: true, limited: false, remaining_seconds: null };
  HTMLMediaElement.prototype.canPlayType = function (type: string) {
    return type === "application/vnd.apple.mpegurl" ? "maybe" : "";
  } as HTMLMediaElement["canPlayType"];
  HTMLMediaElement.prototype.play = vi.fn(() => Promise.resolve());
  HTMLMediaElement.prototype.pause = vi.fn();
  HTMLMediaElement.prototype.load = vi.fn();
  vi.stubGlobal(
    "fetch",
    vi.fn(() => Promise.resolve(new Response("#EXTM3U\n", { status: 200 })))
  );
  mockGet();
});
afterEach(() => {
  // the player lets go of its video elements while they are still stubbed
  cleanup();
  HTMLMediaElement.prototype.canPlayType = realCanPlayType;
  HTMLMediaElement.prototype.play = realPlay;
  HTMLMediaElement.prototype.pause = realPause;
  HTMLMediaElement.prototype.load = realLoad;
  vi.unstubAllGlobals();
});

describe("Live player under a parental limit (57.10, 57.15)", () => {
  it("an account with no limit asks the home nothing and plays", async () => {
    renderPlayer({ restricted: false });
    await waitFor(() => expect(active().getAttribute("src")).toBe("/stream/channel/c1/index.m3u8"));
    expect(asked("/access")).toHaveLength(0);
    expect(asked("/api/me/parental/time")).toHaveLength(0);
  });

  it("a restricted account asks first, and plays once the home says yes", async () => {
    renderPlayer({ restricted: true, maxTier: 9 });
    await waitFor(() => expect(get).toHaveBeenCalledWith("/api/channels/c1/access"));
    await waitFor(() => expect(active().getAttribute("src")).toBe("/stream/channel/c1/index.m3u8"));
    expect(screen.queryByTestId("live-parental-slate")).toBeNull();
  });

  it("shows Not available right now and tunes nothing while the programme is above the limit", async () => {
    const until = new Date();
    until.setHours(23, 45, 0, 0);
    access = { restricted: true, allowed: false, until: until.toISOString() };
    renderPlayer({ restricted: true, maxTier: 9 });
    const slate = await screen.findByTestId("live-parental-slate");
    expect(slate).toHaveTextContent(
      "Not available right now. This channel comes back when the next programme starts."
    );
    expect(slate).toHaveTextContent(/Back at 11:45/);
    // nothing was tuned: no playlist was asked for, no generic "channel unavailable"
    await act(async () => {
      await new Promise((r) => setTimeout(r, 30));
    });
    expect(active().getAttribute("src")).toBeNull();
    expect(active()).not.toBeVisible();
    expect(screen.queryByTestId("live-slate")).toBeNull();
    expect(screen.getByRole("button", { name: "Open guide" })).toBeInTheDocument();
  });

  it("a sign-in limited only by its device is judged too", async () => {
    access = { restricted: true, allowed: false, until: null };
    renderPlayer({ restricted: false, maxTier: 13 });
    expect(await screen.findByTestId("live-parental-slate")).toBeInTheDocument();
    // its time is not kept: only a restricted account has time rules
    expect(asked("/api/me/parental/time")).toHaveLength(0);
  });

  it("plays when the home cannot be asked: the stream itself is the home's to refuse", async () => {
    access = new Error("network");
    renderPlayer({ restricted: true, maxTier: 9 });
    await waitFor(() => expect(active().getAttribute("src")).toBe("/stream/channel/c1/index.m3u8"));
  });

  it("shows the time's-up screen when the account's time is over", async () => {
    myTime = {
      allowed: false,
      limited: true,
      reason: "limit_reached",
      remaining_seconds: 0,
      next_allowed_at: null,
    };
    renderPlayer({ restricted: true, maxTier: 9 });
    const slate = await screen.findByTestId("live-parental-slate");
    expect(slate).toHaveTextContent("Time's up for today");
    expect(slate).toHaveTextContent("Ask a parent for more time");
    expect(active()).not.toBeVisible();
  });

  // The address changes while the player stays open (the browser's Back after
  // surfing, a link): the home is asked about that channel, and it tunes.
  it("a limited sign-in tunes the channel the address changes to, once the home says yes", async () => {
    renderPlayer({ restricted: true, maxTier: 9 });
    await waitFor(() => expect(active().getAttribute("src")).toBe("/stream/channel/c1/index.m3u8"));
    expect(asked("/api/channels/c2/access")).toHaveLength(0);
    await userEvent.click(screen.getByRole("button", { name: "to channel 2" }));
    await waitFor(() => expect(get).toHaveBeenCalledWith("/api/channels/c2/access"));
    await waitFor(() => expect(active().getAttribute("src")).toBe("/stream/channel/c2/index.m3u8"));
    expect(screen.queryByTestId("live-parental-slate")).toBeNull();
  });

  it("the channel the address changes to stays untuned while the home says no", async () => {
    renderPlayer({ restricted: true, maxTier: 9 });
    await waitFor(() => expect(active().getAttribute("src")).toBe("/stream/channel/c1/index.m3u8"));
    get.mockImplementation((url: string) => {
      if (url === "/api/channels/c2/access")
        return Promise.resolve({ restricted: true, allowed: false, until: null });
      if (url.includes("/access")) return Promise.resolve(access);
      if (url.includes("/api/me/parental/time")) return Promise.resolve(myTime);
      if (url.includes("/api/tv/info")) return Promise.resolve(lanInfo);
      if (url.includes("/now"))
        return Promise.resolve({ server_time: new Date().toISOString(), channels: [] });
      return Promise.resolve(lineup);
    });
    await userEvent.click(screen.getByRole("button", { name: "to channel 2" }));
    expect(await screen.findByTestId("live-parental-slate")).toBeInTheDocument();
    await act(async () => {
      await new Promise((r) => setTimeout(r, 30));
    });
    // channel 2's stream was never asked for
    expect(
      [...document.querySelectorAll("video")].some((v) =>
        (v.getAttribute("src") ?? "").includes("/c2/")
      )
    ).toBe(false);
  });

  it("an account with no limit tunes the channel the address changes to", async () => {
    renderPlayer({ restricted: false });
    await waitFor(() => expect(active().getAttribute("src")).toBe("/stream/channel/c1/index.m3u8"));
    await userEvent.click(screen.getByRole("button", { name: "to channel 2" }));
    await waitFor(() => expect(active().getAttribute("src")).toBe("/stream/channel/c2/index.m3u8"));
    expect(asked("/access")).toHaveLength(0);
  });

  it("is Arabic in Arabic", async () => {
    access = { restricted: true, allowed: false, until: null };
    renderPlayer({ restricted: true, maxTier: 9 }, "ar");
    expect(await screen.findByTestId("live-parental-slate")).toHaveTextContent(
      "غير متاح الآن. تعود هذه القناة عند بدء البرنامج التالي."
    );
    expect(document.documentElement).toHaveAttribute("dir", "rtl");
  });
});
