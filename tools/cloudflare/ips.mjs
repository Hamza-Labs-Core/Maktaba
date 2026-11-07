// Cloudflare's published IP ranges (Epic 50, story 50.31's fallback, 50.34's weekly job): the
// list Caddy's `aop` blocks allow. Fetched without a token from Cloudflare's public pages,
// checked (every line a CIDR of its family, at least one), and written one per line.
//
//   node ips.mjs [--dir deploy/cloudflare]    prints "changed" or "unchanged"
import { readFileSync, writeFileSync } from "node:fs";
import { isIPv4, isIPv6 } from "node:net";
import { join } from "node:path";

export const SOURCES = { v4: "https://www.cloudflare.com/ips-v4", v6: "https://www.cloudflare.com/ips-v6" };

/** The ranges of one family from a page's text; throws on anything that is not one. */
export function parse(text, family) {
  const lines = text.split(/\r?\n/).map((l) => l.trim()).filter(Boolean);
  if (!lines.length) throw new Error(`no ${family} range in the answer`);
  for (const l of lines) {
    const [ip, bits, extra] = l.split("/");
    const max = family === "v4" ? 32 : 128;
    const okIp = family === "v4" ? isIPv4(ip) : isIPv6(ip);
    if (extra !== undefined || !okIp || !/^\d+$/.test(bits ?? "") || Number(bits) > max) throw new Error(`not a ${family} range: ${l}`);
  }
  return lines;
}

export async function update(dir, fetchImpl = fetch, sources = SOURCES) {
  let changed = false;
  for (const fam of ["v4", "v6"]) {
    const res = await fetchImpl(sources[fam]);
    if (!res.ok) throw new Error(`${sources[fam]} answered ${res.status}`);
    const text = parse(await res.text(), fam).join("\n") + "\n";
    const file = join(dir, `ips-${fam}.txt`);
    let before = "";
    try {
      before = readFileSync(file, "utf8");
    } catch {
      // first run
    }
    if (before !== text) {
      writeFileSync(file, text);
      changed = true;
    }
  }
  return changed;
}

if (import.meta.url === `file://${process.argv[1]}`) {
  const i = process.argv.indexOf("--dir");
  const dir = i >= 0 ? process.argv[i + 1] : "deploy/cloudflare";
  console.log((await update(dir)) ? "changed" : "unchanged");
}
