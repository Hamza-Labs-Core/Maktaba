// The /tv page's one script (story 58.03). A TV with no account anywhere shows an 8-character
// code and a QR of https://maktaba.tv/tv?c=ABCD2345; the code is approved in Maktaba Cloud, at
// https://app.maktaba.tv/tv. When the address carries a code (8 characters of the code alphabet,
// any case, with or without the dash), this script forwards there before the first paint with
// that code and nothing else of the address. Without a valid code, or without JavaScript, the
// page stays and links Maktaba Cloud's page, where the person types the code the TV shows.
//
// The build publishes this file with its types and its `export`s stripped, under a
// content-hashed name, as a classic script (scripts/build.mjs, classicScriptFile). It imports
// nothing, so it runs as it is in the browser and in the tests (tv.test.ts).

/** Maktaba Cloud's page that approves a TV's code. */
export const CLOUD_TV = "https://app.maktaba.tv/tv";

/** The cloud code alphabet: story 15.06's pairing alphabet without 0 and 1. */
export const TV_CODE_ALPHABET = "23456789ABCDEFGHJKMNPQRSTVWXYZ";

/** The code in an address's query (?c=), as Maktaba Cloud takes it (ABCD2345), or null. */
export function tvCode(search: string): string | null {
  const raw = new URLSearchParams(search).get("c");
  if (raw === null) return null;
  const code = raw.replace(/[\s-]/g, "").toUpperCase();
  if (code.length !== 8) return null;
  for (const ch of code) if (!TV_CODE_ALPHABET.includes(ch)) return null;
  return code;
}

/** Where the page forwards: Maktaba Cloud's page with the code, or null (no valid code: stay). */
export function forwardTo(search: string): string | null {
  const code = tvCode(search);
  return code ? `${CLOUD_TV}?c=${code}` : null;
}

if (typeof location !== "undefined") {
  const to = forwardTo(location.search);
  if (to) location.replace(to);
}
