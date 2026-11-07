#!/usr/bin/env node
// Builds status.maktaba.tv (Epic 50 story 50.11): the English and Arabic pages, one page per
// incident or maintenance notice, and summary.json for the apps.
//
//   node status/build.mjs --checks <dir with checks/> --out <dir> [--now <ISO time>]
//
// The checks come from the status-data branch (status-probe writes them); incident and
// maintenance posts from status/data/incidents/ on main. A post with a mistake (no Arabic
// title, a time not in UTC) fails the build with its file name.
import fs from "node:fs";
import path from "node:path";
import { parseArgs } from "node:util";
import { fileURLToPath } from "node:url";
import { loadPosts, model, summary } from "./lib.mjs";
import { LANGS, page, postPage } from "./render.mjs";

const here = path.dirname(fileURLToPath(import.meta.url));

export function build({ checksDir, incidentsDir, out, now, config }) {
  const posts = loadPosts(incidentsDir);
  const readDay = (c, d) => {
    try {
      return JSON.parse(fs.readFileSync(path.join(checksDir, "checks", c, `${d}.json`), "utf8"));
    } catch (e) {
      if (e.code === "ENOENT") return null;
      throw e;
    }
  };
  let state = { updated_at: null, components: {} };
  try {
    state = JSON.parse(fs.readFileSync(path.join(checksDir, "checks", "state.json"), "utf8"));
  } catch (e) {
    if (e.code !== "ENOENT") throw e;
  }
  const m = model({ config, state, dayOf: readDay, posts, now });
  const write = (rel, text) => {
    const f = path.join(out, rel);
    fs.mkdirSync(path.dirname(f), { recursive: true });
    fs.writeFileSync(f, text);
  };
  for (const lang of LANGS) {
    const base = lang === "en" ? "" : `${lang}/`;
    write(`${base}index.html`, page(lang, m));
    for (const p of posts) write(`${base}i/${p.id}/index.html`, postPage(lang, p));
  }
  write("summary.json", JSON.stringify(summary(m), null, 1) + "\n");
  write("CNAME", new URL(config.site).host + "\n");
  write(".nojekyll", "");
  // Cloudflare Pages (the fallback host) reads these; GitHub Pages already sends CORS *
  write("_headers", "/summary.json\n  Access-Control-Allow-Origin: *\n  Cache-Control: max-age=60\n");
  return m;
}

if (process.argv[1] && fileURLToPath(import.meta.url) === path.resolve(process.argv[1])) {
  const { values } = parseArgs({
    options: {
      checks: { type: "string" }, out: { type: "string" }, now: { type: "string" },
      incidents: { type: "string", default: path.join(here, "data", "incidents") },
    },
  });
  if (!values.out) {
    console.error("usage: build.mjs --checks <dir> --out <dir> [--now <ISO time>] [--incidents <dir>]");
    process.exit(2);
  }
  const config = JSON.parse(fs.readFileSync(path.join(here, "config.json"), "utf8"));
  try {
    const m = build({
      checksDir: values.checks ?? path.join(here, "data"), incidentsDir: values.incidents, out: values.out,
      now: values.now ? new Date(values.now) : new Date(), config,
    });
    console.log(`status: built ${values.out}: ${m.overall}, ${m.open.length} open, ${m.maintenance.length} maintenance`);
  } catch (e) {
    console.error(`status: ${e.message}`);
    process.exit(1);
  }
}
