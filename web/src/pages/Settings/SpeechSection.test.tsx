import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { get, post, put } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post, put } };
});

import { SpeechSection, looksOffline } from "./SpeechSection";
import { renderPage } from "../../test/pageHarness";
import { routeGet } from "../../test/queueHarness";

const MODELS = [
  { name: "tiny", repo: "Systran/faster-whisper-tiny", download_mb: 78, worker_mb: 378, cost: 0.5 },
  { name: "base", repo: "Systran/faster-whisper-base", download_mb: 148, worker_mb: 448, cost: 1 },
  {
    name: "small",
    repo: "Systran/faster-whisper-small",
    download_mb: 486,
    worker_mb: 786,
    cost: 3,
  },
  {
    name: "medium",
    repo: "Systran/faster-whisper-medium",
    download_mb: 1528,
    worker_mb: 1828,
    cost: 8,
  },
];

// a Standard J4125 (no AVX2): base, measured at 0.5× real time, 900 hours waiting
const STANDARD = {
  available: true,
  speech: {
    on: true,
    on_set: false,
    model: "base",
    model_set: false,
    repo: "Systran/faster-whisper-base",
    default: "base",
    default_on: true,
    reason: "no_avx2",
    engine: "ct2",
    offered: ["tiny", "base", "small", "medium"],
    quiet_only: false,
    polite: true,
    second_listen: true,
    arch: "amd64",
  },
  restart_needed: false,
  can_restart: true,
  measure: { model: "Systran/faster-whisper-base", size: "base", rtf: 0.5, busy: true, threads: 2 },
  measuring: false,
  quiet_hours: "01:00-06:00",
  models: MODELS,
  advice: {
    backlog: { files: 300, hours: 900 },
    estimate: { model: "base", rtf: 0.5, measured: true, hours_per_day: 5, days: 360 },
    suggest: { model: "tiny", rtf: 1, measured: false, hours_per_day: 5, days: 180 },
  },
};

// a Light arm64 NAS: off until turned on
const LIGHT = {
  ...STANDARD,
  speech: {
    ...STANDARD.speech,
    on: false,
    reason: "light",
    offered: ["tiny", "base"],
    quiet_only: true,
    second_listen: false,
    arch: "arm64",
  },
  measure: null,
  models: MODELS.slice(0, 2),
  advice: { backlog: { files: 0, hours: 0 }, estimate: null },
};

function open(doc: unknown) {
  routeGet(get, { "/api/server/speech": () => doc });
  return renderPage(<SpeechSection restarting={false} onRestart={() => {}} />, {
    path: "/settings/machine",
  });
}

describe("SpeechSection", () => {
  beforeEach(() => {
    for (const f of [get, post, put]) f.mockReset();
    localStorage.removeItem("mkt:locale");
  });
  afterEach(() => {
    vi.restoreAllMocks();
    localStorage.removeItem("mkt:locale");
  });

  // T07: the measured speed, why the model, the model note and the backlog with the
  // smaller model's estimate, in English
  it("shows the measured speed, the model note and the backlog's estimate", async () => {
    open(STANDARD);
    expect(
      await screen.findByText(
        /This NAS transcribes slower than real time with base \(speed: 0.5×\)/
      )
    ).toBeInTheDocument();
    expect(screen.getByText(/Measured while the NAS was busy/)).toBeInTheDocument();
    expect(screen.getByTestId("speech-reason")).toHaveTextContent(
      "This processor lacks AVX2, so a smaller model was chosen."
    );
    expect(
      screen.getByText("Smaller models are faster but make more mistakes, especially in Arabic.")
    ).toBeInTheDocument();
    expect(screen.getByText(/Audio waiting: 900 h. Estimated time: 360 d./)).toBeInTheDocument();
    expect(screen.getByText(/With tiny, estimated time: 180 d./)).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "small (486 MB)" })).toBeInTheDocument();
    put.mockResolvedValue({ ...STANDARD, speech: { ...STANDARD.speech, model: "tiny" } });
    await userEvent.click(screen.getByRole("button", { name: "Use tiny" }));
    expect(put).toHaveBeenCalledWith("/api/server/speech", { model: "tiny" });
  });

  // T07: the same in Arabic
  it("says it in Arabic", async () => {
    localStorage.setItem("mkt:locale", "ar");
    open(STANDARD);
    expect(await screen.findByText(/أبطأ من الزمن الفعلي باستخدام base/)).toBeInTheDocument();
    expect(
      screen.getByText("النماذج الأصغر أسرع لكنها تخطئ أكثر، لا سيما في العربية.")
    ).toBeInTheDocument();
    expect(screen.getByTestId("speech-reason")).toHaveTextContent("يفتقر هذا المعالج إلى AVX2");
    expect(screen.getByText(/باستخدام tiny، الوقت المقدّر/)).toBeInTheDocument();
  });

  // Light: off, why, and one tap turns it on with base; the second listen is not on ARM
  it("turns speech on with base on the Light tier in one tap", async () => {
    open(LIGHT);
    expect(await screen.findByText(/Speech to text is off on this machine/)).toBeInTheDocument();
    expect(
      screen.getByText("The second listen is not available on ARM processors.")
    ).toBeInTheDocument();
    put.mockResolvedValue({
      ...LIGHT,
      speech: { ...LIGHT.speech, on: true },
      restart_needed: true,
    });
    await userEvent.click(screen.getByRole("button", { name: "Turn on with base" }));
    expect(put).toHaveBeenCalledWith("/api/server/speech", { speech: "on", model: "base" });
    expect(
      await screen.findByText(/The change applies when the server starts again./)
    ).toBeInTheDocument();
  });

  it("names both places for the model when it cannot be downloaded", async () => {
    open({
      ...STANDARD,
      measure: {
        model: STANDARD.speech.repo,
        size: "base",
        error: "LocalEntryNotFoundError: offline",
      },
      advice: undefined,
    });
    expect(await screen.findByText(/Copy it from another Maktaba computer/)).toBeInTheDocument();
    expect(looksOffline("ConnectionError: could not resolve huggingface.co")).toBe(true);
    expect(looksOffline("ValueError: bad model")).toBe(false);
  });

  it("shows nothing without a supervisor", async () => {
    const { container } = open({ available: false, reason: "no supervisor" });
    await new Promise((r) => setTimeout(r, 0));
    expect(container.querySelector("[data-testid=speech-section]")).toBeNull();
  });
});
