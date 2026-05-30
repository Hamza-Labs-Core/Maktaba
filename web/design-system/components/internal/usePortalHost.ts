import { useEffect, useState } from "react";

// Shared portal-host for the overlay primitives (Modal, Drawer). Each
// open overlay gets its own stable <div> appended directly to <body>:
//
//  - It is attached SYNCHRONOUSLY (state initialiser + immediate
//    append) so it is already in the document before any component
//    effect runs. This matters because useFocusTrap's effect runs
//    before the overlay body's own effects and must be able to .focus()
//    a node that is actually connected to the document.
//  - A dedicated host (not document.body directly) gives
//    useInertBackground a precise element to exclude, so stacked
//    overlays compose coherently (only live overlays stay interactive).
//  - It is removed when the overlay CLOSES (and on unmount), restoring
//    the <body> child list exactly — no orphan host lingers after close
//    when the component stays mounted (the normal state-controlled
//    `<Modal open={open} />` pattern). Re-opening re-appends the same
//    stable node synchronously, so open→close→reopen yields exactly one
//    dialog with inert correctly reapplied.

export function usePortalHost(open: boolean, kind: string): HTMLDivElement | null {
  const [host] = useState<HTMLDivElement | null>(() => {
    if (typeof document === "undefined") return null;
    const el = document.createElement("div");
    el.setAttribute("data-mk-overlay-host", kind);
    return el;
  });

  // Attach as early as possible: synchronously on the render that opens
  // the overlay, so the node tree is connected before effects fire.
  if (open && host !== null && typeof document !== "undefined" && host.parentNode === null) {
    document.body.appendChild(host);
  }

  // Remove the host when the overlay closes (and on unmount). The
  // synchronous append above runs on the opening render so the node is
  // connected before any effect (notably useFocusTrap) fires; this
  // effect's cleanup then detaches it the moment `open` flips false (or
  // the component unmounts), so the <body> child list is restored
  // exactly on close instead of lingering until unmount. The focus-trap
  // stack and useInertBackground refcount are driven by their own
  // [open]-dep effects and are unaffected by this.
  useEffect(() => {
    if (!host || !open) return;
    return () => {
      host.remove();
    };
  }, [host, open]);

  return open ? host : null;
}
