// The desktop windows' icons (Epic 13 story 13.13), drawn as the design's are
// (docs/design/desktop/mockups/kit.js): a 24-unit grid, a 1.75 stroke, round caps and joins,
// no fill, currentColor. The ones that point along the reading direction (`dir`) mirror in
// Arabic; media, checks and the rest never do.
const PATHS = {
  server:
    '<rect x="4" y="4" width="16" height="7" rx="1.8"/><rect x="4" y="13" width="16" height="7" rx="1.8"/><path d="M8 7.5h.01M8 16.5h.01"/>',
  db: '<ellipse cx="12" cy="6" rx="7" ry="2.8"/><path d="M5 6v12c0 1.5 3.1 2.8 7 2.8s7-1.3 7-2.8V6M5 12c0 1.5 3.1 2.8 7 2.8s7-1.3 7-2.8"/>',
  live: '<rect x="3" y="7" width="18" height="13" rx="2"/><path d="m8 3 4 4 4-4"/>',
  search: '<circle cx="11" cy="11" r="6.5"/><path d="m20 20-4.4-4.4"/>',
  sparkle: '<path d="M12 3.5 13.8 9 19.5 11 13.8 13 12 18.5 10.2 13 4.5 11 10.2 9z"/>',
  library: '<path d="M5 4h3v16H5zM10 4h3v16h-3z"/><path d="m15.2 4.8 2.9-.8 3.6 15.4-2.9.7z"/>',
  device: '<rect x="3" y="4.5" width="18" height="12" rx="2"/><path d="M8.5 20h7M12 16.5V20"/>',
  logs: '<path d="M7 3.5h7l4 4V20a.5.5 0 0 1-.5.5h-10.5a.5.5 0 0 1-.5-.5V4a.5.5 0 0 1 .5-.5z"/><path d="M14 3.5V8h4M9 12h6M9 15.5h6"/>',
  settings:
    '<circle cx="12" cy="12" r="2.8"/><path d="M18.47 10.69 L20.53 10.89 L20.53 13.11 L18.47 13.31 L17.50 15.65 L18.82 17.24 L17.24 18.82 L15.65 17.50 L13.31 18.47 L13.11 20.53 L10.89 20.53 L10.69 18.47 L8.35 17.50 L6.76 18.82 L5.18 17.24 L6.50 15.65 L5.53 13.31 L3.47 13.11 L3.47 10.89 L5.53 10.69 L6.50 8.35 L5.18 6.76 L6.76 5.18 L8.35 6.50 L10.69 5.53 L10.89 3.47 L13.11 3.47 L13.31 5.53 L15.65 6.50 L17.24 5.18 L18.82 6.76 L17.50 8.35Z"/>',
  flag: '<path d="M5.5 21V4M5.5 4.5h11l-2 4 2 4h-11"/>',
  pausecircle: '<circle cx="12" cy="12" r="9"/><path d="M10 9v6M14 9v6"/>',
  playcircle: '<circle cx="12" cy="12" r="9"/><path d="M10 8.5v7l5.5-3.5z"/>',
  external:
    '<path d="M14 4h6v6M20 4l-9 9M18 14v5a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V7a1 1 0 0 1 1-1h5"/>',
  update: '<path d="M20 12a8 8 0 1 1-2.3-5.6"/><path d="M20 4.5V9h-4.5"/><path d="M12 8v4.5l3 2"/>',
  check: '<path d="m5 12.5 4.5 4.5L19 7.5"/>',
  checkcircle: '<circle cx="12" cy="12" r="9"/><path d="m8 12.3 2.8 2.8L16.2 9.6"/>',
  warn: '<path d="M12 4 21 19.5H3z"/><path d="M12 10v4.5M12 17.3h.01"/>',
  folder:
    '<path d="M3.5 7.5a2 2 0 0 1 2-2h4l2 2h7a2 2 0 0 1 2 2V17a2 2 0 0 1-2 2h-13a2 2 0 0 1-2-2z"/>',
  chip: '<rect x="6.5" y="6.5" width="11" height="11" rx="1.5"/><path d="M9.5 3v3.5M14.5 3v3.5M9.5 17.5V21M14.5 17.5V21M3 9.5h3.5M3 14.5h3.5M17.5 9.5H21M17.5 14.5H21"/>',
  disk: '<rect x="3.5" y="5" width="17" height="14" rx="2"/><path d="M3.5 14.5h17M16.5 17h.01"/>',
  wifi: '<path d="M3.5 9a12 12 0 0 1 17 0M6.5 12.2a7.5 7.5 0 0 1 11 0M9.5 15.4a3.3 3.3 0 0 1 5 0M12 18.5h.01"/>',
  globe:
    '<circle cx="12" cy="12" r="9"/><path d="M3 12h18M12 3c2.5 2.5 3.6 5.7 3.6 9s-1.1 6.5-3.6 9c-2.5-2.5-3.6-5.7-3.6-9S9.5 5.5 12 3z"/>',
  onward: '<path d="M5 12h14M13 6l6 6-6 6"/>',
  plug: '<path d="M9 3.5v4M15 3.5v4M6.5 7.5h11v3a5.5 5.5 0 0 1-11 0zM12 16v4.5"/>',
} as const;

export type DesktopIconName = keyof typeof PATHS;

export function DesktopIcon({
  name,
  size = 20,
  dir = false,
  className,
}: {
  name: DesktopIconName;
  size?: 16 | 20 | 24;
  /** points along the reading direction: mirrored in Arabic */
  dir?: boolean;
  className?: string;
}) {
  return (
    <svg
      className={["mkt-dicon", dir ? "mkt-dicon--dir" : "", className ?? ""].join(" ").trim()}
      viewBox="0 0 24 24"
      width={size}
      height={size}
      aria-hidden="true"
      focusable="false"
      // the paths are this file's own constants, never data
      dangerouslySetInnerHTML={{ __html: PATHS[name] }}
    />
  );
}
