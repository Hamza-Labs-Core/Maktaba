// Epic 46 — the on-screen keyboard's layouts (pure) for big screen's Search.
// English: the alphabet in order (faster with a D-pad than QWERTY), then the
// digits; Arabic: the alphabet in its order, the hamza and taa marbuta forms,
// then the digits. A physical keyboard types into the field directly.

export type KeyboardLayout = "en" | "ar";

export const LAYOUTS: Record<KeyboardLayout, readonly string[][]> = {
  en: [
    ["a", "b", "c", "d", "e", "f", "g", "h", "i", "j"],
    ["k", "l", "m", "n", "o", "p", "q", "r", "s", "t"],
    ["u", "v", "w", "x", "y", "z", "'", "-", ".", "?"],
    ["1", "2", "3", "4", "5", "6", "7", "8", "9", "0"],
  ],
  ar: [
    ["ا", "ب", "ت", "ث", "ج", "ح", "خ", "د", "ذ", "ر"],
    ["ز", "س", "ش", "ص", "ض", "ط", "ظ", "ع", "غ", "ف"],
    ["ق", "ك", "ل", "م", "ن", "ه", "و", "ي", "ة", "ى"],
    ["ء", "أ", "إ", "آ", "ؤ", "ئ", "١", "٢", "٣", "٠"],
  ],
};

export type KeyEdit =
  | { kind: "char"; char: string }
  | { kind: "space" }
  | { kind: "delete" }
  | { kind: "clear" };

/** The text after an on-screen key. Delete removes one character (a whole surrogate pair). */
export function applyKey(text: string, key: KeyEdit): string {
  switch (key.kind) {
    case "char":
      return text + key.char;
    case "space":
      // no leading or doubled spaces: they search nothing
      return text === "" || text.endsWith(" ") ? text : `${text} `;
    case "delete":
      return Array.from(text).slice(0, -1).join("");
    case "clear":
      return "";
  }
}

/** The query worth searching for, or null (too short, only spaces). */
export function searchable(text: string): string | null {
  const q = text.trim();
  return Array.from(q).length >= 2 ? q : null;
}
