// The app renders under <React.StrictMode>, which mounts every effect, cleans it up and
// mounts it again in development. The overlay host used to be appended only during
// render and removed in that cleanup, so the second mount left the dialog in a detached
// node while useInertBackground marked the whole app inert: clicking "Add Maktaba to
// Plex" froze the page with no dialog on screen.
import { StrictMode, useState } from "react";
import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Modal } from "./Modal";
import { Drawer } from "../Drawer/Drawer";

function Page({ kind }: { kind: "modal" | "drawer" }) {
  const [open, setOpen] = useState(false);
  const Overlay = kind === "modal" ? Modal : Drawer;
  return (
    <main>
      <button onClick={() => setOpen(true)}>Open</button>
      {/* mounted already open, as the Live TV page's confirm is */}
      {open && (
        <Overlay open onClose={() => setOpen(false)} title="Confirm">
          <p>Body</p>
          <button onClick={() => setOpen(false)}>Done</button>
        </Overlay>
      )}
    </main>
  );
}

describe.each(["modal", "drawer"] as const)("%s under StrictMode", (kind) => {
  it("is on the page and leaves the app usable", async () => {
    const user = userEvent.setup();
    const { container } = render(
      <StrictMode>
        <Page kind={kind} />
      </StrictMode>
    );
    await user.click(screen.getByRole("button", { name: "Open" }));
    const dialog = screen.getByRole("dialog");
    expect(document.body.contains(dialog)).toBe(true);
    // the app's own root is inert only while the overlay is up, and the overlay is not
    const appRoot = container.closest("body > *") as HTMLElement;
    expect(appRoot.hasAttribute("inert")).toBe(true);
    expect(dialog.closest("[inert]")).toBeNull();

    await user.click(screen.getByRole("button", { name: "Done" }));
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(appRoot.hasAttribute("inert")).toBe(false);
    expect(document.querySelectorAll("[data-mk-overlay-host]").length).toBe(0);
  });
});
