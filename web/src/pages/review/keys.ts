// The review queue's keys (Epic 52, story 52.11 §3). Physical keys (KeyboardEvent.code),
// so an Arabic layout's ن is still N; letters are off while the edit field has focus, and
// J/K mean next/previous in either direction of the page.
export type KeyAct =
  | {
      kind:
        | "next"
        | "prev"
        | "replay"
        | "accept"
        | "edit"
        | "name"
        | "dictionary"
        | "skip"
        | "applyAll"
        | "undo"
        | "help"
        | "leave";
    }
  | { kind: "play"; slow: boolean }
  | { kind: "candidate"; n: number };

export function keyAction(e: KeyboardEvent, inField: boolean): KeyAct | null {
  if (e.altKey) return null;
  const mod = e.ctrlKey || e.metaKey;
  if (inField) {
    if (e.code === "Enter") return { kind: "accept" };
    if (e.code === "Escape") return { kind: "leave" };
    return null;
  }
  const target = e.target as HTMLElement | null;
  if (
    target &&
    (target.tagName === "INPUT" || target.tagName === "SELECT" || target.tagName === "TEXTAREA")
  )
    return null;
  if (mod) return e.code === "KeyZ" ? { kind: "undo" } : null;
  switch (e.code) {
    case "KeyJ":
    case "ArrowDown":
      return { kind: "next" };
    case "KeyK":
    case "ArrowUp":
      return { kind: "prev" };
    case "Space":
      return { kind: "play", slow: e.shiftKey };
    case "KeyR":
      return { kind: "replay" };
    case "Enter":
      return { kind: "accept" };
    case "KeyE":
      return { kind: "edit" };
    case "KeyN":
      return { kind: "name" };
    case "KeyD":
      return { kind: "dictionary" };
    case "KeyS":
      return { kind: "skip" };
    case "KeyA":
      return { kind: "applyAll" };
    case "KeyU":
      return { kind: "undo" };
    case "Slash":
      return e.shiftKey ? { kind: "help" } : null;
  }
  const digit = /^Digit([1-5])$/.exec(e.code);
  return digit ? { kind: "candidate", n: Number(digit[1]) - 1 } : null;
}
