// Story 13.13: the setup in its own window, restyled to the design (docs/design/desktop,
// setup-welcome and setup-check): the brand pane's five steps, the two ways as option cards
// with one button in the footer, the language switch, what will run with memory and disk, and
// the main window that shows nothing while the setup window has the setup.
import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "../../lib/i18n";
import {
  previewSetupApi,
  type AppState,
  type SetupApi,
  type StepName,
} from "../../lib/desktopSetup";
import { DesktopGate } from "../../components/DesktopGate";
import { SETUP_STEPS, SetupWizard, setupStepOf } from "./SetupWizard";

const GB = 2 ** 30;
const CHECK = {
  ram_bytes: 16 * GB,
  disk_free_bytes: 412 * GB,
  data_dir: String.raw`C:\Users\haddad\AppData\Local\Maktaba\data`,
  need: { ram_bytes: 8 * GB, disk_bytes: 20 * GB },
  ok: true,
  problems: [],
  warnings: [
    {
      code: "transcribe_cpu",
      message: "No NVIDIA graphics card",
    },
  ],
  runs: [
    { role: "postgres", what: "The library database" },
    { role: "api", what: "The server" },
    { role: "streaming", what: "Playback" },
    { role: "pipeline", what: "Indexing", params: { workers: 4 } },
  ],
};

function fakeApi(state: Partial<AppState> = {}, steps: Partial<Record<StepName, unknown>> = {}) {
  const api = {
    appState: vi.fn(
      async () =>
        ({
          target: "setup",
          server_available: true,
          login_item: "not_registered",
          platform: "windows",
          ...state,
        }) as AppState
    ),
    runStep: vi.fn(async (step: StepName) => (step in steps ? steps[step] : { sources: [] })),
    finishSetup: vi.fn(async () => "enabled" as const),
    openLoginItems: vi.fn(async () => {}),
    serverStatus: vi.fn(async () => ({ kind: "starting" as const })),
    openLocalServer: vi.fn(async () => {}),
    useRemoteServer: vi.fn(async () => {}),
    openExternal: vi.fn(async () => {}),
  };
  return api as typeof api & SetupApi;
}

function wizard(api: SetupApi) {
  return render(
    <I18nProvider>
      <SetupWizard api={api} />
    </I18nProvider>
  );
}

/** "Set up this PC", once it can be pressed: on Windows it waits for the look for an install
 * to move in (the adopt step), which answers after the first render. */
async function setUpThisPc() {
  const button = await screen.findByRole("button", { name: /Set up this PC/ });
  await waitFor(() => expect(button).toBeEnabled());
  return button;
}

function steps(name = "Setup steps") {
  const list = within(screen.getByRole("complementary", { name })).getByRole("list");
  return within(list).getAllByRole("listitem");
}

describe("the setup window", () => {
  afterEach(() => {
    localStorage.clear();
    delete (window as unknown as Record<string, unknown>).__MAKTABA_SETUP_PREVIEW__;
  });

  it("groups the screens into the five steps the brand pane lists", () => {
    expect(SETUP_STEPS.map((s) => s.key)).toEqual([
      "welcome",
      "check",
      "admin",
      "libraries",
      "ready",
    ]);
    expect(setupStepOf("welcome")).toBe(0);
    expect(setupStepOf("storage")).toBe(1);
    expect(setupStepOf("admin")).toBe(2);
    expect(setupStepOf("plex")).toBe(3);
    expect(setupStepOf("permissions")).toBe(3);
    expect(setupStepOf("install")).toBe(4);
    expect(setupStepOf("starting")).toBe(4);
  });

  it("offers the two ways as cards, this machine chosen, and one button for the way chosen", async () => {
    const api = fakeApi();
    wizard(api);
    expect(await setUpThisPc()).toBeEnabled();
    const [here, remote] = screen.getAllByRole("radio");
    expect(here).toBeChecked();
    expect(here).toHaveAccessibleName("Run Maktaba on this PC");
    expect(remote).not.toBeChecked();
    expect(await screen.findByText("Recommended for the first computer")).toBeInTheDocument();
    // the brand pane: the first step is the one on show
    const list = steps();
    expect(list[0]).toHaveAttribute("aria-current", "step");
    expect(list[1]).not.toHaveAttribute("aria-current");
    // typing an address chooses another Maktaba: the footer's button connects
    await userEvent.type(screen.getByLabelText("Its address"), "http://192.168.1.20:8080");
    expect(remote).toBeChecked();
    expect(screen.queryByRole("button", { name: /Set up this PC/ })).toBeNull();
    await userEvent.click(screen.getByRole("button", { name: "Connect" }));
    expect(api.useRemoteServer).toHaveBeenCalledWith("http://192.168.1.20:8080");
    // and back to this machine
    await userEvent.click(here);
    expect(screen.getByRole("button", { name: /Set up this PC/ })).toBeInTheDocument();
  });

  it("Connect waits for an address", async () => {
    wizard(fakeApi({ server_available: false }));
    expect(await screen.findByRole("button", { name: "Connect" })).toBeDisabled();
    // no server in this build: one way, no radios
    expect(screen.queryAllByRole("radio")).toHaveLength(0);
  });

  it("switches the language from the footer", async () => {
    wizard(fakeApi());
    const lang = await screen.findByRole("group", { name: "Language" });
    expect(within(lang).getByRole("button", { name: "English" })).toHaveAttribute(
      "aria-pressed",
      "true"
    );
    await userEvent.click(within(lang).getByRole("button", { name: "العربية" }));
    expect(await screen.findByRole("heading", { name: "مرحبًا بك في مكتبة" })).toBeInTheDocument();
    expect(document.documentElement.dir).toBe("rtl");
    expect(steps("خطوات الإعداد")[1]).toHaveTextContent("ما الذي سيعمل");
  });

  it("what will run: a row each, memory and disk against what is needed, the data folder", async () => {
    wizard(fakeApi({}, { adopt: { sources: [] }, check: CHECK }));
    await userEvent.click(await setUpThisPc());
    expect(await screen.findByRole("heading", { name: "What will run" })).toBeInTheDocument();
    expect(screen.getByText("Indexing, transcription (4 workers) and search")).toBeInTheDocument();
    expect(screen.getByText(/^.?16\.0 GB.* \(needs .*8\.0 GB.*\)$/)).toBeInTheDocument();
    expect(screen.getByText(/412\.0 GB.* \(needs .*20\.0 GB.*\)/)).toBeInTheDocument();
    expect(
      screen.getByText(
        "No NVIDIA graphics card: this PC transcribes on its processor, many times slower than on a graphics card."
      )
    ).toBeInTheDocument();
    expect(screen.getByText(CHECK.data_dir)).toBeInTheDocument();
    // the first step done, the second on show
    const list = steps();
    expect(list[0]).toHaveTextContent("(done)");
    expect(list[1]).toHaveAttribute("aria-current", "step");
    // the step counter is still there for a screen reader
    expect(screen.getByText(/Step 1 of \d/)).toHaveClass("mkt-visually-hidden");
  });

  it("memory short of what is needed shows short", async () => {
    const short = { ...CHECK, ram_bytes: 4 * GB, ok: false, problems: [], warnings: [] };
    wizard(fakeApi({}, { adopt: { sources: [] }, check: short }));
    await userEvent.click(await setUpThisPc());
    const ram = (await screen.findByText(/^.?4\.0 GB.* \(needs .*8\.0 GB.*\)$/)).closest(
      ".mkt-setup__meter"
    );
    expect(ram).toHaveClass("is-short");
    expect(screen.getByRole("button", { name: "Next" })).toBeDisabled();
  });
});

describe("the main window while the setup window has the setup", () => {
  it("shows nothing and runs no step", async () => {
    const api = fakeApi({ setup_window: true });
    const { container } = render(
      <I18nProvider>
        <DesktopGate api={api} bundled>
          <p>the app</p>
        </DesktopGate>
      </I18nProvider>
    );
    await waitFor(() => expect(api.appState).toHaveBeenCalled());
    await new Promise((r) => setTimeout(r, 0));
    expect(container).toBeEmptyDOMElement();
    expect(api.runStep).not.toHaveBeenCalled();
    expect(api.finishSetup).not.toHaveBeenCalled();
  });
});

describe("previewSetupApi (the visual references' stand-in for the shell)", () => {
  afterEach(() => {
    delete (window as unknown as Record<string, unknown>).__MAKTABA_SETUP_PREVIEW__;
  });

  it("is none without a preview, and one api per preview", async () => {
    expect(previewSetupApi()).toBeNull();
    const state = { target: "setup", server_available: true, login_item: "not_registered" };
    (window as unknown as Record<string, unknown>).__MAKTABA_SETUP_PREVIEW__ = {
      state,
      steps: { check: CHECK },
    };
    const api = previewSetupApi();
    expect(api).not.toBeNull();
    expect(previewSetupApi()).toBe(api);
    expect(await api!.appState()).toEqual(state);
    expect(await api!.runStep("check", {})).toEqual(CHECK);
    // a step it has no answer for never answers: nothing runs
    const never = await Promise.race([
      api!.runStep("finish", {}).then(() => "answered"),
      new Promise((r) => setTimeout(() => r("waiting"), 20)),
    ]);
    expect(never).toBe("waiting");
  });
});
