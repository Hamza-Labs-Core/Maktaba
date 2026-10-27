// Epic 46 — where a D-pad move goes (big-screen mode).
//
// Pure: given the focusable nodes on screen (an id, a rectangle and the group
// they belong to: a row, a grid line, the tab row) and a direction, `next`
// returns the node focus moves to, or null when nothing lies that way.
//
//   - Candidates lie in the direction of the move: their near edge is past
//     the current node's centre.
//   - Left / right stay in the current group when a node of it lies that way;
//     at a row's end focus stays put (no wrap, no jump to another row).
//   - Up / down go to the nearest group that way and land on the node that
//     group last had focused when it is still there (focus memory), else on
//     the node nearest the current one across the axis.
//   - The score is the distance along the move plus twice the distance
//     across it, so a node straight ahead beats a closer one off to the side.
//
// Navigation follows the screen's geometry, so a mirrored (RTL) layout
// mirrors the navigation with no remapping of keys.

export type Direction = "up" | "down" | "left" | "right";

export interface Rect {
  x: number;
  y: number;
  w: number;
  h: number;
}

export interface FocusNode {
  id: string;
  rect: Rect;
  /** The row / grid / bar the node belongs to; nodes without one are each their own. */
  group?: string | null;
}

/** The node each group had focused last (group → node id). */
export type FocusMemory = ReadonlyMap<string, string>;

const cx = (r: Rect) => r.x + r.w / 2;
const cy = (r: Rect) => r.y + r.h / 2;

/** Whether `to` lies in direction `dir` from `from`. */
export function liesToward(from: Rect, to: Rect, dir: Direction): boolean {
  switch (dir) {
    case "left":
      return to.x + to.w <= cx(from) + 0.5 && cx(to) < cx(from);
    case "right":
      return to.x >= cx(from) - 0.5 && cx(to) > cx(from);
    case "up":
      return to.y + to.h <= cy(from) + 0.5 && cy(to) < cy(from);
    case "down":
      return to.y >= cy(from) - 0.5 && cy(to) > cy(from);
  }
}

/**
 * The cost of moving from `from` to `to` in `dir`: the gap along the move
 * (edge to edge) plus twice the offset across it (centre to centre).
 */
export function score(from: Rect, to: Rect, dir: Direction): number {
  const horizontal = dir === "left" || dir === "right";
  const along = horizontal
    ? dir === "right"
      ? Math.max(0, to.x - (from.x + from.w))
      : Math.max(0, from.x - (to.x + to.w))
    : dir === "down"
      ? Math.max(0, to.y - (from.y + from.h))
      : Math.max(0, from.y - (to.y + to.h));
  const across = horizontal ? Math.abs(cy(to) - cy(from)) : Math.abs(cx(to) - cx(from));
  return along + 2 * across;
}

function best(from: Rect, nodes: FocusNode[], dir: Direction): FocusNode | null {
  let pick: FocusNode | null = null;
  let pickScore = Infinity;
  for (const n of nodes) {
    const s = score(from, n.rect, dir);
    if (s < pickScore) {
      pick = n;
      pickScore = s;
    }
  }
  return pick;
}

const groupOf = (n: FocusNode) => n.group ?? `#${n.id}`;

/**
 * The node focus moves to from `currentId` in `dir`, or null. With no current
 * node (nothing focused yet, or it left the page) the first node in reading
 * order is the answer to any move.
 */
export function next(
  nodes: readonly FocusNode[],
  currentId: string | null,
  dir: Direction,
  memory: FocusMemory = new Map()
): FocusNode | null {
  const current = currentId ? nodes.find((n) => n.id === currentId) : undefined;
  if (!current) return first(nodes);
  const from = current.rect;
  const group = groupOf(current);
  const toward = nodes.filter((n) => n.id !== current.id && liesToward(from, n.rect, dir));
  if (toward.length === 0) return null;

  if (dir === "left" || dir === "right") {
    // a row keeps horizontal moves; outside it only what shares the line
    const same = toward.filter((n) => groupOf(n) === group);
    if (same.length > 0) return best(from, same, dir);
    if (current.group) {
      // the end of a row: focus stays (no jump into the row above or below)
      const overlaps = toward.filter((n) => overlapsAcross(from, n.rect, dir));
      return overlaps.length > 0 ? best(from, overlaps, dir) : null;
    }
    return best(from, toward, dir);
  }

  // up / down: the nearest line of another group that way (the next row, even
  // when its cards lie off to the side), then the node to land on
  const others = toward.filter((n) => groupOf(n) !== group);
  const gap = Math.min(...others.map((n) => alongGap(from, n.rect, dir)));
  const nearest = best(
    from,
    others.filter((n) => alongGap(from, n.rect, dir) <= gap + 1),
    dir
  );
  // within one group (a grid's lines share it): plain spatial move
  const inGroup = toward.filter((n) => groupOf(n) === group);
  if (!nearest || (inGroup.length > 0 && isCloser(from, inGroup, nearest, dir))) {
    return best(from, inGroup.length > 0 ? inGroup : toward, dir);
  }
  // the line of that group nearest this way (a grid has several; a row one)
  const target = groupOf(nearest);
  const members = nodes.filter(
    (n) => groupOf(n) === target && overlapsAcross(nearest.rect, n.rect, "left")
  );
  const remembered = memory.get(target);
  const back = remembered ? members.find((n) => n.id === remembered) : undefined;
  if (back) return back;
  // the member nearest the current node across the axis
  let pick = nearest;
  let d = Math.abs(cx(nearest.rect) - cx(from));
  for (const m of members) {
    const md = Math.abs(cx(m.rect) - cx(from));
    if (md < d) {
      pick = m;
      d = md;
    }
  }
  return pick;
}

// Whether a node of the current group lies closer along the move than the
// nearest node of another group (a grid of one group: move within it first).
function isCloser(from: Rect, inGroup: FocusNode[], other: FocusNode, dir: Direction): boolean {
  const along = (r: Rect) => (dir === "down" ? r.y - (from.y + from.h) : from.y - (r.y + r.h));
  const mine = Math.min(...inGroup.map((n) => along(n.rect)));
  return mine <= along(other.rect);
}

// The edge-to-edge distance from `from` to `to` along a vertical move.
function alongGap(from: Rect, to: Rect, dir: Direction): number {
  return dir === "down"
    ? Math.max(0, to.y - (from.y + from.h))
    : Math.max(0, from.y - (to.y + to.h));
}

// For a horizontal move: whether `to` shares the line `from` sits on.
function overlapsAcross(from: Rect, to: Rect, dir: Direction): boolean {
  if (dir === "left" || dir === "right") return to.y < from.y + from.h && to.y + to.h > from.y;
  return to.x < from.x + from.w && to.x + to.w > from.x;
}

/** The first node in reading order (top, then the line's start under `rtl`). */
export function first(nodes: readonly FocusNode[], rtl = false): FocusNode | null {
  let pick: FocusNode | null = null;
  for (const n of nodes) {
    if (!pick) {
      pick = n;
      continue;
    }
    const dy = n.rect.y - pick.rect.y;
    if (Math.abs(dy) > 1) {
      if (dy < 0) pick = n;
      continue;
    }
    const before = rtl ? n.rect.x + n.rect.w > pick.rect.x + pick.rect.w : n.rect.x < pick.rect.x;
    if (before) pick = n;
  }
  return pick;
}
