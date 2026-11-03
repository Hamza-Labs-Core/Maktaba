// A machine's transcription line on Admin > Machines: ready on its GPU, installing with an
// update, or the exact reason it cannot transcribe (the Windows node said nothing:
// "unavailable" in the stage table, no GPU, no stt extra).
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { screen, within } from "@testing-library/react";
import { renderPage } from "../../test/pageHarness";
import { ToastProvider } from "@ds/components/Toast/Toast";

const { get } = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return {
    ...actual,
    api: { ...actual.api, get, post: vi.fn(), put: vi.fn(), patch: vi.fn(), delete: vi.fn() },
  };
});

import type { ClusterNode } from "../../lib/clusterApi";
import { Machines } from "./Machines";

const NOW = new Date().toISOString();
const RTX = { name: "NVIDIA GeForce RTX 2080 SUPER", memory_gb: 8, compute_capability: "7.5" };
const WAITS =
  "unavailable: the second listen does not run here (NVIDIA GeForce RTX 2080 SUPER has 8.0 GB:" +
  " the second listen needs a GPU of 9.5 GB or more here): the machines that run it transcribe" +
  " (rules transcription: without_second_listen lets this one too)";

function pc(
  over: Partial<ClusterNode> = {},
  stages: string[] = [],
  off: Record<string, string> = {}
): ClusterNode {
  return {
    id: "w1",
    name: "windows-pc",
    state: "approved",
    os: "windows",
    arch: "amd64",
    versions: {},
    capabilities: {
      os: "windows",
      gpus: [RTX],
      stt_backend: "ct2-cuda",
      extras: ["search", "stt"],
    },
    storage: { read: "ok", write: "ok", checked_at: NOW, detail: null },
    path_map: { "/Volumes/Originals/": "O:/" },
    path_map_source: "admin",
    drain: false,
    expires_at: null,
    created_at: NOW,
    approved_at: NOW,
    revoked_at: null,
    last_seen_at: NOW,
    online: true,
    workers: [{ id: "node-w1-1", host: "MEYWD-Home", last_seen_at: NOW, stages, stages_off: off }],
    stage_overrides: {},
    ...over,
  };
}

function show(node: ClusterNode) {
  const view = {
    home: {
      instance_id: "i1",
      name: "Home",
      accept_joins: false,
      accept_joins_until: null,
      roles_supported: true,
      roles_reason: null,
    },
    nodes: [node],
  };
  get.mockImplementation((url: string) => {
    const path = String(url ?? "").split("?")[0];
    const routes: Record<string, unknown> = {
      "/api/cluster": view,
      "/api/cluster/relay": {
        available: false,
        enabled: false,
        settings: {},
        extensions: [],
        home_only: [],
        calls: [],
        totals: [],
      },
      "/api/cluster/files": { files: [], nodes: [] },
      "/api/cluster/storage-servers": { servers: [] },
    };
    return Promise.resolve(path in routes ? routes[path] : {});
  });
  renderPage(
    <ToastProvider>
      <Machines />
    </ToastProvider>,
    { path: "/admin/machines" }
  );
}

describe("Machines: the transcription line", () => {
  beforeEach(() => {
    get.mockReset();
  });
  afterEach(() => {
    localStorage.removeItem("mkt:locale");
    document.documentElement.dir = "ltr";
  });

  it("ready on the PC's GPU", async () => {
    show(pc({}, ["transcribe"]));
    const note = await screen.findByTestId("machine-transcription");
    expect(note).toHaveTextContent("Transcription: ready on NVIDIA GeForce RTX 2080 SUPER");
  });

  it("ready on the CPU", async () => {
    show(pc({ capabilities: { gpus: [], stt_backend: "ct2-cpu" } }, ["transcribe"]));
    expect(await screen.findByTestId("machine-transcription")).toHaveTextContent(
      "Transcription: ready on the CPU (slow)"
    );
  });

  it("a machine from before the split waits for the second listen, and says how to let it transcribe", async () => {
    show(pc({}, [], { transcribe: WAITS }));
    const note = await screen.findByTestId("machine-transcription");
    expect(note).toHaveTextContent(/waits for the machines that run the second listen/);
    expect(note).toHaveTextContent(/Transcribe without the second listen/);
    expect(within(note).getByText(/has 8\.0 GB/)).toBeInTheDocument(); // the pipeline's own words
  });

  it("names what is missing", async () => {
    show(
      pc({ capabilities: { gpus: [] } }, [], {
        transcribe: "unavailable: faster-whisper is not installed (the stt extra)",
      })
    );
    const note = await screen.findByTestId("machine-transcription");
    expect(note).toHaveTextContent("Transcription: can't run here");
    expect(
      within(note).getByText("faster-whisper is not installed (the stt extra)")
    ).toBeInTheDocument();
  });

  it("installing with an update", async () => {
    show(
      pc(
        {
          capabilities: { gpus: [RTX] },
          update_state: { run: { run_id: "r1", version: "0.3.63", state: "staging" } },
        },
        [],
        { transcribe: "unavailable" }
      )
    );
    expect(await screen.findByTestId("machine-transcription")).toHaveTextContent(
      "Transcription: installing… (with the update to 0.3.63)"
    );
  });

  it("off for another reason: the share", async () => {
    show(pc({}, [], { transcribe: "storage" }));
    expect(await screen.findByTestId("machine-transcription")).toHaveTextContent(
      /Transcription: not on this machine/
    );
  });

  it("in Arabic", async () => {
    localStorage.setItem("mkt:locale", "ar");
    show(pc({}, ["transcribe"]));
    expect(await screen.findByTestId("machine-transcription")).toHaveTextContent(
      "التفريغ النصي: جاهز على NVIDIA GeForce RTX 2080 SUPER"
    );
  });
});

// The second listen is a stage of its own: the PC transcribes, and its 8 GB card runs the
// second listen too, taking turns with Whisper (capabilities.second_listen_gpu).
describe("Machines: the second-listen line", () => {
  beforeEach(() => {
    get.mockReset();
  });
  afterEach(() => {
    localStorage.removeItem("mkt:locale");
    document.documentElement.dir = "ltr";
  });

  const turns = { gpus: [RTX], stt_backend: "ct2-cuda", second_listen_gpu: "sequential" };

  it("the 8 GB PC: both stages, taking turns on the GPU", async () => {
    show(pc({ capabilities: turns }, ["transcribe", "second_listen"]));
    expect(await screen.findByTestId("machine-transcription")).toHaveTextContent(
      "Transcription: ready on NVIDIA GeForce RTX 2080 SUPER"
    );
    expect(await screen.findByTestId("machine-second-listen")).toHaveTextContent(
      /Second listen: runs here, taking turns with Whisper/
    );
  });

  it("a bigger card keeps both loaded", async () => {
    show(
      pc({ capabilities: { ...turns, second_listen_gpu: "together" } }, [
        "transcribe",
        "second_listen",
      ])
    );
    expect(await screen.findByTestId("machine-second-listen")).toHaveTextContent(
      "Second listen: runs here, beside Whisper on the same GPU"
    );
  });

  it("says why it does not run here, while the machine still transcribes", async () => {
    const why =
      "unavailable: the second listen does not run here (NVIDIA GeForce RTX 2060 has 6.0 GB:" +
      " the second listen needs a GPU of 7.5 GB or more here)";
    show(pc({}, ["transcribe"], { second_listen: why }));
    const note = await screen.findByTestId("machine-second-listen");
    expect(note).toHaveTextContent(/Second listen: doesn't run here\. This machine still transcribes/);
    expect(within(note).getByText(/has 6\.0 GB/)).toBeInTheDocument();
    expect(await screen.findByTestId("machine-transcription")).toHaveTextContent(
      /Transcription: ready/
    );
  });

  it("no line for a stage switched off", async () => {
    show(pc({}, ["transcribe"], { second_listen: "switched_off" }));
    await screen.findByTestId("machine-transcription");
    expect(screen.queryByTestId("machine-second-listen")).toBeNull();
  });

  it("in Arabic", async () => {
    localStorage.setItem("mkt:locale", "ar");
    show(pc({ capabilities: turns }, ["transcribe", "second_listen"]));
    expect(await screen.findByTestId("machine-second-listen")).toHaveTextContent(
      /الاستماع الثاني: يعمل هنا بالتناوب مع Whisper/
    );
  });
});
