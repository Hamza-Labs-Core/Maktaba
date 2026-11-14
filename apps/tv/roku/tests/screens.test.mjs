// Story 58.12 T03: the channel on brs-engine's SceneGraph, driven by the remote's keys against a
// mock home and cloud (tests/mockhome.mjs). Sign in by cloud code, Home within the launch
// budget, the five library tabs with Play and Shuffle, the letter rail, the series page, Back's
// three steps and the one exit confirmation, deep links at launch and while running, and the
// same screens in Arabic, mirrored.
//
// Focus is read from the channel's own log ("[focus] …"), so a focus check never depends on
// pixels. Screenshots (dark, en and ar) go to build/screens and are compared, at a quarter of
// their size, with the references in tests/screens/ by mean pixel difference: brs-engine is
// not Roku's renderer, so the tolerance is loose and the references show layout and
// direction, not Roku's exact pixels (real-device rows: docs/stores/roku.md).
// UPDATE_SCREENS=1 rewrites the references.

import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync, mkdirSync } from "node:fs";
import { createRequire } from "node:module";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { after, before, test } from "node:test";

import { build } from "../tools/build.mjs";
import { LOGS, ROKU } from "./brs.mjs";
import { HIDDEN, LATE, REFUSED, SERIES, startCloud, startHome, videos } from "./mockhome.mjs";
import { Channel, SCREENS } from "./sg.mjs";

const require = createRequire(import.meta.url);
const { createCanvas, loadImage } = require("canvas");
const REFS = join(ROKU, "tests", "screens");
/** Mean absolute difference per channel (0–255) a screen may differ from its reference by. */
const TOLERANCE = 12;
/** Roku's certification budget for the home screen, in ms from launch. */
const HOME_BUDGET_MS = 15000;

const requests = [];
let home;
let cloud;
let zip;
let data;
let tlsDir;
const compared = [];

before(async () => {
  // a certificate for localhost, made for this run and trusted only by the channel's process
  // (NODE_EXTRA_CA_CERTS, read by brs-engine's roUrlTransfer): the home's https name, which the
  // channel's certificate check proves, as a relay's or a home's direct name is on a Roku
  tlsDir = mkdtempSync(join(tmpdir(), "maktaba-roku-tls-"));
  execFileSync("openssl", ["req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1", "-subj", "/CN=localhost", "-addext", "subjectAltName=DNS:localhost", "-keyout", join(tlsDir, "key.pem"), "-out", join(tlsDir, "cert.pem")], { stdio: "ignore" });
  process.env.NODE_EXTRA_CA_CERTS = join(tlsDir, "cert.pem");
  home = await startHome((l) => requests.push(l), { key: readFileSync(join(tlsDir, "key.pem")), cert: readFileSync(join(tlsDir, "cert.pem")) });
  cloud = await startCloud((l) => requests.push(l), home.httpsPort);
  zip = await build(["--test", "--cloud", `http://127.0.0.1:${cloud.port}`, "--probe-ip", "127.0.0.9", "--probe-port", String(home.port), "--out", join(ROKU, "build", "maktaba-roku-test.zip")]);
  data = mkdtempSync(join(tmpdir(), "maktaba-roku-"));
});

after(() => {
  mkdirSync(LOGS, { recursive: true });
  writeFileSync(join(LOGS, "mock-home-requests.log"), requests.join("\n") + "\n");
  writeFileSync(join(LOGS, "screens-compared.log"), compared.join("\n") + "\n");
  home?.server.close();
  cloud?.server.close();
  if (tlsDir) rmSync(tlsDir, { recursive: true, force: true });
});

async function small(path) {
  const img = await loadImage(readFileSync(path));
  const c = createCanvas(480, 270);
  c.getContext("2d").drawImage(img, 0, 0, 480, 270);
  return c;
}

/** Compares a screen with its reference (written when missing or with UPDATE_SCREENS=1). */
async function compare(c, name, settle) {
  const path = await c.shot(name, settle);
  const now = await small(path);
  const ref = join(REFS, `${name}.png`);
  if (process.env.UPDATE_SCREENS === "1" || !existsSync(ref)) {
    mkdirSync(REFS, { recursive: true });
    writeFileSync(ref, now.toBuffer("image/png"));
    compared.push(`${name}: reference written`);
    if (process.env.CI) assert.fail(`${name}: no reference in tests/screens (run with UPDATE_SCREENS=1 and commit it)`);
    return;
  }
  const a = now.getContext("2d").getImageData(0, 0, 480, 270).data;
  const b = (await small(ref)).getContext("2d").getImageData(0, 0, 480, 270).data;
  let sum = 0;
  for (let i = 0; i < a.length; i += 4) sum += Math.abs(a[i] - b[i]) + Math.abs(a[i + 1] - b[i + 1]) + Math.abs(a[i + 2] - b[i + 2]);
  const mean = sum / ((a.length / 4) * 3);
  compared.push(`${name}: mean difference ${mean.toFixed(2)} (tolerance ${TOLERANCE})`);
  assert.ok(mean <= TOLERANCE, `${name} differs from tests/screens/${name}.png by ${mean.toFixed(2)} (tolerance ${TOLERANCE}); see build/screens/${name}.png`);
}

test("T03: sign in by cloud code, browse in English, deep links, Back, then Arabic", { timeout: 600000 }, async () => {
  const c = await Channel.start("main", zip, data);
  try {
    // a new TV starts on Choose your home, with Sign in with a code first
    await c.waitFor(/\[screen\] StartScreen ready homes=\s*0/, 30000);
    await compare(c, "start-en");
    await c.key("Select");
    // the QR of the cloud's verify_url (maktaba.tv/tv?c=…), encoded in the Task and drawn on
    // the device: a version 3 symbol, 29 modules a side; no image is asked of anyone
    await c.waitFor(/\[qr\] encoded\s+32 bytes into\s+29 modules/, 20000);
    await c.waitFor(/\[qr\] drawn\s+29 modules at\s+11 px/);
    await c.waitFor(/\[screen\] CodeScreen ready mode=cloud/);
    assert.ok(!requests.some((l) => l.includes("qr.png")), "the QR is drawn, never fetched");
    await compare(c, "code-en");
    // the phone approves (the mock cloud, on its third poll); the grant goes to the home's relay
    // (an https name), never to the pinned local_ip a Roku cannot verify, and the plain LAN
    // address, where another machine answers listing the home's relay, and the raw-IP direct
    // addresses are never contacted
    await c.waitFor(/\[code\] signed in through relay\b/, 20000);
    assert.equal(cloud.state.trapConnections, 0, "the channel contacted a plain or raw-IP address");
    await c.waitFor(/\[screen\] HomeScreen ready rows=\s*2/, 20000);
    // the first call met an expired token: one refresh, one retry, no sign-out
    assert.ok(requests.some((l) => l.startsWith("home POST /api/auth/refresh")), "the 401 took a refresh");
    assert.ok(!requests.some((l) => l.includes("127.0.0.1:8443")), "the pinned local_ip was never tried");
    assert.ok([...home.state.clientHeaders].some((h) => /^tv\/\d+\.\d+\.\d+ \(roku; tv\)$/.test(h)), "the client header names the channel");
    await c.waitFor(/\[home\] for you\s+8/, 20000);
    await compare(c, "home-en");

    // Home: the rows move by the remote; Up reaches the menu
    await c.key("Right");
    await c.waitFor(/\[focus\] home row\s+0 item\s+1/);
    await c.key("Down");
    await c.waitFor(/\[focus\] home row\s+1 item/);
    // Back from deep in a row goes to its start, then asks once to leave
    await c.key("Back");
    await c.waitFor(/\[back\] home top/);
    await c.key("Back");
    await c.waitFor(/\[back\] home confirm_exit/);
    await c.waitFor(/\[nav\] confirm exit/);
    await compare(c, "exit-en");
    await c.key("Back"); // stay
    await c.waitFor(/\[focus\] confirm\s+0|confirm exit/, 2000).catch(() => {});

    // * on a card opens its actions; Go to the series opens the series page
    await c.key("Info");
    await c.waitFor(/\[actions\] play,over,series,open,cancel/);
    await compare(c, "actions-en");
    await c.keys(["Down", "Down"]);
    await c.key("Select");
    await c.waitFor(/\[series\] season\s+0 episodes\s+6/);
    await c.waitFor(/\[screen\] SeriesScreen ready seasons=\s*2/);
    await compare(c, "series-en");
    await c.key("Down");
    await c.waitFor(/\[focus\] series seasons/);
    await c.key("Right");
    await c.waitFor(/\[series\] season\s+1 episodes\s+6/);
    await c.key("Down");
    await c.waitFor(/\[focus\] series episodes/);
    await c.key("Right");
    await c.waitFor(/\[focus\] series episode\s+1/);
    await c.key("Back");
    await c.waitFor(/\[back\] series leave/);
    await c.waitFor(/\[nav\] back to HomeScreen/);

    // the library: menu → Libraries → the first library, on its Recommended tab
    await c.key("Up");
    await c.waitFor(/\[focus\] home menu\s+0/);
    await c.key("Right");
    await c.key("Select");
    await c.waitFor(/\[screen\] LibrariesScreen ready libraries=\s*2/);
    await c.key("Select");
    await c.waitFor(/\[screen\] LibraryScreen ready tab=recommended rows=\s*3/);
    await compare(c, "library-recommended-en");
    // the tab bar: Right moves to Library (the grid and its letter rail)
    await c.key("Right");
    await c.waitFor(/\[screen\] LibraryScreen ready tab=library items=\s*36/);
    await c.waitFor(/\[grid\] posters loaded\s+36 of\s+36/);
    await c.key("Down");
    await c.waitFor(/\[focus\] library buttons tab=library/);
    // Shuffle starts the server queue with the library's view
    await c.key("Right");
    await c.key("Select");
    await c.waitFor(/\[screen\] PlayerScreen ready/);
    assert.ok(requests.some((l) => l === "home queue source=library shuffle=true"), "Shuffle sent the library queue");
    assert.ok(requests.some((l) => /home session video=\S+ profile=roku/.test(l)), "the stream session asks for the roku profile");
    // story 58.13: the queue's first video is 12 s long, so Up next shows at once with the
    // queue's second; OK plays it, moving the server queue
    await c.waitFor(new RegExp(`\\[upnext\\] shown ${videos[1].id}`));
    await compare(c, "player-upnext-en");
    await c.key("Select");
    await c.waitFor(/\[player\] next /);
    await c.waitFor(/\[screen\] PlayerScreen ready/);
    assert.ok(requests.some((l) => l === "home queue move pos=1 version=1"), "Up next moved the server queue");
    assert.ok(requests.some((l) => /^home close s\d+$/.test(l)), "the first video's session closed");
    assert.ok(requests.some((l) => new RegExp(`^home session video=${videos[1].id} profile=roku`).test(l)), "the second video opened");
    await c.key("Back");
    await c.waitFor(/\[nav\] back to LibraryScreen/);
    await c.key("Down");
    await c.waitFor(/\[focus\] library content tab=library/);
    await compare(c, "library-en");
    // down the grid: pages load as focus nears the end (36 a page, 80 in all), and only the
    // focused page and one each side keep their posters
    // to the last row of two pages (66), then the third page (asked for as focus nears the end)
    // before going on: keys must not outrun the home's answer
    for (let i = 0; i < 11; i++) await c.key("Down");
    await c.waitFor(/\[focus\] library grid\s+66\b/, 10000);
    await c.waitFor(/\[grid\] posters loaded\s+\d+ of\s+80/, 10000);
    for (let i = 0; i < 2; i++) await c.key("Down");
    await c.waitFor(/\[focus\] library grid\s+7\d/, 10000);
    const loads = [...c.out.matchAll(/\[grid\] posters loaded\s+(\d+) of\s+(\d+)/g)].map((m) => [Number(m[1]), Number(m[2])]);
    assert.ok(loads.some(([, total]) => total === 80), "the third page loaded");
    assert.ok(Math.max(...loads.map(([n]) => n)) <= 3 * 36, "at most three pages of posters");
    assert.ok(loads.some(([n, total]) => total === 80 && n < total), "the first page let its posters go");
    // Back: the tab's top, then the tabs
    await c.key("Back");
    await c.waitFor(/\[back\] library top/);
    // the letter rail sits beside the grid's last column
    await c.keys(["Right", "Right", "Right", "Right", "Right", "Right"]);
    await c.waitFor(/\[focus\] library rail tab=library/);
    await c.key("Down");
    await c.key("Select");
    await c.waitFor(/\[library\] letter ر/);
    await c.key("Left");
    await c.waitFor(/\[focus\] library content tab=library/);
    await c.key("Back");
    await c.waitFor(/\[back\] library tabs/);
    await c.waitFor(/\[focus\] library tabs tab=library/);
    // Collections, Playlists, Categories
    await c.key("Right");
    await c.waitFor(/\[screen\] LibraryScreen ready tab=collection items=/);
    await c.key("Right");
    await c.waitFor(/\[screen\] LibraryScreen ready tab=playlist items=/);
    await c.key("Down");
    await c.key("Select");
    await c.waitFor(/\[screen\] ItemsScreen ready items=\s*3/);
    await c.key("Back");
    await c.waitFor(/\[nav\] back to LibraryScreen/);
    await c.key("Up");
    await c.key("Right");
    await c.waitFor(/\[screen\] LibraryScreen ready tab=categories items=\s*4/);
    await c.key("Down");
    await c.key("Select");
    await c.waitFor(/\[library\] category speaker=s1/);
    await c.waitFor(/\[screen\] LibraryScreen ready tab=library items=\s*7/);
    // Back from the grid's first row goes to the tabs, and from the tabs leaves the library
    await c.key("Back");
    await c.waitFor(/\[back\] library tabs/);
    await c.key("Back");
    await c.waitFor(/\[back\] library leave/);
    await c.waitFor(/\[nav\] back to LibrariesScreen/, 5000);
    await c.key("Back");
    await c.waitFor(/\[nav\] back to HomeScreen/);

    // a deep link while running (voice, Roku search) to a video this viewer can watch plays it
    await c.input({ contentId: videos[10].id, mediaType: "movie" });
    await c.waitFor(/\[link\] play/);
    await c.waitFor(/\[screen\] PlayerScreen ready mode=direct format=mkv/);
    // story 58.13: the player, its session carrying the roku profile and device_caps
    const session = requests.findLast((l) => l.startsWith(`home session video=${videos[10].id}`));
    assert.match(session, /profile=roku .*caps=/, "the session carries the roku profile and device_caps");
    // the trickplay index and the subtitle list load beside the session, in any order
    assert.match(c.out, /\[player\] trickplay\s+25 tiles/);
    assert.match(c.out, /\[subs\] off/);
    await compare(c, "player-en");
    // the subtitle picker: Off, then each track in its own language
    await c.key("Down");
    await c.waitFor(/\[focus\] player audio/);
    await c.key("Right");
    await c.waitFor(/\[focus\] player subtitles/);
    await c.key("Select");
    await c.waitFor(/\[picker\] subtitles\s+3/);
    await compare(c, "player-picker-en");
    // Arabic: the sidecar WebVTT, signed with the session's stream_sig, drawn in the channel's
    // own layer (Roku's renderer off), joined and right to left
    await c.keys(["Down", "Down", "Select"]);
    await c.waitFor(/\[picker\] subtitles ar/);
    await c.waitFor(/\[subs\] own ar/);
    await c.waitFor(/\[subs\] cues\s+1/);
    await c.waitFor(/\[subs\] cue at\s+0/);
    assert.ok(requests.some((l) => l === "home subtitles ar signed"), "the Arabic sidecar was fetched with the stream signature");
    await c.key("Up");
    await c.waitFor(/\[focus\] player bar/);
    await c.waitFor(/\[player\] overlay hidden/, 10000);
    await compare(c, "player-subtitles-ar");
    // seeking: the thumbnail from the home's trickplay sheet, then one seek once the keys stop
    await c.key("Right", 200);
    await c.waitFor(/\[seek\]\s+10/);
    await compare(c, "player-seek-en", 100);
    // English goes to Roku's renderer
    await c.key("Down");
    await c.key("Right");
    await c.key("Select");
    await c.waitFor(/\[picker\] subtitles\s+3/);
    await c.keys(["Up", "Select"]); // the list opens on the track showing (Arabic, the last)
    await c.waitFor(/\[subs\] roku en/);
    // Back: the position goes to the home and the session closes
    await c.key("Up");
    await c.key("Back");
    await c.waitFor(/\[player\] back/);
    await c.waitFor(/\[nav\] back to HomeScreen/);
    await new Promise((r) => setTimeout(r, 1500));
    assert.ok(requests.some((l) => /^home progress s\d+ pos=10 completed=false$/.test(l)), "progress after the seek");
    assert.ok(requests.some((l) => /^home close s\d+$/.test(l)), "the session closed on Back");
    // one it cannot (missing, another library, hidden: the home answers 404) opens Home with
    // the not-available line
    await c.input({ contentId: HIDDEN, mediaType: "episode" });
    await c.waitFor(/\[link\] not_available/);
    await c.waitFor(/\[home\] notice roku\.notAvailable/);
    await c.waitFor(/\[home\] for you/, 20000);
    await compare(c, "home-not-available-en");

    // Account → العربية: every screen from here is Arabic and mirrored
    await c.key("Up");
    await c.keys(["Right", "Right", "Right"]);
    await c.key("Select");
    await c.waitFor(/\[screen\] AccountScreen ready lang=en/);
    await compare(c, "account-en");
    await c.keys(["Down", "Down", "Down"]);
    await c.key("Select");
    await c.waitFor(/\[account\] ar/);
    await c.waitFor(/\[screen\] AccountScreen ready lang=ar/);
    await compare(c, "account-ar");
    await c.key("Back");
    await c.waitFor(/\[nav\] back to HomeScreen/);
    await compare(c, "home-ar");
    // in Arabic the first card is the right-most, and Left moves forward
    await c.key("Left");
    await c.waitFor(/\[focus\] home row\s+0 item\s+\d+/);
    await c.key("Up");
    await c.waitFor(/\[focus\] home menu\s+0/);
    await c.key("Left");
    await c.key("Select");
    await c.waitFor(/\[screen\] LibrariesScreen ready/);
    await c.key("Select");
    await c.waitFor(/\[screen\] LibraryScreen ready tab=recommended/);
    await compare(c, "library-recommended-ar");
    await c.key("Left");
    await c.waitFor(/\[screen\] LibraryScreen ready tab=library items=\s*36/);
    await c.keys(["Down", "Down"]);
    await c.waitFor(/\[focus\] library content tab=library/);
    await compare(c, "library-ar");
    await c.keys(["Back", "Back"]);
    await c.waitFor(/\[back\] library leave/);
    await c.waitFor(/\[nav\] back to LibrariesScreen/, 5000);
    await c.key("Back");
    await c.waitFor(/\[nav\] back to HomeScreen/);
    // back on Home, focus is where it was left (the menu); Down reaches the cards
    await c.key("Down");
    await c.key("Info");
    await c.waitFor(/\[actions\]/);
    await c.keys(["Down", "Down"]);
    await c.key("Select");
    await c.waitFor(/\[screen\] SeriesScreen ready/);
    await compare(c, "series-ar");

    // story 58.13: the player in Arabic, its overlay mirrored, and its subtitle picker
    await c.input({ contentId: videos[10].id, mediaType: "movie" });
    await c.waitFor(/\[screen\] PlayerScreen ready/);
    await compare(c, "player-ar");
    await c.key("Down");
    await c.waitFor(/\[focus\] player audio/);
    await c.key("Left"); // in Arabic Left moves forward, to Subtitles
    await c.waitFor(/\[focus\] player subtitles/);
    await c.key("Select");
    await c.waitFor(/\[picker\] subtitles\s+3/);
    await compare(c, "player-picker-ar");
  } finally {
    await c.stop();
  }
});

test("T03: the next launch opens the saved home within the budget, and a launch deep link plays", { timeout: 120000 }, async () => {
  let c = await Channel.start("relaunch", zip, data);
  try {
    const drawn = await c.waitFor(/\[perf\] HomeScreen drawn\s+(\d+) ms after launch/, 30000);
    const ms = Number(drawn[1]);
    writeFileSync(join(LOGS, "home-drawn-ms.txt"), `${ms}\n`);
    assert.ok(ms < HOME_BUDGET_MS, `Home drawn ${ms} ms after launch (budget ${HOME_BUDGET_MS})`);
  } finally {
    await c.stop();
  }
  c = await Channel.start("deeplink", zip, data, `contentId=${videos[5].id},mediaType=episode`);
  try {
    await c.waitFor(/\[link\] play/, 30000);
    await c.waitFor(/\[screen\] PlayerScreen ready/);
  } finally {
    await c.stop();
  }
  c = await Channel.start("deeplink-missing", zip, data, `contentId=${HIDDEN},mediaType=movie`);
  try {
    await c.waitFor(/\[link\] not_available/, 30000);
    await c.waitFor(/\[home\] notice roku\.notAvailable/);
  } finally {
    await c.stop();
  }
});

test("T03 (58.13): a stream session the home refuses shows the error with its reason, and Back", { timeout: 120000 }, async () => {
  // the main test left the channel in Arabic: the error is drawn in Arabic
  const c = await Channel.start("player-refused", zip, data, `contentId=${REFUSED},mediaType=movie`);
  try {
    await c.waitFor(/\[player\] failed reason=Every transcode slot is taken\./, 30000);
    await c.waitFor(/\[screen\] PlayerScreen failed/);
    await compare(c, "player-failed-ar");
    await c.key("Select");
    await c.waitFor(/\[nav\] back to HomeScreen/);
  } finally {
    await c.stop();
  }
});

test("T04 (58.14): a restricted child: the blocked page asks a parent and a yes opens the video; Account; a deep link", { timeout: 180000 }, async () => {
  // the main test left the channel in Arabic: the blocked page is drawn in Arabic first
  home.state.restricted = true;
  home.state.requests = [];
  const c = await Channel.start("parental", zip, data);
  try {
    await c.waitFor(/\[parental\] limited=true/, 30000);
    await c.waitFor(/\[screen\] HomeScreen ready/, 30000);
    await c.waitFor(/\[home\] for you/, 20000);
    // a deep link to the hidden video: the not-available line, nothing more
    await c.input({ contentId: LATE, mediaType: "movie" });
    await c.waitFor(/\[link\] not_available/);
    await c.waitFor(/\[home\] notice roku\.notAvailable/);
    assert.ok(!requests.some((l) => l.startsWith("home ask")), "a deep link asks nothing");
    await c.waitFor(/\[home\] for you/, 20000);
    // Continue watching's first card is the hidden video: * → Open shows the blocked page
    // (Up and Down: the reset Home's cards take the remote's focus, on its first card)
    await c.key("Up");
    await c.waitFor(/\[focus\] home menu/);
    await c.key("Down");
    await c.waitFor(/\[focus\] home row\s+0 item/);
    await c.key("Info");
    await c.waitFor(/\[actions\] play,over,open,cancel|\[actions\] [a-z,]*open/);
    await c.keys(["Down", "Down"]);
    await c.key("Select");
    await c.waitFor(/\[screen\] BlockedScreen ready kind=video restricted=true/, 20000);
    await c.waitFor(/\[focus\] blocked none/);
    await compare(c, "blocked-ar");
    // OK asks, with the id the screen had
    await c.key("Select");
    await c.waitFor(/\[parental\] asked/);
    assert.ok(requests.some((l) => l === `home ask kind=video id=${LATE}`), "the request names the video");
    await c.waitFor(/\[focus\] blocked pending/);
    await compare(c, "blocked-asked-ar");
    // the parent approves from their phone: the next read opens the video, no restart
    home.state.requests[0].status = "approved";
    await c.waitFor(/\[parental\] approved video/, 30000);
    await c.waitFor(/\[screen\] VideoScreen ready/, 20000);
    // Back goes to Home: the blocked page is gone from the stack
    await c.key("Back");
    await c.waitFor(/\[nav\] back to HomeScreen/);

    // Account: a parent manages this account, its limit
    await c.key("Up");
    await c.waitFor(/\[focus\] home menu/);
    await c.keys(["Left", "Left", "Left"]);
    await c.key("Select");
    await c.waitFor(/\[screen\] AccountScreen ready lang=ar managed=true/);
    await compare(c, "account-managed-ar");
    // English: the same lines, left to right
    await c.keys(["Down", "Down"]);
    await c.key("Select");
    await c.waitFor(/\[account\] en/);
    await c.waitFor(/\[screen\] AccountScreen ready lang=en managed=true/);
    await compare(c, "account-managed-en");
  } finally {
    await c.stop();
    home.state.restricted = false;
  }
});

test("T05 (58.14): the player stops for the home's parental answers: time's up, Ask for more time, a title hidden mid-play", { timeout: 300000 }, async () => {
  // T04 left the channel in English. The mock home answers as the real one: 403 parental_time
  // on the open and on the heartbeat once the time is over, 404 for a video hidden mid-play
  home.state.restricted = true;
  home.state.requests = [];
  const later = () => new Date(Date.now() + 2 * 3600e3).toISOString();
  const c = await Channel.start("player-parental", zip, data);
  try {
    await c.waitFor(/\[parental\] limited=true/, 30000);
    await c.waitFor(/\[home\] for you/, 30000);

    // the time is over before the video opens: the open is refused, and the page says why
    home.state.time = { reason: "limit_reached", next: later() };
    const v = videos[12];
    await c.input({ contentId: v.id, mediaType: "movie" });
    await c.waitFor(/\[link\] play/);
    await c.waitFor(/\[screen\] PlayerScreen timesup reason=limit_reached/, 20000);
    assert.ok(requests.some((l) => l.startsWith(`home session video=${v.id} profile=roku start=600`)), "the open asked for the saved place");
    // the main action has the remote's focus: the first minutes to ask for
    await c.waitFor(/\[focus\] timesup tuAsk0/);
    assert.doesNotMatch(c.out, /\[player\] failed/, "never the player's raw error");
    await compare(c, "player-timesup-en");
    // Right moves along the row; OK asks for 30 minutes
    await c.key("Right");
    await c.waitFor(/\[focus\] timesup tuAsk1/);
    await c.key("Select");
    await c.waitFor(/\[parental\] asked time/);
    assert.ok(requests.some((l) => l === "home ask kind=time id=30"), "the request asks for 30 minutes");
    // once asked, the minute buttons go and Check again takes the focus
    await c.waitFor(/\[focus\] timesup tuCheck/);
    await compare(c, "player-timesup-asked-en");
    // Check again asks the home, which still says no: the page stays
    const opens = requests.filter((l) => l.startsWith(`home session video=${v.id}`)).length;
    await c.key("Select");
    await c.waitFor(/\[parental\] check again at\s+600/);
    await c.waitFor(/\[screen\] PlayerScreen timesup reason=limit_reached/, 10000);
    assert.equal(requests.filter((l) => l.startsWith(`home session video=${v.id}`)).length, opens + 1, "Check again opened once more");
    // the parent says yes from their phone: the next read opens the video again, where it stopped
    home.state.requests[0].status = "approved";
    await c.waitFor(/\[parental\] approved time/, 30000);
    await c.waitFor(/\[parental\] times up closed/, 10000);
    await c.waitFor(/\[screen\] PlayerScreen ready/);

    // the home ends the time while it plays (watching hours): the heartbeat after a seek is
    // refused, the video stops and its session closes (brs-engine's Video node stays at 0, so a
    // seek of 10 s lands at 10)
    home.state.requests = [];
    home.state.time = { reason: "outside_slot", next: later() };
    await c.key("Right", 200);
    await c.waitFor(/\[seek\] to\s+10\b/);
    await c.waitFor(/\[screen\] PlayerScreen timesup reason=outside_slot/, 10000);
    await c.waitFor(/\[focus\] timesup tuAsk0/);
    assert.ok(requests.some((l) => /^home progress s\d+ pos=10 completed=false$/.test(l)), "the refused heartbeat carried the position");
    await new Promise((r) => setTimeout(r, 500));
    const last = requests.filter((l) => /^home session video=/.test(l)).length;
    assert.ok(requests.some((l, i) => /^home close s\d+$/.test(l) && i > requests.findLastIndex((x) => x === "home time over")), "the stopped stream's session closed");
    // nothing asks the home again by itself
    await new Promise((r) => setTimeout(r, 2000));
    assert.equal(requests.filter((l) => /^home session video=/.test(l)).length, last, "no open by itself");
    await c.key("Back");
    await c.waitFor(/\[nav\] back to HomeScreen/);

    // a video hidden by the limit while it plays: "This isn't available", that it stopped, and
    // Ask a parent; a yes plays it again from where it stopped
    home.state.time = null;
    const w = videos[16];
    await c.input({ contentId: w.id, mediaType: "movie" });
    await c.waitFor(/\[screen\] PlayerScreen ready/, 20000);
    home.state.hiddenNow.add(w.id);
    await c.key("Right", 200);
    await c.waitFor(/\[parental\] the home ended the stream/, 10000);
    await c.waitFor(/\[parental\] blocked stopped=true at=\s*10\b/);
    await c.waitFor(/\[screen\] BlockedScreen ready kind=video restricted=true/, 10000);
    await c.waitFor(/\[focus\] blocked none/);
    await compare(c, "player-blocked-en");
    await c.key("Select");
    await c.waitFor(/\[parental\] asked/);
    assert.ok(requests.some((l) => l === `home ask kind=video id=${w.id}`), "the request names the video");
    home.state.requests[0].status = "approved";
    await c.waitFor(/\[parental\] approved video/, 30000);
    await c.waitFor(/\[screen\] PlayerScreen ready/, 20000);
    assert.ok(requests.some((l) => l.startsWith(`home session video=${w.id} profile=roku start=10 `)), "the yes played it again from where it stopped");
    // Back goes Home: neither the blocked page nor the first player is left on the stack
    await c.key("Back");
    await c.waitFor(/\[nav\] back to HomeScreen/);

    // Arabic: the same pages, the button row mirrored (the first minutes at the right)
    await c.key("Up");
    await c.waitFor(/\[focus\] home menu/);
    await c.keys(["Right", "Right", "Right"]);
    await c.key("Select");
    await c.waitFor(/\[screen\] AccountScreen ready lang=en managed=true/);
    await c.keys(["Down", "Down", "Down"]);
    await c.key("Select");
    await c.waitFor(/\[account\] ar/);
    await c.waitFor(/\[screen\] AccountScreen ready lang=ar/);
    await c.key("Back");
    await c.waitFor(/\[nav\] back to HomeScreen/);
    await c.waitFor(/\[home\] for you/, 20000);
    home.state.requests = [];
    home.state.time = { reason: "limit_reached", next: later() };
    await c.input({ contentId: v.id, mediaType: "movie" });
    await c.waitFor(/\[screen\] PlayerScreen timesup reason=limit_reached/, 20000);
    await c.waitFor(/\[focus\] timesup tuAsk0/);
    await compare(c, "player-timesup-ar");
    // in Arabic Left moves forward
    await c.key("Left");
    await c.waitFor(/\[focus\] timesup tuAsk1/);
    await c.key("Back");
    await c.waitFor(/\[nav\] back to HomeScreen/);
    home.state.time = null;
    const x = videos[20];
    await c.input({ contentId: x.id, mediaType: "movie" });
    await c.waitFor(/\[screen\] PlayerScreen ready/, 20000);
    home.state.hiddenNow.add(x.id);
    await c.key("Left", 200);
    await c.waitFor(/\[screen\] BlockedScreen ready kind=video restricted=true/, 10000);
    await c.waitFor(/\[focus\] blocked none/);
    await compare(c, "player-blocked-ar");
  } finally {
    await c.stop();
    home.state.restricted = false;
    home.state.time = null;
    home.state.hiddenNow.clear();
    home.state.requests = [];
  }
});

// The probe walks the whole /24; brs-engine's roUrlTransfer is synchronous, so each address
// that does not answer waits for its connection to fail. Linux refuses loopback addresses
// (127.0.0.2…) at once; macOS has no such addresses and waits out each one.
const PROBE_SKIP = process.platform !== "linux" && "the /24 probe runs on Linux only (macOS has no 127.0.0.x loopback addresses)";

test("T03: Find on this network lists the home and signs in by the home's own code", { timeout: 180000, skip: PROBE_SKIP }, async () => {
  const fresh = mkdtempSync(join(tmpdir(), "maktaba-roku-"));
  const c = await Channel.start("find", zip, fresh);
  try {
    await c.waitFor(/\[screen\] StartScreen ready homes=\s*0/, 30000);
    await c.key("Down");
    await c.key("Select");
    await c.waitFor(/\[probe\] home h1test at http:\/\/127\.0\.0\.1:\d+/, 30000);
    await c.waitFor(/\[screen\] FindScreen ready found=\s*1/, 120000);
    await compare(c, "find-en");
    await c.key("Select");
    await c.waitFor(/\[home\] h1test: plain HTTP on the LAN/, 10000);
    // the home's own code carries its approve page (/pair?code=…) as a QR too
    await c.waitFor(/\[qr\] encoded\s+\d+ bytes into\s+\d+ modules/, 20000);
    await c.waitFor(/\[screen\] CodeScreen ready mode=home/);
    await c.waitFor(/\[screen\] HomeScreen ready/, 20000);
  } finally {
    await c.stop();
  }
});
