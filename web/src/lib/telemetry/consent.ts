// Story 16.5: this device's telemetry decision and its pseudonym. Both live in
// localStorage, per device: consent is not synced to the account (each device
// has its own pseudonym, and its own say). The dated decision is the consent
// record (the EU case).

const CONSENT_KEY = "mkt:telemetry:consent";
const PSEUDONYM_KEY = "mkt:telemetry:pseudonym";
// pseudonyms this device sent under before an opt-out: "Forget my device"
// deletes them too
const PAST_KEY = "mkt:telemetry:past";

export interface Consent {
  granted: boolean;
  /** when the user decided (ISO 8601) */
  at: string;
}

function read(key: string): string | null {
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}

function write(key: string, value: string | null): void {
  try {
    if (value === null) localStorage.removeItem(key);
    else localStorage.setItem(key, value);
  } catch {
    /* storage refused: the decision lasts this session only */
  }
}

/** readConsent is the recorded decision, or null when the user has not decided. */
export function readConsent(): Consent | null {
  const raw = read(CONSENT_KEY);
  if (!raw) return null;
  try {
    const c = JSON.parse(raw) as Partial<Consent>;
    return typeof c.granted === "boolean" ? { granted: c.granted, at: String(c.at ?? "") } : null;
  } catch {
    return null;
  }
}

export function writeConsent(granted: boolean, now: Date = new Date()): Consent {
  const c = { granted, at: now.toISOString() };
  write(CONSENT_KEY, JSON.stringify(c));
  return c;
}

const BASE32 = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";

/** makePseudonym is 16 base32 characters from 80 random bits. */
export function makePseudonym(): string {
  const bytes = new Uint8Array(10);
  crypto.getRandomValues(bytes);
  let bits = 0;
  let value = 0;
  let out = "";
  for (const b of bytes) {
    value = (value << 8) | b;
    bits += 8;
    while (bits >= 5) {
      out += BASE32[(value >>> (bits - 5)) & 31];
      bits -= 5;
    }
  }
  return out;
}

/** pseudonym is this device's current pseudonym, or null (not opted in, or forgotten). */
export function pseudonym(): string | null {
  return read(PSEUDONYM_KEY);
}

/** ensurePseudonym returns the current pseudonym, making one at opt-in. */
export function ensurePseudonym(): string {
  let p = pseudonym();
  if (!p) {
    p = makePseudonym();
    write(PSEUDONYM_KEY, p);
  }
  return p;
}

/**
 * retirePseudonym ends the current pseudonym (at opt-out): the next opt-in
 * makes a fresh one, so the two periods are not linked. The old one is kept
 * aside for "Forget my device".
 */
export function retirePseudonym(): void {
  const p = pseudonym();
  if (!p) return;
  write(PAST_KEY, JSON.stringify([...pastPseudonyms(), p]));
  write(PSEUDONYM_KEY, null);
}

export function pastPseudonyms(): string[] {
  try {
    const list = JSON.parse(read(PAST_KEY) ?? "[]") as unknown;
    return Array.isArray(list) ? list.filter((x): x is string => typeof x === "string") : [];
  } catch {
    return [];
  }
}

/** allPseudonyms are every pseudonym this device has sent under. */
export function allPseudonyms(): string[] {
  const p = pseudonym();
  return p ? [...pastPseudonyms(), p] : pastPseudonyms();
}

/** dropPseudonyms forgets the given pseudonyms locally (after the server deleted them). */
export function dropPseudonyms(done: string[]): void {
  const gone = new Set(done);
  write(PAST_KEY, JSON.stringify(pastPseudonyms().filter((p) => !gone.has(p))));
  const p = pseudonym();
  if (p && gone.has(p)) write(PSEUDONYM_KEY, null);
}
