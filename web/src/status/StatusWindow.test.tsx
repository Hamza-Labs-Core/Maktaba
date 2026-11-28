// Story 13.13: the server's status in its own window (status/StatusWindow.tsx).
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const io = vi.hoisted(() => ({
  state: vi.fn(),
  action: vi.fn(async () => {}),
  listen: vi.fn(),
}));
vi.mock("../lib/statusWindow", async (orig) => ({
  ...(await orig<typeof import("../lib/statusWindow")>()),
  statusWindowState: io.state,
  statusWindowAction: io.action,
  onStatusWindow: io.listen,
}));

import { StatusWindow } from "./StatusWindow";
import { I18nProvider } from "../lib/i18n";
import type { StatusWindowView } from "../lib/statusWindow";

const RUNNING: StatusWindowView = {
  status: { kind: "running" },
  host: "Haddad-PC",
  version: "0.3.112",
  since: "2026-11-23T08:14:00Z",
  parts: [
    { id: "database", tone: "ok", running: 1 },
    { id: "server", tone: "ok", running: 1 },
    { id: "playback", tone: "ok", running: 2 },
    {
      id: "indexing",
      tone: "busy",
      running: 0,
      detail: "Installing (40%)",
      detail_code: "venv_building",
      detail_params: { percent: 40 },
      progress: 0.4,
    },
    {
      id: "model",
      tone: "busy",
      running: 1,
      model: "qwen2.5:7b",
      bytes: [2 ** 30, 4 * 2 ** 30],
      progress: 0.25,
    },
  ],
  update: { version: "0.3.113", by: "03:00", installing: false, install: false },
};

let push: (v: StatusWindowView) => void = () => {};

function show(view: StatusWindowView | null, lang: "en" | "ar" = "en") {
  localStorage.setItem("mkt:locale", lang);
  io.state.mockResolvedValue(view);
  return render(
    <I18nProvider>
      <StatusWindow />
    </I18nProvider>
  );
}

describe("StatusWindow", () => {
  beforeEach(() => {
    io.listen.mockImplementation(async (cb: (v: StatusWindowView) => void) => {
      push = cb;
      return () => {};
    });
  });
  afterEach(() => {
    localStorage.clear();
    vi.clearAllMocks();
  });

  it("says the server's state, this machine, the release and the parts", async () => {
    show(RUNNING);
    expect(await screen.findByRole("heading", { name: "Maktaba is running" })).toBeInTheDocument();
    expect(
      screen.getByText(/On .*Haddad-PC.* · version .*0\.3\.112.* · since/)
    ).toBeInTheDocument();
    const tiles = screen.getByRole("list", { name: "The server's parts" });
    const items = within(tiles).getAllByRole("listitem");
    expect(items.map((i) => i.dataset.part)).toEqual([
      "database",
      "server",
      "playback",
      "indexing",
      "model",
    ]);
    expect(within(items[0]).getByText("Library database")).toBeInTheDocument();
    expect(within(items[0]).getByText("Running")).toBeInTheDocument();
    expect(within(items[0]).getByText("Private to Maktaba")).toBeInTheDocument();
    // a part on its way says what it waits on, in the window's words, with its progress
    expect(within(items[3]).getByText("Starting")).toBeInTheDocument();
    expect(
      within(items[3]).getByText("Installing the transcription tools (40%).")
    ).toBeInTheDocument();
    expect(within(items[3]).getByRole("progressbar")).toHaveAttribute("aria-valuenow", "40");
    // the language model's download
    expect(within(items[4]).getByText("Downloading")).toBeInTheDocument();
    expect(
      within(items[4]).getByText(/qwen2\.5:7b.* · .*1\.0 GB.* of .*4\.0 GB/)
    ).toBeInTheDocument();
    expect(within(items[4]).getByRole("progressbar")).toHaveAttribute("aria-valuenow", "25");
    expect(screen.getByText("Closing this window keeps the server running.")).toBeInTheDocument();
  });

  it("says the release on its way: by itself, or with a button", async () => {
    const { unmount } = show(RUNNING);
    const banner = await screen.findByRole("status");
    expect(banner).toHaveTextContent(/Maktaba .*0\.3\.113.* is ready\./);
    expect(banner).toHaveTextContent(/It installs and restarts by .*03:00/);
    expect(within(banner).queryByRole("button")).toBeNull();
    unmount();

    show({ ...RUNNING, update: { version: "0.3.113", by: "", installing: false, install: true } });
    await userEvent.click(await screen.findByRole("button", { name: "Install now" }));
    expect(io.action).toHaveBeenCalledWith("update");
  });

  it("does the tray's server actions, and opens the main window's pages", async () => {
    show(RUNNING);
    await userEvent.click(await screen.findByRole("button", { name: "Pause server" }));
    expect(io.action).toHaveBeenLastCalledWith("pause");
    await userEvent.click(screen.getByRole("button", { name: "Open Maktaba" }));
    expect(io.action).toHaveBeenLastCalledWith("open");
    await userEvent.click(screen.getByRole("button", { name: "Report a problem…" }));
    expect(io.action).toHaveBeenLastCalledWith("report");
    await userEvent.click(screen.getByRole("button", { name: "Open logs" }));
    expect(io.action).toHaveBeenLastCalledWith("logs");
    const rail = screen.getByRole("navigation", { name: "Server" });
    expect(within(rail).getByRole("button", { name: "Status" })).toHaveAttribute(
      "aria-current",
      "page"
    );
    for (const [name, action] of [
      ["Libraries", "libraries"],
      ["Devices", "devices"],
      ["Logs", "logs"],
      ["Settings", "settings"],
    ]) {
      await userEvent.click(within(rail).getByRole("button", { name }));
      expect(io.action).toHaveBeenLastCalledWith(action);
    }
  });

  it("offers Resume for a paused server, and follows each new status", async () => {
    show({ ...RUNNING, status: { kind: "paused" }, update: undefined });
    await userEvent.click(await screen.findByRole("button", { name: "Resume server" }));
    expect(io.action).toHaveBeenLastCalledWith("resume");
    act(() =>
      push({
        ...RUNNING,
        status: { kind: "problem", roles: ["streaming", "tv-slot", "pipeline", "log-reader"] },
      })
    );
    expect(
      await screen.findByRole("heading", { name: "Maktaba has a problem" })
    ).toBeInTheDocument();
    expect(screen.getByRole("alert")).toHaveTextContent(
      "These keep stopping: Playback and Live TV, Indexing, transcription and search, Another part of the server."
    );
    act(() =>
      push({
        ...RUNNING,
        status: { kind: "not_answering", reason: "the server does not answer" },
        parts: [],
      })
    );
    expect(await screen.findByRole("alert")).toHaveTextContent("The server does not answer.");
    expect(
      screen.getByText("The server does not answer yet: its parts show here once it does.")
    ).toBeInTheDocument();
  });

  it("names what keeps stopping in Arabic too, never by its role", async () => {
    show({ ...RUNNING, status: { kind: "problem", roles: ["streaming", "pipeline"] } }, "ar");
    const why = await screen.findByRole("alert");
    expect(why).toHaveTextContent(
      "هذه تتوقف مرارًا: التشغيل والبث المباشر، الفهرسة والتفريغ والبحث."
    );
    expect(why.textContent).not.toMatch(/[a-z]/);
  });

  it("speaks Arabic, right to left", async () => {
    show(RUNNING, "ar");
    expect(await screen.findByRole("heading", { name: "مكتبة تعمل" })).toBeInTheDocument();
    expect(document.documentElement.dir).toBe("rtl");
    expect(screen.getByRole("button", { name: "إيقاف الخادم مؤقتًا" })).toBeInTheDocument();
    expect(screen.getByText("قاعدة بيانات المكتبة")).toBeInTheDocument();
  });

  it("shows its frame and rail before the shell answers", async () => {
    io.state.mockReturnValue(new Promise(() => {}));
    render(
      <I18nProvider>
        <StatusWindow />
      </I18nProvider>
    );
    expect(screen.getByTestId("status-window")).toBeInTheDocument();
    expect(screen.getByRole("navigation", { name: "Server" })).toBeInTheDocument();
    expect(document.title).toBe("Maktaba status");
  });
});
