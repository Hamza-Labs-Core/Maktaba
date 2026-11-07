#!/usr/bin/env node
// Story 50.32: the cloud user app's build carries no operator console code. Run after
// `pnpm build:cloud`:
//
//   node tools/lint/no-admin-in-user-bundle.mjs [web/dist-cloud]
//
// It builds nothing. It fails, naming each file, if any file of the build contains an admin or
// staff API path (/v1/admin/, /v1/staff/) or an admin catalog key ("admin.…"), or if a source
// map lists a module of the console (web/admin/). Exit 0 clean, 1 on a finding, 2 on a bad call.
import { readdirSync, readFileSync, statSync } from "node:fs";
import path from "node:path";

const NEEDLES = ["/v1/admin/", "/v1/staff/", '"admin.'];

/** Every file under dir. */
function walk(dir) {
  const out = [];
  for (const name of readdirSync(dir)) {
    const p = path.join(dir, name);
    if (statSync(p).isDirectory()) out.push(...walk(p));
    else out.push(p);
  }
  return out;
}

/** The findings in a build directory: one line per file and reason. */
export function findings(dir) {
  const found = [];
  for (const file of walk(dir)) {
    const rel = path.relative(dir, file);
    const text = readFileSync(file, "utf8");
    for (const n of NEEDLES) {
      if (text.includes(n)) found.push(`${rel}: contains ${n}`);
    }
    if (file.endsWith(".map")) {
      let sources = [];
      try {
        sources = JSON.parse(text).sources ?? [];
      } catch {
        found.push(`${rel}: not a source map`);
        continue;
      }
      for (const s of sources) {
        if (/(^|\/)admin\/src\//.test(s)) found.push(`${rel}: bundles ${s}`);
      }
    }
  }
  return found;
}

if (import.meta.url === `file://${process.argv[1]}`) {
  const dir = process.argv[2] ?? "web/dist-cloud";
  let ok = true;
  try {
    ok = statSync(dir).isDirectory();
  } catch {
    ok = false;
  }
  if (!ok) {
    console.error(
      `no-admin-in-user-bundle: ${dir} is not a build directory (run pnpm build:cloud first)`,
    );
    process.exit(2);
  }
  const found = findings(dir);
  if (found.length > 0) {
    for (const f of found) console.error(`no-admin-in-user-bundle: ${f}`);
    console.error(
      "the user bundle carries operator console code (story 50.32)",
    );
    process.exit(1);
  }
  console.log(`no-admin-in-user-bundle: ${dir} is clean`);
}
