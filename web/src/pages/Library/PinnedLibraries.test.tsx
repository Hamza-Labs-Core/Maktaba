// Pinned libraries (`/library/pinned`): the pins in order, reordered by the
// move-to-top / up / down buttons, by the keyboard on a row's handle and by
// drag and drop, unpinned with the pin toggle; each change saves through
// lib/pins (PUT /api/me/pinned-libraries) and drops Home's and the libraries'
// kept answers; a refused save puts the order back with a toast; and with
// nothing pinned, an empty state says how to pin from a library card.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ApiError } from "../../lib/api";
import { renderPage, memberUser } from "../../test/pageHarness";
import { cachedAnswer, keepAnswer } from "../../lib/pageCache";
import en from "../../i18n/en.json";

const { get, put } = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, put } };
});

import { PinnedLibraries } from "./PinnedLibraries";
import { resetPinsStore, usePins } from "../../lib/pins";

const A = { id: "aaaaaaaa-0000-4000-8000-000000000001", name: "الحويني" };
const B = { id: "bbbbbbbb-0000-4000-8000-000000000002", name: "Lectures" };
const C = { id: "cccccccc-0000-4000-8000-000000000003", name: "Kids" };
const ALL = [A, B, C];

let pins: Array<{ id: string; name: string }>;

const pinsDoc = () => ({
  library_ids: pins.map((p) => p.id),
  items: pins.map((p, position) => ({ ...p, position })),
});

function serve() {
  get.mockImplementation((url: string) => {
    if (url === "/api/me/pinned-libraries") return Promise.resolve(pinsDoc());
    if (url === "/api/libraries/summary")
      return Promise.resolve({
        items: ALL.map((l, i) => ({
          ...l,
          videos: [6191, 47, 0][i],
          poster_video_ids: [["p1", "p2", "p3", "p4"], ["q1"], []][i],
          pinned: pins.some((p) => p.id === l.id),
        })),
      });
    return Promise.reject(new Error(`unmocked GET ${url}`));
  });
  put.mockImplementation((_url: string, body: { library_ids: string[] }) => {
    pins = body.library_ids.map((id) => ALL.find((l) => l.id === id)!);
    return Promise.resolve(pinsDoc());
  });
}

// what the sidebar sees: the shared store
function PinsProbe() {
  const p = usePins(memberUser.id);
  return <p data-testid="probe">{(p ?? []).map((x) => x.name).join(",")}</p>;
}

function renderPinned() {
  return renderPage(
    <>
      <PinnedLibraries />
      <PinsProbe />
    </>,
    { path: "/library/pinned", user: memberUser }
  );
}

const putCalls = () => put.mock.calls.map((c) => (c[1] as { library_ids: string[] }).library_ids);

async function list() {
  return screen.findByRole("list", { name: en["pinned.title"] });
}
async function names() {
  return within(await list())
    .getAllByRole("listitem")
    .map((li) => li.querySelector("bdi")!.textContent);
}

describe("PinnedLibraries", () => {
  beforeEach(() => {
    get.mockReset();
    put.mockReset();
    resetPinsStore();
    pins = [];
  });

  it("lists the pins in order, each linking to its library, with its video count", async () => {
    pins = [B, A, C];
    serve();
    renderPinned();
    expect(screen.getByRole("heading", { level: 1, name: en["pinned.title"] })).toBeVisible();
    expect(await names()).toEqual([B.name, A.name, C.name]);
    const rowA = within(await list())
      .getAllByRole("listitem")
      .find((li) => li.textContent?.includes(A.name))!;
    expect(within(rowA).getByRole("link", { name: A.name })).toHaveAttribute(
      "href",
      `/library/${A.id}`
    );
    expect(await within(rowA).findByText("6,191 videos")).toBeInTheDocument();
    // the first can not go up, the last can not go down
    expect(screen.getByRole("button", { name: `Move ${B.name} up` })).toBeDisabled();
    expect(screen.getByRole("button", { name: `Move ${B.name} to the top` })).toBeDisabled();
    expect(screen.getByRole("button", { name: `Move ${C.name} down` })).toBeDisabled();
    expect(screen.getByRole("link", { name: en["libraries.all"] })).toHaveAttribute(
      "href",
      "/library"
    );
  });

  it("the up, down and move-to-top buttons save the new order", async () => {
    pins = [A, B, C];
    serve();
    renderPinned();
    const user = userEvent.setup();
    await list();
    await user.click(screen.getByRole("button", { name: `Move ${B.name} up` }));
    await waitFor(() => expect(putCalls()).toEqual([[B.id, A.id, C.id]]));
    expect(put.mock.calls[0][0]).toBe("/api/me/pinned-libraries");
    expect(await names()).toEqual([B.name, A.name, C.name]);
    await user.click(screen.getByRole("button", { name: `Move ${B.name} down` }));
    await waitFor(() => expect(putCalls().at(-1)).toEqual([A.id, B.id, C.id]));
    await user.click(screen.getByRole("button", { name: `Move ${C.name} to the top` }));
    await waitFor(() => expect(putCalls().at(-1)).toEqual([C.id, A.id, B.id]));
    expect(await names()).toEqual([C.name, A.name, B.name]);
    // the sidebar follows
    expect(screen.getByTestId("probe")).toHaveTextContent(`${C.name},${A.name},${B.name}`);
    // a screen reader hears where it went
    expect(screen.getByText(`${C.name} is now 1 of 3`)).toBeInTheDocument();
  });

  it("reorders from the keyboard: the buttons with Enter, the handle with the arrows, Home and End", async () => {
    pins = [A, B, C];
    serve();
    renderPinned();
    const user = userEvent.setup();
    await list();
    // a move button, by keyboard
    screen.getByRole("button", { name: `Move ${A.name} down` }).focus();
    await user.keyboard("{Enter}");
    await waitFor(() => expect(putCalls()).toEqual([[B.id, A.id, C.id]]));
    // the handle: arrows move by one and the focus stays with the moved row
    const handleC = screen.getByRole("button", { name: `Reorder ${C.name}` });
    handleC.focus();
    await user.keyboard("{ArrowUp}");
    await waitFor(() => expect(putCalls().at(-1)).toEqual([B.id, C.id, A.id]));
    await waitFor(() =>
      expect(screen.getByRole("button", { name: `Reorder ${C.name}` })).toHaveFocus()
    );
    await user.keyboard("{Home}");
    await waitFor(() => expect(putCalls().at(-1)).toEqual([C.id, B.id, A.id]));
    await user.keyboard("{End}");
    await waitFor(() => expect(putCalls().at(-1)).toEqual([B.id, A.id, C.id]));
    expect(await names()).toEqual([B.name, A.name, C.name]);
    // an arrow past the end saves nothing
    const before = put.mock.calls.length;
    await user.keyboard("{ArrowDown}");
    expect(put.mock.calls.length).toBe(before);
  });

  it("move to the top keeps the focus on the moved row (its handle)", async () => {
    pins = [A, B, C];
    serve();
    renderPinned();
    const user = userEvent.setup();
    await list();
    await user.click(screen.getByRole("button", { name: `Move ${C.name} to the top` }));
    await waitFor(() => expect(putCalls()).toEqual([[C.id, A.id, B.id]]));
    await waitFor(() =>
      expect(screen.getByRole("button", { name: `Reorder ${C.name}` })).toHaveFocus()
    );
  });

  it("drag and drop by the handle saves the new order", async () => {
    pins = [A, B, C];
    serve();
    renderPinned();
    const rows = within(await list()).getAllByRole("listitem");
    const handleC = screen.getByRole("button", { name: `Reorder ${C.name}` });
    expect(handleC).toHaveAttribute("draggable", "true");
    const dataTransfer = { setData: vi.fn(), effectAllowed: "", dropEffect: "" };
    fireEvent.dragStart(handleC, { dataTransfer });
    fireEvent.dragOver(rows[0]!, { dataTransfer });
    expect(rows[0]).toHaveClass("mkt-pinned__item--over");
    fireEvent.drop(rows[0]!, { dataTransfer });
    fireEvent.dragEnd(handleC, { dataTransfer });
    await waitFor(() => expect(putCalls()).toEqual([[C.id, A.id, B.id]]));
    expect(await names()).toEqual([C.name, A.name, B.name]);
    // a drop on itself saves nothing
    const handleA = screen.getByRole("button", { name: `Reorder ${A.name}` });
    const rowA = handleA.closest("li")!;
    fireEvent.dragStart(handleA, { dataTransfer });
    fireEvent.drop(rowA, { dataTransfer });
    expect(put).toHaveBeenCalledTimes(1);
  });

  it("the pin unpins a library from the list", async () => {
    pins = [A, B, C];
    serve();
    renderPinned();
    const user = userEvent.setup();
    await list();
    await user.click(
      screen.getByRole("button", { name: `${B.name} is pinned to the sidebar; click to unpin` })
    );
    await waitFor(() => expect(putCalls()).toEqual([[A.id, C.id]]));
    expect(await names()).toEqual([A.name, C.name]);
    expect(await screen.findByText(`Unpinned ${B.name}`)).toBeInTheDocument();
    expect(screen.getByTestId("probe")).toHaveTextContent(`${A.name},${C.name}`);
  });

  it("a save drops Home's and the libraries' kept answers", async () => {
    pins = [A, B];
    serve();
    keepAnswer(memberUser.id, "home", { rows: [] });
    keepAnswer(memberUser.id, "libraries", []);
    keepAnswer(memberUser.id, "playlists", []);
    renderPinned();
    const user = userEvent.setup();
    await list();
    await user.click(screen.getByRole("button", { name: `Move ${B.name} up` }));
    await waitFor(() => expect(putCalls()).toEqual([[B.id, A.id]]));
    await waitFor(() => expect(cachedAnswer(memberUser.id, "home")).toBeUndefined());
    expect(cachedAnswer(memberUser.id, "libraries")).toBeUndefined();
    // other pages' answers stay
    expect(cachedAnswer(memberUser.id, "playlists")).toEqual([]);
  });

  it("a refused save puts the order back and says why", async () => {
    pins = [A, B];
    serve();
    put.mockRejectedValue(
      new ApiError({ type: "about:blank", title: "Not Found", status: 404, detail: "no library" })
    );
    renderPinned();
    const user = userEvent.setup();
    await list();
    await user.click(screen.getByRole("button", { name: `Move ${B.name} up` }));
    expect(
      await screen.findByText(`${en["libraries.pin.error"]}: ${en["error.kind.notFound"]}`)
    ).toBeInTheDocument();
    expect(await names()).toEqual([A.name, B.name]);
    expect(screen.getByTestId("probe")).toHaveTextContent(`${A.name},${B.name}`);
  });

  it("with nothing pinned, says how to pin from a library card", async () => {
    serve();
    renderPinned();
    expect(await screen.findByText(en["pinned.empty.title"])).toBeInTheDocument();
    expect(screen.getByText(en["pinned.empty.desc"])).toBeInTheDocument();
    expect(screen.getByRole("link", { name: en["pinned.empty.action"] })).toHaveAttribute(
      "href",
      "/library"
    );
    expect(screen.queryByRole("list", { name: en["pinned.title"] })).toBeNull();
  });
});
