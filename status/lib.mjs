// The status page's model (Epic 50 story 50.11): probe rounds to component states, day
// aggregates to uptime bars, incident and maintenance files to posts, and the model to
// summary.json and the English and Arabic pages. No dependencies: Node's standard library.
import fs from "node:fs";
import path from "node:path";

export const STATES = ["operational", "maintenance", "degraded", "outage"];
export const KNOWN_COMPONENTS = ["relay", "api", "push", "payments", "plugins"];
const INCIDENT_STATES = ["investigating", "identified", "monitoring", "resolved"];
const MAINTENANCE_STATES = ["scheduled", "in_progress", "completed"];
const ISO_UTC = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}(:\d{2}(\.\d{1,3})?)?Z$/;
const FILE_NAME = /^(\d{4}-\d{2}-\d{2})-([a-z0-9]+(?:-[a-z0-9]+)*)\.md$/;
const HELD_DEGRADED = 0.05; // more than 5 % of relayed requests held: degraded
const PUSH_LAG_DEGRADED = 300; // seconds

/** The worst of some states (outage > degraded > maintenance > operational). */
export function worst(states) {
  let w = "operational";
  for (const s of states) if (s && STATES.indexOf(s) > STATES.indexOf(w)) w = s;
  return w;
}

/**
 * One round's state of a component, or null when nothing measured it. A round is
 * { at, relay: { actions, witness, held_ratio? }, api: { actions, witness }, push: { lag } }
 * with 1 (pass), 0 (fail) or null (that network gave no answer) per network.
 * Every network that answered failing is an outage; one of two failing is degraded
 * (probably that probe's own network).
 */
export function roundState(component, round) {
  const r = round?.[component];
  if (!r) return null;
  if (component === "push") {
    if (typeof r.lag !== "number") return null;
    return r.lag > PUSH_LAG_DEGRADED ? "degraded" : "operational";
  }
  const nets = [r.actions, r.witness].filter((v) => v === 0 || v === 1);
  if (nets.length === 0) return null;
  const fails = nets.filter((v) => v === 0).length;
  if (fails === nets.length) return "outage";
  if (fails > 0) return "degraded";
  if (typeof r.held_ratio === "number" && r.held_ratio > HELD_DEGRADED) return "degraded";
  return "operational";
}

/**
 * Applies one round's state to a component's record: a new state takes two rounds in a
 * row (a lost packet never flashes a banner); the first measurement is taken as it is.
 */
export function applyRound(rec, s, at) {
  const cur = rec ?? { state: null, since: null, pending: null };
  if (s === null) return cur;
  if (cur.state === null) return { state: s, since: at, pending: null };
  if (s === cur.state) return { ...cur, pending: null };
  const count = cur.pending?.state === s ? cur.pending.count + 1 : 1;
  if (count >= 2) return { state: s, since: at, pending: null };
  return { ...cur, pending: { state: s, count } };
}

function readJSON(file, fallback) {
  try {
    return JSON.parse(fs.readFileSync(file, "utf8"));
  } catch (e) {
    if (e.code === "ENOENT") return fallback;
    throw new Error(`${file}: ${e.message}`);
  }
}

function writeJSON(file, v) {
  fs.mkdirSync(path.dirname(file), { recursive: true });
  fs.writeFileSync(file, JSON.stringify(v, null, 1) + "\n");
}

/**
 * Folds probe rounds into the data directory: checks/state.json (each component's state
 * with its pending change) and checks/<component>/<yyyy-mm-dd>.json (the day's rounds by
 * the state they left the component in).
 */
export function update(dataDir, rounds, components) {
  const stateFile = path.join(dataDir, "checks", "state.json");
  const state = readJSON(stateFile, { updated_at: null, components: {} });
  const days = new Map();
  const day = (c, date) => {
    const k = `${c}/${date}`;
    if (!days.has(k)) {
      days.set(k, readJSON(path.join(dataDir, "checks", c, `${date}.json`),
        { date, rounds: 0, operational: 0, degraded: 0, outage: 0 }));
    }
    return days.get(k);
  };
  const sorted = [...rounds].sort((a, b) => a.at.localeCompare(b.at));
  for (const round of sorted) {
    if (!ISO_UTC.test(round.at ?? "")) throw new Error(`a round without a UTC time: ${JSON.stringify(round)}`);
    if (state.updated_at && round.at <= state.updated_at) continue; // already folded in
    for (const c of components) {
      const s = roundState(c, round);
      const rec = applyRound(state.components[c], s, round.at);
      state.components[c] = rec;
      if (s === null || rec.state === null) continue;
      const d = day(c, round.at.slice(0, 10));
      d.rounds++;
      d[rec.state === "maintenance" ? "operational" : rec.state]++;
    }
    state.updated_at = round.at;
  }
  for (const [k, v] of days) writeJSON(path.join(dataDir, "checks", `${k}.json`), v);
  writeJSON(stateFile, state);
  return state;
}

/** The share of a set of day records' rounds that were not an outage, or null with none. */
export function uptime(dayRecords) {
  let total = 0, down = 0;
  for (const d of dayRecords) {
    if (!d) continue;
    total += d.rounds;
    down += d.outage;
  }
  return total === 0 ? null : (total - down) / total;
}

function isoDate(d) {
  return d.toISOString().slice(0, 10);
}

/** The last n UTC dates, oldest first, ending today. */
export function lastDays(now, n) {
  const out = [];
  const t = Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), now.getUTCDate());
  for (let i = n - 1; i >= 0; i--) out.push(isoDate(new Date(t - i * 86400000)));
  return out;
}

/** The dates of the calendar month before now's. */
export function lastMonthDays(now) {
  const first = new Date(Date.UTC(now.getUTCFullYear(), now.getUTCMonth() - 1, 1));
  const out = [];
  for (let d = first; d.getUTCMonth() === first.getUTCMonth(); d = new Date(d.getTime() + 86400000)) out.push(isoDate(d));
  return out;
}

function parseFrontMatter(text, file) {
  const m = /^---\n([\s\S]*?)\n---\n?([\s\S]*)$/.exec(text.replace(/\r\n/g, "\n"));
  if (!m) throw new Error(`${file}: no front matter (--- lines at the top)`);
  const fm = {};
  for (const line of m[1].split("\n")) {
    if (!line.trim() || line.trim().startsWith("#")) continue;
    const i = line.indexOf(":");
    if (i < 0) throw new Error(`${file}: front matter line without a colon: ${line}`);
    let v = line.slice(i + 1).trim();
    if (/^".*"$/.test(v) || /^'.*'$/.test(v)) v = v.slice(1, -1);
    fm[line.slice(0, i).trim()] = v;
  }
  return { fm, body: m[2] };
}

function splitSections(body, file) {
  const parts = { en: null, ar: null };
  let cur = null;
  const lines = [];
  const flush = () => {
    if (cur) parts[cur] = lines.splice(0).join("\n").trim();
    else lines.length = 0;
  };
  for (const line of body.split("\n")) {
    const h = /^##\s+(.+?)\s*$/.exec(line);
    if (h && (h[1] === "English" || h[1] === "العربية")) {
      flush();
      cur = h[1] === "English" ? "en" : "ar";
      continue;
    }
    lines.push(line);
  }
  flush();
  if (!parts.en) throw new Error(`${file}: no "## English" section`);
  if (!parts.ar) throw new Error(`${file}: no "## العربية" (Arabic) section`);
  return parts;
}

/** Reads and checks every incident and maintenance file; any mistake fails with the file's name. */
export function loadPosts(dir) {
  let names = [];
  try {
    names = fs.readdirSync(dir).filter((n) => n.endsWith(".md") && n !== "README.md").sort();
  } catch (e) {
    if (e.code !== "ENOENT") throw e;
  }
  return names.map((name) => {
    const file = path.join(dir, name);
    const fn = FILE_NAME.exec(name);
    if (!fn) throw new Error(`${name}: the name must be <yyyy-mm-dd>-<slug>.md`);
    const { fm, body } = parseFrontMatter(fs.readFileSync(file, "utf8"), name);
    const kind = fm.kind || "incident";
    if (kind !== "incident" && kind !== "maintenance") throw new Error(`${name}: kind must be incident or maintenance`);
    for (const k of ["title_en", "title_ar", "component", "state"]) {
      if (!fm[k]) throw new Error(`${name}: ${k} is missing`);
    }
    const components = fm.component.split(",").map((s) => s.trim()).filter(Boolean);
    for (const c of components) if (!KNOWN_COMPONENTS.includes(c)) throw new Error(`${name}: unknown component ${c}`);
    const allowed = kind === "incident" ? INCIDENT_STATES : MAINTENANCE_STATES;
    if (!allowed.includes(fm.state)) throw new Error(`${name}: state must be one of ${allowed.join(", ")}`);
    const times = kind === "incident" ? ["started", "resolved"] : ["starts", "ends"];
    for (const k of times) {
      if (fm[k] === undefined || fm[k] === "") {
        if (k === "resolved") continue;
        throw new Error(`${name}: ${k} is missing`);
      }
      if (!ISO_UTC.test(fm[k])) throw new Error(`${name}: ${k} must be UTC in ISO 8601 (2026-11-03T10:15:00Z), not ${fm[k]}`);
    }
    if (kind === "incident" && fm.state === "resolved" && !fm.resolved) throw new Error(`${name}: a resolved incident needs resolved`);
    if (kind === "maintenance" && fm.ends <= fm.starts) throw new Error(`${name}: ends must come after starts`);
    if (fm.impact && !["degraded", "outage"].includes(fm.impact)) throw new Error(`${name}: impact must be degraded or outage`);
    return {
      id: name.slice(0, -3), kind, components, state: fm.state, impact: fm.impact || null,
      started: fm.started || null, resolved: fm.resolved || null, starts: fm.starts || null, ends: fm.ends || null,
      title: { en: fm.title_en, ar: fm.title_ar }, body: splitSections(body, name),
    };
  });
}

const ms = (iso) => Date.parse(iso);

/** Everything the page and summary.json show, at now. */
export function model({ config, state, dayOf, posts, now }) {
  const t = now.getTime();
  const maintenanceActive = posts.filter((p) => p.kind === "maintenance" && p.state !== "completed" && ms(p.starts) <= t && t < ms(p.ends));
  const maintenanceAhead = posts.filter((p) => p.kind === "maintenance" && p.state !== "completed" && ms(p.ends) > t)
    .sort((a, b) => a.starts.localeCompare(b.starts));
  const open = posts.filter((p) => p.kind === "incident" && p.state !== "resolved")
    .sort((a, b) => b.started.localeCompare(a.started));
  const since90 = t - config.days * 86400000;
  const past = posts.filter((p) => (p.kind === "incident" && p.state === "resolved" && ms(p.resolved) >= since90)
    || (p.kind === "maintenance" && (p.state === "completed" || ms(p.ends) <= t) && ms(p.ends) >= since90))
    .sort((a, b) => (b.resolved || b.ends).localeCompare(a.resolved || a.ends));

  const shown = [...config.components, ...(config.plugins_listed ? ["plugins"] : [])];
  const days = lastDays(now, config.days);
  const components = {};
  for (const c of shown) {
    let s = state.components?.[c]?.state ?? null;
    for (const p of open) if (p.impact && p.components.includes(c)) s = worst([s, p.impact]);
    if (maintenanceActive.some((p) => p.components.includes(c))) s = "maintenance";
    const recs = days.map((d) => dayOf(c, d));
    components[c] = {
      state: s,
      days: days.map((d, i) => ({ date: d, uptime: uptime([recs[i]]) })),
      uptime90: uptime(recs),
    };
  }
  const monthDays = lastMonthDays(now);
  const lastMonth = uptime(monthDays.map((d) => dayOf("relay", d)));
  const overall = worst(Object.values(components).map((c) => c.state));
  return {
    updated_at: state.updated_at, now: now.toISOString().replace(/\.\d{3}Z$/, "Z"),
    overall, components, open, maintenance: maintenanceAhead, past,
    target: config.target, lastMonth, config,
  };
}

/** summary.json for the apps (story 50.11 §4). */
export function summary(m) {
  const site = m.config.site;
  const inc = m.open[0];
  const components = {};
  for (const [c, v] of Object.entries(m.components)) components[c] = v.state ?? "operational";
  return {
    updated_at: m.updated_at ?? m.now,
    overall: m.overall,
    components,
    incident: inc ? { id: inc.id, state: inc.state, components: inc.components, title: inc.title, url: `${site}/i/${inc.id}` } : null,
    maintenance: m.maintenance.map((p) => ({
      id: p.id, starts_at: p.starts, ends_at: p.ends, components: p.components, title: p.title, url: `${site}/i/${p.id}`,
    })),
  };
}
