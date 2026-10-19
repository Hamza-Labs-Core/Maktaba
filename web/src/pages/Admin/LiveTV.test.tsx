// Admin Live TV access: loads /api/tv/info + /api/tv/hdhr, shows the
// addresses Plex needs with copy buttons and the Plex steps, and turns
// the HDHomeRun tuner on / renames it through PATCH /api/tv/hdhr.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "../../lib/i18n";

const { get, patch } = vi.hoisted(() => ({ get: vi.fn(), patch: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, patch } };
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

function renderPage() {
  return render(
    <I18nProvider>
      <AdminLiveTV />
    </I18nProvider>
  );
}

describe("AdminLiveTV", () => {
  beforeEach(() => {
    get.mockReset();
    patch.mockReset();
    get.mockImplementation((url: string) =>
      Promise.resolve(url === "/api/tv/info" ? info : device)
    );
  });

  it("shows the Plex addresses and steps", async () => {
    renderPage();
    expect(await screen.findByText("192.168.1.20:8782")).toBeInTheDocument();
    expect(screen.getByText("http://192.168.1.20:8782/xmltv.xml")).toBeInTheDocument();
    expect(screen.getByText("http://192.168.1.20:8782/lineup.m3u")).toBeInTheDocument();
    expect(screen.getByText(/Live TV & DVR/)).toBeInTheDocument();
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

  it("turns the tuner on", async () => {
    const user = userEvent.setup();
    patch.mockResolvedValue({ ...device, enabled: true });
    renderPage();
    await user.click(await screen.findByRole("switch"));
    expect(patch).toHaveBeenCalledWith("/api/tv/hdhr", { enabled: true });
    expect(await screen.findByText(/Tuner turned on/)).toBeInTheDocument();
    expect(screen.getByRole("switch")).toBeChecked();
  });

  it("saves the name and tuner count, and validates the count", async () => {
    const user = userEvent.setup();
    patch.mockResolvedValue({ ...device, friendly_name: "Maktaba TV", tuner_count: 2 });
    renderPage();
    const name = await screen.findByLabelText("Tuner name");
    const count = screen.getByLabelText("Tuners");
    const save = screen.getByRole("button", { name: "Save" });
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
