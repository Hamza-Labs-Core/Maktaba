// Filler pools: counts and running time per pool, clips added through the
// library search (not typed ids), removed, and the plan's pools flagged.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "../../lib/i18n";
import { ToastProvider } from "@ds/components/Toast/Toast";
import { MemoryRouter } from "react-router-dom";

const { get, post, del } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), del: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post, delete: del } };
});
vi.mock("../../lib/auth", () => ({
  useAuth: () => ({ user: { username: "admin", is_admin: true } }),
}));

import { FillerPools } from "./FillerPools";

const pools = {
  items: [
    {
      id: "p1",
      name: "News clips",
      external_key: "news.topic",
      items: [
        {
          id: "i1",
          pool_id: "p1",
          video_id: "v1",
          type: "filler",
          title: "Headlines",
          duration_ms: 90_000,
        },
        {
          id: "i2",
          pool_id: "p1",
          video_id: "v2",
          type: "bumper",
          title: "Sting",
          duration_ms: 30_000,
        },
      ],
    },
    { id: "p2", name: "Bumpers", items: [] },
  ],
};

describe("Filler pools", () => {
  beforeEach(() => {
    for (const f of [get, post, del]) f.mockReset();
    get.mockImplementation((path: string) =>
      path.startsWith("/api/channels/sources")
        ? Promise.resolve({
            kind: "video",
            items: [
              { kind: "video", id: "v9", name: "Station ident", count: 1, duration_ms: 15_000 },
            ],
          })
        : Promise.resolve(pools)
    );
  });

  it("lists pools with counts and adds a clip from the search", async () => {
    const user = userEvent.setup();
    render(
      <I18nProvider>
        <ToastProvider>
          <FillerPools />
        </ToastProvider>
      </I18nProvider>
    );
    const toggle = await screen.findByRole("button", { name: /news clips/i });
    expect(toggle).toHaveTextContent("2 clips · 2 min");
    await user.click(toggle);
    expect(screen.getByText(/replaces its clips/i)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Headlines" })).toHaveAttribute(
      "href",
      "/videos/v1/watch?t=0"
    );
    post.mockResolvedValue({ items: [] });
    await user.click(await screen.findByRole("button", { name: /station ident/i }));
    expect(post).toHaveBeenCalledWith("/api/filler/pools/p1/items", {
      items: [{ video_id: "v9", type: "filler" }],
    });
    del.mockResolvedValue(undefined);
    const sting = screen.getByRole("link", { name: "Sting" }).closest("li")!;
    await user.click(within(sting).getByRole("button", { name: /remove sting/i }));
    expect(del).toHaveBeenCalledWith("/api/filler/items/i2");
  });

  it("marks what a TV draft change holds and says where a save went (33.12 AC6)", async () => {
    const user = userEvent.setup();
    const drafted = {
      items: [
        {
          id: "p3",
          name: "Station ids",
          draft: { change_id: "c9", number: 12, op: "added" },
          items: [
            {
              id: "i9",
              pool_id: "p3",
              video_id: "v9",
              type: "station_id",
              title: "Ident",
              duration_ms: 15_000,
              pending: true,
            },
          ],
        },
      ],
    };
    get.mockImplementation((path: string) =>
      path.startsWith("/api/channels/sources")
        ? Promise.resolve({ kind: "video", items: [] })
        : Promise.resolve(drafted)
    );
    render(
      <MemoryRouter>
        <I18nProvider>
          <ToastProvider>
            <FillerPools />
          </ToastProvider>
        </I18nProvider>
      </MemoryRouter>
    );
    const badge = await screen.findByTestId("pool-draft-p3");
    expect(badge).toHaveTextContent("In draft change #12");
    expect(badge).toHaveAttribute("href", "/admin/live-tv/changes/c9");
    await user.click(screen.getByRole("button", { name: /station ids/i }));
    expect(screen.getByText("Not on the air yet")).toBeInTheDocument();
    del.mockResolvedValue({ item_id: "i9", draft_change: { id: "c9", number: 12, state: "draft" } });
    await user.click(screen.getByRole("button", { name: /remove ident/i }));
    expect(await screen.findByText(/Saved to draft change #12/)).toBeInTheDocument();
  });
});
