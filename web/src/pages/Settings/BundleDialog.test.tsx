// Story 51.17 — Settings → Components → Change bundle: the difference shown first (a dry
// run), applied as one batch with the revision it showed.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { post } = vi.hoisted(() => ({ post: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, post } };
});

import en from "../../i18n/en.json";
import ar from "../../i18n/ar.json";
import type { BundleDiff } from "../../lib/componentsApi";
import { renderPage } from "../../test/pageHarness";
import { BundleDialog } from "./BundleDialog";

const EN = en as Record<string, string>;
const AR = ar as Record<string, string>;
const NOTHING = { effects: [], conflicts: [], refused: [], queued_jobs_parked: 0, drain_s: 0 };

function diff(over: Partial<BundleDiff>): BundleDiff {
  return {
    based_on: "core",
    to: "full",
    enable: [],
    install: [],
    disable: [],
    remove: [],
    download_bytes: 0,
    revision: 7,
    dry_run: true,
    effect: NOTHING,
    ...over,
  };
}

const titles: Record<string, string> = {
  transcription: "Transcription",
  llm: "Language model",
  "runtime.ml": "ML runtime",
  "model.whisper-turbo": "Whisper turbo",
};

beforeEach(() => {
  post.mockReset();
  post.mockImplementation(
    (_url: string, body: { bundle: string; dry_run?: boolean; remove_unused?: boolean }) =>
      Promise.resolve(
        body.bundle === "full"
          ? diff({
              enable: ["transcription", "llm"],
              install: [
                { id: "runtime.ml", bytes: 2 * 2 ** 30 },
                { id: "model.whisper-turbo", bytes: 1.5 * 2 ** 30 },
              ],
              download_bytes: 3.5 * 2 ** 30,
              dry_run: !!body.dry_run,
            })
          : diff({
              to: "core",
              disable: ["transcription"],
              remove: body.remove_unused ? ["runtime.ml"] : [],
              dry_run: !!body.dry_run,
            })
      )
  );
});

function open(onApplied = vi.fn()) {
  renderPage(
    <BundleDialog
      basedOn="core"
      titleOf={(id) => titles[id] ?? id}
      onClose={vi.fn()}
      onApplied={onApplied}
    />,
    { path: "/settings/components" }
  );
  return onApplied;
}

describe("Change bundle (story 51.17)", () => {
  it("a Core home to Full: shows what turns on and downloads, then applies with the revision", async () => {
    const applied = open();
    expect(
      await screen.findByText("Turns on: Transcription and Language model")
    ).toBeInTheDocument();
    expect(
      screen.getByText("Downloads: ML runtime and Whisper turbo (about 3.5 GB)")
    ).toBeInTheDocument();
    expect(post).toHaveBeenCalledWith("/api/components/bundle", {
      bundle: "full",
      dry_run: true,
      remove_unused: false,
    });
    await userEvent.click(screen.getByRole("button", { name: EN["components.bundle.apply"] }));
    await waitFor(() =>
      expect(post).toHaveBeenLastCalledWith("/api/components/bundle", {
        bundle: "full",
        remove_unused: false,
        generation: 7,
      })
    );
    expect(applied).toHaveBeenCalledWith("full");
  });

  it("to Core: turns off, and removes files only when asked", async () => {
    open();
    await userEvent.click(await screen.findByRole("radio", { name: EN["components.bundle.core"] }));
    expect(await screen.findByText("Turns off: Transcription")).toBeInTheDocument();
    expect(screen.queryByText(/Removes the files/)).not.toBeInTheDocument();
    await userEvent.click(
      screen.getByRole("checkbox", { name: EN["components.bundle.removeUnused"] })
    );
    expect(await screen.findByText("Removes the files of: ML runtime")).toBeInTheDocument();
  });

  it("has its words in English and Arabic", () => {
    for (const k of [
      "change",
      "pick",
      "dialogTitle",
      "enable",
      "install",
      "disable",
      "remove",
      "removeUnused",
      "nothing",
      "apply",
      "done",
    ]) {
      expect(EN[`components.bundle.${k}`], k).toBeTruthy();
      expect(AR[`components.bundle.${k}`], k).toBeTruthy();
    }
  });
});
