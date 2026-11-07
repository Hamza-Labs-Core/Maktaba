// TOTP (RFC 6238): 30-second steps, 6 digits, HMAC-SHA1, ±1 step, and a code used once is
// refused a second time (the factor's last_step).
const B32 = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";

export function base32(b: Uint8Array): string {
  let bits = 0;
  let value = 0;
  let out = "";
  for (const x of b) {
    value = ((value << 8) | x) & 0xffff;
    bits += 8;
    while (bits >= 5) {
      out += B32[(value >>> (bits - 5)) & 31];
      bits -= 5;
    }
  }
  if (bits > 0) out += B32[(value << (5 - bits)) & 31];
  return out;
}

export async function code(secret: Uint8Array, step: number): Promise<string> {
  const msg = new Uint8Array(8);
  new DataView(msg.buffer).setBigUint64(0, BigInt(step));
  const key = await crypto.subtle.importKey("raw", secret as BufferSource, { name: "HMAC", hash: "SHA-1" }, false, ["sign"]);
  const mac = new Uint8Array(await crypto.subtle.sign("HMAC", key, msg as BufferSource));
  const o = mac[19]! & 15;
  const n = (((mac[o]! & 0x7f) << 24) | (mac[o + 1]! << 16) | (mac[o + 2]! << 8) | mac[o + 3]!) % 1_000_000;
  return String(n).padStart(6, "0");
}

export const stepAt = (t: Date) => Math.floor(t.getTime() / 30_000);

/** The step a code matches within ±1 of now and after lastStep (a reuse is refused); null if none. */
export async function verifyCode(secret: Uint8Array, given: string, at: Date, lastStep: number): Promise<number | null> {
  if (!/^\d{6}$/.test(given)) return null;
  const s = stepAt(at);
  for (const step of [s - 1, s, s + 1]) {
    if (step <= lastStep) continue;
    if ((await code(secret, step)) === given) return step;
  }
  return null;
}

export function otpauthURI(secret: Uint8Array, account: string): string {
  const label = encodeURIComponent(`Maktaba Plugins:${account}`);
  return `otpauth://totp/${label}?secret=${base32(secret)}&issuer=${encodeURIComponent("Maktaba Plugins")}&algorithm=SHA1&digits=6&period=30`;
}
