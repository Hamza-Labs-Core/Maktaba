// The ranges' check and update against a local fake of Cloudflare's pages.
import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { parse, update } from "./ips.mjs";
import { fakeServer } from "./fake.mjs";

test("parse accepts each family's CIDRs and refuses anything else", () => {
  assert.deepEqual(parse("173.245.48.0/20\n103.21.244.0/22", "v4"), ["173.245.48.0/20", "103.21.244.0/22"]);
  assert.deepEqual(parse("2400:cb00::/32\n", "v6"), ["2400:cb00::/32"]);
  for (const bad of ["", "<html>", "1.2.3.4", "1.2.3.4/33", "2400:cb00::/32"]) assert.throws(() => parse(bad, "v4"));
});

test("update writes the lists, says changed, then unchanged; a bad answer writes nothing", async () => {
  const f = await fakeServer({
    "GET /ips-v4": () => [200, "173.245.48.0/20\n103.21.244.0/22"],
    "GET /ips-v6": () => [200, "2400:cb00::/32"],
    "GET /bad": () => [200, "<html>maintenance</html>"],
  });
  const dir = mkdtempSync(join(tmpdir(), "ips-"));
  const sources = { v4: `${f.url}/ips-v4`, v6: `${f.url}/ips-v6` };
  try {
    assert.equal(await update(dir, fetch, sources), true);
    assert.equal(readFileSync(join(dir, "ips-v4.txt"), "utf8"), "173.245.48.0/20\n103.21.244.0/22\n");
    assert.equal(await update(dir, fetch, sources), false);
    writeFileSync(join(dir, "ips-v6.txt"), "keep\n");
    await assert.rejects(update(dir, fetch, { v4: `${f.url}/bad`, v6: `${f.url}/ips-v6` }));
    assert.equal(readFileSync(join(dir, "ips-v6.txt"), "utf8"), "keep\n");
  } finally {
    await f.close();
  }
});
