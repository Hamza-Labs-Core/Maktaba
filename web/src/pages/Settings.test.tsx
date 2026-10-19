// Settings: the Account and Sessions tabs link the real token and session
// pages (11.13/11.14), Privacy shows the activity timeline (29.4), and
// About (28.6) badges an update, shows Docker instructions up front,
// renders release notes as Markdown and follows an update to done or
// rolled back.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, waitFor, fireEvent, act } from "@testing-library/react";
import { MemoryRouter, Routes, Route } from "react-router-dom";

const { get, post, auth } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  auth: { user: { id: "u1", username: "root", is_admin: true } as Record<string, unknown> },
}));
vi.mock("../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../lib/api")>("../lib/api");
  return { ...actual, api: { ...actual.api, get, post } };
});
vi.mock("../lib/auth", () => ({
  useAuth: () => ({ user: auth.user, logoutAll: vi.fn() }),
}));
vi.mock("./Settings/UpdatesSettings", () => ({ UpdatesSettings: () => null }));

import { Settings, followRestart } from "./Settings";
import { I18nProvider } from "../lib/i18n";
import { ToastProvider } from "@ds/components/Toast/Toast";
import { ApiError } from "../lib/api";
import { resetUpdateStatusCache } from "../lib/updates";
import en from "../i18n/en.json";

function renderAt(url: string) {
  return render(
    <MemoryRouter initialEntries={[url]}>
      <I18nProvider>
        <ToastProvider>
          <Routes>
            <Route path="/settings/*" element={<Settings />} />
          </Routes>
        </ToastProvider>
      </I18nProvider>
    </MemoryRouter>
  );
}

const upToDate = {
  available: false,
  current_version: "v1.0.0",
  channel: "stable",
  checked_at: "2026-03-01T00:00:00Z",
};
const available = {
  available: true,
  current_version: "v1.0.0",
  latest_version: "v1.1.0",
  channel: "stable",
  checked_at: "2026-03-01T00:00:00Z",
  release_notes: "## Highlights\n\n- **Faster** scans\n- <b>raw</b>",
  install: "binary",
};

function serve(updates: unknown, version: unknown = { version: "v1.0.0", channel: "stable" }) {
  get.mockImplementation((url: string) => {
    if (url.startsWith("/api/system/updates")) return Promise.resolve(updates);
    if (url === "/api/system/version") return Promise.resolve(version);
    if (url.startsWith("/api/me/activity/settings"))
      return Promise.resolve({ track_enabled: true });
    return Promise.resolve({ items: [] });
  });
}

describe("Settings", () => {
  beforeEach(() => {
    get.mockReset();
    post.mockReset();
    resetUpdateStatusCache();
    auth.user = { id: "u1", username: "root", is_admin: true };
  });

  it("links personal access tokens from Account and sessions from Sessions", async () => {
    serve(upToDate);
    renderAt("/settings");
    expect(await screen.findByRole("link", { name: en["settings.tokens.manage"] })).toHaveAttribute(
      "href",
      "/account/tokens"
    );
    expect(screen.queryByText(/not yet available/)).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("link", { name: en["settings.section.sessions"] }));
    expect(
      await screen.findByRole("link", { name: en["settings.sessions.manage"] })
    ).toHaveAttribute("href", "/account/sessions");
  });

  it("shows the activity timeline on Privacy with links to the full pages", async () => {
    serve(upToDate);
    get.mockImplementation((url: string) => {
      if (url.startsWith("/api/me/activity/settings"))
        return Promise.resolve({ track_enabled: true });
      if (url.startsWith("/api/me/activity")) {
        return Promise.resolve({
          items: [{ kind: "searched", at: "2026-03-01T08:00:00Z", meta: { query: "tafsir" } }],
        });
      }
      return Promise.resolve({ items: [] });
    });
    renderAt("/settings/privacy");
    expect(await screen.findByRole("link", { name: "tafsir" })).toBeInTheDocument();
    expect(get).toHaveBeenCalledWith("/api/me/activity?limit=10&offset=0");
    expect(screen.getByRole("link", { name: en["settings.privacy.fullActivity"] })).toHaveAttribute(
      "href",
      "/account/history?tab=activity"
    );
    expect(screen.getByRole("link", { name: en["settings.privacy.fullHistory"] })).toHaveAttribute(
      "href",
      "/account/history"
    );
  });

  it("badges About and renders the release notes as Markdown", async () => {
    serve(available);
    renderAt("/settings/about");
    expect(
      await screen.findByRole("link", {
        name: `${en["settings.section.about"]}, ${en["update.badge"]}`,
      })
    ).toBeInTheDocument();
    expect(await screen.findByRole("heading", { name: "Highlights" })).toBeInTheDocument();
    expect(screen.getByText("Faster").tagName).toBe("STRONG");
    expect(screen.getByText("<b>raw</b>")).toBeInTheDocument(); // shown, never injected
    expect(screen.getByRole("button", { name: en["update.updateNow"] })).toBeInTheDocument();
  });

  it("shows the Docker instruction up front instead of Update now", async () => {
    serve({
      ...available,
      install: "docker",
      instructions: "docker compose pull && docker compose up -d",
      image: "ghcr.io/hamza-labs-core/maktaba-server:1.1.0",
    });
    renderAt("/settings/about");
    expect(await screen.findByText(en["update.docker.instructions"])).toBeInTheDocument();
    expect(screen.getByText("docker compose pull && docker compose up -d")).toBeInTheDocument();
    expect(screen.getByText("ghcr.io/hamza-labs-core/maktaba-server:1.1.0")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: en["update.updateNow"] })).not.toBeInTheDocument();
    expect(post).not.toHaveBeenCalled();
  });

  it("hides Update now from a non-admin", async () => {
    auth.user = { id: "u2", username: "viewer", is_admin: false };
    serve(available);
    renderAt("/settings/about");
    await screen.findByRole("heading", { name: "Highlights" });
    expect(screen.queryByRole("button", { name: en["update.updateNow"] })).not.toBeInTheDocument();
  });

  it("reports an earlier rollback from the status", async () => {
    serve({
      ...available,
      rolled_back: { version: "v1.1.0", reason: "exec format error", at: "" },
    });
    renderAt("/settings/about");
    expect(
      await screen.findByText(en["update.lastRolledBack"].replace("{version}", "v1.1.0"))
    ).toBeInTheDocument();
    expect(screen.getByText("exec format error")).toBeInTheDocument();
  });

  it("falls back to the 409 instruction from an older server", async () => {
    serve({ ...available, install: undefined });
    post.mockRejectedValue(
      new ApiError({
        type: "about:blank",
        title: "conflict",
        status: 409,
        instructions: "docker compose pull && docker compose up -d",
      } as never)
    );
    renderAt("/settings/about");
    fireEvent.click(await screen.findByRole("button", { name: en["update.updateNow"] }));
    expect(await screen.findByText(en["update.docker.instructions"])).toBeInTheDocument();
  });
});

describe("followRestart", () => {
  beforeEach(() => {
    get.mockReset();
    vi.useFakeTimers();
  });
  afterEach(() => vi.useRealTimers());

  async function run(from: string, target: string) {
    const p = followRestart(from, target, { attempts: 5, delayMs: 10 });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(100);
    });
    return p;
  }

  it("is done when the new version answers", async () => {
    get.mockRejectedValueOnce(new Error("down")).mockResolvedValue({ version: "v1.1.0" });
    expect((await run("v1.0.0", "v1.1.0")).phase).toBe("done");
  });

  it("is rolled back when the server comes back on the old version", async () => {
    get
      .mockRejectedValueOnce(new Error("down"))
      .mockRejectedValueOnce(new Error("down"))
      .mockResolvedValue({ version: "v1.0.0" });
    const out = await run("v1.0.0", "v1.1.0");
    expect(out.phase).toBe("rolled_back");
    expect(out.version.version).toBe("v1.0.0");
  });

  it("is rolled back when the server reports it", async () => {
    get.mockImplementation((url: string) =>
      Promise.resolve(
        url === "/api/system/version"
          ? { version: "v1.0.0" }
          : { rolled_back: { version: "v1.1.0", reason: "x", at: "" } }
      )
    );
    expect((await run("v1.0.0", "v1.1.0")).phase).toBe("rolled_back");
  });

  it("is pending when the server never restarted", async () => {
    get.mockImplementation((url: string) =>
      Promise.resolve(url === "/api/system/version" ? { version: "v1.0.0" } : {})
    );
    expect((await run("v1.0.0", "v1.1.0")).phase).toBe("pending");
  });

  it("is an error when the server went away and did not come back", async () => {
    get.mockRejectedValue(new Error("down"));
    expect((await run("v1.0.0", "v1.1.0")).phase).toBe("error");
  });
});

describe("Settings Update now", () => {
  beforeEach(() => {
    get.mockReset();
    post.mockReset();
    resetUpdateStatusCache();
    auth.user = { id: "u1", username: "root", is_admin: true };
  });

  it("posts the update and shows the new version once it answers", async () => {
    let installed = false;
    get.mockImplementation((url: string) => {
      if (url.startsWith("/api/system/updates"))
        return Promise.resolve(installed ? upToDate : available);
      if (url === "/api/system/version") {
        return Promise.resolve({ version: installed ? "v1.1.0" : "v1.0.0", channel: "stable" });
      }
      return Promise.resolve({ items: [] });
    });
    post.mockImplementation(() => {
      installed = true;
      return Promise.resolve({ updated: true, restarting: true });
    });
    renderAt("/settings/about");
    fireEvent.click(await screen.findByRole("button", { name: en["update.updateNow"] }));
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith("/api/admin/system/update", { confirm: true })
    );
    expect(
      await screen.findByText(en["update.phase.done"].replace("{version}", "v1.1.0"), undefined, {
        timeout: 5000,
      })
    ).toBeInTheDocument();
  });
});
