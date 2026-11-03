// Story 51.13 T13: a rules section names the component it belongs to (and says when that
// component is off), and a section the home could not move to this release's format says
// so and is saved whole (replace) from the form as shown.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { get, patch, post } = vi.hoisted(() => ({ get: vi.fn(), patch: vi.fn(), post: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, patch, post } };
});

import type { RuleField, RuleSection, RulesResponse } from "../../lib/rulesApi";
import { renderPage } from "../../test/pageHarness";
import { routeGet } from "../../test/queueHarness";
import { RulesSection } from "./RulesSection";
import en from "../../i18n/en.json";
import ar from "../../i18n/ar.json";

function num(name: string, label: string, value: number, def: number): RuleField {
  return {
    name,
    kind: "int",
    group: "Transcription",
    label,
    help: "",
    default: def,
    value,
    changed: value !== def,
  };
}

function section(extra: Partial<RuleSection>): RuleSection {
  return {
    key: "transcription",
    title: "Transcription",
    what: "which machines transcribe",
    version: [2, 0],
    fields: [
      num("relisten_tries", "Second-listen tries per transcript", 9, 6),
      num("relisten_round", "Relisten round", 0, 0),
    ],
    ...extra,
  };
}

function setup(s: RuleSection) {
  const data: RulesResponse = { sections: [s] };
  routeGet(get, { "/api/rules": () => structuredClone(data) });
  return renderPage(<RulesSection />, { path: "/settings/rules" });
}

describe("Rules: component settings (story 51.13)", { timeout: 20_000 }, () => {
  beforeEach(() => {
    get.mockReset();
    patch.mockReset();
    post.mockReset();
  });

  it("names the component a section belongs to, and says when it is off", async () => {
    setup(section({ component: "transcription", component_state: "off" }));
    expect(await screen.findByTestId("rules-part-of")).toHaveTextContent(
      "Part of Transcription (off)"
    );
    expect(screen.queryByTestId("rules-migrate-failed")).not.toBeInTheDocument();
  });

  it("shows no part-of note for core's sections", async () => {
    setup(section({ component: "core.api", component_state: "enabled" }));
    await screen.findByText("which machines transcribe");
    expect(screen.queryByTestId("rules-part-of")).not.toBeInTheDocument();
  });

  it("a section the home could not migrate says so and is saved whole", async () => {
    setup(
      section({
        component: "transcription",
        component_state: "enabled",
        failed: "migrate_v1_to_v2: boom",
      })
    );
    expect(await screen.findByTestId("rules-migrate-failed")).toHaveTextContent(
      en["rules.section.migrateFailed"]
    );
    expect(screen.getByTestId("rules-part-of")).toHaveTextContent("Part of Transcription");
    patch.mockResolvedValue({ section: "transcription", changed: ["relisten_tries"] });
    // nothing edited: the whole section can still be saved
    await userEvent.click(screen.getByRole("button", { name: en["rules.section.saveAll"] }));
    await waitFor(() => expect(patch).toHaveBeenCalledTimes(1));
    expect(patch).toHaveBeenCalledWith("/api/rules/transcription", {
      set: { relisten_tries: 9, relisten_round: 0 },
      reset: [],
      replace: true,
      version: [2, 0],
    });
  });

  it("a section waiting for the home's move cannot be saved yet", async () => {
    setup(section({ needs_migration: true }));
    expect(await screen.findByTestId("rules-migrating")).toHaveTextContent(
      en["rules.section.migrating"]
    );
    expect(screen.getByRole("button", { name: en["settings.rules.save"] })).toBeDisabled();
  });

  it("has every new word in English and Arabic", () => {
    const enC = en as Record<string, string>;
    const arC = ar as Record<string, string>;
    for (const k of [
      "rules.section.partOf",
      "rules.section.partOfState",
      "rules.section.state.off",
      "rules.section.state.absent",
      "rules.section.state.held",
      "rules.section.migrateFailed",
      "rules.section.migrating",
      "rules.section.saveAll",
      "components.settings.reset",
      "components.settings.resetConfirm",
      "components.settings.resetDone",
      "components.reason.settings_invalid",
    ]) {
      expect(enC[k], `${k} (en)`).toBeTruthy();
      expect(arC[k], `${k} (ar)`).toBeTruthy();
    }
  });
});
