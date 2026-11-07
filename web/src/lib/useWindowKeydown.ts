// A window keydown listener that always runs the handler of the render on screen.
//
// The usual form, a useEffect that adds the listener and lists the state it reads as
// deps, re-attaches it in a passive effect after each render. Between that render's
// commit and its effect, a key still reaches the previous render's handler and acts on
// what the screen no longer shows (a review key on no word: #697, #698). Here the
// listener is attached once, and the handler it calls is set at commit, in a layout
// effect.
import { useEffect, useLayoutEffect, useRef } from "react";

export function useWindowKeydown(handler: (e: KeyboardEvent) => void, capture = false): void {
  const latest = useRef(handler);
  useLayoutEffect(() => {
    latest.current = handler;
  });
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => latest.current(e);
    window.addEventListener("keydown", onKey, capture);
    return () => window.removeEventListener("keydown", onKey, capture);
  }, [capture]);
}
