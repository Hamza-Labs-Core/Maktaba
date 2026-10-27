// Back in the app's windows (no browser Back): where the viewer came from, or a
// pasted link's fallback, and from there on to the next fallback, never back and
// forth between the player and the video page.
import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { I18nProvider } from "../lib/i18n";
import { BackButton } from "./BackButton";

function app(entries: string[]) {
  return render(
    <I18nProvider>
      <MemoryRouter initialEntries={entries} initialIndex={entries.length - 1}>
        <Routes>
          <Route
            path="/videos/v1/watch"
            element={
              <>
                <p>player</p>
                <BackButton fallback="/videos/v1" />
              </>
            }
          />
          <Route
            path="/videos/v1"
            element={
              <>
                <p>video page</p>
                <BackButton fallback="/library" />
              </>
            }
          />
          <Route path="/library" element={<p>library</p>} />
          <Route path="/series/s1" element={<p>series</p>} />
        </Routes>
      </MemoryRouter>
    </I18nProvider>
  );
}

describe("BackButton", () => {
  it("goes back where the viewer came from", async () => {
    app(["/series/s1", "/videos/v1"]);
    await userEvent.setup().click(screen.getByRole("button", { name: "← Back" }));
    expect(await screen.findByText("series")).toBeInTheDocument();
  });

  it("from a pasted link: the player's fallback, then the video page's, not back and forth", async () => {
    app(["/videos/v1/watch"]);
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "← Back" }));
    expect(await screen.findByText("video page")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "← Back" }));
    expect(await screen.findByText("library")).toBeInTheDocument();
  });
});
