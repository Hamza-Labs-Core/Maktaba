// Story 54.13 T01-T05 and T07: Settings → Plugins against a mocked API: the cards (a plugin
// its breaker held says so, with Start), Arabic capability words and RTL, a permissions
// change's PUT body, What it wrote's revert body, a viewer's not-allowed page; and 54.10's
// typed name and the policy's password.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { get, post, put, del, upload } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  put: vi.fn(),
  del: vi.fn(),
  upload: vi.fn(),
}));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return {
    ...actual,
    uploadWithProgress: upload,
    api: { ...actual.api, get, post, put, delete: del },
  };
});

import en from "../../i18n/en.json";
import ar from "../../i18n/ar.json";
import { ApiError } from "../../lib/api";
import type { Plugin, PluginDetail } from "../../lib/pluginsApi";
import { memberUser, renderPage } from "../../test/pageHarness";
import { PluginsSection, capabilityTitle } from "./PluginsSection";

const EN = en as Record<string, string>;
const AR = ar as Record<string, string>;

function plugin(over: Partial<Plugin> = {}): Plugin {
  return {
    id: "dev.example.tagger",
    component: "plugin.dev.example.tagger",
    version: "1.0.0",
    digest: "d",
    names: {
      name: { en: "Tagger", ar: "الواسم" },
      summary: { en: "Adds tags.", ar: "يضيف وسومًا." },
    },
    review_level: "none",
    rating: 4,
    installed_at: "2026-11-06T10:00:00Z",
    want: "enabled",
    generation: 3,
    phase: "running",
    grants: { capabilities: [], libraries: [], all_libraries: false, generation: 1 },
    required: ["media.read"],
    optional: ["tags.write"],
    health: { tripped: false, jobs_done: 12, jobs_failed: 1, recent: [] },
    ...over,
  };
}

function detail(over: Partial<PluginDetail> = {}): PluginDetail {
  return {
    ...plugin(),
    manifest: { maktaba: ">=0.3.0" },
    stages: [
      {
        name: "tag",
        queue: "p_1_tag",
        subject: "video",
        version: 1,
        pending: 5,
        running: 0,
        done_24h: 2,
        failed_24h: 0,
      },
    ],
    writes: 3,
    data_rows: 0,
    data_bytes: 0,
    data_mb: 50,
    ...over,
  };
}

function problem(status: number, code: string, extra: Record<string, unknown> = {}) {
  return new ApiError({
    type: "x",
    title: code,
    status,
    detail: code,
    code,
    ...extra,
  } as never);
}

let trust: unknown = {
  store_keys: false,
  revoked_version: 0,
  revoked_expired: false,
  catalog_version: 0,
  catalog_expired: false,
  store_installs_ok: false,
};

function routes(d: PluginDetail = detail(), list: Plugin[] = [plugin()]) {
  get.mockImplementation(async (path: string) => {
    if (path === "/api/plugins") return { plugins: list, policy: { allow_unreviewed: false } };
    if (path === `/api/plugins/${d.id}`) return d;
    if (path.startsWith(`/api/plugins/${d.id}/writes`))
      return {
        writes: [
          {
            id: 7,
            version: "1.0.0",
            target: "video_tags",
            subject_id: "v1",
            field: "drama",
            before: null,
            after: "Drama",
            at: "2026-11-06T10:00:00Z",
          },
          {
            id: 8,
            version: "1.0.0",
            target: "video_tags",
            subject_id: "v2",
            field: "news",
            before: null,
            after: "News",
            at: "2026-11-06T10:01:00Z",
          },
        ],
      };
    if (path === `/api/plugins/${d.id}/settings`) return { schema: null, values: {}, strings: {} };
    if (path === "/api/plugins/trust") return trust;
    if (path === "/api/plugins/store-account")
      return { linked: false, store: "https://plugins.maktaba.tv" };
    if (path === "/api/libraries/summary")
      return {
        items: [{ id: "lib-1", name: "Films", videos: 1, poster_video_ids: [], pinned: false }],
      };
    throw new Error("unexpected " + path);
  });
}

const open = (path = "/settings/plugins") =>
  renderPage(<PluginsSection />, { path, route: "/settings/plugins/*" });

beforeEach(() => {
  localStorage.removeItem("mkt:locale");
  for (const f of [get, post, put, del, upload]) f.mockReset();
});
afterEach(() => localStorage.removeItem("mkt:locale"));

describe("Settings → Plugins", () => {
  it("T01: cards with their badges; a plugin its breaker held says so, with Start", async () => {
    routes(detail(), [
      plugin(),
      plugin({
        id: "dev.example.flaky",
        names: { name: { en: "Flaky", ar: "متقلبة" }, summary: { en: "", ar: "" } },
        phase: "stopped",
        held_by: "plugin-breaker",
        switch_locked: "held",
        health: { tripped: true, jobs_done: 0, jobs_failed: 6, recent: [] },
      }),
    ]);
    open();
    expect(await screen.findByRole("heading", { name: /Tagger/ })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: /Flaky/ })).toBeInTheDocument();
    expect(screen.getAllByText(EN["plugins.badge.unreviewed"])).toHaveLength(2);
    expect(screen.getByText(EN["components.reason.plugin_crash_loop"])).toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: EN["plugins.action.start"] })).toHaveLength(1);
  });

  it("54.17: an expired list of withdrawn plugins warns; a withdrawn plugin says so and cannot be started", async () => {
    trust = {
      store_keys: true,
      revoked_version: 9,
      revoked_issued: "2026-11-01T10:00:00Z",
      revoked_expired: true,
      catalog_version: 0,
      catalog_expired: false,
      store_installs_ok: false,
    };
    const revocation = {
      action: "disable",
      reason_code: "malware",
      versions: "<1.3.0",
      issued: "2026-11-06T10:00:00Z",
    };
    const d = detail({ revoked: true, revocation } as never);
    routes(d, [plugin({ revoked: true, revocation } as never)]);
    open();
    expect(await screen.findByText(EN["plugins.badge.withdrawn"])).toBeInTheDocument();
    expect(screen.getByRole("alert")).toHaveTextContent(
      EN["plugins.trust.expired"].split("{date}")[0]!.trim()
    );
    trust = {
      store_keys: false,
      revoked_version: 0,
      revoked_expired: false,
      catalog_version: 0,
      catalog_expired: false,
      store_installs_ok: false,
    };
  });

  it("offline (MAKTABA_OFFLINE): the banner says the home doesn't check, and plugins keep running", async () => {
    trust = {
      store_keys: true,
      revoked_version: 9,
      revoked_issued: "2026-11-01T10:00:00Z",
      revoked_expired: true,
      catalog_version: 0,
      catalog_expired: false,
      store_installs_ok: false,
      offline: true,
    };
    routes(detail(), [plugin()]);
    open();
    expect(await screen.findByRole("alert")).toHaveTextContent(
      EN["plugins.trust.offline"].split("(")[0]!.trim()
    );
    trust = {
      store_keys: false,
      revoked_version: 0,
      revoked_expired: false,
      catalog_version: 0,
      catalog_expired: false,
      store_installs_ok: false,
    };
  });

  it("54.17: a store warning shows on the plugin's page; auto-update is a switch", async () => {
    const revocation = {
      action: "warn",
      reason_code: "severe_bug",
      versions: "*",
      issued: "2026-11-06T10:00:00Z",
    };
    const d = detail({ review_level: "reviewed", revocation, auto_update: false } as never);
    routes(d, [plugin({ review_level: "reviewed", revocation } as never)]);
    put.mockResolvedValue(d);
    open();
    expect(await screen.findByText(EN["plugins.badge.store_warning"])).toBeInTheDocument();
    const user = userEvent.setup();
    await user.click(await screen.findByRole("link", { name: EN["plugins.action.open"] }));
    expect(await screen.findByText(/warns about this plugin \(a severe bug\)/)).toBeInTheDocument();
    await user.click(screen.getByRole("switch", { name: EN["plugins.auto_update.label"] }));
    expect(put).toHaveBeenCalledWith(`/api/plugins/${d.id}/auto-update`, { on: true });
  });

  it("54.14 §2: a store-countersigned plugin shows its review level, not Unreviewed", async () => {
    routes(detail(), [
      plugin({ review_level: "reviewed" }),
      plugin({
        id: "dev.example.auto",
        names: { name: { en: "Auto", ar: "آلي" }, summary: { en: "", ar: "" } },
        review_level: "automated",
      }),
    ]);
    open();
    expect(await screen.findByRole("heading", { name: /Tagger/ })).toBeInTheDocument();
    expect(screen.getByText(EN["plugins.badge.reviewed"])).toBeInTheDocument();
    expect(screen.getByText(EN["plugins.badge.automated"])).toBeInTheDocument();
    expect(screen.queryByText(EN["plugins.badge.unreviewed"])).not.toBeInTheDocument();
  });

  it("T02: Arabic: Maktaba's words for each capability, the plugin's own name, RTL", async () => {
    localStorage.setItem("mkt:locale", "ar");
    routes();
    open();
    const user = userEvent.setup();
    await user.click(await screen.findByRole("link", { name: AR["plugins.action.open"] }));
    expect(await screen.findByText(AR["plugins.capability.media_read.title"])).toBeInTheDocument();
    expect(screen.getByText(AR["plugins.capability.tags_write.title"])).toBeInTheDocument();
    expect(screen.getByRole("heading", { level: 2, name: /الواسم/ })).toBeInTheDocument();
    expect(document.documentElement.dir).toBe("rtl");
    expect(capabilityTitle((k) => AR[k] ?? k, "plugin-data.read:dev.x.y").title).toContain(
      "dev.x.y"
    );
  });

  it("T03: a permissions change PUTs the capabilities and libraries ticked", async () => {
    routes();
    put.mockResolvedValue({
      capabilities: ["media.read"],
      libraries: ["lib-1"],
      all_libraries: false,
    });
    open();
    const user = userEvent.setup();
    await user.click(await screen.findByRole("link", { name: EN["plugins.action.open"] }));
    expect(await screen.findByText(EN["plugins.permissions.no_library"])).toBeInTheDocument();
    await user.click(
      screen.getByRole("checkbox", { name: new RegExp(EN["plugins.capability.media_read.title"]) })
    );
    await user.click(await screen.findByRole("checkbox", { name: /Films/ }));
    await user.click(screen.getByRole("button", { name: EN["plugins.permissions.save"] }));
    await waitFor(() =>
      expect(put).toHaveBeenCalledWith("/api/plugins/dev.example.tagger/grants", {
        capabilities: ["media.read"],
        libraries: ["lib-1"],
        all_libraries: false,
        generation: 1,
      })
    );
  });

  it("T04: What it wrote: the selected writes are reverted and the list read again", async () => {
    routes();
    post.mockResolvedValue({ reverted: 1 });
    open();
    const user = userEvent.setup();
    await user.click(await screen.findByRole("link", { name: EN["plugins.action.open"] }));
    const boxes = await screen.findAllByRole("checkbox", { name: EN["plugins.writes.select"] });
    await user.click(boxes[1]);
    await user.click(
      screen.getByRole("button", { name: EN["plugins.writes.revert"].replace("{n}", "1") })
    );
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith("/api/plugins/dev.example.tagger/writes/revert", {
        ids: [8],
      })
    );
    expect(
      await screen.findByText(EN["plugins.writes.reverted"].replace("{n}", "1"))
    ).toBeInTheDocument();
  });

  it("T05: a viewer sees the not-allowed page and calls nothing", () => {
    renderPage(<PluginsSection />, { path: "/settings/plugins", user: memberUser });
    expect(screen.getByText(EN["common.forbidden.title"])).toBeInTheDocument();
    expect(get).not.toHaveBeenCalled();
  });

  it("54.10: an unreviewed file asks for the plugin's name, then installs with it", async () => {
    routes();
    upload
      .mockRejectedValueOnce(
        problem(400, "plugin_name_mismatch", { name: "Tagger", name_ar: "الواسم" })
      )
      .mockResolvedValueOnce({ result: { plugin_id: "dev.example.tagger" }, plugin: detail() });
    open();
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: EN["plugins.install.button"] }));
    const file = new File(["x"], "tagger.mkplugin");
    await user.upload(screen.getByLabelText(EN["plugins.install.file"]), file);
    await user.click(screen.getByRole("button", { name: EN["plugins.install.install"] }));
    const name = await screen.findByRole("textbox");
    await user.type(name, "Tagger");
    await user.click(screen.getByRole("button", { name: EN["plugins.install.install"] }));
    await waitFor(() => expect(upload).toHaveBeenCalledTimes(2));
    const form = upload.mock.calls[1][1] as FormData;
    expect(form.get("confirm_name")).toBe("Tagger");
    expect(form.get("libraries")).toBe("[]");
    expect(await screen.findByRole("heading", { level: 2, name: /Tagger/ })).toBeInTheDocument();
  });

  it("54.10: allowing unreviewed plugins asks for the password", async () => {
    routes();
    put.mockResolvedValue({ allow_unreviewed: true });
    open();
    const user = userEvent.setup();
    await user.click(
      await screen.findByRole("switch", { name: EN["plugins.policy.allow_unreviewed"] })
    );
    await user.type(await screen.findByLabelText(EN["plugins.policy.password"]), "secret-pass");
    await user.click(screen.getByRole("button", { name: EN["plugins.policy.confirm"] }));
    await waitFor(() =>
      expect(put).toHaveBeenCalledWith("/api/plugins/policy", {
        allow_unreviewed: true,
        password: "secret-pass",
      })
    );
  });

  // story 54.41 T03-T05: the plugin's flow drawn with it highlighted and a member that is
  // not installed named; the whole home's graph; Arabic right to left; turning off an
  // upstream lists what goes idle first; a flow refusal shows its own words.
  const node = (plugin: string, kind: string, name: string, state = "enabled") => ({
    id: kind === "core" ? `core:${name}` : `${kind}:${plugin}/${name}`,
    kind,
    name,
    plugin,
    state,
    subject: "video",
  });
  const flowGraph = {
    nodes: [
      node("", "core", "audio.ready"),
      node("dev.example.tagger", "stage", "tag"),
      node("dev.example.tagger", "event", "tag.done"),
      node("dev.example.writer", "stage", "write", "missing"),
    ],
    edges: [
      { from: "core:audio.ready", kind: "trigger", to: "stage:dev.example.tagger/tag" },
      {
        from: "stage:dev.example.tagger/tag",
        kind: "emit",
        to: "event:dev.example.tagger/tag.done",
      },
      {
        from: "event:dev.example.tagger/tag.done",
        kind: "subscribe",
        to: "stage:dev.example.writer/write",
      },
    ],
  };
  function flowRoutes(stages: string[] = []) {
    routes();
    const base = get.getMockImplementation() as (p: string) => Promise<unknown>;
    get.mockImplementation(async (path: string) => {
      if (path === "/api/plugins/dev.example.tagger/flow")
        return {
          id: "dev.example.flow",
          version: "1.0.0",
          name: "flow.name",
          graph: flowGraph,
          missing: [{ id: "dev.example.writer", version: ">=1.0.0" }],
          findings: [],
        };
      if (path === "/api/plugins/graph")
        return {
          graph: { nodes: flowGraph.nodes.slice(0, 3), edges: flowGraph.edges.slice(0, 2) },
          findings: [],
        };
      if (path === "/api/plugins/dev.example.tagger/downstream")
        return {
          stages,
          also: [],
          processes: [],
          loops: [],
          features: [],
          queued_jobs_parked: 0,
          drain_s: 0,
        };
      return base(path);
    });
  }

  it("54.41 T03, T05: the flow, this plugin highlighted, the missing member named", async () => {
    flowRoutes();
    open();
    const user = userEvent.setup();
    await user.click(await screen.findByRole("link", { name: EN["plugins.action.open"] }));
    const charts = await screen.findAllByTestId("flow-chart");
    expect(charts).toHaveLength(2);
    expect(screen.getByText(/dev\.example\.writer >=1\.0\.0/)).toBeInTheDocument();
    const mine = charts[0].querySelector('[data-id="stage:dev.example.tagger/tag"]');
    expect(mine?.getAttribute("class")).toContain("fc-hl");
    const missing = charts[0].querySelector('[data-id="stage:dev.example.writer/write"]');
    expect(missing?.getAttribute("class")).toContain("fc-off");
    expect(missing?.textContent).toContain(EN["plugins.flow.missing"]);
    expect(
      screen.getAllByText(new RegExp(EN["plugins.flow.event.audio.ready"])).length
    ).toBeGreaterThan(0);
    expect(charts[0].querySelector("foreignObject, a, image, script")).toBeNull();
  });

  it("54.41 T04: in Arabic the chart runs right to left, core events in Arabic", async () => {
    localStorage.setItem("mkt:locale", "ar");
    flowRoutes();
    open();
    const user = userEvent.setup();
    await user.click(await screen.findByRole("link", { name: AR["plugins.action.open"] }));
    const [chart] = await screen.findAllByTestId("flow-chart");
    const svg = chart.querySelector("svg") as SVGElement;
    expect(svg.getAttribute("direction")).toBe("rtl");
    const x = (id: string) =>
      Number(chart.querySelector(`[data-id="${id}"] rect`)?.getAttribute("x"));
    expect(x("core:audio.ready")).toBeGreaterThan(x("stage:dev.example.tagger/tag"));
    expect(chart.textContent).toContain(AR["plugins.flow.event.audio.ready"]);
  });

  it("54.39 §6: turning off a plugin others depend on lists what goes idle first", async () => {
    flowRoutes(["dev.example.writer/write"]);
    post.mockResolvedValue(detail({ want: "installed" }));
    open();
    const user = userEvent.setup();
    await user.click(await screen.findByRole("link", { name: EN["plugins.action.open"] }));
    await user.click(await screen.findByRole("switch", { name: /Tagger/ }));
    expect(await screen.findByText("dev.example.writer/write")).toBeInTheDocument();
    expect(post).not.toHaveBeenCalled();
    const dialog = screen.getByRole("dialog");
    await user.click(
      Array.from(dialog.querySelectorAll("button")).find(
        (b) => b.textContent === EN["plugins.action.disable"]
      ) as HTMLButtonElement
    );
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith("/api/plugins/dev.example.tagger/disable")
    );
  });

  it("54.39 §6: an install a loop refuses shows the loop in the admin's language", async () => {
    localStorage.setItem("mkt:locale", "ar");
    routes();
    upload.mockRejectedValueOnce(
      problem(409, "plugin_flow_cycle", {
        message: "These plugins would start each other in a loop: a/x → a/x.",
        message_ar: "ستشغّل هذه الإضافات بعضها بعضًا في حلقة: a/x → a/x.",
      })
    );
    open();
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: AR["plugins.install.button"] }));
    await user.upload(
      screen.getByLabelText(AR["plugins.install.file"]),
      new File(["x"], "a.mkplugin")
    );
    await user.click(screen.getByRole("button", { name: AR["plugins.install.install"] }));
    expect(await screen.findByRole("alert")).toHaveTextContent("في حلقة: a/x → a/x");
  });

  it("54.09 §3: problem reports leave its logs out until the admin ticks it on its page", async () => {
    routes();
    put.mockResolvedValue({ plugin_id: "dev.example.tagger", report_logs: true });
    open();
    const user = userEvent.setup();
    await user.click(await screen.findByRole("link", { name: EN["plugins.action.open"] }));
    const box = await screen
      .findByRole("switch", { name: EN["plugins.report_logs.label"] })
      .catch(() => screen.getByRole("checkbox", { name: EN["plugins.report_logs.label"] }));
    expect(box).not.toBeChecked();
    expect(screen.getByText(EN["plugins.report_logs.hint"])).toBeInTheDocument();
    await user.click(box);
    await waitFor(() =>
      expect(put).toHaveBeenCalledWith("/api/plugins/dev.example.tagger/report-logs", {
        include: true,
      })
    );
    expect(await screen.findByText(EN["plugins.report_logs.on"])).toBeInTheDocument();
  });

  it("T07: every word the page uses is in English and Arabic", () => {
    for (const k of Object.keys(EN).filter(
      (k) => k.startsWith("plugins.") || k === "settings.section.plugins"
    )) {
      expect(AR[k], k).toBeTruthy();
    }
  });
});

describe("Settings → Plugins: one page for every plugin (Epic 54, story 54.44)", () => {
  const light = {
    process: "plugin-runner/light",
    group: "light",
    component: "core.plugin_runners",
    machine: "home",
    phase: "running",
    rss_mb: 310,
    scope: "process" as const,
    stages: ["subtitle_gen"],
    shared_with: ["tv.maktaba.thumbnails"],
    shared_core: true,
  };
  /** A plugin Maktaba publishes, as GET /api/plugins lists it. */
  function maktaba(over: Partial<Plugin> = {}): Plugin {
    return plugin({
      id: "tv.maktaba.subtitles",
      component: "subtitles",
      version: "0.3.90",
      names: {
        name: { en: "Subtitles", ar: "الترجمات" },
        summary: { en: "Writes subtitle files.", ar: "تكتب ملفات الترجمة." },
      },
      review_level: "reviewed",
      rating: 0,
      required: [],
      optional: [],
      publisher: { id: "maktaba", verified: true },
      source: "release",
      stage_names: ["subtitle_gen"],
      queues: ["subtitle_gen"],
      activity: { pending: 4, running: 1, done_24h: 20, failed_24h: 3, problems: 2, avg_ms: 3000 },
      resources: {
        processes: [light],
        shared: true,
        data_rows: 0,
        data_bytes: 0,
        data_mb: 0,
        memory_mb: 0,
      },
      settings: [],
      ...over,
    });
  }
  const thumbs = () =>
    maktaba({
      id: "tv.maktaba.thumbnails",
      component: "media.thumbnails",
      names: { name: { en: "Thumbnails", ar: "الصور المصغّرة" }, summary: { en: "", ar: "" } },
      queues: ["thumbnail", "thumb_small"],
      activity: { pending: 0, running: 0, done_24h: 0, failed_24h: 0, problems: 0 },
    });
  const third = () =>
    plugin({
      publisher: { id: "", verified: false },
      source: "file",
      queues: ["p_1_tag"],
      activity: { pending: 0, running: 0, done_24h: 2, failed_24h: 0, problems: 0 },
    });
  const fromStore = () =>
    plugin({
      id: "dev.example.store",
      names: { name: { en: "Storey", ar: "متجرية" }, summary: { en: "", ar: "" } },
      review_level: "reviewed",
      publisher: { id: "", verified: false },
      source: "store",
    });
  function maktabaDetail(over: Partial<PluginDetail> = {}): PluginDetail {
    return {
      ...maktaba(),
      manifest: { maktaba: ">=0.0.0" },
      stages: [
        {
          name: "subtitle_gen",
          queue: "subtitle_gen",
          subject: "video",
          version: 1,
          pending: 4,
          running: 1,
          done_24h: 20,
          failed_24h: 3,
          problems: 2,
          avg_ms: 3000,
          runs_in: "light",
        },
      ],
      writes: 0,
      data_rows: 0,
      data_bytes: 0,
      data_mb: 0,
      capabilities: [{ key: "fp.trusted.settings.read", param: "subtitles" }],
      machines: [{ machine: "home", phase: "running", restarts_24h: 0, stale: false }],
      settings: ["rules.repair", "toml:transcription.workers"],
      ...over,
    };
  }
  const quiet = {
    stages: [],
    also: [],
    processes: [],
    loops: [],
    features: [],
    queued_jobs_parked: 0,
    drain_s: 0,
  };
  /** The page's reads: the list, one Maktaba plugin's page and one third-party plugin's. */
  function page(
    list: Plugin[],
    opts: { detail?: PluginDetail; effect?: Record<string, unknown>; core?: boolean } = {}
  ) {
    const d = opts.detail ?? maktabaDetail();
    const tp = detail({ ...third(), resources: thirdResources });
    get.mockImplementation(async (path: string) => {
      if (path === "/api/plugins")
        return {
          plugins: list,
          policy: { allow_unreviewed: false },
          core:
            opts.core === false
              ? []
              : [{ component: "core.pipeline", title_key: "components.core.pipeline.title" }],
        };
      if (path === `/api/plugins/${d.id}`) return d;
      if (path === `/api/plugins/${tp.id}`) return tp;
      if (path.endsWith("/downstream")) return opts.effect ?? quiet;
      if (path.endsWith("/flow"))
        return { id: d.id, version: "1", name: "", graph: { nodes: [], edges: [] }, missing: [] };
      if (path.endsWith("/graph")) return { graph: { nodes: [], edges: [] }, findings: [] };
      if (path.includes("/writes")) return { writes: [] };
      if (path.endsWith("/settings")) return { schema: null, values: {}, strings: {} };
      if (path === "/api/plugins/trust") return trust;
      if (path === "/api/plugins/store-account")
        return { linked: false, store: "https://plugins.maktaba.tv" };
      if (path === "/api/libraries/summary") return { items: [] };
      throw new Error("unexpected " + path);
    });
  }
  const thirdResources = {
    processes: [
      {
        process: "plugins/",
        group: "host",
        component: "plugins.host",
        machine: "home",
        rss_mb: 120,
        scope: "process" as const,
        stages: [],
        shared_with: ["dev.example.other"],
        shared_core: false,
      },
    ],
    shared: true,
    data_rows: 12,
    data_bytes: 5 << 20,
    data_mb: 50,
    memory_mb: 256,
    peak_memory_mb: 90,
  };
  const card = (name: RegExp) => screen.findByRole("article", { name });

  // Epic 54: a first-party runner that failed is named above the list, with its stages
  it("names a failed plugin runner, its machine and its stages", async () => {
    get.mockImplementation(async (path: string) => {
      if (path === "/api/plugins") return { plugins: [], policy: { allow_unreviewed: false } };
      if (path === "/api/admin/system/status")
        return {
          status: "degraded",
          code: "",
          reason: "",
          checked_at: "2026-11-08T12:00:00Z",
          processes_supervised: true,
          items: [
            {
              id: "plugin_runners",
              group: "services",
              kind: "plugin_runners",
              name: "Plugin runners",
              status: "degraded",
              code: "plugin_runners_failed",
              reason: "",
              last_checked: "2026-11-08T12:00:00Z",
              help: "plugin_runners",
              details: { failed: [{ machine: "node-b", runner: "net", stages: ["books"] }] },
            },
          ],
        };
      throw new Error("unexpected " + path);
    });
    open();
    const box = await screen.findByTestId("runners-warning");
    expect(box).toHaveTextContent(/The \u2068?net\u2069? runner on \u2068?node-b/);
    expect(box).toHaveTextContent("(books)");
    expect(within(box).getByRole("link")).toHaveAttribute("href", "/settings/components");
  });

  it("T01: one list of every plugin, each marked with who publishes it, and no built-in class", async () => {
    page([third(), maktaba(), thumbs()]);
    open();
    const sub = await card(/Subtitles/);
    expect(within(sub).getByText(EN["plugins.publisher.maktaba_verified"])).toBeInTheDocument();
    expect(within(sub).getByText("0.3.90")).toBeInTheDocument();
    expect(within(sub).getByRole("switch", { name: /Subtitles/ })).toBeChecked();
    expect(sub).toHaveTextContent("In the last day: 20 jobs done, 3 failed");
    expect(sub).toHaveTextContent("310 MB memory, shared with other plugins");
    expect(sub).toHaveTextContent("1 running, 4 waiting");
    expect(within(sub).getByRole("link", { name: /Subtitles/ })).toHaveAttribute(
      "href",
      "/settings/plugins/tv.maktaba.subtitles"
    );
    const tag = await card(/Tagger/);
    expect(within(tag).getByText(EN["plugins.source.file"])).toBeInTheDocument();
    expect(within(tag).getByRole("switch", { name: /Tagger/ })).toBeChecked();
    // all in one list, by one heading level: no second section for Maktaba's
    expect(screen.getAllByRole("article")).toHaveLength(3);
    expect(screen.queryByText(/Built into Maktaba/)).toBeNull();
    expect(screen.queryByText(/on the Components page/)).toBeNull();
    // core is said in one line, with where to see it, and has no card
    const core = screen.getByTestId("plugins-core");
    expect(core).toHaveTextContent(EN["components.core.pipeline.title"]);
    expect(
      within(core).getByRole("link", { name: EN["plugins.core.components_link"] })
    ).toHaveAttribute("href", "/settings/components");
    expect(
      within(core).getByRole("link", { name: EN["plugins.core.health_link"] })
    ).toHaveAttribute("href", "/admin/health");
  });

  it("T02: the filter shows all, Maktaba's, others' from the store, or the ones from a file", async () => {
    page([third(), fromStore(), maktaba(), thumbs()]);
    open();
    await card(/Subtitles/);
    const filter = screen.getByTestId("plugins-filter");
    const pick = (key: string) =>
      userEvent.click(within(filter).getByRole("button", { name: new RegExp("^" + EN[key]) }));
    expect(within(filter).getByRole("button", { name: /^All \(4\)/ })).toHaveAttribute(
      "aria-pressed",
      "true"
    );
    await pick("plugins.filter.maktaba");
    expect(screen.getAllByRole("article").map((a) => a.getAttribute("aria-label"))).toEqual([
      "Subtitles",
      "Thumbnails",
    ]);
    await pick("plugins.filter.others");
    expect(screen.getAllByRole("article").map((a) => a.getAttribute("aria-label"))).toEqual([
      "Storey",
    ]);
    await pick("plugins.filter.file");
    expect(screen.getAllByRole("article").map((a) => a.getAttribute("aria-label"))).toEqual([
      "Tagger",
    ]);
    await pick("plugins.filter.all");
    expect(screen.getAllByRole("article")).toHaveLength(4);
  });

  it("T02: the filter is in the address, so a link opens the list filtered", async () => {
    page([third(), maktaba()]);
    open("/settings/plugins?show=maktaba");
    await card(/Subtitles/);
    expect(screen.getAllByRole("article")).toHaveLength(1);
  });

  it("T03: a Maktaba plugin is switched on its own card, through its own route", async () => {
    page([maktaba({ want: "installed", phase: "off" }), third()]);
    post.mockResolvedValue(maktabaDetail());
    open();
    const sub = await card(/Subtitles/);
    const sw = within(sub).getByRole("switch", { name: /Subtitles/ });
    expect(sw).not.toBeChecked();
    await userEvent.click(sw);
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith("/api/plugins/tv.maktaba.subtitles/enable")
    );
    // nothing of the Components routes is called from this page
    expect(put).not.toHaveBeenCalled();
  });

  it("T03: a third-party plugin's switch is the same switch", async () => {
    page([third()]);
    post.mockResolvedValue(detail({ want: "installed" }));
    open();
    const tag = await card(/Tagger/);
    await userEvent.click(within(tag).getByRole("switch", { name: /Tagger/ }));
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith("/api/plugins/dev.example.tagger/disable")
    );
  });

  it("T04: turning off names exactly what else stops, by plugin name, before anything changes", async () => {
    page(
      [
        maktaba({
          id: "tv.maktaba.transcription",
          component: "transcription",
          names: { name: { en: "Transcription", ar: "التفريغ" }, summary: { en: "", ar: "" } },
        }),
        maktaba(),
      ],
      {
        effect: {
          stages: [],
          also: [
            {
              component: "subtitles",
              title_key: "components.subtitles.title",
              plugin: "tv.maktaba.subtitles",
              to: "waiting",
            },
            {
              component: "parental.detect",
              title_key: "components.parental.detect.title",
              to: "degraded",
            },
          ],
          processes: ["pipeline/stt-*", "plugin-runner/ml"],
          loops: [],
          features: ["transcripts"],
          queued_jobs_parked: 7,
          drain_s: 120,
        },
      }
    );
    post.mockResolvedValue(maktabaDetail());
    open();
    const tr = await card(/Transcription/);
    await userEvent.click(within(tr).getByRole("switch", { name: /Transcription/ }));
    const dialog = await screen.findByRole("dialog");
    expect(post).not.toHaveBeenCalled();
    expect(within(dialog).getByTestId("effect-stops")).toHaveTextContent(
      /This also stops: .?Subtitles/
    );
    expect(within(dialog).getByTestId("effect-degrades")).toHaveTextContent(
      EN["components.parental.detect.title"]
    );
    expect(within(dialog).getByTestId("effect-features")).toHaveTextContent(
      EN["components.feature.transcripts"]
    );
    expect(within(dialog).getByTestId("effect-parts")).toHaveTextContent("plugin-runner/ml");
    expect(dialog).toHaveTextContent("7 queued jobs will wait");
    // Cancel changes nothing
    await userEvent.click(within(dialog).getByRole("button", { name: EN["common.cancel"] }));
    expect(post).not.toHaveBeenCalled();
    await userEvent.click(within(tr).getByRole("switch", { name: /Transcription/ }));
    await userEvent.click(
      within(await screen.findByRole("dialog")).getByRole("button", {
        name: EN["plugins.action.disable"],
      })
    );
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith("/api/plugins/tv.maktaba.transcription/disable")
    );
  });

  it("T04: a refused switch says why in words and leaves the switch as it was", async () => {
    page([maktaba({ want: "installed", phase: "off" })]);
    post.mockRejectedValue(problem(409, "plugin_with_release"));
    open();
    const sub = await card(/Subtitles/);
    await userEvent.click(within(sub).getByRole("switch", { name: /Subtitles/ }));
    expect(await within(sub).findByRole("alert")).toHaveTextContent(
      EN["plugins.error.plugin_with_release"]
    );
    expect(within(sub).getByRole("switch", { name: /Subtitles/ })).not.toBeChecked();
  });

  it.each([
    ["licence_missing", "plugins.lock.licence_missing"],
    ["held", "plugins.lock.held"],
    ["revoked", "plugins.lock.revoked"],
    ["platform_unsupported", "components.reason.platform_unsupported"],
  ])("T05: a switch that cannot be used (%s) is disabled, with why in words", async (code, key) => {
    page([maktaba({ switch_locked: code, held_by: code === "held" ? "owner" : undefined })]);
    open();
    const sub = await card(/Subtitles/);
    const sw = within(sub).getByRole("switch", { name: /Subtitles/ });
    expect(sw).toBeDisabled();
    const why = within(sub).getByTestId("switch-locked");
    expect(why).toHaveTextContent(EN[key]);
    expect(sw).toHaveAttribute("aria-describedby", why.id);
    // a stopped plugin can be started from the same place
    expect(within(sub).queryAllByRole("button", { name: EN["plugins.action.start"] })).toHaveLength(
      code === "held" ? 1 : 0
    );
  });

  it("T06: a card's problems count links to the failures view filtered to its stages", async () => {
    page([maktaba(), thumbs()]);
    open();
    const sub = await card(/Subtitles/);
    const link = within(sub).getByTestId("card-problems");
    expect(link).toHaveTextContent("2 problems");
    expect(link).toHaveAttribute("href", "/queue/failures?stage=subtitle_gen");
    // a plugin with no problem says so and links nowhere
    const th = await card(/Thumbnails/);
    expect(th).toHaveTextContent(EN["plugins.errors.none"]);
    expect(within(th).queryByTestId("card-problems")).toBeNull();
  });

  it("T07: a Maktaba plugin's page opens at its own address, with every section that applies", async () => {
    page([maktaba(), thumbs()]);
    open("/settings/plugins/tv.maktaba.subtitles");
    expect(await screen.findByRole("heading", { level: 2, name: /Subtitles/ })).toBeInTheDocument();
    expect(screen.getByTestId("plugin-source")).toHaveTextContent(
      EN["plugins.source.long.release"]
    );
    // its stages, each a link to its queue
    const stages = screen.getByTestId("plugin-stages");
    expect(
      within(stages).getByRole("link", { name: EN["queue.stage.subtitle_gen.title"] })
    ).toHaveAttribute("href", "/queue/subtitle_gen");
    expect(stages).toHaveTextContent(EN["plugins.group.light"]);
    // what it can do in Maktaba's words, with nothing to grant
    expect(screen.getByTestId("plugin-capabilities")).toHaveTextContent(
      EN["fp.trusted.settings.read"]
    );
    expect(screen.queryByRole("button", { name: EN["plugins.permissions.save"] })).toBeNull();
    // status on each machine
    expect(screen.getByTestId("plugin-machines")).toHaveTextContent("home");
    // errors: failed jobs (its stages) and logs (its component)
    expect(screen.getByTestId("plugin-problems")).toHaveTextContent("2 problems");
    expect(screen.getByTestId("plugin-failures-link")).toHaveAttribute(
      "href",
      "/queue/failures?stage=subtitle_gen"
    );
    expect(screen.getByTestId("plugin-error-logs-link")).toHaveAttribute(
      "href",
      "/admin/logs?components=subtitles&level=error"
    );
    // settings: where they live today
    const links = screen.getByTestId("plugin-settings-links");
    expect(within(links).getByRole("link", { name: EN["rules.repair.title"] })).toHaveAttribute(
      "href",
      "/settings/rules#repair"
    );
    expect(screen.getByText("transcription.workers")).toBeInTheDocument();
    // versions: with Maktaba, a link to Updates; nothing to roll back or upload
    const versions = screen.getByTestId("versions-with-release");
    expect(within(versions).getByRole("link")).toHaveAttribute("href", "/admin/updates");
    expect(screen.queryByText(EN["plugins.versions.update_file"])).toBeNull();
    // what does not apply is absent: no writes, no remove buttons, and why in words
    expect(screen.queryByRole("heading", { name: EN["plugins.section.writes"] })).toBeNull();
    expect(screen.queryByRole("button", { name: EN["plugins.action.remove"] })).toBeNull();
    expect(screen.queryByRole("button", { name: EN["plugins.action.purge"] })).toBeNull();
    const why = screen.getByTestId("remove-with-release");
    expect(why).toHaveTextContent(EN["plugins.remove.with_release"]);
    expect(within(why).getByRole("link")).toHaveAttribute("href", "/settings/components#subtitles");
    // the same switch as its card
    expect(screen.getByRole("switch", { name: /Subtitles/ })).toBeChecked();
    // a link back to the list
    expect(screen.getByRole("link", { name: EN["plugins.page.back"] })).toHaveAttribute(
      "href",
      "/settings/plugins"
    );
  });

  it("T07: Back returns from a plugin's page to the list, and to the page again", async () => {
    page([maktaba()]);
    open();
    const sub = await card(/Subtitles/);
    await userEvent.click(within(sub).getByRole("link", { name: EN["plugins.action.open"] }));
    expect(await screen.findByTestId("plugin-stages")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "go back" }));
    expect(await card(/Subtitles/)).toBeInTheDocument();
    expect(screen.queryByTestId("plugin-stages")).toBeNull();
  });

  it("T07: an id that is no plugin (core included) says so, with the way back", async () => {
    page([maktaba()]);
    get.mockImplementation(async (path: string) => {
      if (path === "/api/plugins/tv.maktaba.core-media") throw problem(404, "plugin_not_installed");
      if (path === "/api/plugins") return { plugins: [], policy: { allow_unreviewed: false } };
      throw new Error("unexpected " + path);
    });
    open("/settings/plugins/tv.maktaba.core-media");
    expect(await screen.findByRole("alert")).toHaveTextContent(
      EN["plugins.error.plugin_not_installed"]
    );
    expect(screen.getByRole("link", { name: EN["plugins.page.back"] })).toBeInTheDocument();
  });

  it("T08: a shared runner group shows the group's memory and who shares it, not a split", async () => {
    page([maktaba(), thumbs()]);
    open("/settings/plugins/tv.maktaba.subtitles");
    const table = await screen.findByTestId("resources-processes");
    expect(table).toHaveTextContent("plugin-runner/light");
    expect(table).toHaveTextContent("310 MB");
    expect(table).toHaveTextContent(/Thumbnails.* and Maktaba itself/);
    expect(screen.getByTestId("resources-shared")).toHaveTextContent(
      EN["plugins.resources.shared_note"]
    );
    expect(screen.getByText(EN["plugins.resources.cpu_note"])).toBeInTheDocument();
    // its jobs: running, waiting and the usual time
    const section = screen.getByRole("region", { name: EN["plugins.section.resources"] });
    expect(section).toHaveTextContent("3 s");
    // no quota bar for a plugin with no store of its own
    expect(screen.queryByTestId("resources-quota")).toBeNull();
  });

  it("T08: memory the supervisor reports only for the whole part is said so", async () => {
    page([maktaba()], {
      detail: maktabaDetail({
        resources: {
          processes: [{ ...light, scope: "component", rss_mb: 900, shared_with: [] }],
          shared: true,
          data_rows: 0,
          data_bytes: 0,
          data_mb: 0,
          memory_mb: 0,
        },
      }),
    });
    open("/settings/plugins/tv.maktaba.subtitles");
    expect(await screen.findByTestId("resources-processes")).toHaveTextContent(
      "900 MB, with the other programs of the same part"
    );
  });

  it("T08: a third-party plugin's page has its quota bar, the host it shares, and every switch", async () => {
    page([third()]);
    open("/settings/plugins/dev.example.tagger");
    const quota = await screen.findByTestId("resources-quota");
    expect(quota).toHaveTextContent("12 entries, 5 MB of 50 MB");
    expect(within(quota).getByRole("progressbar")).toHaveAttribute("aria-valuenow", "10");
    expect(screen.getByTestId("resources-processes")).toHaveTextContent("dev.example.other");
    expect(screen.getByText("up to 90 MB used lately, of 256 MB allowed")).toBeInTheDocument();
    expect(screen.getByTestId("plugin-source")).toHaveTextContent(EN["plugins.source.long.file"]);
    expect(screen.getByTestId("plugin-error-logs-link")).toHaveAttribute(
      "href",
      "/admin/logs?services=plugin.dev.example.tagger&level=error"
    );
    expect(screen.getByTestId("plugin-failures-link")).toHaveAttribute(
      "href",
      "/queue/failures?stage=p_1_tag"
    );
    expect(screen.getByRole("button", { name: EN["plugins.action.remove"] })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: EN["plugins.section.writes"] })).toBeInTheDocument();
    expect(screen.queryByTestId("remove-with-release")).toBeNull();
  });

  it("T09: how to install is on the page, with what unreviewed means", async () => {
    page([maktaba()]);
    open();
    const how = await screen.findByRole("region", { name: EN["plugins.how.title"] });
    expect(how).toHaveTextContent(EN["plugins.how.store"]);
    expect(how).toHaveTextContent(EN["plugins.how.file"]);
    expect(how).toHaveTextContent(EN["plugins.how.unreviewed"]);
    await userEvent.click(within(how).getByRole("button", { name: EN["plugins.how.file_do"] }));
    expect(await screen.findByRole("dialog")).toHaveTextContent(EN["plugins.install.title"]);
  });

  it("T09: a home with no plugin (a lite home) says how to install one", async () => {
    page([], { core: false });
    open();
    const empty = await screen.findByTestId("plugins-empty");
    expect(empty).toHaveTextContent(EN["plugins.page.none"]);
    expect(empty).toHaveTextContent(EN["plugins.page.none_how"]);
    expect(screen.getByRole("button", { name: EN["plugins.browse.button"] })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: EN["plugins.install.button"] })).toBeInTheDocument();
  });

  it("T10: Arabic: the list, the filter, the lock reason and the page, right to left", async () => {
    localStorage.setItem("mkt:locale", "ar");
    page([maktaba({ switch_locked: "licence_missing" }), thumbs()]);
    open();
    const sub = await card(/الترجمات/);
    expect(document.documentElement.dir).toBe("rtl");
    expect(within(sub).getByText(AR["plugins.publisher.maktaba_verified"])).toBeInTheDocument();
    expect(within(sub).getByTestId("switch-locked")).toHaveTextContent(
      AR["plugins.lock.licence_missing"]
    );
    expect(within(sub).getByRole("switch", { name: /الترجمات/ })).toBeDisabled();
    expect(
      within(screen.getByTestId("plugins-filter")).getByRole("button", {
        name: new RegExp("^" + AR["plugins.filter.maktaba"]),
      })
    ).toBeInTheDocument();
    expect(screen.getByRole("region", { name: AR["plugins.how.title"] })).toHaveTextContent(
      AR["plugins.how.unreviewed"]
    );
    await userEvent.click(within(sub).getByRole("link", { name: AR["plugins.action.open"] }));
    expect(await screen.findByTestId("resources-shared")).toHaveTextContent(
      AR["plugins.resources.shared_note"]
    );
    expect(screen.getByTestId("remove-with-release")).toHaveTextContent(
      AR["plugins.remove.with_release"]
    );
    expect(screen.getByTestId("plugin-problems")).toHaveTextContent(/مشكلات/);
  });

  it("says nothing of plugins when the home has none", async () => {
    routes(detail(), []);
    open();
    await screen.findByText(en["plugins.page.none"]);
    expect(screen.queryByRole("article")).toBeNull();
  });

  it("has every trusted capability in English and Arabic", () => {
    const keys = Object.keys(en).filter((k) => k.startsWith("fp.trusted."));
    expect(keys.length).toBeGreaterThanOrEqual(12);
    for (const k of keys) {
      expect((ar as Record<string, string>)[k], k).toBeTruthy();
      expect((ar as Record<string, string>)[k], k).not.toBe((en as Record<string, string>)[k]);
    }
  });
});
