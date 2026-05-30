import { useEffect } from "react";

// HLB-303 gap fix: Modal/Drawer portal to <body> with role="dialog"
// aria-modal + a JS Tab-trap, but the rest of the page was never made
// inert — so AT virtual-cursor / swipe navigation could still walk the
// background content (the single most common modal-a11y defect).
//
// While an overlay is open we mark every <body> child that is NOT this
// overlay's portal root as inert. We set BOTH the `inert` attribute
// (real focus + AT removal in modern engines; React 18 / Vite ship it
// natively and jsdom lets us assert the attribute) AND aria-hidden as a
// belt-and-braces fallback for engines without the inert behaviour.
//
// Stacking: a refcount per element means nested overlays compose
// correctly — opening a Drawer from a Modal inerts the Modal's portal
// sibling too (only the live overlay stays interactive), and closing
// the inner overlay restores exactly one layer. We only ever clear an
// element's inert/aria-hidden when the last overlay that set it closes,
// and we never touch elements that were already inert/aria-hidden
// before any overlay opened.

// Per-element bookkeeping: how many open overlays have inerted it, plus
// the attribute values to restore. A WeakMap keyed by the element keeps
// this robust across portals re-rendering.
interface InertRecord {
  count: number;
  hadInert: boolean;
  prevAriaHidden: string | null;
}

const records = new WeakMap<Element, InertRecord>();

function applyInert(el: HTMLElement) {
  const existing = records.get(el);
  if (existing) {
    existing.count += 1;
    return;
  }
  const rec: InertRecord = {
    count: 1,
    hadInert: el.hasAttribute("inert"),
    prevAriaHidden: el.getAttribute("aria-hidden"),
  };
  records.set(el, rec);
  el.setAttribute("inert", "");
  el.setAttribute("aria-hidden", "true");
}

function releaseInert(el: HTMLElement) {
  const rec = records.get(el);
  if (!rec) return;
  rec.count -= 1;
  if (rec.count > 0) return;
  records.delete(el);
  // Restore the element to exactly its pre-overlay state.
  if (!rec.hadInert) el.removeAttribute("inert");
  if (rec.prevAriaHidden === null) {
    el.removeAttribute("aria-hidden");
  } else {
    el.setAttribute("aria-hidden", rec.prevAriaHidden);
  }
}

/**
 * While `open`, mark every <body> child except `overlayRoot` inert +
 * aria-hidden. Composes (refcounted) with stacked overlays and restores
 * prior state exactly on close.
 */
export function useInertBackground(open: boolean, overlayRoot: Element | null) {
  useEffect(() => {
    if (!open || typeof document === "undefined") return;
    if (!overlayRoot) return;

    const inerted: HTMLElement[] = [];
    for (const child of Array.from(document.body.children)) {
      if (!(child instanceof HTMLElement)) continue;
      // Skip the overlay's own portal subtree (it contains the live
      // dialog) and any element that already contains it (defensive).
      if (child === overlayRoot || child.contains(overlayRoot)) continue;
      applyInert(child);
      inerted.push(child);
    }

    return () => {
      for (const el of inerted) releaseInert(el);
    };
  }, [open, overlayRoot]);
}
