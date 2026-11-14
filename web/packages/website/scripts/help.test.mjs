// Story 60.12: the help pages from the app's own topics (scripts/help.mjs). T01 (every topic in
// both languages, and the indexes), T02 (a topic without its Arabic file fails the build, naming
// it), T03 (an app route is drawn as "In Maktaba: …"), T04 (a route the app's router does not
// have fails), and what the pages carry. The link check (T05) is links.mjs --dist; the redirect
// (T06) src/worker.test.ts; the desktop menu (T07) web/src/lib/desktop.test.ts; the filter (T08)
// e2e/help.spec.ts.
// Run: node --test web/packages/website/scripts/help.test.mjs
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";
import {
  CONTENT,
  HELP,
  helpPages,
  inline,
  link,
  matchRoute,
  parseBlocks,
  readRoutes,
  readSource,
  relatedOf,
  renderMarkdown,
  sourceFindings,
  T,
} from "./help.mjs";

const PKG = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const REPO = path.resolve(PKG, "../../..");
const src = readSource(REPO);
const routes = readRoutes(REPO);
const ctx = (lang, extra = {}) => ({
  lang,
  topic: "settings",
  routes,
  anchors: (l, id) => (src.topics[l][id] === undefined ? null : ["what-it-is", "faq"]),
  ...extra,
});

test("T01: every topic in both languages, and the two indexes", () => {
  const ids = src.categories.flatMap((c) => c.topics);
  assert.equal(ids.length, 44);
  const pages = helpPages(src);
  assert.equal(pages.length, 90);
  for (const lang of ["en", "ar"]) {
    const prefix = lang === "ar" ? "ar/" : "";
    const own = pages.filter((p) => p.lang === lang);
    assert.equal(own.length, 45, lang);
    assert.ok(
      own.some((p) => p.out === `${prefix}help/index.html`),
      `${lang} index`
    );
    for (const id of ids) {
      const p = own.find((x) => x.out === `${prefix}help/${id}/index.html`);
      assert.ok(p, `${lang}/${id}`);
      assert.equal(p.path, `/help/${id}/`);
      assert.equal(p.variant, "b", "part B's shell");
      assert.match(p.body, new RegExp(`data-topic="${id}"`));
      assert.ok(p.body.includes(T[lang].sameAsApp), "the line that this is the app's help");
    }
  }
});

test("T01: each topic renders its Markdown: anchored headings, lists, tables, code", () => {
  const pages = helpPages(src);
  const page = (out) => pages.find((p) => p.out === out).body;
  const search = page("help/search/index.html");
  assert.match(search, /<h2 id="what-it-is">What it is<\/h2>/);
  assert.match(search, /<ol><li>/);
  assert.match(search, /<nav class="toc" aria-labelledby="toc-title">/);
  assert.match(search, /<p class="lead-p">Find a video/);
  const big = page("ar/help/big-screen/index.html");
  assert.match(big, /<div class="table-wrap"><table><thead><tr><th scope="col">/);
  const storage = page("help/storage/index.html");
  assert.match(storage, /<div class="code">[\s\S]*maktaba-server backups show/);
  // the title is the app's, from its catalog; no page renders the topic's own # heading
  assert.doesNotMatch(search, /<h1>Search and YouTube<\/h1>[\s\S]*<h1>/);
});

test("T01: every heading anchor a topic links to exists (the build would fail)", () => {
  for (const lang of ["en", "ar"])
    for (const id of src.categories.flatMap((c) => c.topics)) {
      const { html } = renderMarkdown(src.topics[lang][id], {
        lang,
        topic: id,
        routes,
        anchors: (l, t) => {
          const md = src.topics[l][t];
          return md === undefined ? null : [...md.matchAll(/\{#([a-z0-9-]+)\}/g)].map((m) => m[1]);
        },
      });
      assert.doesNotMatch(html, /href="\/(ar\/)?(settings|admin|account|queue)\b/, `${lang}/${id}`);
    }
});

test("T02: a topic whose Arabic file is missing fails the build, naming it", () => {
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), "help-t02-"));
  try {
    for (const rel of [
      "web/src/help/topics.ts",
      "web/src/help/content",
      "web/src/i18n/en.json",
      "web/src/i18n/ar.json",
      "web/src/App.tsx",
      "web/src/serverRoutes.tsx",
    ])
      fs.cpSync(path.join(REPO, rel), path.join(tmp, rel), { recursive: true });
    fs.rmSync(path.join(tmp, CONTENT, "ar", "plex.md"));
    const written = [];
    assert.throws(
      () =>
        HELP.build({
          write: (f) => written.push(f),
          repo: tmp,
          resolves: () => false,
          script: () => "/assets/f.js",
        }),
      (e) =>
        /the topic plex has no Arabic file \(web\/src\/help\/content\/ar\/plex\.md\)/.test(
          e.message
        )
    );
    assert.ok(!written.some((f) => f.endsWith(".html")), "no page is written");
  } finally {
    fs.rmSync(tmp, { recursive: true, force: true });
  }
  // and a missing title or summary in the catalog, by key
  const broken = structuredClone(src);
  delete broken.catalog.ar["help.topic.plex.summary"];
  assert.deepEqual(sourceFindings(broken), [
    "help: the topic plex has no Arabic summary (help.topic.plex.summary)",
  ]);
});

test("T03: an app route is drawn as an 'In Maktaba' path, in English and Arabic", () => {
  assert.equal(
    inline("Open [Settings → Privacy](/settings/privacy).", ctx("en")),
    'Open <span class="where">In Maktaba: <strong>Settings → Privacy</strong></span>.'
  );
  assert.equal(
    inline("افتحوا [الإعدادات ← الخصوصية](/settings/privacy).", ctx("ar")),
    'افتحوا <span class="where">في مكتبة: <strong>الإعدادات ← الخصوصية</strong></span>.'
  );
  // the arrow points the page's way, whatever the source wrote
  assert.match(inline("[الإدارة → التحديثات](/admin/updates)", ctx("ar")), /الإدارة ← التحديثات/);
  // the label is the link's own text, its marks kept; a fragment or a query is the same page
  assert.equal(
    link("**Extensions**", "/admin/extensions#plex", ctx("en")),
    '<span class="where">In Maktaba: <strong><strong>Extensions</strong></strong></span>'
  );
  assert.match(link("Favourites", "/videos?favorite=true", ctx("en")), /class="where"/);
  // a page with a :param, and one under a splat
  assert.match(link("A library", "/library/7", ctx("en")), /class="where"/);
  assert.match(link("Appearance", "/settings/appearance", ctx("en")), /class="where"/);
  // the app's root is the home screen, never the site's home
  assert.equal(
    link("Home", "/", ctx("en")),
    '<span class="where">In Maktaba: <strong>the home screen</strong></span>'
  );
  assert.equal(
    link("الرئيسية", "/", ctx("ar")),
    '<span class="where">في مكتبة: <strong>الشاشة الرئيسية</strong></span>'
  );
});

test("T04: a route the app's router does not have fails, naming the topic and the link", () => {
  assert.throws(
    () => link("Nowhere", "/nowhere/at-all", ctx("en")),
    /help: web\/src\/help\/content\/en\/settings\.md: the link \/nowhere\/at-all is not a route of the app's router/
  );
  assert.throws(() => link("x", "relative/path", ctx("en")), /neither a path nor an address/);
  // a help link to a topic or a heading that does not exist fails too
  assert.throws(() => link("x", "/help/no-such-topic", ctx("en")), /names no help topic/);
  assert.throws(() => link("x", "/help/search#nope", ctx("en")), /names no heading of the topic/);
  assert.throws(() => link("x", "#nope", ctx("en")), /names no heading of this topic/);
});

test("help links become the site's help pages, in the page's language; external links stay", () => {
  assert.equal(
    link("FAQ", "/help/search#faq", ctx("ar")),
    '<a href="/ar/help/search/#faq">FAQ</a>'
  );
  assert.equal(link("Help", "/help", ctx("en")), '<a href="/help/">Help</a>');
  assert.equal(link("Top", "#faq", ctx("en")), '<a href="#faq">Top</a>');
  const out = "https://github.com/Hamza-Labs-Core/Maktaba/releases/latest";
  assert.equal(link("Releases", out, ctx("en")), `<a href="${out}">Releases</a>`);
});

test("the app's router: read from App.tsx and the route modules it imports", () => {
  assert.ok(routes.includes("/login"), "App.tsx");
  assert.ok(routes.includes("/admin/updates"), "serverRoutes.tsx");
  assert.ok(!routes.includes("*"), "never the catch-all");
  assert.ok(matchRoute(routes, "/account/preferences/privacy"), "a splat");
  assert.ok(matchRoute(routes, "/videos/12/watch"), "a :param");
  assert.ok(!matchRoute(routes, "/videos/12/watch/more"));
  assert.ok(!matchRoute(routes, "/no-such-page"));
});

test("the Markdown: the app's blocks, and pipe tables", () => {
  const blocks = parseBlocks(
    "# T\n\nLead.\n\n## A {#a}\n\n| x | y |\n| - | --- |\n| 1 | 2 |\n\n- one\n  more\n- two\n\n```\ncode\n```\n"
  );
  assert.deepEqual(
    blocks.map((b) => b.kind),
    ["heading", "para", "heading", "para", "list", "code"]
  );
  const t = parseBlocks("| x | y |\n| --- | :---: |\n| 1 | `2` |\n");
  assert.deepEqual(t, [{ kind: "table", head: ["x", "y"], rows: [["1", "`2`"]] }]);
  assert.deepEqual(parseBlocks("- one\n  more\n- two")[0].items, ["one more", "two"]);
});

test("related topics: the ones it links to first, then its category's", () => {
  const cats = [
    { id: "a", topics: ["one", "two", "three", "four"] },
    { id: "b", topics: ["five"] },
  ];
  assert.deepEqual(relatedOf("one", "see [5](/help/five) and [x](/help/five#y)", cats), [
    "five",
    "two",
    "three",
  ]);
  assert.deepEqual(relatedOf("five", "", cats), []);
  for (const p of helpPages(src).filter((p) => p.path !== "/help/"))
    assert.ok(/<section class="related"/.test(p.body), p.out);
});

test("the index: every category in the app's order, every topic, the filter and its count", () => {
  const pages = helpPages(src);
  for (const lang of ["en", "ar"]) {
    const body = pages.find((p) => p.lang === lang && p.path === "/help/").body;
    const cats = [...body.matchAll(/<h2 id="cat-([a-z-]+)">/g)].map((m) => m[1]);
    assert.deepEqual(
      cats,
      src.categories.map((c) => c.id)
    );
    assert.equal([...body.matchAll(/<li data-search=/g)].length, 44);
    assert.match(body, /<div class="frame help-groups" data-filter-list>/);
    assert.match(body, /<input id="filter" type="search"/);
    assert.match(body, lang === "en" ? /44 topics/ : /44 موضوعًا/);
  }
});

test("the strings table of story 60.12 is the pages' words", () => {
  const story = fs.readFileSync(
    path.join(REPO, "specs/epics/60-website/story-60-12-help.md"),
    "utf8"
  );
  const rows = [...story.matchAll(/^\| `help\.([A-Za-z]+)` \| (.+?) \| (.+?) \|$/gm)];
  assert.ok(rows.length >= 5, "the table has its rows");
  for (const [, key, en, ar] of rows) {
    assert.equal(T.en[key], en, `help.${key} (en)`);
    assert.equal(T.ar[key], ar, `help.${key} (ar)`);
  }
});
