// Story 60.04: the visual references (e2e/__screenshots__/) are PNGs under 1 MB with no metadata
// chunk (no text, time or Exif), named <page>-<width>-<lang>[-light].png, one per page, width and
// language the visual spec takes; and the image they are rendered in is the one the CI job runs.
// Run: node --test web/packages/website/scripts/references.test.mjs
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

const PKG = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const REPO = path.resolve(PKG, "../../..");
const DIR = path.join(PKG, "e2e/__screenshots__");
const SIG = Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]);
const METADATA = new Set(["tEXt", "iTXt", "zTXt", "tIME", "eXIf"]);

export function chunks(buf) {
  if (!buf.subarray(0, 8).equals(SIG)) throw new Error("not a PNG");
  const out = [];
  for (let at = 8; at < buf.length; ) {
    const len = buf.readUInt32BE(at);
    out.push(buf.toString("latin1", at + 4, at + 8));
    at += 12 + len;
  }
  return out;
}

const refs = fs.existsSync(DIR) ? fs.readdirSync(DIR).filter((f) => !f.startsWith(".")) : [];

test("each reference is a PNG under 1 MB with no metadata, named as the spec names it", () => {
  for (const f of refs) {
    assert.match(f, /^[a-z0-9-]+-(360|768|1280)-(en|ar)(-light)?\.png$/, f);
    if (f.includes("-light"))
      assert.match(f, /-1280-(en|ar)-light\.png$/, `${f}: light is at 1280 only`);
    const buf = fs.readFileSync(path.join(DIR, f));
    assert.ok(buf.length < 1024 * 1024, `${f}: ${buf.length} bytes, over 1 MB`);
    const meta = chunks(buf).filter((c) => METADATA.has(c));
    assert.deepEqual(meta, [], `${f}: metadata chunks`);
  }
});

test("every reference has its twin in the other language", () => {
  const set = new Set(refs);
  for (const f of refs) {
    const twin = f.replace(
      /-(en|ar)(-light)?\.png$/,
      (_, l, light) => `-${l === "en" ? "ar" : "en"}${light ?? ""}.png`
    );
    assert.ok(set.has(twin), `${f}: no ${twin}`);
  }
});

test("the references render in the image the CI job runs, at the pinned Playwright", () => {
  const version = JSON.parse(fs.readFileSync(path.join(PKG, "package.json"), "utf8"))
    .devDependencies["@playwright/test"];
  assert.match(version, /^\d+\.\d+\.\d+$/, "@playwright/test is pinned");
  const image = `mcr.microsoft.com/playwright:v${version}-noble`;
  const workflow = fs.readFileSync(path.join(REPO, ".github/workflows/_website.yml"), "utf8");
  assert.ok(workflow.includes(`image: ${image}`), `_website.yml's visual job runs in ${image}`);
  const script = fs.readFileSync(path.join(PKG, "scripts/e2e-update.sh"), "utf8");
  assert.match(script, /mcr\.microsoft\.com\/playwright:v\$\{version\}-noble/);
  assert.match(script, /--platform linux\/amd64/);
});
