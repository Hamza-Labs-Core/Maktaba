// VideoDetail honours `?tab=` for every tab, so deep links such as the
// context card's "Find metadata" (?tab=enrichment) land on the right
// panel, and clicking a tab writes it back to the URL.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, fireEvent } from "@testing-library/react";
import { MemoryRouter, Routes, Route, useLocation } from "react-router-dom";

const { get } = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock("../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../lib/api")>("../lib/api");
  return { ...actual, api: { ...actual.api, get } };
});
vi.mock("../lib/auth", () => ({
  useAuth: () => ({ user: { id: "u1", username: "x", is_admin: false } }),
}));
vi.mock("../lib/ws", () => ({ subscribe: () => () => {} }));
vi.mock("../components/MediaContextCard", () => ({
  MediaContextCard: () => <div data-testid="context-card" />,
}));
vi.mock("../components/EnrichmentPanel", () => ({
  EnrichmentPanel: () => <div data-testid="enrichment-panel" />,
}));

import { VideoDetail } from "./VideoDetail";

const VIDEO_TABS = [
  "watch",
  "enrichment",
  "transcript",
  "chapters",
  "files",
  "processing",
  "stats",
] as const;
import { I18nProvider } from "../lib/i18n";

function LocationProbe() {
  const loc = useLocation();
  return <div data-testid="loc">{loc.search}</div>;
}

function renderAt(url: string) {
  return render(
    <MemoryRouter initialEntries={[url]}>
      <I18nProvider>
        <Routes>
          <Route
            path="/videos/:videoId"
            element={
              <>
                <VideoDetail />
                <LocationProbe />
              </>
            }
          />
        </Routes>
      </I18nProvider>
    </MemoryRouter>
  );
}

function selectedTab(): HTMLElement {
  const sel = screen.getAllByRole("tab").find((el) => el.getAttribute("aria-selected") === "true");
  if (!sel) throw new Error("no selected tab");
  return sel;
}

describe("VideoDetail ?tab=", () => {
  beforeEach(() => {
    get.mockReset();
    get.mockImplementation((url: string) => {
      if (url === "/api/videos/v1") {
        return Promise.resolve({ id: "v1", filename: "a.mp4", title: "Clip", state: "ready" });
      }
      return Promise.resolve({ items: [] });
    });
  });

  it("opens the Metadata tab for ?tab=enrichment", async () => {
    renderAt("/videos/v1?tab=enrichment");
    await waitFor(() => expect(screen.getByTestId("enrichment-panel")).toBeInTheDocument());
    expect(screen.queryByTestId("context-card")).not.toBeInTheDocument();
  });

  it.each(VIDEO_TABS.map((id, i) => [id, i] as const))(
    "selects tab %s from the URL",
    async (id, index) => {
      renderAt(`/videos/v1?tab=${id}`);
      await waitFor(() => expect(screen.getAllByRole("tab")).toHaveLength(VIDEO_TABS.length));
      expect(screen.getAllByRole("tab").indexOf(selectedTab())).toBe(index);
    }
  );

  it("falls back to Watch for an unknown tab", async () => {
    renderAt("/videos/v1?tab=bogus");
    await waitFor(() => expect(screen.getByTestId("context-card")).toBeInTheDocument());
  });

  it("writes the clicked tab into the URL", async () => {
    renderAt("/videos/v1");
    await waitFor(() => expect(screen.getAllByRole("tab")).toHaveLength(VIDEO_TABS.length));
    fireEvent.click(screen.getAllByRole("tab")[VIDEO_TABS.indexOf("chapters")]!);
    await waitFor(() => expect(screen.getByTestId("loc").textContent).toBe("?tab=chapters"));
    fireEvent.click(screen.getAllByRole("tab")[0]!);
    await waitFor(() => expect(screen.getByTestId("loc").textContent).toBe(""));
  });
});
