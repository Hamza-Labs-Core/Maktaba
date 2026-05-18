// Drag-and-drop import overlay (Story 13.6).
//
// Rendered by DesktopBridge while a native drag is over the window. The
// Rust WindowEvent::DragDrop handler emits "drop-enter"/"drop-leave"
// (overlay visibility) and "drop-files" (the dropped paths). This
// component is purely presentational — file filtering and the import
// call live in the bridge / desktop.ts so they can be unit-tested
// without a DOM.
interface DropOverlayProps {
  active: boolean;
  libraryName?: string;
  /** Number of dropped files rejected by the video-extension filter. */
  rejectedCount?: number;
  onDismiss?: () => void;
}

export function DropOverlay({ active, libraryName, rejectedCount, onDismiss }: DropOverlayProps) {
  if (!active) return null;

  const target = libraryName ? `Drop here to add to ${libraryName}` : "Drop here to add";

  return (
    <div className="mkt-drop-overlay" role="status" aria-live="polite">
      <div className="mkt-drop-overlay__card">
        <p className="mkt-drop-overlay__title">{target}</p>
        {(rejectedCount ?? 0) > 0 ? (
          <p className="mkt-drop-overlay__warn">
            {rejectedCount} file(s) skipped (unsupported)
            {onDismiss ? (
              <button
                type="button"
                className="mkt-btn mkt-btn--ghost"
                onClick={onDismiss}
                aria-label="Dismiss"
              >
                Dismiss
              </button>
            ) : null}
          </p>
        ) : null}
      </div>
    </div>
  );
}
