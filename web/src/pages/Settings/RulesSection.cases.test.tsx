// Settings -> Rules cases from web/test-cases/settings-rules.md that RulesSection.test.tsx
// does not cover. This file signs users in through the shared page harness (the older
// file mocks lib/auth instead).
import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { get, patch, post } = vi.hoisted(() => ({ get: vi.fn(), patch: vi.fn(), post: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, patch, post } };
});

import { ApiError, type Problem } from "../../lib/api";
import type { RuleField, RuleSection, RulesResponse } from "../../lib/rulesApi";
import { memberUser, quietCrash, renderPage } from "../../test/pageHarness";
import { routeGet } from "../../test/queueHarness";
import rulesJson from "../../test/fixtures/rules.json";
import { RulesSection } from "./RulesSection";
import en from "../../i18n/en.json";

const fixture = () => structuredClone(rulesJson) as unknown as RulesResponse;

function field(data: RulesResponse, section: string, name: string): RuleField {
  return data.sections.find((s) => s.key === section)!.fields.find((f) => f.name === name)!;
}

function extraField(f: Partial<RuleField> & Pick<RuleField, "name" | "kind" | "label">): RuleField {
  return {
    group: "Extra",
    help: "",
    changed: false,
    default: null,
    ...f,
    value: f.value ?? f.default ?? null,
  };
}

/** The fixture plus an "Extra" section with a bounded number, an optional float, a url and a colour. */
function withExtra(data: RulesResponse = fixture()): RulesResponse {
  data.sections.push({
    key: "extra",
    title: "Extra",
    what: "more kinds",
    fields: [
      extraField({
        name: "volume_level",
        kind: "int",
        label: "Volume",
        default: 5,
        minimum: 0,
        maximum: 10,
      }),
      extraField({ name: "gain", kind: "float?", label: "Gain", default: 1.5 }),
      extraField({ name: "logo_url", kind: "str", label: "Logos web address", default: "" }),
      extraField({ name: "bg_color", kind: "str", label: "Background colour", default: "#0b5d3b" }),
    ],
  });
  return data;
}

const idleStatus = {
  running: false,
  kind: null,
  phase: "",
  started_at: null,
  finished_at: null,
  summary: {},
  log: [],
  ready: { preview: false, plan: false },
};

function problem(status: number, title: string, detail?: string) {
  return new ApiError({
    type: "about:blank",
    title,
    status,
    ...(detail ? { detail } : {}),
  } as Problem);
}

function setup(
  rules: unknown = fixture(),
  opts: { user?: typeof memberUser | null; history?: string[] } = {}
) {
  routeGet(get, {
    "/api/rules": typeof rules === "function" ? rules : () => structuredClone(rules),
    "/api/reports/channel-plan/preview": () => idleStatus,
  });
  return renderPage(<RulesSection />, { path: "/settings/rules", ...opts });
}

const rule = (label: string) => screen.findByRole("group", { name: label });
const spin = async (label: string) => within(await rule(label)).getByRole("spinbutton");
const rulesCalls = () => get.mock.calls.filter((c) => c[0] === "/api/rules").length;

async function typeInto(el: HTMLElement, text: string) {
  await userEvent.clear(el);
  if (text) await userEvent.type(el, text);
}

// the rules page renders every section of a big schema: slower than most
describe("Settings rules (test-cases/settings-rules.md)", { timeout: 20_000 }, () => {
  beforeEach(() => {
    get.mockReset();
    patch.mockReset();
    post.mockReset();
  });

  it("SRL-05: Discard changes drops all edits in this section only", async () => {
    setup();
    await typeInto(await spin("Shortest slot (min)"), "3");
    await typeInto(await spin("Slot step (min)"), "15");
    await userEvent.click(screen.getByRole("tab", { name: /Grouping/ }));
    await typeInto(await spin("Playlist group from (videos)"), "7");
    await userEvent.click(screen.getByRole("tab", { name: /Channels/ }));
    expect(screen.getByText(/Fix 1 setting\(s\) marked below/)).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Discard changes" }));
    expect(patch).not.toHaveBeenCalled();
    expect(await spin("Shortest slot (min)")).toHaveValue(30);
    expect(await spin("Slot step (min)")).toHaveValue(30);
    expect(screen.queryByText(/Fix 1 setting/)).not.toBeInTheDocument();
    expect(
      screen.queryByText("unsaved", { selector: ".mkt-rules__set *" })
    ).not.toBeInTheDocument();
    expect(document.querySelector("span[aria-live]")).toHaveTextContent("");
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Discard changes" })).toBeDisabled();
    expect(screen.getByRole("tab", { name: /Grouping/ })).toHaveTextContent("1 unsaved");
  });

  it("SRL-06: the loading state while the rules load", () => {
    get.mockReturnValue(new Promise(() => {}));
    renderPage(<RulesSection />, { path: "/settings/rules" });
    expect(screen.getByRole("heading", { level: 2, name: "Rules" })).toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent("Loading…");
    expect(screen.queryByRole("tab")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Save" })).not.toBeInTheDocument();
  });

  it("SRL-07: a failed load (500 / 401 / network / bad JSON) shows the error", async () => {
    const fails: [unknown, string][] = [
      [problem(500, "Internal Server Error", "db"), "db"],
      [problem(401, "Unauthorized"), "Unauthorized"],
      [new TypeError("Failed to fetch"), "Failed to fetch"],
      [new SyntaxError("Unexpected token b in JSON"), "Unexpected token b in JSON"],
    ];
    for (const [err, text] of fails) {
      const { unmount } = setup(() => Promise.reject(err));
      const alert = await screen.findByRole("alert");
      expect(alert).toHaveTextContent(`Could not load the rules. ${text}`);
      expect(screen.queryByRole("tab")).not.toBeInTheDocument();
      unmount();
    }
  });

  it("SRL-08: sections [] shows the heading, intro and an empty message", async () => {
    setup({ sections: [] });
    expect(await screen.findByText(/The rules detection, grouping/)).toBeInTheDocument();
    expect(screen.getByText("Nothing here yet.")).toBeInTheDocument();
    expect(screen.queryByRole("tab")).not.toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("SRL-09: sections null or missing reads as empty, not an error", async () => {
    for (const body of [{ sections: null }, {}]) {
      const { unmount } = setup(body);
      expect(await screen.findByText("Nothing here yet.")).toBeInTheDocument();
      expect(screen.queryByRole("alert")).not.toBeInTheDocument();
      unmount();
    }
  });

  it("SRL-10: a section with fields null or missing renders empty; other sections work", async () => {
    const bad = { key: "planner", title: "Channels", what: "x", fields: null };
    const good = fixture().sections.find((s) => s.key === "groups")!;
    for (const sections of [
      [bad],
      [{ ...bad, fields: undefined }],
      [good, { ...bad, key: "broken", title: "Broken" }],
    ]) {
      const { unmount } = setup({ sections });
      expect(await screen.findAllByRole("tab")).toHaveLength(sections.length);
      expect(screen.queryByRole("alert")).not.toBeInTheDocument();
      if (sections.length === 2) {
        expect(await rule("Playlist group from (videos)")).toBeInTheDocument();
        await userEvent.click(screen.getByRole("tab", { name: /Broken/ }));
      }
      expect(screen.getByText("Nothing here yet.")).toBeInTheDocument();
      unmount();
    }
  });

  it("SRL-11: null or missing nested lists on a field do not crash", async () => {
    const section = {
      key: "planner",
      title: "Channels",
      what: "x",
      unknown: null,
      fields: [
        extraField({
          name: "khutbah_day",
          kind: "str",
          label: "Khutbah day",
          default: "fri",
          choices: null as unknown as string[],
        }),
        extraField({
          name: "themes",
          kind: "rows",
          label: "Themed channels",
          columns: null as unknown as string[],
          column_kinds: null as unknown as string[],
          time_columns: null as unknown as number[],
          column_optional: null as unknown as boolean[],
        }),
        extraField({ name: "mix_daypart_topics", kind: "map", label: "Topics per daypart" }),
        extraField({ name: "weekend_days", kind: "list", label: "Weekend days" }),
        extraField({
          name: "tags",
          kind: "list",
          label: "Tags",
          default: ["a"],
          value: ["a", null],
        }),
      ],
    } as unknown as RuleSection;
    setup({ sections: [section] });
    expect(within(await rule("Khutbah day")).getByRole("textbox")).toHaveValue("fri");
    expect(screen.queryByText(/no longer exist/)).not.toBeInTheDocument();
    const themes = await rule("Themed channels");
    expect(within(themes).getByText("No rows yet.")).toBeInTheDocument();
    await userEvent.click(within(themes).getByRole("button", { name: "Add row" }));
    expect(within(await rule("Topics per daypart")).getByText("No rows yet.")).toBeInTheDocument();
    expect(
      within(await rule("Weekend days")).getByText(/Default: (empty|none)/)
    ).toBeInTheDocument();
    expect(await rule("Tags")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("SRL-13: a PATCH 403 / 500 / network error shows a banner and keeps the edit", async () => {
    setup();
    const slot = await spin("Shortest slot (min)");
    await typeInto(slot, "45");
    const loads = rulesCalls();
    const fails: [unknown, string][] = [
      [problem(403, "Forbidden", "admin only"), "Not saved: admin only"],
      [problem(500, "Internal Server Error"), "Not saved: Internal Server Error"],
      [new TypeError("Failed to fetch"), "Not saved: Failed to fetch"],
    ];
    for (const [err, text] of fails) {
      patch.mockRejectedValueOnce(err);
      await userEvent.click(screen.getByRole("button", { name: "Save" }));
      expect(await screen.findByRole("alert")).toHaveTextContent(text);
      expect(
        within(await rule("Shortest slot (min)")).queryByRole("alert")
      ).not.toBeInTheDocument();
      expect(await spin("Shortest slot (min)")).toHaveValue(45);
      expect(screen.getByText("1 unsaved", { selector: "span[aria-live]" })).toBeInTheDocument();
      expect(screen.getByRole("button", { name: "Save" })).toBeEnabled();
    }
    expect(rulesCalls()).toBe(loads);
  });

  it("SRL-14: the reload after a good save fails: saved notice plus the load error", async () => {
    let calls = 0;
    setup(() =>
      calls++ === 0 ? fixture() : Promise.reject(problem(500, "Internal Server Error", "db"))
    );
    await typeInto(await spin("Shortest slot (min)"), "45");
    patch.mockResolvedValue({ section: "planner", changed: ["min_slot_minutes"] });
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByRole("status")).toHaveTextContent(/Saved\. 1 rule\(s\) differ/);
    expect(await screen.findByRole("alert")).toHaveTextContent("Could not load the rules. db");
    expect(await spin("Shortest slot (min)")).toHaveValue(30); // the old saved value
    expect(screen.getByRole("tab", { name: /Channels/ })).not.toHaveTextContent("unsaved");
  });

  it("SRL-15: number checks: required, whole, min, max; an optional number may be empty", async () => {
    setup(withExtra());
    const slot = await spin("Shortest slot (min)");
    const group = await rule("Shortest slot (min)");
    for (const [v, msg] of [
      ["", "Required."],
      ["30.5", "Enter a whole number."],
      ["3", "Must be at least 5."],
    ]) {
      await typeInto(slot, v);
      expect(within(group).getByText(msg)).toBeInTheDocument();
      expect(slot).toHaveAttribute("aria-invalid", "true");
      expect(screen.getByText("Fix 1 setting(s) marked below before saving.")).toBeInTheDocument();
      expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
      expect(screen.getByText("1 to fix")).toBeInTheDocument();
    }
    await typeInto(slot, "30");
    expect(screen.queryByText(/Fix 1 setting/)).not.toBeInTheDocument();

    await userEvent.click(screen.getByRole("tab", { name: /Extra/ }));
    const volume = await spin("Volume");
    await typeInto(volume, "11");
    expect(within(await rule("Volume")).getByText("Must be at most 10.")).toBeInTheDocument();
    await typeInto(volume, "7");
    await typeInto(await spin("Gain"), "");
    expect(screen.queryByText(/Fix \d setting/)).not.toBeInTheDocument();
    patch.mockResolvedValue({ section: "extra", changed: [] });
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(patch).toHaveBeenCalledWith("/api/rules/extra", {
      set: { volume_level: 7, gain: null },
      reset: [],
    });
  });

  it("SRL-16: url, colour and select checks on single fields", async () => {
    const data = withExtra();
    field(data, "planner", "quran_rotation").value = "random";
    setup(data);
    // select: a saved value outside the choices stays visible and is flagged
    const rotation = within(await rule("Reciter rotation")).getByRole("combobox");
    expect(within(rotation).getAllByRole("option")[0]).toHaveValue("random");
    // recorded: the choice message only shows for an edit, not for the saved value
    await userEvent.selectOptions(rotation, "by_time");
    expect(rotation).toHaveValue("by_time");
    expect(
      within(await rule("Reciter rotation")).queryByText("Choose one of the options.")
    ).not.toBeInTheDocument();

    await userEvent.click(screen.getByRole("tab", { name: /Extra/ }));
    const url = within(await rule("Logos web address")).getByRole("textbox");
    await typeInto(url, "aljazeera");
    expect(
      within(await rule("Logos web address")).getByText("Enter a full address, such as https://…")
    ).toBeInTheDocument();
    await typeInto(url, "");
    expect(
      within(await rule("Logos web address")).queryByText(/Enter a full address/)
    ).not.toBeInTheDocument();

    const colour = within(await rule("Background colour")).getAllByRole("textbox")[0];
    await typeInto(colour, "#12345");
    expect(
      within(await rule("Background colour")).getByText(
        "Enter a colour as #RRGGBB, such as #0b5d3b."
      )
    ).toBeInTheDocument();
  });

  it("SRL-18: the map table drops empty items and empty keys, splits on the Arabic comma", async () => {
    setup();
    const map = await rule("Mix channel: topics per daypart");
    expect(within(map).getByRole("columnheader", { name: "Key" })).toBeInTheDocument();
    expect(
      within(map).getByRole("columnheader", { name: "Values (comma separated)" })
    ).toBeInTheDocument();
    const values = within(map).getAllByRole("textbox", { name: "Values (comma separated)" });
    const keys = within(map).getAllByRole("textbox", { name: "Key" });
    const morning = keys.findIndex((k) => (k as HTMLInputElement).value === "morning");
    await typeInto(values[morning], "tafsir, , fiqh");
    await userEvent.click(within(map).getByRole("button", { name: "Add row" }));
    let vals = within(map).getAllByRole("textbox", { name: "Values (comma separated)" });
    await userEvent.type(vals[vals.length - 1], "x");
    await userEvent.click(within(map).getByRole("button", { name: "Add row" }));
    const ks = within(map).getAllByRole("textbox", { name: "Key" });
    vals = within(map).getAllByRole("textbox", { name: "Values (comma separated)" });
    await userEvent.type(ks[ks.length - 1], " night ");
    await userEvent.type(vals[vals.length - 1], "a،b");
    patch.mockResolvedValue({ section: "planner", changed: [] });
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(patch).toHaveBeenCalledTimes(1));
    const sent = (patch.mock.calls[0][1] as { set: Record<string, Record<string, string[]>> }).set
      .mix_daypart_topics;
    expect(sent.morning).toEqual(["tafsir", "fiqh"]);
    expect(sent.night).toEqual(["a", "b"]);
    expect(Object.keys(sent)).not.toContain("");
  });

  it("SRL-19: the chips editor adds by Enter / comma, dedupes, Backspace and remove", async () => {
    setup();
    const group = await rule("Topics kept off the mix channel");
    const input = within(group).getByRole("textbox");
    const chips = () =>
      within(group)
        .queryAllByRole("listitem")
        .map((li) => li.textContent?.replace(/Remove.*$/, "").trim());
    expect(input).toHaveAccessibleDescription(/Press Enter or type a comma to add/);
    await userEvent.type(input, "fiqh{Enter}");
    expect(chips()).toEqual(["current_affairs", "fiqh"]);
    await userEvent.type(input, "hadith,");
    expect(chips()).toEqual(["current_affairs", "fiqh", "hadith"]);
    await userEvent.type(input, "fiqh{Enter}");
    expect(chips()).toEqual(["current_affairs", "fiqh", "hadith"]);
    await userEvent.type(input, "{Backspace}");
    expect(chips()).toEqual(["current_affairs", "fiqh"]);
    await userEvent.click(within(group).getByRole("button", { name: "Remove current_affairs" }));
    expect(chips()).toEqual(["fiqh"]);
    await userEvent.type(input, "tafsir");
    await userEvent.tab();
    expect(chips()).toEqual(["fiqh", "tafsir"]);
    patch.mockResolvedValue({ section: "planner", changed: [] });
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(patch).toHaveBeenCalledWith("/api/rules/planner", {
      set: { mix_exclude_topics: ["fiqh", "tafsir"] },
      reset: [],
    });
  });

  it("SRL-20: Save is disabled with nothing to save and while busy", async () => {
    const data = fixture();
    Object.assign(field(data, "planner", "max_slot_minutes"), { value: 90, changed: true });
    setup(data);
    const save = () => screen.getByRole("button", { name: "Save" });
    const discard = () => screen.getByRole("button", { name: "Discard changes" });
    await rule("Shortest slot (min)");
    expect(save()).toBeDisabled();
    expect(discard()).toBeDisabled();

    await typeInto(await spin("Shortest slot (min)"), "45");
    patch.mockReturnValue(new Promise(() => {}));
    await userEvent.click(save());
    await userEvent.click(save());
    expect(patch).toHaveBeenCalledTimes(1);
    expect(discard()).toBeDisabled();
    expect(
      within(await rule("Longest slot (min)")).getByRole("button", { name: "Back to default" })
    ).toBeDisabled();
  });

  it("SRL-20: an edit typed back to the saved value is no longer unsaved", async () => {
    setup();
    const slot = await spin("Shortest slot (min)");
    await typeInto(slot, "45");
    expect(screen.getByRole("button", { name: "Save" })).toBeEnabled();
    await typeInto(slot, "30");
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
  });

  it("SRL-24: a non-admin is read-only everywhere", async () => {
    const data = withExtra();
    data.sections[0].unknown = ["old_rule"];
    Object.assign(field(data, "planner", "max_slot_minutes"), { value: 90, changed: true });
    setup(data, { user: memberUser });
    expect(
      await screen.findByText("Only admins can change the rules. You can view them here.")
    ).toBeInTheDocument();
    await rule("Shortest slot (min)");
    for (const name of [
      "Save",
      "Discard changes",
      "Back to default",
      "Remove them",
      "Add row",
      "Duplicate row",
      "Remove row",
    ])
      expect(screen.queryByRole("button", { name })).not.toBeInTheDocument();
    expect(document.querySelector("span[aria-live]")).toBeNull();
    const chips = await rule("Topics kept off the mix channel");
    expect(within(chips).queryByRole("textbox")).not.toBeInTheDocument();
    expect(within(chips).queryByRole("button")).not.toBeInTheDocument();
    for (const b of within(await rule("Weekend days")).getAllByRole("button"))
      expect(b).toBeDisabled();
    expect(within(await rule("Mix channel name")).getByRole("textbox")).toHaveAttribute("readonly");
    await userEvent.click(screen.getByRole("tab", { name: /Extra/ }));
    expect(within(await rule("Logos web address")).getByRole("textbox")).toHaveAttribute(
      "readonly"
    );
    expect(patch).not.toHaveBeenCalled();
  });

  it("SRL-25: a signed-out deep link redirects to /login", () => {
    setup(fixture(), { user: null });
    expect(screen.getByText("login page (from /settings/rules)")).toBeInTheDocument();
    expect(get).not.toHaveBeenCalled();
  });

  // Bug: leaving with unsaved edits loses them silently; wanted a warning / router blocker.
  it.fails("SRL-26: leaving the tab with unsaved edits asks first", async () => {
    setup();
    await typeInto(await spin("Shortest slot (min)"), "45");
    vi.spyOn(window, "confirm").mockReturnValue(false);
    await userEvent.click(screen.getByRole("link", { name: "go elsewhere" }));
    expect(screen.queryByText("elsewhere")).not.toBeInTheDocument();
  });

  it("SRL-27: a render crash shows the route error fallback; Back works", async () => {
    quietCrash();
    const bad = fixture();
    bad.sections[0].title = { bad: true } as unknown as string;
    let data: RulesResponse = bad;
    setup(() => structuredClone(data), { history: ["/elsewhere"] });
    expect(await screen.findByRole("alert")).toHaveTextContent(en["common.pageError"]);
    await userEvent.click(screen.getByRole("button", { name: "go back" }));
    expect(screen.getByText("elsewhere")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    data = fixture();
    await userEvent.click(screen.getByRole("link", { name: "go elsewhere" }));
    expect(screen.getByText("elsewhere")).toBeInTheDocument();
  });

  it("SRL-28: Arabic: RTL page strings", async () => {
    localStorage.setItem("mkt:locale", "ar");
    try {
      setup();
      expect(document.documentElement).toHaveAttribute("dir", "rtl");
      expect(await screen.findByRole("heading", { level: 2, name: "القواعد" })).toBeInTheDocument();
      expect(screen.getByRole("searchbox", { name: "ابحث عن قاعدة" })).toBeInTheDocument();
      expect(screen.getByRole("button", { name: "حفظ" })).toBeInTheDocument();
      expect(screen.getByRole("button", { name: "تجاهل التغييرات" })).toBeInTheDocument();
    } finally {
      localStorage.removeItem("mkt:locale");
    }
  });

  it("SRL-29: section tabs move with the arrow keys, Home and End", async () => {
    setup();
    const tab = (n: RegExp) => screen.getByRole("tab", { name: n });
    await rule("Shortest slot (min)");
    tab(/Channels/).focus();
    await userEvent.keyboard("{ArrowRight}");
    expect(tab(/Grouping/)).toHaveAttribute("aria-selected", "true");
    expect(tab(/Grouping/)).toHaveFocus();
    await userEvent.keyboard("{ArrowRight}{ArrowRight}");
    expect(tab(/Channels/)).toHaveAttribute("aria-selected", "true");
    await userEvent.keyboard("{End}");
    expect(tab(/Detection/)).toHaveAttribute("aria-selected", "true");
    await userEvent.keyboard("{Home}");
    expect(tab(/Channels/)).toHaveAttribute("aria-selected", "true");
  });

  // Bug in @ds Tabs (shared): ArrowLeft should move to the next tab in RTL.
  it.fails("SRL-29: in Arabic, ArrowLeft moves to the next tab", async () => {
    localStorage.setItem("mkt:locale", "ar");
    try {
      setup();
      await rule("Shortest slot (min)");
      const tabs = await screen.findAllByRole("tab");
      tabs[0].focus();
      await userEvent.keyboard("{ArrowLeft}");
      expect(tabs[1]).toHaveAttribute("aria-selected", "true");
    } finally {
      localStorage.removeItem("mkt:locale");
    }
  });

  it("SRL-30: rows are labelled groups with described controls and live regions", async () => {
    setup();
    const group = await rule("Shortest slot (min)");
    const input = within(group).getByRole("spinbutton", { name: "Shortest slot (min)" });
    await typeInto(input, "3");
    const described = (input.getAttribute("aria-describedby") ?? "").split(" ");
    const err = within(group).getByText("Must be at least 5.");
    expect(described).toContain(err.id);
    expect(err).toHaveAttribute("role", "alert");
    expect(document.querySelector("span[aria-live='polite']")).toHaveTextContent("1 unsaved");
    const fold = screen.getByRole("button", { name: "Grid and slots" });
    expect(fold).toHaveAttribute("aria-expanded", "true");
    expect(fold).toHaveAttribute("aria-controls");
    expect(screen.getByRole("searchbox", { name: "Find a rule" })).toBeInTheDocument();
  });
});
