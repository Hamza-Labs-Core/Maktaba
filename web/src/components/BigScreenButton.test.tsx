// Epic 46 — the ways into big screen from the normal UI: the header button
// and the `g b` shortcut; both remember the page to come back to. Under /big
// the desk shortcuts stay quiet.
import { describe, it, expect, beforeEach } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { I18nProvider } from "../lib/i18n";
import { ShortcutProvider } from "../lib/keyboard/shortcuts";
import { returnPath } from "../lib/bigScreen";
import { BigScreenButton } from "./BigScreenButton";
import en from "../i18n/en.json";

let where = "";
function Where() {
  const l = useLocation();
  where = l.pathname + l.search;
  return null;
}

function renderAt(path: string) {
  return render(
    <I18nProvider>
      <MemoryRouter initialEntries={[path]}>
        <ShortcutProvider>
          <Where />
          <Routes>
            <Route path="*" element={<BigScreenButton />} />
          </Routes>
        </ShortcutProvider>
      </MemoryRouter>
    </I18nProvider>
  );
}

beforeEach(() => {
  sessionStorage.clear();
  localStorage.setItem("mkt:locale", "en");
});

describe("entering big screen", () => {
  it("the header button opens /big and remembers this page", () => {
    renderAt("/library/L1?tab=browse");
    fireEvent.click(
      screen.getByRole("button", { name: (en as Record<string, string>)["big.enter"] })
    );
    expect(where).toBe("/big");
    expect(returnPath()).toBe("/library/L1?tab=browse");
  });

  it("g then b opens it too", () => {
    renderAt("/series");
    fireEvent.keyDown(window, { key: "g" });
    fireEvent.keyDown(window, { key: "b" });
    expect(where).toBe("/big");
    expect(returnPath()).toBe("/series");
  });

  it("the desk shortcuts do nothing inside big screen", () => {
    renderAt("/big/search");
    fireEvent.keyDown(window, { key: "g" });
    fireEvent.keyDown(window, { key: "l" });
    expect(where).toBe("/big/search");
    fireEvent.keyDown(window, { key: "?" });
    expect(screen.queryByRole("dialog")).toBeNull();
  });
});
