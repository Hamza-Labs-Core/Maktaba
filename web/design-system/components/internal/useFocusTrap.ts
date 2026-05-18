import { useEffect, useRef } from "react";

// Shared focus management for overlay primitives (Modal, Drawer). On
// open it moves focus inside, traps Tab/Shift+Tab within the container,
// invokes `onClose` on Escape, and restores focus to the previously
// active element on close. Centralised so every overlay behaves
// identically (Story 17.2 TC: modal traps focus + closes on Esc).

const FOCUSABLE =
  'a[href],button:not([disabled]),textarea:not([disabled]),input:not([disabled]),select:not([disabled]),[tabindex]:not([tabindex="-1"])';

export function useFocusTrap<T extends HTMLElement>(
  open: boolean,
  onClose: () => void
) {
  const ref = useRef<T>(null);

  useEffect(() => {
    if (!open) return;
    const node = ref.current;
    if (!node) return;
    const previouslyFocused = document.activeElement as HTMLElement | null;

    const focusables = () =>
      Array.from(node.querySelectorAll<HTMLElement>(FOCUSABLE)).filter(
        (el) =>
          !el.hasAttribute("hidden") &&
          el.getAttribute("aria-hidden") !== "true" &&
          (el as HTMLElement).getAttribute("disabled") === null
      );

    // Move focus inside on open.
    const first = focusables()[0];
    (first ?? node).focus();

    function onKeyDown(e: KeyboardEvent) {
      if (e.key === "Escape") {
        e.stopPropagation();
        onClose();
        return;
      }
      if (e.key !== "Tab") return;
      const items = focusables();
      if (items.length === 0) {
        e.preventDefault();
        return;
      }
      const firstEl = items[0]!;
      const lastEl = items[items.length - 1]!;
      const active = document.activeElement;
      if (e.shiftKey && active === firstEl) {
        e.preventDefault();
        lastEl.focus();
      } else if (!e.shiftKey && active === lastEl) {
        e.preventDefault();
        firstEl.focus();
      }
    }

    document.addEventListener("keydown", onKeyDown, true);
    return () => {
      document.removeEventListener("keydown", onKeyDown, true);
      previouslyFocused?.focus?.();
    };
  }, [open, onClose]);

  return ref;
}
