import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, within, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { get, patch } = vi.hoisted(() => ({ get: vi.fn(), patch: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, patch } };
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

function setup(data: RulesResponse = fixture()) {
  routeGet(get, { "/api/rules": () => data });
  return renderAt(<RulesSection />, "/settings/rules");
}

const rule = async (label: string) => screen.findByRole("group", { name: label });

describe("RulesSection", () => {
  beforeEach(() => {
    get.mockReset();
    patch.mockReset();
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
    expect(screen.getByText("1 unsaved", { selector: "span" })).toBeInTheDocument();

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
    await userEvent.type(input, "3");
    patch.mockRejectedValue(
      new ApiError({
        status: 400,
        error: "min_slot_minutes: must be at least 5",
        field: "min_slot_minutes",
      } as unknown as Problem)
    );
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(
      await within(slot).findByText("min_slot_minutes: must be at least 5")
    ).toBeInTheDocument();
    expect(input).toHaveValue(3);
    expect(screen.getByRole("button", { name: "Save" })).toBeEnabled();
    // editing the field clears its error
    await userEvent.type(input, "0");
    expect(
      within(slot).queryByText("min_slot_minutes: must be at least 5")
    ).not.toBeInTheDocument();
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
    expect(within(await rule("Mix channel")).getByRole("checkbox")).toBeDisabled();
    expect(within(await rule("Khutbah day")).getByRole("combobox")).toBeDisabled();
    expect(screen.queryByRole("button", { name: "Save" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Add row" })).not.toBeInTheDocument();
    expect(screen.getByText(/Only admins can change the rules/)).toBeInTheDocument();
  });
});
