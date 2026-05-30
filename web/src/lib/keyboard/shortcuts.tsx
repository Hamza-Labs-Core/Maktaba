// Global keyboard-shortcut layer (Story 11.9).
//
//   g l   → Library          /  → focus the header search box
//   g s   → Search           ?  → toggle the shortcut help overlay
//   g q   → Processing       Esc → close help overlay
//   g c   → Settings
//
// `g …` is a leader sequence: press `g`, then the target key within
// 1.2 s. IME guard: while `isComposing` (CJK/Arabic composition) we
// ignore keystrokes so a shortcut never fires mid-word. Typing targets
// (input/textarea/select/contenteditable) are exempt EXCEPT `?`+Esc are
// still swallowed when the help overlay is open.
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { useNavigate } from "react-router-dom";
import { Modal } from "@ds/components/Modal/Modal";
import { useI18n } from "../i18n";

interface ShortcutApi {
  focusSearchRef: (el: HTMLInputElement | null) => void;
}

const Ctx = createContext<ShortcutApi | undefined>(undefined);

export function useShortcuts(): ShortcutApi {
  const ctx = useContext(Ctx);
  if (!ctx) throw new Error("useShortcuts must be used inside <ShortcutProvider>");
  return ctx;
}

function isTypingTarget(el: EventTarget | null): boolean {
  if (!(el instanceof HTMLElement)) return false;
  const tag = el.tagName;
  return tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT" || el.isContentEditable;
}

export function ShortcutProvider({ children }: { children: ReactNode }) {
  const nav = useNavigate();
  const { t } = useI18n();
  const [helpOpen, setHelpOpen] = useState(false);
  const searchInputRef = useRef<HTMLInputElement | null>(null);
  const leaderRef = useRef(false);
  const leaderTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  const clearLeader = useCallback(() => {
    leaderRef.current = false;
    if (leaderTimer.current) {
      clearTimeout(leaderTimer.current);
      leaderTimer.current = null;
    }
  }, []);

  useEffect(() => {
    function onKeyDown(e: KeyboardEvent) {
      // IME guard — never act on a composition keystroke.
      if (e.isComposing || e.keyCode === 229) return;
      if (e.metaKey || e.ctrlKey || e.altKey) return;

      // Esc closes the help overlay from anywhere.
      if (e.key === "Escape" && helpOpen) {
        e.preventDefault();
        setHelpOpen(false);
        return;
      }

      const typing = isTypingTarget(e.target);

      // Leader follow-up (only meaningful outside text fields).
      if (leaderRef.current && !typing) {
        clearLeader();
        const k = e.key.toLowerCase();
        const dest: Record<string, string> = {
          l: "/library",
          s: "/search",
          q: "/queue",
          c: "/settings",
        };
        if (dest[k]) {
          e.preventDefault();
          nav(dest[k]);
        }
        return;
      }

      if (typing) return;

      if (e.key === "g") {
        e.preventDefault();
        leaderRef.current = true;
        if (leaderTimer.current) clearTimeout(leaderTimer.current);
        leaderTimer.current = setTimeout(clearLeader, 1200);
        return;
      }
      if (e.key === "/") {
        e.preventDefault();
        const el = searchInputRef.current;
        if (el) el.focus();
        else nav("/search");
        return;
      }
      if (e.key === "?") {
        e.preventDefault();
        setHelpOpen((v) => !v);
      }
    }

    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [nav, helpOpen, clearLeader]);

  const focusSearchRef = useCallback((el: HTMLInputElement | null) => {
    searchInputRef.current = el;
  }, []);

  const rows: Array<[string, string]> = [
    ["g l", t("shortcuts.goLibrary")],
    ["g s", t("shortcuts.goSearch")],
    ["g q", t("shortcuts.goQueue")],
    ["g c", t("shortcuts.goSettings")],
    ["/", t("shortcuts.focusSearch")],
    ["?", t("shortcuts.help")],
    ["Esc", t("shortcuts.close")],
  ];

  return (
    <Ctx.Provider value={{ focusSearchRef }}>
      {children}
      <Modal open={helpOpen} onClose={() => setHelpOpen(false)} title={t("shortcuts.title")}>
        <dl className="mkt-shortcuts">
          {rows.map(([keys, label]) => (
            <div key={keys} className="mkt-shortcuts__row">
              <dt>
                <kbd>{keys}</kbd>
              </dt>
              <dd>{label}</dd>
            </div>
          ))}
        </dl>
      </Modal>
    </Ctx.Provider>
  );
}
