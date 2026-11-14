// Story 58.11 T02 — the LG package: the version rule, the staged folder (appinfo.json at
// the version, icons, the Arabic title, no source maps) and the lint, on a good app and on
// each thing it must refuse. `node --test apps/tv/web/webos/webos-package.test.mjs` (the CI job then lints the
// real .ipk that ares-package built).
import { test } from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import zlib from "node:zlib";
import { ipkVersion, stage } from "./package.mjs";
import { REQUIRED_ACG, appFiles, lintApp, pngSize, readAr, readTar } from "./lint-ipk.mjs";

const tmp = () => fs.mkdtempSync(path.join(os.tmpdir(), "maktaba-webos-"));

function fakeDist() {
  const dist = tmp();
  fs.mkdirSync(path.join(dist, "assets"));
  fs.writeFileSync(
    path.join(dist, "index.html"),
    '<!doctype html><html><head><script defer src="./assets/index-x.js"></script></head></html>'
  );
  fs.writeFileSync(path.join(dist, "assets/index-x.js"), "console.log(1)");
  fs.writeFileSync(path.join(dist, "assets/index-x.js.map"), "{}");
  return dist;
}

/** The staged folder as the lint reads it: relative path → bytes. */
function filesOf(dir, base = dir, out = new Map()) {
  for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
    const p = path.join(dir, e.name);
    if (e.isDirectory()) filesOf(p, base, out);
    else out.set(path.relative(base, p).split(path.sep).join("/"), fs.readFileSync(p));
  }
  return out;
}

test("the version is three numbers, from a tag", () => {
  assert.equal(ipkVersion("v1.4.2"), "1.4.2");
  assert.equal(ipkVersion("0.12.0"), "0.12.0");
  for (const bad of ["1.4", "1.04.2", "v1.4.2-rc1", "", undefined, "dev"])
    assert.throws(() => ipkVersion(bad));
});

test("the staged app carries appinfo.json at the version, the icons and the Arabic title", () => {
  const dir = path.join(tmp(), "app");
  const info = stage({ dist: fakeDist(), version: "1.4.2", dir });
  assert.equal(info.version, "1.4.2");
  const files = filesOf(dir);
  assert.ok(!files.has("assets/index-x.js.map"), "no source map");
  assert.deepEqual(lintApp(files, { version: "1.4.2" }), []);
  assert.deepEqual(pngSize(files.get("icon.png")), { w: 80, h: 80 });
  assert.deepEqual(pngSize(files.get("largeIcon.png")), { w: 130, h: 130 });
});

test("the lint refuses what the store or the launcher cannot take", () => {
  const dir = path.join(tmp(), "app");
  stage({ dist: fakeDist(), version: "2.0.0", dir });
  const good = filesOf(dir);
  const info = JSON.parse(good.get("appinfo.json").toString());
  const withInfo = (patch) => {
    const f = new Map(good);
    f.set("appinfo.json", Buffer.from(JSON.stringify({ ...info, ...patch })));
    return f;
  };
  const cases = [
    [{ id: "com.example.other" }, /id is/],
    [{ version: "0.0.0" }, /template's 0\.0\.0/],
    [{ type: "native" }, /type is/],
    [{ disableBackHistoryAPI: false }, /disableBackHistoryAPI/],
    [{ resolution: "1280x720" }, /resolution/],
    [{ requiredACG: [] }, /requiredACG/],
    [{ requiredACG: [...REQUIRED_ACG, "database.operation"] }, /requiredACG/],
    [{ icon: "largeIcon.png" }, /icon \(largeIcon\.png\) is not a 80×80 PNG/],
  ];
  for (const [patch, want] of cases) {
    const errs = lintApp(withInfo(patch));
    assert.ok(
      errs.some((e) => want.test(e)),
      `${JSON.stringify(patch)}: ${JSON.stringify(errs)}`
    );
  }
  assert.ok(lintApp(withInfo({}), { version: "2.0.1" }).some((e) => /not 2\.0\.1/.test(e)));
  const remote = new Map(good);
  remote.set("index.html", Buffer.from('<script src="https://cdn.example/x.js"></script>'));
  assert.ok(lintApp(remote).some((e) => /not a relative file/.test(e)));
  const leaky = new Map(good);
  leaky.set("assets/k.js", Buffer.from("-----BEGIN RSA PRIVATE KEY-----"));
  leaky.set("assets/k.js.map", Buffer.from("{}"));
  const errs = lintApp(leaky);
  assert.ok(errs.some((e) => /secret/.test(e)));
  assert.ok(errs.some((e) => /source map/.test(e)));
  const noAr = new Map(good);
  noAr.delete("resources/ar/appinfo.json");
  assert.ok(lintApp(noAr).some((e) => /Arabic title/.test(e)));
});

/** A tar of files (ustar, as ares-package writes). */
function tar(files) {
  const blocks = [];
  for (const [name, body] of files) {
    const h = Buffer.alloc(512);
    h.write(name, 0, "utf8");
    h.write("0000644\0", 100);
    h.write(body.length.toString(8).padStart(11, "0") + "\0", 124);
    h.write("0", 156);
    h.write("ustar\0", 257);
    blocks.push(h, body, Buffer.alloc((512 - (body.length % 512)) % 512));
  }
  blocks.push(Buffer.alloc(1024));
  return Buffer.concat(blocks);
}

function ar(members) {
  const parts = [Buffer.from("!<arch>\n", "latin1")];
  for (const [name, body] of members) {
    const h = `${`${name}/`.padEnd(16)}${"0".padEnd(12)}${"0".padEnd(6)}${"0".padEnd(6)}${"100644".padEnd(8)}${String(body.length).padEnd(10)}\`\n`;
    parts.push(Buffer.from(h, "latin1"), body);
    if (body.length % 2) parts.push(Buffer.from("\n"));
  }
  return Buffer.concat(parts);
}

test("an .ipk is read: ar, then the app folder in data.tar.gz", () => {
  const dir = path.join(tmp(), "app");
  stage({ dist: fakeDist(), version: "3.1.4", dir });
  const app = filesOf(dir);
  const prefix = "./usr/palm/applications/com.hamzalabs.maktaba.tv/";
  const data = zlib.gzipSync(tar([...app].map(([n, b]) => [prefix + n, b])));
  const ipk = ar([
    ["debian-binary", Buffer.from("2.0\n")],
    ["control.tar.gz", zlib.gzipSync(tar([["./control", Buffer.from("Package: x\n")]]))],
    ["data.tar.gz", data],
  ]);
  assert.deepEqual([...readAr(ipk).keys()], ["debian-binary", "control.tar.gz", "data.tar.gz"]);
  assert.equal(readTar(zlib.gunzipSync(data)).size, app.size);
  const files = appFiles(ipk);
  assert.deepEqual(lintApp(files, { version: "3.1.4" }), []);
  assert.throws(() => appFiles(Buffer.from("not an archive")), /not an ar archive/);
});
