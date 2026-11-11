// The Latin-share check of maktaba.dev (web/packages/devportal/scripts/parity.mjs), shared with
// maktaba.tv's install pages (Epic 59 story 59.14): an Arabic page fails when more than
// MAX_LATIN_SHARE of its prose letters (outside code, URLs and paths) are Latin. Product and
// NAS names (Synology, TrueNAS, Container Station) stay Latin, so it is not zero.

// @ts-ignore: parity.mjs is plain JavaScript; its three exports are typed here
import * as parity from "../../devportal/scripts/parity.mjs";

export const MAX_LATIN_SHARE: number = parity.MAX_LATIN_SHARE;
export const latinShare: (text: string) => number = parity.latinShare;
const baseProse: (html: string) => string = parity.htmlProse;

/** A page's prose, as maktaba.dev reads it, less two kinds of names that count as code does:
 * the model table (`table.models`: model and processor names, data), and the NAS's own menu
 * names, which a guide gives in English in «quotes» after an Arabic description where the NAS
 * has no Arabic UI (story 59.14): a «…» span with no Arabic letter in it. */
export function htmlProse(html: string): string {
  const noTable = html.replace(/<table class="models"[\s\S]*?<\/table>/g, " ");
  return baseProse(noTable).replace(/«[^»؀-ۿ]*»/g, " ");
}

/** The pages whose text a script puts in (the install page and the Synology guide): their
 * Arabic is checked where it is made, by their own tests (strings.test.ts and
 * synologyStrings.test.ts), not in the page's shell. */
export const SCRIPTED = ["docker", "synology"];

/** Each Arabic page (an `ar/` path) whose prose is more than MAX_LATIN_SHARE Latin. */
export function latinFindings(pages: Record<string, string>): string[] {
  const out: string[] = [];
  for (const [name, html] of Object.entries(pages)) {
    if (!/(^|\/)ar\//.test(name)) continue;
    if (SCRIPTED.some((p) => name.includes(`/install/${p}/`))) continue;
    const share = latinShare(htmlProse(html));
    if (share > MAX_LATIN_SHARE)
      out.push(
        `${name}: ${(share * 100).toFixed(0)}% of its prose is Latin (limit ${MAX_LATIN_SHARE * 100}%)`
      );
  }
  return out;
}
