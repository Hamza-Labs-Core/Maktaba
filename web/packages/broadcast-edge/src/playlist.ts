// A rung's timeline and its HLS playlists (story 50.21 §3), as plain data and functions so the
// rules are tested without a Durable Object (ChannelLive keeps one RungState per rung).
//
// The timeline numbers its entries itself (`n`, the HLS media sequence), apart from the home's
// segment numbers (`seq`): the offline slate is spliced in between live segments, and a media
// sequence number must always name the same segment. So a live segment is published only in
// order. One that arrives early waits in `pending` until the gap fills; after one target
// duration the gap is skipped and the next segment carries a discontinuity. A segment for a slot
// already published or skipped is dropped (stale: 200, nothing written).

/** Segments kept per rung: about 2 minutes at 4 s. */
export const KEEP = 30;
/** The live window a media playlist lists. */
export const WINDOW = 6;
/** A rung is offline after 3 target durations without a segment (12 s at 4 s). */
export const OFFLINE_FACTOR = 3;
/** The target duration used before any segment arrived. */
export const DEFAULT_TARGET_S = 4;
/** After 24 h offline the channel is off air. */
export const OFF_AIR_AFTER_MS = 24 * 3600 * 1000;
/** Segment objects are deleted 10 minutes after they were written (no DVR, Q19). */
export const RETAIN_MS = 10 * 60 * 1000;

export interface Entry {
  /** media sequence number */
  n: number;
  kind: "live" | "slate";
  /** the home's segment number (live), or the slate segment's index */
  seq: number;
  hash8: string;
  /** the URI written in the playlist, relative to the rung's index.m3u8 */
  uri: string;
  /** duration in seconds */
  dur: number;
  bytes: number;
  disc: boolean;
  /** PROGRAM-DATE-TIME in ms: the home's clock (live), the edge's (slate) */
  pdt: number;
}

export interface Pending {
  seq: number;
  hash8: string;
  key: string;
  dur: number;
  bytes: number;
  disc: boolean;
  pdt: number;
}

export interface SlateSegment {
  idx: number;
  hash8: string;
  key: string;
  dur: number;
  bytes: number;
  at: number;
}

export interface RungState {
  entries: Entry[];
  nextN: number;
  /** the newest published live seq; null before the first */
  lastSeq: number | null;
  /** edge time of the newest live publish */
  lastAt: number;
  pending: Pending[];
  /** the slate is playing: the next live segment carries a discontinuity */
  inSlate: boolean;
  slateIdx: number;
  /** edge time at which the newest slate entry ends */
  slateEnd: number;
  /** discontinuities trimmed from `entries` (EXT-X-DISCONTINUITY-SEQUENCE) */
  discTrimmed: number;
  /** objects written for this rung and not yet deleted: key → edge time written */
  written: Record<string, number>;
}

export type Verdict = "new" | "duplicate" | "conflict" | "stale";

export function newRung(): RungState {
  return {
    entries: [],
    nextN: 0,
    lastSeq: null,
    lastAt: 0,
    pending: [],
    inSlate: false,
    slateIdx: 0,
    slateEnd: 0,
    discTrimmed: 0,
    written: {},
  };
}

export function segmentName(seq: number, hash8: string): string {
  return `${seq}-${hash8}.ts`;
}

export function segmentKey(
  env: string,
  ch: string,
  rung: string,
  seq: number,
  hash8: string
): string {
  return `${env}/${ch}/${rung}/${segmentName(seq, hash8)}`;
}

/** classify says what an incoming live segment is, without changing anything. */
export function classify(rs: RungState, seq: number, hash8: string): Verdict {
  const published = rs.entries.find((e) => e.kind === "live" && e.seq === seq);
  if (published) return published.hash8 === hash8 ? "duplicate" : "conflict";
  const waiting = rs.pending.find((p) => p.seq === seq);
  if (waiting) return waiting.hash8 === hash8 ? "duplicate" : "conflict";
  if (rs.lastSeq !== null && seq <= rs.lastSeq) return "stale";
  return "new";
}

/** targetDuration is the ceiling of the longest segment in the window (EXT-X-TARGETDURATION). */
export function targetDuration(rs: RungState): number {
  const win = rs.entries.slice(-WINDOW);
  if (win.length === 0) return DEFAULT_TARGET_S;
  return Math.max(1, Math.ceil(Math.max(...win.map((e) => e.dur)) - 0.001));
}

function push(rs: RungState, e: Omit<Entry, "n">): void {
  rs.entries.push({ ...e, n: rs.nextN++ });
  while (rs.entries.length > KEEP) {
    const gone = rs.entries.shift();
    if (gone?.disc) rs.discTrimmed++;
  }
}

function publish(rs: RungState, p: Pending, now: number): void {
  const gap = rs.lastSeq !== null && p.seq !== rs.lastSeq + 1;
  push(rs, {
    kind: "live",
    seq: p.seq,
    hash8: p.hash8,
    uri: segmentName(p.seq, p.hash8),
    dur: p.dur,
    bytes: p.bytes,
    disc: p.disc || gap || rs.inSlate,
    pdt: p.pdt,
  });
  rs.inSlate = false;
  rs.lastSeq = p.seq;
  rs.lastAt = now;
}

function drain(rs: RungState, now: number): void {
  rs.pending.sort((a, b) => a.seq - b.seq);
  while (rs.pending.length > 0 && rs.lastSeq !== null && rs.pending[0]!.seq === rs.lastSeq + 1) {
    publish(rs, rs.pending.shift()!, now);
  }
}

/**
 * append adds a live segment classify called "new": in order it is published (and any waiting
 * segments after it), early it waits. Returns the verdict it was given, re-checked.
 */
export function append(rs: RungState, p: Pending, now: number): Verdict {
  const v = classify(rs, p.seq, p.hash8);
  if (v !== "new") return v;
  rs.written[p.key] = now;
  if (rs.lastSeq === null || p.seq === rs.lastSeq + 1) {
    publish(rs, p, now);
    drain(rs, now);
  } else {
    rs.pending.push(p);
    rs.pending.sort((a, b) => a.seq - b.seq);
    // far ahead of a gap that never fills: skip it rather than hold more than a window
    while (rs.pending.length > KEEP) {
      publish(rs, rs.pending.shift()!, now);
      drain(rs, now);
    }
  }
  return "new";
}

export type RungEvent = "stalled" | "skipped";

/**
 * advance moves a rung's timeline to `now`: a gap waited for longer than one target duration is
 * skipped; a rung with no segment for 3 target durations continues with the offline slate
 * (after a discontinuity), one slate entry per slate duration of edge time.
 */
export function advance(rs: RungState, now: number, slate: SlateSegment[]): RungEvent[] {
  const events: RungEvent[] = [];
  const target = targetDuration(rs) * 1000;
  if (rs.pending.length > 0 && now - rs.lastAt > target) {
    publish(rs, rs.pending.shift()!, now);
    drain(rs, now);
    events.push("skipped");
  }
  if (rs.lastSeq === null || slate.length === 0) return events;
  const offlineAt = rs.lastAt + OFFLINE_FACTOR * target;
  if (now <= offlineAt) return events;
  if (!rs.inSlate) {
    rs.inSlate = true;
    rs.slateEnd = offlineAt;
    events.push("stalled");
  }
  const loop = slate.reduce((s, x) => s + x.dur, 0) * 1000;
  // a long silence with nobody asking: jump ahead, the timeline keeps only KEEP entries anyway
  if (loop > 0 && now - rs.slateEnd > KEEP * (loop / slate.length)) {
    rs.slateEnd = now - (KEEP - 1) * (loop / slate.length);
  }
  let first = rs.entries.length === 0 || rs.entries[rs.entries.length - 1]!.kind !== "slate";
  while (rs.slateEnd <= now) {
    const s = slate[rs.slateIdx % slate.length]!;
    push(rs, {
      kind: "slate",
      seq: s.idx,
      hash8: s.hash8,
      uri: `../slate/${segmentName(s.idx, s.hash8)}`,
      dur: s.dur,
      bytes: s.bytes,
      disc: first,
      pdt: rs.slateEnd,
    });
    first = false;
    rs.slateIdx++;
    rs.slateEnd += s.dur * 1000;
  }
  return events;
}

export type RungStatus = "waiting" | "live" | "offline_slate" | "off_air";

export function rungStatus(rs: RungState, now: number): RungStatus {
  if (rs.lastSeq === null) return "waiting";
  if (now - rs.lastAt > OFF_AIR_AFTER_MS) return "off_air";
  if (rs.inSlate || now - rs.lastAt > OFFLINE_FACTOR * targetDuration(rs) * 1000)
    return "offline_slate";
  return "live";
}

function fmtDur(d: number): string {
  return d.toFixed(3);
}

/** renderMedia writes the rung's HLS v6 media playlist, or null before its first segment. */
export function renderMedia(rs: RungState): string | null {
  const win = rs.entries.slice(-WINDOW);
  if (win.length === 0) return null;
  const before = rs.entries.slice(0, rs.entries.length - win.length);
  const discSeq = rs.discTrimmed + before.filter((e) => e.disc).length;
  const lines = [
    "#EXTM3U",
    "#EXT-X-VERSION:6",
    `#EXT-X-TARGETDURATION:${targetDuration(rs)}`,
    `#EXT-X-MEDIA-SEQUENCE:${win[0]!.n}`,
    `#EXT-X-DISCONTINUITY-SEQUENCE:${discSeq}`,
  ];
  for (const e of win) {
    if (e.disc) lines.push("#EXT-X-DISCONTINUITY");
    lines.push(`#EXT-X-PROGRAM-DATE-TIME:${new Date(e.pdt).toISOString()}`);
    lines.push(`#EXTINF:${fmtDur(e.dur)},`);
    lines.push(e.uri);
  }
  return lines.join("\n") + "\n";
}

/** A rung as the home's state post describes it (the master playlist's attributes). */
export interface RungInfo {
  name: string;
  bandwidth: number;
  resolution?: string;
  codecs?: string;
  frame_rate?: number;
}

/**
 * masterRungs is what a master playlist lists for a mode: `full` every rung, `lowest` the one
 * with the lowest bandwidth (50.20 §2). Rungs the plan no longer allows are left out.
 */
export function masterRungs(
  rungs: RungInfo[],
  mode: "full" | "lowest",
  allowed?: string[]
): RungInfo[] {
  const list = allowed ? rungs.filter((r) => allowed.includes(r.name)) : rungs;
  if (mode === "full" || list.length <= 1) return list;
  return [list.reduce((lo, r) => (r.bandwidth < lo.bandwidth ? r : lo))];
}

export function renderMaster(rungs: RungInfo[]): string {
  const lines = ["#EXTM3U", "#EXT-X-VERSION:6", "#EXT-X-INDEPENDENT-SEGMENTS"];
  for (const r of rungs) {
    const attrs = [`BANDWIDTH=${Math.round(r.bandwidth)}`];
    if (r.resolution) attrs.push(`RESOLUTION=${r.resolution}`);
    if (r.codecs) attrs.push(`CODECS="${r.codecs}"`);
    if (r.frame_rate) attrs.push(`FRAME-RATE=${r.frame_rate.toFixed(3)}`);
    lines.push(`#EXT-X-STREAM-INF:${attrs.join(",")}`);
    lines.push(`${r.name}/index.m3u8`);
  }
  return lines.join("\n") + "\n";
}

/** measuredBandwidth estimates a rung's peak bits per second from its window's segments. */
export function measuredBandwidth(rs: RungState): number {
  const live = rs.entries.filter((e) => e.kind === "live").slice(-WINDOW);
  if (live.length === 0) return 0;
  return Math.max(...live.map((e) => (e.bytes * 8) / Math.max(0.1, e.dur)));
}

/** The shortest slate segment the loop plays, in seconds. */
export const MIN_LOOP_SEG_S = 0.1;

/**
 * renderSlateLoop writes a media playlist that loops the channel's slate from `since` (a
 * programme entry's slate, 50.25): the newest WINDOW entries of the loop at `now`, a
 * discontinuity at each wrap. Null with no slate.
 */
export function renderSlateLoop(slate: SlateSegment[], since: number, now: number): string | null {
  // a segment under MIN_LOOP_SEG_S is no real slate segment; leaving it out keeps the loop's
  // length (and the arithmetic below) sane
  const segs = [...slate].sort((a, b) => a.idx - b.idx).filter((x) => x.dur >= MIN_LOOP_SEG_S);
  if (segs.length === 0) return null;
  const len = segs.length;
  const prefix: number[] = [];
  let loopMs = 0;
  for (const x of segs) {
    prefix.push(loopMs);
    loopMs += x.dur * 1000;
  }
  const startOf = (k: number) => since + Math.floor(k / len) * loopMs + prefix[k % len]!;
  const elapsed = Math.max(0, now - since);
  // the loop we are in, then the segment within it: a walk of at most len steps
  const q = Math.floor(elapsed / loopMs);
  const off = elapsed - q * loopMs;
  let j = 0;
  while (j + 1 < len && prefix[j + 1]! <= off) j++;
  const k = q * len + j;
  const first = Math.max(0, k - WINDOW + 1);
  const wrapsBefore = first > 0 ? Math.floor((first - 1) / len) : 0;
  const lines = [
    "#EXTM3U",
    "#EXT-X-VERSION:6",
    `#EXT-X-TARGETDURATION:${Math.ceil(Math.max(...segs.map((x) => x.dur)))}`,
    `#EXT-X-MEDIA-SEQUENCE:${first}`,
    `#EXT-X-DISCONTINUITY-SEQUENCE:${wrapsBefore}`,
  ];
  for (let i = first; i <= k; i++) {
    const x = segs[i % len]!;
    if (i > 0 && i % len === 0) lines.push("#EXT-X-DISCONTINUITY");
    lines.push(`#EXT-X-PROGRAM-DATE-TIME:${new Date(startOf(i)).toISOString()}`);
    lines.push(`#EXTINF:${fmtDur(x.dur)},`);
    lines.push(`../slate/${segmentName(x.idx, x.hash8)}`);
  }
  return lines.join("\n") + "\n";
}
