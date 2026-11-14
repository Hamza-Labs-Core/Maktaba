// Story 58.09 — a few DOM helpers for the launcher's screens (no framework: the launcher is
// small, starts fast on a 2020 TV, and changes only through a store review).

import { encodeQR } from "../../../../../web/src/lib/qr";

type Child = Node | string | null | undefined | false;

export interface Props {
  class?: string;
  id?: string;
  text?: string;
  focus?: string;
  testid?: string;
  dir?: "ltr" | "rtl" | "auto";
  lang?: string;
  role?: string;
  type?: string;
  autofocus?: boolean;
  onClick?: () => void;
  attrs?: Record<string, string>;
}

export function h(tag: string, props: Props = {}, ...children: Child[]): HTMLElement {
  const el = document.createElement(tag);
  if (props.class) el.className = props.class;
  if (props.id) el.id = props.id;
  if (props.text !== undefined) el.textContent = props.text;
  if (props.focus) el.setAttribute("data-focus", props.focus);
  if (props.testid) el.setAttribute("data-testid", props.testid);
  if (props.dir) el.dir = props.dir;
  if (props.lang) el.lang = props.lang;
  if (props.role) el.setAttribute("role", props.role);
  if (props.type) el.setAttribute("type", props.type);
  if (props.autofocus) el.setAttribute("data-autofocus", "");
  if (props.attrs) for (const k of Object.keys(props.attrs)) el.setAttribute(k, props.attrs[k]!);
  if (props.onClick) el.addEventListener("click", props.onClick);
  for (const c of children) {
    if (c === null || c === undefined || c === false) continue;
    el.appendChild(typeof c === "string" ? document.createTextNode(c) : c);
  }
  return el;
}

/** A button the remote can reach. */
export function button(
  label: string,
  focus: string,
  onClick: () => void,
  opts: { primary?: boolean; autofocus?: boolean; testid?: string; sub?: string } = {}
): HTMLElement {
  return h(
    "button",
    {
      class: `ml-btn${opts.primary ? " ml-btn--primary" : ""}`,
      type: "button",
      focus,
      autofocus: opts.autofocus,
      testid: opts.testid ?? focus,
      onClick,
    },
    h("span", { class: "ml-btn__label", text: label }),
    opts.sub ? h("span", { class: "ml-btn__sub", text: opts.sub }) : null
  );
}

/** A left-to-right run (an address, a version) inside a right-to-left sentence. */
export function ltr(text: string): HTMLElement {
  return h("bdi", { dir: "ltr", text });
}

const SVG = "http://www.w3.org/2000/svg";

/**
 * A QR code of text (the web's own encoder, web/src/lib/qr.ts): dark on white with its quiet
 * zone, which a phone's camera reads in any theme. Null when the text does not fit.
 */
export function qr(text: string, label: string, size = 320): Element | null {
  const modules = encodeQR(text);
  if (!modules) return null;
  const quiet = 4;
  const n = modules.length + quiet * 2;
  let d = "";
  modules.forEach((row, y) =>
    row.forEach((dark, x) => {
      if (dark) d += `M${x + quiet} ${y + quiet}h1v1h-1z`;
    })
  );
  const svg = document.createElementNS(SVG, "svg");
  svg.setAttribute("class", "ml-qr");
  svg.setAttribute("viewBox", `0 0 ${n} ${n}`);
  svg.setAttribute("width", String(size));
  svg.setAttribute("height", String(size));
  svg.setAttribute("role", "img");
  svg.setAttribute("aria-label", label);
  svg.setAttribute("data-testid", "cloud-qr");
  svg.setAttribute("shape-rendering", "crispEdges");
  const bg = document.createElementNS(SVG, "rect");
  bg.setAttribute("width", String(n));
  bg.setAttribute("height", String(n));
  bg.setAttribute("fill", "#fff");
  const path = document.createElementNS(SVG, "path");
  path.setAttribute("d", d);
  path.setAttribute("fill", "#000");
  svg.appendChild(bg);
  svg.appendChild(path);
  return svg;
}
