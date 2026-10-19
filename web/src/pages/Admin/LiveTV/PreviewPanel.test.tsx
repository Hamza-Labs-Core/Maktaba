// Story 33.6 AC6: the Changes page's Preview tab mints the preview (POST
// /api/tv/changes/{id}/preview), plays the staging slot's HLS of a channel with the
// shared TV player (the token on the URL), switches channels, shows the staged guide
// strip, plays live next to it on request, and offers the staged M3U / XMLTV. Outside
// verifying / ready / approved it says why there is no preview.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "../../../lib/i18n";

const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock("../../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../../lib/api")>("../../../lib/api");
  return { ...actual, api: { ...actual.api, get, post } };
});

// the shared player: record what each switcher was asked to play
const { tuned } = vi.hoisted(() => ({ tuned: [] as Array<{ path: string; url: string }> }));
vi.mock("@maktaba/tvplayer", async () => {
  const React = await import("react");
  return {
    useTvSwitcher: (o: {
      channel: { id: string } | null;
      pathKey?: string;
      onSettled?: (c: unknown, from: unknown) => void;
      resolve: (c: unknown, ctx: unknown) => Promise<{ url: string }>;
    }) => {
      const [current, setCurrent] = React.useState<unknown>(null);
      const tune = React.useCallback((c: { id: string }) => {
        void o.resolve(c, { attempt: 0, signal: new AbortController().signal }).then((s) => {
          tuned.push({ path: o.pathKey ?? "", url: s.url });
          setCurrent(c);
          o.onSettled?.(c, null);
        });
      }, []);
      const id = o.channel?.id;
      React.useEffect(() => {
        if (o.channel) tune(o.channel);
      }, [id]);
      return {
        state: {
          current,
          target: null,
          phase: current ? "playing" : "idle",
          status: null,
          error: null,
          last: null,
        },
        videoRef: [() => {}, () => {}],
        activeIndex: 0,
        mode: "idle",
        reconnecting: false,
        mutedByPolicy: false,
        gestureRef: () => {},
        numberBuffer: "",
        noChannel: null,
        surf: () => {},
        tune,
        last: () => {},
        retune: () => {},
        onKeyDown: () => {},
        activeVideo: () => null,
      };
    },
  };
});

import { PreviewPanel } from "./PreviewPanel";
import { CH_HADITH, CH_QURAN, readyChange } from "./changeFixtures";
import type { Preview, TvChangeDetail } from "../../../lib/tvChanges";

const preview: Preview = {
  hls_base: "/stream/staging/channel/",
  token: "c.1.x",
  m3u: "http://mac:8081/staging/lineup.m3u?pt=c.1.x",
  xmltv: "http://mac:8081/staging/xmltv.xml?pt=c.1.x",
  expires_at: new Date(Date.now() + 3600_000).toISOString(),
  channels: [
    {
      id: CH_QURAN,
      number: 101,
      name: "Quran",
      url: `/stream/staging/channel/${CH_QURAN}/index.m3u8?pt=c.1.x`,
      live_url: `/stream/channel/${CH_QURAN}/index.m3u8`,
    },
    {
      id: CH_HADITH,
      number: 115,
      name: "Hadith",
      changed: true,
      url: `/stream/staging/channel/${CH_HADITH}/index.m3u8?pt=c.1.x`,
      live_url: `/stream/channel/${CH_HADITH}/index.m3u8`,
    },
  ],
};

function renderPanel(c: TvChangeDetail) {
  return render(
    <I18nProvider>
      <PreviewPanel change={c} />
    </I18nProvider>
  );
}

beforeEach(() => {
  tuned.length = 0;
  get.mockReset();
  post.mockReset();
  post.mockImplementation(async (path: string) => {
    if (path.endsWith("/preview")) return preview;
    throw new Error("unexpected " + path);
  });
  get.mockImplementation(async (path: string) => {
    if (path.includes("schedule-diff?channel=")) {
      return {
        channel: { id: CH_HADITH, number: 115, name: "Hadith" },
        from: "2026-06-15T20:30:00Z",
        to: "2026-06-16T20:30:00Z",
        before: [
          {
            start_at: "2026-06-15T20:30:00Z",
            end_at: "2026-06-15T21:00:00Z",
            kind: "program",
            title: "Old",
          },
        ],
        after: [
          {
            start_at: "2026-06-15T20:30:00Z",
            end_at: "2026-06-15T21:00:00Z",
            kind: "program",
            title: "Seerah 1",
          },
        ],
        staged: true,
        unchanged: false,
      };
    }
    throw new Error("unexpected " + path);
  });
});

describe("Preview tab", () => {
  it("mints the preview and plays the changed channel from staging, with the token", async () => {
    renderPanel({ ...readyChange, state: "ready" });
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith(`/api/tv/changes/${readyChange.id}/preview`)
    );
    await waitFor(() => expect(tuned.length).toBeGreaterThan(0));
    expect(tuned[0]).toEqual({ path: "staging", url: preview.channels[1]!.url });
    expect(await screen.findByText("Seerah 1")).toBeTruthy();
    expect(screen.getByTestId("preview-m3u").getAttribute("href")).toBe(preview.m3u);
    expect(screen.getByTestId("preview-xmltv").getAttribute("href")).toBe(preview.xmltv);
    // no live player until asked for
    expect(screen.queryByTestId("preview-live")).toBeNull();
  });

  it("switches channels and plays live side by side, following the staging channel", async () => {
    const user = userEvent.setup();
    renderPanel({ ...readyChange, state: "approved" });
    await screen.findByTestId("preview-ch-101");
    await user.click(screen.getByTestId("preview-ch-101"));
    await waitFor(() => expect(tuned.some((x) => x.url === preview.channels[0]!.url)).toBe(true));
    await user.click(screen.getByTestId("preview-side"));
    expect(await screen.findByTestId("preview-live")).toBeTruthy();
    await waitFor(() =>
      expect(tuned.some((x) => x.path === "live" && x.url === preview.channels[0]!.live_url)).toBe(
        true
      )
    );
    // the staging player never plays a live URL, the live one never a staging URL
    expect(
      tuned.filter((x) => x.path === "staging").every((x) => x.url.includes("/stream/staging/"))
    ).toBe(true);
    expect(tuned.filter((x) => x.path === "live").every((x) => !x.url.includes("/staging/"))).toBe(
      true
    );
  });

  it("says why there is no preview outside verifying / ready / approved", () => {
    renderPanel({ ...readyChange, state: "draft" });
    expect(screen.getByTestId("preview-off").textContent).toMatch(/verifying, ready or approved/);
    expect(post).not.toHaveBeenCalled();
  });
});
