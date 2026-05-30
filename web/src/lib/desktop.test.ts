// Unit tests for the desktop (Tauri) integration layer — Epic 13.
//
// These cover the pure, runtime-agnostic logic that the native shell
// drives via emitted events:
//   - Tauri-runtime detection (13.x gating: web build must be inert)
//   - menu-event → app-action routing (13.1 / 13.7)
//   - dropped-file video-extension filter (13.6)
//   - client/server version-skew classification (13.8)
import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import {
  isDesktop,
  resolveMenuAction,
  KNOWN_MENU_IDS,
  filterVideoFiles,
  VIDEO_EXTENSIONS,
  classifyVersionSkew,
} from "./desktop";

describe("isDesktop", () => {
  afterEach(() => {
    delete (window as unknown as Record<string, unknown>).__TAURI_INTERNALS__;
    delete (window as unknown as Record<string, unknown>).__TAURI__;
  });

  it("is false in a plain browser (no Tauri globals)", () => {
    expect(isDesktop()).toBe(false);
  });

  it("is true when the Tauri v2 internals global is present", () => {
    (window as unknown as Record<string, unknown>).__TAURI_INTERNALS__ = {};
    expect(isDesktop()).toBe(true);
  });

  it("is true when the legacy __TAURI__ global is present", () => {
    (window as unknown as Record<string, unknown>).__TAURI__ = {};
    expect(isDesktop()).toBe(true);
  });
});

describe("resolveMenuAction", () => {
  it("maps preferences → navigate /settings", () => {
    expect(resolveMenuAction("preferences")).toEqual({
      kind: "navigate",
      to: "/settings",
    });
  });

  it("maps scan-library → scan", () => {
    expect(resolveMenuAction("scan-library")).toEqual({ kind: "scan" });
  });

  it("maps focus-search → navigate /search", () => {
    expect(resolveMenuAction("focus-search")).toEqual({
      kind: "navigate",
      to: "/search",
    });
  });

  it("maps switch-server → open the server picker", () => {
    expect(resolveMenuAction("switch-server")).toEqual({
      kind: "open-server-picker",
    });
  });

  it("maps open-docs → external link", () => {
    const a = resolveMenuAction("open-docs");
    expect(a).not.toBeNull();
    expect(a?.kind).toBe("external");
    if (a?.kind === "external") {
      expect(a.url).toMatch(/^https:\/\//);
    }
  });

  it("maps library-1..9 → navigate to that library slot", () => {
    expect(resolveMenuAction("library-1")).toEqual({
      kind: "navigate-library",
      slot: 1,
    });
    expect(resolveMenuAction("library-9")).toEqual({
      kind: "navigate-library",
      slot: 9,
    });
  });

  it("maps new-window / new-private to window actions", () => {
    expect(resolveMenuAction("new-window")).toEqual({ kind: "new-window" });
    expect(resolveMenuAction("new-private")).toEqual({
      kind: "new-window",
      private: true,
    });
  });

  it("returns null for an unknown / predefined menu id", () => {
    expect(resolveMenuAction("quit")).toBeNull();
    expect(resolveMenuAction("totally-unknown")).toBeNull();
  });

  // Cross-language contract guard (Rust↔TS menu-id desync): every static
  // id declared in lib.rs is mirrored by KNOWN_MENU_IDS and must resolve
  // to a real action — a future TS-side rename fails loudly here instead
  // of silently becoming a dead no-op.
  it("resolves every id in KNOWN_MENU_IDS to a non-null action", () => {
    expect(KNOWN_MENU_IDS.length).toBeGreaterThan(0);
    for (const id of KNOWN_MENU_IDS) {
      expect(resolveMenuAction(id), `menu id "${id}" must resolve`).not.toBeNull();
    }
  });

  it("KNOWN_MENU_IDS exactly matches the Rust static menu-id contract", () => {
    // Mirrors the MenuItem::with_id(...) ids in
    // apps/desktop/src-tauri/src/lib.rs (excluding dynamic library-N).
    expect([...KNOWN_MENU_IDS].sort()).toEqual(
      [
        "preferences",
        "focus-search",
        "scan-library",
        "switch-server",
        "new-window",
        "new-private",
        "open-docs",
      ].sort()
    );
  });

  it("resolves the dynamic library-N ids for N=1..9 only", () => {
    for (let n = 1; n <= 9; n++) {
      expect(resolveMenuAction(`library-${n}`), `library-${n} must resolve`).toEqual({
        kind: "navigate-library",
        slot: n,
      });
    }
    // Out of contract: slot 0 and multi-digit slots must NOT resolve.
    expect(resolveMenuAction("library-0")).toBeNull();
    expect(resolveMenuAction("library-10")).toBeNull();
    expect(resolveMenuAction("library-")).toBeNull();
  });
});

describe("filterVideoFiles", () => {
  it("accepts known video extensions case-insensitively", () => {
    const r = filterVideoFiles(["/a/clip.mp4", "/a/Talk.MKV", "/a/lecture.webm"]);
    expect(r.accepted).toEqual(["/a/clip.mp4", "/a/Talk.MKV", "/a/lecture.webm"]);
    expect(r.rejected).toEqual([]);
  });

  it("rejects non-video files and reports them", () => {
    const r = filterVideoFiles(["/a/clip.mp4", "/a/notes.pdf", "/a/cover.jpg"]);
    expect(r.accepted).toEqual(["/a/clip.mp4"]);
    expect(r.rejected).toEqual(["/a/notes.pdf", "/a/cover.jpg"]);
  });

  it("rejects extensionless paths", () => {
    const r = filterVideoFiles(["/a/README", "/a/clip.mov"]);
    expect(r.accepted).toEqual(["/a/clip.mov"]);
    expect(r.rejected).toEqual(["/a/README"]);
  });

  it("covers the documented video container set", () => {
    expect(VIDEO_EXTENSIONS).toEqual(
      expect.arrayContaining([
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
      ])
    );
  });
});

describe("classifyVersionSkew", () => {
  it("returns ok when versions match", () => {
    expect(classifyVersionSkew("1.4.2", "1.4.2")).toBe("ok");
  });

  it("returns minor when only patch/minor differs", () => {
    expect(classifyVersionSkew("1.4.2", "1.5.0")).toBe("minor");
    expect(classifyVersionSkew("1.4.2", "1.4.9")).toBe("minor");
  });

  it("returns major when the major component differs", () => {
    expect(classifyVersionSkew("1.4.2", "2.0.0")).toBe("major");
    expect(classifyVersionSkew("3.0.0", "2.9.9")).toBe("major");
  });

  it("returns unknown when either version is unparseable", () => {
    expect(classifyVersionSkew("1.4.2", "")).toBe("unknown");
    expect(classifyVersionSkew("dev", "1.0.0")).toBe("unknown");
  });
});

describe("registerMenuRouter", () => {
  beforeEach(() => {
    (window as unknown as Record<string, unknown>).__TAURI_INTERNALS__ = {};
  });
  afterEach(() => {
    delete (window as unknown as Record<string, unknown>).__TAURI_INTERNALS__;
    vi.resetModules();
  });

  it("dispatches resolved actions and unsubscribes via the returned disposer", async () => {
    type Handler = (e: { payload: string }) => void;
    let captured: Handler | null = null;
    const unlisten = vi.fn();
    vi.doMock("@tauri-apps/api/event", () => ({
      listen: vi.fn(async (_name: string, cb: Handler) => {
        captured = cb;
        return unlisten;
      }),
    }));
    const { registerMenuRouter } = await import("./desktop");
    const onAction = vi.fn();
    const dispose = await registerMenuRouter(onAction);

    expect(captured).not.toBeNull();
    captured!({ payload: "scan-library" });
    expect(onAction).toHaveBeenCalledWith({ kind: "scan" });

    captured!({ payload: "quit" }); // predefined → no action
    expect(onAction).toHaveBeenCalledTimes(1);

    dispose();
    expect(unlisten).toHaveBeenCalledTimes(1);
  });

  it("is a no-op (returns a disposer) when not running under Tauri", async () => {
    delete (window as unknown as Record<string, unknown>).__TAURI_INTERNALS__;
    const { registerMenuRouter } = await import("./desktop");
    const onAction = vi.fn();
    const dispose = await registerMenuRouter(onAction);
    expect(typeof dispose).toBe("function");
    expect(onAction).not.toHaveBeenCalled();
    dispose();
  });
});
