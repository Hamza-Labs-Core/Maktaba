// Story 60.11: the download page's data and cards (download.mjs), offline, from a saved Releases
// API answer (fixtures/release/, v0.3.135's, with its own checksums.txt). T01 (the cards, with
// the fixture's names, sizes and digests), T02 (a failed Windows leg), T03 (a public listing),
// T04 (a digest that differs from checksums.txt fails), T05 (the package repositories), the
// edge cases (a pre-release, an unreachable API) and the copy: every key a card can need, in
// both languages. `node --test`; the built pages are src/site.test.ts's.
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { test } from "node:test";
import { fileURLToPath } from "node:url";
import {
  cards,
  compareTags,
  cosignCommand,
  downloadPages,
  fileSize,
  FIXTURE,
  LISTINGS,
  packageRows,
  parseChecksums,
  plain,
  readCopy,
  readManifest,
  readPlatforms,
  readRelease,
  releaseData,
  releaseDate,
  renderBody,
  styleFile,
  tapName,
} from "./download.mjs";

const HERE = path.dirname(fileURLToPath(import.meta.url));
const PKG = path.resolve(HERE, "..");
const RAW = JSON.parse(fs.readFileSync(path.join(FIXTURE, "release.json"), "utf8"));
const SUMS = fs.readFileSync(path.join(FIXTURE, "checksums.txt"), "utf8");
const clone = () => structuredClone(RAW);
const data = (release = RAW, checksums = SUMS, opts) => releaseData({ release, checksums }, opts);
const NO_ENV = {};
const byId = (ks) => Object.fromEntries(ks.map((k) => [k.id, k]));
const asset = (name) => RAW.assets.find((a) => a.name === name);

test("T01: the fixture's release makes the Mac, Windows, Linux and NAS cards, and no other", () => {
  const ks = cards(data(), readPlatforms(), NO_ENV);
  assert.deepEqual(
    ks.map((k) => k.id),
    ["mac", "windows", "linux", "nas"]
  );
  const k = byId(ks);
  // the primary files, by pattern, with the API's names, sizes and digests
  for (const [id, name] of [
    ["mac", "Maktaba_0.3.135_aarch64.dmg"],
    ["windows", "Maktaba_0.3.135_x64-setup.exe"],
    ["linux", "Maktaba_0.3.135_amd64.AppImage"],
  ]) {
    const a = asset(name);
    assert.equal(k[id].primary.name, name);
    assert.equal(k[id].primary.size, a.size);
    assert.equal(`sha256:${k[id].primary.sha256}`, a.digest);
    assert.equal(k[id].primary.url, a.browser_download_url);
  }
  assert.equal(k.nas.primary, null);
  assert.equal(k.nas.href, "/install/nas/");
  assert.deepEqual(
    k.linux.others.map((o) => o.asset.name),
    [
      "Maktaba_0.3.135_amd64.deb",
      "Maktaba-0.3.135-1.x86_64.rpm",
      "Maktaba_0.3.135_aarch64.AppImage",
      "maktaba-player_0.3.135_amd64.deb",
      "install.sh",
    ]
  );
  assert.deepEqual(
    k.windows.others.map((o) => o.asset.name),
    ["maktaba-windows-portable-0.3.135.zip", "Maktaba-Player_0.3.135_x64.msi"]
  );
  assert.deepEqual(
    k.nas.others.map((o) => o.asset.name),
    [
      "maktaba-docker-compose.yml",
      "maktaba-synology-compose.yaml",
      "maktaba-truenas-custom-app.yaml",
    ]
  );
  // the desktop installers' updater signatures beside them (the .dmg has none: the app's
  // updater archive carries it)
  assert.equal(k.windows.sig.name, "Maktaba_0.3.135_x64-setup.exe.sig");
  assert.equal(k.linux.sig.name, "Maktaba_0.3.135_amd64.AppImage.sig");
  assert.equal(k.mac.sig, null);
  // the NAS files are in the signed checksums.txt; the desktop installers are not
  assert.ok(k.nas.others.every((o) => o.asset.signed));
  assert.equal(k.mac.primary.signed, false);
});

test("T01: the page shows the fixture's version, date, sizes, digests and links", () => {
  const d = data();
  const ks = cards(d, readPlatforms(), NO_ENV);
  for (const lang of ["en", "ar"]) {
    const html = renderBody(d, ks, { lang, manifest: readManifest() });
    assert.match(html, /data-release="version">[^<]*0\.3\.135/);
    assert.ok(html.includes(releaseDate(RAW.published_at, lang)));
    for (const k of ks.filter((x) => x.primary)) {
      assert.ok(html.includes(`href="${k.primary.url}" download="${k.primary.name}"`), k.id);
      assert.ok(html.includes(`sha256:${k.primary.sha256}`), k.id);
      assert.ok(html.includes(fileSize(k.primary.size, lang)), k.id);
      for (const o of k.others) assert.ok(html.includes(`href="${o.asset.url}"`), o.asset.name);
    }
    // the cosign command names this tag's identity, as the release notes do
    assert.ok(html.includes("release.yml@refs/tags/v0.3.135"));
    // every file of the release, on GitHub
    assert.ok(html.includes(`href="${RAW.html_url}"`));
    // never a phone or TV app, never a package row (Q5, A6, A7)
    for (const id of ["iphone", "appletv", "android", "server"])
      assert.ok(!html.includes(`id="${id}"`), id);
  }
});

test("T02: a release without the Windows installer promotes the portable zip, never a dead button", () => {
  const r = clone();
  r.assets = r.assets.filter((a) => !a.name.endsWith("_x64-setup.exe"));
  const k = byId(cards(data(r), readPlatforms(), NO_ENV));
  assert.equal(k.windows.primary.name, "maktaba-windows-portable-0.3.135.zip");
  assert.deepEqual(
    k.windows.others.map((o) => o.asset.name),
    ["Maktaba-Player_0.3.135_x64.msi"]
  );
  // with neither, the card is gone, though the Player alone is still there
  r.assets = r.assets.filter((a) => !a.name.startsWith("maktaba-windows-portable-"));
  assert.ok(!("windows" in byId(cards(data(r), readPlatforms(), NO_ENV))));
  // and a release whose Linux leg failed has no Linux card
  r.assets = r.assets.filter((a) => !a.name.includes("AppImage"));
  assert.ok(!("linux" in byId(cards(data(r), readPlatforms(), NO_ENV))));
});

test("T02: the NAS card needs the compose file the guides start from", () => {
  const r = clone();
  r.assets = r.assets.filter((a) => a.name !== "maktaba-docker-compose.yml");
  const sums = SUMS.split("\n")
    .filter((l) => !l.endsWith("maktaba-docker-compose.yml"))
    .join("\n");
  assert.ok(!("nas" in byId(cards(data(r, sums), readPlatforms(), NO_ENV))));
});

test("T03: a public Google Play listing shows the Android card with its store link", () => {
  const platforms = readPlatforms().map((p) =>
    p.id === "android"
      ? {
          ...p,
          googlePlay: "https://play.google.com/store/apps/details?id=com.hamzalabs.maktaba.player",
        }
      : p
  );
  const d = data();
  const ks = cards(d, platforms, NO_ENV);
  const k = byId(ks);
  assert.deepEqual(
    k.android.listings.map((l) => l.key),
    ["googlePlay"]
  );
  for (const lang of ["en", "ar"]) {
    const html = renderBody(d, ks, { lang, manifest: readManifest() });
    assert.ok(html.includes('id="android"'));
    assert.ok(
      html.includes(
        'href="https://play.google.com/store/apps/details?id=com.hamzalabs.maktaba.player"'
      )
    );
    assert.ok(html.includes(lang === "ar" ? "التنزيل من Google Play" : "Get it on Google Play"));
    assert.ok(html.includes(lang === "ar" ? "Android وAndroid TV" : "Android and Android TV"));
  }
  // a listing that is not an https address fails the build
  const bad = readPlatforms().map((p) =>
    p.id === "iphone" ? { ...p, appStore: "http://apps.apple.com/x" } : p
  );
  assert.throws(
    () => cards(d, bad, NO_ENV),
    /appStore: http:\/\/apps\.apple\.com\/x is not an https address/
  );
});

test("T03: every listing field downloads.json names is one the page knows", () => {
  for (const p of readPlatforms())
    for (const k of Object.keys(p).filter((k) => /store|testflight|play/i.test(k)))
      assert.ok(LISTINGS.includes(k), `${p.id}.${k}`);
});

test("T04: an API digest that differs from checksums.txt fails the build, naming the file", () => {
  const r = clone();
  const a = r.assets.find((x) => x.name === "maktaba-server-0.3.135-linux-amd64.tar.gz");
  a.digest = `sha256:${"0".repeat(64)}`;
  assert.throws(
    () => data(r),
    /files differ from its signed checksums\.txt:\nmaktaba-server-0\.3\.135-linux-amd64\.tar\.gz: GitHub's sha256:0{64}, checksums\.txt's 1ef67b32/
  );
});

test("T04: the fixture's digests all agree with its checksums.txt", () => {
  const sums = parseChecksums(SUMS);
  assert.equal(sums.size, 106);
  const d = data();
  for (const [name, hex] of sums)
    if (d.assets.has(name)) assert.equal(d.assets.get(name).sha256, hex, name);
});

test("T05: with the package repositories unset, no apt, dnf or brew row and no server card", () => {
  assert.deepEqual(packageRows({}), []);
  assert.ok(!("server" in byId(cards(data(), readPlatforms(), {}))));
});

test("T05: with them set, the server card shows the rows the release notes print", () => {
  const env = {
    MAKTABA_PACKAGES_URL: "https://packages.example",
    HOMEBREW_TAP_REPO: "Hamza-Labs-Core/homebrew-tap",
  };
  const d = data();
  const ks = cards(d, readPlatforms(), env);
  const k = byId(ks);
  assert.deepEqual(
    k.server.packages.map((r) => r.key),
    ["server.apt", "server.dnf", "server.brew"]
  );
  const html = renderBody(d, ks, { lang: "en", manifest: readManifest() });
  // release notes: "(or from the apt repository, which carries every stable release: <url>)"
  // and "brew install hamza-labs-core/tap/maktaba"
  assert.ok(html.includes('href="https://packages.example"'));
  assert.ok(html.includes("brew install hamza-labs-core/tap/maktaba"));
  assert.ok(html.includes("maktaba-server-0.3.135-linux-amd64.tar.gz"));
  // only the tap: the brew row alone
  assert.deepEqual(
    packageRows({ HOMEBREW_TAP_REPO: "Hamza-Labs-Core/homebrew-tap" }).map((r) => r.key),
    ["server.brew"]
  );
  assert.equal(tapName("Hamza-Labs-Core/homebrew-tap"), "hamza-labs-core/tap");
  assert.throws(() => packageRows({ MAKTABA_PACKAGES_URL: "ftp://x" }), /not an https address/);
});

test("a draft, a pre-release or a tag that is not vX.Y.Z is never shown", () => {
  assert.throws(() => data({ ...RAW, prerelease: true }), /pre-release, never shown/);
  assert.throws(() => data({ ...RAW, draft: true }), /pre-release, never shown/);
  assert.throws(() => data({ ...RAW, tag_name: "v0.3.135-rc.1" }), /is not vX\.Y\.Z/);
});

test("a deploy started for a newer tag fails on an older latest release", () => {
  assert.throws(() => data(RAW, SUMS, { expect: "v0.3.136" }), /older than v0\.3\.136/);
  assert.equal(data(RAW, SUMS, { expect: "v0.3.135" }).tag, "v0.3.135");
  assert.equal(data(RAW, SUMS, { expect: "v0.3.99" }).tag, "v0.3.135");
  assert.ok(compareTags("v0.3.10", "v0.3.9") > 0);
});

test("GitHub unreachable or refusing: the build fails, saying why, and never builds an empty page", async () => {
  const down = async () => {
    throw Object.assign(new TypeError("fetch failed"), { cause: { code: "ENOTFOUND" } });
  };
  await assert.rejects(
    readRelease({ env: {}, fetchImpl: down }),
    /could not be read \(ENOTFOUND\)\. The page is built from the latest release/
  );
  const limited = async () => ({ ok: false, status: 403 });
  await assert.rejects(
    readRelease({ env: {}, fetchImpl: limited }),
    /answered 403 \(rate limited: set GITHUB_TOKEN\)/
  );
});

test("the API is read with GITHUB_TOKEN, and checksums.txt from the release", async () => {
  const seen = [];
  const fetchImpl = async (url, opts) => {
    seen.push([url, opts.headers.Authorization]);
    if (url.startsWith("https://api.github.com/")) return { ok: true, json: async () => RAW };
    return { ok: true, text: async () => SUMS };
  };
  const got = await readRelease({ env: { GITHUB_TOKEN: "t0k" }, fetchImpl });
  assert.equal(got.release.tag_name, "v0.3.135");
  assert.equal(seen[0][0], "https://api.github.com/repos/Hamza-Labs-Core/Maktaba/releases/latest");
  assert.equal(seen[0][1], "Bearer t0k");
  assert.equal(seen[1][0], asset("checksums.txt").browser_download_url);
  // and from the fixture folder, offline
  const fx = await readRelease({ env: { WEBSITE_RELEASE_FIXTURE: FIXTURE }, fetchImpl: null });
  assert.equal(fx.release.tag_name, "v0.3.135");
});

test("sizes and dates read as the mockups print them, with Western Arabic numerals", () => {
  assert.equal(fileSize(202578321, "en"), "203 MB");
  assert.equal(fileSize(13648680, "en"), "13.6 MB");
  assert.equal(fileSize(23048, "ar"), "23 كيلوبايت");
  assert.equal(fileSize(6754, "en"), "6.8 KB");
  assert.equal(releaseDate("2026-11-12T06:32:41Z", "en"), "12 November 2026");
  assert.equal(releaseDate("2026-11-12T06:32:41Z", "ar"), "12 نوفمبر 2026");
});

test("downloads.json's requirements lines are copy/download.*.md's, word for word", () => {
  for (const lang of ["en", "ar"]) {
    const c = readCopy(lang);
    for (const p of readPlatforms()) {
      const key = `${p.id}.needs`;
      if (key in c) assert.equal(p.needs[lang], plain(c[key]), `${lang} ${key}`);
      else assert.ok(p.needs[lang], `${lang} ${key}`);
    }
  }
});

test("every card the page can show has its words in both languages, and the keys match", () => {
  const en = readCopy("en");
  const ar = readCopy("ar");
  assert.deepEqual(Object.keys(ar).sort(), Object.keys(en).sort());
  // every platform at once: each listing, the package rows, every file
  const all = readPlatforms().map((p) => ({
    ...p,
    ...Object.fromEntries(LISTINGS.filter((k) => k in p).map((k) => [k, "https://example.com/x"])),
  }));
  const d = data();
  const ks = cards(d, all, {
    MAKTABA_PACKAGES_URL: "https://packages.example",
    HOMEBREW_TAP_REPO: "a/homebrew-b",
  });
  assert.equal(ks.length, readPlatforms().length);
  for (const lang of ["en", "ar"]) {
    const html = renderBody(d, ks, { lang, resolves: () => true, manifest: readManifest() });
    assert.ok(!html.includes("undefined"), lang);
    assert.ok(!/\{(file|version|date)\}/.test(html), lang);
  }
});

test("the pages: the release in a meta tag, the head script with the computer cards, JSON-LD", () => {
  const d = data();
  const ks = cards(d, readPlatforms(), NO_ENV);
  const pages = downloadPages(d, ks, {
    styles: "/assets/download-x.css",
    script: "/assets/download-platform-x.js",
    manifest: readManifest(),
  });
  assert.deepEqual(
    pages.map((p) => p.out),
    ["download/index.html", "ar/download/index.html"]
  );
  for (const p of pages) {
    assert.ok(p.meta.includes('<meta name="maktaba-release" content="v0.3.135" />'));
    assert.ok(
      p.head.includes(
        '<script src="/assets/download-platform-x.js" data-cards="mac windows linux"></script>'
      )
    );
    const ld = JSON.parse(p.head.match(/<script type="application\/ld\+json">(.*?)<\/script>/)[1]);
    assert.equal(ld["@type"], "SoftwareApplication");
    assert.equal(ld.softwareVersion, "0.3.135");
    assert.equal(ld.operatingSystem, "macOS, Windows, Linux, Docker");
    // the head script must run before the page's stylesheet is applied: no async, no defer
    assert.ok(!/download-platform[^>]*(async|defer|type="module")/.test(p.head));
  }
});

test("the page's stylesheet is part B's components and the page's own, logical properties only", () => {
  const s = styleFile();
  assert.match(s.file, /^assets\/download-[\w-]{10}\.css$/);
  assert.ok(s.content.includes(".card.mine"));
  assert.ok(s.content.includes(':root[data-platform="mac"] #mac'));
  assert.ok(fs.existsSync(path.join(PKG, "src/download.css")));
});

test("cosign: the release notes' command for the tag", () => {
  assert.ok(
    cosignCommand("v1.2.3").includes(
      "--certificate-identity https://github.com/Hamza-Labs-Core/Maktaba/.github/workflows/release.yml@refs/tags/v1.2.3 \\"
    )
  );
  const notes = fs.readFileSync(
    path.resolve(PKG, "../../../tools/release/releasenotes/notes.md.tmpl"),
    "utf8"
  );
  for (const line of cosignCommand("{{.SAN}}")
    .split("\n")
    .filter((l) => !l.includes("identity") && !l.includes("issuer")))
    assert.ok(notes.includes(line), line);
});

test("plain(): a copy string's text, no tag left however nested, entities as text", () => {
  assert.equal(plain("Run <code>brew</code> &amp; wait"), "Run brew & wait");
  assert.equal(plain("<scr<script>ipt>alert(1)</script>"), "alert(1)");
  assert.equal(plain("a &lt;b&gt; c"), "a b c");
});
