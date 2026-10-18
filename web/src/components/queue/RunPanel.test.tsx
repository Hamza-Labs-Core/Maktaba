import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { get, post, put, patch } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  put: vi.fn(),
  patch: vi.fn(),
}));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post, put, patch } };
});

import { renderAt } from "../../test/queueHarness";
import { CATALOG_ID, RunPanel, buildRunRequest, canStartAt, leaveOutChoices } from "./RunPanel";
import {
  extensionsFixture,
  librariesFixture,
  stageInfo,
  stagesFixture,
} from "../../test/fixtures/queues";

const stages = stagesFixture;

function setup(over: Partial<React.ComponentProps<typeof RunPanel>> = {}) {
  const onDone = vi.fn();
  renderAt(
    <RunPanel
      stages={stages}
      libraries={librariesFixture}
      extensions={extensionsFixture}
      canRun
      onDone={onDone}
      defaultOpen
      {...over}
    />,
    "/queue"
  );
  return { onDone };
}

const retriggerBody = () => post.mock.calls.find((c) => c[0] === "/api/queue/retrigger")![1];

describe("buildRunRequest", () => {
  const seasonsOf = vi.fn(async (id: string) => [`${id}-s1`, `${id}-s2`]);

  it("without a start step queues each library's entry queues, only what changed", async () => {
    const req = await buildRunRequest({ libraries: ["a", "b"], startAt: "", stages, seasonsOf });
    expect(req).toEqual({
      items: [
        { subject_kind: "library", subject_id: "a" },
        { subject_kind: "library", subject_id: "b" },
      ],
      mode: "queue",
      priority: 50,
    });
  });

  it("a per-library step starts at each library and redoes what follows", async () => {
    const req = await buildRunRequest({ libraries: ["a"], startAt: "detect", stages, seasonsOf });
    expect(req.mode).toBe("force_cascade");
    expect(req.items).toEqual([{ subject_kind: "library", subject_id: "a", stage: "detect" }]);
  });

  it("the planner step is one catalog item", async () => {
    const req = await buildRunRequest({
      libraries: ["a", "b"],
      startAt: "plan_channels",
      stages,
      seasonsOf,
    });
    expect(req.items).toEqual([
      { subject_kind: "catalog", subject_id: CATALOG_ID, stage: "plan_channels" },
    ]);
  });

  it("a per-season step expands to the seasons of every chosen library", async () => {
    const req = await buildRunRequest({
      libraries: ["a", "b"],
      startAt: "rate_style",
      stages,
      seasonsOf,
    });
    expect(req.items.map((i) => i.subject_id)).toEqual(["a-s1", "a-s2", "b-s1", "b-s2"]);
    expect(req.items.every((i) => i.subject_kind === "season" && i.stage === "rate_style")).toBe(
      true
    );
  });
});

describe("what the panel offers", () => {
  it("starts only at queues a library, season or catalog is the subject of", () => {
    const names = stages.filter(canStartAt).map((s) => s.stage);
    expect(names).toContain("detect");
    expect(names).toContain("plan_channels");
    expect(names).not.toContain("probe"); // a per-video step
    expect(names).not.toContain("dizquetv_show"); // switched off
  });

  it("leaves extension queues and the YouTube read to the write switches", () => {
    const names = leaveOutChoices(stages).map((s) => s.stage);
    expect(names).toContain("tag");
    expect(names).not.toContain("youtube_sync");
    expect(names).not.toContain("plex_collection");
  });
});

describe("RunPanel", () => {
  beforeEach(() => {
    get.mockReset();
    post.mockReset().mockResolvedValue({
      results: [
        {
          subject_kind: "library",
          subject_id: "x",
          stage: "detect",
          job_id: 1,
          outcome: "inserted",
        },
      ],
    });
    put.mockReset().mockResolvedValue({});
    patch.mockReset().mockResolvedValue({});
  });

  it("starts all libraries from the first step and changes no switch", async () => {
    const { onDone } = setup();
    expect(screen.getByTestId("run-review")).toHaveTextContent(
      `${librariesFixture.length} libraries · from the first step`
    );
    await userEvent.click(screen.getByRole("button", { name: "Start run" }));
    await waitFor(() => expect(onDone).toHaveBeenCalled());
    expect(retriggerBody().items).toHaveLength(librariesFixture.length);
    expect(retriggerBody().mode).toBe("queue");
    expect(patch).not.toHaveBeenCalled();
    expect(put).not.toHaveBeenCalled();
    expect(await screen.findByRole("status")).toHaveTextContent("1 queued");
  });

  it("saves a changed write switch and a left-out step, then queues from the chosen step", async () => {
    setup();
    // dizqueTV is not set up: its switch is off and cannot be turned on from here
    expect(screen.getByRole("checkbox", { name: /dizqueTV shows/ })).toBeDisabled();
    const plex = screen.getByRole("checkbox", { name: /Season collections in Plex/ });
    expect(plex).toBeChecked();
    await userEvent.click(plex); // stop writing to Plex
    await userEvent.click(screen.getByText(/^Leave out/));
    await userEvent.click(screen.getByRole("checkbox", { name: "Tag topics" })); // leave a step out
    await userEvent.selectOptions(screen.getByLabelText("Start at"), "detect");
    expect(screen.getByTestId("run-review")).toHaveTextContent("from Place episodes");
    await userEvent.click(screen.getByRole("button", { name: "Start run" }));
    await waitFor(() => expect(post).toHaveBeenCalled());
    expect(put).toHaveBeenCalledWith("/api/extensions/plex", { enabled: false });
    expect(patch).toHaveBeenCalledWith("/api/queues/tag", { enabled: false });
    expect(retriggerBody().mode).toBe("force_cascade");
    expect(retriggerBody().items[0]).toMatchObject({ subject_kind: "library", stage: "detect" });
    // the switches are saved before the work is queued
    expect(put.mock.invocationCallOrder[0]).toBeLessThan(post.mock.invocationCallOrder[0]!);
    expect(patch.mock.invocationCallOrder[0]).toBeLessThan(post.mock.invocationCallOrder[0]!);
  });

  it("a step that is left out cannot also be the start", async () => {
    setup();
    await userEvent.selectOptions(screen.getByLabelText("Start at"), "detect");
    await userEvent.click(screen.getByText(/^Leave out/));
    await userEvent.click(screen.getByRole("checkbox", { name: "Place episodes" }));
    expect(screen.getByLabelText("Start at")).toHaveValue("");
  });

  it("only the ticked libraries are queued, and none is an error", async () => {
    setup();
    await userEvent.click(screen.getByRole("radio", { name: /Only the libraries ticked/ }));
    await userEvent.click(screen.getByRole("button", { name: "Start run" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Pick at least one library");
    expect(post).not.toHaveBeenCalled();
    const first = librariesFixture[0]!;
    await userEvent.click(screen.getByRole("checkbox", { name: new RegExp(first.name) }));
    await userEvent.click(screen.getByRole("button", { name: "Start run" }));
    await waitFor(() => expect(post).toHaveBeenCalled());
    expect(retriggerBody().items).toEqual([
      { subject_kind: "library", subject_id: first.library_id },
    ]);
  });

  it("exact dates is shown, switched off, with the reason", () => {
    setup();
    const box = screen.getByRole("checkbox", { name: /Exact upload dates/ });
    expect(box).toBeDisabled();
    expect(box).not.toBeChecked();
  });

  it("a non-admin sees the panel but cannot start", () => {
    setup({ canRun: false });
    expect(screen.getByText("Only an admin can start a run.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Start run" })).toBeDisabled();
  });

  it("starts nothing for a step with nothing to queue", async () => {
    get.mockResolvedValue({ seasons: [] });
    setup({
      stages: [
        ...stages.filter((s) => s.stage !== "rate_style"),
        stageInfo({ stage: "rate_style" }),
      ],
    });
    await userEvent.selectOptions(screen.getByLabelText("Start at"), "rate_style");
    await userEvent.click(screen.getByRole("button", { name: "Start run" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Nothing to queue");
    expect(post).not.toHaveBeenCalled();
    expect(within(screen.getByRole("alert")).getByText(/Nothing to queue/)).toBeInTheDocument();
  });
});
