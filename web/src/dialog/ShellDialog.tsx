// Maktaba's own dialog for the desktop shell's errors (Epic 13 story 13.11; the design:
// docs/design/desktop/mockups/dialog.html): its own title bar (the app's icon and name, a
// close button; the bar drags the window), an error tile, the sentence, the programs' own
// errors as they are (left to right, in monospace), and a foot with the way to the logs at
// the start and Close at the end. Close has the focus, so Enter closes; Escape closes too.
import { useEffect, useLayoutEffect, useRef, useState } from "react";
import {
  chooseDialog,
  loadDialogContent,
  onDialogContent,
  type DialogContent,
} from "../lib/shellDialog";

/** The address in a sentence ("Cannot open http://…."), as a left-to-right run of its own:
 *  in an Arabic sentence the full stop after it stays the sentence's. */
export function withAddresses(text: string) {
  return text.split(/(https?:\/\/[^\s]*[^\s.,;:!?)])/).map((part, i) =>
    i % 2 === 1 ? (
      <bdi key={i} dir="ltr">
        {part}
      </bdi>
    ) : (
      part
    )
  );
}

export function ShellDialog() {
  const [content, setContent] = useState<DialogContent | null>(null);
  const closeRef = useRef<HTMLButtonElement>(null);

  // the newest error's words (asking shows the dialog), then any newer one's
  useEffect(() => {
    let live = true;
    void loadDialogContent().then((c) => live && c && setContent(c));
    const stop = onDialogContent(setContent);
    return () => {
      live = false;
      stop();
    };
  }, []);

  useLayoutEffect(() => {
    if (!content) return;
    document.documentElement.lang = content.lang;
    document.documentElement.dir = content.lang === "ar" ? "rtl" : "ltr";
    document.title = content.brand;
  }, [content]);

  // a new error takes the focus back to Close
  useEffect(() => {
    closeRef.current?.focus();
  }, [content]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.preventDefault();
        void chooseDialog("close");
      }
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, []);

  if (!content) return null;
  const close = () => void chooseDialog("close");
  return (
    <div
      // a new error is a new dialog: a screen reader says it again
      key={content.revision}
      className="mkt-dlg"
      role="alertdialog"
      aria-modal="true"
      aria-labelledby="mkt-dlg-title"
      aria-describedby={content.errors.length ? "mkt-dlg-errors" : undefined}
    >
      <div className="mkt-dlg__bar" data-tauri-drag-region="">
        <img src="/icons/icon-192.png" alt="" width={16} height={16} />
        <span className="mkt-dlg__brand">{content.brand}</span>
        <span className="mkt-dlg__grow" />
        <button
          type="button"
          className="mkt-dlg__x"
          aria-label={content.close}
          tabIndex={-1}
          onClick={close}
        >
          <svg viewBox="0 0 10 10" aria-hidden="true">
            <path d="M.5.5l9 9M9.5.5l-9 9" />
          </svg>
        </button>
      </div>
      <div className="mkt-dlg__body">
        <span className="mkt-dlg__icon" aria-hidden="true">
          <svg viewBox="0 0 24 24">
            <path d="M12 4 21 19.5H3z" />
            <path d="M12 10v4.5M12 17.3h.01" />
          </svg>
        </span>
        <div className="mkt-dlg__text">
          <h1 id="mkt-dlg-title">{withAddresses(content.title)}</h1>
          {content.errors.length > 0 && (
            <pre id="mkt-dlg-errors" className="mkt-dlg__errors" dir="ltr" tabIndex={0}>
              {content.errors.join("\n")}
            </pre>
          )}
        </div>
      </div>
      <div className="mkt-dlg__foot">
        {content.logs && (
          <button
            type="button"
            className="mkt-dlg__btn mkt-dlg__btn--ghost"
            onClick={() => void chooseDialog("logs")}
          >
            {content.seeLogs}
          </button>
        )}
        <span className="mkt-dlg__grow" />
        <button
          ref={closeRef}
          type="button"
          className="mkt-dlg__btn mkt-dlg__btn--primary"
          onClick={close}
        >
          {content.close}
        </button>
      </div>
    </div>
  );
}
