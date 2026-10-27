// Epic 46 — polls the connected game controllers once a frame (the Gamepad
// API has no button events) and hands their actions on. Polling runs only
// while a pad is connected; a pad already connected before the page loaded
// shows up on its first button press (the browser's rule), which this catches
// through the "gamepadconnected" event.
import { useEffect, useRef } from "react";
import type { BigAction } from "./actions";
import { PadReader, padLayout, type PadLayout } from "./gamepad";

export function usePads(onAction: (a: BigAction, layout: PadLayout) => void) {
  const handler = useRef(onAction);
  handler.current = onAction;

  useEffect(() => {
    if (typeof navigator === "undefined" || typeof navigator.getGamepads !== "function") return;
    const readers = new Map<number, PadReader>();
    let frame = 0;

    const poll = () => {
      frame = 0;
      const pads = navigator.getGamepads?.() ?? [];
      let any = false;
      for (const pad of pads) {
        if (!pad || !pad.connected) continue;
        any = true;
        let r = readers.get(pad.index);
        if (!r) {
          r = new PadReader();
          readers.set(pad.index, r);
        }
        const layout = padLayout(pad.id);
        for (const a of r.step(pad, performance.now())) handler.current(a, layout);
      }
      if (any && document.visibilityState !== "hidden") frame = requestAnimationFrame(poll);
    };
    const start = () => {
      if (!frame) frame = requestAnimationFrame(poll);
    };
    const onDisconnect = (e: GamepadEvent) => {
      readers.delete(e.gamepad.index);
    };

    window.addEventListener("gamepadconnected", start);
    window.addEventListener("gamepaddisconnected", onDisconnect);
    document.addEventListener("visibilitychange", start);
    // a pad that was already connected
    if ((navigator.getGamepads?.() ?? []).some((p) => p?.connected)) start();
    return () => {
      window.removeEventListener("gamepadconnected", start);
      window.removeEventListener("gamepaddisconnected", onDisconnect);
      document.removeEventListener("visibilitychange", start);
      if (frame) cancelAnimationFrame(frame);
    };
  }, []);
}
