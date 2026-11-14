// Epic 58 story 58.08 — the TV build's gate: signed out, a TV-sized page sends
// the person to the home's sign-in, which brings them back to /tv/.
import { afterEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { I18nProvider } from "../lib/i18n";
import { TestAuth } from "../test/pageHarness";
import en from "../i18n/en.json";
import ar from "../i18n/ar.json";
import { TV_SIGN_IN, TvGate } from "./TvApp";

const E = en as Record<string, string>;
const A = ar as Record<string, string>;

function renderGate() {
  return render(
    <I18nProvider>
      <TestAuth user={null}>
        <MemoryRouter>
          <TvGate />
        </MemoryRouter>
      </TestAuth>
    </I18nProvider>
  );
}

afterEach(() => {
  localStorage.clear();
  vi.unstubAllGlobals();
});

describe("TvGate", () => {
  it("signed out: the sign-in page, its only button focused, going to the home's sign-in", () => {
    localStorage.setItem("mkt:locale", "en");
    const assign = vi.fn();
    vi.stubGlobal("location", { ...window.location, assign });
    renderGate();
    expect(screen.getByTestId("tv-signin")).toHaveAttribute("dir", "ltr");
    expect(screen.getByRole("heading", { name: E["tv.signIn.title"] })).toBeInTheDocument();
    const go = screen.getByRole("button", { name: E["tv.signIn.go"] });
    expect(document.activeElement).toBe(go);
    fireEvent.click(go);
    expect(assign).toHaveBeenCalledWith(TV_SIGN_IN);
    expect(TV_SIGN_IN).toBe("/login?next=%2Ftv%2F");
  });

  it("in Arabic, right to left", () => {
    localStorage.setItem("mkt:locale", "ar");
    renderGate();
    expect(screen.getByTestId("tv-signin")).toHaveAttribute("dir", "rtl");
    expect(screen.getByRole("heading", { name: A["tv.signIn.title"] })).toBeInTheDocument();
    expect(screen.getByText(A["tv.signIn.body"]!)).toBeInTheDocument();
  });
});
