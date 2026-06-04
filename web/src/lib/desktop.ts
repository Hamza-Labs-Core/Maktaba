// Desktop (Tauri) integration layer — Epic 13.
//
// The web bundle is shared between the browser app and the Tauri shell
// (apps/desktop loads web/dist). This module is the single bridge:
//
//   - isDesktop() gates every native code path so the plain browser
//     build stays inert (no @tauri-apps import is evaluated unless we
//     are actually inside the shell — keeps the web bundle dependency-free).
//   - resolveMenuAction() maps the custom menu-item / accelerator ids
//     declared in src-tauri/src/lib.rs to app actions (Stories 13.1,
//     13.7). The Rust side now emits a "menu" event for each click; the
//     React shell subscribes via registerMenuRouter().
//   - filterVideoFiles() is the drag-drop video-extension allow-list
//     (Story 13.6) — the Rust WindowEvent::DragDrop handler forwards
//     the dropped paths; we accept video containers and report the rest
//     so the UI can show the "unsupported file" toast.
//   - classifyVersionSkew() powers the Settings->About client/server
//     version comparison (Story 13.8).
//
// Native calls use dynamic import() so a non-Tauri environment never
// pulls @tauri-apps/* into the graph (those packages are devDeps of
// apps/desktop, intentionally not web/ deps).

import { deepLinkToPath } from "./native";

export function isDesktop(): boolean {
  if (typeof window === "undefined") return false;
  const w = window as unknown as Record<string, unknown>;
  return "__TAURI_INTERNALS__" in w || "__TAURI__" in w;
}

// ---------------------------------------------------------------------------
// Menu / accelerator routing (Stories 13.1, 13.7)
// ---------------------------------------------------------------------------

export type MenuAction =
  | { kind: "navigate"; to: string }
  | { kind: "navigate-library"; slot: number }
  | { kind: "scan" }
  | { kind: "new-window"; private?: boolean }
  | { kind: "open-server-picker" }
  | { kind: "media-control"; command: "play" | "pause" }
  | { kind: "external"; url: string };

const DOCS_URL = "https://maktaba.dev/docs";

/**
 * The static custom menu-item ids this layer knows how to resolve.
 *
 * CROSS-LANGUAGE CONTRACT: every id here MUST exactly match a
 * `MenuItem::with_id(...)` declaration in
 * `apps/desktop/src-tauri/src/lib.rs` (the authoritative mirror). The
 * dynamic `library-N` ids (N = 1..9) are produced by
 * `format!("library-{slot}")` there and matched by the regex in
 * `resolveMenuAction` below — they are intentionally NOT listed here.
 * `desktop.test.ts` asserts every id in this set still resolves, so a
 * TS-side rename fails loudly instead of becoming a dead no-op.
 */
export const KNOWN_MENU_IDS = [
  "preferences",
  "focus-search",
  "scan-library",
  "switch-server",
  "new-window",
  "new-private",
  "open-docs",
  // System-tray transport controls (Story 13.4) — emitted through the
  // same "menu" channel so the player can be driven without focusing
  // the window.
  "media-play",
  "media-pause",
] as const;

/**
 * Map a custom menu-item / accelerator id to an app action.
 *
 * CROSS-LANGUAGE CONTRACT: the id strings below are the cross-language
 * contract with the Rust shell. They MUST stay in sync with the
 * `MenuItem::with_id(...)` ids declared in
 * `apps/desktop/src-tauri/src/lib.rs` (the authoritative mirror) — a
 * typo on either side silently falls through to `null` and the menu
 * item becomes a dead no-op. See `KNOWN_MENU_IDS` and its test.
 *
 * Predefined items (quit/close/copy/...) and unknown ids return null —
 * those are handled natively and must not trigger a frontend action.
 */
export function resolveMenuAction(id: string): MenuAction | null {
  // Cmd+1..9 — switch to library slot N (Story 13.7).
  const libMatch = /^library-([1-9])$/.exec(id);
  if (libMatch) {
    return { kind: "navigate-library", slot: Number(libMatch[1]) };
  }

  switch (id) {
    case "preferences":
      return { kind: "navigate", to: "/settings" };
    case "focus-search":
      return { kind: "navigate", to: "/search" };
    case "scan-library":
      return { kind: "scan" };
    case "new-window":
      return { kind: "new-window" };
    case "new-private":
      return { kind: "new-window", private: true };
    case "switch-server":
      return { kind: "open-server-picker" };
    case "media-play":
      return { kind: "media-control", command: "play" };
    case "media-pause":
      return { kind: "media-control", command: "pause" };
    case "open-docs":
      return { kind: "external", url: DOCS_URL };
    default:
      return null;
  }
}

/**
 * Subscribe to the native "menu" event emitted by the Rust shell and
 * forward resolved actions to onAction. Returns a disposer. Safe to
 * call in the browser build — it becomes a no-op.
 */
export async function registerMenuRouter(
  onAction: (action: MenuAction) => void
): Promise<() => void> {
  if (!isDesktop()) return () => {};
  try {
    const { listen } = await import("@tauri-apps/api/event");
    const unlisten = await listen<string>("menu", (event) => {
      const action = resolveMenuAction(event.payload);
      if (action) onAction(action);
    });
    return () => unlisten();
  } catch {
    // @tauri-apps/api unavailable (e.g. unexpected env) — fail inert.
    return () => {};
  }
}

// ---------------------------------------------------------------------------
// Deep links — `maktaba://` (Story 13.x)
// ---------------------------------------------------------------------------

/**
 * Subscribe to the native "deep-link" event the Rust shell emits when a
 * `maktaba://…` URL is opened (incl. the cold-launch / single-instance
 * relaunch cases). Each URL is mapped to an in-app path via the SAME
 * `deepLinkToPath` parser the Capacitor shell uses, so desktop and
 * mobile resolve `maktaba://watch/123?t=42` identically. Unknown /
 * foreign URLs resolve to null and are skipped. Returns a disposer;
 * a no-op in the browser build.
 */
export async function registerDeepLinkRouter(
  onNavigate: (path: string) => void
): Promise<() => void> {
  if (!isDesktop()) return () => {};
  try {
    const { listen } = await import("@tauri-apps/api/event");
    const unlisten = await listen<string[]>("deep-link", (event) => {
      for (const url of event.payload ?? []) {
        const path = deepLinkToPath(url);
        if (path) onNavigate(path);
      }
    });
    return () => unlisten();
  } catch {
    return () => {};
  }
}

// ---------------------------------------------------------------------------
// Drag-and-drop import (Story 13.6)
// ---------------------------------------------------------------------------

export const VIDEO_EXTENSIONS = [
  "mp4",
  "mkv",
  "webm",
  "mov",
  "avi",
  "m4v",
  "ts",
  "flv",
  "wmv",
  "mpg",
  "mpeg",
] as const;

const VIDEO_EXT_SET = new Set<string>(VIDEO_EXTENSIONS);

function extensionOf(path: string): string | null {
  const base = path.split(/[\\/]/).pop() ?? path;
  const dot = base.lastIndexOf(".");
  if (dot <= 0 || dot === base.length - 1) return null;
  return base.slice(dot + 1).toLowerCase();
}

export interface FilteredFiles {
  accepted: string[];
  rejected: string[];
}

/**
 * Partition dropped file paths into video containers we can import vs
 * everything else (Story 13.6 — "video-extension filter; reject others
 * with toast").
 */
export function filterVideoFiles(paths: string[]): FilteredFiles {
  const accepted: string[] = [];
  const rejected: string[] = [];
  for (const p of paths) {
    const ext = extensionOf(p);
    if (ext && VIDEO_EXT_SET.has(ext)) accepted.push(p);
    else rejected.push(p);
  }
  return { accepted, rejected };
}

/**
 * Subscribe to the native "drop-files" event (forwarded by the Rust
 * WindowEvent::DragDrop handler). Returns a disposer; no-op in the
 * browser build.
 */
export async function registerDropHandler(
  onDrop: (files: FilteredFiles) => void
): Promise<() => void> {
  if (!isDesktop()) return () => {};
  try {
    const { listen } = await import("@tauri-apps/api/event");
    const unlisten = await listen<string[]>("drop-files", (event) => {
      onDrop(filterVideoFiles(event.payload ?? []));
    });
    return () => unlisten();
  } catch {
    return () => {};
  }
}

// ---------------------------------------------------------------------------
// Auto-update / version skew (Story 13.8)
// ---------------------------------------------------------------------------

export type VersionSkew = "ok" | "minor" | "major" | "unknown";

function parseSemver(v: string): [number, number, number] | null {
  const m = /^(\d+)\.(\d+)\.(\d+)/.exec(v.trim());
  if (!m) return null;
  return [Number(m[1]), Number(m[2]), Number(m[3])];
}

/**
 * Classify the desktop-client vs server version relationship for the
 * Settings->About skew banner (Story 13.8). Major mismatch is a hard
 * incompatibility warning; minor/patch is informational.
 */
export function classifyVersionSkew(client: string, server: string): VersionSkew {
  const c = parseSemver(client);
  const s = parseSemver(server);
  if (!c || !s) return "unknown";
  if (c[0] !== s[0]) return "major";
  if (c[1] !== s[1] || c[2] !== s[2]) return "minor";
  return "ok";
}

/**
 * Read the native app version via the app_version Tauri command
 * (declared in src-tauri/src/lib.rs). Returns null in the browser.
 */
export async function getDesktopVersion(): Promise<string | null> {
  if (!isDesktop()) return null;
  try {
    const { invoke } = await import("@tauri-apps/api/core");
    return await invoke<string>("app_version");
  } catch {
    return null;
  }
}
