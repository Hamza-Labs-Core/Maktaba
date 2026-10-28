// Epic 45 Phase 4 (story 45.24) — the library page's Series and Collections
// tabs: their addresses (?tab=series, ?tab=collections, Back between tabs),
// the Series sort and Load more, the Collections kind filter with its counts,
// the Automatic badge, admins' New collection, the old Collections page's
// redirect, and an automatic collection's read-only page.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Route, useLocation } from "react-router-dom";
import { ApiError } from "../../lib/api";
import { renderPage, memberUser, adminUser } from "../../test/pageHarness";
import en from "../../i18n/en.json";

const { get } = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get } };
});

import { LibraryPage } from "./LibraryPage";
import { CollectionsRedirect } from "../Collections/Collections";
import { AutoCollectionPage } from "../Collections/AutoCollectionPage";

const ID = "3f1c2a4e-0000-4000-8000-000000000001";
const HUB = {
  library: { id: ID, name: "د. عثمان الخميس", videos: 300 },
  rows: {
    continue_watching: [],
    recently_added: [{ id: "a1", title: "Lesson", filename: "a1.mp4" }],
    recently_watched: [],
    most_watched: [],
    series: [],
    years: [],
  },
};
const series = (id: string, name: string, extra: Record<string, unknown> = {}) => ({
  id,
  name,
  episodes: 10,
  watched: 0,
  in_progress: 0,
  poster_video_id: `${id}-v`,
  ...extra,
});
const col = (kind: string, id: string, name: string, extra: Record<string, unknown> = {}) => ({
  kind,
  id,
  name,
  videos: 5,
  poster_video_ids: [],
  automatic: kind === "topic" || kind === "clip",
  ...extra,
});
const COUNTS = { all: 4, topic: 1, filler: 1, standalone: 1, curated: 1 };
const ALL = {
  items: [
    col("topic", "t1", "الصيام"),
    col("clip", "g1", "فوائد", { subkind: "filler" }),
    col("clip", "g2", "متنوعة", { subkind: "standalone" }),
    col("curated", "c1", "مختارات"),
  ],
  counts: COUNTS,
  total: 4,
  next: null,
};

function serve(over: Record<string, unknown> = {}) {
  get.mockImplementation((url: string) => {
    if (url in over) {
      const v = over[url];
      return v instanceof Error ? Promise.reject(v) : Promise.resolve(v);
    }
    if (url === `/api/libraries/${ID}/hub`) return Promise.resolve(HUB);
    if (url === "/api/me/pinned-libraries") return Promise.resolve({ library_ids: [], items: [] });
    if (url === `/api/libraries/${ID}/series?sort=name`)
      return Promise.resolve({
        items: [series("s1", "الفقه", { watched: 5 }), series("s2", "بلوغ المرام")],
        total: 3,
        next: 2,
      });
    if (url === `/api/libraries/${ID}/series?sort=name&offset=2`)
      return Promise.resolve({ items: [series("s3", "التفسير")], total: 3, next: null });
    if (url === `/api/libraries/${ID}/series?sort=newest`)
      return Promise.resolve({ items: [series("s2", "بلوغ المرام")], total: 1, next: null });
    if (url === `/api/libraries/${ID}/collections`) return Promise.resolve(ALL);
    if (url === `/api/libraries/${ID}/collections?kind=topic`)
      return Promise.resolve({ items: [ALL.items[0]], counts: COUNTS, total: 1, next: null });
    return Promise.reject(new Error(`unmocked GET ${url}`));
  });
}

function Loc() {
  const { pathname, search } = useLocation();
  return <span data-testid="loc">{pathname + search}</span>;
}

const renderLib = (path: string, user = memberUser) =>
  renderPage(
    <>
      <LibraryPage />
      <Loc />
    </>,
    { path, route: "/library/:libraryId", user }
  );

const loc = () => screen.getAllByTestId("loc").at(-1)!;

describe("Library page tabs", () => {
  beforeEach(() => {
    get.mockReset();
  });

  it("has Overview, Browse, Series and Collections, each its own address; Back returns", async () => {
    serve();
    renderLib(`/library/${ID}`);
    const user = userEvent.setup();
    const tabs = await screen.findByRole("tablist", { name: en["libraryPage.tabs"] });
    expect(
      within(tabs)
        .getAllByRole("tab")
        .map((t) => t.textContent)
    ).toEqual([
      en["libraryPage.tab.overview"],
      en["libraryPage.tab.browse"],
      en["libraryPage.tab.series"],
      en["libraryPage.tab.collections"],
    ]);
    await user.click(within(tabs).getByRole("tab", { name: en["libraryPage.tab.series"] }));
    expect(loc()).toHaveTextContent(`/library/${ID}?tab=series`);
    expect(await screen.findByText("الفقه")).toBeInTheDocument();
    await user.click(within(tabs).getByRole("tab", { name: en["libraryPage.tab.collections"] }));
    expect(loc()).toHaveTextContent(`/library/${ID}?tab=collections`);
    expect(await screen.findByText("الصيام")).toBeInTheDocument();
    // Back: the Series tab, then the Overview
    await user.click(screen.getByRole("button", { name: "go back" }));
    expect(loc()).toHaveTextContent(`/library/${ID}?tab=series`);
    expect(screen.getByRole("tab", { name: en["libraryPage.tab.series"] })).toHaveAttribute(
      "aria-selected",
      "true"
    );
    await user.click(screen.getByRole("button", { name: "go back" }));
    expect(loc()).toHaveTextContent(new RegExp(`/library/${ID}$`));
    // the header has no Collections button: the only link is the row's title
    const links = screen.getAllByRole("link", { name: en["collections.title"] });
    expect(links.every((a) => a.closest("[data-testid='row-collections']"))).toBe(true);
  });

  it("Series: cards with episodes and progress, the sort in the URL, Load more", async () => {
    serve();
    renderLib(`/library/${ID}?tab=series`);
    const user = userEvent.setup();
    const grid = await screen.findByTestId("series-grid");
    const fiqh = within(grid).getByText("الفقه").closest("a")!;
    expect(fiqh).toHaveAttribute("href", "/series/s1");
    expect(within(fiqh).getByText("10 episodes")).toBeInTheDocument();
    expect(within(fiqh).getByRole("progressbar")).toHaveAccessibleName("5 of 10 watched");
    expect(screen.getByText("3 series")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: /Load more/ }));
    expect(await within(grid).findByText("التفسير")).toBeInTheDocument();
    expect(within(grid).getAllByRole("link")).toHaveLength(3);
    expect(screen.queryByRole("button", { name: /Load more/ })).toBeNull();

    await user.selectOptions(screen.getByRole("combobox", { name: en["series.sort"] }), "newest");
    expect(loc()).toHaveTextContent(`/library/${ID}?tab=series&by=newest`);
    await waitFor(() => expect(screen.queryByText("الفقه")).toBeNull());
    expect(screen.getByText("بلوغ المرام")).toBeInTheDocument();
  });

  it("Collections: one heading per kind, Automatic on the pipeline's, a kind filter with counts", async () => {
    serve();
    renderLib(`/library/${ID}?tab=collections`);
    const user = userEvent.setup();
    const topics = await screen.findByTestId("collections-topic");
    expect(within(topics).getByRole("heading", { name: /Topics/ })).toBeInTheDocument();
    expect(within(topics).getByText("الصيام").closest("a")).toHaveAttribute(
      "href",
      `/library/${ID}/collections/topic/t1`
    );
    expect(within(topics).getByText(en["collections.automatic"])).toBeInTheDocument();
    const clips = screen.getByTestId("collections-filler");
    expect(within(clips).getByText("فوائد").closest("a")).toHaveAttribute(
      "href",
      `/library/${ID}/collections/clip/g1`
    );
    expect(screen.getByTestId("collections-standalone")).toHaveTextContent("متنوعة");
    const curated = screen.getByTestId("collections-curated");
    expect(within(curated).getByText("مختارات").closest("a")).toHaveAttribute(
      "href",
      "/collections/c1"
    );
    expect(within(curated).queryByText(en["collections.automatic"])).toBeNull();
    // a viewer gets no New collection
    expect(screen.queryByLabelText(en["collections.newName"])).toBeNull();

    const filter = screen.getByRole("group", { name: en["collections.kindFilter"] });
    expect(
      within(filter)
        .getAllByRole("button")
        .map((b) => b.textContent)
    ).toEqual(["All 4", "Topics 1", "Clips 1", "Stand-alone 1", "Curated 1"]);
    await user.click(within(filter).getByRole("button", { name: /Topics/ }));
    expect(loc()).toHaveTextContent(`/library/${ID}?tab=collections&kind=topic`);
    await waitFor(() => expect(screen.queryByText("مختارات")).toBeNull());
    expect(screen.getByText("الصيام")).toBeInTheDocument();
    expect(within(filter).getByRole("button", { name: /Topics/ })).toHaveAttribute(
      "aria-pressed",
      "true"
    );
    expect(get).toHaveBeenCalledWith(`/api/libraries/${ID}/collections?kind=topic`);
  });

  it("Collections: admins keep New collection", async () => {
    serve();
    renderLib(`/library/${ID}?tab=collections`, adminUser);
    expect(await screen.findByLabelText(en["collections.newName"])).toBeInTheDocument();
    expect(screen.getByRole("button", { name: en["collections.create"] })).toBeDisabled();
  });

  it("the old Collections page sends you to the tab", async () => {
    serve();
    renderPage(
      <>
        <CollectionsRedirect />
        <Loc />
      </>,
      {
        path: `/library/${ID}/collections`,
        route: "/library/:libraryId/collections",
        extraRoutes: (
          <Route
            path="/library/:libraryId"
            element={
              <>
                <LibraryPage />
                <Loc />
              </>
            }
          />
        ),
      }
    );
    expect(await screen.findByText("الصيام")).toBeInTheDocument();
    expect(loc()).toHaveTextContent(`/library/${ID}?tab=collections`);
    expect(screen.getByRole("tab", { name: en["libraryPage.tab.collections"] })).toHaveAttribute(
      "aria-selected",
      "true"
    );
  });
});

describe("AutoCollectionPage", () => {
  beforeEach(() => {
    get.mockReset();
  });

  const PAGE = {
    collection: {
      kind: "topic",
      id: "t1",
      name: "الصيام",
      library: { id: ID, name: "د. عثمان الخميس" },
      videos: 3,
      automatic: true,
    },
    items: [
      { id: "v2", title: "الدرس الأول", duration_sec: 600, completed: true },
      { id: "v1", title: "الدرس الثاني", duration_sec: 900 },
    ],
    total: 3,
    next: 2,
  };
  const renderAuto = (kind = "topic", user = adminUser) =>
    renderPage(<AutoCollectionPage />, {
      path: `/library/${ID}/collections/${kind}/t1`,
      route: "/library/:libraryId/collections/:kind/:groupId",
      user,
    });

  it("lists its videos in order, read-only even for an admin, with Load more", async () => {
    get.mockImplementation((url: string) => {
      if (url === `/api/libraries/${ID}/collections/topic/t1`) return Promise.resolve(PAGE);
      if (url === `/api/libraries/${ID}/collections/topic/t1?offset=2`)
        return Promise.resolve({
          ...PAGE,
          items: [{ id: "v3", title: "الدرس الثالث" }],
          next: null,
        });
      return Promise.reject(new Error(`unmocked GET ${url}`));
    });
    renderAuto();
    const user = userEvent.setup();
    expect(await screen.findByRole("heading", { level: 1, name: "الصيام" })).toBeInTheDocument();
    expect(screen.getByText(en["collections.automatic"])).toBeInTheDocument();
    expect(screen.getByText(en["collections.kind.topic"])).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /د. عثمان الخميس/ })).toHaveAttribute(
      "href",
      `/library/${ID}?tab=collections`
    );
    const titles = () =>
      screen
        .getAllByRole("listitem")
        .map((li) => within(li).queryByText(/^الدرس/)?.textContent)
        .filter(Boolean);
    expect(titles()).toEqual(["الدرس الأول", "الدرس الثاني"]);
    expect(screen.getByRole("button", { name: /Play all/ })).toBeEnabled();
    expect(screen.queryByRole("button", { name: en["collections.rename"] })).toBeNull();
    expect(screen.queryByRole("button", { name: en["collections.delete"] })).toBeNull();
    await user.click(screen.getByRole("button", { name: /Load more/ }));
    await waitFor(() => expect(titles()).toHaveLength(3));
  });

  it("is not found for a 404, or a kind that is not one", async () => {
    get.mockRejectedValue(new ApiError({ type: "about:blank", title: "nf", status: 404 }));
    const first = renderAuto("clip", memberUser);
    expect(await screen.findByText(en["collections.notFound"])).toBeInTheDocument();
    first.unmount();
    get.mockReset();
    renderAuto("promo", memberUser);
    expect(await screen.findByText(en["collections.notFound"])).toBeInTheDocument();
    expect(get).not.toHaveBeenCalled();
  });
});
