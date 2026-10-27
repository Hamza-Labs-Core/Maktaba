// A page that throws while rendering is replaced by an error message; the rest
// of the app keeps working, and moving to another route clears the error (the
// Back button worked only after a reload when the admin Live TV page crashed).
// The message never shows what was thrown (it goes to the console).
import { describe, it, expect, vi, afterEach } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "../lib/i18n";
import { RouteErrorBoundary } from "./RouteErrorBoundary";
import en from "../i18n/en.json";

function Boom(): JSX.Element {
  throw new Error("Cannot read properties of null (reading 'filter') at /Volumes/media/x.mp4");
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

  it("shows a friendly error in place of the page and keeps the rest of the app", () => {
    const log = vi.spyOn(console, "error").mockImplementation(() => {});
    render(shell("/admin/live-tv", <Boom />));
    const alert = screen.getByRole("alert");
    expect(alert).toHaveTextContent(en["common.pageError"]);
    expect(alert).not.toHaveTextContent("reading 'filter'");
    expect(alert).not.toHaveTextContent("/Volumes");
    expect(screen.getByText("menu")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: en["common.reloadPage"] })).toBeInTheDocument();
    // the reason is in the console for troubleshooting
    expect(log.mock.calls.some(([m]) => m === "[page] render failed")).toBe(true);
  });

  it("Try again renders the page again", async () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    let broken = true;
    function Flaky() {
      if (broken) throw new Error("once");
      return <p>the page</p>;
    }
    render(shell("/x", <Flaky />));
    expect(screen.getByRole("alert")).toBeInTheDocument();
    broken = false;
    await userEvent.setup().click(screen.getByRole("button", { name: en["common.retry"] }));
    expect(screen.getByText("the page")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
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
