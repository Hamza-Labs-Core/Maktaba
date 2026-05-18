import { describe, it, expect, vi } from "vitest";
import { useState } from "react";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Modal } from "./Modal";
import { Drawer } from "../Drawer/Drawer";

// HLB-303 a11y gap fixes — these exercise the stacked-overlay
// interaction that single-overlay tests never reached.
//
// Without the focus-trap stack (Fix #2) the "Esc closes ONLY the top"
// test fails: a single Escape used to fire BOTH traps' onClose at once
// (capture-phase document listeners are siblings; stopPropagation does
// not stop a sibling listener on the same target), collapsing the whole
// stack. Without background-inert (Fix #3) the inert assertions fail:
// nothing ever marked the background non-interactive for AT.
//
// While the Drawer is the top overlay the Modal's portal host is itself
// inert + aria-hidden (only the topmost overlay stays interactive), so
// the Modal dialog is intentionally absent from the a11y tree; we query
// it through `{ hidden: true }` / DOM presence accordingly.

function Stacked() {
  const [modalOpen, setModalOpen] = useState(true);
  const [drawerOpen, setDrawerOpen] = useState(false);
  return (
    <Modal
      open={modalOpen}
      onClose={() => setModalOpen(false)}
      title="Parent modal"
    >
      <button onClick={() => setDrawerOpen(true)}>Open drawer</button>
      <button>Modal action</button>
      <Drawer
        open={drawerOpen}
        onClose={() => setDrawerOpen(false)}
        title="Child drawer"
      >
        <button>Drawer first</button>
        <button>Drawer last</button>
      </Drawer>
    </Modal>
  );
}

describe("Nested overlays — focus-trap stack (Fix #2)", () => {
  it("Escape closes ONLY the topmost overlay, not the whole stack", async () => {
    const user = userEvent.setup();
    render(<Stacked />);

    // Open the child drawer from inside the modal.
    await user.click(screen.getByRole("button", { name: "Open drawer" }));
    expect(
      screen.getByRole("dialog", { name: "Child drawer" })
    ).toBeInTheDocument();
    // Modal host is inerted while the drawer is top → its dialog is out
    // of the a11y tree but still mounted.
    expect(
      document.querySelector('[data-mk-overlay-host="modal"]')
    ).toBeInTheDocument();

    // One Escape: only the drawer (top) closes; the modal stays open
    // and becomes the top + interactive again.
    await user.keyboard("{Escape}");
    expect(
      screen.queryByRole("dialog", { name: "Child drawer" })
    ).not.toBeInTheDocument();
    expect(
      screen.getByRole("dialog", { name: "Parent modal" })
    ).toBeInTheDocument();

    // A second Escape now closes the modal (parent trap reactivated).
    await user.keyboard("{Escape}");
    expect(
      screen.queryByRole("dialog", { name: "Parent modal" })
    ).not.toBeInTheDocument();
  });

  it("traps Tab inside the topmost overlay while a parent overlay is open", async () => {
    const user = userEvent.setup();
    render(<Stacked />);
    await user.click(screen.getByRole("button", { name: "Open drawer" }));

    const drawer = screen.getByRole("dialog", { name: "Child drawer" });
    const drawerLast = within(drawer).getByRole("button", {
      name: "Drawer last",
    });

    // Tabbing forward off the drawer's last focusable wraps back into
    // the drawer — it must NOT escape into the (dormant) parent modal.
    drawerLast.focus();
    await user.tab();
    expect(drawer.contains(document.activeElement)).toBe(true);

    // Shift+Tab off the first focusable also stays inside the drawer.
    const drawerFirst = within(drawer).getByRole("button", {
      name: "Drawer first",
    });
    drawerFirst.focus();
    await user.tab({ shift: true });
    expect(drawer.contains(document.activeElement)).toBe(true);
  });

  it("reactivates the parent trap (Tab trapped there) after the child closes", async () => {
    const user = userEvent.setup();
    render(<Stacked />);
    await user.click(screen.getByRole("button", { name: "Open drawer" }));
    await user.keyboard("{Escape}"); // close drawer

    const modal = screen.getByRole("dialog", { name: "Parent modal" });
    const modalClose = within(modal).getByRole("button", { name: "Close" });

    // Parent trap is top again: Tab off the modal's last focusable
    // wraps back inside the modal, never out to the document.
    modalClose.focus();
    await user.tab();
    expect(modal.contains(document.activeElement)).toBe(true);
  });

  it("preserves single-overlay behavior: Esc fires onClose exactly once", async () => {
    const onClose = vi.fn();
    const user = userEvent.setup();
    render(
      <Modal open onClose={onClose} title="Solo">
        <button>Only</button>
      </Modal>
    );
    await user.keyboard("{Escape}");
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("preserves single-overlay focus restore to the previously-focused element", async () => {
    const user = userEvent.setup();
    function Host() {
      const [open, setOpen] = useState(false);
      return (
        <div>
          <button onClick={() => setOpen(true)}>Trigger</button>
          <Modal open={open} onClose={() => setOpen(false)} title="M">
            <button>Inside</button>
          </Modal>
        </div>
      );
    }
    render(<Host />);
    const trigger = screen.getByRole("button", { name: "Trigger" });
    trigger.focus();
    await user.click(trigger);
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(trigger).toHaveFocus();
  });
});

describe("Nested overlays — background inert (Fix #3)", () => {
  it("marks background siblings inert + aria-hidden while open and restores on close", async () => {
    const user = userEvent.setup();

    function Host() {
      const [open, setOpen] = useState(true);
      return (
        <div data-testid="app-root">
          <button onClick={() => setOpen(false)}>bg button</button>
          <Modal open={open} onClose={() => setOpen(false)} title="M">
            <button>X</button>
          </Modal>
        </div>
      );
    }

    const { container } = render(<Host />);
    // RTL renders into a <div> that is a direct <body> sibling of the
    // Modal's portal host. While open that sibling is inert +
    // aria-hidden; the portal host itself is NOT.
    expect(container).toHaveAttribute("inert");
    expect(container).toHaveAttribute("aria-hidden", "true");
    expect(
      document.querySelector('[data-mk-overlay-host="modal"]')
    ).not.toHaveAttribute("inert");

    // Close via Escape; prior state restored exactly (attrs removed,
    // they did not exist before the overlay opened).
    await user.keyboard("{Escape}");
    expect(container).not.toHaveAttribute("inert");
    expect(container).not.toHaveAttribute("aria-hidden");
  });

  it("composes with stacking: closing the inner overlay restores one layer only", async () => {
    const user = userEvent.setup();
    const { container } = render(<Stacked />);

    // Modal open → background sibling inert.
    expect(container).toHaveAttribute("inert");

    // Open drawer (two overlays). Background still inert; the Modal's
    // own host is now inert too (only the top drawer stays live).
    await user.click(screen.getByRole("button", { name: "Open drawer" }));
    expect(container).toHaveAttribute("inert");
    expect(
      document.querySelector('[data-mk-overlay-host="modal"]')
    ).toHaveAttribute("inert");
    expect(
      document.querySelector('[data-mk-overlay-host="drawer"]')
    ).not.toHaveAttribute("inert");

    // Close drawer only — modal still open, so the background sibling
    // stays inert (refcount 2→1, not cleared) and the modal host is
    // restored to interactive.
    await user.keyboard("{Escape}");
    expect(container).toHaveAttribute("inert");
    expect(
      document.querySelector('[data-mk-overlay-host="modal"]')
    ).not.toHaveAttribute("inert");

    // Close modal — last overlay gone, background fully restored.
    await user.keyboard("{Escape}");
    expect(container).not.toHaveAttribute("inert");
    expect(container).not.toHaveAttribute("aria-hidden");
  });
});

describe("Overlay portal host — DOM hygiene on close (not just unmount)", () => {
  it("removes the portal host from <body> on close while the component stays MOUNTED, and reopening yields exactly one working dialog with inert reapplied", async () => {
    const user = userEvent.setup();

    // The component is ALWAYS mounted — only `open` toggles. This is the
    // normal state-controlled pattern. Before the fix the host <div> was
    // only removed in a [host]-dep cleanup (component unmount), so an
    // empty [data-mk-overlay-host] div lingered in <body> after close,
    // deviating from the "no orphan hosts; body child list restored
    // exactly on close" contract.
    function Host() {
      const [open, setOpen] = useState(false);
      return (
        <div data-testid="app-root">
          <button onClick={() => setOpen(true)}>open</button>
          <Modal open={open} onClose={() => setOpen(false)} title="M">
            <button>Inside</button>
          </Modal>
        </div>
      );
    }

    const { container } = render(<Host />);

    // Initially closed: no host in <body>.
    expect(
      document.querySelectorAll("[data-mk-overlay-host]")
    ).toHaveLength(0);

    // Open → exactly one host, dialog present, background inerted.
    await user.click(screen.getByRole("button", { name: "open" }));
    expect(
      document.querySelectorAll('[data-mk-overlay-host="modal"]')
    ).toHaveLength(1);
    expect(screen.getByRole("dialog", { name: "M" })).toBeInTheDocument();
    expect(container).toHaveAttribute("inert");

    // Close (component STAYS MOUNTED). The host must be gone — no orphan
    // host lingering in <body>. (Pre-fix: this assertion FAILS — an
    // empty data-mk-overlay-host div remains until unmount.)
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("dialog", { name: "M" })).not.toBeInTheDocument();
    expect(
      document.querySelectorAll("[data-mk-overlay-host]")
    ).toHaveLength(0);
    expect(container).not.toHaveAttribute("inert");

    // Reopen → still exactly ONE dialog (host re-appended synchronously,
    // not duplicated) with inert correctly reapplied.
    await user.click(screen.getByRole("button", { name: "open" }));
    expect(
      document.querySelectorAll('[data-mk-overlay-host="modal"]')
    ).toHaveLength(1);
    expect(screen.getAllByRole("dialog", { name: "M" })).toHaveLength(1);
    expect(
      within(screen.getByRole("dialog", { name: "M" })).getByRole("button", {
        name: "Inside",
      })
    ).toBeInTheDocument();
    expect(container).toHaveAttribute("inert");
    expect(container).toHaveAttribute("aria-hidden", "true");

    // Final close restores <body> exactly once more.
    await user.keyboard("{Escape}");
    expect(
      document.querySelectorAll("[data-mk-overlay-host]")
    ).toHaveLength(0);
    expect(container).not.toHaveAttribute("inert");
  });
});
