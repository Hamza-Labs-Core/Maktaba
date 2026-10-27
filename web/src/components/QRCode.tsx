// A QR code of `value`, drawn as an SVG (lib/qr.ts encodes it, in the browser). Always
// dark on white with its quiet zone, in light and dark themes alike: that is what a
// camera reads.
import { useMemo } from "react";
import { encodeQR } from "../lib/qr";

/** One SVG path of the dark modules (one unit per module, offset by the quiet zone). */
export function qrPath(modules: boolean[][], quiet = 4): string {
  let d = "";
  modules.forEach((row, y) =>
    row.forEach((dark, x) => {
      if (dark) d += `M${x + quiet} ${y + quiet}h1v1h-1z`;
    })
  );
  return d;
}

export function QRCode({
  value,
  label,
  size = 224,
}: {
  value: string;
  /** What the code opens, for screen readers. */
  label: string;
  size?: number;
}) {
  const modules = useMemo(() => encodeQR(value), [value]);
  if (!modules) return null;
  const quiet = 4;
  const n = modules.length + quiet * 2;
  return (
    <svg
      className="mkt-qr"
      role="img"
      aria-label={label}
      width={size}
      height={size}
      viewBox={`0 0 ${n} ${n}`}
      shapeRendering="crispEdges"
      data-modules={modules.length}
    >
      <rect width={n} height={n} fill="#fff" />
      <path d={qrPath(modules, quiet)} fill="#000" />
    </svg>
  );
}
