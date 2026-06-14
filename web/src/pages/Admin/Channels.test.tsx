// Story 27.8 — channel admin UI tests. Lists channels from
// /api/channels, gates the page behind admin, opens the CRUD form with a
// mode-aware rule builder, and validates a duplicate channel number
// inline before submit.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "../../lib/i18n";
import { ToastProvider } from "@ds/components/Toast/Toast";

const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post } };
});

const { authState } = vi.hoisted(() => ({
  authState: { user: { username: "admin", is_admin: true } as { username: string; is_admin: boolean } },
}));
vi.mock("../../lib/auth", () => ({ useAuth: () => authState }));

import { Channels } from "./Channels";

const channels = {
  items: [
    { id: "c1", number: 1, name: "Movies", category: "movies", mode: "shuffle", enabled: true, sort_order: 0 },
  ],
};

function renderChannels() {
  return render(
    <I18nProvider>
      <ToastProvider>
        <Channels />
      </ToastProvider>
    </I18nProvider>
  );
}

describe("Channels admin", () => {
  beforeEach(() => {
    get.mockReset();
    post.mockReset();
    authState.user = { username: "admin", is_admin: true };
  });

  it("lists channels", async () => {
    get.mockResolvedValue(channels);
    renderChannels();
    expect(await screen.findByText("Movies")).toBeInTheDocument();
    expect(screen.getByText("Shuffle")).toBeInTheDocument();
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

  // AC2 — the rule builder adapts to the mode (shuffle shows a filter).
  it("opens the CRUD form with a mode-aware rule builder", async () => {
    const user = userEvent.setup();
    get.mockResolvedValue(channels);
    renderChannels();
    await screen.findByText("Movies");
    await user.click(screen.getByRole("button", { name: /new channel/i }));
    // shuffle is the default mode → the shuffle filter field is shown.
    expect(await screen.findByText(/shuffle filter/i)).toBeInTheDocument();
  });

  // TC1 — a duplicate channel number is flagged inline before submit.
  it("flags a duplicate channel number inline", async () => {
    const user = userEvent.setup();
    get.mockResolvedValue(channels);
    renderChannels();
    await screen.findByText("Movies");
    await user.click(screen.getByRole("button", { name: /new channel/i }));
    const numberField = await screen.findByLabelText(/channel number/i);
    await user.clear(numberField);
    await user.type(numberField, "1"); // collides with the existing channel 1
    expect(await screen.findByText(/already in use/i)).toBeInTheDocument();
  });
});
