# Story 13.6 — File drag-and-drop to add videos

Dragging a video file into the desktop app moves / copies it into a
selected library and triggers a scan.

**Anchors:** [`architecture.md` §6.4](../../architecture.md). Depends on
Epic 1 Story 1.1 (scanner), Epic 7 Story 7.3 (library CRUD).

## AC

- A drop zone overlays every page when a drag enters; the drop zone
  shows "Drop here to add to {selected library}".
- Drop semantics: copy by default; Shift to move; Cmd/Ctrl to add by
  reference (no file move, just register the path).
- File type filter: only video extensions (`.mkv`, `.mp4`, `.mov`, `.avi`,
  `.webm`, `.m4v`, etc.); reject others with a toast.
- After drop, the file appears immediately in the library list with
  `DISCOVERED` state and a "Watching" badge.
- Multi-file drops are batched: a single progress toast covers the lot.

## TC

- Drag a 1.2 GB MKV from Finder: file copies to the library root, scan
  picks it up, transcribe job enqueues.
- Drag a 50-file batch: copy proceeds in parallel up to a cap (4 parallel);
  UI shows aggregate progress.
- Drag a non-video file: rejected with a polite toast.

## EC

- Source disk runs out of space mid-copy: rollback (delete partial),
  surface a clear error.
- Permissions issue (read-only library): surface "Library is read-only —
  drop disabled" before the drop completes.
- Drag from a network share: copy works but is slow; we surface the
  expected ETA.
