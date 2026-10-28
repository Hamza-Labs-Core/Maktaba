// Preferences (Epic 42.5): a person's own settings in every edition, at
// /account/preferences. The panels are the ones Settings shows; here without the
// admin parts, whoever is signed in.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { ToastProvider } from "@ds/components/Toast/Toast";
import { I18nProvider } from "../../lib/i18n";
import en from "../../i18n/en.json";
import ar from "../../i18n/ar.json";

const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post } };
});

const auth = vi.hoisted(() => ({
  user: { id: "u1", username: "root", is_admin: true } as {
    id: string;
    username: string;
    is_admin: boolean;
  },
}));
vi.mock("../../lib/auth", () => ({
  useAuth: () => ({ user: auth.user, logoutAll: vi.fn() }),
  // the pages' kept answers (lib/usePageData) are per user
  useOptionalAuth: () => ({ user: auth.user }),
}));

import { Preferences } from "./Preferences";

function renderAt(path: string) {
  return render(
    <I18nProvider>
      <ToastProvider>
        <MemoryRouter initialEntries={[path]}>
          <Routes>
            <Route path="/account/preferences/*" element={<Preferences />} />
          </Routes>
        </MemoryRouter>
      </ToastProvider>
    </I18nProvider>
  );
}

beforeEach(() => {
  localStorage.clear();
  document.documentElement.removeAttribute("data-density");
  get.mockReset().mockImplementation((p: string) => {
    if (p === "/api/analytics/privacy") return Promise.resolve({ track_enabled: true });
    return Promise.resolve({ items: [] });
  });
  post.mockReset().mockResolvedValue({});
});

describe("Preferences", () => {
  it("opens on Appearance, with the three tabs", async () => {
    renderAt("/account/preferences");
    const tabs = screen.getByRole("navigation", { name: en["nav.preferences"] });
    expect(
      within(tabs)
        .getAllByRole("link")
        .map((a) => a.getAttribute("href"))
    ).toEqual([
      "/account/preferences",
      "/account/preferences/privacy",
      "/account/preferences/account",
    ]);
    expect(
      within(tabs).getByRole("link", { name: en["settings.section.appearance"] })
    ).toHaveAttribute("aria-current", "page");
    expect(screen.getByRole("radiogroup", { name: en["settings.theme"] })).toBeVisible();
  });

  it("keeps the density in this browser and applies it", async () => {
    renderAt("/account/preferences");
    await userEvent.click(screen.getByRole("radio", { name: en["settings.density.compact"] }));
    expect(localStorage.getItem("mkt:density")).toBe("compact");
    expect(document.documentElement.dataset.density).toBe("compact");
  });

  it("switches to Arabic, right to left", async () => {
    renderAt("/account/preferences");
    await userEvent.click(screen.getByRole("radio", { name: en["settings.language.ar"] }));
    expect(
      await screen.findByRole("heading", { level: 1, name: ar["nav.preferences"] })
    ).toBeVisible();
    expect(document.documentElement.dir).toBe("rtl");
    await userEvent.click(screen.getByRole("radio", { name: ar["settings.language.en"] }));
  });

  it("shows an admin no server parts: no home name, no server usage data", async () => {
    renderAt("/account/preferences/account");
    expect(screen.getByRole("button", { name: en["settings.signOutEverywhere"] })).toBeVisible();
    expect(screen.queryByText(en["settings.homeName.title"])).not.toBeInTheDocument();

    renderAt("/account/preferences/privacy");
    expect(await screen.findByText(en["telemetry.settings.title"])).toBeVisible();
    expect(screen.queryByText(en["telemetry.server.title"])).not.toBeInTheDocument();
    expect(get).not.toHaveBeenCalledWith("/api/telemetry/server");
  });

  it("opens Appearance for an unknown tab", async () => {
    renderAt("/account/preferences/nope");
    expect(await screen.findByRole("radiogroup", { name: en["settings.theme"] })).toBeVisible();
  });
});
