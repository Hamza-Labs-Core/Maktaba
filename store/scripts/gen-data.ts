// The shared files the store's checks read, as JSON the Worker imports (story 54.16):
// shared/plugins/capabilities.toml (name, risk, interface) and the rating table's cases
// (shared/plugins/testdata/ratings.json is read by the tests directly).
//
//   pnpm data          write checks/capabilities.json
//   pnpm data:check    fail when it differs (CI)
import { readFileSync, writeFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { parse } from "smol-toml";

const HERE = dirname(fileURLToPath(import.meta.url));
const toml = parse(readFileSync(resolve(HERE, "../../shared/plugins/capabilities.toml"), "utf8")) as {
  capability: { name: string; risk: string; interface: string; param?: string }[];
};
// Maktaba's own words for each capability, English and Arabic (web/src/i18n), for the
// listing pages (54.18 §2: capabilities in Maktaba's words)
const en = JSON.parse(readFileSync(resolve(HERE, "../../web/src/i18n/en.json"), "utf8")) as Record<string, string>;
const ar = JSON.parse(readFileSync(resolve(HERE, "../../web/src/i18n/ar.json"), "utf8")) as Record<string, string>;
const words = (name: string) => {
  const k = `plugins.capability.${name.replace(/[.-]/g, "_")}.title`;
  return { en: en[k] ?? name, ar: ar[k] ?? name };
};
const out =
  JSON.stringify(
    {
      _comment: "Made by store/scripts/gen-data.ts from shared/plugins/capabilities.toml and web/src/i18n. Do not edit.",
      capabilities: toml.capability.map((c) => ({ name: c.name, risk: c.risk, interface: c.interface, param: c.param ?? "", words: words(c.name) })),
    },
    null,
    2,
  ) + "\n";
const path = resolve(HERE, "../checks/capabilities.json");
if (process.argv.includes("--check")) {
  if (readFileSync(path, "utf8") !== out) {
    console.error(`${path} is stale: run pnpm data in store/`);
    process.exit(1);
  }
} else writeFileSync(path, out);
