// Story 16.5: this app install's decision about sharing anonymous usage data
// with Maktaba, and its install id. Both live in localStorage, per install:
// consent is not synced to the account (each install has its own id, and its
// user's own say), and the home's admin cannot give it. The dated decision is
// the consent record (the EU case).

const CONSENT_KEY = "mkt:usage:consent";
const PSEUDONYM_KEY = "mkt:usage:install-id";
// ids this install sent under before an opt-out: "Delete what this app sent"
// deletes them too
const PAST_KEY = "mkt:usage:past";
// before the rework the data went to the home's admin: a yes to that is not a yes
// to Maktaba, so it is forgotten and the user is asked again; a no stays a no
const LEGACY_CONSENT_KEY = "mkt:telemetry:consent";
const LEGACY_KEYS = [
  LEGACY_CONSENT_KEY,
  "mkt:telemetry:pseudonym",
  "mkt:telemetry:past",
  "mkt:telemetry:queue",
];

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

/**
 * forgetLegacyConsent drops the decision and ids from before the rework. A
 * refusal carries over, dated as it was: the user who said no is not asked again.
 */
export function forgetLegacyConsent(): void {
  const legacy = parseConsent(read(LEGACY_CONSENT_KEY));
  if (legacy?.granted === false && readConsent() === null) {
    write(CONSENT_KEY, JSON.stringify(legacy));
  }
  for (const k of LEGACY_KEYS) write(k, null);
}

function parseConsent(raw: string | null): Consent | null {
  if (!raw) return null;
  try {
    const c = JSON.parse(raw) as Partial<Consent>;
    return typeof c.granted === "boolean" ? { granted: c.granted, at: String(c.at ?? "") } : null;
  } catch {
    return null;
  }
}

/** readConsent is the recorded decision, or null when the user has not decided. */
export function readConsent(): Consent | null {
  return parseConsent(read(CONSENT_KEY));
}

export function writeConsent(granted: boolean, now: Date = new Date()): Consent {
  const c = { granted, at: now.toISOString() };
  write(CONSENT_KEY, JSON.stringify(c));
  return c;
}

const BASE32 = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";

/** makePseudonym is an install id: 24 base32 characters from 120 random bits. */
export function makePseudonym(): string {
  const bytes = new Uint8Array(15);
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

/** pseudonym is this install's current id, or null (not opted in, or forgotten). */
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
 * retirePseudonym ends the current id (at opt-out): the next opt-in makes a
 * fresh one, so the two periods are not linked. The old one is kept aside for
 * "Delete what this app sent".
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

/** allPseudonyms are every id this install has sent under. */
export function allPseudonyms(): string[] {
  const p = pseudonym();
  return p ? [...pastPseudonyms(), p] : pastPseudonyms();
}

/** dropPseudonyms forgets the given ids locally (after the home took the deletes). */
export function dropPseudonyms(done: string[]): void {
  const gone = new Set(done);
  write(PAST_KEY, JSON.stringify(pastPseudonyms().filter((p) => !gone.has(p))));
  const p = pseudonym();
  if (p && gone.has(p)) write(PSEUDONYM_KEY, null);
}
