// Epic 56 story 56.13 T06 (56.11 on the web): Home offers to continue the queue last
// used on any device in the last day, and Select plays it from its current video.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Route } from "react-router-dom";
import { renderPage, memberUser } from "../test/pageHarness";
import ar from "../i18n/ar.json";

const { get } = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock("../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../lib/api")>("../lib/api");
  return { ...actual, api: { ...actual.api, get } };
});

import { ContinueQueue } from "./ContinueQueue";
import { readServerQueue } from "../lib/playQueue";

const NOW = Date.parse("2026-11-06T12:00:00Z");
const queue = (usedAt: string) => ({
  id: "Q",
  source: { kind: "series", id: "S" },
  shuffle: false,
  current_pos: 3,
  version: 7,
  device_id: "phone-1",
  source_done: false,
  total_estimate: 10,
  used_at: usedAt,
  items: [
    { pos: 2, origin: "source", id: "v2", title: "Lesson 2" },
    { pos: 3, origin: "source", id: "v3", title: "Lesson 3", position_sec: 125 },
  ],
});

const open = () =>
  renderPage(<ContinueQueue now={() => NOW} />, {
    path: "/",
    user: memberUser,
    extraRoutes: <Route path="/videos/:videoId/watch" element={<p>player</p>} />,
  });

describe("ContinueQueue", () => {
  beforeEach(() => {
    get.mockReset();
    sessionStorage.clear();
    localStorage.clear();
  });

  it("offers a queue from a phone ten minutes ago and plays it at its position", async () => {
    get.mockResolvedValue(queue("2026-11-06T11:50:00Z"));
    open();
    const b = await screen.findByRole("button", { name: /Lesson 3 · from a phone/ });
    await userEvent.setup().click(b);
    expect(await screen.findByText("player")).toBeInTheDocument();
    expect(readServerQueue()?.id).toBe("Q");
    expect(get).toHaveBeenCalledWith("/api/me/play-queues/current");
  });

  it("leaves out a queue older than a day, and none at all", async () => {
    get.mockResolvedValue(queue("2026-11-04T11:00:00Z"));
    const { unmount } = open();
    await new Promise((r) => setTimeout(r, 20));
    expect(screen.queryByTestId("row-continue-queue")).toBeNull();
    unmount();
    get.mockResolvedValue(undefined);
    open();
    await new Promise((r) => setTimeout(r, 20));
    expect(screen.queryByTestId("row-continue-queue")).toBeNull();
  });

  it("speaks Arabic", async () => {
    localStorage.setItem("mkt:locale", "ar");
    get.mockResolvedValue(queue("2026-11-06T11:50:00Z"));
    open();
    expect(
      await screen.findByRole("heading", { name: ar["home.continueQueue"] })
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Lesson 3 · من هاتف/ })).toBeInTheDocument();
  });
});
