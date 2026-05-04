# Implementation Plan — Story 13.6 File Drag-and-Drop

> Companion to [story-13-06-drag-drop.md](story-13-06-drag-drop.md).
> Drag a video file → copy/move/reference into the selected library → trigger a scan.
>
> ACL: see `plan-13-01-macos.md` §Capabilities. This story requires
> `src-tauri/capabilities/fs.json` (granting `fs:allow-read-file` and
> `fs:allow-write-file` scoped to user-allowed library roots — see §1.1
> below).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Native side | Tauri 2's `WebviewWindowBuilder::on_drag_drop_event` (and `WindowEvent::DragDrop`) provides paths; we re-emit to the React layer with `selected_library_id` context. |
| Modifier semantics | Default copy; Shift = move; Cmd/Ctrl = add by reference (no file move). |
| File filter | Whitelist `.mkv .mp4 .mov .avi .webm .m4v .ts .mpg .mpeg .flv .wmv .mp3 .m4a .opus .ogg`. |
| Concurrency | Up to 4 parallel copies. |
| Server interaction | Calls `POST /api/libraries/{id}/files` for "register by reference"; for copy/move, uses Tauri's filesystem API to put the file under the library root, then calls `POST /api/libraries/{id}/scan?path=...`. Both `fs.copyFile` and `fs.renameFile` require `fs.json` capability scopes; see §1.1. |
| Out of scope | Library selection logic (Story 11.6); scanner internals (Epic 1 Story 1.1). |

## 1. Native side

In Tauri 2, the drag-drop event is `WindowEvent::DragDrop(DragDropEvent::Drop { ... })`
(NOT the Tauri 1 names `FileDrop`/`FileDropEvent::Dropped`). The
WebviewWindowBuilder hook is `on_drag_drop_event` (NOT `on_drop_event`).
Drag enter / leave are `DragDropEvent::Enter` and `DragDropEvent::Leave`.

```rust
// main.rs (or window.rs)
use tauri::{DragDropEvent, WindowEvent};

tauri::Builder::default()
    // ...
    .setup(|app| {
        let main = app.get_webview_window("main").unwrap();
        let app_handle = app.handle().clone();
        main.on_window_event(move |event| {
            if let WindowEvent::DragDrop(drag) = event {
                match drag {
                    DragDropEvent::Enter { .. } => {
                        let _ = app_handle.emit("drag-event", serde_json::json!({"type":"enter"}));
                    }
                    DragDropEvent::Leave => {
                        let _ = app_handle.emit("drag-event", serde_json::json!({"type":"leave"}));
                    }
                    DragDropEvent::Drop { paths, position: _ } => {
                        let _ = app_handle.emit("files-dropped", paths.clone());
                    }
                    _ => {}
                }
            }
        });
        Ok(())
    })
```

The per-window `dragDropEnabled` flag in `tauri.conf.json` must be `true`
for these events to fire on the main window:

```json
"app": {
  "windows": [{
    "label": "main",
    "title": "Maktaba",
    "dragDropEnabled": true
  }]
}
```

If you build additional windows from Rust (e.g. private window, Story
13.7 §6), pass `.on_drag_drop_event(...)` on the builder.

## 1.1 ACL: fs capability scope

`fs.copyFile` / `fs.renameFile` (used by the drop handler in §3) require
capability entries scoping the allowed read/write roots. Without these,
the calls fail at runtime with permission-denied. The library roots
the user has configured (Epic 7 Story 7.3) are the allowed scopes.

`src-tauri/capabilities/fs.json`:

```json
{
  "$schema": "../gen/schemas/desktop-schema.json",
  "identifier": "fs",
  "description": "Read/write under user-allowed library roots only.",
  "windows": ["main"],
  "permissions": [
    { "identifier": "fs:allow-read-file",  "allow": [{"path": "$LIBRARY_ROOTS/**"}] },
    { "identifier": "fs:allow-write-file", "allow": [{"path": "$LIBRARY_ROOTS/**"}] }
  ]
}
```

`$LIBRARY_ROOTS` is a runtime variable injected at startup from the
library configuration; the Tauri runtime expands it before applying the
ACL. (`tauri::Manager::config_mut` does **not** exist; use a managed
state struct or startup-injected variable to drive the substitution.)

## 1.2 Path-traversal validation (defense-in-depth)

Capability scoping is the primary defense, but symlinks and tampered
drag sources can still hand the app a path that points outside the
library roots. Before forwarding any dropped path to
`POST /api/libraries/{id}/files`, canonicalize and verify it resolves
under one of the user-allowed library roots:

```rust
use std::path::{Path, PathBuf};

fn is_under_root(path: &Path, allowed_roots: &[PathBuf]) -> bool {
    let canonical = path.canonicalize().ok();
    canonical.map_or(false, |p| {
        allowed_roots.iter().any(|root| p.starts_with(root))
    })
}
```

Paths failing this check are rejected with a user-visible warning toast
("This file isn't under any configured library root and can't be
imported"). **Why:** a tampered Finder window or hostile drag source
could submit `/etc/shadow` (or `C:\Windows\System32\config\SAM`); without
this check the API call would carry a server-side path the server then
trusts as a library asset.

## 2. React drop overlay

```tsx
// web/src/features/desktop/DropOverlay.tsx
import { listen } from '@tauri-apps/api/event';

export function DropOverlay() {
  const { selectedLibrary } = useSelectedLibrary();
  const [draggingOver, setOver] = useState(false);

  useEffect(() => {
    const a = listen<{type:'enter'|'leave'}>('drag-event', ({ payload }) =>
      setOver(payload.type === 'enter'));
    const b = listen<string[]>('files-dropped', async ({ payload }) => {
      setOver(false);
      await handleDrop(payload, selectedLibrary, getActiveModifier());
    });
    return () => { a.then(off => off()); b.then(off => off()); };
  }, [selectedLibrary]);

  if (!draggingOver) return null;
  return <div className="fixed inset-0 z-50 grid place-content-center bg-overlay/70 pointer-events-none">
    <p className="text-2xl">{t('desktop.drop.dropHere', { library: selectedLibrary?.name })}</p>
  </div>;
}
```

## 3. Drop handler

```ts
async function handleDrop(paths: string[], lib: LibraryDef, mode: 'copy'|'move'|'reference') {
  // Step 1: extension whitelist.
  const byExt = paths.filter(p => isVideoExt(p));
  // Step 2: defense-in-depth path-traversal check (§1.2). For 'reference' mode
  // this validates the source path; for copy/move we re-validate the destination
  // before each fs operation. The Tauri command `validate_under_root` wraps the
  // Rust `is_under_root` helper.
  const accepted: string[] = [];
  for (const p of byExt) {
    if (await invoke<boolean>('validate_under_root', { path: p })) accepted.push(p);
    else toast.warning(t('desktop.drop.rejectedTraversal', { path: p }));
  }
  const rejected = paths.length - accepted.length;
  if (rejected > 0) toast.warning(t('desktop.drop.rejected', { count: rejected }));
  if (!accepted.length) return;

  const concurrency = 4;
  const tasks = accepted.map((p, i) => ({ p, i }));

  await pool(concurrency, tasks, async ({ p }) => {
    if (mode === 'reference') {
      await api.post(`/libraries/${lib.id}/files`, { path: p });
    } else {
      const dst = libRootPath(lib) + '/' + basename(p);
      if (mode === 'copy') await fs.copyFile(p, dst);
      else                 await fs.renameFile(p, dst);
      await api.post(`/libraries/${lib.id}/scan`, { path: dst },
        { headers: { 'Idempotency-Key': uuidv4() } });
    }
  });
  toast.success(t('desktop.drop.completed', { n: accepted.length }));
}
```

`pool(concurrency, items, fn)` is a small concurrency-limited Promise.all
that aborts in-flight on partial failure (we cancel pending; in-flight
finish gracefully):

```ts
// web/src/features/desktop/pool.ts
export async function pool<T, R>(
  concurrency: number,
  items: T[],
  fn: (item: T) => Promise<R>,
): Promise<R[]> {
  const results: R[] = new Array(items.length);
  let next = 0;
  async function worker() {
    while (next < items.length) {
      const i = next++;
      results[i] = await fn(items[i]);
    }
  }
  await Promise.all(Array.from({ length: Math.min(concurrency, items.length) }, worker));
  return results;
}
```

(Cancellation hook omitted from the skeleton; pass an `AbortSignal`
through `fn` if cancellation is wired into the toast UI.)

## 4. Modifier detection

The OS captures the keyboard during a native drag, so global
`keydown`/`keyup` listeners on `window` are unreliable: modifier-key
events are not delivered while the drag is active on macOS or Windows.
The only reliable place to read modifiers is inside the WebView's HTML5
`dragover` event, where the browser exposes `event.altKey`,
`event.shiftKey`, `event.ctrlKey`, `event.metaKey` for the live drag.

```ts
// web/src/features/desktop/DropOverlay.tsx (excerpt)
const modifierRef = useRef<'copy'|'move'|'reference'>('copy');

useEffect(() => {
  const onDragOver = (e: DragEvent) => {
    e.preventDefault();   // required to allow the drop
    if (e.shiftKey)                  modifierRef.current = 'move';
    else if (e.ctrlKey || e.metaKey) modifierRef.current = 'reference';
    else                             modifierRef.current = 'copy';
  };
  window.addEventListener('dragover', onDragOver);
  return () => window.removeEventListener('dragover', onDragOver);
}, []);

function getActiveModifier(): 'copy'|'move'|'reference' {
  return modifierRef.current;
}
```

The native `DragDropEvent::Drop` callback fires immediately after the
last `dragover` tick, so `modifierRef.current` carries the modifier
state in effect at drop time.

## 5. UI feedback

- Per-batch progress toast with cancellable-per-file affordance.
- After server `scan` accepts the new file, the Library page (Story 11.1) live-updates via `/ws/library/{id}` and shows the row with `DISCOVERED` badge.

## 6. Edge cases

| Case | Handling |
|---|---|
| Source disk runs out of space mid-copy | Delete partial; surface error; mark batch as partial. |
| Read-only library | Drop overlay shows "Library is read-only — drop disabled" before drop completes (we check `lib.writable` from API). |
| Drag from network share | Copy succeeds slowly; surface ETA via byte-progress toast. |
| Non-video files | Rejected with toast count. |
| Move across volumes | `renameFile` falls back to copy + delete. |

## 7. Test cases

### 7.1 Unit

| Test | Asserts |
|---|---|
| `non-video files filtered` | `.txt` rejected; toast fires. |
| `modifier detection` | Shift → 'move'; Cmd/Ctrl → 'reference'. |
| `concurrency cap` | 50 files queued → at most 4 running at any time. |
| `read-only library disables drop` | Overlay caption changes; click no-op. |

### 7.2 Manual

- Drag 1.2 GB MKV from Finder/Explorer → copy completes; library row appears with `DISCOVERED`.
- Drag 50 files → progress aggregate; transcribe jobs enqueued.
- Drag with Shift on macOS → file moved out of source.
- Drag with Cmd/Ctrl → file remains; library row appears as a reference (`storage_kind = link`).

## 8. Performance

- Drop event → first overlay paint ≤ 100 ms.
- Throughput limited by disk speed; logic overhead < 5% over raw copy.

## 9. Dependencies

- Story 13.1 (Tauri shell).
- Epic 1 Story 1.1 (scanner) consumes the new files.
- Epic 7 Story 7.3 (libraries) provides `lib.path`, `lib.writable`.
