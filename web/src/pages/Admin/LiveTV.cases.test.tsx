// Admin Live TV page, from web/test-cases/admin-live-tv.md (LTV-*): access,
// load errors, every null / missing list the API may send (cluster, remote,
// simulcast, Plex, rules), the error and stale views, the form validation,
// the crash fallback, the links, polling cleanup, Arabic and a11y basics.
// The whole page is mounted through the shared page harness (RequireAuth +
// RouteErrorBoundary), with every GET routed by path.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Route } from "react-router-dom";
import { ApiError } from "../../lib/api";
import { memberUser, quietCrash, renderPage, type RenderPageOptions } from "../../test/pageHarness";
import type { User } from "../../lib/auth";

const { get, post, put, patch, del } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  put: vi.fn(),
  patch: vi.fn(),
  del: vi.fn(),
}));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post, put, patch, delete: del } };
});

import { AdminLiveTV } from "./LiveTV";
import { clusterView, host, onAir } from "./LiveTV/fixtures";

// ─── data ─────────────────────────────────────────────────────────────

type Problem = ConstructorParameters<typeof ApiError>[0];
const apiErr = (status: number, title: string, extra: Record<string, unknown> = {}) =>
  new ApiError({ type: "about:blank", title, status, ...extra } as Problem);

const info = {
  stream_base_url: "http://192.168.1.20:8782",
  hdhr_address: "192.168.1.20:8782",
  xmltv_url: "http://192.168.1.20:8782/xmltv.xml",
  m3u_url: "http://192.168.1.20:8782/lineup.m3u",
  base_from_config: true,
  xmltv_path: "/xmltv.xml",
  m3u_path: "/lineup.m3u",
  hls_path_template: "/stream/channel/{id}/index.m3u8",
};
const device = {
  device_id: "A1B2C3D4",
  friendly_name: "Maktaba",
  tuner_count: 4,
  enabled: false,
  max_tuners: 16,
};
const ruleField = (name: string, value: unknown) => ({
  name,
  kind: typeof value === "boolean" ? "bool" : "int",
  group: "Plex Live TV",
  label: name,
  help: "",
  default: value,
  value,
  changed: false,
});
const rules = {
  sections: [
    {
      key: "plexlive",
      title: "Plex Live TV",
      what: "",
      fields: [
        ruleField("auto_register", false),
        ruleField("auto_refresh_guide", true),
        ruleField("auto_refresh_channels", true),
        ruleField("guide_refresh_min_minutes", 60),
      ],
    },
  ],
};
const plexStatus = {
  reachable: true,
  dvrs: [
    {
      key: "11",
      title: "dizqueTV",
      lineup: "lineup://x",
      maktaba: false,
      devices: [
        { key: "3", uri: "http://pc:8000", device_id: "D1", title: "dizqueTV", kind: "dizquetv" },
      ],
    },
  ],
  maktaba: { tuner_key: null, dvr_key: null, registered: false },
  dizquetv_present: true,
  channels: { lineup: 3, mapped: null, in_sync: null },
  target: {
    tuner_url: "http://192.168.1.20:8782",
    xmltv_url: "http://192.168.1.20:8782/xmltv.xml",
    source: "env",
  },
  warnings: [] as string[],
};
const plexEmpty = {
  plex: { enabled: true, configured: true, url: "http://plex.lan:32400" },
  stage: { enabled: true, disabled_reason: null },
  job: null as null | Record<string, unknown>,
  checked_at: "2026-10-18T08:00:00.000Z",
  guide_reloaded_at: null,
  channelmap_at: null,
  last_error: null,
  last_error_at: null,
  register_requested_at: null,
  status: plexStatus,
};

const integration = (over: Record<string, unknown> = {}) => ({
  configured: true,
  account_id: "acc",
  token_set: true,
  token_problem: null,
  signing_key_set: true,
  customer_domain: "customer-abc.cloudflarestream.com",
  enabled: true,
  can_enable: true,
  last_test: null,
  ...over,
});
const estimate = {
  month: "2026-10",
  delivered_minutes: 120,
  running_minutes: 30,
  delivery_usd: 0.15,
  storage_usd: 5,
  total_usd: 5.15,
  budget_usd: 10,
  percent: 51.5,
  action: "block_new",
  source: "analytics",
  peak_recording_minutes: 200,
  exhausted: false,
};
const remoteStatus = (over: Record<string, unknown> = {}): Record<string, unknown> => ({
  ready: true,
  inputs: [
    {
      id: "i1",
      cf_uid: "cf1",
      name: "maktaba-1a2b-1",
      state: "broadcasting",
      channel_id: "c1aaaaaa-0000",
      assigned_at: "2026-10-19T10:00:00Z",
    },
  ],
  pushes: [
    {
      id: "p1",
      channel_id: "c1",
      channel_number: 105,
      channel_name: "Quran",
      state: "live",
      host_id: "home-host-1",
      started_at: "2026-10-19T10:00:00Z",
      viewers: 2,
      cf_viewers: 3,
    },
  ],
  sessions: 1,
  session_list: [
    {
      id: "s1",
      user: "amina",
      device: "Safari on iOS",
      channel_id: "c1",
      channel_number: 105,
      channel_name: "Quran",
      started_at: "2026-10-19T10:01:00Z",
      last_beat_at: "2026-10-19T10:05:00Z",
    },
  ],
  events: [
    { at: "2026-10-19T10:00:10Z", level: "INFO", event: "tv_push_live", message: "push live" },
  ],
  estimate,
  storage: {},
  limits: {},
  ...over,
});

const dest = (over: Record<string, unknown> = {}) => ({
  id: "d1",
  name: "My YouTube",
  kind: "youtube",
  ingest_url: "rtmp://a.rtmp.youtube.com/live2",
  key_set: true,
  stream_key_masked: "••••wxyz",
  in_use: true,
  ...over,
});
const attachment = (over: Record<string, unknown> = {}) => ({
  id: "a1",
  channel_id: "c1",
  destination_id: "d1",
  mode: "always",
  rules: [],
  enabled: true,
  rights_confirmed_by: "admin",
  rights_confirmed_at: "2026-10-19T10:00:00Z",
  channel_name: "Seerah",
  channel_number: 101,
  destination_name: "My YouTube",
  destination_kind: "youtube",
  output: { state: "live", platform_note: false, minutes: 12 },
  ...over,
});
const simView = (over: Record<string, unknown> = {}): Record<string, unknown> => ({
  enabled: true,
  ready: true,
  settings: {
    max_outputs_per_channel: 3,
    max_hours_per_month: 60,
    lead_seconds: 60,
    tail_seconds: 30,
  },
  destinations: [dest()],
  attachments: [attachment()],
  usage: {
    month: "2026-10",
    minutes: 750,
    hours: 12.5,
    cap_hours: 60,
    percent: 20.8,
    capped: false,
    cost_usd: 0.75,
    budget_exhausted: false,
  },
  default_ingest: {
    youtube: "rtmp://a.rtmp.youtube.com/live2",
    facebook: "rtmps://live-api-s.facebook.com:443/rtmp/",
  },
  ...over,
});
const channels = {
  items: [
    { id: "c1", number: 101, name: "Seerah" },
    { id: "c2", number: 102, name: "Fiqh" },
  ],
};

/** A copy of `o` without `key` (the API left it out). */
function without(o: object, key: string): Record<string, unknown> {
  const c: Record<string, unknown> = { ...o };
  delete c[key];
  return c;
}

// ─── routing ──────────────────────────────────────────────────────────

/** A route value: a payload, or a function answering one (may reject). */
type Answer = unknown | ((url: string) => unknown);
const fail = (e: unknown) => () => Promise.reject(e);
/** Answers in order; the last one repeats. */
const seq =
  (...answers: Answer[]) =>
  () => {
    const a = answers.length > 1 ? answers.shift() : answers[0];
    return typeof a === "function" ? (a as () => unknown)() : a;
  };

function routes(over: Record<string, Answer> = {}) {
  const table: Record<string, Answer> = {
    "/api/tv/info": info,
    "/api/tv/hdhr": device,
    "/api/tv/plex": plexEmpty,
    "/api/rules": rules,
    "/api/tv/cluster": clusterView(),
    "/api/tv/remote": { integration: integration(), remote: remoteStatus() },
    "/api/tv/simulcast": simView(),
    "/api/channels": channels,
    ...over,
  };
  get.mockImplementation((url: string) => {
    const path = url.split("?")[0];
    if (!(path in table)) return Promise.reject(new Error(`unmocked GET ${url}`));
    const v = table[path];
    try {
      return Promise.resolve(typeof v === "function" ? (v as (u: string) => unknown)(url) : v);
    } catch (e) {
      return Promise.reject(e);
    }
  });
}

const reads = (path: string) => get.mock.calls.filter(([u]) => u === path).length;

function open(opts: Partial<RenderPageOptions> = {}) {
  return renderPage(<AdminLiveTV />, { path: "/admin/live-tv", ...opts });
}

/** Opens the page and waits for the tuner card. */
async function openLoaded(opts: Partial<RenderPageOptions> = {}) {
  const r = open(opts);
  await screen.findByText("A1B2C3D4");
  return r;
}

const crashed = () => screen.queryByText("This page hit an error and could not be shown.");
const hostCard = (name: string) => screen.getByRole("article", { name });
const setVisibility = (v: "visible" | "hidden") => {
  Object.defineProperty(document, "visibilityState", { value: v, configurable: true });
  document.dispatchEvent(new Event("visibilitychange"));
};

/** The page's other cards are still there. */
async function otherPanelsRender() {
  expect(await screen.findByRole("heading", { name: "HDHomeRun tuner" })).toBeInTheDocument();
  expect(await screen.findByTestId("remote-panel")).toBeInTheDocument();
  expect(await screen.findByRole("heading", { name: "Simulcast" })).toBeInTheDocument();
  expect(screen.getByRole("heading", { name: "Plex" })).toBeInTheDocument();
}

describe("AdminLiveTV (test-cases/admin-live-tv.md)", () => {
  beforeEach(() => {
    for (const f of [get, post, put, patch, del]) f.mockReset();
    routes();
  });
  afterEach(() => {
    Object.defineProperty(document, "visibilityState", { value: "visible", configurable: true });
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  // ─── access ────────────────────────────────────────────────────────

  it.each([
    ["is_admin false", memberUser],
    ["is_admin missing", { id: "u-x", username: "u" } as User],
  ])("LTV-02: non-admin (%s) sees Admins only and nothing is requested", async (_, user) => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    open({ user });
    expect(await screen.findByText("Admins only")).toBeInTheDocument();
    expect(
      screen.getByText("You need administrator access to view this page.")
    ).toBeInTheDocument();
    await act(() => vi.advanceTimersByTimeAsync(6_000));
    expect(get).not.toHaveBeenCalled();
  });

  it("LTV-03: signed-out user is sent to /login and nothing is requested", async () => {
    open({ user: null });
    expect(await screen.findByText("login page (from /admin/live-tv)")).toBeInTheDocument();
    expect(get).not.toHaveBeenCalled();
  });

  // ─── load ──────────────────────────────────────────────────────────

  it.each([
    ["info 500", "/api/tv/info", apiErr(500, "Internal Server Error"), "Internal Server Error"],
    ["tuner 404", "/api/tv/hdhr", apiErr(404, "Not Found"), "Not Found"],
    ["network", "/api/tv/info", new TypeError("Failed to fetch"), "Failed to fetch"],
    ["401", "/api/tv/hdhr", apiErr(401, "Unauthorized"), "Unauthorized"],
    ["403", "/api/tv/info", apiErr(403, "Forbidden"), "Forbidden"],
  ])("LTV-05: %s shows the load error, not a blank page", async (_, path, err, text) => {
    routes({ [path]: fail(err) });
    open();
    expect(await screen.findByRole("alert")).toHaveTextContent(
      `Could not load the Live TV settings: ${text}`
    );
    expect(screen.getByRole("heading", { name: "Live TV access" })).toBeInTheDocument();
    expect(crashed()).toBeNull();
  });

  // ─── tuner ─────────────────────────────────────────────────────────

  it("LTV-06: tuner on succeeds; a failed PATCH keeps it off and says why", async () => {
    const user = userEvent.setup();
    patch.mockRejectedValueOnce(new Error("boom"));
    await openLoaded();
    const toggle = screen.getByRole("switch", { name: /Tuner off/ });
    await user.click(toggle);
    expect(patch).toHaveBeenCalledWith("/api/tv/hdhr", { enabled: true });
    expect(await screen.findByText("boom")).toBeInTheDocument();
    expect(screen.getByText("boom").closest("[role=alert]")).not.toBeNull();
    expect(screen.getByRole("switch", { name: /Tuner off/ })).not.toBeChecked();
    expect(screen.getByRole("switch", { name: /Tuner off/ })).toBeEnabled();

    patch.mockResolvedValueOnce({ ...device, enabled: true });
    await user.click(screen.getByRole("switch", { name: /Tuner off/ }));
    expect(patch).toHaveBeenLastCalledWith("/api/tv/hdhr", { enabled: true });
    const ok = await screen.findByText("Tuner turned on.");
    expect(ok.closest("[role=status]")).not.toBeNull();
    expect(screen.getByRole("switch", { name: /Tuner on/ })).toBeChecked();
  });

  it("LTV-07: tuner name and count validation; save sends the trimmed name", async () => {
    const user = userEvent.setup();
    patch.mockResolvedValue({ ...device, friendly_name: "Den" });
    await openLoaded();
    const tunerCard = screen.getByRole("heading", { name: "HDHomeRun tuner" }).parentElement!;
    const name = within(tunerCard).getByLabelText("Tuner name");
    const count = within(tunerCard).getByLabelText(/^Tuners/);
    const save = within(tunerCard).getByRole("button", { name: "Save" });
    expect(save).toBeDisabled(); // nothing changed

    await user.clear(name);
    expect(screen.getByText("Give the tuner a name.")).toBeInTheDocument();
    expect(save).toBeDisabled();
    await user.type(name, "   ");
    expect(screen.getByText("Give the tuner a name.")).toBeInTheDocument();
    expect(save).toBeDisabled();
    await user.clear(name);
    await user.type(name, "Maktaba");

    for (const bad of ["0", "17", "1.5", ""]) {
      await user.clear(count);
      if (bad) await user.type(count, bad);
      expect(screen.getByText("Between 1 and 16.")).toBeInTheDocument();
      expect(save).toBeDisabled();
    }
    await user.clear(count);
    await user.type(count, "4");
    expect(screen.queryByText("Between 1 and 16.")).not.toBeInTheDocument();
    expect(save).toBeDisabled(); // back to the server's values

    await user.clear(name);
    await user.type(name, "  Den  ");
    await user.click(save);
    expect(patch).toHaveBeenCalledWith("/api/tv/hdhr", { friendly_name: "Den", tuner_count: 4 });
  });

  it.each([
    ["0", { ...device, max_tuners: 0 }],
    ["missing", without(device, "max_tuners")],
  ])("LTV-07: max_tuners %s means a limit of 16", async (_, d) => {
    const user = userEvent.setup();
    routes({ "/api/tv/hdhr": d });
    await openLoaded();
    const count = screen.getByLabelText(/^Tuners/);
    await user.clear(count);
    await user.type(count, "16");
    expect(screen.queryByText(/Between 1 and/)).not.toBeInTheDocument();
    await user.type(count, "7"); // 167
    expect(screen.getByText("Between 1 and 16.")).toBeInTheDocument();
  });

  // ─── staging banner ────────────────────────────────────────────────

  const staging = (over: Record<string, unknown> = {}) => ({
    enabled: true,
    change_in_flight: "ch-1",
    change_in_flight_number: 7,
    change_in_flight_state: "verifying",
    live_release: null,
    live_gen: 3,
    ...over,
  });

  it("LTV-08: in flight: links to the change and to Changes", async () => {
    routes({ "/api/tv/info": { ...info, staging: staging() } });
    await openLoaded();
    const banner = screen.getByTestId("staging-banner");
    expect(within(banner).getByRole("link", { name: "Change #7 is Verifying" })).toHaveAttribute(
      "href",
      "/admin/live-tv/changes/ch-1"
    );
    expect(within(banner).getByRole("link", { name: "Open Changes" })).toHaveAttribute(
      "href",
      "/admin/live-tv/changes"
    );
  });

  it("LTV-08: no change in flight; staging missing shows no banner", async () => {
    routes({ "/api/tv/info": { ...info, staging: staging({ change_in_flight: null }) } });
    const { unmount } = await openLoaded();
    expect(within(screen.getByTestId("staging-banner")).getByText("No change is in flight."));
    unmount();
    routes();
    await openLoaded();
    expect(screen.queryByTestId("staging-banner")).not.toBeInTheDocument();
  });

  // Bug: a change in flight without its number / state reads "Change #0 is ".
  // The fix needs an i18n string for "a change is in flight" (i18n.tsx / en.json).
  it.fails("LTV-08: in flight without number or state shows no #0 or dangling state", async () => {
    routes({
      "/api/tv/info": {
        ...info,
        staging: without(without(staging(), "change_in_flight_number"), "change_in_flight_state"),
      },
    });
    await openLoaded();
    const banner = screen.getByTestId("staging-banner");
    expect(banner).not.toHaveTextContent("#0");
    expect(banner).not.toHaveTextContent(/ is\s*$/m);
    expect(within(banner).getByRole("link", { name: /Change/ }).textContent).not.toMatch(/is $/);
  });

  // ─── cluster ───────────────────────────────────────────────────────

  it.each([
    [403, "Forbidden"],
    [404, "Not Found"],
  ])("LTV-10: cluster %s hides the sections and stops polling", async (status, title) => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    routes({ "/api/tv/cluster": fail(apiErr(status, title)) });
    await openLoaded();
    await act(() => vi.advanceTimersByTimeAsync(20_000));
    setVisibility("hidden");
    setVisibility("visible");
    await act(() => vi.advanceTimersByTimeAsync(5_000));
    expect(reads("/api/tv/cluster")).toBe(1);
    expect(screen.queryByRole("heading", { name: "Streaming hosts" })).not.toBeInTheDocument();
    expect(screen.queryByRole("table", { name: "On air" })).not.toBeInTheDocument();
    expect(screen.queryByText(/streaming hosts/i)).not.toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "HDHomeRun tuner" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Plex" })).toBeInTheDocument();
  });

  it("LTV-11: a failed first read says so, and the next good read replaces it", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    routes({ "/api/tv/cluster": seq(fail(new Error("boom")), clusterView()) });
    await openLoaded();
    expect(await screen.findByText("Could not load the streaming hosts: boom")).toBeInTheDocument();
    await act(() => vi.advanceTimersByTimeAsync(5_000));
    expect(await screen.findByRole("heading", { name: "Streaming hosts" })).toBeInTheDocument();
    expect(screen.queryByText(/Could not load the streaming hosts/)).not.toBeInTheDocument();
  });

  it("LTV-11: a failed refresh keeps the last view with a stale hint until a good read", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    routes({
      "/api/tv/cluster": seq(clusterView(), fail(new TypeError("Failed to fetch")), clusterView()),
    });
    await openLoaded();
    expect(await screen.findByRole("heading", { name: "Streaming hosts" })).toBeInTheDocument();
    await act(() => vi.advanceTimersByTimeAsync(5_000));
    expect(
      await screen.findByText(
        "Could not refresh the streaming hosts (Failed to fetch); showing the last view."
      )
    ).toBeInTheDocument();
    expect(screen.getByRole("table", { name: "On air" })).toBeInTheDocument();
    expect(hostCard("Gaming-PC")).toBeInTheDocument();
    await act(() => vi.advanceTimersByTimeAsync(5_000));
    await waitFor(() =>
      expect(screen.queryByText(/Could not refresh the streaming hosts/)).not.toBeInTheDocument()
    );
  });

  const view = clusterView();
  it.each<[string, unknown, { noHosts?: boolean; noOnAir?: boolean; noSettings?: boolean }]>([
    ["hosts null", { ...view, hosts: null }, { noHosts: true }],
    ["hosts missing", without(view, "hosts"), { noHosts: true }],
    ["on_air null", { ...view, on_air: null }, { noOnAir: true }],
    ["on_air missing", without(view, "on_air"), { noOnAir: true }],
    ["notices null", { ...view, notices: null }, {}],
    ["notices missing", without(view, "notices"), {}],
    ["settings null", { ...view, settings: null }, { noSettings: true }],
    ["settings missing", without(view, "settings"), { noSettings: true }],
    [
      "all four null",
      { settings: null, hosts: null, on_air: null, notices: null },
      { noHosts: true, noOnAir: true, noSettings: true },
    ],
    ["{}", {}, { noHosts: true, noOnAir: true, noSettings: true }],
  ])("LTV-12: cluster %s never blanks the page", async (_, body, want) => {
    routes({ "/api/tv/cluster": () => body });
    await openLoaded();
    expect(await screen.findByRole("heading", { name: "Streaming hosts" })).toBeInTheDocument();
    expect(crashed()).toBeNull();
    if (want.noHosts) expect(screen.getByText("No machine streams TV yet.")).toBeInTheDocument();
    else expect(hostCard("Gaming-PC")).toBeInTheDocument();
    if (want.noOnAir) expect(screen.getByText("No channel is on air.")).toBeInTheDocument();
    else expect(screen.getByRole("table", { name: "On air" })).toBeInTheDocument();
    if (want.noSettings) expect(screen.queryByLabelText(/Home full at/)).not.toBeInTheDocument();
    else expect(screen.getByLabelText(/Home full at/)).toBeInTheDocument();
    expect(screen.queryByText("Two homes.")).not.toBeInTheDocument();
    await otherPanelsRender();
  });

  it("LTV-12: a JSON null cluster body reads as an empty cluster", async () => {
    routes({ "/api/tv/cluster": () => null });
    await openLoaded();
    expect(await screen.findByText("No machine streams TV yet.")).toBeInTheDocument();
    expect(screen.queryByText(/Could not load the streaming hosts/)).not.toBeInTheDocument();
    expect(crashed()).toBeNull();
    await otherPanelsRender();
  });

  it("LTV-12: with hosts null the Move dialog offers only Automatic", async () => {
    const user = userEvent.setup();
    routes({ "/api/tv/cluster": { ...clusterView(), hosts: null } });
    await openLoaded();
    await user.click(await screen.findByRole("button", { name: "Move Seerah" }));
    const dialog = await screen.findByRole("dialog");
    const select = within(dialog).getByRole("combobox");
    expect(
      within(select)
        .getAllByRole("option")
        .map((o) => o.textContent)
    ).toEqual(["Automatic"]);
    expect(
      within(dialog).getByText(
        "No machine is online and taking channels; Automatic picks the best one when there is."
      )
    ).toBeInTheDocument();
  });

  it.each<[string, Record<string, unknown>]>([
    ["encoders null", { ...host(), encoders: null }],
    ["encoders missing", without(host(), "encoders")],
  ])("LTV-13: a host with %s shows —", async (_, h) => {
    routes({ "/api/tv/cluster": clusterView({ hosts: [h as never] }) });
    await openLoaded();
    const card = await screen.findByRole("article", { name: "Mac-Studio" });
    expect(within(card).getByText("Encoders").nextElementSibling).toHaveTextContent("—");
    expect(crashed()).toBeNull();
  });

  it("LTV-13: load and can_push null read as — and no", async () => {
    routes({
      "/api/tv/cluster": clusterView({
        hosts: [host({ load: null, can_push: null } as never)],
      }),
    });
    await openLoaded();
    const card = await screen.findByRole("article", { name: "Mac-Studio" });
    expect(within(card).getByText("Load").nextElementSibling).toHaveTextContent("—");
    expect(within(card).getByText("Can push").nextElementSibling).toHaveTextContent("no");
  });

  it.each([
    ["null", (h: object) => ({ ...h, channels: null })],
    ["missing", (h: object) => without(h, "channels")],
  ])("LTV-13: a draining host with channels %s: Move now says 0", async (_, change) => {
    const user = userEvent.setup();
    const nas = change(
      host({ host_id: "h-nas", role: "node", name: "NAS", state: "draining", draining: true })
    );
    routes({ "/api/tv/cluster": clusterView({ hosts: [host(), nas as never] }) });
    await openLoaded();
    const card = await screen.findByRole("article", { name: "NAS" });
    await user.click(within(card).getByRole("button", { name: "Move now" }));
    expect(await screen.findByRole("dialog")).toHaveTextContent(
      "The channels on this machine (0) move to other machines."
    );
  });

  it("LTV-13: two-homes notice with hosts null falls back to the home names", async () => {
    routes({
      "/api/tv/cluster": clusterView({
        notices: [{ kind: "two_homes", message: "x", hosts: null } as never],
      }),
    });
    await openLoaded();
    const banner = await screen.findByText("Two homes.");
    expect(banner.parentElement).toHaveTextContent("Mac-Studio and ? both say they are the home");
  });

  it("LTV-13: on-air viewers / push / since null and unknown states do not throw", async () => {
    routes({
      "/api/tv/cluster": clusterView({
        hosts: [host({ state: "weird" as never })],
        on_air: [onAir({ viewers: null, push: null, since: null, state: "weird" } as never)],
      }),
    });
    await openLoaded();
    const table = await screen.findByRole("table", { name: "On air" });
    const cells = within(table).getAllByRole("row")[1].querySelectorAll("td");
    expect([...cells].map((c) => c.textContent)).toEqual([
      "105",
      "Seerah",
      "Mac-Studio",
      "tv.cluster.onAir.stateName.weird",
      "videotoolbox",
      "0",
      "0",
      "0",
      "—",
      "0",
      "3",
      "—",
      "MoveStop",
    ]);
    expect(within(hostCard("Mac-Studio")).getByText("tv.cluster.state.weird")).toBeInTheDocument();
    expect(crashed()).toBeNull();
  });

  it("LTV-15: host capacity, weight and address validation", async () => {
    const user = userEvent.setup();
    put.mockResolvedValue({});
    routes({
      "/api/tv/cluster": clusterView({
        hosts: [
          host(),
          host({ host_id: "h-pc", role: "node", name: "Gaming-PC", capacity_override: 4 }),
        ],
      }),
    });
    await openLoaded();
    const card = await screen.findByRole("article", { name: "Gaming-PC" });
    const cap = within(card).getByLabelText(/^Capacity/);
    const weight = within(card).getByLabelText(/^Weight/);
    const url = within(card).getByLabelText(/^Stream base URL/);
    const save = within(card).getByRole("button", { name: "Save" });
    const capErr = "A whole number from 0 to 64, or empty for automatic.";

    for (const bad of ["65", "-1", "1.5"]) {
      await user.clear(cap);
      await user.type(cap, bad);
      expect(within(card).getByText(capErr)).toBeInTheDocument();
      expect(save).toBeDisabled();
    }
    await user.clear(cap); // empty = automatic, valid
    expect(within(card).queryByText(capErr)).not.toBeInTheDocument();
    expect(save).toBeEnabled();

    for (const bad of ["0", "101", ""]) {
      await user.clear(weight);
      if (bad) await user.type(weight, bad);
      expect(within(card).getByText("Between 1 and 100.")).toBeInTheDocument();
      expect(save).toBeDisabled();
    }
    await user.clear(weight);
    await user.type(weight, "1");

    for (const bad of ["ftp://x", "not a url"]) {
      await user.clear(url);
      await user.type(url, bad);
      expect(within(card).getByText("An http:// or https:// address.")).toBeInTheDocument();
      expect(save).toBeDisabled();
    }
    await user.clear(url);
    expect(within(card).queryByText("An http:// or https:// address.")).not.toBeInTheDocument();
    await user.click(save);
    expect(put).toHaveBeenCalledWith("/api/tv/cluster/hosts/h-pc", { capacity_override: null });
    // the home has no address field
    expect(
      within(hostCard("Mac-Studio")).queryByLabelText(/^Stream base URL/)
    ).not.toBeInTheDocument();
  });

  it("LTV-15: cluster spill-at validation", async () => {
    const user = userEvent.setup();
    await openLoaded();
    const spill = await screen.findByLabelText(/^Home full at/);
    const settings = spill.closest(".mkt-tvc-settings") as HTMLElement;
    const saveSpill = within(settings).getByRole("button", { name: "Save" });
    expect(saveSpill).toBeDisabled(); // unchanged (100)
    for (const bad of ["0", "101", "50.5"]) {
      await user.clear(spill);
      await user.type(spill, bad);
      expect(within(settings).getByText("Between 1 and 100.")).toBeInTheDocument();
      expect(saveSpill).toBeDisabled();
    }
    await user.clear(spill);
    await user.type(spill, "100");
    expect(within(settings).queryByText("Between 1 and 100.")).not.toBeInTheDocument();
    expect(saveSpill).toBeDisabled();
  });

  it("LTV-17: no movable host leaves only Automatic", async () => {
    const user = userEvent.setup();
    routes({
      "/api/tv/cluster": clusterView({
        hosts: [
          host({ state: "down" }),
          host({ host_id: "h-nas", name: "NAS", role: "node", state: "draining", draining: true }),
        ],
      }),
    });
    await openLoaded();
    await user.click(await screen.findByRole("button", { name: "Move Seerah" }));
    const dialog = await screen.findByRole("dialog");
    expect(
      within(dialog)
        .getAllByRole("option")
        .map((o) => o.textContent)
    ).toEqual(["Automatic"]);
    expect(
      within(dialog).getByText(/No machine is online and taking channels/)
    ).toBeInTheDocument();
  });

  it("LTV-17: a refused move keeps the dialog open; Cancel and Escape close it", async () => {
    const user = userEvent.setup();
    post.mockRejectedValueOnce(apiErr(409, "Conflict", { error: "no room" }));
    await openLoaded();
    await user.click(await screen.findByRole("button", { name: "Move Seerah" }));
    let dialog = await screen.findByRole("dialog");
    await user.click(within(dialog).getByRole("button", { name: "Move" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent("no room");
    expect(within(dialog).getByRole("button", { name: "Move" })).toBeEnabled();

    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(post).toHaveBeenCalledTimes(1);

    await user.click(screen.getByRole("button", { name: "Move Seerah" }));
    dialog = await screen.findByRole("dialog");
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(post).toHaveBeenCalledTimes(1);
  });

  it("LTV-17: a failed stop closes the dialog and shows the error on the card", async () => {
    const user = userEvent.setup();
    del.mockRejectedValueOnce(new Error("boom"));
    await openLoaded();
    await user.click(await screen.findByRole("button", { name: "Stop Seerah" }));
    const dialog = await screen.findByRole("dialog");
    await user.click(within(dialog).getByRole("button", { name: "Stop" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    const onAirCard = screen.getByRole("table", { name: "On air" }).closest(".mkt-tvc")!;
    expect(within(onAirCard as HTMLElement).getByRole("alert")).toHaveTextContent("boom");
    expect(screen.getByRole("button", { name: "Stop Seerah" })).toBeEnabled();
    expect(screen.getByRole("button", { name: "Move Seerah" })).toBeEnabled();
  });

  // ─── remote ────────────────────────────────────────────────────────

  const remote = (over: Record<string, unknown>) => ({
    "/api/tv/remote": { integration: integration(), remote: remoteStatus(over) },
  });

  it.each<[string, Record<string, Answer>, (panel: HTMLElement) => void]>([
    [
      "inputs null",
      remote({ inputs: null }),
      () => expect(screen.queryByTestId("remote-inputs")).not.toBeInTheDocument(),
    ],
    [
      "inputs missing",
      {
        "/api/tv/remote": { integration: integration(), remote: without(remoteStatus(), "inputs") },
      },
      () => expect(screen.queryByTestId("remote-inputs")).not.toBeInTheDocument(),
    ],
    [
      "pushes / sessions / events null",
      remote({ pushes: null, session_list: null, events: null }),
      (p) => {
        expect(within(p).getByText("No channel is being sent to Cloudflare.")).toBeInTheDocument();
        expect(within(p).getByText("Nobody is watching away from home.")).toBeInTheDocument();
        expect(
          within(p).getByText("No remote events since the server started.")
        ).toBeInTheDocument();
      },
    ],
    [
      "pushes / sessions / events missing",
      {
        "/api/tv/remote": {
          integration: integration(),
          remote: without(without(without(remoteStatus(), "pushes"), "session_list"), "events"),
        },
      },
      (p) =>
        expect(within(p).getByText("No channel is being sent to Cloudflare.")).toBeInTheDocument(),
    ],
    [
      "remote missing",
      { "/api/tv/remote": { integration: integration() } },
      () =>
        expect(screen.getByTestId("remote-status")).toHaveTextContent(
          "Remote viewing is on and ready."
        ),
    ],
    [
      "integration null",
      { "/api/tv/remote": { integration: null, remote: remoteStatus() } },
      (p) => {
        expect(screen.getByTestId("remote-status")).toHaveTextContent(
          "Cloudflare Stream is not set up."
        );
        expect(within(p).queryByText("Loading…")).not.toBeInTheDocument();
      },
    ],
    [
      "integration missing",
      { "/api/tv/remote": { remote: remoteStatus() } },
      () =>
        expect(screen.getByTestId("remote-status")).toHaveTextContent(
          "Cloudflare Stream is not set up."
        ),
    ],
    [
      "estimate null",
      remote({ estimate: null }),
      () => expect(screen.queryByTestId("remote-month")).not.toBeInTheDocument(),
    ],
    [
      "estimate total_usd null and budget_usd missing",
      remote({ estimate: { ...without(estimate, "budget_usd"), total_usd: null } }),
      () => expect(screen.getByTestId("remote-month")).toHaveTextContent("—"),
    ],
    [
      "a push without channel_id",
      remote({
        pushes: [
          {
            id: "p1",
            state: "live",
            channel_id: null,
            channel_number: 0,
            viewers: 1,
            started_at: "",
          },
        ],
      }),
      () =>
        expect(
          within(screen.getByTestId("remote-pushes")).getAllByRole("row")[1].querySelector("td")
        ).toHaveTextContent("—"),
    ],
    [
      "limits and storage missing",
      {
        "/api/tv/remote": {
          integration: integration(),
          remote: without(without(remoteStatus(), "limits"), "storage"),
        },
      },
      () => expect(screen.getByTestId("remote-pushes")).toBeInTheDocument(),
    ],
  ])("LTV-19: remote %s does not crash", async (_, over, check) => {
    routes(over);
    await openLoaded();
    const panel = await screen.findByTestId("remote-panel");
    await waitFor(() => expect(screen.getByTestId("remote-status")).toBeInTheDocument());
    check(panel);
    expect(crashed()).toBeNull();
    await otherPanelsRender();
  });

  it.each([
    [403, "Forbidden"],
    [404, "Not Found"],
  ])("LTV-20: remote %s hides the panel and stops polling", async (status, title) => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    routes({ "/api/tv/remote": fail(apiErr(status, title)) });
    await openLoaded();
    await waitFor(() => expect(reads("/api/tv/remote")).toBe(1));
    await act(() => vi.advanceTimersByTimeAsync(15_000));
    expect(screen.queryByTestId("remote-panel")).not.toBeInTheDocument();
    expect(reads("/api/tv/remote")).toBe(1);
  });

  it("LTV-20: remote 500 shows the error in the card", async () => {
    routes({ "/api/tv/remote": fail(new Error("boom")) });
    await openLoaded();
    expect(
      await within(await screen.findByTestId("remote-panel")).findByText(
        "Could not read remote viewing: boom"
      )
    ).toBeInTheDocument();
  });

  it("LTV-20: a failed remote refresh keeps the tables with a stale hint", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    routes({
      "/api/tv/remote": seq(
        { integration: integration(), remote: remoteStatus() },
        fail(new TypeError("Failed to fetch"))
      ),
    });
    await openLoaded();
    const panel = await screen.findByTestId("remote-panel");
    expect(await within(panel).findByTestId("remote-pushes")).toHaveTextContent("105 Quran");
    await act(() => vi.advanceTimersByTimeAsync(5_000));
    expect(await within(panel).findByText(/Failed to fetch/)).toBeInTheDocument();
    expect(within(panel).getByTestId("remote-pushes")).toHaveTextContent("105 Quran");
  });

  // Bug: the remote panel's stale hint reuses tv.cluster.staleView ("Could not
  // refresh the streaming hosts …"). Needs its own i18n key (tv.remote.staleView).
  it.fails("LTV-20: the remote stale hint talks about remote viewing", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    routes({
      "/api/tv/remote": seq(
        { integration: integration(), remote: remoteStatus() },
        fail(new TypeError("Failed to fetch"))
      ),
    });
    await openLoaded();
    const panel = await screen.findByTestId("remote-panel");
    await within(panel).findByTestId("remote-pushes");
    await act(() => vi.advanceTimersByTimeAsync(5_000));
    const hint = await within(panel).findByText(/Failed to fetch/);
    expect(hint).not.toHaveTextContent("streaming hosts");
  });

  // ─── simulcast ─────────────────────────────────────────────────────

  const simPanel = async () =>
    (await screen.findByRole("heading", { name: "Simulcast" })).closest(
      ".mkt-simulcast"
    ) as HTMLElement;

  it.each<[string, Record<string, unknown> | null, (p: HTMLElement) => void]>([
    [
      "destinations null",
      simView({ destinations: null }),
      (p) => {
        expect(within(p).getByText("No destinations yet.")).toBeInTheDocument();
        expect(within(p).getByText("Add a destination first.")).toBeInTheDocument();
        expect(within(p).getByRole("button", { name: "Simulcast…" })).toBeDisabled();
      },
    ],
    [
      "destinations missing",
      without(simView(), "destinations"),
      (p) => expect(within(p).getByText("No destinations yet.")).toBeInTheDocument(),
    ],
    [
      "attachments null",
      simView({ attachments: null }),
      (p) =>
        expect(within(p).getByText("No channel is attached to a destination.")).toBeInTheDocument(),
    ],
    [
      "attachments missing",
      without(simView(), "attachments"),
      (p) =>
        expect(within(p).getByText("No channel is attached to a destination.")).toBeInTheDocument(),
    ],
    ["usage null", simView({ usage: null }), (p) => expect(p).toHaveTextContent("0.00")],
    ["usage missing", without(simView(), "usage"), (p) => expect(p).toHaveTextContent("0.00")],
    [
      "an attachment with output null",
      simView({ attachments: [attachment({ output: null })] }),
      (p) => expect(within(p).getByText("Off")).toBeInTheDocument(),
    ],
    ["a JSON null body", null, (p) => expect(within(p).getByText("No destinations yet."))],
  ])("LTV-22: simulcast %s does not crash", async (_, body, check) => {
    routes({ "/api/tv/simulcast": () => body });
    await openLoaded();
    check(await simPanel());
    expect(crashed()).toBeNull();
    await otherPanelsRender();
  });

  it.each([
    ["null", simView({ default_ingest: null })],
    ["missing", without(simView(), "default_ingest")],
  ])("LTV-22: default_ingest %s leaves the URL empty", async (_, body) => {
    const user = userEvent.setup();
    routes({ "/api/tv/simulcast": body });
    await openLoaded();
    await user.click(within(await simPanel()).getByRole("button", { name: "Add destination" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByLabelText(/^Ingest URL/)).toHaveValue("");
    await user.selectOptions(within(dialog).getByLabelText("Kind"), "facebook");
    expect(within(dialog).getByLabelText(/^Ingest URL/)).toHaveValue("");
  });

  it("LTV-22: settings null, rules null and days null still open the channel editor", async () => {
    const user = userEvent.setup();
    routes({
      "/api/tv/simulcast": simView({
        settings: null,
        attachments: [
          attachment({ mode: "schedule", rules: [{ kind: "window", days: null, start: "20:00" }] }),
          attachment({ id: "a2", channel_id: "c2", channel_name: "Fiqh", rules: null }),
        ],
      }),
      "/api/channels": channels,
    });
    await openLoaded();
    const p = await simPanel();
    await user.selectOptions(within(p).getByLabelText("Channel"), "c1");
    await user.click(within(p).getByRole("button", { name: "Simulcast…" }));
    const dialog = await screen.findByRole("dialog", { name: "Simulcast: Seerah" });
    expect(within(dialog).getByLabelText("Mon")).not.toBeChecked();
    expect(crashed()).toBeNull();
  });

  it("LTV-22: channels {items:null} leaves only the pick option", async () => {
    routes({ "/api/channels": { items: null } });
    await openLoaded();
    const p = await simPanel();
    expect(within(within(p).getByLabelText("Channel")).getAllByRole("option")).toHaveLength(1);
  });

  it("LTV-23: a simulcast 500 says so in a Simulcast card; a 404 hides it", async () => {
    routes({ "/api/tv/simulcast": fail(new Error("boom")) });
    const { unmount } = await openLoaded();
    const p = await simPanel();
    expect(within(p).getByRole("alert")).toHaveTextContent("boom");
    unmount();

    routes({ "/api/tv/simulcast": fail(apiErr(404, "Not Found")) });
    await openLoaded();
    await waitFor(() => expect(reads("/api/tv/simulcast")).toBeGreaterThan(1));
    await screen.findByTestId("remote-panel");
    expect(screen.queryByRole("heading", { name: "Simulcast" })).not.toBeInTheDocument();
  });

  it("LTV-24: destination form validation; a failed save keeps the form", async () => {
    const user = userEvent.setup();
    post.mockRejectedValueOnce(
      apiErr(400, "Bad Request", { code: "invalid_ingest_url", detail: "host missing" })
    );
    routes({ "/api/tv/simulcast": simView({ destinations: [], attachments: [] }) });
    await openLoaded();
    await user.click(within(await simPanel()).getByRole("button", { name: "Add destination" }));
    const dialog = await screen.findByRole("dialog", { name: "Add destination" });
    const name = within(dialog).getByLabelText("Name");
    const url = within(dialog).getByLabelText(/^Ingest URL/);
    const key = within(dialog).getByLabelText(/^Stream key/);
    const save = within(dialog).getByRole("button", { name: "Save" });
    expect(key).toHaveAttribute("type", "password");
    expect(key).toHaveAttribute("autocomplete", "off");
    expect(save).toBeDisabled(); // name and key empty

    for (const bad of ["http://x", "rtmp://"]) {
      await user.clear(url);
      await user.type(url, bad);
      expect(
        within(dialog).getByText("Use an rtmp:// or rtmps:// address with a host.")
      ).toBeInTheDocument();
    }
    await user.clear(url);
    await user.type(url, "rtmps://live.example/app");
    expect(within(dialog).queryByText(/Use an rtmp/)).not.toBeInTheDocument();
    await user.type(name, "   ");
    expect(save).toBeDisabled();
    await user.clear(name);
    await user.type(name, "Studio");
    expect(save).toBeDisabled(); // a new destination needs its key
    await user.type(key, "secret");
    expect(save).toBeEnabled();

    await user.click(save);
    expect(post).toHaveBeenCalledWith("/api/tv/simulcast/destinations", {
      name: "Studio",
      ingest_url: "rtmps://live.example/app",
      stream_key: "secret",
      kind: "youtube",
    });
    expect(await screen.findByText("Bad Request: host missing")).toBeInTheDocument();
    // the form stays open with what was typed
    const still = screen.getByRole("dialog", { name: "Add destination" });
    expect(within(still).getByLabelText("Name")).toHaveValue("Studio");
    expect(within(still).getByLabelText(/^Ingest URL/)).toHaveValue("rtmps://live.example/app");
  });

  it("LTV-24: editing keeps the kind and the stored key when the key is left empty", async () => {
    const user = userEvent.setup();
    put.mockResolvedValue({});
    await openLoaded();
    const p = await simPanel();
    const dests = p.querySelector(".mkt-simulcast__dests") as HTMLElement;
    const row = within(dests).getByText("My YouTube").closest("tr")!;
    await user.click(within(row).getByRole("button", { name: "Edit" }));
    const dialog = await screen.findByRole("dialog", { name: "Edit destination" });
    expect(within(dialog).getByLabelText("Kind")).toBeDisabled();
    const save = within(dialog).getByRole("button", { name: "Save" });
    expect(save).toBeEnabled();
    await user.click(save);
    expect(put).toHaveBeenCalledWith("/api/tv/simulcast/destinations/d1", {
      name: "My YouTube",
      ingest_url: "rtmp://a.rtmp.youtube.com/live2",
    });
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });

  it("LTV-25: delete asks first, in-use cannot be deleted, a 409 explains", async () => {
    const user = userEvent.setup();
    const confirm = vi.spyOn(window, "confirm");
    routes({
      "/api/tv/simulcast": simView({
        destinations: [dest({ id: "d2", name: "Spare", kind: "rtmp", in_use: false }), dest()],
      }),
    });
    await openLoaded();
    const p = await simPanel();
    const dests = p.querySelector(".mkt-simulcast__dests") as HTMLElement;
    const rowOf = (n: string) => within(dests).getByText(n).closest("tr")!;
    expect(within(rowOf("My YouTube")).getByRole("button", { name: "Delete" })).toBeDisabled();

    confirm.mockReturnValueOnce(false);
    await user.click(within(rowOf("Spare")).getByRole("button", { name: "Delete" }));
    expect(confirm).toHaveBeenCalledWith(
      'Delete the destination "Spare"? Its channel attachments go with it.'
    );
    expect(del).not.toHaveBeenCalled();

    confirm.mockReturnValueOnce(true);
    del.mockResolvedValueOnce(undefined);
    await user.click(within(rowOf("Spare")).getByRole("button", { name: "Delete" }));
    expect(del).toHaveBeenCalledWith("/api/tv/simulcast/destinations/d2");
    expect(await within(p).findByRole("status")).toHaveTextContent("Destination deleted.");

    confirm.mockReturnValueOnce(true);
    del.mockRejectedValueOnce(apiErr(409, "Conflict", { code: "destination_in_use" }));
    await user.click(within(rowOf("Spare")).getByRole("button", { name: "Delete" }));
    expect(await within(p).findByRole("alert")).toHaveTextContent(
      "A channel is simulcasting to this destination: stop or detach it first."
    );
  });

  it("LTV-26: schedule rules, max outputs and an unknown channel", async () => {
    const user = userEvent.setup();
    put.mockResolvedValue({});
    routes({
      "/api/tv/simulcast": simView({
        settings: {
          max_outputs_per_channel: 2,
          max_hours_per_month: 60,
          lead_seconds: 0,
          tail_seconds: 0,
        },
        destinations: [
          dest({ in_use: false }),
          dest({ id: "d2", name: "Page", kind: "facebook", in_use: false }),
          dest({ id: "d3", name: "Custom", kind: "rtmp", in_use: false }),
        ],
        attachments: [attachment({ id: "a9", channel_id: "c9", channel_name: "Nine" })],
      }),
    });
    await openLoaded();
    const p = await simPanel();
    await user.selectOptions(within(p).getByLabelText("Channel"), "c1");
    await user.click(within(p).getByRole("button", { name: "Simulcast…" }));
    let dialog = await screen.findByRole("dialog", { name: "Simulcast: Seerah" });
    const save = () => within(dialog).getByRole("button", { name: "Save" });

    await user.click(within(dialog).getByLabelText("My YouTube (YouTube Live)"));
    const fs = within(dialog).getByLabelText("My YouTube (YouTube Live)").closest("fieldset")!;
    await user.selectOptions(within(fs).getByLabelText("When"), "schedule");
    expect(within(fs).getByText("A schedule needs at least one rule.")).toBeInTheDocument();
    expect(save()).toBeDisabled();

    await user.click(within(fs).getByRole("button", { name: "Add rule" }));
    await user.click(within(fs).getByLabelText("Wed"));
    await user.click(within(fs).getByLabelText("Mon"));
    expect(save()).toBeEnabled();

    // three attached is more than the two allowed
    await user.click(within(dialog).getByLabelText("Page (Facebook Live)"));
    await user.click(within(dialog).getByLabelText("Custom (Custom RTMP/RTMPS)"));
    expect(within(dialog).getByRole("alert")).toHaveTextContent(
      "Too many destinations for one channel."
    );
    expect(save()).toBeDisabled();
    await user.click(within(dialog).getByLabelText("Page (Facebook Live)"));
    await user.click(within(dialog).getByLabelText("Custom (Custom RTMP/RTMPS)"));

    await user.click(save());
    expect(put).toHaveBeenCalledWith("/api/channels/c1/simulcast", [
      {
        destination_id: "d1",
        mode: "schedule",
        rules: [{ kind: "window", days: [1, 3], start: "20:00", end: "22:00" }],
        enabled: false,
      },
    ]);
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());

    // an output whose channel is not in the channel list still opens its editor
    const row = within(p).getByText(/Nine/, { selector: "bdi" }).closest("tr")!;
    await user.click(within(row).getByRole("button", { name: "Edit" }));
    dialog = await screen.findByRole("dialog", { name: "Simulcast: Nine" });
    expect(dialog).toBeInTheDocument();
  });

  // ─── Plex ──────────────────────────────────────────────────────────

  const plexWith = (status: Record<string, unknown> | null | undefined, top = {}) => ({
    ...plexEmpty,
    status,
    ...top,
  });

  it.each<[string, unknown, (card: HTMLElement) => void]>([
    [
      "dvrs null",
      plexWith({ ...plexStatus, dvrs: null }),
      (c) => expect(within(c).getByText("None")).toBeInTheDocument(),
    ],
    [
      "dvrs missing",
      plexWith(without(plexStatus, "dvrs")),
      (c) => expect(within(c).getByText("None")).toBeInTheDocument(),
    ],
    [
      "a DVR with devices null",
      plexWith({ ...plexStatus, dvrs: [{ ...plexStatus.dvrs[0], devices: null }] }),
      (c) => expect(within(c).getByText("dizqueTV: —")).toBeInTheDocument(),
    ],
    [
      "a DVR with devices missing",
      plexWith({ ...plexStatus, dvrs: [without(plexStatus.dvrs[0], "devices")] }),
      (c) => expect(within(c).getByText("dizqueTV: —")).toBeInTheDocument(),
    ],
    [
      "warnings null",
      plexWith({ ...plexStatus, warnings: null }),
      (c) => expect(within(c).getByText("Not added yet")).toBeInTheDocument(),
    ],
    [
      "warnings missing",
      plexWith(without(plexStatus, "warnings")),
      (c) => expect(within(c).getByText("Not added yet")).toBeInTheDocument(),
    ],
    ["status null", plexWith(null), (c) => expect(within(c).getByText("None")).toBeInTheDocument()],
    [
      "status missing",
      without(plexEmpty, "status"),
      (c) => expect(within(c).getByText("None")).toBeInTheDocument(),
    ],
    [
      "channels, maktaba and target missing",
      plexWith(without(without(without(plexStatus, "channels"), "maktaba"), "target")),
      (c) => expect(within(c).getByText("Not added yet")).toBeInTheDocument(),
    ],
    [
      "plex null",
      { ...plexEmpty, plex: null },
      (c) => {
        expect(
          within(c).getByText(/Turn on the Plex extension and sign in with Plex first/)
        ).toBeInTheDocument();
        expect(within(c).getByRole("link", { name: "Open Extensions" })).toHaveAttribute(
          "href",
          "/admin/extensions"
        );
      },
    ],
    [
      "plex missing",
      without(plexEmpty, "plex"),
      (c) => expect(within(c).getByText(/sign in with Plex first/)).toBeInTheDocument(),
    ],
    [
      "job null",
      { ...plexEmpty, job: null },
      (c) => expect(within(c).queryByText("Talking to Plex…")).not.toBeInTheDocument(),
    ],
  ])("LTV-28: Plex %s renders", async (_, body, check) => {
    routes({ "/api/tv/plex": body });
    await openLoaded();
    const card = (await screen.findByRole("heading", { name: "Plex" })).parentElement!;
    await waitFor(() => expect(within(card).getByText("DVRs in Plex")).toBeInTheDocument());
    check(card);
    expect(crashed()).toBeNull();
  });

  it.each([
    ["{sections:null}", { sections: null }],
    ["{}", {}],
    ["a plexlive section with fields null", { sections: [{ ...rules.sections[0], fields: null }] }],
  ])("LTV-28: rules %s hides the automatic settings", async (_, body) => {
    routes({ "/api/rules": body });
    await openLoaded();
    await screen.findByText("DVRs in Plex");
    await waitFor(() => expect(reads("/api/rules")).toBe(1));
    await act(async () => {});
    expect(screen.queryByRole("button", { name: "Save interval" })).not.toBeInTheDocument();
    expect(crashed()).toBeNull();
  });

  it("LTV-29: a Plex load failure is shown and the actions are disabled", async () => {
    routes({ "/api/tv/plex": fail(new Error("boom")) });
    await openLoaded();
    expect(await screen.findByText("Could not load the Plex status: boom")).toHaveAttribute(
      "role",
      "alert"
    );
    expect(screen.getByRole("button", { name: "Refresh Plex now" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Add Maktaba to Plex" })).toBeDisabled();
  });

  it("LTV-29: a busy job re-reads every 3 s and stops after 40 polls", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    routes({ "/api/tv/plex": { ...plexEmpty, job: { id: 1, state: "running" } } });
    await openLoaded();
    expect(await screen.findByText("Talking to Plex…")).toBeInTheDocument();
    const first = reads("/api/tv/plex");
    await act(() => vi.advanceTimersByTimeAsync(3_000));
    expect(reads("/api/tv/plex")).toBe(first + 1);
    for (let i = 0; i < 45; i++) await act(() => vi.advanceTimersByTimeAsync(3_000));
    const total = reads("/api/tv/plex");
    expect(total).toBeLessThanOrEqual(first + 42);
    await act(() => vi.advanceTimersByTimeAsync(30_000));
    expect(reads("/api/tv/plex")).toBe(total);
  });

  it("LTV-29: a paused job shows its reason", async () => {
    routes({
      "/api/tv/plex": { ...plexEmpty, job: { id: 1, state: "paused", paused_reason: "plex down" } },
    });
    await openLoaded();
    expect(await screen.findByText("Waiting: plex down")).toBeInTheDocument();
  });

  it("LTV-29: a refused register closes the confirm and shows why", async () => {
    const user = userEvent.setup();
    routes({ "/api/tv/hdhr": { ...device, enabled: true } });
    post.mockRejectedValueOnce(new Error("already registered"));
    await openLoaded();
    await user.click(await screen.findByRole("button", { name: "Add Maktaba to Plex" }));
    await user.click(
      within(await screen.findByRole("dialog")).getByRole("button", { name: "Add to Plex" })
    );
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(await screen.findByText("already registered")).toBeInTheDocument();
    expect(screen.getByText("already registered").closest("[role=alert]")).not.toBeNull();
  });

  it("LTV-29: a refused rules save shows the error; minutes are validated", async () => {
    const user = userEvent.setup();
    patch.mockRejectedValueOnce(apiErr(400, "Bad Request", { error: "auto_register: nope" }));
    await openLoaded();
    const auto = await screen.findByRole("switch", { name: /Add Maktaba to Plex by itself/ });
    await user.click(auto);
    expect(await screen.findByText("auto_register: nope")).toBeInTheDocument();
    await waitFor(() => expect(auto).toBeEnabled());

    const minutes = screen.getByLabelText(/Guide reload at most every/);
    const saveMin = screen.getByRole("button", { name: "Save interval" });
    for (const bad of ["0", "-5", "1.5"]) {
      await user.clear(minutes);
      await user.type(minutes, bad);
      expect(screen.getByText("At least 1 minute.")).toBeInTheDocument();
      expect(saveMin).toBeDisabled();
    }
    await user.clear(minutes);
    await user.type(minutes, "60");
    expect(screen.queryByText("At least 1 minute.")).not.toBeInTheDocument();
    expect(saveMin).toBeDisabled(); // the stored value
  });

  // ─── crash, links, leaving ─────────────────────────────────────────

  it("LTV-30: a render crash shows the route error fallback; Back works; polls stop", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    quietCrash();
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    routes({
      "/api/tv/cluster": clusterView({ on_air: [onAir({ name: {} as never })] }),
    });
    open();
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "This page hit an error and could not be shown."
    );
    const cluster = reads("/api/tv/cluster");
    const remoteReads = reads("/api/tv/remote");
    await act(() => vi.advanceTimersByTimeAsync(15_000));
    expect(reads("/api/tv/cluster")).toBe(cluster);
    expect(reads("/api/tv/remote")).toBe(remoteReads);

    await user.click(screen.getByRole("link", { name: "go elsewhere" }));
    expect(await screen.findByText("elsewhere")).toBeInTheDocument();
    routes();
    await user.click(screen.getByRole("button", { name: "go back" }));
    expect(await screen.findByRole("heading", { name: "Streaming hosts" })).toBeInTheDocument();
    expect(crashed()).toBeNull();
  });

  it("LTV-31: links to Changes, the change in flight, Extensions; doc links open a new tab", async () => {
    const user = userEvent.setup();
    routes({
      "/api/tv/info": { ...info, staging: staging({ change_in_flight_state: "ready" }) },
      "/api/tv/plex": { ...plexEmpty, plex: { enabled: false, configured: false, url: "" } },
      "/api/tv/remote": { integration: integration({ configured: false }) },
    });
    open({
      extraRoutes: (
        <>
          <Route path="/admin/live-tv/changes" element={<p>changes page</p>} />
          <Route path="/admin/live-tv/changes/:id" element={<p>change detail page</p>} />
          <Route path="/admin/extensions" element={<p>extensions page</p>} />
        </>
      ),
    });
    await screen.findByText("A1B2C3D4");

    await user.click(screen.getByRole("link", { name: "Open Changes" }));
    expect(await screen.findByText("changes page")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "go back" }));

    await user.click(await screen.findByRole("link", { name: "Change #7 is Ready" }));
    expect(await screen.findByText("change detail page")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "go back" }));

    expect(await screen.findByRole("link", { name: "Set it up" })).toHaveAttribute(
      "href",
      "/admin/extensions#cloudflare_stream"
    );
    await user.click(await screen.findByRole("link", { name: "Open Extensions" }));
    expect(await screen.findByText("extensions page")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "go back" }));
    await screen.findByText("A1B2C3D4");
    expect(reads("/api/tv/info")).toBeGreaterThanOrEqual(4); // fresh data each time back

    const external = screen
      .getAllByRole("link")
      .filter((a) => /^https?:/.test(a.getAttribute("href") ?? ""));
    expect(external.length).toBeGreaterThan(0);
    for (const a of external) {
      expect(a).toHaveAttribute("target", "_blank");
      expect(a).toHaveAttribute("rel", "noreferrer");
    }
  });

  it("LTV-32: leaving the page stops every poll", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    const errs = vi.spyOn(console, "error");
    routes({ "/api/tv/plex": { ...plexEmpty, job: { id: 1, state: "running" } } });
    await openLoaded();
    await screen.findByText("Talking to Plex…");
    await screen.findByTestId("remote-panel");
    await user.click(screen.getByRole("link", { name: "go elsewhere" }));
    await screen.findByText("elsewhere");
    const before = ["/api/tv/cluster", "/api/tv/remote", "/api/tv/plex"].map(reads);
    await act(() => vi.advanceTimersByTimeAsync(30_000));
    setVisibility("hidden");
    setVisibility("visible");
    await act(() => vi.advanceTimersByTimeAsync(5_000));
    expect(["/api/tv/cluster", "/api/tv/remote", "/api/tv/plex"].map(reads)).toEqual(before);
    expect(errs.mock.calls.filter((c) => String(c[0]).includes("unmounted"))).toHaveLength(0);
  });

  it("LTV-34: the whole page reads in Arabic, right to left, with addresses kept LTR", async () => {
    localStorage.setItem("mkt:locale", "ar");
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    await openLoaded();
    expect(document.documentElement.dir).toBe("rtl");
    expect(
      await screen.findByRole("heading", { name: "الوصول إلى البث المباشر" })
    ).toBeInTheDocument();
    expect(await screen.findByRole("heading", { name: "أجهزة البث" })).toBeInTheDocument();
    expect(
      within(await screen.findByTestId("remote-panel")).getByRole("heading", {
        name: /^المشاهدة عن بُعد/,
      })
    ).toBeInTheDocument();
    expect(await screen.findByRole("heading", { name: "البث المتزامن" })).toBeInTheDocument();
    for (const en of [
      "Live TV access",
      "Streaming hosts",
      "On air",
      "Remote viewing",
      "Simulcast",
    ]) {
      expect(screen.queryByText(en)).not.toBeInTheDocument();
    }
    const missing = warn.mock.calls
      .map((c) => String(c[0]))
      .filter(
        (m) =>
          m.includes("missing key") && /livetv\.|tv\.cluster\.|tv\.remote\.|simulcast\./.test(m)
      );
    expect(missing).toEqual([]);
    // host stream addresses stay LTR
    expect(screen.getByText("http://192.168.1.30:8081").tagName).toBe("BDI");
    // ingest URLs stay LTR
    expect(screen.getByText("rtmp://a.rtmp.youtube.com/live2")).toHaveAttribute("dir", "ltr");
  });

  it("LTV-36: labelled controls, named host cards and row buttons, modal dialogs", async () => {
    const user = userEvent.setup();
    routes({ "/api/tv/hdhr": { ...device, enabled: true } });
    await openLoaded();
    for (const label of ["Tuner name", /^Tuners/, /^Home full at/, /Guide reload at most every/]) {
      expect(screen.getByLabelText(label)).toBeInTheDocument();
    }
    const pc = await screen.findByRole("article", { name: "Gaming-PC" });
    for (const label of [/^Capacity/, /^Weight/, /^Stream base URL/]) {
      expect(within(pc).getByLabelText(label)).toBeInTheDocument();
    }
    expect(within(pc).getByRole("switch", { name: /\S/ })).toBeInTheDocument();
    for (const name of ["Mac-Studio", "Gaming-PC", "Old-Laptop", "NAS"]) {
      expect(screen.getByRole("article", { name })).toBeInTheDocument();
    }
    for (const s of screen.getAllByRole("switch")) expect(s).toHaveAccessibleName();

    const openers: Array<[() => HTMLElement, string]> = [
      [() => screen.getByRole("button", { name: "Move Seerah" }), "Move Seerah"],
      [() => screen.getByRole("button", { name: "Stop Seerah" }), "Stop Seerah?"],
      [
        () => within(hostCard("NAS")).getByRole("button", { name: "Move now" }),
        "Move every channel off NAS?",
      ],
      [() => screen.getByRole("button", { name: "Add Maktaba to Plex" }), ""],
      [() => screen.getByRole("button", { name: "Add destination" }), "Add destination"],
    ];
    for (const [opener, title] of openers) {
      const btn = opener();
      await user.click(btn);
      const dialog = await screen.findByRole("dialog");
      expect(dialog).toHaveAttribute("aria-modal", "true");
      if (title) expect(dialog).toHaveAccessibleName(title);
      expect(dialog.contains(document.activeElement)).toBe(true);
      await user.keyboard("{Escape}");
      await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
      expect(document.activeElement).toBe(btn);
    }
  });
});
