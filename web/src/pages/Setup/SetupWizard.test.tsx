// Epic 36, story 36.6: the Mac app's first-run setup screens, driven by a
// fake SetupApi that answers like `maktaba-server setup --json` would
// (specs/epics/36-mac-app/contract-setup.md).
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "../../lib/i18n";
import {
  NO_ANSWER,
  type AppState,
  type SetupApi,
  type StepError,
  type StepName,
  type StepProgress,
} from "../../lib/desktopSetup";
import { PLEX_POLL_MS, SetupWizard, parsePathMap, type Screen } from "./SetupWizard";
import { STARTING_POLL_MS, ServerStarting } from "./ServerStarting";
import { DesktopGate } from "../../components/DesktopGate";

const CHECK = {
  platform: "darwin",
  arch: "arm64",
  ram_bytes: 96 * 2 ** 30,
  disk_free_bytes: 800 * 2 ** 30,
  data_dir: "/Users/me/Library/Application Support/Maktaba",
  need: { ram_bytes: 16 * 2 ** 30, disk_bytes: 20 * 2 ** 30 },
  ok: true,
  problems: [],
  warnings: [],
  runs: [
    { role: "postgres", what: "The library database" },
    { role: "pipeline", what: "Indexing, transcription and search" },
  ],
};

type Handler = (input: Record<string, unknown>, onProgress?: (p: StepProgress) => void) => unknown;

function stepError(code: string, message: string, field?: string): StepError {
  return { code, message, field, details: [] };
}

function fakeApi(steps: Partial<Record<StepName, Handler>> = {}, state: Partial<AppState> = {}) {
  const runStep = vi.fn(
    async (
      step: StepName,
      input: Record<string, unknown>,
      onProgress?: (p: StepProgress) => void
    ) => {
      const h = steps[step];
      if (!h) return {};
      return h(input, onProgress);
    }
  );
  const api = {
    appState: vi.fn(
      async () =>
        ({
          target: "setup",
          server_available: true,
          login_item: "not_registered",
          ...state,
        }) as AppState
    ),
    runStep,
    finishSetup: vi.fn(async () => "enabled" as const),
    openLoginItems: vi.fn(async () => {}),
    serverStatus: vi.fn(async () => ({ kind: "starting" as const })),
    openLocalServer: vi.fn(async () => {}),
    useRemoteServer: vi.fn(async () => {}),
    openExternal: vi.fn(async () => {}),
  };
  return api as typeof api & SetupApi;
}

function renderWizard(api: SetupApi, initial?: Screen) {
  return render(
    <I18nProvider>
      <SetupWizard api={api} initial={initial} />
    </I18nProvider>
  );
}

describe("SetupWizard", () => {
  beforeEach(() => localStorage.clear());

  it("offers this Mac or another Maktaba, and connects to another one", async () => {
    const api = fakeApi();
    renderWizard(api);
    expect(screen.getByRole("heading", { name: "Welcome to Maktaba" })).toBeInTheDocument();
    await userEvent.type(screen.getByLabelText(/Its address/), "https://maktaba.home");
    await userEvent.click(screen.getByRole("button", { name: "Connect" }));
    expect(api.useRemoteServer).toHaveBeenCalledWith("https://maktaba.home");
  });

  it("hides 'this Mac' in a build without the server", async () => {
    renderWizard(fakeApi({}, { server_available: false }));
    await waitFor(() =>
      expect(screen.queryByRole("button", { name: "Set up this Mac" })).not.toBeInTheDocument()
    );
  });

  it("shows what will run and the RAM/disk check", async () => {
    const api = fakeApi({ check: () => CHECK });
    renderWizard(api);
    await userEvent.click(screen.getByRole("button", { name: "Set up this Mac" }));
    expect(await screen.findByText("Indexing, transcription and search")).toBeInTheDocument();
    expect(screen.getByText("96.0 GB (needs 16.0 GB)")).toBeInTheDocument();
    expect(screen.getByText("Step 1 of 6")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Next" })).toBeEnabled();
  });

  it("blocks Next when the check fails, each finding in the app's words", async () => {
    renderWizard(
      fakeApi({
        check: () => ({
          ...CHECK,
          ok: false,
          problems: [
            {
              code: "disk_low",
              params: { free_bytes: 5 * 2 ** 30, need_bytes: 20 * 2 ** 30, dir: "/data" },
              message: "en",
            },
            { code: "brand_new", message: "A finding this app has no text for." },
          ],
          warnings: [{ code: "transcribe_cpu", message: "No NVIDIA GPU" }],
        }),
      }),
      "check"
    );
    expect(
      await screen.findByText(
        "Only 5.0 GB free on the disk of /data: Maktaba needs 20.0 GB to start."
      )
    ).toBeInTheDocument();
    expect(screen.getByText("A finding this app has no text for.")).toBeInTheDocument();
    expect(screen.getByText(/this Mac transcribes on its processor/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Next" })).toBeDisabled();
  });

  it("says what will run in the app's words, Arabic too", async () => {
    localStorage.setItem("mkt:locale", "ar");
    const runs = [
      { role: "api", what: "en" },
      { role: "pipeline", what: "en", params: { workers: 2, gpu: "RTX 2080" } },
    ];
    renderWizard(fakeApi({ check: () => ({ ...CHECK, runs }) }), "check");
    expect(await screen.findByText("الخادم الذي يتصل به التطبيق وأجهزتك")).toBeInTheDocument();
    expect(
      screen.getByText("الفهرسة والتفريغ (2 عمّال) والبحث، والتفريغ على RTX 2080")
    ).toBeInTheDocument();
    document.documentElement.dir = "ltr";
  });

  it("admin: a password typo never reaches the server; a weak password lands on its field", async () => {
    const admin = vi.fn((input: Record<string, unknown>) => {
      if (input.password === "short")
        throw {
          ...stepError("password_short", "Use at least 12 characters", "password"),
          params: { min: 12 },
        };
      return { username: input.username };
    });
    const api = fakeApi({ admin });
    renderWizard(api, "admin");
    const [pw, again] = screen.getAllByLabelText(/Password/);
    await userEvent.type(pw, "short");
    await userEvent.type(again, "shorT");
    await userEvent.click(screen.getByRole("button", { name: "Next" }));
    expect(screen.getByText("The two passwords are not the same.")).toBeInTheDocument();
    expect(admin).not.toHaveBeenCalled();

    await userEvent.clear(again);
    await userEvent.type(again, "short");
    await userEvent.click(screen.getByRole("button", { name: "Next" }));
    expect(await screen.findByText("Use at least 12 characters.")).toBeInTheDocument();
    expect(admin).toHaveBeenCalledWith(
      { username: "admin", password: "short" },
      expect.any(Function)
    );

    await userEvent.clear(pw);
    await userEvent.type(pw, "a long passphrase");
    await userEvent.clear(again);
    await userEvent.type(again, "a long passphrase");
    await userEvent.click(screen.getByRole("button", { name: "Next" }));
    expect(
      await screen.findByRole("heading", { name: "Your library folders" })
    ).toBeInTheDocument();
  });

  it("libraries: sends the folders and the path map", async () => {
    const libraries = vi.fn(() => ({ roots: [] }));
    renderWizard(fakeApi({ libraries }), "libraries");
    await userEvent.type(
      screen.getByLabelText("Folders (one per line)"),
      "/Volumes/Media/Lectures\n\n/Users/me/Videos"
    );
    await userEvent.type(
      screen.getByLabelText("NAS path map (optional)"),
      "/mnt/media => /Volumes/Media"
    );
    await userEvent.click(screen.getByRole("button", { name: "Next" }));
    await waitFor(() =>
      expect(libraries).toHaveBeenCalledWith(
        {
          roots: ["/Volumes/Media/Lectures", "/Users/me/Videos"],
          path_map: [{ from: "/mnt/media", to: "/Volumes/Media" }],
        },
        expect.any(Function)
      )
    );
    expect(
      await screen.findByRole("heading", { name: "Transcription models" })
    ).toBeInTheDocument();
  });

  it("libraries: a bad path-map line is caught before the step", async () => {
    const libraries = vi.fn(() => ({}));
    renderWizard(fakeApi({ libraries }), "libraries");
    await userEvent.type(screen.getByLabelText("Folders (one per line)"), "/a");
    await userEvent.type(screen.getByLabelText("NAS path map (optional)"), "/mnt/media");
    await userEvent.click(screen.getByRole("button", { name: "Next" }));
    expect(screen.getByRole("alert")).toHaveTextContent("their path => the path here");
    expect(libraries).not.toHaveBeenCalled();
  });

  it("models: the token goes to the step, download progress shows, a gated model explains itself", async () => {
    let release: () => void = () => {};
    const models = vi.fn(
      (input: Record<string, unknown>, onProgress?: (p: StepProgress) => void) =>
        new Promise((resolve, reject) => {
          onProgress?.({
            stage: "download",
            message: "Downloading turbo",
            done: 512 * 2 ** 20,
            total: 2 ** 30,
          });
          release = () =>
            input.hf_token === "hf_good"
              ? resolve({ models: [] })
              : reject(stepError("hf_gated", "cohere is gated", "hf_token"));
        })
    );
    const api = fakeApi({ models });
    renderWizard(api, "models");
    await userEvent.click(screen.getByRole("button", { name: "Get a token" }));
    expect(api.openExternal).toHaveBeenCalledWith("https://huggingface.co/settings/tokens");

    await userEvent.type(screen.getByLabelText(/Hugging Face token/), "hf_bad");
    await userEvent.click(screen.getByRole("button", { name: "Next" }));
    expect(await screen.findByText("Downloading turbo (512.0 MB / 1.0 GB)")).toBeInTheDocument();
    expect(screen.getByRole("progressbar", { name: "Downloading models" })).toHaveAttribute(
      "aria-valuenow",
      "50"
    );
    await act(async () => release());
    // the field says it in the app's words (the step's English is for a code it does not know)
    expect(
      await screen.findByText("Accept the model's terms on huggingface.co with this account first.")
    ).toBeInTheDocument();

    await userEvent.clear(screen.getByLabelText(/Hugging Face token/));
    await userEvent.type(screen.getByLabelText(/Hugging Face token/), "hf_good");
    await userEvent.click(screen.getByRole("button", { name: "Next" }));
    await act(async () => release());
    expect(await screen.findByRole("heading", { name: "Plex (optional)" })).toBeInTheDocument();
    expect(models).toHaveBeenLastCalledWith(
      { source: "huggingface", hf_token: "hf_good" },
      expect.any(Function)
    );
  });

  it("models: the NAS copy sends its path", async () => {
    const models = vi.fn(() => ({ models: [] }));
    renderWizard(fakeApi({ models }), "models");
    await userEvent.click(screen.getByLabelText("Use the copy on the NAS"));
    await userEvent.type(screen.getByLabelText(/Folder with the models/), "/Volumes/Models");
    await userEvent.click(screen.getByRole("button", { name: "Next" }));
    await waitFor(() =>
      expect(models).toHaveBeenCalledWith(
        { source: "nas", path: "/Volumes/Models" },
        expect.any(Function)
      )
    );
  });

  describe("plex", () => {
    beforeEach(() => vi.useFakeTimers({ shouldAdvanceTime: true }));
    afterEach(() => vi.useRealTimers());

    it("can be skipped", async () => {
      const plex = vi.fn(() => ({ linked: false }));
      renderWizard(fakeApi({ plex, venv: () => new Promise(() => {}) }), "plex");
      await userEvent.click(screen.getByRole("button", { name: "Skip" }));
      expect(plex).toHaveBeenCalledWith({ action: "skip" }, expect.any(Function));
      expect(
        await screen.findByRole("heading", { name: "Getting Maktaba ready" })
      ).toBeInTheDocument();
    });

    it("shows the PIN, opens plex.tv/link and moves on once linked", async () => {
      let polls = 0;
      const plex = vi.fn((input: Record<string, unknown>) => {
        if (input.action === "pin")
          return { pin_id: 7, code: "ABCD", link_url: "https://plex.tv/link" };
        polls++;
        return { linked: polls >= 2 };
      });
      const api = fakeApi({ plex, venv: () => new Promise(() => {}) });
      renderWizard(api, "plex");
      await userEvent.click(screen.getByRole("button", { name: "Sign in with Plex" }));
      expect(await screen.findByText("ABCD")).toBeInTheDocument();
      expect(api.openExternal).toHaveBeenCalledWith("https://plex.tv/link");
      await act(async () => vi.advanceTimersByTime(PLEX_POLL_MS));
      await act(async () => vi.advanceTimersByTime(PLEX_POLL_MS));
      expect(
        await screen.findByRole("heading", { name: "Getting Maktaba ready" })
      ).toBeInTheDocument();
      expect(plex).toHaveBeenCalledWith({ action: "poll", pin_id: 7 }, undefined);
    });
  });

  it("install: builds the venv, downloads the models, finishes, registers the login item, then waits for the server", async () => {
    const order: string[] = [];
    const venv = vi.fn((_: Record<string, unknown>, onProgress?: (p: StepProgress) => void) => {
      order.push("venv");
      onProgress?.({ stage: "venv", message: "Installing mlx", done: 3, total: 10 });
      return { reused: false };
    });
    const models = vi.fn(
      (input: Record<string, unknown>, onProgress?: (p: StepProgress) => void) => {
        order.push(`models:${String(input.action)}`);
        onProgress?.({ stage: "download", message: "Downloading turbo", done: 1, total: 4 });
        return { models: { whisper: "ready" } };
      }
    );
    const llm = vi.fn(() => {
      order.push("llm");
      return { models: [{ name: "qwen2.5:7b", ready: true }] };
    });
    const finish = vi.fn(() => {
      order.push("finish");
      return { web_url: "http://127.0.0.1:8088", supervisor_url: "http://127.0.0.1:9103" };
    });
    const api = fakeApi({ venv, models, llm, finish });
    renderWizard(api, "install");
    expect(await screen.findByRole("heading", { name: "Starting Maktaba" })).toBeInTheDocument();
    expect(order).toEqual(["venv", "models:download", "llm", "finish"]);
    expect(api.finishSetup).toHaveBeenCalledWith("http://127.0.0.1:8088", "http://127.0.0.1:9103");
  });

  it("install: a failed models download shows why and can be retried", async () => {
    let fail = true;
    const models = vi.fn(() => {
      if (fail) throw stepError("hf_gated", "Accept the terms first.");
      return {};
    });
    const finish = vi.fn(() => ({ web_url: "http://127.0.0.1:8088" }));
    renderWizard(fakeApi({ models, finish }), "install");
    expect(await screen.findByRole("alert")).toHaveTextContent("Accept the model's terms");
    expect(finish).not.toHaveBeenCalled();
    fail = false;
    await userEvent.click(screen.getByRole("button", { name: "Try again" }));
    expect(await screen.findByRole("heading", { name: "Starting Maktaba" })).toBeInTheDocument();
  });

  it("on Windows: the words are a PC's, and a turned-off sign-in task is turned on from the app", async () => {
    const api = fakeApi(
      { finish: () => ({ web_url: "http://127.0.0.1:8088" }) },
      { platform: "windows" }
    );
    const { unmount } = renderWizard(api);
    expect(await screen.findByRole("button", { name: "Set up this PC" })).toBeInTheDocument();
    expect(screen.getByText(/run your library on this PC/)).toBeInTheDocument();
    unmount();

    api.finishSetup.mockResolvedValue("requires_approval" as never);
    renderWizard(api, "install");
    expect(await screen.findByText(/Task Scheduler/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "I turned it on" })).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Turn it on" }));
    expect(api.openLoginItems).toHaveBeenCalled();
    expect(await screen.findByRole("heading", { name: "Starting Maktaba" })).toBeInTheDocument();
    expect(screen.getByText(/Maktaba runs in the tray \(beside the clock\)/)).toBeInTheDocument();
  });

  describe("moving the Maktaba a PC runs into the app (Epic 38 story 38.13)", () => {
    const THIS_PC = {
      id: String.raw`\Maktaba node (maktaba-local)`,
      kind: "script",
      task: String.raw`\Maktaba node (maktaba-local)`,
      task_enabled: true,
      dir: String.raw`E:\tv\maktaba-local`,
      config_found: true,
      db: "node",
      home: "Mahmouds-Mac-Studio",
      ports: { web: "0.0.0.0:8780", api: "127.0.0.1:8781", stream: "0.0.0.0:8782" },
      pids: [100, 101, 102],
    };
    const PLAN = {
      source: THIS_PC,
      items: [
        {
          code: "key",
          params: { name: "stream-signing.key" },
          what: "stream-signing.key",
          from: "a",
          to: "b",
          size: 1704,
          done: false,
        },
        { code: "node_config", what: "node config", from: "c", to: "d", size: 587, done: false },
      ],
      carried: [
        { code: "section", params: { name: "[server]" }, message: "[server]" },
        { code: "section", params: { name: "[media]" }, message: "[media]" },
        { code: "no_mdns", message: 'en: --no-mdns as [server] mdns = "off"' },
      ],
      not_carried: [
        { code: "env", params: { name: "MAKTABA_FANCY" }, message: "en: MAKTABA_FANCY" },
      ],
      problems: [],
      warnings: [],
      need: 2291,
      free: 10 ** 12,
    };
    const URLS = { web_url: "http://127.0.0.1:8780", supervisor_url: "http://127.0.0.1:9103" };

    function adoptStep(
      plan: unknown = PLAN,
      move: Handler = () => URLS,
      find: Handler = () => ({ sources: [THIS_PC] })
    ) {
      return vi.fn((input: Record<string, unknown>, onProgress?: (p: StepProgress) => void) => {
        if (input.action === "find") return find(input, onProgress);
        if (input.action === "plan") return plan;
        if (input.action === "undo") return { move: { stage: "undone", source: THIS_PC } };
        return move(input, onProgress);
      });
    }

    it("offers the move first, shows its plan, moves, downloads the models and starts the server", async () => {
      const order: string[] = [];
      let seen = "";
      const adopt = adoptStep(PLAN, async (_, onProgress) => {
        order.push("move");
        onProgress?.({ stage: "stop", message: "en: Stopping the old install" });
        // the line under the bar, while the move runs: the app's own words
        await waitFor(() => {
          seen = screen.getByText("Stopping the old install").textContent ?? "";
        });
        expect(screen.queryByText(/en: Stopping/)).not.toBeInTheDocument();
        return URLS;
      });
      const models = vi.fn((input: Record<string, unknown>) => {
        order.push(`models:${String(input.action)}`);
        return {};
      });
      const api = fakeApi({ adopt, models }, { platform: "windows" });
      renderWizard(api);
      expect(
        await screen.findByText("Move the Maktaba this PC runs into the app")
      ).toBeInTheDocument();
      expect(screen.getByRole("button", { name: "Start fresh" })).toBeInTheDocument();
      await userEvent.click(screen.getByRole("button", { name: "Move it" }));

      expect(await screen.findByText("A node of the home Mahmouds-Mac-Studio")).toBeInTheDocument();
      expect(screen.getByText(String.raw`E:\tv\maktaba-local`)).toBeInTheDocument();
      expect(
        screen.getByText("web 0.0.0.0:8780, api 127.0.0.1:8781, stream 0.0.0.0:8782")
      ).toBeInTheDocument();
      expect(screen.getByText(/Its key stream-signing\.key/)).toBeInTheDocument();
      expect(
        screen.getByText(/Its node config \(the home's address and the node's own key\)/)
      ).toBeInTheDocument();
      expect(screen.getByText("Copied into the app (2.2 KB)")).toBeInTheDocument();
      expect(
        screen.getByText(
          'Settings carried: [server], [media], no announcing on the network (--no-mdns, as [server] mdns = "off")'
        )
      ).toBeInTheDocument();
      expect(
        screen.getByText("Not carried: MAKTABA_FANCY from its script (no server.toml setting)")
      ).toBeInTheDocument();
      expect(screen.queryByText(/en:/)).not.toBeInTheDocument();
      await userEvent.click(screen.getByRole("button", { name: "Move" }));

      expect(await screen.findByRole("heading", { name: "Starting Maktaba" })).toBeInTheDocument();
      expect(order).toEqual(["move", "models:download"]);
      // the step's English line became the app's own
      expect(seen).toBe("Stopping the old install");
      expect(adopt).toHaveBeenCalledWith(
        { action: "move", source: THIS_PC.id },
        expect.any(Function)
      );
      expect(api.finishSetup).toHaveBeenCalledWith(URLS.web_url, URLS.supervisor_url);
    });

    it("is not looked for on a Mac; Start fresh is the usual setup beside a home", async () => {
      const adopt = adoptStep();
      const { unmount } = renderWizard(fakeApi({ adopt }));
      expect(await screen.findByRole("button", { name: "Set up this Mac" })).toBeInTheDocument();
      expect(adopt).not.toHaveBeenCalled();
      unmount();

      const home = { ...THIS_PC, db: "sqlite", home: undefined };
      const besideAHome = adoptStep(PLAN, undefined, () => ({ sources: [home] }));
      renderWizard(fakeApi({ adopt: besideAHome, check: () => CHECK }, { platform: "windows" }));
      await userEvent.click(await screen.findByRole("button", { name: "Start fresh" }));
      expect(await screen.findByRole("heading", { name: "What will run" })).toBeInTheDocument();
    });

    it("beside a node of a home Start fresh is not offered: the app would be its second copy", async () => {
      renderWizard(fakeApi({ adopt: adoptStep() }, { platform: "windows" }));
      expect(
        await screen.findByText(
          "this PC runs a node of the home Mahmouds-Mac-Studio outside the app: a fresh setup would be a second copy of that node. Move it into the app instead."
        )
      ).toBeInTheDocument();
      expect(screen.getByRole("button", { name: "Start fresh" })).toBeDisabled();
      expect(screen.getByRole("button", { name: "Move it" })).toBeEnabled();
    });

    it("a find that fails is said, with Try again, and does not fall through to a fresh setup", async () => {
      let fails = true;
      const adopt = adoptStep(PLAN, undefined, () => {
        if (fails) throw stepError("adopt_find", "en: schtasks failed");
        return { sources: [] };
      });
      renderWizard(fakeApi({ adopt, check: () => CHECK }, { platform: "windows" }));
      expect(
        await screen.findByText("Maktaba could not look at what this PC runs.")
      ).toBeInTheDocument();
      expect(screen.getByRole("button", { name: "Set up this PC" })).toBeDisabled();
      fails = false;
      await userEvent.click(screen.getByRole("button", { name: "Try again" }));
      await waitFor(() =>
        expect(screen.getByRole("button", { name: "Set up this PC" })).toBeEnabled()
      );
      expect(adopt).toHaveBeenCalledTimes(2);
    });

    it("a move recorded part way is offered to go on, or to restore the old install", async () => {
      let stage = "stopped";
      const adopt = adoptStep(PLAN, undefined, () => ({
        sources: [{ ...THIS_PC, task_enabled: false }],
        move: { stage, source: THIS_PC },
      }));
      adopt.mockImplementationOnce(() => ({
        sources: [{ ...THIS_PC, task_enabled: false }],
        move: { stage, source: THIS_PC },
      }));
      renderWizard(fakeApi({ adopt }, { platform: "windows" }));
      expect(await screen.findByText("A move into the app stopped part way")).toBeInTheDocument();
      expect(screen.getByRole("button", { name: "Go on with the move" })).toBeInTheDocument();
      stage = "undone";
      await userEvent.click(screen.getByRole("button", { name: "Restore the old install" }));
      expect(adopt).toHaveBeenCalledWith({ action: "undo" }, undefined);
      expect(
        await screen.findByText("The old install runs again, as before the move.")
      ).toBeInTheDocument();
      // looked again: the old install is offered to move, as before
      expect(await screen.findByRole("button", { name: "Move it" })).toBeInTheDocument();
      expect(screen.queryByText("A move into the app stopped part way")).not.toBeInTheDocument();
    });

    it("a problem in the plan stops it before anything changes, each finding in the app's words", async () => {
      const where = "127.0.0.1:5432";
      const adopt = adoptStep({
        ...PLAN,
        source: { ...THIS_PC, db: "own-postgres", db_where: where },
        problems: [{ code: "postgres_down", params: { where }, message: "en: not running" }],
        warnings: [
          { code: "postgres_stays", params: { where }, message: "en: stays" },
          { code: "brand_new", message: "A finding this app has no text for." },
        ],
      });
      renderWizard(fakeApi({ adopt }, { platform: "windows" }));
      await userEvent.click(await screen.findByRole("button", { name: "Move it" }));
      expect(await screen.findByRole("alert")).toHaveTextContent(
        "Its library database, the Postgres at 127.0.0.1:5432, is not running: the move needs it running."
      );
      expect(
        screen.getByText(
          "The library database stays in the Postgres at 127.0.0.1:5432 on this PC, which keeps running as it does now."
        )
      ).toBeInTheDocument();
      expect(screen.getByText("A finding this app has no text for.")).toBeInTheDocument();
      expect(screen.queryByText(/^en:/)).not.toBeInTheDocument();
      expect(screen.getByRole("button", { name: "Move" })).toBeDisabled();
    });

    it("a move that stopped part way says why in the app's words and goes on when tried again", async () => {
      let fail = true;
      const english = "port 8782 is still in use after the old install stopped";
      const move = vi.fn((): unknown => {
        if (fail)
          throw {
            code: "adopt_port_busy",
            params: { port: "8782" },
            message: english,
            details: ["adopt.log: stop: Stopping the old install"],
          } satisfies StepError;
        return URLS;
      });
      const api = fakeApi({ adopt: adoptStep(PLAN, move) }, { platform: "windows" });
      renderWizard(api);
      await userEvent.click(await screen.findByRole("button", { name: "Move it" }));
      await userEvent.click(await screen.findByRole("button", { name: "Move" }));
      const alert = await screen.findByRole("alert");
      // the app's text with the port, alone; the step's English under Details
      expect(alert.querySelector("p")?.textContent).toBe(
        "The move stopped part way: port 8782 is still in use after the old install stopped. Try again goes on from there."
      );
      expect(within(alert).getByText("Details")).toBeInTheDocument();
      expect(alert.querySelector("details pre")).toHaveTextContent(english);
      expect(alert.querySelector("details pre")).toHaveTextContent("adopt.log: stop");
      expect(api.finishSetup).not.toHaveBeenCalled();
      fail = false;
      await userEvent.click(screen.getByRole("button", { name: "Try again" }));
      expect(await screen.findByRole("heading", { name: "Starting Maktaba" })).toBeInTheDocument();
      expect(move).toHaveBeenCalledTimes(2);
    });

    it("a move that failed can restore the old install from the screen", async () => {
      const adopt = adoptStep(PLAN, () => {
        throw stepError("adopt_key_differs", "en: differs");
      });
      const api = fakeApi({ adopt }, { platform: "windows" });
      renderWizard(api);
      await userEvent.click(await screen.findByRole("button", { name: "Move it" }));
      await userEvent.click(await screen.findByRole("button", { name: "Move" }));
      expect(await screen.findByRole("button", { name: "Try again" })).toBeInTheDocument();
      expect(screen.getByText(/turns its task back on and runs it/)).toBeInTheDocument();
      await userEvent.click(screen.getByRole("button", { name: "Restore the old install" }));
      expect(adopt).toHaveBeenCalledWith({ action: "undo" }, undefined);
      expect(
        await screen.findByText("The old install runs again, as before the move.")
      ).toBeInTheDocument();
      expect(screen.getByRole("heading", { name: "Welcome to Maktaba" })).toBeInTheDocument();
      expect(api.finishSetup).not.toHaveBeenCalled();
    });

    it("a move done can be undone from the welcome screen", async () => {
      let stage = "finished";
      const adopt = adoptStep(PLAN, undefined, () => ({
        sources: [],
        move: { stage, source: THIS_PC },
      }));
      renderWizard(fakeApi({ adopt }, { platform: "windows" }));
      expect(await screen.findByText("The Maktaba this PC ran is in the app")).toBeInTheDocument();
      stage = "undone";
      await userEvent.click(screen.getByRole("button", { name: "Restore the old install" }));
      expect(adopt).toHaveBeenCalledWith({ action: "undo" }, undefined);
      expect(
        await screen.findByText("The old install runs again, as before the move.")
      ).toBeInTheDocument();
      await waitFor(() =>
        expect(screen.queryByText("The Maktaba this PC ran is in the app")).not.toBeInTheDocument()
      );
    });

    it("a node config nothing runs: a fresh setup is a new home, and says so", async () => {
      const adopt = adoptStep(PLAN, undefined, () => ({
        sources: [],
        leftover_node: {
          home: "Mahmouds-Mac-Studio",
          path: String.raw`C:\Users\me\AppData\Roaming\Maktaba\node.json`,
        },
      }));
      renderWizard(fakeApi({ adopt, check: () => CHECK }, { platform: "windows" }));
      expect(
        await screen.findByText(
          String.raw`this PC holds the node config of the home Mahmouds-Mac-Studio (C:\Users\me\AppData\Roaming\Maktaba\node.json), which nothing here runs: setting up fresh makes a new home and leaves that node config unused.`
        )
      ).toBeInTheDocument();
      await userEvent.click(screen.getByRole("button", { name: "Set up this PC" }));
      expect(await screen.findByRole("heading", { name: "What will run" })).toBeInTheDocument();
    });

    it("two findings of one code are two rows; the firewall prompt is said before the move", async () => {
      const errors = vi.spyOn(console, "error").mockImplementation(() => {});
      const adopt = adoptStep({
        ...PLAN,
        problems: [
          {
            code: "key_differs",
            params: { path: String.raw`C:\a\keys\secrets.key`, from: "x" },
            message: "en: a",
          },
          {
            code: "key_differs",
            params: { path: String.raw`C:\a\node.json`, from: "y" },
            message: "en: b",
          },
        ],
        warnings: [{ code: "firewall_prompt", params: { count: 4 }, message: "en: firewall" }],
      });
      renderWizard(fakeApi({ adopt }, { platform: "windows" }));
      await userEvent.click(await screen.findByRole("button", { name: "Move it" }));
      expect(await screen.findAllByRole("alert")).toHaveLength(2);
      expect(
        screen.getByText(
          "So that other devices still reach it, the move gives the app's programs the old install's 4 firewall rules: Windows asks you once to allow that."
        )
      ).toBeInTheDocument();
      expect(errors.mock.calls.some((c) => String(c[0]).includes("same key"))).toBe(false);
      errors.mockRestore();
    });

    it("a service install is named, and its plan refuses it in the app's words", async () => {
      const svc = {
        ...THIS_PC,
        id: "service:Maktaba",
        kind: "service",
        task: undefined,
        service: "Maktaba",
        db: "",
        dir: String.raw`C:\ProgramData\Maktaba`,
        ports: null,
      };
      const adopt = adoptStep(
        {
          ...PLAN,
          source: svc,
          items: [],
          carried: [],
          not_carried: [],
          problems: [
            { code: "service_install", params: { name: "Maktaba" }, message: "en: a service" },
          ],
        },
        undefined,
        () => ({ sources: [svc] })
      );
      renderWizard(fakeApi({ adopt }, { platform: "windows" }));
      expect(await screen.findByText("The Windows service Maktaba")).toBeInTheDocument();
      await userEvent.click(screen.getByRole("button", { name: "Move it" }));
      expect(await screen.findByRole("alert")).toHaveTextContent(
        "It runs as the Windows service Maktaba, under the system's account: only an administrator can stop it, so the app cannot move it."
      );
      expect(
        screen.getByText("A Windows service (it runs under the system's account)")
      ).toBeInTheDocument();
      expect(screen.getByRole("button", { name: "Move" })).toBeDisabled();
    });

    it("a gated model asks for the Hugging Face sign-in, then the move goes on", async () => {
      let gated = true;
      const models = vi.fn((input: Record<string, unknown>) => {
        if (input.action === "download" && gated) throw stepError("hf_gated", "sign in first");
        return {};
      });
      const move = vi.fn(() => URLS);
      const api = fakeApi({ adopt: adoptStep(PLAN, move), models }, { platform: "windows" });
      renderWizard(api);
      await userEvent.click(await screen.findByRole("button", { name: "Move it" }));
      await userEvent.click(await screen.findByRole("button", { name: "Move" }));
      expect(
        await screen.findByRole("heading", { name: "Transcription models" })
      ).toBeInTheDocument();
      gated = false;
      await userEvent.type(screen.getByLabelText(/Hugging Face token/), "hf_tok");
      await userEvent.click(screen.getByRole("button", { name: "Next" }));
      expect(await screen.findByRole("heading", { name: "Starting Maktaba" })).toBeInTheDocument();
      expect(models).toHaveBeenCalledWith(
        { source: "huggingface", hf_token: "hf_tok" },
        expect.any(Function)
      );
      expect(move).toHaveBeenCalledTimes(1);
    });
  });

  it("install: a language model that cannot download now is left to the server", async () => {
    const llm = vi.fn(() => {
      throw { code: "llm_failed", message: "no network", details: [] };
    });
    const api = fakeApi({ llm, finish: () => ({ web_url: "http://127.0.0.1:8088" }) });
    renderWizard(api, "install");
    expect(await screen.findByRole("heading", { name: "Starting Maktaba" })).toBeInTheDocument();
    expect(llm).toHaveBeenCalledTimes(1);
  });

  it("install: too little disk for the language model stops setup", async () => {
    const llm = vi.fn(() => {
      throw {
        code: "disk_full",
        message: "4.7 GB needed, 1.2 GB free",
        details: [],
      };
    });
    const finish = vi.fn(() => ({ web_url: "http://127.0.0.1:8088" }));
    const api = fakeApi({ llm, finish });
    renderWizard(api, "install");
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("The disk is full.");
    expect(alert.querySelector("details pre")?.textContent).toBe("4.7 GB needed, 1.2 GB free");
    expect(finish).not.toHaveBeenCalled();
  });

  it("install: asks for Login Items approval when macOS turned the item off", async () => {
    const api = fakeApi({ finish: () => ({ web_url: "http://127.0.0.1:8088" }) });
    api.finishSetup.mockResolvedValue("requires_approval" as never);
    renderWizard(api, "install");
    await userEvent.click(await screen.findByRole("button", { name: "Open Login Items" }));
    expect(api.openLoginItems).toHaveBeenCalled();
    await userEvent.click(screen.getByRole("button", { name: "I turned it on" }));
    expect(await screen.findByRole("heading", { name: "Starting Maktaba" })).toBeInTheDocument();
  });

  it("install: a failed venv shows the error with details and can be retried", async () => {
    let fail = true;
    const venv = vi.fn(() => {
      if (fail)
        throw { code: "venv_failed", message: "mlx 0.20 has no wheel", details: ["pip: error"] };
      return {};
    });
    const api = fakeApi({ venv, finish: () => ({ web_url: "http://127.0.0.1:8088" }) });
    renderWizard(api, "install");
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Installing the tools failed.");
    // the step's English (what failed) and its log lines are in the details
    expect(alert.querySelector("details pre")?.textContent).toBe(
      "mlx 0.20 has no wheel\npip: error"
    );
    fail = false;
    await userEvent.click(screen.getByRole("button", { name: "Try again" }));
    expect(await screen.findByRole("heading", { name: "Starting Maktaba" })).toBeInTheDocument();
    expect(venv).toHaveBeenCalledTimes(2);
  });

  it("goes back a screen", async () => {
    renderWizard(fakeApi({ check: () => CHECK }), "libraries");
    await userEvent.click(screen.getByRole("button", { name: "Back" }));
    expect(screen.getByRole("heading", { name: "Your admin account" })).toBeInTheDocument();
  });

  it("a refused field says why in Arabic, never in the step's English", async () => {
    localStorage.setItem("mkt:locale", "ar");
    const libraries = vi.fn(() => {
      throw {
        ...stepError("root_missing", "Y:\\Lectures is not a folder on this machine.", "roots"),
        params: { path: "Y:\\Lectures" },
      };
    });
    renderWizard(fakeApi({ libraries }, { platform: "windows" }), "libraries");
    await userEvent.type(await screen.findByRole("textbox", { name: /المجلدات/ }), "Y:\\Lectures");
    await userEvent.click(screen.getByRole("button", { name: "التالي" }));
    expect(await screen.findByText("Y:\\Lectures ليس مجلدًا على هذا الحاسوب.")).toBeInTheDocument();
    expect(screen.queryByText(/is not a folder/)).not.toBeInTheDocument();
    document.documentElement.dir = "ltr";
  });

  it("speaks Arabic", async () => {
    localStorage.setItem("mkt:locale", "ar");
    renderWizard(fakeApi());
    expect(await screen.findByRole("heading", { name: "مرحبًا بك في مكتبة" })).toBeInTheDocument();
    document.documentElement.dir = "ltr";
  });
});

describe("parsePathMap", () => {
  it("reads 'from => to' lines and rejects anything else", () => {
    expect(parsePathMap("")).toEqual([]);
    expect(parsePathMap(" /a => /b \n\n/c=>/d")).toEqual([
      { from: "/a", to: "/b" },
      { from: "/c", to: "/d" },
    ]);
    expect(parsePathMap("/a")).toBeNull();
    expect(parsePathMap("/a => ")).toBeNull();
    expect(parsePathMap("/a => /b => /c")).toBeNull();
  });
});

describe("ServerStarting", () => {
  beforeEach(() => vi.useFakeTimers({ shouldAdvanceTime: true }));
  afterEach(() => vi.useRealTimers());

  it("opens the local server once it runs", async () => {
    const api = fakeApi();
    api.serverStatus
      .mockResolvedValueOnce({ kind: "starting" })
      .mockResolvedValue({ kind: "running" } as never);
    render(
      <I18nProvider>
        <ServerStarting api={api} />
      </I18nProvider>
    );
    expect(await screen.findByText("The server is starting…")).toBeInTheDocument();
    await act(async () => vi.advanceTimersByTime(STARTING_POLL_MS * 2));
    await waitFor(() => expect(api.openLocalServer).toHaveBeenCalledTimes(1));
  });

  it("names the processes that keep stopping", async () => {
    const api = fakeApi();
    api.serverStatus.mockResolvedValue({
      kind: "problem",
      roles: ["pipeline", "transcribe 1"],
    } as never);
    render(
      <I18nProvider>
        <ServerStarting api={api} />
      </I18nProvider>
    );
    expect(
      await screen.findByText(/Some parts keep stopping: pipeline, transcribe 1/)
    ).toBeInTheDocument();
    expect(api.openLocalServer).not.toHaveBeenCalled();
  });

  it("says why a server that runs does not answer", async () => {
    const api = fakeApi();
    api.serverStatus.mockResolvedValue({
      kind: "not_answering",
      reason: "another launcher runs this install",
    } as never);
    render(
      <I18nProvider>
        <ServerStarting api={api} />
      </I18nProvider>
    );
    expect(await screen.findByText(/The server does not start\./)).toBeInTheDocument();
    // the log's own words, apart from the sentence (an Arabic screen keeps them as they are)
    expect(screen.getByText("another launcher runs this install").tagName).toBe("CODE");
    expect(api.openLocalServer).not.toHaveBeenCalled();
  });

  it("says a server that runs does not answer, with no log line when the log names none", async () => {
    const api = fakeApi();
    api.serverStatus.mockResolvedValue({ kind: "not_answering", reason: NO_ANSWER } as never);
    render(
      <I18nProvider>
        <ServerStarting api={api} />
      </I18nProvider>
    );
    expect(await screen.findByText(/The server does not start\./)).toBeInTheDocument();
    expect(screen.queryByText(NO_ANSWER)).not.toBeInTheDocument();
    expect(screen.queryByText(/Its log says/)).not.toBeInTheDocument();
  });
});

describe("DesktopGate", () => {
  const app = <p>the app</p>;
  const gate = (api: SetupApi, bundled: boolean) =>
    render(
      <I18nProvider>
        <DesktopGate api={api} bundled={bundled}>
          {app}
        </DesktopGate>
      </I18nProvider>
    );

  it("is transparent outside the app's bundled pages", () => {
    const api = fakeApi();
    gate(api, false);
    expect(screen.getByText("the app")).toBeInTheDocument();
    expect(api.appState).not.toHaveBeenCalled();
  });

  it("shows setup, the starting screen, or the app, as the shell says", async () => {
    const { unmount } = gate(fakeApi(), true);
    expect(await screen.findByRole("heading", { name: "Welcome to Maktaba" })).toBeInTheDocument();
    unmount();

    const starting = gate(
      fakeApi({}, { target: "starting", url: "http://127.0.0.1:8088" } as Partial<AppState>),
      true
    );
    expect(await screen.findByRole("heading", { name: "Starting Maktaba" })).toBeInTheDocument();
    starting.unmount();

    gate(fakeApi({}, { target: "bundled" }), true);
    expect(await screen.findByText("the app")).toBeInTheDocument();
  });

  it("adopts an already configured server instead of a second setup", async () => {
    const api = fakeApi({
      status: () => ({
        configured: true,
        web_url: "http://127.0.0.1:8088",
        supervisor_url: "http://127.0.0.1:9103",
      }),
    });
    gate(api, true);
    expect(await screen.findByRole("heading", { name: "Starting Maktaba" })).toBeInTheDocument();
    expect(api.finishSetup).toHaveBeenCalledWith("http://127.0.0.1:8088", "http://127.0.0.1:9103");
  });

  it("runs setup when this Mac is not configured, or when the user asked to change servers", async () => {
    const status = vi.fn(() => ({ configured: false }));
    const first = gate(fakeApi({ status }), true);
    expect(await screen.findByRole("heading", { name: "Welcome to Maktaba" })).toBeInTheDocument();
    expect(status).toHaveBeenCalled();
    first.unmount();

    const configured = vi.fn(() => ({ configured: true, web_url: "http://127.0.0.1:8088" }));
    const api = fakeApi({ status: configured }, { reset: true });
    gate(api, true);
    expect(await screen.findByRole("heading", { name: "Welcome to Maktaba" })).toBeInTheDocument();
    expect(configured).not.toHaveBeenCalled();
    expect(api.finishSetup).not.toHaveBeenCalled();
  });

  it("falls back to the app if the shell does not answer", async () => {
    const api = fakeApi();
    api.appState.mockRejectedValue(new Error("no ipc"));
    gate(api, true);
    expect(await screen.findByText("the app")).toBeInTheDocument();
  });
});
