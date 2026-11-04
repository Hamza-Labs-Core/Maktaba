// Story 51.10 — Settings → Components, cases T01–T12 of the story, and the parts other
// stories add to the page: Restart (51.08), the settings-reset dialog (51.13), the drawer's
// health (51.14), offline files (51.16) and the licence lock (51.19).
// The fake API is built from the real catalog (shared/components/testdata/catalog.dump.json),
// in the shape of the API's View: `settings` is the catalog's list as it is.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { get, post, put, patch, del, upload } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  put: vi.fn(),
  patch: vi.fn(),
  del: vi.fn(),
  upload: vi.fn(),
}));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return {
    ...actual,
    uploadWithProgress: upload,
    api: { ...actual.api, get, post, put, patch, delete: del },
  };
});

import en from "../../i18n/en.json";
import ar from "../../i18n/ar.json";
import { ApiError } from "../../lib/api";
import type { ComponentDetail, ComponentView, ListView, Preview } from "../../lib/componentsApi";
import { adminUser, memberUser, renderPage } from "../../test/pageHarness";
import { axeViolations } from "../../test/axe";
import { ComponentsSection } from "./ComponentsSection";
import { GROUP_ORDER } from "./componentWords";

// the catalog is outside src/: read it at run time, as help.coverage.test.ts does
const FS = "node:fs";
const { readFileSync } = (await import(/* @vite-ignore */ FS)) as {
  readFileSync: (path: string, encoding: "utf8") => string;
};
interface DumpEntry {
  id: string;
  kind: string;
  group: string;
  title_key: string;
  desc_key: string;
  switchable: boolean;
  placement: string;
  licence: string;
  requires: string[][];
  wants: string[];
  conflicts: string[];
  bundles: string[];
  features: string[];
  settings: string[];
}
const DUMP = JSON.parse(
  readFileSync("../shared/components/testdata/catalog.dump.json", "utf8")
) as { components: DumpEntry[] };

const EN = en as Record<string, string>;
const AR = ar as Record<string, string>;

function view(c: DumpEntry): ComponentView {
  const asset = c.kind === "asset";
  const phase = asset ? "off" : "running";
  return {
    id: c.id,
    kind: c.kind,
    group: c.group,
    title_key: c.title_key,
    desc_key: c.desc_key,
    switchable: c.switchable,
    placement: c.placement,
    licence: c.licence,
    licensed: true,
    requires: c.requires,
    wants: c.wants,
    conflicts: c.conflicts,
    dependents: [],
    bundles: c.bundles,
    features: c.features,
    want: "enabled",
    hold: null,
    generation: 3,
    has_row: true,
    phase,
    expected: { id: c.id, from: "", to: phase },
    machines: [{ machine: "home", phase, observed_generation: 3 }],
    required_by: [],
    install_size_mb: asset ? 1600 : 0,
    // the API sends the catalog's list as it is (View.settings)
    settings: c.settings,
  };
}

let list: ListView;
function setRow(id: string, over: Partial<ComponentView>) {
  list = {
    ...list,
    revision: list.revision + 1,
    components: list.components.map((c) => (c.id === id ? { ...c, ...over } : c)),
  };
}
const row = (id: string) => {
  const el = document.querySelector<HTMLElement>(`[data-component="${id}"]`);
  if (!el) throw new Error(`no row ${id}`);
  return el;
};
const NOTHING: Preview = {
  effects: [],
  conflicts: [],
  refused: [],
  queued_jobs_parked: 0,
  drain_s: 0,
};
const detailOf = (id: string): ComponentDetail => ({
  ...list.components.find((c) => c.id === id)!,
  audit: [],
});

let previewAnswer: (body: unknown) => Preview | Promise<Preview>;

beforeEach(() => {
  list = {
    revision: 40,
    bundle: { base: "full", set: "custom", plus: [], minus: ["youtube"] },
    components: DUMP.components.map(view),
  };
  previewAnswer = () => NOTHING;
  get.mockImplementation((url: string) => {
    if (url === "/api/components") return Promise.resolve(structuredClone(list));
    const m = /^\/api\/components\/([^/]+)$/.exec(url);
    if (m) return Promise.resolve(detailOf(decodeURIComponent(m[1]!)));
    return Promise.reject(new Error(`unmocked GET ${url}`));
  });
  post.mockImplementation((url: string, body?: unknown) => {
    if (url === "/api/components/preview") return Promise.resolve(previewAnswer(body));
    const m = /^\/api\/components\/([^/]+)\/(stop|start|install)$/.exec(url);
    if (m) {
      const id = decodeURIComponent(m[1]!);
      if (m[2] === "stop")
        setRow(id, {
          hold: { by: "admin", at: "2026-11-03T10:00:00Z" },
          phase: "stopped",
          reason: "held",
        });
      if (m[2] === "start") setRow(id, { hold: null, phase: "running", reason: "" });
      return Promise.resolve(detailOf(id));
    }
    return Promise.reject(new Error(`unmocked POST ${url}`));
  });
  put.mockImplementation(
    (url: string, body: { want: ComponentView["want"]; generation: number }) => {
      const id = decodeURIComponent(/^\/api\/components\/([^/]+)$/.exec(url)![1]!);
      const cur = list.components.find((c) => c.id === id)!;
      setRow(id, {
        want: body.want,
        generation: cur.generation + 1,
        phase: body.want === "enabled" ? "running" : "off",
      });
      return Promise.resolve(detailOf(id));
    }
  );
});

afterEach(() => {
  vi.useRealTimers();
  vi.clearAllMocks();
  try {
    localStorage.removeItem("mkt:locale");
  } catch {
    // ignored
  }
  document.documentElement.dir = "ltr";
  document.documentElement.lang = "en";
});

function open(user = adminUser) {
  return renderPage(<ComponentsSection />, { path: "/settings/components", user });
}
async function loaded() {
  await screen.findByRole("heading", { name: EN["components.page.title"] });
  await waitFor(() => expect(document.querySelector("[data-component]")).not.toBeNull());
}
const puts = () => put.mock.calls;

describe("Components page (story 51.10)", () => {
  it("T01: every component in its group, groups in order, chips in words, no switch on core", async () => {
    setRow("classify", { phase: "waiting", reason: "dependency_off:llm" });
    open();
    await loaded();
    const groups = [...document.querySelectorAll<HTMLElement>("[data-group]")].map(
      (g) => g.dataset.group
    );
    const expected = GROUP_ORDER.filter((g) => DUMP.components.some((c) => c.group === g));
    expect(groups).toEqual(expected);
    expect(groups.at(-1)).toBe("core");
    expect(screen.getByRole("heading", { name: "Always on" })).toBeInTheDocument();
    // every catalog component has its row, in its group
    for (const c of DUMP.components) {
      const r = row(c.id);
      expect(r.closest("[data-group]")?.getAttribute("data-group")).toBe(c.group);
      expect(within(r).getByText(EN[c.title_key]!)).toBeInTheDocument();
    }
    // the chip says the phase and why, never the code
    const chip = within(row("classify")).getByTestId("comp-chip");
    expect(chip).toHaveTextContent("Waiting · Needs Language model, which is off.");
    expect(chip.textContent).not.toContain("dependency_off");
    expect(within(row("transcription")).getByTestId("comp-chip")).toHaveTextContent("Running");
    expect(within(row("model.whisper-turbo")).getByTestId("comp-chip")).toHaveTextContent(
      /Installed · 1\.6 GB/
    );
    // core rows have no switch and no stop
    for (const c of DUMP.components.filter((c) => c.id.startsWith("core."))) {
      expect(within(row(c.id)).queryByRole("switch")).toBeNull();
      expect(within(row(c.id)).queryByRole("button", { name: /^Stop/ })).toBeNull();
    }
    expect(
      within(row("transcription")).getByRole("switch", { name: "Transcription" })
    ).toBeChecked();
    expect(screen.getByTestId("comp-bundle")).toHaveTextContent("Based on Full, minus YouTube");
  });

  it("T02: turning llm off shows what else stops and the parked jobs first; Cancel changes nothing", async () => {
    previewAnswer = () => ({
      effects: [
        { id: "llm", from: "running", to: "off" },
        { id: "classify", from: "running", to: "waiting", reason: "dependency_off:llm" },
      ],
      conflicts: [],
      refused: [],
      queued_jobs_parked: 1840,
      drain_s: 30,
    });
    const user = userEvent.setup();
    open();
    await loaded();
    await user.click(within(row("llm")).getByRole("switch", { name: "Language model" }));
    const dialog = await screen.findByRole("dialog", { name: "Turn off Language model?" });
    expect(dialog).toHaveTextContent("This also stops: Organising.");
    expect(dialog).toHaveTextContent(
      "1,840 queued jobs will wait until you turn it back on. Nothing is lost."
    );
    expect(dialog).toHaveTextContent("Jobs running now get up to 30 seconds to finish.");
    expect(post).toHaveBeenCalledWith("/api/components/preview", {
      changes: [{ id: "llm", want: "installed", generation: 3 }],
      with_conflicts: false,
    });
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(puts()).toHaveLength(0);
  });

  it("T02b: confirming writes want installed with the generation it read, and the row follows", async () => {
    previewAnswer = () => ({
      effects: [{ id: "classify", from: "running", to: "waiting" }],
      conflicts: [],
      refused: [],
      queued_jobs_parked: 2,
      drain_s: 30,
    });
    const user = userEvent.setup();
    open();
    await loaded();
    await user.click(within(row("llm")).getByRole("switch", { name: "Language model" }));
    const dialog = await screen.findByRole("dialog");
    await user.click(within(dialog).getByRole("button", { name: "Turn off" }));
    await waitFor(() =>
      expect(put).toHaveBeenCalledWith("/api/components/llm", { want: "installed", generation: 3 })
    );
    await waitFor(() =>
      expect(within(row("llm")).getByRole("switch", { name: "Language model" })).not.toBeChecked()
    );
    expect(within(row("llm")).getByTestId("comp-chip")).toHaveTextContent("Off");
  });

  it("T03: a change that touches only its row applies at once, with an undo", async () => {
    const user = userEvent.setup();
    open();
    await loaded();
    await user.click(within(row("analytics")).getByRole("switch", { name: "Watch history" }));
    await waitFor(() =>
      expect(put).toHaveBeenCalledWith("/api/components/analytics", {
        want: "installed",
        generation: 3,
      })
    );
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(await screen.findByText("Watch history is turned off.")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Undo" }));
    await waitFor(() =>
      expect(put).toHaveBeenLastCalledWith("/api/components/analytics", {
        want: "enabled",
        generation: 4,
      })
    );
  });

  it("T04: removing what others need shows why and who, with links and no confirm button", async () => {
    previewAnswer = () => ({
      ...NOTHING,
      refused: [
        {
          id: "runtime.ml",
          code: "component_required_by",
          detail: "",
          required_by: ["transcription", "subtitles"],
        },
      ],
    });
    const user = userEvent.setup();
    open();
    await loaded();
    await user.click(
      within(row("runtime.ml")).getByRole("button", {
        name: "Details of Machine learning runtime",
      })
    );
    const drawer = await screen.findByRole("dialog", { name: "Machine learning runtime" });
    await user.click(within(drawer).getByRole("button", { name: "Remove" }));
    const dialog = await screen.findByRole("dialog", { name: "Remove Machine learning runtime?" });
    expect(within(dialog).getByTestId("comp-refused")).toHaveTextContent(
      "Transcription and Subtitles need it. Turn them off first."
    );
    expect(within(dialog).getByRole("button", { name: "Transcription" })).toBeInTheDocument();
    expect(within(dialog).getByRole("button", { name: "Subtitles" })).toBeInTheDocument();
    expect(within(dialog).queryByRole("button", { name: "Remove" })).toBeNull();
    expect(within(dialog).getAllByRole("button", { name: "Close" }).length).toBeGreaterThan(0);
    // a blocker's link closes the dialog and points at its row
    await user.click(within(dialog).getByRole("button", { name: "Transcription" }));
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(row("transcription")).toHaveClass("is-flash");
    expect(puts()).toHaveLength(0);
  });

  it("T05: turning on one engine names the conflict it turns off and sends with_conflicts", async () => {
    setRow("livetv.inprocess", { want: "installed", phase: "off" });
    previewAnswer = () => ({
      effects: [
        { id: "livetv.inprocess", from: "off", to: "running" },
        { id: "livetv.slots", from: "running", to: "off" },
      ],
      conflicts: ["livetv.slots"],
      refused: [],
      queued_jobs_parked: 0,
      drain_s: 5,
    });
    const user = userEvent.setup();
    open();
    await loaded();
    await user.click(
      within(row("livetv.inprocess")).getByRole("switch", { name: "Live TV in one process" })
    );
    const dialog = await screen.findByRole("dialog", { name: "Turn on Live TV in one process?" });
    expect(dialog).toHaveTextContent("Turning on Live TV in one process turns off Live TV slots.");
    expect(post).toHaveBeenCalledWith("/api/components/preview", {
      changes: [{ id: "livetv.inprocess", want: "enabled", generation: 3 }],
      with_conflicts: true,
    });
    await user.click(within(dialog).getByRole("button", { name: "Turn on" }));
    await waitFor(() =>
      expect(put).toHaveBeenCalledWith("/api/components/livetv.inprocess", {
        want: "enabled",
        generation: 3,
        with_conflicts: true,
      })
    );
  });

  it("T06: Stop sends no body and the row reads 'Stopped until you turn it on' with Start; no time picker", async () => {
    const user = userEvent.setup();
    const { unmount } = open();
    await loaded();
    const stop = within(row("transcription")).getByRole("button", {
      name: "Stop Transcription until I turn it on",
    });
    expect(stop).toHaveTextContent("Stop");
    await user.click(stop);
    await waitFor(() => expect(post).toHaveBeenCalledWith("/api/components/transcription/stop"));
    const stopCall = post.mock.calls.find(([u]) => u === "/api/components/transcription/stop")!;
    expect(stopCall).toHaveLength(1); // no body at all
    expect(post).toHaveBeenCalledWith("/api/components/preview", {
      changes: [{ id: "transcription", hold: true, generation: 3 }],
      with_conflicts: false,
    });
    await waitFor(() =>
      expect(within(row("transcription")).getByTestId("comp-chip")).toHaveTextContent(
        "Stopped until you turn it on"
      )
    );
    expect(
      within(row("transcription")).getByRole("button", { name: "Start Transcription" })
    ).toBeInTheDocument();
    // a hold has one choice: nothing picks a time
    expect(
      document.querySelector('input[type="time"], input[type="datetime-local"], select')
    ).toBeNull();
    unmount();

    // reloading the page (the hold is the server's): it still reads stopped, in Arabic too
    localStorage.setItem("mkt:locale", "ar");
    open();
    await screen.findByRole("heading", { name: AR["components.page.title"] });
    await waitFor(() =>
      expect(within(row("transcription")).getByTestId("comp-chip")).toHaveTextContent(
        "متوقف حتى تشغّله"
      )
    );
    expect(
      within(row("transcription")).getByRole("button", { name: "تشغيل التفريغ النصي" })
    ).toBeInTheDocument();
  });

  it("T07: a stale generation reloads the row and says someone else changed it", async () => {
    put.mockImplementationOnce(() => {
      setRow("analytics", { want: "installed", phase: "off", generation: 9 });
      return Promise.reject(
        new ApiError({
          type: "https://maktaba.tv/problems/stale-generation",
          title: "stale generation",
          status: 409,
          code: "stale_generation",
        })
      );
    });
    const user = userEvent.setup();
    open();
    await loaded();
    await user.click(within(row("analytics")).getByRole("switch", { name: "Watch history" }));
    expect(
      await screen.findByText("Someone else changed this. Here is what it is now.")
    ).toBeInTheDocument();
    await waitFor(() =>
      expect(
        within(row("analytics")).getByRole("switch", { name: "Watch history" })
      ).not.toBeChecked()
    );
    expect(row("analytics")).toHaveClass("is-flash");
  });

  it("T08: in Arabic the page is right to left and has no English left", async () => {
    localStorage.setItem("mkt:locale", "ar");
    setRow("classify", { phase: "waiting", reason: "dependency_off:llm" });
    open();
    await screen.findByRole("heading", { name: AR["components.page.title"] });
    await waitFor(() => expect(document.querySelector("[data-component]")).not.toBeNull());
    expect(document.documentElement.dir).toBe("rtl");
    // Latin words allowed are only the product names the Arabic titles keep (Plex, FFmpeg...)
    const allowed = new Set(
      Object.entries(AR)
        .filter(([k]) => k.startsWith("components."))
        .flatMap(([, v]) => v.match(/[A-Za-z][A-Za-z0-9._-]*/g) ?? [])
    );
    allowed.add("YouTube");
    const panel = document.querySelector(".mkt-comp")!;
    const latin = (panel.textContent ?? "").match(/[A-Za-z][A-Za-z0-9._-]*/g) ?? [];
    expect(latin.filter((w) => !allowed.has(w))).toEqual([]);
    // every word the page adds has Arabic, and not a copy of the English
    const keys = Object.keys(EN).filter(
      (k) =>
        k.startsWith("components.page.") ||
        k.startsWith("components.group.") ||
        k.startsWith("components.confirm.") ||
        k.startsWith("components.refused.") ||
        k.startsWith("components.drawer.") ||
        k.startsWith("components.bundle.") ||
        k.startsWith("components.done.")
    );
    expect(keys.length).toBeGreaterThan(50);
    for (const k of keys) {
      expect(AR[k], k).toBeTruthy();
      if (k !== "components.bundle.sep" && !/\{[a-z]+\}$/.test(EN[k]!)) {
        expect(AR[k], k).not.toBe(EN[k]);
      }
    }
  });

  it("T09: axe finds no violation, in English and in Arabic", async () => {
    setRow("classify", { phase: "waiting", reason: "dependency_off:llm" });
    for (const locale of ["en", "ar"]) {
      localStorage.setItem("mkt:locale", locale);
      const { container, unmount } = open();
      await waitFor(() => expect(document.querySelector("[data-component]")).not.toBeNull());
      expect(await axeViolations(container)).toEqual([]);
      unmount();
    }
  });

  it("T10: a viewer gets the admins-only panel and nothing is read", async () => {
    open(memberUser);
    expect(await screen.findByText(EN["common.forbidden.title"]!)).toBeInTheDocument();
    expect(get).not.toHaveBeenCalled();
    expect(screen.queryByRole("switch")).toBeNull();
  });

  it("T11: a component that owns a rules section links to its form (the catalog's list as the API sends it)", async () => {
    // no field is faked: transcription's settings are the catalog's
    expect(list.components.find((c) => c.id === "transcription")!.settings).toEqual([
      "rules.transcription",
      "toml:transcription.workers",
    ]);
    open();
    await loaded();
    expect(
      within(row("transcription")).getByRole("link", { name: "Settings for Transcription" })
    ).toHaveAttribute("href", "/settings/rules#transcription");
    // a component with server.toml keys only, or with no settings, has no link
    expect(within(row("llm")).queryByRole("link", { name: /^Settings for/ })).toBeNull();
    expect(
      within(row("library.convert")).queryByRole("link", { name: /^Settings for/ })
    ).toBeNull();
  });

  it("T11: the drawer lists the server.toml keys and says when the database overrides one", async () => {
    get.mockImplementation((url: string) => {
      if (url === "/api/components") return Promise.resolve(structuredClone(list));
      if (url === "/api/components/llm")
        return Promise.resolve({
          ...detailOf("llm"),
          machines: [
            {
              machine: "home",
              phase: "running",
              observed_generation: 3,
              parts: [
                {
                  machine: "home",
                  id: "llm",
                  part: "config",
                  phase: "running",
                  conditions: [
                    {
                      type: "Configured",
                      status: "false",
                      reason: "toml_superseded",
                      params: {
                        key: "llm.enabled",
                        value: "false",
                        line: 12,
                        file_says: "installed",
                      },
                      changed_at: "2026-11-03T09:00:00Z",
                      generation: 3,
                    },
                  ],
                  observed_generation: 3,
                  since: "2026-11-03T09:00:00Z",
                  updated_at: "2026-11-03T09:00:00Z",
                },
              ],
            },
          ],
        } satisfies ComponentDetail);
      return Promise.reject(new Error(`unmocked GET ${url}`));
    });
    const user = userEvent.setup();
    open();
    await loaded();
    await user.click(within(row("llm")).getByRole("button", { name: "Details of Language model" }));
    const drawer = await screen.findByRole("dialog", { name: "Language model" });
    expect(drawer).toHaveTextContent("In server.tomlllm.enabled, llm.models");
    expect(
      await within(drawer).findByText(
        /llm\.enabled: server\.toml says false; the setting here wins\./
      )
    ).toBeInTheDocument();
  });

  it("T12: it reads the list every 5 s while visible, and not at all while the tab is hidden", async () => {
    vi.useFakeTimers({ toFake: ["setInterval", "clearInterval"] });
    let state: DocumentVisibilityState = "visible";
    const spy = vi.spyOn(document, "visibilityState", "get").mockImplementation(() => state);
    try {
      open();
      await loaded();
      const reads = () => get.mock.calls.filter(([u]) => u === "/api/components").length;
      expect(reads()).toBe(1);
      await act(async () => {
        vi.advanceTimersByTime(5_000);
      });
      expect(reads()).toBe(2);
      state = "hidden";
      act(() => {
        document.dispatchEvent(new Event("visibilitychange"));
      });
      await act(async () => {
        vi.advanceTimersByTime(30_000);
      });
      expect(reads()).toBe(2);
      state = "visible";
      await act(async () => {
        document.dispatchEvent(new Event("visibilitychange"));
      });
      expect(reads()).toBe(3);
    } finally {
      spy.mockRestore();
    }
  });

  it("the API down: the last answer stays, greyed, with the can't-reach line, and no switch works", async () => {
    vi.useFakeTimers({ toFake: ["setInterval", "clearInterval"] });
    open();
    await loaded();
    get.mockImplementation(() => Promise.reject(new TypeError("Failed to fetch")));
    await act(async () => {
      vi.advanceTimersByTime(5_000);
    });
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent(EN["error.kind.unreachable"]!);
    expect(within(row("llm")).getByRole("switch", { name: "Language model" })).toBeDisabled();
    expect(document.querySelector(".mkt-comp")).toHaveClass("is-offline");
  });

  it("a phase that differs per machine says how many and links to the Machines page", async () => {
    setRow("transcription", {
      machines: [
        { machine: "home", phase: "running", observed_generation: 3 },
        { machine: "node-a", phase: "running", observed_generation: 3 },
        {
          machine: "node-b",
          phase: "waiting",
          reason: "not_enough_memory",
          observed_generation: 3,
        },
      ],
    });
    setRow("llm", { want: "installed", phase: "off" });
    setRow("subtitles", {
      machines: [
        { machine: "home", phase: "running", observed_generation: 3 },
        { machine: "node-a", phase: "running", observed_generation: 3 },
      ],
    });
    open();
    await loaded();
    expect(within(row("transcription")).getByTestId("comp-chip")).toHaveTextContent(
      "Running · 2 of 3 machines"
    );
    expect(
      within(row("transcription")).getByRole("link", { name: "Machines for Transcription" })
    ).toHaveAttribute("href", "/admin/machines#components-node-b");
    expect(within(row("subtitles")).getByTestId("comp-chip")).toHaveTextContent(
      "Running · 2 machines"
    );
    expect(within(row("subtitles")).queryByRole("link", { name: /^Machines for/ })).toBeNull();
    // a home with no other machine never links there, whatever its rows say
    expect(within(row("llm")).queryByRole("link", { name: /^Machines for/ })).toBeNull();
  });

  it("the drawer shows each machine's conditions and the last changes", async () => {
    get.mockImplementation((url: string) => {
      if (url === "/api/components") return Promise.resolve(structuredClone(list));
      if (url === "/api/components/classify")
        return Promise.resolve({
          ...detailOf("classify"),
          machines: [
            {
              machine: "home",
              phase: "waiting",
              reason: "dependency_off",
              observed_generation: 3,
              parts: [
                {
                  machine: "home",
                  id: "classify",
                  part: "pipeline",
                  phase: "waiting",
                  conditions: [
                    {
                      type: "RequirementsMet",
                      status: "false",
                      reason: "dependency_off:llm",
                      changed_at: "2026-11-03T09:00:00Z",
                      generation: 3,
                    },
                  ],
                  observed_generation: 3,
                  version: "0.9.1",
                  since: "2026-11-03T09:00:00Z",
                  updated_at: "2026-11-03T09:00:00Z",
                },
              ],
            },
          ],
          audit: [
            {
              id: 7,
              at: "2026-11-03T08:00:00Z",
              action: "component.disable",
              actor_source: "web",
              payload: { by: "maya" },
            },
          ],
        } satisfies ComponentDetail);
      return Promise.reject(new Error(`unmocked GET ${url}`));
    });
    const user = userEvent.setup();
    open();
    await loaded();
    await user.click(
      within(row("classify")).getByRole("button", { name: "Details of Organising" })
    );
    const drawer = await screen.findByRole("dialog", { name: "Organising" });
    expect(
      await within(drawer).findByText(
        /What it needs is running: no · Needs Language model, which is off\./
      )
    ).toBeInTheDocument();
    expect(drawer).toHaveTextContent("version 0.9.1");
    expect(drawer).toHaveTextContent(/Turned off · maya/);
  });

  it("the drawer names what a component waits for, in English and Arabic", async () => {
    get.mockImplementation((url: string) => {
      if (url === "/api/components") return Promise.resolve(structuredClone(list));
      if (url === "/api/components/llm")
        return Promise.resolve({
          ...detailOf("llm"),
          machines: [
            {
              machine: "home",
              phase: "waiting",
              observed_generation: 2,
              parts: [
                {
                  machine: "home",
                  id: "llm",
                  part: "supervisor",
                  phase: "waiting",
                  // the supervisor says what it waits on only in its params
                  conditions: [
                    {
                      type: "RequirementsMet",
                      status: "unknown",
                      params: { component: "model.llm.qwen2_5-7b" },
                      changed_at: "2026-11-03T09:00:00Z",
                      generation: 2,
                    },
                  ],
                  observed_generation: 2,
                  since: "2026-11-03T09:00:00Z",
                  updated_at: "2026-11-03T09:00:00Z",
                },
                {
                  machine: "home",
                  id: "llm",
                  part: "pipeline",
                  phase: "waiting",
                  // the API's rule: its files are not on this machine
                  conditions: [
                    {
                      type: "RequirementsMet",
                      status: "false",
                      reason: "requirement_missing:runtime.ml",
                      params: { component: "runtime.ml" },
                      changed_at: "2026-11-03T09:00:00Z",
                      generation: 2,
                    },
                  ],
                  observed_generation: 2,
                  since: "2026-11-03T09:00:00Z",
                  updated_at: "2026-11-03T09:00:00Z",
                },
              ],
            },
          ],
        } satisfies ComponentDetail);
      return Promise.reject(new Error(`unmocked GET ${url}`));
    });
    for (const [locale, details, title, waiting, missing] of [
      [
        "en",
        "Details of Language model",
        "Language model",
        /What it needs is running: not known yet · waiting for Qwen 2\.5 \(7B\)/,
        /What it needs is running: no · Waits for Machine learning runtime, which is not on this machine yet\./,
      ],
      [
        "ar",
        "تفاصيل النموذج اللغوي",
        "النموذج اللغوي",
        /بانتظار نموذج Qwen 2\.5/,
        /ينتظر بيئة تعلّم الآلة، وهو غير موجود على هذا الجهاز بعد\./,
      ],
    ] as const) {
      localStorage.setItem("mkt:locale", locale);
      const user = userEvent.setup();
      const { unmount } = open();
      await screen.findByRole("heading", {
        name: (locale === "ar" ? AR : EN)["components.page.title"],
      });
      await waitFor(() => expect(document.querySelector("[data-component]")).not.toBeNull());
      await user.click(within(row("llm")).getByRole("button", { name: details }));
      const drawer = await screen.findByRole("dialog", { name: title });
      expect(await within(drawer).findByText(waiting)).toBeInTheDocument();
      expect(within(drawer).getByText(missing)).toBeInTheDocument();
      unmount();
    }
    localStorage.removeItem("mkt:locale");
  });
});

describe("Components page: a wanted asset not on this home", () => {
  const drawerOf = async (id: string, name: string) => {
    const user = userEvent.setup();
    const { unmount } = open();
    await loaded();
    await user.click(within(row(id)).getByRole("button", { name: `Details of ${name}` }));
    return { user, unmount, drawer: await screen.findByRole("dialog", { name }) };
  };
  const installs = (id: string) =>
    post.mock.calls.filter(([url]) => url === `/api/components/${id}/install`);
  const previews = () => post.mock.calls.filter(([url]) => url === "/api/components/preview");

  it("offers Install for a fresh home's search model (no desired row), with no preview", async () => {
    setRow("model.embedder", { phase: "not_installed", has_row: false });
    const { user, drawer } = await drawerOf("model.embedder", "Search model");
    expect(within(drawer).getByRole("button", { name: "Remove" })).toBeInTheDocument();
    await user.click(within(drawer).getByRole("button", { name: EN["components.row.install"]! }));
    await waitFor(() => expect(installs("model.embedder")).toHaveLength(1));
    expect(previews()).toHaveLength(0);
    expect(
      await screen.findByText(EN["components.done.install"]!.replace("{name}", "Search model"))
    ).toBeInTheDocument();
  });

  it("offers Retry after a failed install, and it asks the install route again", async () => {
    setRow("runtime.ml", {
      phase: "failed",
      reason: "download_failed",
      progress: {
        phase: "failed",
        percent: 0,
        done: 0,
        total: 0,
        busy: false,
        reason: "download_failed",
      },
    });
    const { user, drawer } = await drawerOf("runtime.ml", "Machine learning runtime");
    expect(
      within(drawer).queryByRole("button", { name: EN["components.row.install"]! })
    ).toBeNull();
    await user.click(within(drawer).getByRole("button", { name: EN["components.row.retry"]! }));
    await waitFor(() => expect(installs("runtime.ml")).toHaveLength(1));
    expect(previews()).toHaveLength(0);
    expect(
      await screen.findByText(
        EN["components.done.retry"]!.replace("{name}", "Machine learning runtime")
      )
    ).toBeInTheDocument();
  });

  it("an open drawer follows the row when an install finishes (the revision stays)", async () => {
    // an install's end changes the status, not the desired state: the list's revision stays
    const quiet = (over: Partial<ComponentView>) => {
      list = {
        ...list,
        components: list.components.map((c) => (c.id === "runtime.ml" ? { ...c, ...over } : c)),
      };
    };
    quiet({
      phase: "failed",
      reason: "download_failed",
      machines: [
        { machine: "home", phase: "failed", reason: "download_failed", observed_generation: 3 },
      ],
    });
    const user = userEvent.setup();
    renderPage(<ComponentsSection pollMs={40} />, {
      path: "/settings/components",
      user: adminUser,
    });
    await loaded();
    await user.click(
      within(row("runtime.ml")).getByRole("button", { name: "Details of Machine learning runtime" })
    );
    const drawer = await screen.findByRole("dialog", { name: "Machine learning runtime" });
    const home = () =>
      within(drawer)
        .getByText(/This home/)
        .closest("li");
    await within(drawer).findByText(/This home/);
    expect(home()).toHaveTextContent(EN["components.phase.failed"]!);
    quiet({
      phase: "off",
      reason: "",
      machines: [{ machine: "home", phase: "off", observed_generation: 3 }],
    });
    await waitFor(() =>
      expect(within(row("runtime.ml")).getByTestId("comp-chip")).toHaveTextContent(
        EN["components.page.installed"]!
      )
    );
    await waitFor(() =>
      expect(home()).toHaveTextContent(
        `${EN["components.drawer.home"]}: ${EN["components.page.installed"]}`
      )
    );
    expect(home()).not.toHaveTextContent(EN["components.phase.failed"]!);
  });

  it("offers neither when it is on disk, installing, or packs are off here", async () => {
    for (const over of [
      { phase: "off" },
      {
        phase: "installing",
        progress: { phase: "installing", percent: 5, done: 0, total: 0, busy: true },
      },
      { phase: "not_installed", reason: "packs_disabled" },
    ] satisfies Partial<ComponentView>[]) {
      setRow("runtime.ml", over);
      const { drawer, unmount } = await drawerOf("runtime.ml", "Machine learning runtime");
      expect(
        within(drawer).queryByRole("button", { name: EN["components.row.install"]! })
      ).toBeNull();
      expect(
        within(drawer).queryByRole("button", { name: EN["components.row.retry"]! })
      ).toBeNull();
      unmount();
    }
  });

  it("says Installed, not Turned off, for an asset moved to installed", async () => {
    get.mockImplementation((url: string) => {
      if (url === "/api/components") return Promise.resolve(structuredClone(list));
      if (url === "/api/components/runtime.ml")
        return Promise.resolve({
          ...detailOf("runtime.ml"),
          audit: [
            {
              id: 9,
              at: "2026-11-03T08:00:00Z",
              action: "component.disable",
              actor_source: "web",
              payload: { by: "maya" },
            },
          ],
        } satisfies ComponentDetail);
      return Promise.reject(new Error(`unmocked GET ${url}`));
    });
    const { drawer } = await drawerOf("runtime.ml", "Machine learning runtime");
    expect(await within(drawer).findByText(/Installed · maya/)).toBeInTheDocument();
    expect(drawer).not.toHaveTextContent(EN["components.audit.disable"]!);
  });

  it("has the new words in Arabic", () => {
    for (const k of [
      "components.row.retry",
      "components.done.retry",
      "components.audit.install",
      "components.audit.asset.installed",
      "components.reason.offline_verify_failed",
    ]) {
      expect(EN[k], k).toBeTruthy();
      expect(AR[k], k).toBeTruthy();
      expect(AR[k], k).not.toBe(EN[k]);
    }
    // an import's mismatch names the file, not a download
    expect(EN["components.reason.offline_verify_failed"]).toMatch(/^The file /);
  });
});

async function axeBoth(render: () => Promise<{ container: HTMLElement; unmount: () => void }>) {
  for (const locale of ["en", "ar"]) {
    localStorage.setItem("mkt:locale", locale);
    const { container, unmount } = await render();
    expect(await axeViolations(container), locale).toEqual([]);
    expect(await axeViolations(document.body), `${locale} (dialogs)`).toEqual([]);
    unmount();
  }
}

describe("Components page: Restart (story 51.08 §2)", () => {
  it("shows Restart for process and loop kinds only, and restarts with no body", async () => {
    const user = userEvent.setup();
    post.mockImplementation((url: string) => {
      if (url === "/api/components/transcription/restart") return Promise.resolve(undefined);
      return Promise.reject(new Error(`unmocked POST ${url}`));
    });
    open();
    await loaded();
    // transcription is stages+process, livetv.slots stages+process+loop, cluster a loop
    for (const id of ["transcription", "livetv.slots", "cluster", "llm"]) {
      expect(
        within(row(id)).getByRole("button", { name: new RegExp(`^Restart `) }),
        id
      ).toBeInTheDocument();
    }
    // stages only, an asset and an always-on part have none
    for (const id of ["classify", "model.whisper-turbo", "core.api"]) {
      expect(within(row(id)).queryByRole("button", { name: /^Restart / }), id).toBeNull();
    }
    await user.click(
      within(row("transcription")).getByRole("button", { name: "Restart Transcription" })
    );
    await waitFor(() => expect(post).toHaveBeenCalledWith("/api/components/transcription/restart"));
    const call = post.mock.calls.find(([u]) => u === "/api/components/transcription/restart")!;
    expect(call).toHaveLength(1); // no body
    expect(post).not.toHaveBeenCalledWith("/api/components/preview", expect.anything());
    expect(await screen.findByText("Transcription is restarting.")).toBeInTheDocument();
  });

  it("a held or off row has no Restart, and a refusal is said in words", async () => {
    setRow("transcription", {
      hold: { by: "admin", at: "2026-11-03T10:00:00Z" },
      phase: "stopped",
      reason: "held",
    });
    setRow("llm", { want: "installed", phase: "off" });
    post.mockImplementation((url: string) =>
      Promise.reject(
        new ApiError({
          type: "https://maktaba.tv/problems/not-running",
          title: "not running",
          status: 409,
          code: url.includes("cluster") ? "not_running" : "not_restartable",
        })
      )
    );
    const user = userEvent.setup();
    open();
    await loaded();
    expect(within(row("transcription")).queryByRole("button", { name: /^Restart / })).toBeNull();
    expect(within(row("llm")).queryByRole("button", { name: /^Restart / })).toBeNull();
    await user.click(within(row("cluster")).getByRole("button", { name: /^Restart / }));
    expect(
      await screen.findByText(
        /is not running, so it cannot be restarted\. Turn it on or start it first\./
      )
    ).toBeInTheDocument();
    await user.click(within(row("livetv.slots")).getByRole("button", { name: /^Restart / }));
    expect(
      await screen.findByText(/has nothing that runs on its own, so it cannot be restarted\./)
    ).toBeInTheDocument();
  });

  it("reads in Arabic", async () => {
    localStorage.setItem("mkt:locale", "ar");
    open();
    await screen.findByRole("heading", { name: AR["components.page.title"] });
    await waitFor(() => expect(document.querySelector("[data-component]")).not.toBeNull());
    expect(
      within(row("transcription")).getByRole("button", { name: "إعادة تشغيل التفريغ النصي" })
    ).toHaveTextContent(AR["components.page.restart"]!);
  });
});

describe("Components page: the licence lock and the Waiting link (story 51.19 §4)", () => {
  it("an off part the plan does not include has a locked switch; pressing it says why and links Subscription", async () => {
    setRow("cloud.relay", { want: "installed", phase: "off", licensed: false });
    const user = userEvent.setup();
    open();
    await loaded();
    const r = row("cloud.relay");
    const sw = within(r).getByRole("switch", { name: EN["components.cloud.relay.title"]! });
    expect(sw).not.toBeChecked();
    expect(r).toHaveTextContent("Needs a plan");
    expect(r.querySelector(".mkt-comp-row__locked")).not.toBeNull();
    await user.click(sw);
    const said = await within(r).findByTestId("comp-licence");
    expect(said).toHaveTextContent(EN["components.licence.refused"]!);
    expect(within(said).getByRole("link", { name: "Open Subscription" })).toHaveAttribute(
      "href",
      "/settings/subscription"
    );
    // nothing was asked of the server
    expect(post).not.toHaveBeenCalledWith("/api/components/preview", expect.anything());
    expect(puts()).toHaveLength(0);
  });

  it("a part waiting for the plan keeps its wish, says so and links Subscription", async () => {
    setRow("cloud.relay", { phase: "waiting", reason: "licence_missing", licensed: false });
    open();
    await loaded();
    const r = row("cloud.relay");
    expect(within(r).getByTestId("comp-chip")).toHaveTextContent(
      `Waiting · ${EN["components.licence.waiting"]}`
    );
    expect(within(r).getByTestId("comp-chip").textContent).not.toContain("{");
    expect(within(r).getByRole("link", { name: "Open Subscription" })).toHaveAttribute(
      "href",
      "/settings/subscription"
    );
    expect(within(r).getByRole("switch")).toBeChecked();
    // a licensed part has neither
    expect(
      within(row("transcription")).queryByRole("link", { name: "Open Subscription" })
    ).toBeNull();
  });

  it("reads in Arabic, and axe finds nothing in either language", async () => {
    setRow("cloud.relay", { want: "installed", phase: "off", licensed: false });
    setRow("cloud.link", {
      phase: "waiting",
      reason: "licence_missing",
      licensed: false,
      licence: "cloud_relay",
    });
    await axeBoth(async () => {
      const r = open();
      await waitFor(() => expect(document.querySelector("[data-component]")).not.toBeNull());
      return r;
    });
    localStorage.setItem("mkt:locale", "ar");
    open();
    await screen.findByRole("heading", { name: AR["components.page.title"] });
    await waitFor(() => expect(document.querySelector("[data-component]")).not.toBeNull());
    expect(row("cloud.relay")).toHaveTextContent(AR["components.licence.locked"]!);
    expect(row("cloud.link")).toHaveTextContent(AR["components.licence.waiting"]!);
    expect(
      within(row("cloud.link")).getByRole("link", {
        name: AR["components.licence.openSubscription"]!,
      })
    ).toBeInTheDocument();
  });
});

const HEALTHY: ComponentDetail["machines"] = [
  {
    machine: "home",
    phase: "running",
    observed_generation: 3,
    health: {
      since: new Date(Date.now() - (3 * 3600 + 5 * 60) * 1000).toISOString(),
      restarts: 9,
      restarts_24h: 2,
      last_error_code: "crash_loop",
      last_error_at: "2026-11-02T22:00:00Z",
      version: "0.9.1",
      rss_mb: 512,
      disk_mb: 2048,
      stale: true,
    },
  },
];

function serveDetail(id: string, machines: ComponentDetail["machines"]) {
  get.mockImplementation((url: string) => {
    if (url === "/api/components") return Promise.resolve(structuredClone(list));
    if (url === `/api/components/${id}`) return Promise.resolve({ ...detailOf(id), machines });
    if (url === "/api/rules") return Promise.resolve(RULES);
    return Promise.reject(new Error(`unmocked GET ${url}`));
  });
}

describe("Components page: the drawer's health (story 51.14)", () => {
  it("the drawer's close button is in the page's language", async () => {
    serveDetail("transcription", HEALTHY);
    localStorage.setItem("mkt:locale", "ar");
    const user = userEvent.setup();
    open();
    await waitFor(() => expect(document.querySelector("[data-component]")).not.toBeNull());
    await user.click(within(row("transcription")).getAllByRole("button").at(-1)!);
    const drawer = await screen.findByRole("dialog", {
      name: AR["components.transcription.title"],
    });
    expect(within(drawer).getByRole("button", { name: AR["common.close"] })).toBeInTheDocument();
    expect(within(drawer).queryByRole("button", { name: "Close" })).toBeNull();
  });

  it("shows each machine's uptime, restarts in 24 h, memory, disk and last error", async () => {
    serveDetail("transcription", HEALTHY);
    const user = userEvent.setup();
    open();
    await loaded();
    await user.click(
      within(row("transcription")).getByRole("button", { name: "Details of Transcription" })
    );
    const drawer = await screen.findByRole("dialog", { name: "Transcription" });
    const h = await within(drawer).findByTestId("comp-health");
    expect(h).toHaveTextContent("Running for3h 5m");
    // the 24 h count, not the count since the supervisor started
    expect(h).toHaveTextContent("Restarts in the last 24 hours2");
    expect(h).toHaveTextContent("Memory512 MB");
    expect(h).toHaveTextContent("On disk2 GB");
    expect(h).toHaveTextContent(`Last error${EN["components.reason.crash_loop"]}`);
    expect(h).toHaveTextContent(EN["components.drawer.stale"]!);
  });

  it("an older server's restart count still shows; an asset has no restarts line", async () => {
    serveDetail("model.whisper-turbo", [
      {
        machine: "home",
        phase: "off",
        observed_generation: 3,
        health: { disk_mb: 1600, stale: false },
      },
    ]);
    const user = userEvent.setup();
    open();
    await loaded();
    await user.click(
      within(row("model.whisper-turbo")).getByRole("button", { name: /^Details of/ })
    );
    const h = await screen.findByTestId("comp-health");
    expect(h).toHaveTextContent("On disk1.6 GB");
    expect(h).not.toHaveTextContent("Restarts");
    expect(h).not.toHaveTextContent("Running for");
  });

  it("reads in Arabic, axe clean in both languages", async () => {
    serveDetail("transcription", HEALTHY);
    await axeBoth(async () => {
      const user = userEvent.setup();
      const r = open();
      await waitFor(() => expect(document.querySelector("[data-component]")).not.toBeNull());
      await user.click(within(row("transcription")).getAllByRole("button").at(-1)!);
      await screen.findByTestId("comp-health");
      return r;
    });
    localStorage.setItem("mkt:locale", "ar");
    const user = userEvent.setup();
    open();
    await waitFor(() => expect(document.querySelector("[data-component]")).not.toBeNull());
    await user.click(within(row("transcription")).getAllByRole("button").at(-1)!);
    const h = await screen.findByTestId("comp-health");
    expect(h).toHaveTextContent(AR["components.drawer.uptime"]!);
    expect(h).toHaveTextContent(AR["components.drawer.restarts"]!);
    expect(h).toHaveTextContent(AR["components.drawer.memory"]!);
    expect(h).toHaveTextContent(AR["components.drawer.disk"]!);
  });
});

const RULES = {
  sections: [
    {
      key: "transcription",
      title: "Transcription",
      what: "",
      component: "transcription",
      fields: Array.from({ length: 9 }, (_, i) => ({
        name: `f${i}`,
        kind: "int",
        group: "",
        label: `F${i}`,
        help: "",
        default: 0,
        value: i < 7 ? 1 : 0,
        changed: i < 7,
      })),
    },
    {
      key: "detect",
      title: "Detect",
      what: "",
      component: "classify",
      fields: [
        {
          name: "x",
          kind: "int",
          group: "",
          label: "X",
          help: "",
          default: 0,
          value: 1,
          changed: true,
        },
      ],
    },
  ],
};

async function openReset(user: ReturnType<typeof userEvent.setup>) {
  await user.click(
    within(row("transcription")).getByRole("button", { name: "Details of Transcription" })
  );
  const drawer = await screen.findByRole("dialog", { name: "Transcription" });
  await user.click(within(drawer).getByRole("button", { name: "Reset settings" }));
  return screen.findByRole("dialog", { name: "Reset Transcription's settings?" });
}

describe("Components page: the settings-reset dialog (story 51.13 §5)", () => {
  it("asks first with the number of changed settings; Cancel resets nothing", async () => {
    serveDetail("transcription", []);
    const user = userEvent.setup();
    open();
    await loaded();
    const dialog = await openReset(user);
    expect(await within(dialog).findByTestId("comp-reset-text")).toHaveTextContent(
      "Reset Transcription's settings to the defaults? Your changes to 7 settings will be lost."
    );
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    expect(screen.queryByRole("dialog", { name: /^Reset/ })).toBeNull();
    expect(post).not.toHaveBeenCalledWith(
      "/api/components/transcription/settings/reset",
      expect.anything()
    );
    expect(post.mock.calls.some(([u]) => String(u).endsWith("/settings/reset"))).toBe(false);
  });

  it("confirming resets them and says so", async () => {
    serveDetail("transcription", []);
    post.mockImplementation((url: string) =>
      url === "/api/components/transcription/settings/reset"
        ? Promise.resolve({
            id: "transcription",
            sections: [{ section: "transcription", changed: 7 }],
            changed: 7,
          })
        : Promise.reject(new Error(`unmocked POST ${url}`))
    );
    const user = userEvent.setup();
    open();
    await loaded();
    const dialog = await openReset(user);
    await within(dialog).findByTestId("comp-reset-text");
    await user.click(within(dialog).getByRole("button", { name: "Reset settings" }));
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith("/api/components/transcription/settings/reset")
    );
    expect(
      await screen.findByText("Transcription's settings are back to the defaults.")
    ).toBeInTheDocument();
    expect(screen.queryByRole("dialog", { name: /^Reset/ })).toBeNull();
  });

  it("a component that owns no rules section has no Reset settings", async () => {
    serveDetail("llm", []);
    const user = userEvent.setup();
    open();
    await loaded();
    await user.click(within(row("llm")).getByRole("button", { name: "Details of Language model" }));
    const drawer = await screen.findByRole("dialog", { name: "Language model" });
    expect(within(drawer).queryByRole("button", { name: "Reset settings" })).toBeNull();
  });

  it("reads in Arabic, axe clean in both languages", async () => {
    serveDetail("transcription", []);
    await axeBoth(async () => {
      const user = userEvent.setup();
      const r = open();
      await waitFor(() => expect(document.querySelector("[data-component]")).not.toBeNull());
      await user.click(within(row("transcription")).getAllByRole("button").at(-1)!);
      const drawer = await screen.findByRole("dialog");
      await user.click(
        within(drawer).getByRole("button", {
          name: new RegExp(
            `^(${EN["components.settings.reset"]}|${AR["components.settings.reset"]})$`
          ),
        })
      );
      await screen.findByTestId("comp-reset-text");
      return r;
    });
    localStorage.setItem("mkt:locale", "ar");
    const user = userEvent.setup();
    open();
    await waitFor(() => expect(document.querySelector("[data-component]")).not.toBeNull());
    await user.click(within(row("transcription")).getAllByRole("button").at(-1)!);
    const drawer = await screen.findByRole("dialog");
    await user.click(
      within(drawer).getByRole("button", { name: AR["components.settings.reset"]! })
    );
    expect(await screen.findByTestId("comp-reset-text")).toHaveTextContent(
      "هل تريد إعادة إعدادات التفريغ النصي إلى الافتراضية؟ ستفقد تغييراتك على ٧ من الإعدادات."
    );
  });
});

describe("Components page: offline files (story 51.16 §6)", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("Download for offline makes the file, shows its progress and then a link to save it", async () => {
    const NAME = "maktaba-offline-1.2.3-linux-arm64-core.tar";
    post.mockImplementation((url: string) =>
      url === "/api/components/offline"
        ? Promise.resolve({ name: NAME, url: `/api/components/offline/${NAME}` })
        : Promise.reject(new Error(`unmocked POST ${url}`))
    );
    let asks = 0;
    const fetchMock = vi.fn(async (u: string) => {
      expect(u).toContain(`/api/components/offline/${NAME}`);
      asks++;
      if (asks === 1)
        return new Response(
          JSON.stringify({
            name: NAME,
            progress: {
              phase: "installing",
              percent: 40,
              detail: "pack_offline",
              name: "runtime.ml",
              done: 1048576,
              total: 4194304,
              busy: true,
            },
          }),
          { status: 202, headers: { "Content-Type": "application/json" } }
        );
      return new Response("TAR", { status: 200, headers: { "Content-Type": "application/x-tar" } });
    });
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    open();
    await loaded();
    await user.click(screen.getByRole("button", { name: "Download for offline" }));
    const dialog = await screen.findByRole("dialog", { name: "Download for offline" });
    expect(within(dialog).getByRole("radio", { name: "Full" })).toBeChecked();
    await user.click(within(dialog).getByRole("radio", { name: "Core" }));
    await user.selectOptions(within(dialog).getByRole("combobox"), "linux-arm64");
    await user.click(within(dialog).getByRole("button", { name: "Make the file" }));
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith("/api/components/offline", {
        bundle: "core",
        platform: "linux-arm64",
      })
    );
    const building = await within(dialog).findByTestId("comp-offline-building");
    expect(building).toHaveTextContent("Gathering runtime.ml: 1 MB of 4 MB");
    const ready = await within(dialog).findByTestId("comp-offline-ready", {}, { timeout: 5000 });
    const link = within(ready).getByRole("link", { name: "Save the file" });
    expect(link.getAttribute("href")).toContain(`/api/components/offline/${NAME}`);
    expect(link).toHaveAttribute("download", NAME);
  });

  it("a failed build says why in the reason's words", async () => {
    post.mockImplementation(() =>
      Promise.reject(
        new ApiError({
          type: "about:blank",
          title: "components unavailable",
          status: 503,
          code: "components_unavailable",
        })
      )
    );
    const user = userEvent.setup();
    open();
    await loaded();
    await user.click(screen.getByRole("button", { name: "Download for offline" }));
    const dialog = await screen.findByRole("dialog", { name: "Download for offline" });
    await user.click(within(dialog).getByRole("button", { name: "Make the file" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent(
      EN["components.reason.components_unavailable"]!
    );
  });

  it("Import a file sends the file itself and says what it brought", async () => {
    upload.mockResolvedValue({ installed: ["runtime.ml"], changed: [] });
    const user = userEvent.setup();
    open();
    await loaded();
    expect(screen.getByRole("button", { name: "Import a file" })).toBeInTheDocument();
    const file = new File(["TAR"], "home.tar", { type: "application/x-tar" });
    await user.upload(screen.getByTestId("comp-import-file"), file);
    await waitFor(() => expect(upload).toHaveBeenCalled());
    const [url, body, , type] = upload.mock.calls[0]!;
    expect(url).toBe("/api/components/import?name=home.tar");
    expect(body).toBe(file);
    expect(type).toBe("application/x-tar");
    expect(
      await screen.findByText("Imported: Machine learning runtime. Turn on what you want to use.")
    ).toBeInTheDocument();
  });

  it("an import for another version is refused in words, with the version", async () => {
    upload.mockRejectedValue(
      new ApiError({
        type: "about:blank",
        title: "offline wrong version",
        status: 409,
        code: "offline_wrong_version",
        params: { version: "1.2.3" },
      } as never)
    );
    const user = userEvent.setup();
    open();
    await loaded();
    await user.upload(
      screen.getByTestId("comp-import-file"),
      new File(["TAR"], "old.tar", { type: "application/x-tar" })
    );
    expect(await screen.findByText("This file is for Maktaba 1.2.3.")).toBeInTheDocument();
  });

  it("reads in Arabic, axe clean in both languages", async () => {
    await axeBoth(async () => {
      const user = userEvent.setup();
      const r = open();
      await waitFor(() => expect(document.querySelector("[data-component]")).not.toBeNull());
      await user.click(
        screen.getByRole("button", {
          name: new RegExp(
            `^(${EN["components.offline.download"]}|${AR["components.offline.download"]})$`
          ),
        })
      );
      await screen.findByRole("dialog");
      return r;
    });
    localStorage.setItem("mkt:locale", "ar");
    open();
    await waitFor(() => expect(document.querySelector("[data-component]")).not.toBeNull());
    expect(
      screen.getByRole("button", { name: AR["components.offline.download"]! })
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: AR["components.offline.import"]! })
    ).toBeInTheDocument();
  });
});

describe("Components page: a reason's params fill its words", () => {
  for (const locale of ["en", "ar"] as const) {
    it(`fills {name} and the like from the status or the install's progress (${locale})`, async () => {
      localStorage.setItem("mkt:locale", locale);
      const M = locale === "ar" ? AR : EN;
      setRow("runtime.ml", {
        phase: "failed",
        reason: "download_failed",
        reason_params: { name: "release" },
      });
      setRow("model.whisper-turbo", {
        phase: "failed",
        reason: "disk_space",
        progress: {
          phase: "failed",
          percent: 0,
          done: 0,
          total: 0,
          busy: false,
          reason: "disk_space",
          params: { need: "3.2 GB" },
        },
      });
      setRow("tool.yt-dlp", {
        phase: "installing",
        progress: {
          phase: "installing",
          percent: 25,
          detail: "pack_downloading",
          name: "yt-dlp",
          done: 1048576,
          total: 4194304,
          busy: true,
        },
      });
      // the same code with no params says nothing rather than a raw placeholder
      setRow("runtime.ollama", { phase: "failed", reason: "download_failed" });
      open();
      await screen.findByRole("heading", { name: M["components.page.title"] });
      await waitFor(() => expect(document.querySelector("[data-component]")).not.toBeNull());
      const chip = (id: string) => within(row(id)).getByTestId("comp-chip");
      expect(chip("runtime.ml")).toHaveTextContent(
        M["components.reason.download_failed"]!.replace("{name}", "release")
      );
      expect(chip("model.whisper-turbo")).toHaveTextContent(
        M["components.reason.disk_space"]!.replace("{need}", "3.2 GB")
      );
      expect(chip("tool.yt-dlp").textContent).toContain("yt-dlp");
      const panel = document.querySelector(".mkt-comp")!;
      expect(panel.textContent).not.toMatch(/\{\w+\}/);
    });
  }

  for (const locale of ["en", "ar"] as const) {
    it(`a param that is a component's id reads as its title, in the row and the drawer (${locale})`, async () => {
      localStorage.setItem("mkt:locale", locale);
      const M = locale === "ar" ? AR : EN;
      const failed = {
        phase: "failed",
        reason: "pack_not_published",
        reason_params: { name: "runtime.ml", version: "0.4.2" },
      };
      setRow("runtime.ml", failed);
      serveDetail("runtime.ml", [{ machine: "home", observed_generation: 3, ...failed }]);
      const user = userEvent.setup();
      open();
      await screen.findByRole("heading", { name: M["components.page.title"] });
      await waitFor(() => expect(document.querySelector("[data-component]")).not.toBeNull());
      const said = M["components.reason.pack_not_published"]!.replace(
        "{name}",
        M["components.runtime.ml.title"]!
      );
      const chip = within(row("runtime.ml")).getByTestId("comp-chip");
      expect(chip).toHaveTextContent(said);
      expect(chip.textContent).not.toContain("runtime.ml");
      await user.click(within(row("runtime.ml")).getAllByRole("button").at(-1)!);
      const drawer = await screen.findByRole("dialog", { name: M["components.runtime.ml.title"] });
      await waitFor(() => expect(drawer).toHaveTextContent(said));
      expect(drawer.textContent).not.toContain("runtime.ml");
    });
  }

  it("a param that names no component stays as it came", async () => {
    setRow("runtime.ml", {
      phase: "failed",
      reason: "pack_not_published",
      reason_params: { name: "v0.4.2" },
    });
    open();
    await loaded();
    expect(within(row("runtime.ml")).getByTestId("comp-chip")).toHaveTextContent(
      EN["components.reason.pack_not_published"]!.replace("{name}", "v0.4.2")
    );
  });

  it("has the words for an unpublished pack in English and Arabic", () => {
    expect(EN["components.reason.pack_not_published"]).toContain("{name}");
    expect(AR["components.reason.pack_not_published"]).toContain("{name}");
    expect(AR["components.reason.pack_not_published"]).not.toBe(
      EN["components.reason.pack_not_published"]
    );
  });
});

describe("Components page: every new word has Arabic", () => {
  it("restart, licence, health, reset, offline and catch-up words", () => {
    const keys = Object.keys(EN).filter(
      (k) =>
        k.startsWith("components.offline.") ||
        k.startsWith("components.licence.") ||
        k.startsWith("components.settings.") ||
        k.startsWith("components.drawer.") ||
        k.startsWith("queue.catchup") ||
        k === "components.page.restart" ||
        k === "components.page.restartOf" ||
        k === "components.done.restart" ||
        k === "components.refused.not_restartable" ||
        k === "components.refused.not_running"
    );
    expect(keys.length).toBeGreaterThan(30);
    for (const k of keys) {
      expect(AR[k], k).toBeTruthy();
      expect(AR[k], k).not.toBe(EN[k]);
    }
  });
});
