// The stages Maktaba's own first-party plugins declare (plugins/first-party/<p>/manifest.toml,
// one [[stage]] table each, Epic 54 Phase 4). Tests that read the graph from engine/graph.py
// read these too: a stage a wave moves out of graph.py (_fp("<name>") there) is declared
// here, with the same fields.
const FS = "node:fs";
const { readFileSync, readdirSync, existsSync } = (await import(/* @vite-ignore */ FS)) as {
  readFileSync: (path: string, encoding: "utf8") => string;
  readdirSync: (path: string) => string[];
  existsSync: (path: string) => boolean;
};

/** A stage's string and boolean fields as its manifest gives them (name, label, entry,
 * model_rules, unreleased …). */
export type ManifestStage = Record<string, string | boolean>;

const FIRST_PARTY = "../plugins/first-party"; // vitest runs in web/

/** name -> fields of each [[stage]] of every first-party plugin's manifest. */
export function firstPartyStages(root = FIRST_PARTY): Map<string, ManifestStage> {
  const out = new Map<string, ManifestStage>();
  if (!existsSync(root)) return out;
  for (const plugin of readdirSync(root).sort()) {
    const path = `${root}/${plugin}/manifest.toml`;
    if (!existsSync(path)) continue;
    for (const table of readFileSync(path, "utf8")
      .split(/^\[\[stage\]\]\s*$/m)
      .slice(1)) {
      const body = table.split(/^\[/m)[0]!;
      const fields: ManifestStage = {};
      for (const m of body.matchAll(/^([a-z_]+)\s*=\s*(?:"([^"]*)"|(true|false))\s*$/gm)) {
        fields[m[1]!] = m[2] !== undefined ? m[2] : m[3] === "true";
      }
      if (typeof fields.name === "string") out.set(fields.name, fields);
    }
  }
  return out;
}

/** The names graph.py takes from a manifest (_fp("<name>")). */
export function graphFirstPartyNames(graphSource: string): string[] {
  return [...graphSource.matchAll(/\b_fp\(\s*"([a-z0-9_]+)"\s*\)/g)].map((m) => m[1]!);
}
