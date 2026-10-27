// Epic 45 Phase 3 (story 45.19) — scrub thumbnails: the tile of a time, the
// preview over the bottom of the video and while seeking, and nothing for a
// video without a sheet.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { act, render, screen, waitFor } from "@testing-library/react";
import { ApiError } from "../lib/api";

const { get } = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock("../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../lib/api")>("../lib/api");
  return { ...actual, api: { ...actual.api, get } };
});

import { ScrubPreview, tileAt, tileStyle, type TrickplayIndex } from "./ScrubPreview";

const IX: TrickplayIndex = {
  sprite_url: "/api/videos/v1/trickplay/sprite",
  columns: 10,
  rows: 10,
  count: 100,
  interval_sec: 36,
  tile_width: 160,
  tile_height: 90,
  sheet_width: 1600,
  sheet_height: 900,
};

// a <video> 1000 px wide, 500 tall, at the page's origin, an hour long
function fakeVideo(): HTMLVideoElement {
  const el = document.createElement("video");
  Object.defineProperty(el, "duration", { value: 3600, configurable: true });
  el.getBoundingClientRect = () =>
    ({
      left: 0,
      top: 0,
      right: 1000,
      bottom: 500,
      width: 1000,
      height: 500,
      x: 0,
      y: 0,
    }) as DOMRect;
  document.body.appendChild(el);
  return el;
}

const move = (el: HTMLElement, x: number, y: number) =>
  act(() => {
    el.dispatchEvent(new MouseEvent("pointermove", { clientX: x, clientY: y, bubbles: true }));
  });

describe("ScrubPreview", () => {
  beforeEach(() => {
    get.mockReset();
    document.body.innerHTML = "";
  });

  it("finds the tile of a time, the last one for the end", () => {
    expect(tileAt(IX, 0)).toEqual({ col: 0, row: 0 });
    expect(tileAt(IX, 36 * 13 + 5)).toEqual({ col: 3, row: 1 });
    expect(tileAt(IX, 99999)).toEqual({ col: 9, row: 9 });
    const st = tileStyle(IX, 36 * 13, 160);
    expect(st.backgroundPosition).toBe("-480px -90px");
    expect(st.backgroundSize).toBe("1600px 900px");
  });

  it("shows the tile under the pointer over the progress bar, and hides it above", async () => {
    get.mockResolvedValue(IX);
    const el = fakeVideo();
    render(<ScrubPreview videoId="v1" el={el} />);
    await waitFor(() => expect(get).toHaveBeenCalledWith("/api/videos/v1/trickplay"));
    await act(async () => {});
    // half way along the bar: 30:00, tile 50 (col 0, row 5)
    move(el, 500, 490);
    const preview = await screen.findByTestId("scrub-preview");
    expect(preview).toHaveTextContent("30:00");
    const tile = preview.firstElementChild as HTMLElement;
    expect(tile.style.backgroundImage).toContain("/api/videos/v1/trickplay/sprite");
    expect(tile.style.backgroundPosition).toBe("0px -495px");
    // the middle of the picture is not the bar
    move(el, 500, 200);
    expect(screen.queryByTestId("scrub-preview")).toBeNull();
  });

  it("shows the seek's time while the bar is dragged", async () => {
    get.mockResolvedValue(IX);
    const el = fakeVideo();
    render(<ScrubPreview videoId="v1" el={el} />);
    await waitFor(() => expect(get).toHaveBeenCalled());
    await act(async () => {});
    Object.defineProperty(el, "currentTime", { value: 900, configurable: true });
    act(() => {
      el.dispatchEvent(new Event("seeking"));
    });
    expect(await screen.findByTestId("scrub-preview")).toHaveTextContent("15:00");
    act(() => {
      el.dispatchEvent(new Event("seeked"));
    });
    expect(screen.queryByTestId("scrub-preview")).toBeNull();
  });

  it("shows nothing for a video without a sheet", async () => {
    get.mockRejectedValue(new ApiError({ type: "about:blank", title: "not found", status: 404 }));
    const el = fakeVideo();
    render(<ScrubPreview videoId="v1" el={el} />);
    await waitFor(() => expect(get).toHaveBeenCalled());
    move(el, 500, 490);
    expect(screen.queryByTestId("scrub-preview")).toBeNull();
  });
});
