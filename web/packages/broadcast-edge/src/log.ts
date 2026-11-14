// The edge's log lines, in the 53.01 schema (shared/log/schema/line.schema.json):
// service = broadcast-edge, ids hashed. They go to Workers Logs, and Logpush forwards them to
// the witness (53.17's model). A channel id is already opaque (the cloud derives it), a
// publisher or home id is never written in the clear.
export type Level = "debug" | "info" | "warn" | "error";

export interface LogFields {
  component?: string;
  channel?: string;
  /** a publisher id; written hashed as user_h */
  pub?: string;
  /** a home (server) id; written hashed as home_h */
  home?: string;
  err?: unknown;
  attrs?: Record<string, unknown>;
}

/** hashId is the 16-hex-digit FNV-1a 64 of an id: stable, not reversible in practice for UUIDs. */
export function hashId(id: string): string {
  let h = 0xcbf29ce484222325n;
  for (const c of new TextEncoder().encode(id)) {
    h ^= BigInt(c);
    h = (h * 0x100000001b3n) & 0xffffffffffffffffn;
  }
  return h.toString(16).padStart(16, "0");
}

export function logLine(level: Level, msg: string, f: LogFields = {}): Record<string, unknown> {
  const line: Record<string, unknown> = {
    ts: new Date().toISOString(),
    level,
    service: "broadcast-edge",
    msg,
  };
  if (f.component) line.component = f.component;
  if (f.channel) line.channel_id = f.channel;
  if (f.pub) line.user_h = hashId(f.pub);
  if (f.home) line.home_h = hashId(f.home);
  if (f.err !== undefined) {
    const e = f.err instanceof Error ? f.err : new Error(String(f.err));
    line.err = { kind: e.name, msg: e.message };
  }
  if (f.attrs) line.attrs = f.attrs;
  return line;
}

export function log(level: Level, msg: string, f: LogFields = {}): void {
  const text = JSON.stringify(logLine(level, msg, f));
  if (level === "error") console.error(text);
  else if (level === "warn") console.warn(text);
  else console.log(text);
}
