// Story 27.8 — channel admin UI tests. Lists channels from
// /api/channels, gates the page behind admin, opens the CRUD form with a
// mode-aware rule builder, and validates a duplicate channel number
// inline before submit.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "../../lib/i18n";
import { ToastProvider } from "@ds/components/Toast/Toast";
import { MemoryRouter } from "react-router-dom";

const { get, post, patch } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), patch: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post, patch } };
});

const { authState } = vi.hoisted(() => ({
  authState: {
    user: { username: "admin", is_admin: true } as { username: string; is_admin: boolean },
  },
}));
vi.mock("../../lib/auth", () => ({ useAuth: () => authState, useOptionalAuth: () => null }));

import { Channels } from "./Channels";
import { type Deferred, deferred } from "../../test/deferred";

const channels = {
  items: [
    {
      id: "c1",
      number: 1,
      name: "Movies",
      category: "movies",
      mode: "shuffle",
      enabled: true,
      sort_order: 0,
    },
  ],
};

function renderChannels() {
  return render(
    <MemoryRouter>
      <I18nProvider>
        <ToastProvider>
          <Channels />
        </ToastProvider>
      </I18nProvider>
    </MemoryRouter>
  );
}

describe("Channels admin", () => {
  beforeEach(() => {
    get.mockReset();
    post.mockReset();
    patch.mockReset();
    authState.user = { username: "admin", is_admin: true };
  });

  it("lists channels", async () => {
    get.mockResolvedValue(channels);
    renderChannels();
    expect(await screen.findByText("Movies")).toBeInTheDocument();
    expect(screen.getByText("Shuffle")).toBeInTheDocument();
  });

  it("shows the XMLTV guide address with a copy button in the header", async () => {
    get.mockImplementation((url: string) =>
      Promise.resolve(
        url === "/api/tv/info" ? { xmltv_url: "http://192.168.1.20:8782/xmltv.xml" } : channels
      )
    );
    renderChannels();
    expect(
      await screen.findByDisplayValue("http://192.168.1.20:8782/xmltv.xml")
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /copy xmltv guide/i })).toBeInTheDocument();
  });

  it("shows an empty state when there are no channels", async () => {
    get.mockResolvedValue({ items: [] });
    renderChannels();
    expect(await screen.findByText(/no channels yet/i)).toBeInTheDocument();
  });

  // TC11 — a view-only user cannot reach the admin page.
  it("is gated behind admin", async () => {
    authState.user = { username: "viewer", is_admin: false };
    get.mockResolvedValue(channels);
    renderChannels();
    expect(await screen.findByText(/admins only|forbidden|permission/i)).toBeInTheDocument();
  });

  // AC2 — the rule builder adapts to the mode (a schedule channel gets the grid).
  it("opens the CRUD form with a mode-aware rule builder", async () => {
    const user = userEvent.setup();
    get.mockResolvedValue(channels);
    renderChannels();
    await screen.findByText("Movies");
    await user.click(screen.getByRole("button", { name: /new channel/i }));
    // schedule is the default mode: the grid asks for a first slot.
    await user.click(screen.getByRole("tab", { name: /schedule/i }));
    expect(await screen.findByText(/has no slots yet/i)).toBeInTheDocument();
    expect(screen.getByRole("group", { name: /weekly schedule/i })).toBeInTheDocument();
  });

  // TC1 — a duplicate channel number is flagged inline before submit.
  describe("a change is not undone by a read begun while it was saved", () => {
    const row = (id: string, number: number, name: string, enabled = true) => ({
      id,
      number,
      name,
      category: "movies",
      mode: "shuffle",
      enabled,
      sort_order: number,
    });
    /** Every read after the first is held for the test. */
    function holdReads(first: unknown) {
      const reads: Deferred[] = [];
      get.mockImplementation((url: string) => {
        if (!url.startsWith("/api/channels")) return Promise.resolve({});
        if (reads.length === 0 && get.mock.calls.filter((c) => c[0] === url).length === 1)
          return Promise.resolve(first);
        const d = deferred();
        reads.push(d);
        return d.promise;
      });
      return reads;
    }
    const sw = (name: string) => screen.getByRole("switch", { name: `${name} on or off` });

    it("a channel switched off stays off when a read begun during its save lands", async () => {
      const reads = holdReads({ items: [row("c1", 1, "Movies"), row("c2", 2, "News")] });
      const saveMovies = deferred();
      patch.mockImplementation((url: string) =>
        url.endsWith("/c1") ? saveMovies.promise : Promise.resolve({})
      );
      renderChannels();
      await screen.findByText("Movies");
      const user = userEvent.setup();
      await user.click(sw("Movies"));
      // News is saved at once: the list is read again while Movies is still saving
      await user.click(sw("News"));
      await waitFor(() => expect(reads).toHaveLength(1));
      await act(async () =>
        reads[0]!.resolve({ items: [row("c1", 1, "Movies"), row("c2", 2, "News", false)] })
      );
      await act(async () => saveMovies.resolve({}));
      await waitFor(() => expect(reads).toHaveLength(2));
      expect(sw("Movies")).not.toBeChecked();
      expect(sw("News")).not.toBeChecked();
    });

    it("a new order stays when a read begun during its save lands", async () => {
      const before = [row("c1", 1, "Movies"), row("c2", 2, "News"), row("c3", 3, "Kids")];
      const reads = holdReads({ items: before });
      const saveOrder = deferred();
      post.mockImplementation((url: string) =>
        url === "/api/channels/reorder" ? saveOrder.promise : Promise.resolve({})
      );
      patch.mockResolvedValue({});
      renderChannels();
      await screen.findByText("Movies");
      const names = () =>
        screen
          .getAllByRole("switch", { name: / on or off$/ })
          .map((s) => s.getAttribute("aria-label")!.replace(" on or off", ""));
      const user = userEvent.setup();
      fireEvent.keyDown(screen.getByRole("button", { name: "Move Movies" }), { key: "ArrowDown" });
      expect(post).toHaveBeenCalledWith("/api/channels/reorder", expect.any(Array));
      expect(names()).toEqual(["News", "Movies", "Kids"]);
      // Kids is saved at once: the list is read again while the order is still saving
      await user.click(sw("Kids"));
      await waitFor(() => expect(reads).toHaveLength(1));
      await act(async () =>
        reads[0]!.resolve({ items: [before[0], before[1], row("c3", 3, "Kids", false)] })
      );
      await act(async () => saveOrder.resolve(undefined));
      await waitFor(() => expect(reads).toHaveLength(2));
      expect(names()).toEqual(["News", "Movies", "Kids"]);
    });
  });

  it("flags a duplicate channel number inline", async () => {
    const user = userEvent.setup();
    get.mockResolvedValue(channels);
    renderChannels();
    await screen.findByText("Movies");
    await user.click(screen.getByRole("button", { name: /new channel/i }));
    const numberField = await screen.findByLabelText(/channel number/i);
    await user.clear(numberField);
    await user.type(numberField, "1"); // collides with the existing channel 1
    expect(await screen.findByText(/already used by movies/i)).toBeInTheDocument();
  });
});
