// Story 60.06: the problem pages' builder (problems.mjs). T10 (an entry missing a text fails the
// build, naming the entry and the field), T09's half that holds before a build (the identifiers
// a text names are code), and the structure over the whole registry: every entry has both pages
// and every section. `node --test`; the built pages are src/site.test.ts's.
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { test } from "node:test";
import { fileURLToPath } from "node:url";
import {
  AREAS,
  AUDIENCES,
  BASE,
  indexDoc,
  marksCode,
  problemDoc,
  problemPages,
  registryFindings,
  renderIndex,
  renderProblem,
  T,
  topicLinks,
} from "./problems.mjs";

const HERE = path.dirname(fileURLToPath(import.meta.url));
const REPO = path.resolve(HERE, "../../../..");
const REG = JSON.parse(fs.readFileSync(path.join(REPO, "shared/api/problems.json"), "utf8"));

const entry = (over = {}) => ({
  slug: "seat-limit",
  status: 403,
  area: "accounts",
  audience: "admin",
  codes: ["seat_limit"],
  title: { en: "This home has no free seats", ar: "لا مقاعد شاغرة في هذا البيت" },
  means: { en: "Every seat is taken.", ar: "كل المقاعد مشغولة." },
  do: { en: "Free a seat, or ask for more.", ar: "حرّروا مقعدًا، أو اطلبوا المزيد." },
  help: "users#seat-limit",
  install: null,
  ...over,
});
const reg = (...problems) => ({ base: BASE, problems });

test("T10: an entry missing its Arabic do fails, naming the entry and the field", () => {
  const e = entry();
  e.do = { en: e.do.en };
  assert.deepEqual(registryFindings(reg(e)), ["problems.json: seat-limit: no Arabic do (do.ar)"]);
  assert.throws(
    () => problemPages(reg(e)),
    /the problem registry has 1 problem\(s\):\nproblems\.json: seat-limit: no Arabic do \(do\.ar\)/
  );
});

test("T10: every text in both languages, a known area and audience, a slug and a status", () => {
  const blank = entry({ slug: "a", title: { en: " ", ar: "x" }, means: { ar: "x" } });
  assert.deepEqual(registryFindings(reg(blank)), [
    "problems.json: a: no English title (title.en)",
    "problems.json: a: no English means (means.en)",
  ]);
  assert.deepEqual(
    registryFindings(
      reg(
        entry({ slug: "b", area: "elsewhere", audience: "robot", status: 200, codes: [1] }),
        entry({ slug: "B!" }),
        entry({ slug: "c" }),
        entry({ slug: "c", help: "" })
      )
    ),
    [
      "problems.json: b: the status 200 is not an HTTP error status",
      'problems.json: b: no heading for the area "elsewhere" (AREAS)',
      'problems.json: b: the audience "robot" is not one of anyone, person, admin, app, node',
      "problems.json: b: codes is not a list of strings",
      "problems.json: B!: the slug is not [a-z0-9_-]+",
      "problems.json: c: the slug is listed twice",
      "problems.json: c: help is neither null nor a topic",
    ]
  );
  assert.deepEqual(registryFindings({ problems: "no" }), ["problems.json: no problems list"]);
});

test("the registry as it is: no finding, and words for every area and audience it uses", () => {
  assert.deepEqual(registryFindings(REG), []);
  const areas = new Set(AREAS.map((a) => a.id));
  for (const e of REG.problems) {
    assert.ok(areas.has(e.area), e.slug);
    assert.ok(AUDIENCES.includes(e.audience), e.slug);
  }
  for (const lang of ["en", "ar"])
    for (const a of AUDIENCES) assert.ok(T[lang].who[a], `${lang} who.${a}`);
});

test("T09: identifiers in a text are code; a plain word that is also a slug stays a word", () => {
  const known = new Set(["seat-limit", "busy", "expired"]);
  assert.equal(
    marksCode("If the code is unknown_node, see seat-limit. It is busy & expired.", known),
    "If the code is <code>unknown_node</code>, see <code>seat-limit</code>. It is busy &amp; expired."
  );
  assert.equal(
    marksCode("وإن كان الرمز unknown_key، فاعتمدوه.", known).includes("<code>unknown_key</code>"),
    true
  );
  assert.equal(marksCode("<b>", known), "&lt;b&gt;");
});

test("a page has every section: the way back, title, who, means, do, who helps, details", () => {
  for (const lang of ["en", "ar"]) {
    const t = T[lang];
    const html = renderProblem(problemDoc(entry(), lang));
    const prefix = lang === "ar" ? "/ar" : "";
    assert.match(
      html,
      new RegExp(
        `<a href="${prefix}/problems/"><svg class="dir-icon" data-mirror [^>]*>.*?</svg>${t.crumbs}</a>`
      )
    );
    assert.match(html, /<h1>[^<]+<\/h1>/);
    assert.match(
      html,
      new RegExp(`<p class="who"><svg [^>]*aria-hidden="true"[^>]*>.*?</svg>${t.who.admin}</p>`),
      lang
    );
    for (const h of [t.means, t.do, t.whoHelps, t.details])
      assert.ok(html.includes(`>${h}</h2>`), h);
    assert.ok(html.includes(`<code>${BASE}seat-limit</code>`));
    assert.ok(html.includes("<dd><code>403</code></dd>"));
    assert.ok(html.includes("<dd><code>seat_limit</code></dd>"));
    assert.ok(
      html.includes(`href="${prefix}/install/troubleshooting/"`),
      "who helps links troubleshooting"
    );
  }
  // no codes: no code row; several: the plural label
  assert.doesNotMatch(renderProblem(problemDoc(entry({ codes: [] }), "en")), /<dt>Code/);
  assert.match(renderProblem(problemDoc(entry({ codes: ["a", "b"] }), "en")), /<dt>Codes<\/dt>/);
});

test("who sees it and who helps follow the audience", () => {
  const html = (audience, lang = "en") => renderProblem(problemDoc(entry({ audience }), lang));
  assert.ok(html("person").includes("You may see this while using Maktaba."));
  assert.ok(html("person").includes("ask the person who runs your home"));
  assert.ok(html("admin").includes("github.com/Hamza-Labs-Core/Maktaba/issues"));
  assert.ok(html("app").includes("the app's maker"));
  // the Arabic says «مكتبة», never the Latin name, and addresses the reader in the plural
  for (const a of AUDIENCES) assert.doesNotMatch(T.ar.who[a], /Maktaba/);
  assert.ok(html("person", "ar").includes("قد تظهر لكم هذه الرسالة أثناء استخدام برنامج مكتبة."));
  assert.equal(T.ar.do, "ما الذي يمكنكم فعله");
});

test("related pages appear only when the page they name is built", () => {
  const e = entry({ install: "port-in-use" });
  assert.deepEqual(topicLinks(e, "ar"), [
    { kind: "help", href: "/ar/help/users/#seat-limit" },
    { kind: "install", href: "/ar/install/troubleshooting/#port-in-use" },
  ]);
  const none = renderProblem(problemDoc(e, "en"));
  assert.doesNotMatch(none, /related-title/);
  const some = renderProblem(
    problemDoc(e, "en", { resolves: (href) => href.startsWith("/install/") })
  );
  assert.match(some, /<h2 id="related-title">Related pages<\/h2>/);
  assert.ok(
    some.includes('<a href="/install/troubleshooting/#port-in-use">Install troubleshooting</a>')
  );
  assert.doesNotMatch(some, /\/help\//);
});

test("the index groups by area in AREAS' order, sorts by title, and leaves empty areas out", () => {
  const doc = indexDoc(
    [
      entry({ slug: "z", area: "general", title: { en: "Zed", ar: "ي" } }),
      entry({ slug: "b", area: "accounts", title: { en: "Bee", ar: "ب" } }),
      entry({ slug: "a", area: "accounts", title: { en: "Ant", ar: "أ" } }),
    ],
    "en"
  );
  assert.deepEqual(
    doc.areas.map((a) => [a.id, a.entries.map((e) => e.slug)]),
    [
      ["accounts", ["a", "b"]],
      ["general", ["z"]],
    ]
  );
  const html = renderIndex(doc, { script: "/assets/problems-filter-x.js" });
  assert.match(html, /<div class="filter" role="search">/);
  assert.match(
    html,
    /<p class="count" id="count" aria-live="polite" lang="en" data-count-one="1 message" data-count-other="\{n\} messages">3 messages<\/p>/
  );
  assert.match(
    html,
    /<a href="\/problems\/a\/"><span class="t">Ant<\/span> <code class="slug">a<\/code><\/a>/
  );
  assert.match(html, /<script type="module" src="\/assets\/problems-filter-x\.js"><\/script>/);
  // the Arabic count, in the plural form of the registry's size (few for 3 to 10 after the
  // hundreds, many for 11 to 99: 307 رسائل, 288 رسالة)
  const ar = renderIndex(indexDoc(REG.problems, "ar"));
  const n = REG.problems.length;
  const form = { few: "رسائل", many: "رسالة", other: "رسالة" }[
    new Intl.PluralRules("ar").select(n)
  ];
  assert.ok(form, `the registry's size ${n} has a plural form this test does not name`);
  assert.match(ar, new RegExp(`>${n} ${form}</p>`));
});

test("every slug of the registry has both pages, each with every section, and both indexes", () => {
  const pages = problemPages(REG);
  const outs = new Set(pages.map((p) => p.out));
  assert.equal(pages.length, REG.problems.length * 2 + 2);
  assert.ok(outs.has("problems/index.html") && outs.has("ar/problems/index.html"));
  for (const e of REG.problems)
    for (const lang of ["en", "ar"]) {
      const out = `${lang === "ar" ? "ar/" : ""}problems/${e.slug}/index.html`;
      const p = pages.find((x) => x.out === out);
      assert.ok(p, `${out} is built`);
      assert.equal(p.title, e.title[lang]);
      assert.equal(p.path, `/problems/${e.slug}/`);
      for (const id of ["means", "do", "who-helps", "details-title"])
        assert.ok(p.body.includes(`id="${id}"`), `${out}: #${id}`);
      assert.ok(p.body.includes(`data-problem="${e.slug}"`), out);
    }
  // each index lists every entry once
  for (const lang of ["en", "ar"]) {
    const idx = pages.find((p) => p.out === `${lang === "ar" ? "ar/" : ""}problems/index.html`);
    const links = [...idx.body.matchAll(/<li><a href="([^"]+)"/g)].map((m) => m[1]);
    assert.equal(links.length, REG.problems.length, lang);
    assert.equal(new Set(links).size, REG.problems.length, lang);
  }
});
