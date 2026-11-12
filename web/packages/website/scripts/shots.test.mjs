// Story 60.09's tests: T01 (the committed shots: no metadata, under budget, both languages,
// the manifest and the files agree) and T06 (shots.mjs on a fixture run: the manifest and the
// files as listed; a red source keeps its previous images). `node --test scripts/shots.test.mjs`.
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  existsSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  readdirSync,
  rmSync,
  statSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import sharp from "sharp";
import { PACKAGE, loadConfig, makeShots } from "./shots.mjs";

const SHOTS = join(PACKAGE, "static", "shots");
const MANIFEST = join(PACKAGE, "shots-manifest.json");
const config = loadConfig();

/** Whether `bytes` (a WebP or an AVIF) carries EXIF or XMP. */
function hasMetadata(bytes) {
  return (
    bytes.includes(Buffer.from("EXIF")) ||
    bytes.includes(Buffer.from("Exif\0")) ||
    bytes.includes(Buffer.from("XMP ")) ||
    bytes.includes(Buffer.from("x:xmpmeta")) ||
    bytes.includes(Buffer.from("application/rdf+xml"))
  );
}

test("T01: every shot has no metadata and is under the budget", () => {
  const files = existsSync(SHOTS) ? readdirSync(SHOTS) : [];
  assert.ok(files.length > 0, "static/shots/ has images");
  for (const f of files) {
    assert.match(
      f,
      /^[a-z0-9-]+-(en|ar)-\d+\.(webp|avif)$/,
      `${f}: <app>-<screen>-<lang>-<width>.<webp|avif>`
    );
    const bytes = readFileSync(join(SHOTS, f));
    assert.ok(!hasMetadata(bytes), `${f}: no EXIF or XMP`);
    assert.ok(
      bytes.length < config.budgetBytes,
      `${f}: ${bytes.length} bytes, over ${config.budgetBytes}`
    );
  }
});

test("T01: the manifest and static/shots/ agree, with sizes, in both languages", async () => {
  const manifest = JSON.parse(readFileSync(MANIFEST, "utf8"));
  const listed = new Set();
  for (const [id, img] of Object.entries(manifest.images)) {
    assert.equal(id, `${img.app}-${img.screen}-${img.lang}`);
    const twin = `${img.app}-${img.screen}-${img.lang === "en" ? "ar" : "en"}`;
    assert.ok(manifest.images[twin], `${id} has its ${twin}`);
    assert.ok(img.width > 0 && img.height > 0, `${id}: width and height`);
    for (const s of img.sizes) {
      for (const kind of ["webp", "avif"]) {
        const file = join(SHOTS, s[kind].replace(/^\/shots\//, ""));
        listed.add(s[kind].replace(/^\/shots\//, ""));
        assert.ok(existsSync(file), `${id}: ${s[kind]}`);
      }
      const meta = await sharp(join(SHOTS, s.webp.replace(/^\/shots\//, ""))).metadata();
      assert.deepEqual(
        [meta.width, meta.height],
        [s.width, s.height],
        `${s.webp}: the manifest's size`
      );
    }
  }
  for (const f of readdirSync(SHOTS)) assert.ok(listed.has(f), `${f} is in shots-manifest.json`);
});

/** A small dark PNG standing in for a capture. */
const png = (w, h) =>
  sharp({ create: { width: w, height: h, channels: 3, background: { r: 12, g: 18, b: 32 } } })
    .png()
    .toBuffer();

test("T06: shots.mjs on a fixture run writes the manifest and the files, and keeps a red source's images", async () => {
  const root = mkdtempSync(join(tmpdir(), "shots-t06-"));
  try {
    const input = join(root, "run");
    const out = join(root, "shots");
    const manifestPath = join(root, "shots-manifest.json");
    const cfg = {
      budgetBytes: 150000,
      langs: ["en", "ar"],
      sources: {
        web: {
          artifact: "product-shots-web",
          job: "web",
          screens: ["home", "guide"],
          crop: { top: 10, right: 0, bottom: 10, left: 0 },
          widths: [200, 400],
        },
        tv: { artifact: "product-shots-tv", job: "tv", screens: ["home"], crop: {}, widths: [160] },
      },
    };
    mkdirSync(join(input, "product-shots-web"), { recursive: true });
    mkdirSync(join(input, "product-shots-tv", "nested"), { recursive: true });
    for (const s of ["home", "guide"])
      for (const l of ["en", "ar"])
        writeFileSync(join(input, "product-shots-web", `web-${s}-${l}.png`), await png(800, 520));
    for (const l of ["en", "ar"])
      writeFileSync(
        join(input, "product-shots-tv", "nested", `tv-home-${l}.png`),
        await png(640, 360)
      );

    const green = {
      jobs: [
        { name: "web / product shots", conclusion: "success" },
        { name: "tv-ui-tests", conclusion: "success" },
      ],
    };
    const first = await makeShots({
      input,
      repo: root,
      out,
      manifestPath,
      config: cfg,
      run: green,
      label: "run 1",
    });
    assert.deepEqual(first.taken, ["web", "tv"]);
    assert.deepEqual(first.kept, []);
    assert.deepEqual(
      readdirSync(out).sort(),
      [
        "tv-home-ar-160.avif",
        "tv-home-ar-160.webp",
        "tv-home-en-160.avif",
        "tv-home-en-160.webp",
        ...["guide", "home"].flatMap((s) =>
          ["ar", "en"].flatMap((l) =>
            [200, 400].flatMap((w) => [`web-${s}-${l}-${w}.avif`, `web-${s}-${l}-${w}.webp`])
          )
        ),
      ].sort()
    );
    const m = JSON.parse(readFileSync(manifestPath, "utf8"));
    assert.deepEqual(Object.keys(m.images), [
      "tv-home-ar",
      "tv-home-en",
      "web-guide-ar",
      "web-guide-en",
      "web-home-ar",
      "web-home-en",
    ]);
    // cropped 10 px top and bottom: 800×500, so 400×250 at the largest size
    assert.deepEqual([m.images["web-home-en"].width, m.images["web-home-en"].height], [400, 250]);
    assert.deepEqual(
      m.images["web-home-en"].sizes.map((s) => [s.width, s.height]),
      [
        [200, 125],
        [400, 250],
      ]
    );
    for (const f of readdirSync(out)) assert.ok(statSync(join(out, f)).size < cfg.budgetBytes);

    // the next run: tv's job is red, web's is green with new captures
    const before = readFileSync(join(out, "tv-home-en-160.webp"));
    for (const l of ["en", "ar"])
      writeFileSync(
        join(input, "product-shots-tv", "nested", `tv-home-${l}.png`),
        await png(320, 180)
      );
    const red = {
      jobs: [
        { name: "web / product shots", conclusion: "success" },
        { name: "tv-ui-tests", conclusion: "failure" },
      ],
    };
    const second = await makeShots({
      input,
      repo: root,
      out,
      manifestPath,
      config: cfg,
      run: red,
      label: "run 2",
    });
    assert.deepEqual(second.taken, ["web"]);
    assert.deepEqual(second.kept, [{ app: "tv", why: 'its job "tv-ui-tests" concluded failure' }]);
    assert.deepEqual(
      readFileSync(join(out, "tv-home-en-160.webp")),
      before,
      "the red source's image is unchanged"
    );
    const m2 = JSON.parse(readFileSync(manifestPath, "utf8"));
    assert.equal(m2.images["tv-home-en"].kept, true);
    assert.equal(m2.images["web-home-en"].kept, undefined);

    // a source with a capture missing keeps its images too, and says which
    rmSync(join(input, "product-shots-web", "web-guide-ar.png"));
    const third = await makeShots({
      input,
      repo: root,
      out,
      manifestPath,
      config: cfg,
      run: green,
    });
    assert.deepEqual(
      third.kept.map((k) => k.app),
      ["web"]
    );
    assert.match(third.kept[0].why, /missing web-guide-ar/);

    // a run for one app alone (--only tv) leaves every other app's entries in the manifest
    const fourth = await makeShots({
      input,
      repo: root,
      out,
      manifestPath,
      config: { ...cfg, sources: { tv: cfg.sources.tv } },
      run: green,
    });
    assert.deepEqual(fourth.taken, ["tv"]);
    const m4 = JSON.parse(readFileSync(manifestPath, "utf8"));
    assert.deepEqual(Object.keys(m4.images), Object.keys(m.images));
    assert.equal(m4.images["web-home-en"].app, "web");
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});
