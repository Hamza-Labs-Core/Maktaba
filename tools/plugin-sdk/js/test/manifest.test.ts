// Story 54.01 T01, T02, T06 (ts): the shared corpus through the SDK's validator,
// byte-identical normalised JSON, the queue name vector, and en + ar words for every code.
import { strict as assert } from "node:assert";
import { readdirSync, readFileSync, existsSync } from "node:fs";
import { dirname, join } from "node:path";
import { test } from "node:test";
import { fileURLToPath } from "node:url";

import { CODES, ManifestInvalid, normalised, queueName, validate } from "../src/manifest.js";

const here = dirname(fileURLToPath(import.meta.url));
// dist/test → repo root
const root = join(here, "..", "..", "..", "..", "..");
const corpus = join(root, "shared", "plugins", "testdata", "manifests");
const files = (p: string) => {
  const f = join(corpus, "package", p);
  return existsSync(f) ? readFileSync(f) : undefined;
};

for (const name of readdirSync(join(corpus, "valid")).filter((f) => f.endsWith(".toml"))) {
  test(`valid/${name}`, () => {
    const m = validate(readFileSync(join(corpus, "valid", name)), files);
    const golden = readFileSync(join(corpus, "normalised", name.replace(/\.toml$/, ".json")), "utf8").trimEnd();
    assert.equal(normalised(m), golden);
  });
}

for (const name of readdirSync(join(corpus, "invalid")).filter((f) => f.endsWith(".toml"))) {
  test(`invalid/${name}`, () => {
    const src = readFileSync(join(corpus, "invalid", name), "utf8");
    const [code, detail = ""] = (src.split("\n")[0] ?? "").replace("# expect: ", "").trim().split(" ");
    assert.throws(
      () => validate(src, files),
      (e: unknown) => e instanceof ManifestInvalid && e.errors.some((x) => x.code === code && (!detail || x.detail === detail)),
    );
  });
}

test("queue name vectors", () => {
  const vectors = JSON.parse(readFileSync(join(root, "shared", "plugins", "testdata", "stage_names.json"), "utf8"));
  for (const v of vectors) assert.equal(queueName(v.id, v.stage), v.name);
});

test("1.0 is a float, not the integer 1", () => {
  const src = readFileSync(join(corpus, "valid", "minimal.toml"), "utf8").replace("version = 3", "version = 3.0");
  assert.throws(() => validate(src, files), (e: unknown) => e instanceof ManifestInvalid && e.has("manifest_bad_stage"));
});

test("T06: every error code has English and Arabic words", () => {
  const en = JSON.parse(readFileSync(join(root, "web", "src", "i18n", "en.json"), "utf8"));
  const ar = JSON.parse(readFileSync(join(root, "web", "src", "i18n", "ar.json"), "utf8"));
  const pick = (o: Record<string, unknown>, k: string) => k.split(".").reduce<unknown>((x, p) => (x as Record<string, unknown> | undefined)?.[p], o);
  for (const code of Object.values(CODES)) {
    for (const [lang, words] of [["en", en], ["ar", ar]] as const) {
      const w = pick(words, `plugins.error.${code}`) ?? (words as Record<string, unknown>)[`plugins.error.${code}`];
      assert.equal(typeof w, "string", `${lang}: plugins.error.${code}`);
    }
  }
});
