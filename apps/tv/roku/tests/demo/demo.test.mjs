// Story 60.14 part 2: the channel's screens on the demo library (tests/fixtures/demo-library),
// for the Roku store gallery, drawn by brs-engine's SceneGraph at 1920×1080 as screens.test.mjs
// draws its own (keys over ECP, the channel's log saying when a screen is ready). The library
// is answered by tests/demo/demohome.mjs on top of the mock home and cloud.
//
// Each screen is compared, at a quarter of its size, with its reference in
// tests/screenshots/demo/roku-<screen>-<lang>.png by mean pixel difference, as the other screen
// references are; the references are full size, for compose.mjs (the store gallery, which
// reads every `screenshots/demo/` folder under apps/). UPDATE_SCREENS=1 rewrites them.
//
// The screens: sign in with a code, Home, the Caminandes series, the player with the Arabic
// subtitle line (brs-engine draws no video picture: the line and the overlay on black), and
// Search with the library's query, in English, then the same in Arabic. Roku has no Live TV
// screen, so the gallery's guide beat has no Roku capture.
//
// It runs alone (`npm test` runs it after the other files): one brs-engine at a time holds
// ECP's port.

import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { copyFileSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { createRequire } from "node:module";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { after, before, test } from "node:test";

import { build } from "../../tools/build.mjs";
import { LOGS, ROKU } from "../brs.mjs";
import { startCloud, startHome } from "../mockhome.mjs";
import { Channel } from "../sg.mjs";
import { demoHome } from "./demohome.mjs";

const require = createRequire(import.meta.url);
const { createCanvas, loadImage } = require("canvas");
const REFS = join(ROKU, "tests", "screenshots", "demo");
/** Mean absolute difference per channel (0–255), as screens.test.mjs allows. */
const TOLERANCE = 12;

const requests = [];
const compared = [];
let demo;
let home;
let cloud;
let zip;
let data;
let tlsDir;

before(async () => {
  tlsDir = mkdtempSync(join(tmpdir(), "maktaba-roku-tls-"));
  execFileSync("openssl", ["req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1", "-subj", "/CN=localhost", "-addext", "subjectAltName=DNS:localhost", "-keyout", join(tlsDir, "key.pem"), "-out", join(tlsDir, "cert.pem")], { stdio: "ignore" });
  process.env.NODE_EXTRA_CA_CERTS = join(tlsDir, "cert.pem");
  demo = demoHome();
  home = await startHome((l) => requests.push(l), { key: readFileSync(join(tlsDir, "key.pem")), cert: readFileSync(join(tlsDir, "cert.pem")) }, demo);
  cloud = await startCloud((l) => requests.push(l), home.httpsPort);
  cloud.state.homeName = demo.name;
  mkdirSync(LOGS, { recursive: true });
  zip = await build(["--test", "--cloud", `http://127.0.0.1:${cloud.port}`, "--probe-ip", "127.0.0.9", "--probe-port", String(home.port), "--out", join(ROKU, "build", "maktaba-roku-demo.zip")]);
  data = mkdtempSync(join(tmpdir(), "maktaba-roku-demo-"));
});

after(() => {
  mkdirSync(LOGS, { recursive: true });
  writeFileSync(join(LOGS, "demo-home-requests.log"), requests.join("\n") + "\n");
  writeFileSync(join(LOGS, "demo-screens-compared.log"), compared.join("\n") + "\n");
  home?.server.close();
  cloud?.server.close();
  if (tlsDir) rmSync(tlsDir, { recursive: true, force: true });
  if (data) rmSync(data, { recursive: true, force: true });
});

async function small(path) {
  const img = await loadImage(readFileSync(path));
  const c = createCanvas(480, 270);
  c.getContext("2d").drawImage(img, 0, 0, 480, 270);
  return c;
}

/** Takes the screen as roku-<name> and compares it with its reference (or writes it). */
async function shoot(c, name, settle = 2500) {
  const file = `roku-${name}`;
  const path = await c.shot(file, settle);
  const img = await loadImage(readFileSync(path));
  assert.deepEqual([img.width, img.height], [1920, 1080], `${file}: brs-engine drew ${img.width}×${img.height}`);
  const ref = join(REFS, `${file}.png`);
  if (process.env.UPDATE_SCREENS === "1" || !existsSync(ref)) {
    mkdirSync(REFS, { recursive: true });
    copyFileSync(path, ref);
    compared.push(`${file}: reference written`);
    if (process.env.CI) assert.fail(`${file}: no reference in tests/screenshots/demo (run with UPDATE_SCREENS=1 and commit it)`);
    return;
  }
  const a = (await small(path)).getContext("2d").getImageData(0, 0, 480, 270).data;
  const b = (await small(ref)).getContext("2d").getImageData(0, 0, 480, 270).data;
  let sum = 0;
  for (let i = 0; i < a.length; i += 4) sum += Math.abs(a[i] - b[i]) + Math.abs(a[i + 1] - b[i + 1]) + Math.abs(a[i + 2] - b[i + 2]);
  const mean = sum / ((a.length / 4) * 3);
  compared.push(`${file}: mean difference ${mean.toFixed(2)} (tolerance ${TOLERANCE})`);
  assert.ok(mean <= TOLERANCE, `${file} differs from tests/screenshots/demo/${file}.png by ${mean.toFixed(2)} (tolerance ${TOLERANCE}); see build/screens/${file}.png`);
}

/** Presses key until the channel's log matches re (at most max presses). */
async function pressUntil(c, key, re, max = 8) {
  for (let i = 0; i < max; i++) {
    await c.key(key);
    if (re.test(c.out.slice(c.seen))) return c.waitFor(re, 1000);
  }
  return c.waitFor(re, 3000);
}

/** The code screen (the cloud holds its approval while the screen is taken), then Home. */
async function signIn(c, lang, homes = 0) {
  // the saved homes come first in the list, then Sign in with a code
  for (let i = 0; i < homes; i++) await c.key("Down");
  cloud.state.delivered = false;
  cloud.state.polls = 0;
  cloud.state.hold = true;
  await c.key("Select");
  await c.waitFor(/\[qr\] drawn\s+29 modules/, 20000);
  await c.waitFor(/\[screen\] CodeScreen ready mode=cloud/);
  await shoot(c, `code-${lang}`, 1500);
  cloud.state.hold = false;
  await c.waitFor(/\[code\] signed in through relay\b/, 20000);
  await c.waitFor(new RegExp(`\\[screen\\] HomeScreen ready rows=\\s*\\d+ lang=${lang}`), 20000);
  await c.waitFor(/\[home\] for you\s+\d+/, 20000);
}

/** Home, the series, the player with its Arabic line, Search: in the channel's language. */
async function screens(c, lang) {
  const forward = lang === "ar" ? "Left" : "Right";
  await shoot(c, `home-${lang}`, 4000);

  // Continue watching's second card is Caminandes: Llamigos (in Arabic the row is laid from the
  // right, its index counted from the left); * → Go to the series
  const first = Number((c.out.match(/\[focus\] home row\s+0 item\s+(\d+)/g) ?? []).pop()?.match(/(\d+)$/)?.[1] ?? 0);
  await c.key(forward);
  await c.waitFor(new RegExp(`\\[focus\\] home row\\s+0 item\\s+${lang === "ar" ? first - 1 : first + 1}\\b`));
  await c.key("Info");
  const keys = (await c.waitFor(/\[actions\] ([a-z,]+)/))[1].split(",");
  assert.ok(keys.includes("series"), `the episode's actions offer its series (${keys})`);
  for (let i = 0; i < keys.indexOf("series"); i++) await c.key("Down");
  await c.key("Select");
  await c.waitFor(/\[screen\] SeriesScreen ready seasons=\s*2/);
  await shoot(c, `series-${lang}`, 4000);
  await c.key("Back");
  await c.waitFor(/\[nav\] back to HomeScreen/);

  // the player: Curiosity, its Arabic subtitle line in the channel's own layer
  await c.input({ contentId: demo.uuid("v-curiosity"), mediaType: "movie" });
  // the session and the subtitle list come back in any order: the line once both are in
  await c.waitFor(/\[subs\] cue at\s+\d+/, 20000);
  assert.match(c.out, /\[screen\] PlayerScreen ready mode=direct/);
  assert.match(c.out.slice(c.out.lastIndexOf("[link] play")), /\[subs\] own ar/, "the Arabic line is the channel's own layer");
  await shoot(c, `subtitles-${lang}`, 1500);
  await c.key("Back");
  await c.waitFor(/\[nav\] back to HomeScreen/);

  // Search: the library's query typed on the keyboard. Roku's keyboard is Latin (ECP's Lit_
  // takes one ASCII character, and brs-engine's text box draws Arabic unjoined), so the Arabic
  // screen searches the English query too, its answers in Arabic
  const q = demo.query("en");
  await c.key("Up");
  await c.waitFor(/\[focus\] home menu\s+0/);
  await c.keys([forward, forward]);
  await c.key("Select");
  await c.waitFor(/\[screen\] SearchScreen ready/);
  for (const ch of q) await c.key(`Lit_${ch}`, 300);
  // the last request asked the whole query (each letter's own came before it)
  for (;;) {
    const asked = JSON.parse((await c.waitFor(/\[search\] ask (\{[^\n]*\})/, 20000))[1]);
    if (asked.q === q) break;
  }
  await c.waitFor(/\[search\] results\s+[1-9]/, 20000);
  await shoot(c, `search-${lang}`, 4000);
  await c.key("Back");
  await c.waitFor(/\[nav\] back to HomeScreen/);
}

test("60.14: the channel on the demo library, English then Arabic", { timeout: 600000 }, async () => {
  const c = await Channel.start("demo", zip, data);
  try {
    await c.waitFor(/\[screen\] StartScreen ready homes=\s*0/, 30000);
    await signIn(c, "en");
    await screens(c, "en");

    // Account → العربية, then Sign out: the code screen in Arabic, and the home named in Arabic
    // (back from Search, the focus is on the menu's Search)
    await c.key("Right");
    await c.key("Select");
    await c.waitFor(/\[screen\] AccountScreen ready lang=en/);
    await c.keys(["Down", "Down", "Down"]);
    demo.lang = "ar";
    await c.key("Select");
    await c.waitFor(/\[account\] ar/);
    await c.waitFor(/\[screen\] AccountScreen ready lang=ar/);
    // Home is drawn again under it in Arabic; then from the list's top to Sign out
    await c.waitFor(/\[screen\] HomeScreen ready rows=\s*\d+ lang=ar/, 20000);
    await new Promise((r) => setTimeout(r, 1500));
    await c.keys(["Up", "Up", "Up", "Up", "Down"], 800);
    await c.key("Select");
    await c.waitFor(/\[account\] signOut/);
    const saved = Number((await c.waitFor(/\[screen\] StartScreen ready homes=\s*(\d+)/, 20000))[1]);
    demo.name = demo.names.ar;
    cloud.state.homeName = demo.name;
    await signIn(c, "ar", saved);
    await screens(c, "ar");
  } finally {
    await c.stop();
  }
});
