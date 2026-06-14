// Story 11.1 — Library browser speaks the real /api/videos contract:
//   ?sort=&limit=60[&library=&language=&cursor=]  ->  { items, next }
// The server field is `next` (NOT `next_cursor`); pagination must
// advance using it. Grid/list toggle persists per-user.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, act } from "@testing-library/react";
import { MemoryRouter, Routes, Route } from "react-router-dom";

const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock("../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../lib/api")>("../lib/api");
  return { ...actual, api: { ...actual.api, get, post } };
});
vi.mock("../lib/auth", () => ({
  useAuth: () => ({ user: { id: "u1", username: "x", is_admin: false } }),
}));

import { LibraryBrowser } from "./LibraryBrowser";
import { I18nProvider } from "../lib/i18n";

function renderLib() {
  return render(
    <MemoryRouter initialEntries={["/library"]}>
      <I18nProvider>
        <Routes>
          <Route path="/library" element={<LibraryBrowser />} />
        </Routes>
      </I18nProvider>
    </MemoryRouter>
  );
}

describe("LibraryBrowser", () => {
  beforeEach(() => {
    get.mockReset();
    post.mockReset();
  });

  it("requests /api/videos with sort+limit and renders items", async () => {
    // The home screen also mounts the "What's On Now" rail, which reads
    // /api/channels/now — route it to an empty payload so it hides.
    get.mockImplementation((url: string) =>
      url.includes("/api/channels/now")
        ? Promise.resolve({ server_time: "", items: [] })
        : Promise.resolve({
            items: [
              { id: "v1", title: "أهلا", filename: "a.mp4", state: "ready", duration_sec: 65 },
            ],
            next: null,
          })
    );
    renderLib();
    await waitFor(() => expect(get).toHaveBeenCalled());
    const url = get.mock.calls.map((c) => c[0] as string).find((u) => u.includes("/api/videos?"));
    expect(url).toBeDefined();
    expect(url).toContain("sort=updated_at");
    expect(url).toContain("limit=60");
    await waitFor(() => expect(screen.getByText("أهلا")).toBeInTheDocument());
  });

  it("paginates using the server `next` cursor (not next_cursor)", async () => {
    // Route the rail's /api/channels/now to empty; serve the videos pages
    // by whether the request carries the cursor (the rail call would
    // otherwise consume one of the sequential once-mocks).
    get.mockImplementation((url: string) => {
      if (url.includes("/api/channels/now")) {
        return Promise.resolve({ server_time: "", items: [] });
      }
      if (url.includes("cursor=CURSOR_2")) {
        return Promise.resolve({ items: [{ id: "v2", filename: "b.mp4", state: "ready" }], next: null });
      }
      return Promise.resolve({ items: [{ id: "v1", filename: "a.mp4", state: "ready" }], next: "CURSOR_2" });
    });
    renderLib();
    const more = await screen.findByRole("button", { name: /load more/i });
    await act(async () => {
      more.click();
    });
    await waitFor(() => {
      const cursorCall = get.mock.calls
        .map((c) => c[0] as string)
        .find((u) => u.includes("cursor=CURSOR_2"));
      expect(cursorCall).toBeDefined();
    });
    await waitFor(() => expect(screen.getByText("b.mp4")).toBeInTheDocument());
  });

  it("shows the first-run empty state when there are no videos", async () => {
    get.mockResolvedValue({ items: [], next: null });
    renderLib();
    await waitFor(() => expect(screen.getByText(/no videos yet/i)).toBeInTheDocument());
  });
});
