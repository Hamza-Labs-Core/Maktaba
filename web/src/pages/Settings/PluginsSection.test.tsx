// Story 54.13 T01-T05 and T07: Settings → Plugins against a mocked API: the cards (a plugin
// its breaker held says so, with Start), Arabic capability words and RTL, a permissions
// change's PUT body, What it wrote's revert body, a viewer's not-allowed page; and 54.10's
// typed name and the policy's password.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { screen, waitFor } from "@testing-library/react";
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

const open = () => renderPage(<PluginsSection />, { path: "/settings/plugins" });

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
    await user.click(await screen.findByRole("button", { name: EN["plugins.action.open"] }));
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
    await user.click(await screen.findByRole("button", { name: AR["plugins.action.open"] }));
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
    await user.click(await screen.findByRole("button", { name: EN["plugins.action.open"] }));
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
    await user.click(await screen.findByRole("button", { name: EN["plugins.action.open"] }));
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
      if (path === "/api/plugins/dev.example.tagger/downstream") return { stages };
      return base(path);
    });
  }

  it("54.41 T03, T05: the flow, this plugin highlighted, the missing member named", async () => {
    flowRoutes();
    open();
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: EN["plugins.action.open"] }));
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
    await user.click(await screen.findByRole("button", { name: AR["plugins.action.open"] }));
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
    await user.click(await screen.findByRole("button", { name: EN["plugins.action.open"] }));
    await user.click(await screen.findByRole("button", { name: EN["plugins.action.disable"] }));
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
    await user.click(await screen.findByRole("button", { name: EN["plugins.action.open"] }));
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

describe("Settings → Plugins: built into Maktaba (Epic 54, story 54.22 §4)", () => {
  const builtIn = {
    id: "tv.maktaba.classify",
    name: "Classify",
    name_ar: "التصنيف",
    summary: "Groups and titles the library.",
    summary_ar: "يجمع المكتبة ويسمّيها.",
    component: "classify",
    stages: ["promo_links"],
    capabilities: [
      { key: "fp.trusted.db.statements", param: "promo_links" },
      { key: "fp.trusted.settings.read", param: "groups" },
    ],
  };

  it("lists Maktaba's own plugins with what each can do, and no grants dialog", async () => {
    get.mockImplementation(async (path: string) => {
      if (path === "/api/plugins")
        return { plugins: [], policy: { allow_unreviewed: false }, built_in: [builtIn] };
      throw new Error("unexpected " + path);
    });
    open();
    const section = await screen.findByRole("region", { name: "Built into Maktaba" });
    expect(section).toHaveTextContent("Classify");
    expect(section).toHaveTextContent("Groups and titles the library.");
    await userEvent.click(screen.getByText("What it can do"));
    expect(section).toHaveTextContent(en["fp.trusted.db.statements"]);
    expect(section).toHaveTextContent(en["fp.trusted.settings.read"]);
    expect(section).toHaveTextContent("Turned on and off with");
    // turning its component on is the decision (54.22 §4): no grants dialog to open
    expect(section.querySelector("button")).toBeNull();
  });

  it("says nothing of built-in plugins when the release has none", async () => {
    routes(detail(), []);
    open();
    await screen.findByText(en["plugins.page.none"]);
    expect(screen.queryByRole("region", { name: "Built into Maktaba" })).toBeNull();
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
