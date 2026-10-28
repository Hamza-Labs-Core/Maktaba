// Settings: the Account and Sessions tabs link the real token and session
// pages (11.13/11.14), Privacy shows the activity timeline (29.4), and
// About (28.6) badges an update and says so in one line (the Updates page
// has the rest).
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, fireEvent } from "@testing-library/react";
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

import { Settings } from "./Settings";
import { I18nProvider } from "../lib/i18n";
import { ToastProvider } from "@ds/components/Toast/Toast";
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

  it("badges About when an update is available, and About says so in one line", async () => {
    auth.user = { id: "u1", username: "root", is_admin: false };
    serve(available);
    renderAt("/settings/about");
    const about = screen.getByRole("link", { name: en["settings.section.about"] });
    await waitFor(() => expect(about).toHaveAccessibleDescription(en["update.badge"]));
    const line = await screen.findByTestId("about-update-status");
    await waitFor(() => expect(line).toHaveTextContent(en["updates.status.update_available"]));
    expect(line).toHaveTextContent("v1.1.0");
    // the notes, the instructions and Update now are the Updates page's
    expect(screen.queryByRole("button", { name: en["update.updateNow"] })).toBeNull();
  });
});
