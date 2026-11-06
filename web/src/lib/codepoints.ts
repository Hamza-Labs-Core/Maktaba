// Epic 52 (story 52.9): word fixes address a sentence in Unicode code points (Python
// indexes str by them, Go by runes); JavaScript strings index UTF-16 units. These convert
// between the two (an emoji is one code point and two units; a combining mark is one of
// each).

/** The code-point offset of a UTF-16 offset into text. */
export function toCodePoints(text: string, utf16: number): number {
  return Array.from(text.slice(0, Math.max(0, utf16))).length;
}

/** The UTF-16 offset of a code-point offset into text. */
export function fromCodePoints(text: string, cp: number): number {
  return Array.from(text).slice(0, Math.max(0, cp)).join("").length;
}

/** text[start, start+len) in code points. */
export function cpSlice(text: string, start: number, len: number): string {
  return Array.from(text)
    .slice(start, start + len)
    .join("");
}
