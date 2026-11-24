// The demo library's tests (story 60.09): T02 (every title licensed and credited, its art
// present and small), T03 (no person's name, LAN address, path or email in any answer), and
// routes/ in step with library.json. `node --test tests/fixtures/demo-library/demo-library.test.mjs`.
import { test } from "node:test";
import assert from "node:assert/strict";
import { existsSync, readFileSync, readdirSync, statSync } from "node:fs";
import { execFileSync } from "node:child_process";
import { join } from "node:path";
import { ROOT, LANGS, loadLibrary, buildRoutes } from "./routes.mjs";

const lib = loadLibrary();
// licences that allow showing a title and its art with a credit: CC BY, CC BY-SA, CC0, public domain
const OPEN = /^CC BY(-SA)?( \d\.\d)?$|^CC0|^Public domain$/;

test("T02: every title has a licence, a credit, its sources and its art", () => {
  for (const v of lib.videos) {
    assert.match(v.licence ?? "", OPEN, `${v.id}: an open licence`);
    assert.match(v.artwork_licence ?? "", OPEN, `${v.id}: its art's open licence`);
    assert.ok((v.artwork_from ?? "").length > 5, `${v.id}: what its art is`);
    assert.ok((v.credit ?? "").length > 5, `${v.id}: a credit`);
    assert.match(v.source ?? "", /^https:\/\//, `${v.id}: the film's page`);
    assert.match(
      v.artwork_url ?? "",
      /^https:\/\//,
      `${v.id}: where the art came from`,
    );
    for (const kind of ["poster", "backdrop"]) {
      const file = join(ROOT, "art", `${v.art}-${kind}.jpg`);
      assert.ok(existsSync(file), `${v.id}: art/${v.art}-${kind}.jpg`);
      assert.ok(statSync(file).size < 60_000, `${v.id}: ${kind} under 60 KB`);
    }
    for (const lang of LANGS) {
      assert.ok(
        v.title[lang] && v.synopsis[lang],
        `${v.id}: a title and a synopsis in ${lang}`,
      );
    }
    assert.match(
      v.title.ar + v.synopsis.ar,
      /[؀-ۿ]/,
      `${v.id}: the Arabic is Arabic`,
    );
  }
  const readme = readFileSync(join(ROOT, "README.md"), "utf8");
  for (const v of lib.videos) {
    assert.ok(readme.includes(v.source), `README credits ${v.title.en}`);
    assert.ok(readme.includes(v.artwork_url), `README names where ${v.title.en}'s art came from`);
  }
});

test("T02: the art carries no metadata", () => {
  for (const f of readdirSync(join(ROOT, "art"))) {
    const bytes = readFileSync(join(ROOT, "art", f));
    // EXIF (APP1 "Exif"), XMP (APP1 "http://ns.adobe.com/xap") or a comment segment (0xFFFE)
    assert.ok(!bytes.includes(Buffer.from("Exif\0")), `${f}: no EXIF`);
    assert.ok(!bytes.includes(Buffer.from("ns.adobe.com/xap")), `${f}: no XMP`);
    assert.ok(!bytes.includes(Buffer.from([0xff, 0xfe])), `${f}: no comment`);
  }
});

/** Every string in `value`, with the key it sits under. */
function* strings(value, key = "") {
  if (typeof value === "string") yield [key, value];
  else if (Array.isArray(value)) for (const v of value) yield* strings(v, key);
  else if (value && typeof value === "object")
    for (const [k, v] of Object.entries(value)) yield* strings(v, k);
}

// the names the fixture may show: the household, the home, the account
const NAMES = new Set([
  lib.user.username,
  ...LANGS.map((l) => lib.household[l]),
  ...LANGS.map((l) => lib.home[l]),
]);
const PERSON_KEYS = new Set(["username", "display_name", "home_name", "email"]);

test("T03: no person's name, LAN address, media path or email in any answer", () => {
  for (const lang of LANGS) {
    for (const r of buildRoutes(lang, lib)) {
      for (const [key, s] of strings(r.body ?? {})) {
        const where = `${lang} ${r.method} ${r.path} ${key}`;
        if (PERSON_KEYS.has(key) && s !== "")
          assert.ok(
            NAMES.has(s),
            `${where}: "${s}" is not the fixture's household`,
          );
        assert.doesNotMatch(
          s,
          /\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\b/,
          `${where}: an IP address`,
        );
        assert.doesNotMatch(s, /[\w.+-]+@[\w-]+\.[\w.]+/, `${where}: an email`);
        assert.doesNotMatch(
          s,
          /(^|[\s"'])(\/(Volumes|Users|home|mnt|srv|media|data|share|volume\d)\/|[A-Z]:\\)/,
          `${where}: a path`,
        );
        assert.doesNotMatch(s, /\.local\b|\bnas\b/i, `${where}: a host name`);
      }
    }
  }
});

// The demo library shows no woman or girl (story 60.09): its titles are documentaries with no
// people and animation with no female character, and every picture was checked by eye. This
// catches a title or synopsis that names or describes one.
const FEMALE = new Set([
  ..."woman women girl girls she her hers herself lady ladies mother daughter sister wife princess queen heroine".split(" "),
  ..."امرأة امراة نساء النساء فتاة فتيات الفتاة بنت بنات شابة سيدة أم الأم ابنة أخت زوجة أميرة ملكة راعية بطلة".split(" "),
]);

test("no title, synopsis or line names or describes a woman or girl", () => {
  const texts = [
    lib.household,
    ...lib.videos.flatMap((v) => [v.title, v.synopsis]),
    ...lib.series.map((s) => s.name),
    ...lib.channels.map((c) => c.name),
  ].flatMap((t) => Object.values(t));
  texts.push(...lib.search.passages.map((p) => p.text.replace(/<\/?mark>/g, "")));
  for (const text of texts)
    for (const word of text.toLowerCase().split(/[^\p{L}]+/u))
      assert.ok(!FEMALE.has(word), `"${text}": "${word}"`);
});

test("routes/ is in step with library.json", () => {
  execFileSync(process.execPath, [join(ROOT, "routes.mjs"), "--check"], {
    stdio: "pipe",
  });
});

test("every route a table names has its file", () => {
  for (const lang of LANGS) {
    const dir = join(ROOT, "routes", lang);
    for (const r of JSON.parse(
      readFileSync(join(dir, "routes.json"), "utf8"),
    )) {
      if (r.art)
        assert.ok(
          existsSync(join(ROOT, "art", r.art)),
          `${lang} ${r.path}: art/${r.art}`,
        );
      else
        assert.ok(
          existsSync(join(dir, r.file)),
          `${lang} ${r.path}: ${r.file}`,
        );
    }
  }
});
