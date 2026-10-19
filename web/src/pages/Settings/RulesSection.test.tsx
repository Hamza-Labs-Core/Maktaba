import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, within, waitFor, fireEvent } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { get, patch, post } = vi.hoisted(() => ({ get: vi.fn(), patch: vi.fn(), post: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, patch, post } };
});
const authState = vi.hoisted(() => ({
  user: { id: "u1", username: "admin", is_admin: true } as {
    id: string;
    username: string;
    is_admin: boolean;
  } | null,
}));
vi.mock("../../lib/auth", () => ({ useAuth: () => authState }));

import { ApiError, type Problem } from "../../lib/api";
import type { RuleField, RulesResponse } from "../../lib/rulesApi";
import { RulesSection } from "./RulesSection";
import { renderAt, routeGet } from "../../test/queueHarness";
import rulesJson from "../../test/fixtures/rules.json";

// The fixture is api/internal/handlers/rules/schema.json with value =
// default and changed = false on every field.
const fixture = () => structuredClone(rulesJson) as unknown as RulesResponse;

function field(data: RulesResponse, section: string, name: string): RuleField {
  return data.sections.find((s) => s.key === section)!.fields.find((f) => f.name === name)!;
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

function setup(data: RulesResponse = fixture()) {
  routeGet(get, {
    "/api/rules": () => data,
    "/api/reports/channel-plan/preview": () => idleStatus,
  });
  return renderAt(<RulesSection />, "/settings/rules");
}

// A TV section shaped like the served schema (tv + dizquetv rules): a
// live-broadcast table, a time kept as text, bounded and weight numbers,
// and a long group whose settings share name prefixes.
const LIVE_COLUMNS = [
  "channel",
  "url",
  "title",
  "start",
  "end",
  "every_minutes",
  "minutes",
  "days",
];
function tvField(f: Partial<RuleField> & Pick<RuleField, "name" | "kind" | "label">): RuleField {
  return {
    group: "Maktaba TV",
    help: "",
    changed: false,
    default: null,
    ...f,
    value: f.value ?? f.default ?? null,
  };
}
function withTv(data: RulesResponse = fixture()): RulesResponse {
  const weight = (n: string, label: string, d: number) =>
    tvField({
      name: `dz_weight_${n}`,
      kind: "int",
      label,
      default: d,
      minimum: 0,
      group: "dizqueTV",
    });
  data.sections.push({
    key: "tv",
    title: "Maktaba TV",
    what: "how Maktaba's own channels air",
    fields: [
      tvField({
        name: "live",
        kind: "rows",
        label: "Live broadcasts",
        help: "A live stream a channel carries at set times.",
        default: [],
        columns: LIVE_COLUMNS,
        column_kinds: ["int", "str", "str", "time", "time", "int", "int", "list"],
        column_optional: LIVE_COLUMNS.map(() => true),
      }),
      tvField({ name: "news_start", kind: "str", label: "News from", default: "06:00" }),
      tvField({
        name: "volume_level",
        kind: "int",
        label: "Volume",
        default: 5,
        minimum: 0,
        maximum: 10,
      }),
      tvField({ name: "loudnorm", kind: "bool", label: "Even loudness", default: true }),
      tvField({
        name: "dz_lateness_ms",
        kind: "int",
        label: "Allowed lateness (ms)",
        default: 86400000,
        minimum: 0,
        group: "dizqueTV",
      }),
      tvField({
        name: "dz_max_days",
        kind: "int",
        label: "Schedule length (days)",
        default: 365,
        minimum: 1,
        group: "dizqueTV",
      }),
      tvField({
        name: "dz_pad_ms",
        kind: "int",
        label: "Pad (ms)",
        default: 1,
        minimum: 0,
        group: "dizqueTV",
      }),
      weight("topic", "Filler weight: the channel's topic clips", 300),
      weight("promos", "Filler weight: its shows' promos", 150),
      weight("breaks", "Filler weight: breaks", 200),
      weight("general", "Filler weight: general clips", 100),
      tvField({
        name: "dz_art_url",
        kind: "str",
        label: "Logos web address",
        default: "",
        group: "dizqueTV",
      }),
    ],
  });
  return data;
}

async function openTv() {
  await userEvent.click(await screen.findByRole("tab", { name: /Maktaba TV/ }));
}

const rule = async (label: string) => screen.findByRole("group", { name: label });

// the rules page renders every section and types into many fields: slower than most
describe("RulesSection", { timeout: 20_000 }, () => {
  beforeEach(() => {
    get.mockReset();
    patch.mockReset();
    post.mockReset();
    authState.user = { id: "u1", username: "admin", is_admin: true };
  });

  it("renders a tab per section and the fields grouped, with label, name, help and default", async () => {
    setup();
    expect(await screen.findByRole("tab", { name: /Channels/ })).toHaveAttribute(
      "aria-selected",
      "true"
    );
    expect(screen.getByRole("tab", { name: /Grouping/ })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: /Detection/ })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Grid and slots" })).toBeInTheDocument();
    const slot = await rule("Shortest slot (min)");
    expect(within(slot).getByText("min_slot_minutes")).toBeInTheDocument();
    expect(within(slot).getByText("Default: 30")).toBeInTheDocument();
    const input = within(slot).getByRole("spinbutton", { name: "Shortest slot (min)" });
    expect(input).toHaveValue(30);
    expect(input).toHaveAttribute("min", "5");
    // tables are labelled by their setting and build typed cells
    const themes = await rule("Themed channels");
    expect(within(themes).getByRole("table", { name: "Themed channels" })).toBeInTheDocument();
    expect(within(themes).getAllByRole("textbox", { name: "name ar" })[0]).toHaveValue(
      "القرآن الكريم"
    );
    expect(within(themes).getAllByRole("textbox", { name: "name ar" })[0]).toHaveAttribute(
      "dir",
      "auto"
    );
    // other sections render on their tab
    await userEvent.click(screen.getByRole("tab", { name: /Grouping/ }));
    expect(await rule("Playlist group from (videos)")).toBeInTheDocument();
  });

  it("saves only the edited field and keeps edits made in another tab", async () => {
    const data = fixture();
    setup(data);
    // an edit in the Grouping tab first
    await userEvent.click(await screen.findByRole("tab", { name: /Grouping/ }));
    const playlist = within(await rule("Playlist group from (videos)")).getByRole("spinbutton");
    await userEvent.clear(playlist);
    await userEvent.type(playlist, "7");
    expect(screen.getByRole("tab", { name: /Grouping/ })).toHaveTextContent(
      "0 changed · 1 unsaved"
    );

    await userEvent.click(screen.getByRole("tab", { name: /Channels/ }));
    const input = within(await rule("Shortest slot (min)")).getByRole("spinbutton");
    await userEvent.clear(input);
    await userEvent.type(input, "45");
    expect(screen.getByText("1 unsaved", { selector: "span[aria-live]" })).toBeInTheDocument();

    patch.mockImplementation(async () => {
      Object.assign(field(data, "planner", "min_slot_minutes"), { value: 45, changed: true });
      return { section: "planner", changed: ["min_slot_minutes"] };
    });
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(patch).toHaveBeenCalledTimes(1);
    expect(patch).toHaveBeenCalledWith("/api/rules/planner", {
      set: { min_slot_minutes: 45 },
      reset: [],
    });
    expect(await screen.findByText(/Saved\. 1 rule\(s\) differ/)).toBeInTheDocument();
    await waitFor(() =>
      expect(screen.getByRole("tab", { name: /Channels/ })).toHaveTextContent("1 changed")
    );
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
    expect(within(await rule("Shortest slot (min)")).getByText("changed")).toBeInTheDocument();
    // the Grouping edit was not sent and is still pending
    expect(screen.getByRole("tab", { name: /Grouping/ })).toHaveTextContent("1 unsaved");
  });

  it("a save that went to a TV change draft links to it (Epic 33)", async () => {
    const data = fixture();
    setup(data);
    await userEvent.click(await screen.findByRole("tab", { name: /Channels/ }));
    const input = within(await rule("Shortest slot (min)")).getByRole("spinbutton");
    await userEvent.clear(input);
    await userEvent.type(input, "45");
    patch.mockResolvedValue({
      section: "planner",
      changed: ["min_slot_minutes"],
      draft_change: { id: "c0000000-0000-4000-8000-000000000043", number: 43 },
    });
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByText(/Saved to draft change #43/)).toBeInTheDocument();
    expect(screen.getByTestId("rules-draft-link")).toHaveAttribute(
      "href",
      "/admin/live-tv/changes/c0000000-0000-4000-8000-000000000043"
    );
  });

  it("puts a changed field back to its default", async () => {
    const data = fixture();
    Object.assign(field(data, "planner", "min_slot_minutes"), { value: 45, changed: true });
    setup(data);
    const slot = await rule("Shortest slot (min)");
    expect(within(slot).getByRole("spinbutton")).toHaveValue(45);
    patch.mockImplementation(async () => {
      Object.assign(field(data, "planner", "min_slot_minutes"), { value: 30, changed: false });
      return { section: "planner", changed: [] };
    });
    await userEvent.click(within(slot).getByRole("button", { name: "Back to default" }));
    expect(patch).toHaveBeenCalledWith("/api/rules/planner", {
      set: {},
      reset: ["min_slot_minutes"],
    });
    await waitFor(() =>
      expect(
        within(screen.getByRole("group", { name: "Shortest slot (min)" })).getByRole("spinbutton")
      ).toHaveValue(30)
    );
    expect(
      within(screen.getByRole("group", { name: "Shortest slot (min)" })).queryByRole("button", {
        name: "Back to default",
      })
    ).not.toBeInTheDocument();
  });

  it("shows a 400 under its field and keeps the edit", async () => {
    setup();
    const slot = await rule("Shortest slot (min)");
    const input = within(slot).getByRole("spinbutton");
    await userEvent.clear(input);
    await userEvent.type(input, "7");
    patch.mockRejectedValue(
      new ApiError({
        status: 400,
        error: "min_slot_minutes: must divide the slot step",
        field: "min_slot_minutes",
      } as unknown as Problem)
    );
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(
      await within(slot).findByText("min_slot_minutes: must divide the slot step")
    ).toBeInTheDocument();
    expect(input).toHaveValue(7);
    expect(screen.getByRole("button", { name: "Save" })).toBeEnabled();
    // editing the field clears its error
    await userEvent.type(input, "0");
    expect(
      within(slot).queryByText("min_slot_minutes: must divide the slot step")
    ).not.toBeInTheDocument();
  });

  it("checks a typed number against its minimum before saving", async () => {
    setup();
    const slot = await rule("Shortest slot (min)");
    const input = within(slot).getByRole("spinbutton");
    await userEvent.clear(input);
    await userEvent.type(input, "3");
    expect(within(slot).getByText("Must be at least 5.")).toBeInTheDocument();
    expect(input).toHaveAttribute("aria-invalid", "true");
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
    expect(screen.getByText(/Fix 1 setting\(s\) marked below/)).toBeInTheDocument();
    await userEvent.type(input, "1");
    expect(within(slot).queryByText("Must be at least 5.")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Save" })).toBeEnabled();
  });

  it("adds a row to an empty table with typed cells", async () => {
    const data = fixture();
    Object.assign(field(data, "planner", "themes"), { value: [], changed: true });
    setup(data);
    const themes = await rule("Themed channels");
    expect(within(themes).queryAllByRole("textbox")).toHaveLength(0);
    await userEvent.click(within(themes).getByRole("button", { name: "Add row" }));
    await userEvent.type(within(themes).getByRole("textbox", { name: "key" }), "news");
    await userEvent.type(within(themes).getByRole("spinbutton", { name: "number" }), "120");
    await userEvent.type(within(themes).getByRole("textbox", { name: "topics" }), "fiqh, hadith");
    patch.mockResolvedValue({ section: "planner", changed: ["themes"] });
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(patch).toHaveBeenCalledWith("/api/rules/planner", {
      set: {
        themes: [
          {
            key: "news",
            name: "",
            name_ar: "",
            number: 120,
            topics: ["fiqh", "hadith"],
            audiences: [],
            formats: [],
            category: "",
            min_hours: null,
          },
        ],
      },
      reset: [],
    });
  });

  it("edits a list one per line, keeping a trailing space and dropping empty lines", async () => {
    setup();
    await userEvent.click(await screen.findByRole("tab", { name: /Grouping/ }));
    const box = within(await rule("Question / fatwa phrases")).getByRole("textbox");
    await userEvent.clear(box);
    await userEvent.type(box, "حكم {enter}{enter}ما هو");
    patch.mockResolvedValue({ section: "groups", changed: ["qa_phrases"] });
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(patch).toHaveBeenCalledWith("/api/rules/groups", {
      set: { qa_phrases: ["حكم ", "ما هو"] },
      reset: [],
    });
  });

  it("filters fields by name, label, help or group", async () => {
    setup();
    const search = await screen.findByRole("searchbox", { name: "Find a rule" });
    await userEvent.type(search, "khutbah");
    expect(screen.getByRole("group", { name: "Khutbah day" })).toBeInTheDocument();
    expect(screen.queryByRole("group", { name: "Shortest slot (min)" })).not.toBeInTheDocument();
    await userEvent.clear(search);
    await userEvent.type(search, "dz_pad");
    expect(screen.getByRole("group", { name: "Pad (ms)" })).toBeInTheDocument();
    await userEvent.clear(search);
    await userEvent.type(search, "zzz-nothing");
    expect(screen.getByText("No rule matches “zzz-nothing”.")).toBeInTheDocument();
  });

  it("flags invalid saved values and removes unknown names", async () => {
    const data = fixture();
    const planner = data.sections[0];
    planner.unknown = ["old_rule"];
    Object.assign(field(data, "planner", "max_slot_minutes"), {
      value: 2,
      changed: true,
      invalid: "must be at least 5",
    });
    setup(data);
    const slot = await rule("Longest slot (min)");
    expect(within(slot).getByText(/default applies: must be at least 5/)).toBeInTheDocument();
    expect(within(slot).getByRole("button", { name: "Back to default" })).toBeInTheDocument();
    expect(screen.getByText(/no longer exist: old_rule/)).toBeInTheDocument();
    patch.mockResolvedValue({ section: "planner", changed: ["max_slot_minutes"] });
    await userEvent.click(screen.getByRole("button", { name: "Remove them" }));
    expect(patch).toHaveBeenCalledWith("/api/rules/planner", { set: {}, reset: ["old_rule"] });
  });

  it("is read-only for a non-admin", async () => {
    authState.user = { id: "u2", username: "viewer", is_admin: false };
    setup();
    const slot = await rule("Shortest slot (min)");
    expect(within(slot).getByRole("spinbutton")).toHaveAttribute("readonly");
    expect(within(await rule("Mix channel")).getByRole("switch")).toBeDisabled();
    expect(within(await rule("Khutbah day")).getByRole("combobox")).toBeDisabled();
    expect(screen.queryByRole("button", { name: "Save" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Add row" })).not.toBeInTheDocument();
    expect(screen.getByText(/Only admins can change the rules/)).toBeInTheDocument();
  });
  it("previews the channel plan next to the Channels rules, and only there", async () => {
    post.mockResolvedValue({ started: true, kind: "preview" });
    setup();
    const preview = await screen.findByRole("region", { name: "Preview channels" });
    // the preview uses what is saved: an edit that is not saved yet is flagged
    expect(within(preview).queryByRole("status")).not.toBeInTheDocument();
    const input = within(await rule("Shortest slot (min)")).getByRole("spinbutton");
    await userEvent.clear(input);
    await userEvent.type(input, "45");
    expect(within(preview).getByRole("status")).toHaveTextContent(
      "1 edits are not saved yet; the preview uses the saved settings. Save first."
    );
    await userEvent.click(within(preview).getByRole("button", { name: "Preview channels" }));
    expect(post).toHaveBeenCalledWith("/api/reports/channel-plan/preview", { kind: "preview" });
    // the stored-plan button belongs to the Reports page, not to the rules
    expect(within(preview).queryByRole("button", { name: "Show the stored plan" })).toBeNull();
    await userEvent.click(screen.getByRole("tab", { name: /Grouping/ }));
    expect(screen.queryByRole("region", { name: "Preview channels" })).not.toBeInTheDocument();
  });

  it("does not offer the preview to a non-admin", async () => {
    authState.user = { id: "u2", username: "viewer", is_admin: false };
    setup();
    const preview = await screen.findByRole("region", { name: "Preview channels" });
    expect(within(preview).queryByRole("button", { name: "Preview channels" })).toBeNull();
  });

  it("edits an HH:MM text setting with a time picker and writes HH:MM back", async () => {
    setup(withTv());
    await openTv();
    const news = await rule("News from");
    const input = within(news).getByLabelText("News from");
    expect(input).toHaveAttribute("type", "time");
    expect(input).toHaveValue("06:00");
    fireEvent.change(input, { target: { value: "07:30" } });
    patch.mockResolvedValue({ section: "tv", changed: ["news_start"] });
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(patch).toHaveBeenCalledWith("/api/rules/tv", {
      set: { news_start: "07:30" },
      reset: [],
    });
  });

  it("toggles weekdays as chips", async () => {
    setup();
    const weekend = await rule("Weekend days");
    const fri = within(weekend).getByRole("button", { name: "Friday" });
    expect(fri).toHaveAttribute("aria-pressed", "true");
    expect(within(weekend).getByRole("button", { name: "Monday" })).toHaveAttribute(
      "aria-pressed",
      "false"
    );
    await userEvent.click(within(weekend).getByRole("button", { name: "Sunday" }));
    await userEvent.click(fri);
    expect(within(weekend).getByRole("button", { name: "Sunday" })).toHaveAttribute(
      "aria-pressed",
      "true"
    );
    patch.mockResolvedValue({ section: "planner", changed: ["weekend_days"] });
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(patch).toHaveBeenCalledWith("/api/rules/planner", {
      set: { weekend_days: ["sat", "sun"] },
      reset: [],
    });
  });

  it("moves a bounded number and a weight with a slider, with the unit beside", async () => {
    setup(withTv());
    await openTv();
    const volume = await rule("Volume");
    const slider = within(volume).getByRole("slider", { name: "Volume" });
    expect(slider).toHaveAttribute("min", "0");
    expect(slider).toHaveAttribute("max", "10");
    fireEvent.change(slider, { target: { value: "8" } });
    expect(within(volume).getByRole("spinbutton", { name: "Volume" })).toHaveValue(8);
    // a weight has a slider though only its minimum is known
    const topic = await rule("Filler weight: the channel's topic clips");
    fireEvent.change(within(topic).getByRole("slider"), { target: { value: "450" } });
    // a duration in ms says its unit and how long that is
    const lateness = await rule("Allowed lateness (ms)");
    expect(within(lateness).getByText("ms")).toBeInTheDocument();
    expect(within(lateness).getByText("≈ 1 days")).toBeInTheDocument();
    patch.mockResolvedValue({ section: "tv", changed: [] });
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(patch).toHaveBeenCalledWith("/api/rules/tv", {
      set: { volume_level: 8, dz_weight_topic: 450 },
      reset: [],
    });
  });

  it("checks table cells, blocks the save while one is wrong, and leaves empty optional cells out", async () => {
    setup(withTv());
    await openTv();
    const live = await rule("Live broadcasts");
    await userEvent.click(within(live).getByRole("button", { name: "Add row" }));
    await userEvent.type(within(live).getByRole("spinbutton", { name: "channel" }), "101");
    const url = within(live).getByRole("textbox", { name: "url" });
    expect(url).toHaveAttribute("type", "url");
    await userEvent.type(url, "aljazeera live");
    expect(url).toHaveAttribute("aria-invalid", "true");
    expect(within(live).getByText("Enter a full address, such as https://…")).toBeInTheDocument();
    expect(
      within(live).getByText("Row 1, url: Enter a full address, such as https://…")
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();

    await userEvent.clear(url);
    await userEvent.type(url, "https://www.youtube.com/@aljazeera/live");
    const start = within(live).getByLabelText("start");
    expect(start).toHaveAttribute("type", "time");
    fireEvent.change(start, { target: { value: "20:00" } });
    fireEvent.change(within(live).getByLabelText("end"), { target: { value: "02:00" } });
    expect(within(live).getByText("Runs past midnight")).toBeInTheDocument();
    await userEvent.click(within(live).getByRole("button", { name: "Friday" }));
    expect(screen.getByRole("button", { name: "Save" })).toBeEnabled();

    // a duplicated row is a copy below it
    await userEvent.click(within(live).getByRole("button", { name: "Duplicate row" }));
    expect(within(live).getAllByRole("textbox", { name: "url" })).toHaveLength(2);
    await userEvent.click(within(live).getAllByRole("button", { name: "Remove row" })[1]);

    patch.mockResolvedValue({ section: "tv", changed: ["live"] });
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(patch).toHaveBeenCalledWith("/api/rules/tv", {
      set: {
        live: [
          {
            channel: 101,
            url: "https://www.youtube.com/@aljazeera/live",
            start: "20:00",
            end: "02:00",
            days: ["fri"],
          },
        ],
      },
      reset: [],
    });
  });

  it("requires a time in a table column that cannot be left out", async () => {
    setup();
    const parts = await rule("Day parts");
    await userEvent.click(within(parts).getByRole("button", { name: "Add row" }));
    expect(within(parts).getAllByText("Required.").length).toBeGreaterThan(0);
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
  });

  it("puts an unsaved edit back to the default without a request", async () => {
    setup(withTv());
    await openTv();
    const volume = await rule("Volume");
    const box = within(volume).getByRole("spinbutton");
    await userEvent.clear(box);
    await userEvent.type(box, "9");
    await userEvent.click(within(volume).getByRole("button", { name: "Back to default" }));
    expect(box).toHaveValue(5);
    expect(within(volume).queryByRole("button", { name: "Back to default" })).toBeNull();
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
    expect(patch).not.toHaveBeenCalled();
  });

  it("folds groups and splits a long group by its settings' shared prefix", async () => {
    setup(withTv());
    await openTv();
    const heading = await screen.findByRole("heading", { name: "dizqueTV" });
    // dz_weight_* (the dz_ namespace skipped) form a sub-group named by their labels
    expect(screen.getByRole("heading", { name: "Filler weight" })).toBeInTheDocument();
    const fold = within(heading).getByRole("button", { name: "dizqueTV" });
    expect(fold).toHaveAttribute("aria-expanded", "true");
    await userEvent.click(fold);
    expect(fold).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByRole("group", { name: "Pad (ms)" })).not.toBeInTheDocument();
    // the Maktaba TV group stays open
    expect(screen.getByRole("group", { name: "Volume" })).toBeInTheDocument();
    // a search opens every group
    await userEvent.type(screen.getByRole("searchbox", { name: "Find a rule" }), "dz_pad");
    expect(screen.getByRole("group", { name: "Pad (ms)" })).toBeInTheDocument();
  });
});
