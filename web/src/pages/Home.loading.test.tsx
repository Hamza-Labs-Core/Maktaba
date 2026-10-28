// Home while it loads: no placeholder for the first 0.4 s (most loads are
// done by then), then one in the shape of the page (the hero and rows of
// poster cards, MediaRow's own classes); a return to Home shows the last rows
// at once, for the same user only.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderPage, memberUser, adminUser } from "../test/pageHarness";

const { get } = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock("../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../lib/api")>("../lib/api");
  return { ...actual, api: { ...actual.api, get } };
});

import { Home } from "./Home";

const card = (id: string) => ({ id, title: `Video ${id}`, duration_sec: 600, state: "ready" });
const HOME = { continue_watching: [card("c1"), card("c2")], recently_added: [card("a1")] };

/** GET /api/home answers when `answer` is called (or never). */
function serveHeld() {
  let answer: (body: unknown) => void = () => {};
  get.mockImplementation((url: string) => {
    if (url === "/api/home") return new Promise((resolve) => (answer = resolve));
    if (url.startsWith("/api/channels/now")) return Promise.resolve({ channels: [] });
    return Promise.reject(new Error(`unmocked GET ${url}`));
  });
  return (body: unknown) => answer(body);
}

const busy = () => document.querySelector('[aria-busy="true"]');
const shapes = () => document.querySelectorAll(".mk-skeleton");
const flush = () => act(async () => {});

describe("Home loading", () => {
  beforeEach(() => {
    get.mockReset();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it("shows no placeholder for 0.4 s, then one shaped like the page", async () => {
    vi.useFakeTimers();
    const answer = serveHeld();
    renderPage(<Home />, { path: "/", user: memberUser });
    // busy at once, but nothing drawn yet
    expect(busy()).not.toBeNull();
    expect(shapes()).toHaveLength(0);
    act(() => {
      vi.advanceTimersByTime(399);
    });
    expect(shapes()).toHaveLength(0);
    act(() => {
      vi.advanceTimersByTime(1);
    });
    const region = busy()!;
    expect(shapes().length).toBeGreaterThan(0);
    // the hero's area, then rows of poster cards with a title: the real classes
    expect(region.querySelector(".mkt-hero")).not.toBeNull();
    const rows = region.querySelectorAll("section.mkt-row");
    expect(rows.length).toBeGreaterThan(1);
    expect(rows[0]!.querySelector(".mkt-row__head > .mkt-row__title")).not.toBeNull();
    expect(
      rows[0]!.querySelectorAll(
        "ul.mkt-row__scroller > li.mkt-row__item > .mkt-media-card > .mkt-media-card__art"
      ).length
    ).toBeGreaterThan(3);
    // hidden from assistive tech: the region's aria-busy says it is loading
    expect(rows[0]!.getAttribute("aria-hidden")).toBe("true");

    answer(HOME);
    await flush();
    expect(busy()).toBeNull();
    expect(shapes()).toHaveLength(0);
    // the loaded rows have the same structure the placeholder drew
    const real = screen.getByTestId("row-continue");
    expect(
      real.querySelectorAll(
        "ul.mkt-row__scroller > li.mkt-row__item > .mkt-media-card > .mkt-media-card__art"
      )
    ).toHaveLength(2);
  });

  it("never shows a placeholder when the rows come within 0.4 s", async () => {
    vi.useFakeTimers();
    const answer = serveHeld();
    renderPage(<Home />, { path: "/", user: memberUser });
    act(() => {
      vi.advanceTimersByTime(200);
    });
    answer(HOME);
    await flush();
    act(() => {
      vi.advanceTimersByTime(1000);
    });
    expect(shapes()).toHaveLength(0);
    expect(busy()).toBeNull();
    expect(screen.getByText("Video c1")).toBeInTheDocument();
  });

  it("a return to Home shows the last rows at once and reads them again quietly", async () => {
    const answer = serveHeld();
    renderPage(<Home />, { path: "/", user: memberUser });
    answer(HOME);
    expect(await screen.findByText("Video c1")).toBeInTheDocument();

    const user = userEvent.setup();
    await user.click(screen.getByRole("link", { name: "go elsewhere" }));
    expect(screen.getByText("elsewhere")).toBeInTheDocument();
    // the next read never answers: what shows is the kept rows
    serveHeld();
    await user.click(screen.getByRole("button", { name: "go back" }));
    expect(screen.getByText("Video c1")).toBeInTheDocument();
    expect(busy()).toBeNull();
    // and Home read them again behind them
    expect(get.mock.calls.filter(([u]) => u === "/api/home")).toHaveLength(2);
  });

  it("another user never sees the kept rows", async () => {
    const answer = serveHeld();
    const first = renderPage(<Home />, { path: "/", user: memberUser });
    answer(HOME);
    expect(await screen.findByText("Video c1")).toBeInTheDocument();
    first.unmount();

    serveHeld();
    renderPage(<Home />, { path: "/", user: adminUser });
    expect(screen.queryByText("Video c1")).toBeNull();
    expect(busy()).not.toBeNull();
  });
});
