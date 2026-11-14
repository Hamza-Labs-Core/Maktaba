// Epic 58 story 58.08 — the TV build's CSS, for Chromium 68 (a 2020 Samsung or
// LG TV). The build lowers its CSS in two steps:
//
//   1. lowerForM68 (here, before Vite reads the file) rewrites what no CSS tool
//      lowers for M68:
//        - clamp(min, preferred, max) → the preferred value, and min()/max()
//          with a viewport term → that term: a TV's page is 1920 or 1280 wide,
//          where the big screen's preferred sizes are inside their bounds;
//        - gap on a flex container (M84) → margins between its children
//          (> * + * for a row or a column; after each child when it wraps);
//        - logical properties (M69/M87) → physical ones: the block axis to top and
//          bottom; the inline axis to left and right under [dir="ltr"] and
//          mirrored under [dir="rtl"] (the page's <html> carries dir); inset to
//          its four sides;
//        - aspect-ratio (M88) keeps its declaration, with a fallback for engines
//          without it: a floated ::before holds the box's height (an image keeps
//          its own ratio);
//        - @font-face sources other than WOFF2 are dropped (every TV reads it);
//   2. Lightning CSS (Vite's css.transformer, targets Chrome 68) adds the
//      prefixes and minifies.
//
// tvcheck.mjs then fails the build when the output still uses what M68 cannot
// run. A flex container's gap is set in the same rule as its display (the
// rule below can only see one rule at a time); tvcheck fails on one it missed.

/** The text of a function call's arguments split at top-level commas. */
function splitArgs(s) {
  const out = [];
  let depth = 0;
  let cur = "";
  for (const ch of s) {
    if (ch === "(") depth++;
    if (ch === ")") depth--;
    if (ch === "," && depth === 0) {
      out.push(cur.trim());
      cur = "";
    } else cur += ch;
  }
  out.push(cur.trim());
  return out;
}

/** A value split at top-level spaces (calc(a * b) stays one part). */
function splitSpaces(s) {
  const out = [];
  let depth = 0;
  let cur = "";
  for (const ch of s.trim()) {
    if (ch === "(") depth++;
    if (ch === ")") depth--;
    if (/\s/.test(ch) && depth === 0) {
      if (cur) out.push(cur);
      cur = "";
    } else cur += ch;
  }
  if (cur) out.push(cur);
  return out;
}

/** Replaces every call of `name(...)` in a value, innermost first. */
function replaceCalls(value, name, pick) {
  const re = new RegExp(`(^|[^\\w-])${name}\\(`, "i");
  for (let guard = 0; guard < 100; guard++) {
    const m = re.exec(value);
    if (!m) return value;
    const start = m.index + m[1].length;
    const open = start + name.length;
    let depth = 0;
    let end = -1;
    for (let i = open; i < value.length; i++) {
      if (value[i] === "(") depth++;
      else if (value[i] === ")" && --depth === 0) {
        end = i;
        break;
      }
    }
    if (end < 0) return value;
    const args = splitArgs(value.slice(open + 1, end));
    value = value.slice(0, start) + pick(args) + value.slice(end + 1);
  }
  return value;
}

const viewportTerm = (args) =>
  args.find((a) => /v(w|h|min|max)\b/.test(a)) ?? args[0];

/** A declaration value with clamp(), min() and max() resolved for a TV's page. */
export function lowerValue(value) {
  let v = replaceCalls(value, "clamp", (a) => a[1] ?? a[0]);
  v = replaceCalls(v, "min", viewportTerm);
  v = replaceCalls(v, "max", viewportTerm);
  return v;
}

function decls(body) {
  return body
    .split(";")
    .map((d) => d.trim())
    .filter(Boolean)
    .map((d) => {
      const i = d.indexOf(":");
      if (i < 0) return null;
      const k = d.slice(0, i).trim();
      // a custom property's name keeps its case
      return [k.startsWith("--") ? k : k.toLowerCase(), d.slice(i + 1).trim()];
    })
    .filter(Boolean);
}

/** The rules a flex container's gap becomes, or null when the rule has none. */
function flexGap(selector, list) {
  const get = (p) => [...list].reverse().find(([k]) => k === p)?.[1];
  const display = get("display");
  if (!display || !/^(inline-)?flex\b/.test(display)) return null;
  const gap = get("gap");
  const rowGap = get("row-gap");
  const colGap = get("column-gap");
  if (!gap && !rowGap && !colGap) return null;
  const parts = gap ? splitSpaces(lowerValue(gap)) : [];
  const row = rowGap ? lowerValue(rowGap) : (parts[0] ?? "0");
  const col = colGap ? lowerValue(colGap) : (parts[1] ?? parts[0] ?? "0");
  const flow = get("flex-flow") ?? "";
  const dir = get("flex-direction") ?? flow;
  const wrap = /\bwrap\b/.test(get("flex-wrap") ?? flow);
  const column = /\bcolumn\b/.test(dir);
  const sels = splitArgs(selector);
  const kids = (suffix) => sels.map((s) => `${s} > *${suffix}`).join(", ");
  if (wrap)
    return column
      ? `${kids("")} { margin-block-end: ${row}; margin-inline-end: ${col}; }`
      : `${kids("")} { margin-inline-end: ${col}; margin-block-end: ${row}; }`;
  return column
    ? `${kids(" + *")} { margin-block-start: ${row}; margin-inline-start: 0; }`
    : `${kids(" + *")} { margin-inline-start: ${col}; }`;
}

const SIDES = {
  start: { ltr: "left", rtl: "right" },
  end: { ltr: "right", rtl: "left" },
};

/** A box property's physical name for a side: margin/padding/inset/border(+suffix). */
function physical(base, side, suffix) {
  if (base === "inset") return side;
  return `${base}-${side}${suffix}`;
}

/**
 * One declaration lowered: [declarations for the rule itself, for [dir=ltr],
 * for [dir=rtl]]. Anything not logical is the rule's own, as it was.
 */
export function lowerDecl(k, v) {
  const m =
    /^(margin|padding|inset|border)-(block|inline)(?:-(start|end))?(-width|-color|-style)?$/.exec(
      k,
    );
  if (m) {
    const [, base, axis, side, suffix = ""] = m;
    if (base !== "border" && suffix) return [[[k, v]], [], []];
    const parts = base === "border" ? [v] : splitSpaces(v);
    const first = parts[0];
    const second = parts[1] ?? parts[0];
    if (axis === "block") {
      if (side)
        return [
          [[physical(base, side === "start" ? "top" : "bottom", suffix), v]],
          [],
          [],
        ];
      return [
        [
          [physical(base, "top", suffix), first],
          [physical(base, "bottom", suffix), second],
        ],
        [],
        [],
      ];
    }
    if (side) {
      return [
        [],
        [[physical(base, SIDES[side].ltr, suffix), v]],
        [[physical(base, SIDES[side].rtl, suffix), v]],
      ];
    }
    // one value: the same on both sides, whatever the direction
    if (parts.length === 1 || first === second)
      return [
        [
          [physical(base, "left", suffix), first],
          [physical(base, "right", suffix), first],
        ],
        [],
        [],
      ];
    return [
      [],
      [
        [physical(base, "left", suffix), first],
        [physical(base, "right", suffix), second],
      ],
      [
        [physical(base, "right", suffix), first],
        [physical(base, "left", suffix), second],
      ],
    ];
  }
  if (k === "inset") {
    const p = splitSpaces(v);
    const [t, r = t, b = t, l = r] = p;
    return [
      [
        ["top", t],
        ["right", r],
        ["bottom", b],
        ["left", l],
      ],
      [],
      [],
    ];
  }
  return [[[k, v]], [], []];
}

/** The aspect-ratio fallback of a rule: its ratio as a percentage of the width. */
function ratioFallback(selector, value) {
  const sels = splitArgs(selector);
  if (value.trim() === "auto") {
    // a variant turning the ratio off turns its fallback off too
    const pseudo = sels
      .flatMap((s) => [`${s}::before`, `${s}::after`])
      .join(", ");
    return `@supports not (aspect-ratio: 1 / 1) { ${pseudo} { content: none; padding-top: 0; } }`;
  }
  const m = /^\s*([\d.]+)\s*(?:\/\s*([\d.]+))?\s*$/.exec(value);
  if (!m) return "";
  const w = Number(m[1]);
  const h = Number(m[2] ?? 1);
  if (!(w > 0 && h > 0)) return "";
  const pct = ((h / w) * 100).toFixed(4).replace(/\.?0+$/, "");
  const before = sels.map((s) => `${s}::before`).join(", ");
  const after = sels.map((s) => `${s}::after`).join(", ");
  return `@supports not (aspect-ratio: 1 / 1) { ${before} { content: ""; float: left; padding-top: ${pct}%; } ${after} { content: ""; display: block; clear: both; } }`;
}

/** A selector under a direction: [dir="rtl"] .a (html and :root take it themselves). */
function underDir(selector, dir) {
  return splitArgs(selector)
    .map((s) => {
      if (/^html\b/.test(s)) return s.replace(/^html/, `html[dir="${dir}"]`);
      if (/^:root\b/.test(s)) return s.replace(/^:root/, `:root[dir="${dir}"]`);
      return `[dir="${dir}"] ${s}`;
    })
    .join(", ");
}

const FONT_SRC = /url\([^)]*\)\s*format\(\s*["']?(?!woff2)[\w-]+["']?\s*\)/g;

/** The whole stylesheet lowered for Chromium 68 (step 1 above). */
export function lowerForM68(css) {
  // comments go first: one may hold braces, colons or semicolons
  return css
    .replace(/\/\*[\s\S]*?\*\//g, "")
    .replace(/([^{}]*)\{([^{}]*)\}/g, (whole, rawSel, body) => {
      const selector = rawSel.replace(/\/\*[\s\S]*?\*\//g, "").trim();
      if (selector.startsWith("@font-face")) {
        const kept = body.replace(/src\s*:([^;]*)/g, (_, srcs) => {
          const woff2 = splitArgs(srcs).filter(
            (s) => /format\(\s*["']?woff2/.test(s) || !/url\(/.test(s),
          );
          return `src: ${woff2.join(", ")}`;
        });
        return `${rawSel}{${kept.replace(FONT_SRC, "")}}`;
      }
      if (!selector || selector.startsWith("@")) return whole;
      const list = decls(body);
      const extra = flexGap(selector, list);
      const own = [];
      const ltr = [];
      const rtl = [];
      let ratio = "";
      for (const [k, raw] of list) {
        if (extra && (k === "gap" || k === "row-gap" || k === "column-gap"))
          continue;
        const v = lowerValue(raw);
        if (k === "aspect-ratio") ratio = ratioFallback(selector, v);
        const [o, l, r] = lowerDecl(k, v);
        own.push(...o);
        ltr.push(...l);
        rtl.push(...r);
      }
      const block = (sel, ds) =>
        `${sel} {\n${ds.map(([k, v]) => `  ${k}: ${v};`).join("\n")}\n}`;
      const out = [
        `${rawSel}{\n${own.map(([k, v]) => `  ${k}: ${v};`).join("\n")}\n}`,
      ];
      if (ltr.length) out.push(block(underDir(selector, "ltr"), ltr));
      if (rtl.length) out.push(block(underDir(selector, "rtl"), rtl));
      // the gap's margins are logical too: lowered the same way
      if (extra) out.push(lowerForM68(extra));
      if (ratio) out.push(ratio);
      return out.join("\n");
    });
}
