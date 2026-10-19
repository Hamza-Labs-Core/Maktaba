// Epic 35, story 35.10: a 503 paused / home_unreachable from any API call
// swaps the app for the paused page; otherwise the app renders as usual.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, render, screen } from "@testing-library/react";
import { I18nProvider } from "../lib/i18n";
import { NODE_UNAVAILABLE_EVENT, clearNodeUnavailable, request } from "../lib/api";
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

  it("shows the home_unreachable page from the event", async () => {
    fetchMock.mockResolvedValue(json(200, { role: "node", id: "n1", version: "1.5.0", sso: true }));
    renderGuard();
    act(() => {
      window.dispatchEvent(
        new CustomEvent(NODE_UNAVAILABLE_EVENT, { detail: { code: "home_unreachable" } })
      );
    });
    expect(
      screen.getByRole("heading", { name: "This machine cannot reach its home" })
    ).toBeInTheDocument();
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
