// The channel builder through the page: the list (origin badges, guide
// coverage, keyboard reorder), the editor's grid (open a slot, change its
// time and source with the search picker, split, add), filler weights, live
// broadcasts, the plan warning and "back to plan", and regenerate.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, within, fireEvent } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { I18nProvider } from "../../../lib/i18n";
import { ToastProvider } from "@ds/components/Toast/Toast";

const { get, post, patch } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), patch: vi.fn() }));
vi.mock("../../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../../lib/api")>("../../../lib/api");
  return { ...actual, api: { ...actual.api, get, post, patch } };
});
vi.mock("../../../lib/auth", () => ({
  useAuth: () => ({ user: { username: "admin", is_admin: true } }),
  useOptionalAuth: () => null,
}));

import { Channels } from "../Channels";

const NOW = Date.now();
const iso = (h: number) => new Date(NOW + h * 3_600_000).toISOString();

function planChannel(over: Record<string, unknown> = {}) {
  return {
    id: "c1",
    number: 101,
    name: "Kids",
    category: "kids",
    mode: "schedule",
    enabled: true,
    sort_order: 101,
    plan: { owned: true, edited: false },
    guide_until: iso(30),
    now_playing: { title: "Prophets 3", start_at: iso(-0.2), end_at: iso(0.2), progress: 0.5 },
    mode_config: {
      slots: [
        {
          start: "06:00",
          end: "09:00",
          source: { type: "lane", lane: "kids.weekly", shows: ["Prophets"] },
        },
      ],
      filler: {
        lists: [
          { pool: "news.topic", weight: 300, cooldown_ms: 0 },
          { pool: "p2", weight: 100, cooldown_ms: 0 },
        ],
      },
    },
    ...over,
  };
}

const manual = {
  id: "c2",
  number: 102,
  name: "Talks",
  category: "islamic",
  mode: "shuffle",
  enabled: false,
  sort_order: 0,
};

let channels: unknown[] = [];
let liveRows: unknown[] = [];

function routeGet(path: string) {
  if (path.startsWith("/api/channels/sources")) {
    const kind = new URLSearchParams(path.split("?")[1]).get("kind");
    if (kind === "series")
      return Promise.resolve({
        kind,
        items: [
          {
            kind: "series",
            id: "s1",
            name: "Stories of the Prophets",
            count: 30,
            duration_ms: 45_000_000,
          },
        ],
      });
    return Promise.resolve({ kind, items: [] });
  }
  if (path.startsWith("/api/channels/") && path.includes("/guide"))
    return Promise.resolve({
      channel: { id: "c1", number: 101, name: "Kids" },
      blocks: [
        { channel_id: "c1", kind: "program", start: iso(0), stop: iso(0.5), title: "Prophets 4" },
        { channel_id: "c1", kind: "filler", start: iso(0.5), stop: iso(0.55), title: "clip" },
      ],
    });
  if (path.startsWith("/api/channels")) return Promise.resolve({ items: channels });
  if (path.startsWith("/api/filler/pools"))
    return Promise.resolve({
      items: [
        {
          id: "p1",
          name: "News clips",
          external_key: "news.topic",
          items: [{ id: "i1", pool_id: "p1", video_id: "v", type: "filler", duration_ms: 60000 }],
        },
        { id: "p2", name: "Bumpers", items: [] },
        { id: "p3", name: "Station idents", items: [] },
      ],
    });
  if (path.startsWith("/api/rules"))
    return Promise.resolve({
      sections: [
        {
          key: "tv",
          title: "TV",
          what: "",
          fields: [
            {
              name: "live",
              kind: "rows",
              group: "",
              label: "",
              help: "",
              default: [],
              value: liveRows,
              changed: false,
            },
          ],
        },
        {
          key: "dizquetv",
          title: "dz",
          what: "",
          fields: [
            {
              name: "live",
              kind: "rows",
              group: "",
              label: "",
              help: "",
              default: [],
              value: [],
              changed: false,
            },
          ],
        },
      ],
    });
  return Promise.resolve({});
}

function renderPage() {
  return render(
    <MemoryRouter>
      <I18nProvider>
        <ToastProvider>
          <Channels />
        </ToastProvider>
      </I18nProvider>
    </MemoryRouter>
  );
}

async function openEditor(name = "Kids") {
  const user = userEvent.setup();
  renderPage();
  const row = (await screen.findByText(name)).closest("tr")!;
  await user.click(within(row).getByRole("button", { name: /^edit$/i }));
  await screen.findByRole("tablist", { name: /channel settings/i });
  return user;
}

describe("channel list", () => {
  beforeEach(() => {
    for (const f of [get, post, patch]) f.mockReset();
    get.mockImplementation(routeGet);
    channels = [planChannel(), manual];
    liveRows = [];
  });

  it("shows origin, what is on now and guide coverage", async () => {
    renderPage();
    const row = (await screen.findByText("Kids")).closest("tr")!;
    expect(within(row).getByText("From plan")).toBeInTheDocument();
    expect(within(row).getByText("Prophets 3")).toBeInTheDocument();
    expect(within(row).getByText(/^(29|30) h$/)).toBeInTheDocument();
    const other = screen.getByText("Talks").closest("tr")!;
    expect(within(other).getByText("Made by hand")).toBeInTheDocument();
  });

  it("filters by search and disables reordering while filtered", async () => {
    const user = userEvent.setup();
    renderPage();
    await screen.findByText("Kids");
    await user.type(screen.getByLabelText(/search channels/i), "talk");
    expect(screen.queryByText("Kids")).toBeNull();
    expect(screen.getByRole("button", { name: /move talks/i })).toBeDisabled();
  });

  it("reorders from the keyboard, after warning about plan channels", async () => {
    const user = userEvent.setup();
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(true);
    post.mockResolvedValue({ reordered: 2 });
    renderPage();
    await screen.findByText("Talks");
    screen.getByRole("button", { name: /move talks/i }).focus();
    await user.keyboard("{ArrowUp}");
    expect(confirm).toHaveBeenCalled();
    expect(post).toHaveBeenCalledWith("/api/channels/reorder", [
      { id: "c2", number: 101 },
      { id: "c1", number: 102 },
    ]);
    confirm.mockRestore();
  });
});

describe("channel editor", () => {
  beforeEach(() => {
    for (const f of [get, post, patch]) f.mockReset();
    get.mockImplementation(routeGet);
    channels = [planChannel(), manual];
    liveRows = [];
  });

  it("draws the grid and edits a slot's time and source", async () => {
    const user = await openEditor();
    expect(screen.getByText(/follows the lineup plan/i)).toBeInTheDocument();
    await user.click(screen.getByRole("tab", { name: /schedule/i }));
    const blocks = screen.getAllByRole("button", { name: /06:00–09:00: Prophets/ });
    expect(blocks).toHaveLength(7); // a daily slot on every day
    await user.click(blocks[0]);
    const editor = screen.getByRole("region", { name: /^slot$/i });
    fireEvent.change(within(editor).getByLabelText(/^ends$/i), { target: { value: "10:00" } });
    expect(screen.getAllByRole("button", { name: /06:00–10:00/ })).toHaveLength(7);

    await user.click(within(editor).getByRole("radio", { name: /^series$/i }));
    await user.click(
      await within(editor).findByRole("button", { name: /stories of the prophets/i })
    );
    expect(
      within(editor).getByText("Stories of the Prophets", { selector: "strong" })
    ).toBeInTheDocument();

    // the warning now says saving stops following the plan
    expect(screen.getByText(/stops following the lineup plan/i)).toBeInTheDocument();
    patch.mockResolvedValue(planChannel());
    await user.click(screen.getByRole("button", { name: /save and stop following the plan/i }));
    expect(patch).toHaveBeenCalledTimes(1);
    const [path, body] = patch.mock.calls[0] as [string, Record<string, unknown>];
    expect(path).toBe("/api/channels/c1");
    expect(Object.keys(body)).toEqual(["mode_config"]); // only what changed
    const slot = (body.mode_config as { slots: Array<Record<string, unknown>> }).slots[0];
    expect(slot).toMatchObject({
      start: "06:00",
      end: "10:00",
      source: { type: "series", series_id: "s1", order: "next" },
    });
  });

  it("splits, adds and deletes slots", async () => {
    const user = await openEditor();
    await user.click(screen.getByRole("tab", { name: /schedule/i }));
    await user.click(screen.getAllByRole("button", { name: /06:00–09:00/ })[0]);
    await user.click(screen.getByRole("button", { name: /split in two/i }));
    expect(screen.getAllByRole("button", { name: /06:00–07:30/ })).toHaveLength(7);
    expect(screen.getAllByRole("button", { name: /07:30–09:00/ })).toHaveLength(7);
    await user.click(screen.getByRole("button", { name: /delete slot/i }));
    expect(screen.queryAllByRole("button", { name: /06:00–07:30/ })).toHaveLength(0);
    await user.click(screen.getByRole("button", { name: /add slot/i }));
    expect(screen.getAllByRole("button", { name: /20:00–21:00/ })).toHaveLength(7);
  });

  it("a weekly grid puts a slot on its days", async () => {
    const user = await openEditor();
    await user.click(screen.getByRole("tab", { name: /schedule/i }));
    await user.click(screen.getByRole("radio", { name: /by day of the week/i }));
    await user.click(screen.getAllByRole("button", { name: /06:00–09:00/ })[0]);
    const days = screen.getByRole("group", { name: /^days$/i });
    for (const d of ["Tue", "Wed", "Thu", "Fri", "Sat", "Sun"]) {
      await user.click(within(days).getByRole("button", { name: d }));
    }
    expect(screen.getAllByRole("button", { name: /06:00–09:00/ })).toHaveLength(1);
    expect(screen.getByRole("button", { name: /monday 06:00–09:00/i })).toBeInTheDocument();
  });

  it("changes filler weights and adds a pool", async () => {
    const user = await openEditor();
    await user.click(screen.getByRole("tab", { name: /^filler$/i }));
    const first = screen.getByTestId("filler-list-0");
    expect(within(first).getByText("News clips")).toBeInTheDocument(); // by the plan key
    expect(within(first).getByText("75%")).toBeInTheDocument();
    fireEvent.change(within(first).getByRole("slider"), { target: { value: "100" } });
    expect(within(first).getByText("50%")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /station idents/i }));
    expect(screen.getByTestId("filler-list-2")).toBeInTheDocument();
    patch.mockResolvedValue(planChannel());
    await user.click(screen.getByRole("button", { name: /save and stop following/i }));
    const body = patch.mock.calls[0][1] as { mode_config: { filler: { lists: unknown[] } } };
    expect(body.mode_config.filler.lists).toEqual([
      { pool: "news.topic", weight: 100, cooldown_ms: 0 },
      { pool: "p2", weight: 100, cooldown_ms: 0 },
      { pool: "p3", weight: 100, cooldown_ms: 0 },
    ]);
  });

  it("saves live broadcasts through the rules, without editing the channel", async () => {
    liveRows = [{ channel: 999, url: "https://other/live", title: "Other" }];
    const user = await openEditor();
    await user.click(screen.getByRole("tab", { name: /live broadcasts/i }));
    await user.click(screen.getByRole("button", { name: /add live broadcast/i }));
    const save = screen.getByRole("button", { name: /^save$/i });
    expect(save).toBeDisabled(); // no address yet
    await user.type(
      screen.getByLabelText(/stream address/i),
      "https://www.youtube.com/@aljazeera/live"
    );
    fireEvent.change(screen.getByLabelText(/^from$/i), { target: { value: "20:00" } });
    await user.click(screen.getByRole("button", { name: /^fri$/i }));
    patch.mockResolvedValue({ section: "tv", changed: ["live"] });
    await user.click(save);
    expect(patch).toHaveBeenCalledTimes(1);
    const [path, body] = patch.mock.calls[0] as [string, { set: { live: unknown[] } }];
    expect(path).toBe("/api/rules/tv");
    expect(body.set.live).toEqual([
      { channel: 999, url: "https://other/live", title: "Other" },
      expect.objectContaining({
        channel: 101,
        url: "https://www.youtube.com/@aljazeera/live",
        start: "20:00",
      }),
    ]);
    expect((body.set.live[1] as { days: string[] }).days).toHaveLength(6);
  });

  it("offers back to plan on an edited channel", async () => {
    channels = [planChannel({ plan: { owned: true, edited: true } })];
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(true);
    post.mockResolvedValue({ channel_id: "c1", status: "following-plan", queued: true });
    const user = await openEditor();
    expect(screen.getByText(/no longer updates it/i)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /back to plan/i }));
    expect(post).toHaveBeenCalledWith("/api/channels/c1/follow-plan");
    confirm.mockRestore();
  });

  it("previews 48 hours and regenerates after confirming", async () => {
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(true);
    post.mockResolvedValue({ channel_id: "c1", status: "regeneration-queued" });
    const user = await openEditor();
    await user.click(screen.getByRole("tab", { name: /preview/i }));
    expect(await screen.findByText("Prophets 4")).toBeInTheDocument();
    expect(screen.getByText(/filler \/ bumper/i)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /regenerate schedule/i }));
    expect(confirm).toHaveBeenCalled();
    expect(post).toHaveBeenCalledWith("/api/channels/c1/regenerate");
    confirm.mockRestore();
  });

  it("a new channel needs a slot and a free number; its art uploads after the first save", async () => {
    const user = userEvent.setup();
    renderPage();
    await screen.findByText("Kids");
    await user.click(screen.getByRole("button", { name: /new channel/i }));
    await user.type(screen.getByLabelText(/^name$/i), "Quran");
    const number = screen.getByLabelText(/channel number/i);
    await user.clear(number);
    await user.type(number, "101");
    expect(screen.getByText(/already used by kids/i)).toBeInTheDocument();
    await user.clear(number);
    await user.type(number, "110");
    expect(screen.getByText(/has no slots yet/i)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /^save$/i })).toBeDisabled();
    await user.click(screen.getByRole("tab", { name: /^art$/i }));
    expect(screen.getByText(/uploaded as soon as the channel is saved/i)).toBeInTheDocument();
  });
});
