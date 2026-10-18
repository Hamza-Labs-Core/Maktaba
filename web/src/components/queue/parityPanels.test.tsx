import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { get, put } = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, put } };
});

import type { ExplainResponse, Insights, Pulse, SeasonStyle } from "../../lib/queueApi";
import { ExplainPanel } from "./ExplainPanel";
import { InsightsPanel } from "./InsightsPanel";
import { PulseBanner } from "./PulseBanner";
import { StylePanel } from "./StylePanel";
import { renderAt, routeGet } from "../../test/queueHarness";
import { librariesFixture } from "../../test/fixtures/queues";

const SEASON = "11111111-1111-4111-8111-111111111111";
const SERIES = "33333333-3333-4333-8333-333333333333";

const explained: ExplainResponse = {
  q: "طلع",
  limit: 30,
  remote: [
    {
      youtube_id: "yt-remote",
      title: "طلع البدر الجزء الثاني",
      aired: "2020-02-01",
      date_quality: "approximate",
      duration_sec: 600,
      tab: "videos",
      playlists: [{ title: "شمائل", position: 3 }],
    },
  ],
  videos: [
    {
      id: "44444444-4444-4444-8444-444444444444",
      title: "طلع البدر",
      file: "e1.mp4",
      library_id: "lib-1",
      library: "Islamic lectures",
      aired: "2020-01-01",
      date_quality: "exact",
      duration_sec: 1500,
      width: 1920,
      height: 1080,
      youtube_id: "yt-1",
      link_method: "info_json",
      tab: "videos",
      playlists: [{ title: "سيرة", position: 2 }],
      plex_rating_key: "99123",
      verdict: {
        kind: "episode",
        label: "كنوز السيرة S2 E7",
        season_id: SEASON,
        series_id: SERIES,
        season: 2,
        episode: 7,
        attached: false,
      },
    },
  ],
};

describe("ExplainPanel", () => {
  beforeEach(() => {
    get.mockReset();
  });

  it("explains a video: air date and its quality, length, playlists, Plex item, verdict", async () => {
    routeGet(get, { "/api/explain": explained });
    renderAt(<ExplainPanel libraries={librariesFixture} />, "/queue");
    await userEvent.type(screen.getByRole("searchbox", { name: "Title text" }), "طلع");
    await userEvent.selectOptions(
      screen.getByLabelText("Library"),
      librariesFixture[0]!.library_id
    );
    await userEvent.click(screen.getByRole("button", { name: "Explain" }));
    const card = (await screen.findByRole("heading", { name: /^⁨?طلع البدر⁩?$/ })).closest("li")!;
    expect(within(card).getByText("2020-01-01 (exact)")).toBeInTheDocument();
    expect(within(card).getByText(/99123/)).toBeInTheDocument();
    expect(within(card).getByText(/yt-1 · linked via info_json/)).toBeInTheDocument();
    expect(within(card).getByText(/^.?سيرة.? #2$/)).toBeInTheDocument();
    expect(within(card).getByText(/1500|25 min/)).toHaveTextContent("1920×1080");
    expect(within(card).getByRole("link", { name: /كنوز السيرة S2 E7/ })).toHaveAttribute(
      "href",
      `/queue/items/season/${SEASON}`
    );
    expect(screen.getByText("On YouTube but not downloaded (1)")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "yt-remote" })).toHaveAttribute(
      "href",
      "https://youtu.be/yt-remote"
    );
    const url = get.mock.calls[0]![0] as string;
    expect(url).toContain(`library_id=${librariesFixture[0]!.library_id}`);
    expect(url).toContain(`q=${encodeURIComponent("طلع")}`);
  });

  it("needs two characters and says when nothing matches", async () => {
    routeGet(get, { "/api/explain": { ...explained, videos: [], remote: [] } });
    renderAt(<ExplainPanel libraries={[]} />, "/queue");
    const button = screen.getByRole("button", { name: "Explain" });
    expect(button).toBeDisabled();
    await userEvent.type(screen.getByRole("searchbox"), "x");
    expect(button).toBeDisabled();
    await userEvent.type(screen.getByRole("searchbox"), "y{Enter}");
    expect(await screen.findByText("No video matches that text.")).toBeInTheDocument();
  });

  it("shows the API's error", async () => {
    get.mockRejectedValue(new Error("boom"));
    renderAt(<ExplainPanel libraries={[]} />, "/queue");
    await userEvent.type(screen.getByRole("searchbox"), "ab{Enter}");
    expect(await screen.findByRole("alert")).toHaveTextContent("boom");
  });
});

const row = (over: Partial<SeasonStyle>): SeasonStyle => ({
  season_id: SEASON,
  series_id: SERIES,
  series: "الحبيب",
  name: "الحبيب",
  season: 1,
  library_id: "lib-1",
  library: "Islamic lectures",
  style: "talk",
  tier: 1,
  confidence: 0.8,
  override_style: null,
  ...over,
});

describe("StylePanel", () => {
  beforeEach(() => {
    get.mockReset();
    put.mockReset().mockResolvedValue({ style: "cinematic", seasons: [] });
  });

  const find = async (items: SeasonStyle[], canEdit = true) => {
    routeGet(get, { "/api/seasons/styles": { items } });
    renderAt(<StylePanel libraries={librariesFixture} canEdit={canEdit} />, "/queue");
    await userEvent.type(screen.getByRole("searchbox", { name: "Series name" }), "حبيب");
    await userEvent.click(screen.getByRole("button", { name: "Find seasons" }));
  };

  it("pins one season, saves it and says the rating was queued again", async () => {
    await find([row({}), row({ season_id: "s2", season: 2, style: null, confidence: null })]);
    const pin = await screen.findByRole("combobox", { name: "Style of الحبيب S1" });
    expect(screen.getByText("rated Studio / lecture (80%)")).toBeInTheDocument();
    expect(screen.getByText("not rated yet")).toBeInTheDocument();
    await userEvent.selectOptions(pin, "cinematic");
    expect(put).toHaveBeenCalledWith(`/api/seasons/${SEASON}/style`, { style: "cinematic" });
    expect(await screen.findByRole("status")).toHaveTextContent(
      "Pinned الحبيب as Cinematic / AI. The rating was queued again."
    );
    expect(screen.getByText("Pinned")).toBeInTheDocument();
    expect(pin).toHaveValue("cinematic");
  });

  it("clears a pin with Automatic", async () => {
    await find([row({ override_style: "talk" })]);
    const pin = await screen.findByRole("combobox", { name: "Style of الحبيب S1" });
    expect(pin).toHaveValue("talk");
    await userEvent.selectOptions(pin, "");
    expect(put).toHaveBeenCalledWith(`/api/seasons/${SEASON}/style`, { style: null });
    expect(await screen.findByRole("status")).toHaveTextContent("Cleared the pin for الحبيب");
  });

  it("sets every season of a series at once", async () => {
    await find([row({}), row({ season_id: "s2", season: 2 })]);
    await userEvent.selectOptions(
      await screen.findByRole("combobox", { name: "Set the style of every season of الحبيب" }),
      "produced"
    );
    expect(put).toHaveBeenCalledWith(`/api/series/${SERIES}/style`, { style: "produced" });
    await waitFor(() =>
      expect(
        screen
          .getAllByRole("combobox", { name: /^Style of/ })
          .map((s) => (s as HTMLSelectElement).value)
      ).toEqual(["produced", "produced"])
    );
  });

  it("a viewer can look but not pin", async () => {
    await find([row({})], false);
    expect(await screen.findByRole("combobox", { name: "Style of الحبيب S1" })).toBeDisabled();
    expect(screen.getByText("Only an admin can pin a style.")).toBeInTheDocument();
  });

  it("filters to the pinned ones and reports a failed save", async () => {
    await find([row({})]);
    put.mockRejectedValue(new Error("admin-only"));
    await userEvent.selectOptions(
      await screen.findByRole("combobox", { name: "Style of الحبيب S1" }),
      "talk"
    );
    expect(await screen.findByRole("alert")).toHaveTextContent("admin-only");
    await userEvent.click(screen.getByRole("checkbox", { name: "Pinned only" }));
    await userEvent.click(screen.getByRole("button", { name: "Find seasons" }));
    await waitFor(() =>
      expect(get.mock.calls.some((c) => String(c[0]).includes("pinned=true"))).toBe(true)
    );
  });
});

const pulse = (over: Partial<Pulse>): Pulse => ({
  state: "working",
  running: 1,
  waiting: 2,
  paused_all: false,
  last_activity_at: "2026-05-18T11:59:48Z",
  age_sec: 12,
  stuck: [],
  ...over,
});

describe("PulseBanner", () => {
  beforeEach(() => {
    get.mockReset();
  });

  it("shows the age of the last activity", async () => {
    routeGet(get, { "/api/queues/pulse": pulse({}) });
    renderAt(<PulseBanner />, "/queue");
    expect(await screen.findByText("Last activity 12 s ago")).toBeInTheDocument();
  });

  it("raises an alert and lists the jobs that stopped beating", async () => {
    routeGet(get, {
      "/api/queues/pulse": pulse({
        state: "stalled",
        age_sec: 840,
        stuck: [
          {
            job_id: 9,
            stage: "probe",
            label: "حلقة 12",
            subject_kind: "video",
            subject_id: "v-1",
            heartbeat_age_sec: 1800,
            claimed_by: "pc/1",
            stalled_after_sec: 120,
            running_for_sec: 2000,
          },
        ],
      }),
    });
    renderAt(<PulseBanner />, "/queue");
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("No activity for 14 min. It may be stuck or stopped");
    expect(within(alert).getByRole("link", { name: /حلقة 12/ })).toHaveAttribute(
      "href",
      "/queue/items/video/v-1"
    );
    expect(alert).toHaveTextContent("probe: no heartbeat for 30 min");
  });

  it("says paused, idle, and a worker that never ran", async () => {
    routeGet(get, { "/api/queues/pulse": pulse({ state: "paused", paused_all: true }) });
    const { unmount } = renderAt(<PulseBanner />, "/queue");
    expect(await screen.findByText(/Paused: nothing new starts/)).toBeInTheDocument();
    unmount();
    routeGet(get, {
      "/api/queues/pulse": pulse({ state: "idle", running: 0, waiting: 0, age_sec: null }),
    });
    const second = renderAt(<PulseBanner />, "/queue");
    expect(await screen.findByText("Nothing is running or waiting.")).toBeInTheDocument();
    second.unmount();
    routeGet(get, {
      "/api/queues/pulse": pulse({ state: "stalled", age_sec: null, last_activity_at: null }),
    });
    renderAt(<PulseBanner />, "/queue");
    expect(await screen.findByRole("alert")).toHaveTextContent("Is a worker running?");
  });

  it("renders nothing until the pulse is known", () => {
    get.mockReturnValue(new Promise(() => undefined));
    const { container } = renderAt(<PulseBanner />, "/queue");
    expect(container.querySelector(".mkt-q-pulse")).toBeNull();
  });
});

const insights: Insights = {
  totals: {
    libraries: 3,
    videos: 1200,
    videos_with_length: 1100,
    series: 40,
    seasons: 120,
    plex_collections: 99,
    dizquetv_shows: 7,
    channels: 12,
    style_pinned: 2,
    youtube_videos: 900,
    videos_without_exact_date: 5,
  },
  styles: { talk: 80, cinematic: 5 },
  genres: [
    { genre: "aqeedah", seasons: 30 },
    { genre: "seerah", seasons: 15 },
  ],
};

describe("InsightsPanel", () => {
  beforeEach(() => {
    get.mockReset();
  });

  it("shows the counters and the style and topic panels with bars scaled to the largest", async () => {
    routeGet(get, { "/api/queues/insights": insights });
    renderAt(<InsightsPanel />, "/queue");
    const region = await screen.findByRole("region", { name: "Library at a glance" });
    expect(within(region).getByText("Plex collections").nextSibling).toHaveTextContent("99");
    expect(within(region).getByText("Videos").nextSibling).toHaveTextContent("1,200");
    const styles = within(region).getByRole("list", { name: "Production style" });
    expect(within(styles).getByText("Studio / lecture")).toBeInTheDocument();
    expect(within(styles).queryByText("Produced")).toBeNull(); // none rated: not listed
    const topics = within(region).getByRole("list", { name: "Topics (seasons per main topic)" });
    const bars = topics.querySelectorAll<HTMLElement>(".mkt-q-bars__track i");
    expect(bars[0]!.style.inlineSize).toBe("100%");
    expect(bars[1]!.style.inlineSize).toBe("50%");
  });

  it("renders nothing when the API is not reachable", async () => {
    get.mockRejectedValue(new Error("down"));
    const { container } = renderAt(<InsightsPanel />, "/queue");
    await waitFor(() => expect(get).toHaveBeenCalled());
    expect(container.querySelector(".mkt-q-insights")).toBeNull();
  });
});
