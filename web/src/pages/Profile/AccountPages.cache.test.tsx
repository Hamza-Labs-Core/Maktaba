// The account pages keep their last answer (lib/usePageData): a return to
// Sessions or API tokens shows the last list at once and reads it again
// quietly, for the same user only; a revoke changes the kept answer too, so a
// return never shows what was revoked as it was. The history keeps every
// page read so far.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { get, del } = vi.hoisted(() => ({ get: vi.fn(), del: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, delete: del } };
});

import { renderPage, adminUser, memberUser } from "../../test/pageHarness";
import { Sessions } from "./Sessions";
import { APITokens } from "./APITokens";
import { WatchHistory, HISTORY_PAGE } from "./WatchHistory";

const S1 = {
  id: "s1",
  created_at: "2026-10-01T09:00:00Z",
  last_seen_at: "2026-10-19T07:00:00Z",
  expires_at: "2026-11-01T09:00:00Z",
  ip: "192.168.1.20",
  user_agent: "Firefox on the Mac",
  current: true,
};
const S2 = { ...S1, id: "s2", ip: "10.0.0.5", user_agent: "Maktaba-iOS", current: false };
const T1 = {
  id: "t1",
  name: "CLI laptop",
  prefix: "ab12cd",
  scopes: [],
  last_used_at: null,
  expires_at: null,
  created_at: "2026-10-01T09:00:00Z",
  revoked_at: null,
};

/** GET `url` answers `body` once, then never (a quiet re-read still under way). */
function serveOnce(url: string, body: unknown) {
  let first = true;
  get.mockImplementation((u: string) => {
    if (u !== url) return Promise.reject(new Error(`unmocked GET ${u}`));
    if (!first) return new Promise(() => {});
    first = false;
    return Promise.resolve(body);
  });
}

const busy = () => document.querySelector('[aria-busy="true"]');
const reads = (url: string) => get.mock.calls.filter(([u]) => u === url).length;

async function leaveAndReturn() {
  const user = userEvent.setup();
  await user.click(screen.getByRole("link", { name: "go elsewhere" }));
  expect(screen.getByText("elsewhere")).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "go back" }));
}

describe("account pages: the kept answer", () => {
  beforeEach(() => {
    get.mockReset();
    del.mockReset();
  });

  it("a return to Sessions shows the last list at once and reads it again quietly", async () => {
    serveOnce("/api/me/sessions", { items: [S1, S2] });
    renderPage(<Sessions />, { path: "/account/sessions", user: memberUser });
    expect(await screen.findByText("Maktaba-iOS")).toBeInTheDocument();

    await leaveAndReturn();
    // the re-read never answers: what shows is the kept list
    expect(screen.getByText("Maktaba-iOS")).toBeInTheDocument();
    expect(busy()).toBeNull();
    expect(reads("/api/me/sessions")).toBe(2);
  });

  it("another user never sees the kept list", async () => {
    serveOnce("/api/me/sessions", { items: [S1, S2] });
    const first = renderPage(<Sessions />, { path: "/account/sessions", user: memberUser });
    expect(await screen.findByText("Maktaba-iOS")).toBeInTheDocument();
    first.unmount();

    renderPage(<Sessions />, { path: "/account/sessions", user: adminUser });
    expect(screen.queryByText("Maktaba-iOS")).toBeNull();
    expect(busy()).not.toBeNull();
  });

  it("a revoked session is gone from the kept list: a return does not show it", async () => {
    serveOnce("/api/me/sessions", { items: [S1, S2] });
    del.mockResolvedValue(undefined);
    renderPage(<Sessions />, { path: "/account/sessions", user: memberUser });
    await screen.findByText("Maktaba-iOS");
    await userEvent.setup().click(screen.getByRole("button", { name: "Revoke" }));
    // gone at once, before the re-read (which never answers) is in
    await waitFor(() => expect(screen.queryByText("Maktaba-iOS")).toBeNull());

    await leaveAndReturn();
    expect(screen.getByText("Firefox on the Mac")).toBeInTheDocument();
    expect(screen.queryByText("Maktaba-iOS")).toBeNull();
    expect(busy()).toBeNull();
  });

  it("a revoked token shows as revoked on a return", async () => {
    serveOnce("/api/me/tokens", { items: [T1] });
    del.mockResolvedValue(undefined);
    renderPage(<APITokens />, { path: "/account/tokens", user: memberUser });
    await screen.findByText("CLI laptop");
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Revoke" }));
    const confirm = await screen.findByRole("dialog");
    await user.click(within(confirm).getByRole("button", { name: "Revoke" }));
    await waitFor(() => expect(del).toHaveBeenCalledWith("/api/me/tokens/t1"));

    await leaveAndReturn();
    const row = screen.getByText("CLI laptop").closest("tr") as HTMLElement;
    expect(within(row).getByText("Revoked")).toBeInTheDocument();
    expect(within(row).queryByRole("button", { name: "Revoke" })).toBeNull();
    expect(busy()).toBeNull();
  });

  it("a return to the history shows every page read so far and reads them all again", async () => {
    const hist = (i: number) => ({
      video_id: `v${i}`,
      title: `Video ${i}`,
      times_watched: 1,
      total_watch_sec: 300,
      best_percent: 50,
      last_watched_at: "2026-03-01T12:00:00Z",
      completed: false,
    });
    get.mockImplementation((url: string) => {
      const offset = Number(new URL(url, "http://x").searchParams.get("offset") ?? 0);
      const items =
        offset === 0 ? Array.from({ length: HISTORY_PAGE }, (_, i) => hist(i)) : [hist(999)];
      return Promise.resolve({ items, limit: HISTORY_PAGE, offset });
    });
    renderPage(<WatchHistory />, { path: "/account/history", user: memberUser });
    await screen.findByText("Video 0");
    await userEvent.setup().click(screen.getByRole("button", { name: "Load more" }));
    await screen.findByText("Video 999");

    get.mockClear();
    await leaveAndReturn();
    expect(screen.getByText("Video 999")).toBeInTheDocument();
    expect(busy()).toBeNull();
    // the re-read covers both pages, not just the first
    await waitFor(() =>
      expect(get).toHaveBeenCalledWith(
        `/api/me/history?limit=${HISTORY_PAGE}&offset=${HISTORY_PAGE}`
      )
    );
    expect(screen.getByText("Video 999")).toBeInTheDocument();
  });
});
