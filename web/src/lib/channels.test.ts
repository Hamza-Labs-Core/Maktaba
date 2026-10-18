// Client contract for the channel routes the API actually serves.
import { describe, it, expect, vi, beforeEach } from "vitest";

const { get, post, patch } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), patch: vi.fn() }));
vi.mock("./api", async () => {
  const actual = await vi.importActual<typeof import("./api")>("./api");
  return { ...actual, api: { ...actual.api, get, post, patch } };
});

import { channelBody, channelLogo, channelsApi, liveStreamUrl, tvApi } from "./channels";

describe("channels client", () => {
  beforeEach(() => {
    get.mockReset();
    post.mockReset();
    patch.mockReset();
  });

  it("builds the lazy-tuning HLS URL on the web port", () => {
    expect(liveStreamUrl("c 1")).toBe("/stream/channel/c%201/index.m3u8");
    expect("tune" in channelsApi).toBe(false);
  });

  it("regenerates through /api/channels/{id}/regenerate", async () => {
    post.mockResolvedValue({ channel_id: "c1", status: "regeneration-queued" });
    await channelsApi.regenerate("c1");
    expect(post).toHaveBeenCalledWith("/api/channels/c1/regenerate");
  });

  it("previews the schedule from the per-channel guide", async () => {
    get.mockResolvedValue({ channel: { id: "c1", number: 1, name: "A" }, blocks: [] });
    const now = new Date("2026-10-18T12:00:00Z");
    const r = await channelsApi.schedulePreview("c1", 48, now);
    const url = get.mock.calls[0][0] as string;
    expect(url.startsWith("/api/channels/c1/guide?")).toBe(true);
    const q = new URLSearchParams(url.split("?")[1]);
    expect(q.get("start")).toBe("2026-10-18T12:00:00.000Z");
    expect(q.get("end")).toBe("2026-10-20T12:00:00.000Z");
    expect(r.channels[0].channel.id).toBe("c1");
  });

  it("sends only writable fields on update", async () => {
    patch.mockResolvedValue({});
    await channelsApi.update("c1", {
      id: "c1",
      slug: "a",
      name: "A",
      number: 3,
      logo_path: "https://x/a.png",
      logo_url: "/api/channels/c1/logo",
      updated_at: "x",
      library_id: "lib",
      mode: "shuffle",
      enabled: true,
      sort_order: 0,
    });
    expect(patch).toHaveBeenCalledWith("/api/channels/c1", {
      name: "A",
      number: 3,
      logo_path: "https://x/a.png",
      mode: "shuffle",
      enabled: true,
      sort_order: 0,
    });
  });

  it("keeps library_id on create and clears a nulled logo", () => {
    const body = channelBody({ name: "A", library_id: "lib", logo_path: null, mode: "shuffle" }, [
      "name",
      "library_id",
      "logo_path",
      "mode",
    ]);
    expect(body).toEqual({ name: "A", library_id: "lib", logo_path: "", mode: "shuffle" });
  });

  it("prefers the served logo URL", () => {
    expect(channelLogo({ logo_url: "/api/channels/c1/logo", logo_path: "/srv/a.png" })).toBe(
      "/api/channels/c1/logo"
    );
    expect(channelLogo({ logo_path: "https://x/a.png" })).toBe("https://x/a.png");
    expect(channelLogo({ logo_path: "/srv/a.png" })).toBeNull();
    expect(channelLogo({})).toBeNull();
  });

  it("talks to the Live TV endpoints", async () => {
    get.mockResolvedValue({});
    patch.mockResolvedValue({});
    await tvApi.info();
    await tvApi.hdhr();
    await tvApi.updateHdhr({ enabled: true });
    expect(get).toHaveBeenCalledWith("/api/tv/info");
    expect(get).toHaveBeenCalledWith("/api/tv/hdhr");
    expect(patch).toHaveBeenCalledWith("/api/tv/hdhr", { enabled: true });
  });
});

describe("channel art client", () => {
  it("checks a picked file's type and size per kind", async () => {
    const { artFileProblem, ART_MAX_BYTES } = await import("./channels");
    const f = (type: string, size = 10) => new File([new Uint8Array(size)], "x", { type });
    expect(artFileProblem("logo", f("image/svg+xml"))).toBeNull();
    expect(artFileProblem("watermark", f("image/svg+xml"))).toBe("type");
    expect(artFileProblem("loading", f("image/gif"))).toBeNull();
    expect(artFileProblem("loading", f("text/plain"))).toBe("type");
    expect(artFileProblem("logo", f("image/png", ART_MAX_BYTES + 1))).toBe("size");
  });

  it("never sends the derived art block in a channel body", () => {
    const body = channelBody({
      name: "A",
      art: {
        logo: { overridden: true },
        watermark: { overridden: false, position: "top-right", width_pct: 12, margin_pct: 2 },
        loading: { overridden: false },
      },
    });
    expect("art" in body).toBe(false);
  });

  it("talks to /api/channels/{id}/art/{kind}", async () => {
    const { channelArtApi } = await import("./channels");
    patch.mockResolvedValue({});
    await channelArtApi.watermark("c 1", { position: "bottom-left" });
    expect(patch).toHaveBeenCalledWith("/api/channels/c%201/art/watermark", {
      position: "bottom-left",
    });
  });
});
