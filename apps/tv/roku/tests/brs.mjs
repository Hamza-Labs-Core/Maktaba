// Runs BrightScript tests on brs-engine (its Node CLI, brs-node) and keeps what they print
// (story 58.15; 58.16: every TV test keeps the app's log in the CI report).
//
// A test is a .brs file with `sub Main()` that prints `FAIL <what>` for each failure and ends
// with `RESULT pass=<n> fail=<n>`. It runs with the channel's source files, `pkg:/` being
// apps/tv/roku (paths are passed relative to it: brs-cli refuses absolute ones), and its stdout
// goes to build/logs/<name>.log, which the Roku job uploads whether it passed or failed.

import { execFileSync } from "node:child_process";
import { mkdirSync, readdirSync, writeFileSync } from "node:fs";
import { dirname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";

export const ROKU = join(dirname(fileURLToPath(import.meta.url)), "..");
export const LOGS = join(ROKU, "build", "logs");
export const GENERATED = join(ROKU, "build", "tests");
const CLI = join(ROKU, "node_modules", "brs-node", "bin", "brs.cli.js");

/** The channel's BrightScript sources (source/*.brs), as paths. */
export function sources() {
  const dir = join(ROKU, "source");
  return readdirSync(dir)
    .filter((f) => f.endsWith(".brs") && f !== "main.brs")
    .sort()
    .map((f) => join(dir, f));
}

/** A JS value as a BrightScript expression: ParseJson of an ASCII-only string literal. */
export function brsJson(value) {
  const json = JSON.stringify(value).replace(/[\u007f-￿]/g, (c) => `\\u${c.charCodeAt(0).toString(16).padStart(4, "0")}`);
  return `ParseJson("${json.replaceAll('"', '""')}")`;
}

/** Writes a generated test file under build/tests and returns its path. */
export function writeTest(name, code) {
  mkdirSync(GENERATED, { recursive: true });
  const path = join(GENERATED, `${name}.brs`);
  writeFileSync(path, code);
  return path;
}

/**
 * Runs `files` (the test's Main first) with the channel's sources on brs-engine, saves stdout to
 * build/logs/<name>.log and returns {pass, fail, failures, out}.
 */
export function runBrs(name, files) {
  mkdirSync(LOGS, { recursive: true });
  let out;
  try {
    out = execFileSync(process.execPath, [CLI, "--no-sg", "--root", ".", ...[...files, ...sources()].map((f) => relative(ROKU, f))], {
      cwd: ROKU,
      encoding: "utf8",
      maxBuffer: 64 * 1024 * 1024,
      timeout: 120_000,
    });
  } catch (err) {
    out = `${err.stdout ?? ""}\n${err.stderr ?? ""}\n${err.message}`;
  }
  writeFileSync(join(LOGS, `${name}.log`), out);
  const result = /RESULT pass=\s*(\d+) fail=\s*(\d+)/.exec(out);
  const failures = out.split(/\r?\n/).filter((l) => l.startsWith("FAIL "));
  if (!result) {
    return { pass: 0, fail: -1, failures: [`no RESULT line (brs-engine crashed?), see build/logs/${name}.log`, ...out.split("\n").slice(-20)], out };
  }
  return { pass: Number(result[1]), fail: Number(result[2]), failures, out };
}

/** BrightScript helpers every test may use (appended to each generated test). */
export const BRS_HELPERS = `
function t_Hex(s as String) as String
    out = []
    for each ch in s.Split("")
        out.Push(UCase(StrI(Asc(ch), 16)))
    end for
    return out.Join(" ")
end function

function t_Lines(a as Object) as String
    out = []
    for each s in a
        out.Push("[" + t_Hex(s) + "]")
    end for
    return out.Join(" ")
end function

function t_Int(v as Dynamic, fallback as Integer) as Integer
    if v = invalid then return fallback
    return v
end function

function t_Str(v as Dynamic, fallback as String) as String
    if v = invalid then return fallback
    return v
end function
`;
