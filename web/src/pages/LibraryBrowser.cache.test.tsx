// The library grid kept for a return (lib/pageCache): the same library,
// filters and sort come back at once with every page loaded, and are read
// again quietly; another filter, or another user, starts afresh.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { act, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderPage, memberUser, adminUser } from "../test/pageHarness";

const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock("../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../lib/api")>("../lib/api");
  return { ...actual, api: { ...actual.api, get, post } };
});

import { ToastProvider } from "@ds/components/Toast/Toast";
import { LibraryBrowser } from "./LibraryBrowser";

const video = (id: string) => ({ id, title: `Video ${id}`, filename: `${id}.mp4`, state: "ready" });

/** /api/videos answers with `answer(url)`; a pending promise holds it. */
function serve(answer: (url: string) => Promise<unknown>) {
  get.mockImplementation((url: string) =>
    url.startsWith("/api/videos?") ? answer(url) : Promise.reject(new Error(`unmocked GET ${url}`))
  );
}

// two pages: the first without a cursor, then the rest
const paged = (url: string) =>
  Promise.resolve(
    new URL(url, "http://x").searchParams.get("cursor")
      ? { items: [video("v3")], next: null }
      : { items: [video("v1"), video("v2")], next: "c2" }
  );

const videoCalls = () =>
  get.mock.calls.map((c) => c[0] as string).filter((u) => u.startsWith("/api/videos?"));
const busy = () => document.querySelector('[aria-busy="true"]');

function renderGrid(path = "/videos", user = memberUser) {
  return renderPage(
    <ToastProvider>
      <LibraryBrowser />
    </ToastProvider>,
    { path, route: "/videos", user }
  );
}

describe("LibraryBrowser kept for a return", () => {
  beforeEach(() => {
    get.mockReset();
    post.mockReset();
  });

  it("the same list comes back at once with its loaded pages, and is read again", async () => {
    serve(paged);
    renderGrid("/videos?sort=title");
    expect(await screen.findByText("Video v1")).toBeInTheDocument();
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: /load more/i }));
    expect(await screen.findByText("Video v3")).toBeInTheDocument();

    await user.click(screen.getByRole("link", { name: "go elsewhere" }));
    // the quiet re-read never answers: what shows is the kept list
    serve(() => new Promise(() => {}));
    await user.click(screen.getByRole("button", { name: "go back" }));
    expect(screen.getByText("Video v1")).toBeInTheDocument();
    expect(screen.getByText("Video v3")).toBeInTheDocument();
    expect(busy()).toBeNull();
    // read again as long as it was shown (at least a page)
    const last = new URL(videoCalls().at(-1)!, "http://x").searchParams;
    expect(last.get("sort")).toBe("title");
    expect(last.get("cursor")).toBeNull();
    expect(last.get("limit")).toBe("60");
  });

  it("a mark saved while the kept list is read again stays on its cards", async () => {
    serve(() => Promise.resolve({ items: [video("v1"), video("v2")], next: null }));
    post.mockImplementation((path: string, body: { video_ids?: string[] }) =>
      path === "/api/me/watched" || path === "/api/me/favorites"
        ? Promise.resolve({ updated: body.video_ids?.length ?? 0 })
        : Promise.reject(new Error(`unmocked POST ${path}`))
    );
    renderGrid("/videos?sort=title");
    expect(await screen.findByText("Video v1")).toBeInTheDocument();
    const user = userEvent.setup();
    await user.click(screen.getByRole("link", { name: "go elsewhere" }));

    // the quiet re-read is answered as the home has it now (no marks), and held
    // until both marks are saved
    let answer: (v: unknown) => void = () => {};
    serve(() => new Promise((resolve) => (answer = resolve)));
    await user.click(screen.getByRole("button", { name: "go back" }));
    const before = { items: [video("v1"), video("v2")], next: null };
    await user.click(screen.getByRole("button", { name: "Select" }));
    await user.click(screen.getByRole("checkbox", { name: "Select Video v1" }));
    await user.click(screen.getByRole("button", { name: "Add to favorites" }));
    expect(await screen.findByText("Added 1 to your favorites.")).toBeInTheDocument();
    await user.click(screen.getByRole("checkbox", { name: "Select Video v1" }));
    await user.click(screen.getByRole("button", { name: "Mark watched" }));
    expect(await screen.findByText("Marked 1 as watched.")).toBeInTheDocument();

    // the read is applied, and keeps the marks
    await act(async () => answer(before));
    const card = screen.getByText("Video v1").closest("li")!;
    expect(within(card).getByRole("img", { name: "Favorite" })).toBeInTheDocument();
    expect(within(card).getByRole("img", { name: "Watched" })).toBeInTheDocument();
    const other = screen.getByText("Video v2").closest("li")!;
    expect(within(other).queryByRole("img", { name: "Favorite" })).toBeNull();
  });

  it("another filter is another list: nothing kept shows", async () => {
    serve(paged);
    const first = renderGrid("/videos?sort=title");
    expect(await screen.findByText("Video v1")).toBeInTheDocument();
    first.unmount();

    serve(() => new Promise(() => {}));
    renderGrid("/videos?sort=title&favorite=true");
    expect(screen.queryByText("Video v1")).toBeNull();
    expect(busy()).not.toBeNull();
  });

  it("another user never sees the kept list", async () => {
    serve(paged);
    const first = renderGrid("/videos?sort=title");
    expect(await screen.findByText("Video v1")).toBeInTheDocument();
    first.unmount();

    serve(() => new Promise(() => {}));
    renderGrid("/videos?sort=title", adminUser);
    expect(screen.queryByText("Video v1")).toBeNull();
    expect(busy()).not.toBeNull();
  });
});
