// Epic 46 — spatial focus for big-screen mode: feeds the pure graph
// (graph.ts) with what is on screen, keeps the focus memories, and the layers.
//
// - Focusable: an element marked data-bs-focus="<stable id>", and any enabled
//   button, link, field or tabindex >= 0 (so an ErrorState's Retry is
//   reachable too). Inside [data-bs-skip] nothing is.
// - Groups: the nearest [data-bs-group] (a row, a grid, the tab row).
// - Layers: a dialog or a panel registers itself (useLayer); while one is
//   open only its controls are reachable, and Back closes it (no traps).
//   Closing it puts focus back on what opened it.
// - Restore: the data-bs-focus id focused on each history entry is kept; on
//   coming back to that entry (Back) it is focused again once it is on the page
//   (data that loads later is waited for), else the screen's
//   [data-bs-autofocus], else its first control.
// - The mouse: pointing at a control focuses it (only after a real move, not
//   when the page scrolls under a still pointer).
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  type ReactNode,
  type RefObject,
} from "react";
import { useLocation, useNavigationType } from "react-router-dom";
import { first, next, type Direction, type FocusNode } from "./graph";

export const FOCUSABLE =
  '[data-bs-focus], button:not([disabled]), a[href], input:not([disabled]):not([type="hidden"]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])';

interface Layer {
  el: RefObject<HTMLElement | null>;
  onBack: () => void;
  /** what had focus when the layer opened */
  opener: Element | null;
}

export interface FocusApi {
  /** Moves focus that way; false when nothing lies there. */
  move: (dir: Direction) => boolean;
  /** Focuses the screen's (or the top layer's) first control. */
  focusFirst: () => void;
  /** The open layer on top, if any. */
  topLayer: () => Layer | null;
  pushLayer: (layer: Omit<Layer, "opener">) => () => void;
  /** Focuses `el` and scrolls it into view. */
  focusEl: (el: HTMLElement) => void;
}

const Ctx = createContext<FocusApi | null>(null);

export function useFocusApi(): FocusApi {
  const c = useContext(Ctx);
  if (!c) throw new Error("useFocusApi must be used inside <FocusProvider>");
  return c;
}

// the focus id each history entry left with; kept across screens (Back)
const routeMemory = new Map<string, string>();
// the node each group had focused last (focus memory for up / down)
const groupMemory = new Map<string, string>();
const autoIds = new WeakMap<Element, string>();
let autoSeq = 0;

/** Test seam. */
export function resetFocusMemory() {
  routeMemory.clear();
  groupMemory.clear();
}

function idOf(el: Element): string {
  const own = el.getAttribute("data-bs-focus");
  if (own) return own;
  let id = autoIds.get(el);
  if (!id) {
    id = `bs-auto-${++autoSeq}`;
    autoIds.set(el, id);
  }
  return id;
}

function groupOf(el: Element): string | null {
  return el.closest("[data-bs-group]")?.getAttribute("data-bs-group") ?? null;
}

function reachable(el: HTMLElement, scope: HTMLElement): boolean {
  if (el.closest("[data-bs-skip], [hidden], [inert]")) return false;
  // a layer's controls are reachable only while it is the scope
  const layer = el.closest("[data-bs-layer]");
  if (layer && layer !== scope && !scope.contains(layer)) return false;
  if (layer && scope.hasAttribute("data-bs-root")) return false;
  return true;
}

function reducedMotion(): boolean {
  return typeof window.matchMedia === "function"
    ? window.matchMedia("(prefers-reduced-motion: reduce)").matches
    : false;
}

export function FocusProvider({
  rootRef,
  children,
}: {
  rootRef: RefObject<HTMLElement | null>;
  children: ReactNode;
}) {
  const location = useLocation();
  const layers = useRef<Layer[]>([]);
  // set once the user acts or focus is placed on the entry: no late restore then
  const settled = useRef(false);

  const scope = useCallback((): HTMLElement | null => {
    const top = layers.current[layers.current.length - 1];
    return top?.el.current ?? rootRef.current;
  }, [rootRef]);

  const candidates = useCallback((): HTMLElement[] => {
    const s = scope();
    if (!s) return [];
    // with a layout (not jsdom), a control with no box (display: none) is not there
    const laidOut = s.getBoundingClientRect().width > 0;
    return Array.from(s.querySelectorAll<HTMLElement>(FOCUSABLE)).filter((el) => {
      if (!reachable(el, s)) return false;
      if (!laidOut) return true;
      const r = el.getBoundingClientRect();
      return r.width > 0 && r.height > 0;
    });
  }, [scope]);

  const focusEl = useCallback((el: HTMLElement) => {
    settled.current = true;
    el.focus({ preventScroll: true });
    el.scrollIntoView?.({
      block: "nearest",
      inline: "nearest",
      behavior: reducedMotion() ? "auto" : "smooth",
    });
  }, []);

  const focusFirst = useCallback(() => {
    const s = scope();
    if (!s) return;
    const auto = s.querySelector<HTMLElement>("[data-bs-autofocus]");
    if (auto && reachable(auto, s)) return focusEl(auto);
    const els = candidates();
    const nodes = toNodes(els);
    const rtl = getComputedStyle(s).direction === "rtl";
    const f = first(nodes, rtl);
    const el = f && els[nodes.indexOf(f)];
    if (el) focusEl(el);
  }, [scope, candidates, focusEl]);

  const move = useCallback(
    (dir: Direction) => {
      settled.current = true;
      const els = candidates();
      const nodes = toNodes(els);
      const active = document.activeElement;
      const current = active && els.includes(active as HTMLElement) ? idOf(active) : null;
      if (!current) {
        focusFirst();
        return true;
      }
      const target = next(nodes, current, dir, groupMemory);
      if (!target) return false;
      const el = els[nodes.indexOf(target)];
      if (el) focusEl(el);
      return !!el;
    },
    [candidates, focusEl, focusFirst]
  );

  const pushLayer = useCallback(
    (layer: Omit<Layer, "opener">) => {
      const entry: Layer = { ...layer, opener: document.activeElement };
      layers.current.push(entry);
      // the layer's own first control (once it has rendered)
      queueMicrotask(() => {
        if (layers.current[layers.current.length - 1] === entry) focusFirst();
      });
      return () => {
        const i = layers.current.indexOf(entry);
        if (i >= 0) layers.current.splice(i, 1);
        const opener = entry.opener as HTMLElement | null;
        if (opener && opener.isConnected && rootRef.current?.contains(opener)) {
          opener.focus({ preventScroll: true });
        }
      };
    },
    [focusFirst, rootRef]
  );

  const topLayer = useCallback(() => layers.current[layers.current.length - 1] ?? null, []);

  // memories: the focused id per history entry, and per group
  const key = location.key;
  useEffect(() => {
    const root = rootRef.current;
    if (!root) return;
    const onFocusIn = (e: FocusEvent) => {
      const el = e.target as HTMLElement | null;
      if (!el || !el.matches?.(FOCUSABLE)) return;
      const g = groupOf(el);
      if (g) groupMemory.set(g, idOf(el));
      const own = el.getAttribute("data-bs-focus");
      if (own && !el.closest("[data-bs-layer]")) routeMemory.set(key, own);
    };
    root.addEventListener("focusin", onFocusIn);
    return () => root.removeEventListener("focusin", onFocusIn);
  }, [rootRef, key]);

  // restore (or first focus) when an entry is shown; a screen that only
  // rewrites its own URL (Search's ?q=) keeps its focus
  const navType = useNavigationType();
  const shown = useRef<{ key: string; path: string } | null>(null);
  useEffect(() => {
    const root = rootRef.current;
    if (!root) return;
    const prev = shown.current;
    shown.current = { key, path: location.pathname };
    if (prev && navType === "REPLACE" && prev.path === location.pathname) {
      const had = routeMemory.get(prev.key);
      if (had) routeMemory.set(key, had);
      return;
    }
    settled.current = false;
    const want = routeMemory.get(key);
    const attempt = (): boolean => {
      if (settled.current) return true;
      if (layers.current.length > 0) return false;
      if (want) {
        const el = root.querySelector<HTMLElement>(`[data-bs-focus="${cssEscape(want)}"]`);
        if (el) {
          focusEl(el);
          return true;
        }
        return false;
      }
      const auto = root.querySelector<HTMLElement>("[data-bs-autofocus]");
      if (auto) {
        focusEl(auto);
        return true;
      }
      return false;
    };
    if (attempt()) return;
    const mo = new MutationObserver(() => {
      if (attempt()) mo.disconnect();
    });
    mo.observe(root, { childList: true, subtree: true });
    // nothing to restore turned up: the screen's first control
    const timer = setTimeout(() => {
      mo.disconnect();
      if (!settled.current && !root.contains(document.activeElement)) focusFirst();
    }, 1500);
    return () => {
      mo.disconnect();
      clearTimeout(timer);
    };
    // the path and the navigation type come with the key
  }, [key, rootRef, focusEl, focusFirst]);

  // the mouse: a real move over a control focuses it
  useEffect(() => {
    const root = rootRef.current;
    if (!root) return;
    let lastMove = 0;
    let lx = -1;
    let ly = -1;
    const onMove = (e: PointerEvent) => {
      if (e.clientX === lx && e.clientY === ly) return;
      lx = e.clientX;
      ly = e.clientY;
      lastMove = Date.now();
    };
    const onOver = (e: PointerEvent) => {
      if (Date.now() - lastMove > 500) return;
      const s = scope();
      const el = (e.target as Element | null)?.closest?.(FOCUSABLE) as HTMLElement | null;
      if (!el || !s || !s.contains(el) || !reachable(el, s) || el === document.activeElement)
        return;
      // typing stays where it is
      if (document.activeElement?.matches?.("input, textarea")) return;
      settled.current = true;
      el.focus({ preventScroll: true });
    };
    root.addEventListener("pointermove", onMove);
    root.addEventListener("pointerover", onOver);
    return () => {
      root.removeEventListener("pointermove", onMove);
      root.removeEventListener("pointerover", onOver);
    };
  }, [rootRef, scope]);

  const api = useMemo<FocusApi>(
    () => ({ move, focusFirst, topLayer, pushLayer, focusEl }),
    [move, focusFirst, topLayer, pushLayer, focusEl]
  );
  return <Ctx.Provider value={api}>{children}</Ctx.Provider>;
}

function toNodes(els: HTMLElement[]): FocusNode[] {
  return els.map((el) => {
    const r = el.getBoundingClientRect();
    // in the screen's own vertical scroller, the content's place (not the
    // viewport's): a row scrolled up under the fixed tab row still lies below it
    const scroller = el.closest<HTMLElement>("[data-bs-scroll]");
    const top = r.top + (scroller?.scrollTop ?? 0);
    return {
      id: idOf(el),
      group: groupOf(el),
      rect: { x: r.left, y: top, w: r.width, h: r.height },
    };
  });
}

function cssEscape(s: string): string {
  return typeof CSS !== "undefined" && CSS.escape ? CSS.escape(s) : s.replace(/["\\]/g, "\\$&");
}

/**
 * Registers an open dialog or panel as a layer: only its controls are
 * reachable while it is open, Back calls `onBack`, and focus returns to what
 * opened it when it closes.
 */
export function useLayer(ref: RefObject<HTMLElement | null>, onBack: () => void, open = true) {
  const { pushLayer } = useFocusApi();
  const back = useRef(onBack);
  back.current = onBack;
  useEffect(() => {
    if (!open) return;
    return pushLayer({ el: ref, onBack: () => back.current() });
  }, [open, pushLayer, ref]);
}
