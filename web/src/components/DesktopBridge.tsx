// DesktopBridge — the single React mount point for native (Tauri)
// integration (Epic 13). Rendered once inside the router so it can
// drive navigation.
//
//   - Menu items / accelerators (Stories 13.1, 13.7): the Rust shell
//     emits a "menu" event per click; registerMenuRouter() resolves it
//     to a MenuAction which we apply here (route changes done directly;
//     app-level actions re-broadcast as maktaba:* DOM events so feature
//     pages can subscribe without coupling to this component).
//   - Drag-drop import (Story 13.6): registerDropHandler() forwards the
//     filtered dropped paths; the DropOverlay renders the affordance.
//
// In a plain browser the register* helpers are inert no-ops, so this
// component costs nothing in the web build.
import { useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import {
  registerMenuRouter,
  registerDropHandler,
  type MenuAction,
  type FilteredFiles,
} from "../lib/desktop";
import { DropOverlay } from "./DropOverlay";

function emit(name: string, detail?: unknown) {
  window.dispatchEvent(new CustomEvent(name, { detail }));
}

export function DesktopBridge() {
  const navigate = useNavigate();
  const [drop, setDrop] = useState<FilteredFiles | null>(null);

  useEffect(() => {
    let disposed = false;
    let disposeMenu: (() => void) | null = null;

    const apply = (action: MenuAction) => {
      switch (action.kind) {
        case "navigate":
          navigate(action.to);
          break;
        case "navigate-library":
          // Slot maps to /library; the concrete library id is resolved
          // by the page from its loaded list (Story 13.7 Cmd+1..9).
          navigate(`/library?slot=${action.slot}`);
          break;
        case "scan":
          emit("maktaba:scan");
          break;
        case "new-window":
          emit("maktaba:new-window", { private: action.private ?? false });
          break;
        case "open-server-picker":
          emit("maktaba:open-server-picker");
          break;
        case "external":
          // Native shell handles real opening; browser falls back.
          window.open(action.url, "_blank", "noopener,noreferrer");
          break;
      }
    };

    registerMenuRouter(apply).then((d) => {
      if (disposed) d();
      else disposeMenu = d;
    });

    return () => {
      disposed = true;
      disposeMenu?.();
    };
  }, [navigate]);

  useEffect(() => {
    let disposed = false;
    let disposeDrop: (() => void) | null = null;

    registerDropHandler((files) => {
      setDrop(files);
      if (files.accepted.length > 0) {
        emit("maktaba:import-files", { paths: files.accepted });
      }
    }).then((d) => {
      if (disposed) d();
      else disposeDrop = d;
    });

    return () => {
      disposed = true;
      disposeDrop?.();
    };
  }, []);

  return (
    <DropOverlay
      active={drop !== null}
      rejectedCount={drop?.rejected.length}
      onDismiss={() => setDrop(null)}
    />
  );
}
