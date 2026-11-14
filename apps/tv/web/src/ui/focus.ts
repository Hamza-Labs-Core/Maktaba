// Story 58.09 — the D-pad on the launcher's own screens. Its screens are columns of
// buttons, so Up and Down step through them in order; Left and Right do the same along the
// reading direction (mirrored in Arabic). In a text field Left and Right move the caret,
// and only Up and Down leave it.

export type Move = "up" | "down" | "left" | "right";

/** The reachable controls of root, in order. */
export function focusables(root: ParentNode): HTMLElement[] {
  return Array.from(root.querySelectorAll<HTMLElement>("[data-focus]")).filter((el) => {
    if ((el as HTMLButtonElement).disabled || el.closest("[hidden]")) return false;
    // inside a closed <details>, only its <summary> is shown
    const closed = el.closest("details:not([open])");
    return !closed || el.tagName === "SUMMARY";
  });
}

/** The step a move makes through the list: -1, +1, or 0 (left to the text field). */
export function step(move: Move, rtl: boolean, inText: boolean): number {
  if (move === "up") return -1;
  if (move === "down") return 1;
  if (inText) return 0;
  const forward = move === "right" ? 1 : -1;
  return rtl ? -forward : forward;
}

/** Moves focus within root; true when the key was used. */
export function moveFocus(root: ParentNode, move: Move, rtl: boolean): boolean {
  const list = focusables(root);
  if (list.length === 0) return false;
  const active = document.activeElement as HTMLElement | null;
  const at = active ? list.indexOf(active) : -1;
  const inText = !!active && active.tagName === "INPUT";
  const d = step(move, rtl, inText);
  if (d === 0) return false;
  const next = at < 0 ? 0 : Math.max(0, Math.min(list.length - 1, at + d));
  list[next]!.focus();
  return true;
}

/** Focuses the screen's first control (or the one marked data-autofocus). */
export function focusFirst(root: ParentNode): void {
  const marked = root.querySelector<HTMLElement>("[data-autofocus]");
  const el = marked ?? focusables(root)[0];
  el?.focus();
}
