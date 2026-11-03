// Story 51.10 — Settings → Components, cases T01–T12 of the story (T11's server.toml note
// waits for the API to carry it, story 51.13; the Settings link is checked here).
// The fake API is built from the real catalog (shared/components/testdata/catalog.dump.json).
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

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

  it("T11: a component that owns a rules section links to its form", async () => {
    setRow("transcription", { settings: ["rules.transcription", "toml:transcription.workers"] });
    open();
    await loaded();
    expect(
      within(row("transcription")).getByRole("link", { name: "Settings for Transcription" })
    ).toHaveAttribute("href", "/settings/rules#transcription");
    expect(
      within(row("library.convert")).queryByRole("link", { name: /^Settings for/ })
    ).toBeNull();
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
});
