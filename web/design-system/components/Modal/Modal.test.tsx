import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Modal } from "./Modal";

describe("Modal", () => {
  it("renders an accessible dialog labelled by its title when open", () => {
    render(
      <Modal open onClose={() => {}} title="Confirm delete">
        <p>Body</p>
      </Modal>
    );
    const dialog = screen.getByRole("dialog");
    expect(dialog).toHaveAttribute("aria-modal", "true");
    expect(dialog).toHaveAccessibleName("Confirm delete");
  });

  it("does not render when closed", () => {
    render(
      <Modal open={false} onClose={() => {}} title="X">
        <p>Body</p>
      </Modal>
    );
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("closes on Escape (TC: modal closes on Esc)", async () => {
    const onClose = vi.fn();
    render(
      <Modal open onClose={onClose} title="X">
        <button>Inside</button>
      </Modal>
    );
    const user = userEvent.setup();
    await user.keyboard("{Escape}");
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("traps Tab focus within the dialog (TC: modal traps focus)", async () => {
    render(
      <Modal open onClose={() => {}} title="X">
        <button>First</button>
        <button>Second</button>
      </Modal>
    );
    const user = userEvent.setup();
    const first = screen.getByRole("button", { name: "First" });
    const second = screen.getByRole("button", { name: "Second" });
    const close = screen.getByRole("button", { name: "Close" });
    // Focus moved inside on open.
    expect([first, second, close]).toContain(document.activeElement);
    // Tabbing forward from the last focusable wraps back inside the dialog.
    close.focus();
    await user.tab();
    expect(screen.getByRole("dialog").contains(document.activeElement)).toBe(true);
  });
});
