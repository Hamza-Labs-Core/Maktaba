// Libraries while it loads: a quick answer (the usual ~10 ms) never shows a
// placeholder; a slow one shows the library cards' own shape.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act } from "@testing-library/react";
import { renderPage, memberUser } from "../../test/pageHarness";

const { get } = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get } };
});

import { Libraries } from "./Libraries";
import { resetPinsStore } from "../../lib/pins";

const LIB = {
  id: "aaaaaaaa-0000-4000-8000-000000000001",
  name: "Lectures",
  videos: 4,
  poster_video_ids: ["p1", "p2", "p3", "p4"],
  pinned: false,
};

function serve(summary: Promise<unknown>) {
  get.mockImplementation((url: string) => {
    if (url === "/api/libraries/summary") return summary;
    if (url === "/api/me/pinned-libraries") return Promise.resolve({ items: [] });
    return Promise.reject(new Error(`unmocked GET ${url}`));
  });
}

// mosaic, name and count, inside the card's link-shaped box
const CARD = "ul.mkt-libgrid > li.mkt-libcard > .mkt-libcard__link";

describe("Libraries loading", () => {
  beforeEach(() => {
    get.mockReset();
    resetPinsStore();
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it("a quick answer shows the cards without a placeholder first", async () => {
    serve(Promise.resolve({ items: [LIB] }));
    renderPage(<Libraries />, { path: "/library", user: memberUser });
    await act(async () => {});
    act(() => {
      vi.advanceTimersByTime(1000);
    });
    expect(document.querySelector(".mk-skeleton")).toBeNull();
    expect(document.querySelector('[aria-busy="true"]')).toBeNull();
    expect(document.querySelectorAll(CARD)).toHaveLength(1);
  });

  it("a slow one shows placeholder cards shaped like the real ones", () => {
    serve(new Promise(() => {}));
    renderPage(<Libraries />, { path: "/library", user: memberUser });
    expect(document.querySelectorAll(CARD)).toHaveLength(0);
    act(() => {
      vi.advanceTimersByTime(400);
    });
    const region = document.querySelector('[aria-busy="true"]')!;
    const cards = region.querySelectorAll(CARD);
    expect(cards.length).toBeGreaterThan(0);
    expect(cards[0]!.querySelector(".mkt-libcard__name")).not.toBeNull();
    expect(cards[0]!.querySelector(".mkt-libcard__meta")).not.toBeNull();
  });
});
