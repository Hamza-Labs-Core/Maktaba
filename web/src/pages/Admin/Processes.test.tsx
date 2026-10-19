// Processes card (Story 22.9): renders each supervised process with its
// state chip, restarts and last crash reason, lists recent crashes, and
// says so when the API is not running under the supervisor.
import { describe, it, expect, vi, afterEach } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import { I18nProvider } from "../../lib/i18n";

const { get } = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get } };
});

import { ProcessesCard, type ProcessesDoc } from "./Processes";

const doc: ProcessesDoc = {
  available: true,
  processes: [
    { role: "api", state: "running", pid: 10, restarts: 0, uptime_sec: 7300 },
    {
      role: "streaming",
      state: "backoff",
      restarts: 3,
      last_exit: { code: 2, reason: "crash", uptime_sec: 4 },
      next_restart_at: "2026-10-18T10:01:00Z",
    },
    {
      role: "pipeline",
      state: "failed",
      restarts: 4,
      crash_loop: true,
      last_exit: { code: -1, reason: "hang", uptime_sec: 30 },
    },
  ],
  events: [
    { seq: 1, at: "2026-10-18T09:00:00Z", role: "streaming", kind: "started" },
    {
      seq: 2,
      at: "2026-10-18T09:10:00Z",
      role: "streaming",
      kind: "exited",
      reason: "crash",
      code: 2,
    },
    { seq: 3, at: "2026-10-18T09:20:00Z", role: "pipeline", kind: "exited", reason: "hang" },
    { seq: 4, at: "2026-10-18T09:30:00Z", role: "api", kind: "exited", reason: "shutdown" },
  ],
};

function renderCard() {
  return render(
    <I18nProvider>
      <ProcessesCard />
    </I18nProvider>
  );
}

describe("ProcessesCard", () => {
  afterEach(() => vi.clearAllMocks());

  it("shows each process with its state, restarts and last crash", async () => {
    get.mockResolvedValue(doc);
    renderCard();
    await waitFor(() => expect(screen.getByTestId("process-streaming")).toBeInTheDocument());
    expect(get).toHaveBeenCalledWith("/api/system/processes");

    const api = within(screen.getByTestId("process-api"));
    expect(api.getByText("Running")).toBeInTheDocument();
    expect(api.getByText("2h 1m")).toBeInTheDocument();

    const tv = within(screen.getByTestId("process-streaming"));
    expect(tv.getByText("Live TV and streaming")).toBeInTheDocument();
    expect(tv.getByText("Restarting")).toBeInTheDocument();
    expect(tv.getByText("3")).toBeInTheDocument();
    expect(tv.getByText("exited with code 2")).toBeInTheDocument();

    const pipe = within(screen.getByTestId("process-pipeline"));
    expect(pipe.getByText("Failed")).toBeInTheDocument();
    expect(pipe.getByText("stopped responding and was restarted")).toBeInTheDocument();
    expect(pipe.getByText(/Crash loop/)).toBeInTheDocument();

    // Recent crashes: the crash and the hang, not the shutdown.
    expect(screen.getByText("Recent crashes")).toBeInTheDocument();
    expect(screen.getByText(/Processing pipeline —/)).toBeInTheDocument();
    expect(screen.queryByText(/API —/)).not.toBeInTheDocument();
  });

  it("explains when the supervisor is not available", async () => {
    get.mockResolvedValue({ available: false, reason: "x", processes: [], events: [] });
    renderCard();
    await waitFor(() =>
      expect(screen.getByText(/Process supervision is not available/)).toBeInTheDocument()
    );
  });

  it("shows the section error when the request fails", async () => {
    get.mockRejectedValue(new Error("403"));
    renderCard();
    await waitFor(() =>
      expect(screen.getByText("Could not load this section.")).toBeInTheDocument()
    );
  });
});
