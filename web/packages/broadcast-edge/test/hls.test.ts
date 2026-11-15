// The watch pages' hls.js (story 50.27's open item): an exact version with a Subresource
// Integrity hash. The bytes are the npm package's own file (jsDelivr serves a package's files
// unchanged), read by vitest.config.ts from node_modules, so a new version or a wrong hash fails
// here before a browser refuses the script.
import { describe, expect, it } from "vitest";
import { csp, HLS_INTEGRITY, HLS_SRC, HLS_VERSION } from "../src/site/pages";
import { E } from "./helpers";

const T = E as unknown as { TEST_HLS_JS: string; TEST_HLS_VERSION: string };

async function sri384(text: string): Promise<string> {
  const d = new Uint8Array(await crypto.subtle.digest("SHA-384", new TextEncoder().encode(text)));
  let bin = "";
  for (const b of d) bin += String.fromCharCode(b);
  return `sha384-${btoa(bin)}`;
}

describe("hls.js integrity", () => {
  it("the page's hash is the pinned file's", async () => {
    expect(T.TEST_HLS_JS.length).toBeGreaterThan(100_000);
    expect(await sri384(T.TEST_HLS_JS)).toBe(HLS_INTEGRITY);
  });

  it("the version is exact and the package's", () => {
    expect(HLS_VERSION).toMatch(/^\d+\.\d+\.\d+$/);
    expect(T.TEST_HLS_VERSION).toBe(HLS_VERSION);
    expect(HLS_SRC).toBe(`https://cdn.jsdelivr.net/npm/hls.js@${HLS_VERSION}/dist/hls.min.js`);
  });

  it("the CSP allows that one file from jsDelivr, nothing else there", () => {
    const scriptSrc = csp("https://live.maktaba.tv")
      .split("; ")
      .find((d) => d.startsWith("script-src "))!;
    expect(scriptSrc.split(" ")).toEqual([
      "script-src",
      "'self'",
      HLS_SRC,
      "https://challenges.cloudflare.com",
    ]);
  });
});
