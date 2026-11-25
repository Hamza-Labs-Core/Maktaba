// Story 60.07: the legal pages' builder (legal.mjs). T02 (a source changed without the page's
// copy fails the build, naming the section), its cloud-section half of T01 (each policy value
// on the page, in each language), and the copy's small Markdown. `node --test`.
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { test } from "node:test";
import { fileURLToPath } from "node:url";
import {
  APPS_HEADING,
  appsSource,
  formatDate,
  inline,
  LEGAL,
  legalDoc,
  legalPages,
  licenceName,
  parseCopy,
  POLICY_FIELDS,
  renderLegal,
  renderMd,
} from "./legal.mjs";

const HERE = path.dirname(fileURLToPath(import.meta.url));
const REPO = path.resolve(HERE, "../../../..");
const APP_PRIVACY = fs.readFileSync(path.join(REPO, "docs/app-privacy.md"), "utf8");
const LICENSE = fs.readFileSync(path.join(REPO, "LICENSE"), "utf8");

/** CurrentPolicy() as cloud/internal/privacy/policy.go has it, so these tests need no Go (the
 * site's own test compares the built page with `go run ./cmd/privacy-json`). */
const POLICY = {
  controller: "Hamza Labs",
  contact: "privacy@maktaba.tv",
  purpose:
    "Operate and secure the Maktaba cloud relay: capacity planning, abuse prevention, and aggregate service health; and run the accounts people make on Maktaba Cloud.",
  data_categories: [
    "Aggregate connection counts (no server identity)",
    "Aggregate bandwidth and request volume",
    "Country of request (derived at the edge, IP discarded)",
    "Aggregate push-delivery outcomes",
  ],
  not_collected: [
    "IP addresses in analytics",
    "Server identifiers in analytics",
    "User identifiers in analytics",
    "Media titles, filenames, or content",
    "Request URLs or payloads in analytics",
  ],
  account_data: [
    "Email address, name, language, password hash, plan and homes: until the account is deleted, 30 days after its owner asks",
    "A home's public IP address, for direct connections, and how much it relayed, every 5 minutes and every month: until the home is removed",
    "Sign-in sessions, with the device's name and IP address: up to 31 days after they were last used",
    "The cloud's request log, with the IP address and a code for the account: until it is overwritten",
    "Security and abuse records, with IP addresses and sometimes an email address: with no fixed end yet, after the account is deleted too",
    "Account email: its content sealed until it is sent; then the address as a keyed hash, with the kind, time and outcome, 35 days; the network that asked for it, as a keyed hash, a day",
    "Backups of the database: 14 days",
  ],
  processors: [
    "Resend (account email): sent from Ireland, stored in the United States; the address, the content and the delivery record, 30 days; an address that bounced or complained stays on its suppression list",
    "Hetzner (the cloud's servers): Finland",
    "Apple and Google (push notifications), for an app that asks for them",
  ],
  lawful_basis:
    "Legitimate interest (Art. 6(1)(f)) in operating a reliable, abuse-resistant relay, balanced by collecting only anonymous aggregates; for an account and its email, the contract with its owner (Art. 6(1)(b)).",
  retention_days: 90,
  rights: ["Access", "Erasure", "Restriction", "Objection", "Portability"],
  updated_at: "2026-11-25",
};

const sources = (over = {}) => ({
  policy: POLICY,
  appPrivacy: APP_PRIVACY,
  license: LICENSE,
  ...over,
});

test("the fixture policy is policy.go's (a change there updates this fixture too)", () => {
  const go = fs.readFileSync(path.join(REPO, "cloud/internal/privacy/policy.go"), "utf8");
  for (const f of POLICY_FIELDS) {
    const vals = Array.isArray(POLICY[f]) ? POLICY[f] : [POLICY[f]];
    for (const v of vals) {
      if (typeof v !== "string") continue;
      // policy.go splits the purpose over two string literals
      const words = v.split(" ").slice(0, 3).join(" ");
      assert.ok(go.includes(words), `${f}: "${v}" is not in policy.go`);
    }
  }
  assert.ok(go.includes(`UpdatedAt: "${POLICY.updated_at}"`));
});

test("every legal page builds in both languages, with the same sections", () => {
  const pages = legalPages({ sources: sources() });
  assert.equal(pages.length, LEGAL.length * 2);
  for (const p of pages) {
    assert.match(p.body, /<h1>/);
    assert.match(p.body, /<time datetime="\d{4}-\d{2}-\d{2}">/);
    assert.ok(p.title && p.description, p.name);
  }
});

test("T01: the cloud section is the policy, value by value, in each language", () => {
  const en = legalDoc("privacy", "en", { sources: sources() });
  const html = en.sections.find((s) => s.id === "cloud").html;
  for (const v of [
    ...POLICY.data_categories,
    ...POLICY.not_collected,
    ...POLICY.account_data,
    ...POLICY.processors,
    ...POLICY.rights,
  ])
    assert.ok(
      html.includes(`<li>${v.replaceAll("'", "&#39;")}</li>`) || html.includes(`<li>${v}</li>`),
      v
    );
  assert.ok(html.includes('<dd data-field="controller">Hamza Labs</dd>'));
  assert.ok(html.includes('href="mailto:privacy@maktaba.tv"'));
  const ar = legalDoc("privacy", "ar", { sources: sources() });
  const arHtml = ar.sections.find((s) => s.id === "cloud").html;
  assert.ok(arHtml.includes("90 يومًا"));
  assert.ok(!arHtml.includes("Aggregate bandwidth"), "an English value left on the Arabic page");
});

test("the account's emails: who delivers them, where they are stored and for how long, in each language", () => {
  const say = {
    en: ["United States", "30 days", "35 days"],
    ar: ["الولايات المتحدة", "30 يومًا", "35 يومًا"],
  };
  for (const lang of ["en", "ar"]) {
    const doc = legalDoc("privacy", lang, { sources: sources() });
    const html = doc.sections.find((s) => s.id === "account").html;
    for (const w of ["Resend", "https://resend.com/security/gdpr", ...say[lang]])
      assert.ok(html.includes(w), `${lang}: ${w}`);
    assert.ok(doc.summary.includes("Resend"), `${lang}: the summary`);
    // what only the relay does is not said of the whole cloud
    assert.ok(
      !/(Maktaba Cloud|relay) keeps only|لا تحتفظ إلا|لا يحتفظ إلا/.test(doc.summary),
      lang
    );
  }
});

test("T01: a policy value with no Arabic fails the build, naming the cloud section", () => {
  const changed = { ...POLICY, rights: [...POLICY.rights, "Rectification"] };
  assert.throws(
    () => legalDoc("privacy", "ar", { sources: sources({ policy: changed }) }),
    /privacy, the cloud section \(#cloud\): CurrentPolicy\(\) says "Rectification"/
  );
  const dropped = { ...POLICY, rights: POLICY.rights.slice(1) };
  assert.throws(
    () => legalDoc("privacy", "ar", { sources: sources({ policy: dropped }) }),
    /the cloud section \(#cloud\): .* no longer has: "Access"/
  );
});

test("the page's date follows the policy's when the policy is newer", () => {
  const later = { ...POLICY, updated_at: "2099-01-02" };
  assert.equal(
    legalDoc("privacy", "en", { sources: sources({ policy: later }) }).updated,
    "2099-01-02"
  );
  assert.equal(legalDoc("privacy", "en", { sources: sources() }).updated, "2026-11-25");
});

test("T02: docs/app-privacy.md changed without the page fails the build, naming the section", () => {
  const changed = APP_PRIVACY.replace(
    "Maktaba keeps it for 90 days",
    "Maktaba keeps it for 180 days"
  );
  assert.notEqual(changed, APP_PRIVACY, "the fixture's sentence left app-privacy.md");
  for (const lang of ["en", "ar"])
    assert.throws(
      () => legalDoc("privacy", lang, { sources: sources({ appPrivacy: changed }) }),
      /privacy, the apps section \(#apps\): docs\/app-privacy\.md's "What the apps send, and where" changed/
    );
  // a change elsewhere in the file (the store forms' tables) is not the page's
  const elsewhere = APP_PRIVACY.replace(
    "## Google Play: Data safety",
    "## Google Play: Data safety\n\nA note."
  );
  assert.doesNotThrow(() =>
    legalDoc("privacy", "en", { sources: sources({ appPrivacy: elsewhere }) })
  );
  assert.throws(() => appsSource("# nothing here\n"), new RegExp(APPS_HEADING.slice(3)));
});

test("terms: LICENSE names the licence, and another licence fails the build", () => {
  assert.equal(licenceName(LICENSE), "MIT License");
  const doc = legalDoc("terms", "en", { sources: sources() });
  assert.ok(doc.summary.includes("MIT License"));
  assert.throws(
    () => legalDoc("terms", "en", { sources: sources({ license: "Apache License\n" }) }),
    /terms, the software section \(#software\): LICENSE is the Apache License/
  );
});

test("the legal pages are privacy and terms only, and none names a copyright page or its address", () => {
  assert.deepEqual(LEGAL, ["privacy", "terms"]);
  for (const name of LEGAL)
    for (const lang of ["en", "ar"]) {
      const doc = legalDoc(name, lang, { sources: sources() });
      const html = renderLegal(doc);
      assert.ok(
        !html.includes("/legal/copyright"),
        `${name}.${lang}: a link to the copyright page`
      );
      assert.ok(!html.includes("copyright@"), `${name}.${lang}: the copyright address`);
      assert.ok(
        !doc.sections.some((s) => s.id === "copyright"),
        `${name}.${lang}: a copyright section`
      );
    }
});

test("the copy's Markdown", () => {
  assert.equal(
    inline("a **b [c](/d/)** `<e>` & [f](https://x.example/?a=1&b=2)"),
    'a <strong>b <a href="/d/">c</a></strong> <code>&lt;e&gt;</code> &amp; <a href="https://x.example/?a=1&amp;b=2">f</a>'
  );
  assert.equal(
    renderMd("One\ntwo.\n\n- a\n  more\n  - b\n- c\n\n1. x\n2. y\n\n{{slot}}", { slot: "<hr>" }),
    "<p>One two.</p>\n<ul>\n<li>a more\n<ul>\n<li>b</li>\n</ul></li>\n<li>c</li>\n</ul>\n<ol>\n<li>x</li>\n<li>y</li>\n</ol>\n<hr>"
  );
  assert.throws(() => renderMd("{{nope}}"), /no block \{\{nope\}\}/);
  assert.throws(() => renderMd("- a\n\n  b"), /an indented block outside a list/);
  assert.throws(() => parseCopy("---\ntitle: x\n---\n"), /no description/);
  assert.throws(
    () => parseCopy("---\ntitle: x\ndescription: y\nupdated: 2026-01-01\n---\nS\n\n## No id\n"),
    /without its \{#id\}/
  );
  assert.equal(formatDate("2026-11-01", "en"), "1 November 2026");
  assert.equal(formatDate("2026-11-01", "ar"), "1 نوفمبر 2026");
});
