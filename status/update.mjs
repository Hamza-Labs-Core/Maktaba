#!/usr/bin/env node
// Folds probe rounds into the status data (Epic 50 story 50.11):
//
//   node status/update.mjs --data <status-data checkout> --rounds <file of JSON lines>
//
// Each line is one round: {"at":"…Z","relay":{"actions":1,"witness":1,"held_ratio":0.01},
// "api":{"actions":1,"witness":null},"push":{"lag":12}} (probe.sh writes them). A round
// already folded in (at or before state.json's updated_at) is skipped, so a rerun is harmless.
import fs from "node:fs";
import path from "node:path";
import { parseArgs } from "node:util";
import { fileURLToPath } from "node:url";
import { update } from "./lib.mjs";

const here = path.dirname(fileURLToPath(import.meta.url));
const { values } = parseArgs({ options: { data: { type: "string" }, rounds: { type: "string" } } });
if (!values.data || !values.rounds) {
  console.error("usage: update.mjs --data <dir> --rounds <file>");
  process.exit(2);
}
const config = JSON.parse(fs.readFileSync(path.join(here, "config.json"), "utf8"));
const rounds = fs.readFileSync(values.rounds, "utf8").split("\n").filter((l) => l.trim()).map((l, i) => {
  try {
    return JSON.parse(l);
  } catch {
    console.error(`status: round ${i + 1} is not JSON: ${l}`);
    process.exit(1);
  }
});
try {
  const s = update(values.data, rounds, config.probed);
  const line = Object.entries(s.components).map(([c, r]) => `${c} ${r.state ?? "unknown"}${r.pending ? ` (→ ${r.pending.state}?)` : ""}`).join(", ");
  console.log(`status: ${rounds.length} rounds; ${line}`);
} catch (e) {
  console.error(`status: ${e.message}`);
  process.exit(1);
}
