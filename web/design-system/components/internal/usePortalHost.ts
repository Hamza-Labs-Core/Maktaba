import { useLayoutEffect, useState } from "react";

// Shared portal-host for the overlay primitives (Modal, Drawer). Each
// open overlay gets its own stable <div> appended directly to <body>:
//
//  - It is attached in a LAYOUT effect: after React commits the overlay's
//    nodes into it and before any passive effect runs. useFocusTrap's and
//    useInertBackground's effects (passive) therefore always see it in
//    the document: the trap can .focus() a connected node, and the inert
//    pass can exclude it.
//  - Nothing touches the document during render. Under StrictMode
//    (development) React renders twice and mounts each effect, cleans it
//    up and mounts it again; an append during render leaked the discarded
//    render's host, and an overlay mounted already open
//    (`{show && <Modal open />}`) was left detached by the cleanup, its
//    dialog off the page while useInertBackground made the whole app inert
//    (the "Add Maktaba to Plex" freeze). The layout effect re-attaches on
//    each mount, so the double mount ends attached.
//  - A dedicated host (not document.body directly) gives
//    useInertBackground a precise element to exclude, so stacked
//    overlays compose coherently (only live overlays stay interactive).
//  - It is removed when the overlay CLOSES (and on unmount), restoring
//    the <body> child list exactly; re-opening re-attaches the same
//    stable node, so open→close→reopen yields exactly one dialog with
//    inert correctly reapplied.
export function usePortalHost(open: boolean, kind: string): HTMLDivElement | null {
  const [host] = useState<HTMLDivElement | null>(() => {
    if (typeof document === "undefined") return null;
    const el = document.createElement("div");
    el.setAttribute("data-mk-overlay-host", kind);
    return el;
  });

  useLayoutEffect(() => {
    if (!host || !open) return;
    if (host.parentNode === null) document.body.appendChild(host);
    return () => {
      host.remove();
    };
  }, [host, open]);

  return open ? host : null;
}
