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
//  - It is removed on unmount, restoring the <body> child list exactly.

export function usePortalHost(open: boolean, kind: string): HTMLDivElement | null {
  const [host] = useState<HTMLDivElement | null>(() => {
    if (typeof document === "undefined") return null;
    const el = document.createElement("div");
    el.setAttribute("data-mk-overlay-host", kind);
    return el;
  });

  // Attach as early as possible: synchronously on the render that opens
  // the overlay, so the node tree is connected before effects fire.
  if (
    open &&
    host !== null &&
    typeof document !== "undefined" &&
    host.parentNode === null
  ) {
    document.body.appendChild(host);
  }

  useEffect(() => {
    if (!host) return;
    return () => {
      host.remove();
    };
  }, [host]);

  return open ? host : null;
}
