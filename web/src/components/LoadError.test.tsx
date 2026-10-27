// LoadError: the one error state for a screen whose data did not load.
import { describe, it, expect, vi, afterEach } from "vitest";
import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { I18nProvider } from "../lib/i18n";
import { ApiError } from "../lib/api";
import { LoadError } from "./LoadError";
import en from "../i18n/en.json";

const problem = (status: number, detail?: string) =>
  new ApiError({
    type: "about:blank",
    title: "internal error",
    status,
    ...(detail ? { detail } : {}),
  });

function LoginProbe() {
  const from = (useLocation().state as { from?: string } | null)?.from ?? "";
  return <p>login page (from {from})</p>;
}

function show(error: unknown, onRetry?: () => void, backTo?: string) {
  return render(
    <I18nProvider>
      <MemoryRouter initialEntries={["/before", "/videos/v1?tab=watch"]} initialIndex={1}>
        <Routes>
          <Route
            path="/videos/v1"
            element={<LoadError error={error} onRetry={onRetry} backTo={backTo} />}
          />
          <Route path="/login" element={<LoginProbe />} />
          <Route path="/before" element={<p>the page before</p>} />
          <Route path="/library" element={<p>library page</p>} />
        </Routes>
      </MemoryRouter>
    </I18nProvider>
  );
}

afterEach(() => vi.unstubAllGlobals());

describe("LoadError", () => {
  it("a failed fetch says so in the app's words, never the server's, and Try again retries", async () => {
    const retry = vi.fn();
    show(problem(500, "open /Volumes/media/x/index.m3u8: no such file"), retry);
    const alert = screen.getByRole("alert");
    expect(alert).toHaveAttribute("data-kind", "server");
    expect(alert).toHaveTextContent(en["error.kind.server"]);
    expect(alert).not.toHaveTextContent("/Volumes");
    expect(alert).not.toHaveTextContent("internal error");
    await userEvent.setup().click(screen.getByRole("button", { name: en["common.retry"] }));
    expect(retry).toHaveBeenCalledTimes(1);
  });

  it("a 401 offers Sign in again, which comes back here", async () => {
    show(problem(401), vi.fn());
    const alert = screen.getByRole("alert");
    expect(alert).toHaveAttribute("data-kind", "permission");
    expect(alert).toHaveTextContent(en["error.kind.signedOut"]);
    expect(screen.queryByRole("button", { name: en["common.retry"] })).toBeNull();
    await userEvent.setup().click(screen.getByRole("link", { name: en["error.signIn"] }));
    expect(screen.getByText("login page (from /videos/v1?tab=watch)")).toBeInTheDocument();
  });

  it("a 404 offers Back (to the page before, or the one named)", async () => {
    const user = userEvent.setup();
    const first = show(problem(404, "video not found"), vi.fn());
    expect(screen.getByRole("alert")).toHaveAttribute("data-kind", "not_found");
    expect(screen.getByRole("alert")).toHaveTextContent(en["error.kind.notFound"]);
    await user.click(screen.getByRole("button", { name: en["error.back"] }));
    expect(screen.getByText("the page before")).toBeInTheDocument();
    first.unmount();
    show(problem(404), vi.fn(), "/library");
    await user.click(screen.getByRole("link", { name: en["error.back"] }));
    expect(screen.getByText("library page")).toBeInTheDocument();
  });

  it("a 403's reason is not shown on a page (it would say the thing exists)", () => {
    show(problem(403, "you may not see library L2"), vi.fn());
    expect(screen.getByRole("alert")).toHaveTextContent(en["error.kind.noAccess"]);
    expect(screen.getByRole("alert")).not.toHaveTextContent("library L2");
  });

  it("offline: says so, and loads again once the network is back", () => {
    vi.stubGlobal("navigator", { ...navigator, onLine: false });
    const retry = vi.fn();
    show(new TypeError("Failed to fetch"), retry);
    expect(screen.getByRole("alert")).toHaveAttribute("data-kind", "network");
    expect(screen.getByRole("alert")).toHaveTextContent(en["error.kind.offline"]);
    act(() => {
      window.dispatchEvent(new Event("online"));
    });
    expect(retry).toHaveBeenCalledTimes(1);
  });
});
