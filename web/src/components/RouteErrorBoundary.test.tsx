// A page that throws while rendering is replaced by an error message; the rest
// of the app keeps working, and moving to another route clears the error (the
// Back button worked only after a reload when the admin Live TV page crashed).
import { describe, it, expect, vi, afterEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { I18nProvider } from "../lib/i18n";
import { RouteErrorBoundary } from "./RouteErrorBoundary";

function Boom(): JSX.Element {
  throw new Error("Cannot read properties of null (reading 'filter')");
}

function shell(route: string, page: JSX.Element) {
  return (
    <I18nProvider>
      <nav>menu</nav>
      <RouteErrorBoundary resetKey={route}>{page}</RouteErrorBoundary>
    </I18nProvider>
  );
}

describe("RouteErrorBoundary", () => {
  afterEach(() => vi.restoreAllMocks());

  it("shows the error in place of the page and keeps the rest of the app", () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    render(shell("/admin/live-tv", <Boom />));
    expect(screen.getByRole("alert")).toHaveTextContent("reading 'filter'");
    expect(screen.getByText("menu")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /reload/i })).toBeInTheDocument();
  });

  it("clears the error when the route changes", () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    const { rerender } = render(shell("/admin/live-tv", <Boom />));
    expect(screen.getByRole("alert")).toBeInTheDocument();
    rerender(shell("/library", <p>library page</p>));
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(screen.getByText("library page")).toBeInTheDocument();
  });

  it("renders the page as it is when nothing throws", () => {
    render(shell("/library", <p>library page</p>));
    expect(screen.getByText("library page")).toBeInTheDocument();
  });
});
