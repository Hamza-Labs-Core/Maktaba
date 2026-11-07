// #756, the isolated runner on Cloudflare Containers: the deploy config that turns it on, and
// the container's server (check-runner/serve.py) as a process: health, the order of the
// files, the size limit, nothing else answered.
import { spawn, type ChildProcess } from "node:child_process";
import { request } from "node:http";
import { existsSync, mkdtempSync, readFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { afterAll, beforeAll, describe, expect, it } from "vitest";
// @ts-expect-error a plain ES module script
import { containersConfig, stripJsonc } from "../scripts/containers-config.mjs";

const STORE = resolve(__dirname, "..");

describe("the containers deploy config", () => {
  it("adds the CheckRunner container and RUNNER = containers to the committed config, nothing else", () => {
    const base = readFileSync(join(STORE, "wrangler.jsonc"), "utf8");
    const committed = JSON.parse(stripJsonc(base));
    expect(committed.vars.RUNNER).toBe("github");
    expect(committed.containers).toBeUndefined();
    const cfg = containersConfig(base);
    expect(cfg.vars.RUNNER).toBe("containers");
    expect(cfg.containers).toEqual([expect.objectContaining({ class_name: "CheckRunner", image: "./check-runner/Dockerfile", image_build_context: ".." })]);
    expect(existsSync(join(STORE, "check-runner/Dockerfile"))).toBe(true);
    expect(cfg.durable_objects).toEqual(committed.durable_objects);
    expect(cfg.migrations.at(-1)).toEqual({ tag: "v2", new_sqlite_classes: ["CheckRunner"] });
    const { vars: _a, containers: _b, ...rest } = cfg;
    const { vars: _c, ...committedRest } = committed;
    expect(rest).toEqual(committedRest);
  });

  it("strips comments outside strings only, and trailing commas", () => {
    expect(JSON.parse(stripJsonc('{ "a": "x // y", // c\n "b": [1, 2,], /* d */ }'))).toEqual({ a: "x // y", b: [1, 2] });
  });
});

describe("the container's server", () => {
  let p: ChildProcess;
  const port = 18000 + Math.floor(Math.random() * 2000);
  const url = (path: string) => `http://127.0.0.1:${port}${path}`;

  beforeAll(async () => {
    p = spawn("python3", [join(STORE, "check-runner/serve.py"), String(port)], { stdio: "ignore", env: { PATH: process.env.PATH ?? "", TMPDIR: mkdtempSync(join(tmpdir(), "serve-")) } });
    for (let i = 0; i < 50; i++) {
      try {
        if ((await fetch(url("/health"))).ok) return;
      } catch {
        // not listening yet
      }
      await new Promise((r) => setTimeout(r, 100));
    }
    throw new Error("serve.py did not start");
  });
  afterAll(() => {
    p.kill();
  });

  it("answers health, refuses a run before its files, a file over the limit and any other path", async () => {
    expect((await fetch(url("/health"))).status).toBe(200);
    expect((await fetch(url("/run"), { method: "POST" })).status).toBe(409);
    expect((await fetch(url("/package"), { method: "PUT", body: new Uint8Array(10) })).status).toBe(204);
    expect((await fetch(url("/run"), { method: "POST" })).status).toBe(409); // no source yet
    // a source over the store's limit is refused on its length, before any byte is read
    const over = await new Promise<number>((res, rej) => {
      const r = request({ host: "127.0.0.1", port, path: "/source", method: "PUT", headers: { "Content-Length": "209715201" } }, (a) => res(a.statusCode ?? 0));
      r.on("error", rej);
      r.end();
    });
    expect(over).toBe(413);
    expect((await fetch(url("/etc/passwd"), { method: "PUT", body: "x" })).status).toBe(404);
    expect((await fetch(url("/anything"))).status).toBe(404);
  });
});
