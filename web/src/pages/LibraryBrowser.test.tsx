// Story 11.1 — Library browser speaks the real /api/videos contract:
//   ?sort=&limit=60[&library=&language=&cursor=]  ->  { items, next }
// The server field is `next` (NOT `next_cursor`); pagination must
// advance using it. Grid/list toggle persists per-user.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, act } from "@testing-library/react";
import { MemoryRouter, Routes, Route, useNavigate } from "react-router-dom";

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
import { ToastProvider } from "@ds/components/Toast/Toast";

function renderLib() {
  return render(
    <MemoryRouter initialEntries={["/library"]}>
      <I18nProvider>
        <ToastProvider>
          <Routes>
            <Route path="/library" element={<LibraryBrowser />} />
          </Routes>
        </ToastProvider>
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
    // every app's default: recently added first
    expect(url).toContain("sort=created_at");
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
        return Promise.resolve({
          items: [{ id: "v2", filename: "b.mp4", state: "ready" }],
          next: null,
        });
      }
      return Promise.resolve({
        items: [{ id: "v1", filename: "a.mp4", state: "ready" }],
        next: "CURSOR_2",
      });
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

  describe("LibraryBrowser load more (test-cases/library.md)", () => {
    const v1 = { id: "v1", filename: "a.mp4", state: "ready" };
    const v2 = { id: "v2", filename: "b.mp4", state: "ready" };
    const videoCalls = () =>
      get.mock.calls.map((c) => c[0] as string).filter((u) => u.startsWith("/api/videos?"));

    function serve(page1: unknown, page2: () => Promise<unknown>) {
      get.mockImplementation((url: string) => {
        if (url.includes("/api/channels/now"))
          return Promise.resolve({ server_time: "", items: [] });
        return url.includes("cursor=") ? page2() : Promise.resolve(page1);
      });
    }

    it("LIB-16: Load more shows the count, sends one request, keeps params, hides at the end", async () => {
      let release: (v: unknown) => void = () => {};
      serve({ items: [v1], next: "CURSOR_2" }, () => new Promise((r) => (release = r)));
      renderLib();
      const more = await screen.findByRole("button", { name: /load more 1/i });
      await act(async () => {
        more.click();
        more.click();
      });
      expect(videoCalls().filter((u) => u.includes("cursor=CURSOR_2"))).toHaveLength(1);
      const page2 = new URLSearchParams(videoCalls()[1].split("?")[1]);
      expect(page2.get("sort")).toBe("created_at");
      expect(page2.get("limit")).toBe("60");
      await act(async () => release({ items: [v2], next: null }));
      expect(await screen.findByText("b.mp4")).toBeInTheDocument();
      expect(screen.getByText("a.mp4")).toBeInTheDocument();
      expect(screen.queryByRole("button", { name: /load more/i })).not.toBeInTheDocument();
    });

    it("LIB-17: a page that repeats loaded videos does not duplicate cards", async () => {
      serve({ items: [v1, v2], next: "C2" }, () =>
        Promise.resolve({ items: [v2, v1], next: null })
      );
      renderLib();
      const more = await screen.findByRole("button", { name: /load more/i });
      await act(async () => more.click());
      await waitFor(() =>
        expect(screen.queryByRole("button", { name: /load more/i })).not.toBeInTheDocument()
      );
      expect(screen.getAllByText("a.mp4")).toHaveLength(1);
      expect(screen.getAllByText("b.mp4")).toHaveLength(1);
    });

    it("LIB-18: a failed Load more keeps loaded videos, shows the error and keeps the button", async () => {
      const { ApiError } = await vi.importActual<typeof import("../lib/api")>("../lib/api");
      serve({ items: [v1], next: "C2" }, () =>
        Promise.reject(new ApiError({ type: "about:blank", title: "boom", status: 500 }))
      );
      renderLib();
      const more = await screen.findByRole("button", { name: /load more/i });
      await act(async () => more.click());
      expect(await screen.findByText("boom")).toBeInTheDocument();
      expect(screen.getByText("a.mp4")).toBeInTheDocument();
      expect(screen.getByRole("button", { name: /load more/i })).toBeInTheDocument();
    });

    it("LIB-16: a Load more answer that arrives after the sort changed is dropped", async () => {
      let release: (v: unknown) => void = () => {};
      get.mockImplementation((url: string) => {
        if (url.includes("/api/channels/now"))
          return Promise.resolve({ server_time: "", items: [] });
        if (url.includes("cursor=")) return new Promise((r) => (release = r));
        return Promise.resolve(
          url.includes("sort=title")
            ? { items: [{ id: "t1", filename: "title-sorted.mp4", state: "ready" }], next: null }
            : { items: [v1], next: "C2" }
        );
      });
      render(
        <MemoryRouter initialEntries={["/library"]}>
          <I18nProvider>
            <ToastProvider>
              <Routes>
                <Route path="/library" element={<LibraryBrowser />} />
              </Routes>
              <SortLink />
            </ToastProvider>
          </I18nProvider>
        </MemoryRouter>
      );
      const more = await screen.findByRole("button", { name: /load more/i });
      await act(async () => more.click());
      await act(async () => screen.getByRole("button", { name: "sort by title" }).click());
      expect(await screen.findByText("title-sorted.mp4")).toBeInTheDocument();
      await act(async () => release({ items: [v2], next: "OLD_SORT_CURSOR" }));
      expect(screen.queryByText("b.mp4")).not.toBeInTheDocument();
      expect(screen.queryByRole("button", { name: /load more/i })).not.toBeInTheDocument();
    });

    it("LIB-13: ?sort=constructor falls back to recently added", async () => {
      serve({ items: [], next: null }, () => Promise.resolve({ items: [], next: null }));
      render(
        <MemoryRouter initialEntries={["/library?sort=constructor"]}>
          <I18nProvider>
            <ToastProvider>
              <Routes>
                <Route path="/library" element={<LibraryBrowser />} />
              </Routes>
            </ToastProvider>
          </I18nProvider>
        </MemoryRouter>
      );
      await waitFor(() => expect(videoCalls()).toHaveLength(1));
      expect(videoCalls()[0]).toContain("sort=created_at");
    });
  });
});

function SortLink() {
  const navigate = useNavigate();
  return (
    <button type="button" onClick={() => navigate("/library?sort=title")}>
      sort by title
    </button>
  );
}
