import { useEffect, useRef } from "react";

// Shared focus management for overlay primitives (Modal, Drawer). On
// open it moves focus inside, traps Tab/Shift+Tab within the container,
// invokes `onClose` on Escape, and restores focus to the previously
// active element on close. Centralised so every overlay behaves
// identically (Story 17.2 TC: modal traps focus + closes on Esc).
//
// Stacking (HLB-303 gap fix): overlays nest (Modal opens Drawer/Menu).
// Each useFocusTrap previously attached its own capture-phase document
// keydown; e.stopPropagation() does NOT stop a sibling listener bound to
// the same target, so Escape fired every trap's onClose (closing the
// whole stack) and every trap fought over Tab-wrap. We keep a single
// module-level stack: only the topmost active trap handles Escape and
// Tab. Non-top traps stay mounted but dormant; when the top closes the
// previous trap becomes top again automatically (no re-subscription —
// the stack is consulted at event time, not at bind time).

const FOCUSABLE =
  'a[href],button:not([disabled]),textarea:not([disabled]),input:not([disabled]),select:not([disabled]),[tabindex]:not([tabindex="-1"])';

interface TrapEntry {
  node: HTMLElement;
  onClose: () => void;
}

// Innermost-open overlay is last. Only the last entry is "top".
const trapStack: TrapEntry[] = [];

function isElementVisible(el: HTMLElement): boolean {
  if (el.hasAttribute("hidden")) return false;
  if (el.getAttribute("aria-hidden") === "true") return false;
  // Cheap in-browser style check; jsdom returns empty strings here so
  // this is a no-op there (we deliberately don't over-engineer for
  // jsdom layout, which it does not compute).
  if (typeof window !== "undefined" && window.getComputedStyle) {
    const style = window.getComputedStyle(el);
    if (style.display === "none" || style.visibility === "hidden") {
      return false;
    }
  }
  return true;
}

function focusablesIn(node: HTMLElement): HTMLElement[] {
  // The selector already excludes :disabled native controls; we no
  // longer re-filter on the `disabled` attribute (it was dead — the
  // selector covers the attribute case and the filter never caught
  // programmatic `.disabled = true` either). Visibility is the only
  // remaining meaningful exclusion.
  return Array.from(node.querySelectorAll<HTMLElement>(FOCUSABLE)).filter(isElementVisible);
}

// Single document-level handler shared by every trap. It dispatches to
// the topmost stack entry only, so dormant (non-top) traps never act.
let listening = false;

function handleKeyDown(e: KeyboardEvent) {
  const top = trapStack[trapStack.length - 1];
  if (!top) return;

  if (e.key === "Escape") {
    e.stopPropagation();
    top.onClose();
    return;
  }
  if (e.key !== "Tab") return;

  const items = focusablesIn(top.node);
  if (items.length === 0) {
    e.preventDefault();
    top.node.focus();
    return;
  }
  const firstEl = items[0]!;
  const lastEl = items[items.length - 1]!;
  const active = document.activeElement as HTMLElement | null;

  // If focus has escaped the top trap entirely (e.g. it lived in a now
  // dormant parent overlay), pull it back in.
  if (active && !top.node.contains(active)) {
    e.preventDefault();
    firstEl.focus();
    return;
  }
  if (e.shiftKey && active === firstEl) {
    e.preventDefault();
    lastEl.focus();
  } else if (!e.shiftKey && active === lastEl) {
    e.preventDefault();
    firstEl.focus();
  }
}

function ensureListening() {
  if (listening) return;
  document.addEventListener("keydown", handleKeyDown, true);
  listening = true;
}

function maybeStopListening() {
  if (!listening || trapStack.length > 0) return;
  document.removeEventListener("keydown", handleKeyDown, true);
  listening = false;
}

export function useFocusTrap<T extends HTMLElement>(open: boolean, onClose: () => void) {
  const ref = useRef<T>(null);
  // Keep the latest onClose without re-running the effect (so a parent
  // re-render doesn't tear down / rebuild the trap and lose its stack
  // position).
  const onCloseRef = useRef(onClose);
  onCloseRef.current = onClose;

  useEffect(() => {
    if (!open) return;
    const node = ref.current;
    if (!node) return;
    const previouslyFocused = document.activeElement as HTMLElement | null;

    const entry: TrapEntry = {
      node,
      onClose: () => onCloseRef.current(),
    };
    trapStack.push(entry);
    ensureListening();

    // Move focus inside on open (this overlay is now top).
    const first = focusablesIn(node)[0];
    (first ?? node).focus();

    return () => {
      const idx = trapStack.indexOf(entry);
      if (idx !== -1) trapStack.splice(idx, 1);
      maybeStopListening();
      // Restore focus to whatever was focused before this overlay
      // opened. If a parent trap is now top again, focus naturally
      // returns into it (its trigger / previously-focused element).
      previouslyFocused?.focus?.();
    };
  }, [open]);

  return ref;
}
