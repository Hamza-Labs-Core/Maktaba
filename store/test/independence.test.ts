// Story 54.15 T14: the store is independent of Maktaba Cloud. Nothing under store/
// imports from cloud/ or calls the cloud's API host (README §6.0).
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join, relative, resolve } from "node:path";
import { describe, expect, it } from "vitest";

const STORE = resolve(__dirname, "..");
const SKIP = new Set(["node_modules", ".wrangler", "dist", "test"]);

function* files(dir: string): Generator<string> {
  for (const name of readdirSync(dir)) {
    if (SKIP.has(name)) continue;
    const p = join(dir, name);
    if (statSync(p).isDirectory()) yield* files(p);
    else if (/\.(ts|tsx|js|mjs|json|toml|sql|html)$/.test(name)) yield p;
  }
}

describe("the store stands alone", () => {
  it("imports nothing from cloud/ and never calls api.maktaba.tv", () => {
    const bad: string[] = [];
    for (const f of files(STORE)) {
      const text = readFileSync(f, "utf8");
      if (/from\s+["'][^"']*\/cloud\//.test(text) || /require\(["'][^"']*\/cloud\//.test(text))
        bad.push(`${relative(STORE, f)}: imports cloud/`);
      if (/api\.maktaba\.tv/.test(text)) bad.push(`${relative(STORE, f)}: names api.maktaba.tv`);
    }
    expect(bad).toEqual([]);
  });
});
