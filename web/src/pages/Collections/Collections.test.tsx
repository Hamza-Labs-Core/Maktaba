// Epic 45 Phase 3 (story 45.17) — one curated collection's page: curation
// for admins only (rename, reorder, remove, delete), a saved filter's chips
// and its Browse link, and the 404 of a collection the user may not see. The
// library's list of collections is its page's Collections tab
// (Library/LibraryTabs.test.tsx).
import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Route, useLocation } from "react-router-dom";
import { ApiError } from "../../lib/api";
import { renderPage, memberUser, adminUser } from "../../test/pageHarness";
import en from "../../i18n/en.json";

const { get, post, patch, del } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  patch: vi.fn(),
  del: vi.fn(),
}));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post, patch, delete: del } };
});

import { CollectionPage } from "./CollectionPage";

const LIB = "11111111-1111-4111-8111-111111111111";
const TAG = "22222222-2222-4222-8222-222222222222";

const MANUAL = {
  id: "c1",
  library_id: LIB,
  name: "رمضان",
  is_smart: false,
  videos: 3,
  poster_video_ids: ["v1", "v2", "v3"],
};
const SMART = {
  id: "c2",
  library_id: LIB,
  name: "Long tafsir",
  is_smart: true,
  smart_query: { filters: { duration: "long", tag: TAG, sort: "title" } },
};
const ITEMS = {
  items: ["v1", "v2", "v3"].map((id, i) => ({
    video_id: id,
    position: (i + 1) * 10,
    video: {
      id,
      title: `Lesson ${i + 1}`,
      filename: `${id}.mp4`,
      duration_sec: 600,
      state: "ready",
    },
  })),
};

const notFound = () => new ApiError({ type: "about:blank", title: "not found", status: 404 });

function serve(over: Record<string, unknown> = {}) {
  get.mockImplementation((url: string) => {
    if (url in over) {
      const v = over[url];
      return v instanceof Error ? Promise.reject(v) : Promise.resolve(v);
    }
    if (url === "/api/collections/c1") return Promise.resolve(MANUAL);
    if (url === "/api/collections/c2") return Promise.resolve(SMART);
    if (url === "/api/collections/c1/items") return Promise.resolve(ITEMS);
    if (url.startsWith("/api/videos/facets"))
      return Promise.resolve({ tags: [{ id: TAG, name: "Fiqh", videos: 2 }] });
    return Promise.reject(new Error(`unmocked GET ${url}`));
  });
}

function Loc() {
  const { pathname, search } = useLocation();
  return <span data-testid="loc">{pathname + search}</span>;
}

const renderOne = (id: string, user = memberUser) =>
  renderPage(
    <>
      <CollectionPage />
      <Loc />
    </>,
    {
      path: `/collections/${id}`,
      route: "/collections/:collectionId",
      user,
      extraRoutes: <Route path="/library/:libraryId" element={<Loc />} />,
    }
  );

describe("CollectionPage", () => {
  beforeEach(() => {
    for (const f of [get, post, patch, del]) f.mockReset();
    sessionStorage.clear();
  });

  it("shows a list in order; a viewer can play but not curate", async () => {
    serve();
    renderOne("c1");
    const items = await screen.findAllByRole("listitem");
    expect(items.map((li) => within(li).queryByText(/^Lesson/)?.textContent)).toEqual([
      "Lesson 1",
      "Lesson 2",
      "Lesson 3",
    ]);
    expect(screen.getByRole("button", { name: /Play all/ })).toBeEnabled();
    expect(screen.queryByRole("button", { name: en["collections.rename"] })).toBeNull();
    expect(screen.queryByRole("button", { name: en["collections.delete"] })).toBeNull();
    expect(screen.queryByRole("button", { name: /Lesson 2/ })).toBeTruthy(); // play from here
    expect(
      screen.queryByRole("button", { name: en["libraries.moveUp"].replace("{name}", "Lesson 2") })
    ).toBeNull();
  });

  it("lets an admin reorder, remove and rename", async () => {
    serve();
    patch.mockResolvedValue({});
    del.mockResolvedValue(undefined);
    renderOne("c1", adminUser);
    await screen.findByText("Lesson 2");
    await userEvent.click(
      screen.getByRole("button", { name: en["libraries.moveUp"].replace("{name}", "Lesson 2") })
    );
    await waitFor(() =>
      expect(patch).toHaveBeenCalledWith("/api/collections/c1", {
        items: [
          { video_id: "v2", position: 10 },
          { video_id: "v1", position: 20 },
          { video_id: "v3", position: 30 },
        ],
      })
    );
    await userEvent.click(
      screen.getByRole("button", {
        name: en["collections.removeNamed"].replace("{name}", "Lesson 3"),
      })
    );
    await waitFor(() => expect(del).toHaveBeenCalledWith("/api/collections/c1/items/v3"));
    await waitFor(() => expect(screen.queryByText("Lesson 3")).toBeNull());

    await userEvent.click(screen.getByRole("button", { name: en["collections.rename"] }));
    const field = screen.getByLabelText(en["collections.name"]);
    await userEvent.clear(field);
    await userEvent.type(field, "Ramadan 1447");
    await userEvent.click(screen.getByRole("button", { name: en["common.save"] }));
    await waitFor(() =>
      expect(patch).toHaveBeenLastCalledWith("/api/collections/c1", { name: "Ramadan 1447" })
    );
    expect(await screen.findByRole("heading", { name: "Ramadan 1447" })).toBeInTheDocument();
  });

  it("deletes after a confirmation and goes back to the library's Collections tab", async () => {
    serve();
    del.mockResolvedValue(undefined);
    renderOne("c1", adminUser);
    await userEvent.click(await screen.findByRole("button", { name: en["collections.delete"] }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByRole("button", { name: en["collections.delete"] }));
    await waitFor(() => expect(del).toHaveBeenCalledWith("/api/collections/c1"));
    await waitFor(() =>
      expect(screen.getAllByTestId("loc").at(-1)).toHaveTextContent(
        `/library/${LIB}?tab=collections`
      )
    );
  });

  it("shows a saved filter's filters, the tag by name, and opens Browse with them", async () => {
    serve();
    renderOne("c2", adminUser);
    expect(await screen.findByText("Fiqh")).toBeInTheDocument();
    expect(screen.getByText(en["browse.duration.long"])).toBeInTheDocument();
    const show = screen.getByRole("link", { name: en["collections.showVideos"] });
    const href = new URL(show.getAttribute("href")!, "http://x");
    expect(href.pathname).toBe(`/library/${LIB}`);
    expect(Object.fromEntries(href.searchParams)).toEqual({
      tab: "browse",
      duration: "long",
      tag: TAG,
      sort: "title",
    });
    const edit = screen.getByRole("link", { name: en["collections.editFilters"] });
    expect(edit.getAttribute("href")).toContain("collection=c2");
    // a saved filter has no items of its own to read
    expect(get).not.toHaveBeenCalledWith("/api/collections/c2/items");
  });

  it("answers not found for a collection the user may not see", async () => {
    serve({ "/api/collections/c1": notFound() });
    renderOne("c1");
    expect(await screen.findByText(en["collections.notFound"])).toBeInTheDocument();
  });
});
