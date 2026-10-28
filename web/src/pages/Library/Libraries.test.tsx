// Epic 45 — Libraries (`/library`): the summary cards, pin / unpin, the
// compact Pinned strip with its Reorder link (the order is set on
// /library/pinned, PinnedLibraries.test.tsx), and the shared pins store the
// sidebar reads.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ApiError } from "../../lib/api";
import { renderPage, memberUser, adminUser } from "../../test/pageHarness";
import { ToastProvider } from "@ds/components/Toast/Toast";
import en from "../../i18n/en.json";

const { get, put } = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, put } };
});

import { Libraries } from "./Libraries";
import { resetPinsStore, usePins } from "../../lib/pins";

const A = { id: "aaaaaaaa-0000-4000-8000-000000000001", name: "الحويني" };
const B = { id: "bbbbbbbb-0000-4000-8000-000000000002", name: "Lectures" };
const C = { id: "cccccccc-0000-4000-8000-000000000003", name: "Kids" };

const summary = (pinned: string[] = []) => ({
  items: [A, B, C].map((l, i) => ({
    ...l,
    videos: [6191, 47, 0][i],
    duration_sec: 1000,
    poster_video_ids: [["p1", "p2", "p3", "p4", "p5"], ["q1"], []][i],
    latest_added_at: null,
    pinned: pinned.includes(l.id),
    pin_position: pinned.indexOf(l.id) >= 0 ? pinned.indexOf(l.id) : null,
  })),
});

let pins: Array<{ id: string; name: string }>;

// GET and PUT /api/me/pinned-libraries answer the same document
const pinsDoc = () => ({
  library_ids: pins.map((p) => p.id),
  items: pins.map((p, position) => ({ ...p, position })),
});

function serve(sum: unknown = summary()) {
  get.mockImplementation((url: string) => {
    if (url === "/api/libraries/summary") return Promise.resolve(sum);
    if (url === "/api/me/pinned-libraries") return Promise.resolve(pinsDoc());
    return Promise.reject(new Error(`unmocked GET ${url}`));
  });
  put.mockImplementation((_url: string, body: { library_ids: string[] }) => {
    pins = body.library_ids.map((id) => [A, B, C].find((l) => l.id === id)!);
    return Promise.resolve(pinsDoc());
  });
}

// what the sidebar sees: the shared store
function PinsProbe() {
  const p = usePins(memberUser.id);
  return <p data-testid="probe">{(p ?? []).map((x) => x.name).join(",")}</p>;
}

function renderLibraries(user = memberUser) {
  return renderPage(
    <ToastProvider>
      <Libraries />
      <PinsProbe />
    </ToastProvider>,
    { path: "/library", user }
  );
}

const putCalls = () => put.mock.calls.map((c) => (c[1] as { library_ids: string[] }).library_ids);

describe("Libraries", () => {
  beforeEach(() => {
    get.mockReset();
    put.mockReset();
    resetPinsStore();
    pins = [];
  });

  it("shows a card per library with its mosaic, name, count and link", async () => {
    serve();
    renderLibraries();
    expect(screen.getByRole("heading", { level: 1, name: en["libraries.title"] })).toBeVisible();
    expect(screen.getByRole("link", { name: en["libraries.allVideos"] })).toHaveAttribute(
      "href",
      "/videos"
    );
    const card = (await screen.findByText("الحويني")).closest("li")!;
    expect(within(card).getByRole("link")).toHaveAttribute("href", `/library/${A.id}`);
    expect(within(card).getByText("6,191 videos")).toBeInTheDocument();
    expect(within(card).getByText("الحويني").tagName).toBe("BDI");
    // four posters at most
    const imgs = card.querySelectorAll(".mkt-libcard__mosaic img");
    expect(Array.from(imgs).map((i) => i.getAttribute("src"))).toEqual([
      "/api/videos/p1/poster",
      "/api/videos/p2/poster",
      "/api/videos/p3/poster",
      "/api/videos/p4/poster",
    ]);
    // a library with no posters gets one quiet placeholder, not blank tiles
    const kids = screen.getByText("Kids").closest("li")!;
    expect(kids.querySelector(".mkt-libcard__mosaic--empty")).not.toBeNull();
    expect(kids.querySelectorAll(".mkt-libcard__mosaic img")).toHaveLength(0);
    // nothing pinned: no Pinned section
    expect(screen.queryByRole("region", { name: /^Pinned/ })).toBeNull();
  });

  it("Pin adds the library at the end, Unpin removes it; the shared store follows", async () => {
    pins = [A];
    serve(summary([A.id]));
    renderLibraries();
    const user = userEvent.setup();
    const pinB = await screen.findByRole("button", { name: `Pin ${B.name} to the sidebar` });
    expect(pinB).toHaveAttribute("aria-pressed", "false");
    await user.click(pinB);
    await waitFor(() => expect(putCalls()).toEqual([[A.id, B.id]]));
    expect(put.mock.calls[0][0]).toBe("/api/me/pinned-libraries");
    expect(screen.getByTestId("probe")).toHaveTextContent(`${A.name},${B.name}`);
    const pinned = screen.getByRole("region", { name: "Pinned (2)" });
    expect(within(pinned).getAllByRole("listitem")).toHaveLength(2);
    // a toast says what happened
    expect(await screen.findByText(`Pinned ${B.name} to the sidebar`)).toBeInTheDocument();
    const unpinA = screen.getAllByRole("button", {
      name: `${A.name} is pinned to the sidebar; click to unpin`,
    })[0];
    expect(unpinA).toHaveAttribute("aria-pressed", "true");
    await user.click(unpinA);
    await waitFor(() => expect(putCalls().at(-1)).toEqual([B.id]));
    expect(screen.getByTestId("probe")).toHaveTextContent(B.name);
    // the pins were read once; changes did not re-read them
    expect(get.mock.calls.filter(([u]) => u === "/api/me/pinned-libraries")).toHaveLength(1);
  });

  it("the Pinned strip lists the pins in order with a Reorder link, and no ordering controls", async () => {
    pins = [C, A, B];
    serve(summary([C.id, A.id, B.id]));
    renderLibraries();
    const pinned = await screen.findByRole("region", { name: "Pinned (3)" });
    const links = within(pinned).getAllByRole("link");
    // the pins, in the user's order, then Reorder
    expect(links.map((l) => l.textContent)).toEqual([
      en["libraries.reorder"],
      C.name,
      A.name,
      B.name,
    ]);
    expect(within(pinned).getByRole("link", { name: A.name })).toHaveAttribute(
      "href",
      `/library/${A.id}`
    );
    expect(
      within(pinned).getByRole("link", { name: en["libraries.reorder.label"] })
    ).toHaveAttribute("href", "/library/pinned");
    // the order is set on its own page: no move buttons, no drag handles, no pin
    // toggles in the strip
    expect(within(pinned).queryAllByRole("button")).toHaveLength(0);
    expect(screen.queryByRole("button", { name: /^Move / })).toBeNull();
    expect(screen.queryByRole("button", { name: /^Reorder / })).toBeNull();
    expect(document.querySelector("[draggable=true]")).toBeNull();
    // each card keeps its pin toggle
    for (const l of [A, B, C]) {
      const card = screen
        .getAllByText(l.name)
        .map((el) => el.closest(".mkt-libcard"))
        .find(Boolean)!;
      expect(within(card as HTMLElement).getByRole("button", { pressed: true })).toBeVisible();
    }
  });

  it("no libraries: a viewer is told to ask an admin; an admin gets Manage libraries", async () => {
    serve({ items: [] });
    const first = renderLibraries();
    expect(await screen.findByText(en["libraries.empty.title"])).toBeInTheDocument();
    expect(screen.getByText(en["libraries.empty.desc"])).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: en["home.empty.manage"] })).toBeNull();
    first.unmount();
    resetPinsStore();
    renderLibraries(adminUser);
    expect(await screen.findByRole("link", { name: en["home.empty.manage"] })).toHaveAttribute(
      "href",
      "/admin/libraries"
    );
  });

  it("a failed read shows the error with Try again", async () => {
    get.mockImplementation((url: string) =>
      url === "/api/libraries/summary"
        ? Promise.reject(new ApiError({ type: "about:blank", title: "boom", status: 500 }))
        : Promise.resolve({ library_ids: [], items: [] })
    );
    renderLibraries();
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent(en["error.kind.server"]);
    expect(alert).not.toHaveTextContent("boom");
    expect(within(alert).getByRole("button", { name: en["common.retry"] })).toBeInTheDocument();
  });
});
