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
    expect(screen.queryByRole("button", { name: en["plugins.grants.save"] ?? "Save" })).toBeNull();
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
