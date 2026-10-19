// "Scan now" on an empty library: a toast when the scan is queued or
// refused, progress while its job runs, and a reload when it is done.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, act, fireEvent } from "@testing-library/react";
import { MemoryRouter, Routes, Route } from "react-router-dom";

const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock("../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../lib/api")>("../lib/api");
  return { ...actual, api: { ...actual.api, get, post } };
});
vi.mock("../lib/auth", () => ({
  useAuth: () => ({ user: { id: "u1", username: "root", is_admin: true } }),
}));

import { LibraryBrowser, SCAN_POLL_MS } from "./LibraryBrowser";
import { I18nProvider } from "../lib/i18n";
import { ToastProvider } from "@ds/components/Toast/Toast";
import { ApiError } from "../lib/api";
import en from "../i18n/en.json";

const LIB = "11111111-1111-1111-1111-111111111111";

function renderLib() {
  return render(
    <MemoryRouter initialEntries={[`/library/${LIB}`]}>
      <I18nProvider>
        <ToastProvider>
          <Routes>
            <Route path="/library/:libraryId" element={<LibraryBrowser />} />
          </Routes>
        </ToastProvider>
      </I18nProvider>
    </MemoryRouter>
  );
}

async function tick(ms: number) {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(ms);
  });
}

describe("LibraryBrowser Scan now", () => {
  let jobState: string;
  let videos: unknown[];
  beforeEach(() => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    get.mockReset();
    post.mockReset();
    jobState = "pending";
    videos = [];
    get.mockImplementation((url: string) => {
      if (url.includes("/api/channels/now")) return Promise.resolve({ server_time: "", items: [] });
      if (url.startsWith("/api/jobs/")) {
        return Promise.resolve({ id: "j1", state: jobState, estimated_remaining_sec: 120 });
      }
      return Promise.resolve({ items: videos, next: null });
    });
  });
  afterEach(() => vi.useRealTimers());

  it("toasts, shows progress, and reloads the list when the scan is done", async () => {
    post.mockResolvedValue({ job_id: "j1" });
    renderLib();
    fireEvent.click(await screen.findByRole("button", { name: en["library.scanNow"] }));
    expect(post).toHaveBeenCalledWith(`/api/libraries/${LIB}/scan`);
    expect(await screen.findByText(en["library.scan.started"])).toBeInTheDocument();
    expect(screen.getByRole("progressbar")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: en["library.scan.running"] })).toBeDisabled();

    jobState = "running";
    await tick(SCAN_POLL_MS);
    expect(
      await screen.findByText(/Looking for new and changed files · about 2 min left/)
    ).toBeInTheDocument();
    expect(get).toHaveBeenCalledWith("/api/jobs/j1");

    jobState = "done";
    videos = [{ id: "v1", title: "Found it", filename: "a.mp4", state: "ready" }];
    await tick(SCAN_POLL_MS);
    expect(await screen.findByText(en["library.scan.done"])).toBeInTheDocument();
    expect(await screen.findByText("Found it")).toBeInTheDocument();
  });

  it("toasts a failed scan with its error", async () => {
    post.mockResolvedValue({ job_id: "j1" });
    get.mockImplementation((url: string) => {
      if (url.includes("/api/channels/now")) return Promise.resolve({ server_time: "", items: [] });
      if (url.startsWith("/api/jobs/")) {
        return Promise.resolve({ id: "j1", state: "failed", error: "share offline" });
      }
      return Promise.resolve({ items: [], next: null });
    });
    renderLib();
    fireEvent.click(await screen.findByRole("button", { name: en["library.scanNow"] }));
    await tick(SCAN_POLL_MS);
    expect(
      await screen.findByText(`${en["library.scan.failed"]}: share offline`)
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: en["library.scanNow"] })).toBeEnabled();
  });

  it("toasts the error when the scan cannot start", async () => {
    post.mockRejectedValue(
      new ApiError({ type: "about:blank", title: "Internal", status: 500, detail: "no queue" })
    );
    renderLib();
    fireEvent.click(await screen.findByRole("button", { name: en["library.scanNow"] }));
    expect(await screen.findByText(`${en["library.scan.error"]}: no queue`)).toBeInTheDocument();
    expect(screen.queryByRole("progressbar")).toBeNull();
  });
});
