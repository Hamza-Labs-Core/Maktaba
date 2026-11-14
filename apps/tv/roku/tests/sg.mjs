// Drives the channel on brs-engine's SceneGraph (story 58.12 T03): the CLI runs the built zip
// under a terminal (tests/pty_run.py), keys go to its ECP server (port 8060, as on a Roku),
// screens are saved with its Ctrl+S snapshot, and the channel's own log lines ("[screen] …
// ready", "[focus] …") say when a step is done. Everything it prints is kept in build/logs.

import { spawn } from "node:child_process";
import { mkdirSync, renameSync, writeFileSync, existsSync, rmSync } from "node:fs";
import { join } from "node:path";

import { LOGS, ROKU } from "./brs.mjs";

const CLI = join(ROKU, "node_modules", "brs-node", "bin", "brs.cli.js");
export const SCREENS = join(ROKU, "build", "screens");
const ECP = "http://127.0.0.1:8060";
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

export class Channel {
  constructor(name) {
    this.name = name;
    this.out = "";
    this.seen = 0;
  }

  /** Runs zip with a registry kept in dataDir (a saved home survives to the next launch). */
  static async start(name, zip, dataDir, deepLink) {
    const c = new Channel(name);
    mkdirSync(SCREENS, { recursive: true });
    mkdirSync(LOGS, { recursive: true });
    c.snap = join(ROKU, "build", `snap-${name}.png`);
    const args = [join(ROKU, "tests", "pty_run.py"), process.execPath, CLI, zip, "--ecp", "-y", "-c", "0", "-s", c.snap];
    if (deepLink) args.push("-k", deepLink);
    c.started = Date.now();
    c.proc = spawn("python3", args, {
      cwd: join(ROKU, "build"),
      env: { ...process.env, HOME: dataDir, XDG_DATA_HOME: join(dataDir, "data"), XDG_CONFIG_HOME: join(dataDir, "config"), FORCE_COLOR: "0", NODE_OPTIONS: `--require ${join(ROKU, "tests", "roku_text.cjs")}` },
      stdio: ["pipe", "pipe", "pipe"],
    });
    const take = (d) => {
      c.out += d.toString("utf8").replace(/\r/g, "");
    };
    c.proc.stdout.on("data", take);
    c.proc.stderr.on("data", take);
    c.exited = new Promise((r) => c.proc.on("exit", r));
    await c.waitFor(/ECP and SSDP servers initialized/, 30000);
    return c;
  }

  /** Waits for a line matching re printed after the last match; returns the match. */
  async waitFor(re, ms = 15000) {
    const end = Date.now() + ms;
    while (Date.now() < end) {
      const rest = this.out.slice(this.seen);
      const mt = re.exec(rest);
      if (mt) {
        this.seen += mt.index + mt[0].length;
        return mt;
      }
      await sleep(50);
    }
    this.save();
    throw new Error(`${this.name}: no ${re} within ${ms} ms (see build/logs/screens-${this.name}.log)\n${this.out.slice(-3000)}`);
  }

  /** Presses a key on the ECP server, as a Roku remote would (Up, Select, Back, Info…). */
  async key(k, settle = 600) {
    const res = await fetch(`${ECP}/keypress/${k}`, { method: "POST" });
    if (!res.ok) throw new Error(`keypress ${k}: ${res.status}`);
    await sleep(settle);
  }

  async keys(list, settle = 600) {
    for (const k of list) await this.key(k, settle);
  }

  /** A deep link while running (ECP /input: what voice and Roku search send). */
  async input(params) {
    const q = new URLSearchParams(params).toString();
    const res = await fetch(`${ECP}/input?${q}`, { method: "POST" });
    if (!res.ok) throw new Error(`input: ${res.status}`);
  }

  /** Saves the screen as build/screens/<name>.png after posters have had time to load. */
  async shot(name, settle = 1500) {
    await sleep(settle);
    if (existsSync(this.snap)) rmSync(this.snap);
    this.proc.stdin.write("\x13");
    await this.waitFor(/Screenshot saved as/, 10000);
    const to = join(SCREENS, `${name}.png`);
    renameSync(this.snap, to);
    return to;
  }

  save() {
    writeFileSync(join(LOGS, `screens-${this.name}.log`), this.out);
  }

  async stop() {
    this.save();
    this.proc.stdin.end();
    await Promise.race([this.exited, sleep(3000)]);
    this.proc.kill("SIGKILL");
    await sleep(500);
  }
}
