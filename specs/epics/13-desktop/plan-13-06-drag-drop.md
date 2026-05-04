# Implementation Plan — Story 13.6 File Drag-and-Drop

> Companion to [story-13-06-drag-drop.md](story-13-06-drag-drop.md).
> Drag a video file → copy/move/reference into the selected library → trigger a scan.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Native side | Tauri's `tauri::WebviewWindowBuilder::on_drop_event` provides paths; we re-emit to the React layer with `selected_library_id` context. |
| Modifier semantics | Default copy; Shift = move; Cmd/Ctrl = add by reference (no file move). |
| File filter | Whitelist `.mkv .mp4 .mov .avi .webm .m4v .ts .mpg .mpeg .flv .wmv .mp3 .m4a .opus .ogg`. |
| Concurrency | Up to 4 parallel copies. |
| Server interaction | Calls `POST /api/libraries/{id}/files` for "register by reference"; for copy/move, uses Tauri's filesystem API to put the file under the library root, then calls `POST /api/libraries/{id}/scan?path=...`. |
| Out of scope | Library selection logic (Story 11.6); scanner internals (Epic 1 Story 1.1). |

## 1. Native side

```rust
// main.rs (or window.rs)
tauri::Builder::default()
    // ...
    .setup(|app| {
        let main = app.get_webview_window("main").unwrap();
        let app_handle = app.handle().clone();
        main.on_window_event(move |event| {
            if let WindowEvent::FileDrop(FileDropEvent::Dropped { paths, position: _ }) = event {
                let _ = app_handle.emit("files-dropped", paths.clone());
            }
        });
        Ok(())
    })
```

Drag-enter / drag-leave events also forward to the React layer to drive the overlay.

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
  const accepted = paths.filter(p => isVideoExt(p));
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

`pool(concurrency, items, fn)` is a small helper that aborts in-flight on partial failure (we cancel pending; in-flight finish gracefully).

## 4. Modifier detection

```ts
function getActiveModifier(): 'copy'|'move'|'reference' {
  const k = currentModifierKey();   // tracked via global keydown/keyup
  if (k === 'shift')         return 'move';
  if (k === 'cmd' || k === 'ctrl') return 'reference';
  return 'copy';
}
```

A small subscription on `window.addEventListener('keydown'|'keyup')` updates a global state.

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
