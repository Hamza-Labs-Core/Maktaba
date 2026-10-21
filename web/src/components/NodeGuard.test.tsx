// Epic 35, story 35.10: a 503 paused from any API call swaps the app for the
// paused page; home_unreachable keeps it readable under a notice (the node
// decides what still works, so every request is still sent), unless the node
// cannot serve even reads ({readable: false}): then the paused page too;
// otherwise the app renders as usual.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, render, screen } from "@testing-library/react";
import { I18nProvider } from "../lib/i18n";
import {
  HOME_STATE_HEADER,
  NODE_UNAVAILABLE_EVENT,
  clearNodeUnavailable,
  homeUnreachable,
  request,
} from "../lib/api";
import { HOME_OFFLINE_POLL_MS } from "./HomeOffline";
import { NodeGuard } from "./NodeGuard";

function json(status: number, body: unknown, type = "application/json"): Response {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": type } });
}

function renderGuard() {
  return render(
    <I18nProvider>
      <NodeGuard>
        <p>the app</p>
      </NodeGuard>
    </I18nProvider>
  );
}

describe("NodeGuard", () => {
  const fetchMock = vi.fn<typeof fetch>();
  beforeEach(() => {
    localStorage.clear();
    clearNodeUnavailable();
    fetchMock.mockReset();
    vi.stubGlobal("fetch", fetchMock);
    vi.spyOn(console, "error").mockImplementation(() => {});
  });
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it("renders the app on the home", async () => {
    fetchMock.mockResolvedValue(json(200, { role: "home", id: "h", version: "1.5.0", sso: true }));
    renderGuard();
    expect(screen.getByText("the app")).toBeInTheDocument();
    await act(() => Promise.resolve());
    expect(screen.getByText("the app")).toBeInTheDocument();
  });

  it("shows the paused page when an API call answers 503 paused", async () => {
    const why = "paused: home database at 0130, this machine needs 0134 — update the home";
    fetchMock.mockImplementation((input) =>
      Promise.resolve(
        String(input).endsWith("/api/cluster/self")
          ? json(200, {
              role: "node",
              id: "n1",
              version: "1.5.0",
              sso: true,
              home: { reachable: true },
            })
          : json(
              503,
              { title: "paused", status: 503, code: "paused", reason: why },
              "application/problem+json"
            )
      )
    );
    const seen: unknown[] = [];
    const listen = (e: Event) => seen.push((e as CustomEvent).detail);
    window.addEventListener(NODE_UNAVAILABLE_EVENT, listen);
    renderGuard();
    expect(screen.getByText("the app")).toBeInTheDocument();
    await act(async () => {
      await expect(request("/api/library")).rejects.toMatchObject({ status: 503 });
    });
    window.removeEventListener(NODE_UNAVAILABLE_EVENT, listen);
    expect(seen).toEqual([{ code: "paused", reason: why }]);
    expect(screen.queryByText("the app")).not.toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "This machine is paused" })).toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent(why);
  });

  it("keeps the app readable under a notice when the home is unreachable", async () => {
    fetchMock.mockResolvedValue(json(200, { role: "node", id: "n1", version: "1.5.0", sso: true }));
    renderGuard();
    act(() => {
      window.dispatchEvent(
        new CustomEvent(NODE_UNAVAILABLE_EVENT, { detail: { code: "home_unreachable" } })
      );
    });
    expect(screen.getByText("the app")).toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent("This machine can't reach its home.");
    expect(
      screen.queryByRole("heading", { name: /cannot reach its home/ })
    ).not.toBeInTheDocument();
  });

  it("a read marked X-Maktaba-Home: unreachable shows the notice; a change still goes to the node", async () => {
    const marked = (body: unknown, status = 200) =>
      new Response(JSON.stringify(body), {
        status,
        headers: { "Content-Type": "application/json", [HOME_STATE_HEADER]: "unreachable" },
      });
    fetchMock.mockImplementation((input, init) => {
      const url = String(input);
      if (url.endsWith("/api/cluster/self"))
        return Promise.resolve(json(200, { role: "node", id: "n1", version: "1.5.0", sso: true }));
      // the node plays a video it can read (a `local` route) and refuses a change of the home's
      if (url.endsWith("/api/stream/sessions") && init?.method === "POST")
        return Promise.resolve(marked({ session_id: "s1" }, 201));
      if (init?.method === "PATCH")
        return Promise.resolve(
          json(
            503,
            { title: "the home is not reachable", status: 503, code: "home_unreachable" },
            "application/problem+json"
          )
        );
      return Promise.resolve(marked({ items: [] }));
    });
    renderGuard();
    await act(async () => {
      await expect(request("/api/library")).resolves.toEqual({ items: [] });
    });
    expect(screen.getByText("the app")).toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent("This machine can't reach its home.");
    await act(async () => {
      await expect(
        request("/api/stream/sessions", { method: "POST", body: { video_id: "v1" } })
      ).resolves.toEqual({ session_id: "s1" });
      await expect(
        request("/api/videos/v1", { method: "PATCH", body: { title: "x" } })
      ).rejects.toMatchObject({ status: 503, problem: { code: "home_unreachable" } });
    });
    // both went to the node; the refused change keeps the app under the notice
    expect(fetchMock.mock.calls.some(([u]) => String(u).endsWith("/api/stream/sessions"))).toBe(
      true
    );
    expect(screen.getByText("the app")).toBeInTheDocument();
  });

  it("swaps the app for the paused page when the node cannot serve even reads", async () => {
    fetchMock.mockImplementation((input) =>
      Promise.resolve(
        String(input).endsWith("/api/cluster/self")
          ? json(200, {
              role: "node",
              id: "n1",
              version: "1.5.0",
              sso: true,
              home: { reachable: false },
            })
          : json(
              503,
              { title: "x", status: 503, code: "home_unreachable", readable: false },
              "application/problem+json"
            )
      )
    );
    renderGuard();
    act(() => {
      window.dispatchEvent(
        new CustomEvent(NODE_UNAVAILABLE_EVENT, { detail: { code: "home_unreachable" } })
      );
    });
    expect(screen.getByText("the app")).toBeInTheDocument();
    await act(async () => {
      await expect(request("/api/videos")).rejects.toMatchObject({ status: 503 });
    });
    expect(screen.queryByText("the app")).not.toBeInTheDocument();
    expect(
      screen.getByRole("heading", { name: "This machine cannot reach its home" })
    ).toBeInTheDocument();
    // a later read marked unreachable does not bring the readable app back
    act(() => {
      window.dispatchEvent(
        new CustomEvent(NODE_UNAVAILABLE_EVENT, { detail: { code: "home_unreachable" } })
      );
    });
    expect(screen.queryByText("the app")).not.toBeInTheDocument();
  });

  it("clears the notice once the home answers again", async () => {
    vi.useFakeTimers();
    try {
      let reachable = false;
      fetchMock.mockImplementation(() =>
        Promise.resolve(
          json(200, { role: "node", id: "n1", version: "1.5.0", sso: true, home: { reachable } })
        )
      );
      renderGuard();
      act(() => {
        window.dispatchEvent(
          new CustomEvent(NODE_UNAVAILABLE_EVENT, { detail: { code: "home_unreachable" } })
        );
      });
      expect(screen.getByRole("status")).toBeInTheDocument();
      await act(async () => {
        await vi.advanceTimersByTimeAsync(HOME_OFFLINE_POLL_MS);
      });
      expect(screen.getByRole("status")).toBeInTheDocument();
      reachable = true;
      await act(async () => {
        await vi.advanceTimersByTimeAsync(HOME_OFFLINE_POLL_MS);
      });
      expect(screen.queryByRole("status")).not.toBeInTheDocument();
      expect(homeUnreachable()).toBe(false);
    } finally {
      vi.useRealTimers();
    }
  });

  it("paused outranks home_unreachable", async () => {
    fetchMock.mockResolvedValue(json(200, { role: "node", id: "n1", version: "1.5.0", sso: true }));
    renderGuard();
    act(() => {
      window.dispatchEvent(
        new CustomEvent(NODE_UNAVAILABLE_EVENT, { detail: { code: "home_unreachable" } })
      );
      window.dispatchEvent(
        new CustomEvent(NODE_UNAVAILABLE_EVENT, { detail: { code: "paused", reason: "x" } })
      );
      window.dispatchEvent(
        new CustomEvent(NODE_UNAVAILABLE_EVENT, { detail: { code: "home_unreachable" } })
      );
    });
    expect(screen.getByRole("heading", { name: "This machine is paused" })).toBeInTheDocument();
  });

  it("ignores other 503s", async () => {
    fetchMock.mockImplementation((input) =>
      Promise.resolve(
        String(input).endsWith("/api/cluster/self")
          ? json(200, { role: "node", id: "n1", version: "1.5.0", sso: true })
          : json(503, { title: "busy", status: 503, code: "overloaded" })
      )
    );
    renderGuard();
    await act(async () => {
      await expect(request("/api/library")).rejects.toMatchObject({ status: 503 });
    });
    expect(screen.getByText("the app")).toBeInTheDocument();
  });
});
