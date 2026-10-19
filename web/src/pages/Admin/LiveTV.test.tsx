// Admin Live TV access: loads /api/tv/info + /api/tv/hdhr, shows the
// addresses Plex needs with copy buttons (with a fallback for plain-http
// LAN origins) and the Plex steps, turns the HDHomeRun tuner on / renames
// it through PATCH /api/tv/hdhr, and runs the Plex Live TV panel
// (/api/tv/plex, refresh, register, the plexlive rules).
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { I18nProvider } from "../../lib/i18n";

const { get, patch, post } = vi.hoisted(() => ({ get: vi.fn(), patch: vi.fn(), post: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, patch, post } };
});
vi.mock("../../lib/auth", () => ({
  useAuth: () => ({ user: { username: "admin", is_admin: true } }),
}));

import { AdminLiveTV } from "./LiveTV";

const info = {
  stream_base_url: "http://192.168.1.20:8782",
  hdhr_address: "192.168.1.20:8782",
  xmltv_url: "http://192.168.1.20:8782/xmltv.xml",
  m3u_url: "http://192.168.1.20:8782/lineup.m3u",
  base_from_config: true,
  xmltv_path: "/xmltv.xml",
  m3u_path: "/lineup.m3u",
  hls_path_template: "/stream/channel/{id}/index.m3u8",
};
const device = {
  device_id: "A1B2C3D4",
  friendly_name: "Maktaba",
  tuner_count: 4,
  enabled: false,
  max_tuners: 16,
};

const ruleField = (name: string, value: unknown) => ({
  name,
  kind: typeof value === "boolean" ? "bool" : "int",
  group: "Plex Live TV",
  label: name,
  help: "",
  default: value,
  value,
  changed: false,
});
const rules = {
  sections: [
    {
      key: "plexlive",
      title: "Plex Live TV",
      what: "",
      fields: [
        ruleField("auto_register", false),
        ruleField("auto_refresh_guide", true),
        ruleField("auto_refresh_channels", true),
        ruleField("guide_refresh_min_minutes", 60),
      ],
    },
  ],
};
const plexEmpty = {
  plex: { enabled: true, configured: true, url: "http://plex.lan:32400" },
  stage: { enabled: true, disabled_reason: null },
  job: null,
  checked_at: "2026-10-18T08:00:00.000Z",
  guide_reloaded_at: null,
  channelmap_at: null,
  last_error: null,
  last_error_at: null,
  register_requested_at: null,
  status: {
    reachable: true,
    dvrs: [
      {
        key: "11",
        title: "dizqueTV",
        lineup: "lineup://x",
        maktaba: false,
        devices: [
          { key: "3", uri: "http://pc:8000", device_id: "D1", title: "dizqueTV", kind: "dizquetv" },
        ],
      },
    ],
    maktaba: { tuner_key: null, dvr_key: null, registered: false },
    dizquetv_present: true,
    channels: { lineup: 3, mapped: null, in_sync: null },
    target: {
      tuner_url: "http://192.168.1.20:8782",
      xmltv_url: "http://192.168.1.20:8782/xmltv.xml",
      source: "env",
    },
    warnings: [],
  },
};
const plexRegistered = {
  ...plexEmpty,
  guide_reloaded_at: "2026-10-18T08:00:00.000Z",
  channelmap_at: "2026-10-18T08:00:00.000Z",
  status: {
    ...plexEmpty.status,
    maktaba: { tuner_key: "14", dvr_key: "15", registered: true },
    channels: { lineup: 3, mapped: 3, in_sync: true },
  },
};

function routes(over: Record<string, unknown> = {}) {
  const table: Record<string, unknown> = {
    "/api/tv/info": info,
    "/api/tv/hdhr": device,
    "/api/tv/plex": plexEmpty,
    "/api/rules": rules,
    ...over,
  };
  get.mockImplementation((url: string) =>
    url in table ? Promise.resolve(table[url]) : Promise.reject(new Error("unexpected " + url))
  );
}

function renderPage() {
  return render(
    <MemoryRouter>
      <I18nProvider>
        <AdminLiveTV />
      </I18nProvider>
    </MemoryRouter>
  );
}

describe("AdminLiveTV", () => {
  const exec0 = document.execCommand;
  beforeEach(() => {
    get.mockReset();
    patch.mockReset();
    post.mockReset();
    routes();
  });
  afterEach(() => {
    Object.defineProperty(window, "isSecureContext", { value: true, configurable: true });
    Object.defineProperty(document, "execCommand", { value: exec0, configurable: true });
  });

  it("shows the Plex addresses and steps", async () => {
    renderPage();
    expect(await screen.findByDisplayValue("192.168.1.20:8782")).toBeInTheDocument();
    expect(screen.getByDisplayValue("http://192.168.1.20:8782/xmltv.xml")).toBeInTheDocument();
    expect(screen.getByDisplayValue("http://192.168.1.20:8782/lineup.m3u")).toBeInTheDocument();
    expect(screen.getByText(/Live TV & DVR → Set up/)).toBeInTheDocument();
    expect(screen.getByText("A1B2C3D4")).toBeInTheDocument();
  });

  it("copies an address to the clipboard", async () => {
    const user = userEvent.setup();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });
    renderPage();
    await user.click(await screen.findByRole("button", { name: /copy xmltv guide/i }));
    expect(writeText).toHaveBeenCalledWith("http://192.168.1.20:8782/xmltv.xml");
    expect(await screen.findByText("Copied")).toBeInTheDocument();
  });

  it("copies on a plain-http LAN origin through the selection fallback", async () => {
    const user = userEvent.setup();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });
    Object.defineProperty(window, "isSecureContext", { value: false, configurable: true });
    const exec = vi.fn().mockReturnValue(true);
    Object.defineProperty(document, "execCommand", { value: exec, configurable: true });
    renderPage();
    await user.click(await screen.findByRole("button", { name: /copy m3u playlist/i }));
    expect(writeText).not.toHaveBeenCalled(); // not a secure context
    expect(exec).toHaveBeenCalledWith("copy");
    const field = screen.getByDisplayValue(
      "http://192.168.1.20:8782/lineup.m3u"
    ) as HTMLInputElement;
    expect(document.activeElement).toBe(field);
    expect((field.selectionEnd ?? 0) - (field.selectionStart ?? 0)).toBe(field.value.length);
    expect(await screen.findByText("Copied")).toBeInTheDocument();
  });

  it("leaves the text selected and says so when copying is blocked", async () => {
    const user = userEvent.setup();
    Object.defineProperty(navigator, "clipboard", { value: undefined, configurable: true });
    Object.defineProperty(document, "execCommand", {
      value: vi.fn().mockReturnValue(false),
      configurable: true,
    });
    renderPage();
    await user.click(await screen.findByRole("button", { name: /copy hdhomerun address/i }));
    expect(await screen.findByText("Copy failed")).toBeInTheDocument();
    expect(screen.getByText(/press Ctrl\+C/)).toBeInTheDocument();
    expect(document.activeElement).toBe(screen.getByDisplayValue("192.168.1.20:8782"));
  });

  it("shows the Plex status with the dizqueTV DVR left alone", async () => {
    renderPage();
    expect(await screen.findByText("Not added yet")).toBeInTheDocument();
    expect(screen.getByText("dizqueTV: dizqueTV (left as it is)")).toBeInTheDocument();
    expect(screen.getByText(/tuner is in Plex too/)).toBeInTheDocument();
    expect(screen.getByText("http://plex.lan:32400 · reachable")).toBeInTheDocument();
  });

  it("adds Maktaba to Plex only after a confirm that says what changes", async () => {
    const user = userEvent.setup();
    routes({ "/api/tv/hdhr": { ...device, enabled: true } });
    post.mockResolvedValue({ queued: true, outcome: "inserted", job_id: 7 });
    renderPage();
    await user.click(await screen.findByRole("button", { name: "Add Maktaba to Plex" }));
    expect(post).not.toHaveBeenCalled();
    const dialog = await screen.findByRole("dialog");
    expect(dialog).toHaveTextContent("http://192.168.1.20:8782 is added");
    expect(dialog).toHaveTextContent("XMLTV guide http://192.168.1.20:8782/xmltv.xml");
    expect(dialog).toHaveTextContent("dizqueTV's included, stay as they are");
    routes({ "/api/tv/hdhr": { ...device, enabled: true }, "/api/tv/plex": plexRegistered });
    await user.click(screen.getByRole("button", { name: "Add to Plex" }));
    expect(post).toHaveBeenCalledWith("/api/tv/plex/register");
    expect(await screen.findByText(/^Queued/)).toBeInTheDocument();
    expect(await screen.findByText(/Added: a DVR uses/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Add Maktaba to Plex" })).not.toBeInTheDocument();
    expect(screen.getByText("3 of 3 channels")).toBeInTheDocument();
  });

  it("cannot add Maktaba to Plex while the tuner is off", async () => {
    renderPage();
    expect(await screen.findByRole("button", { name: "Add Maktaba to Plex" })).toBeDisabled();
    expect(screen.getByText(/Turn the tuner on above before adding/)).toBeInTheDocument();
  });

  it("refreshes Plex now and shows a refusal", async () => {
    const user = userEvent.setup();
    post.mockRejectedValueOnce(new Error("the Plex Live TV stage is not available"));
    renderPage();
    await user.click(await screen.findByRole("button", { name: "Refresh Plex now" }));
    expect(post).toHaveBeenCalledWith("/api/tv/plex/refresh");
    expect(await screen.findByRole("alert")).toHaveTextContent("stage is not available");
  });

  it("asks to sign in with Plex when the extension is not set up", async () => {
    routes({
      "/api/tv/plex": {
        ...plexEmpty,
        plex: { enabled: false, configured: false, url: "" },
        checked_at: null,
      },
    });
    renderPage();
    expect(await screen.findByText(/sign in with Plex first/)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Open Extensions" })).toHaveAttribute(
      "href",
      "/admin/extensions"
    );
    expect(screen.getByRole("button", { name: "Refresh Plex now" })).toBeDisabled();
  });

  it("saves the automatic Plex settings as rules", async () => {
    const user = userEvent.setup();
    patch.mockResolvedValue({ section: "plexlive", changed: [] });
    renderPage();
    const auto = await screen.findByRole("switch", { name: /Add Maktaba to Plex by itself/ });
    expect(auto).not.toBeChecked();
    await user.click(auto);
    expect(patch).toHaveBeenCalledWith("/api/rules/plexlive", {
      set: { auto_register: true },
      reset: [],
    });
    await waitFor(() => expect(auto).toBeChecked());
    await user.click(auto); // back to the default: the change is reset, not stored
    expect(patch).toHaveBeenLastCalledWith("/api/rules/plexlive", {
      set: {},
      reset: ["auto_register"],
    });
    const minutes = screen.getByLabelText(/Guide reload at most every/);
    await user.clear(minutes);
    await user.type(minutes, "90");
    await user.click(screen.getByRole("button", { name: "Save interval" }));
    expect(patch).toHaveBeenLastCalledWith("/api/rules/plexlive", {
      set: { guide_refresh_min_minutes: 90 },
      reset: [],
    });
  });

  it("turns the tuner on", async () => {
    const user = userEvent.setup();
    patch.mockResolvedValue({ ...device, enabled: true });
    renderPage();
    await user.click(await screen.findByRole("switch", { name: /Tuner off/ }));
    expect(patch).toHaveBeenCalledWith("/api/tv/hdhr", { enabled: true });
    expect(await screen.findByText(/Tuner turned on/)).toBeInTheDocument();
    expect(screen.getByRole("switch", { name: /Tuner on/ })).toBeChecked();
  });

  it("saves the name and tuner count, and validates the count", async () => {
    const user = userEvent.setup();
    patch.mockResolvedValue({ ...device, friendly_name: "Maktaba TV", tuner_count: 2 });
    renderPage();
    const name = await screen.findByLabelText("Tuner name");
    const count = screen.getByLabelText("Tuners");
    const save = screen.getAllByRole("button", { name: "Save" })[0];
    expect(save).toBeDisabled();

    await user.clear(count);
    await user.type(count, "40");
    expect(screen.getByText("Between 1 and 16.")).toBeInTheDocument();
    expect(save).toBeDisabled();

    await user.clear(count);
    await user.type(count, "2");
    await user.clear(name);
    await user.type(name, "Maktaba TV");
    await user.click(save);
    await waitFor(() =>
      expect(patch).toHaveBeenCalledWith("/api/tv/hdhr", {
        friendly_name: "Maktaba TV",
        tuner_count: 2,
      })
    );
  });
});
