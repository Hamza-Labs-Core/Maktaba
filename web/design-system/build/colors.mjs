// Colour helpers for the token build and its checks (Story 48.2).

/**
 * Parses `#RGB`, `#RRGGBB`, `#RRGGBBAA`, `rgb(r, g, b)` and `rgba(r, g, b, a)`.
 * Returns `{ r, g, b, a }`: channels 0-255, alpha 0-1. Throws on anything else.
 */
export function parseColor(input) {
  const s = String(input).trim().toLowerCase();
  let m = /^#([0-9a-f]{3})$/.exec(s);
  if (m) {
    const [r, g, b] = [...m[1]].map((c) => parseInt(c + c, 16));
    return { r, g, b, a: 1 };
  }
  m = /^#([0-9a-f]{6})([0-9a-f]{2})?$/.exec(s);
  if (m) {
    const n = parseInt(m[1], 16);
    const a = m[2] === undefined ? 1 : parseInt(m[2], 16) / 255;
    return { r: (n >> 16) & 0xff, g: (n >> 8) & 0xff, b: n & 0xff, a };
  }
  m = /^rgba?\(\s*(\d+)\s*,\s*(\d+)\s*,\s*(\d+)\s*(?:,\s*([\d.]+)\s*)?\)$/.exec(s);
  if (m) {
    return { r: +m[1], g: +m[2], b: +m[3], a: m[4] === undefined ? 1 : +m[4] };
  }
  throw new Error(`not a colour: ${JSON.stringify(input)}`);
}

/** Same colour: equal channels and alpha within one 8-bit step. */
export function sameColor(x, y) {
  const a = parseColor(x);
  const b = parseColor(y);
  return a.r === b.r && a.g === b.g && a.b === b.b && Math.abs(a.a - b.a) <= 1 / 255;
}

/** `top` painted over the opaque `bottom`. */
export function composite(top, bottom) {
  const t = parseColor(top);
  const b = parseColor(bottom);
  const mix = (x, y) => Math.round(x * t.a + y * (1 - t.a));
  return { r: mix(t.r, b.r), g: mix(t.g, b.g), b: mix(t.b, b.b), a: 1 };
}

function channel(c) {
  const v = c / 255;
  return v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4;
}

/** WCAG 2.2 relative luminance of an opaque colour. */
export function luminance(color) {
  const { r, g, b } = typeof color === "string" ? parseColor(color) : color;
  return 0.2126 * channel(r) + 0.7152 * channel(g) + 0.0722 * channel(b);
}

/** WCAG 2.2 contrast ratio of two opaque colours. */
export function contrast(x, y) {
  const [hi, lo] = [luminance(x), luminance(y)].sort((a, b) => b - a);
  return (hi + 0.05) / (lo + 0.05);
}
