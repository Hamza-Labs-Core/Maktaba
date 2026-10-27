// Help can't fall behind (Epic 41): every topic exists in both languages
// with the five standard sections and the same anchors; every link the app
// or the content makes points at a topic and anchor that exist; every page,
// rules section, failure cause and waiting reason has an entry.
import { describe, expect, it } from "vitest";
import en from "../i18n/en.json";
import ar from "../i18n/ar.json";
import { anchors, headings, links } from "./parse";
import {
  FAILURE_CLASS_HELP,
  KNOWN_CAUSES,
  PAGE_HELP,
  WAITING_REASONS,
  causeHelp,
  failureActionHelp,
  failureClassHelp,
  failureStatusHelp,
  pageHelp,
  ruleHelp,
  stageHelp,
  SYSTEM_HEALTH_KINDS,
  systemHealthHelp,
  tileHealthHelp,
  UPDATE_CODE_HELP,
  updateCodeHelp,
  waitingHelp,
  type HelpRef,
} from "./registry";
import { HELP_TOPICS, STANDARD_ANCHORS, isHelpTopic } from "./topics";
import { DOC_SECTIONS } from "../pages/Admin/LiveTV/docs";

const FILES = import.meta.glob<string>("./content/*/*.md", {
  query: "?raw",
  import: "default",
  eager: true,
});

const LOCALES = ["en", "ar"] as const;
const src = (locale: string, id: string) => FILES[`./content/${locale}/${id}.md`];

// Files outside src/ are read at run time (the web image builds from web/ alone).
const FS = "node:fs";
const { readFileSync } = (await import(/* @vite-ignore */ FS)) as {
  readFileSync: (path: string, encoding: "utf8") => string;
};
const read = (path: string) => readFileSync(path, "utf8"); // vitest runs in web/

function anchorSet(topic: string): Set<string> {
  return new Set(anchors(src("en", topic) ?? ""));
}

function expectRef(ref: HelpRef, what: string) {
  expect(isHelpTopic(ref.topic), `${what}: no topic ${ref.topic}`).toBe(true);
  if (ref.anchor) {
    expect(anchorSet(ref.topic).has(ref.anchor), `${what}: ${ref.topic}#${ref.anchor}`).toBe(true);
  }
}

describe("help topics", () => {
  it("every topic has an English and an Arabic file, and every file is a topic", () => {
    for (const id of HELP_TOPICS) {
      for (const l of LOCALES) expect(src(l, id), `${l}/${id}.md`).toBeTypeOf("string");
    }
    for (const path of Object.keys(FILES)) {
      const id = /\/([^/]+)\.md$/.exec(path)![1]!;
      expect(isHelpTopic(id), `${path} is not in HELP_TOPICS`).toBe(true);
    }
  });

  it("every topic has its title and summary in both catalogs", () => {
    for (const id of HELP_TOPICS) {
      for (const cat of [en, ar] as Array<Record<string, string>>) {
        expect(cat[`help.topic.${id}.title`], `help.topic.${id}.title`).toBeTruthy();
        expect(cat[`help.topic.${id}.summary`], `help.topic.${id}.summary`).toBeTruthy();
      }
    }
  });

  it("every topic has a title and the five standard sections, in order", () => {
    for (const id of HELP_TOPICS) {
      for (const l of LOCALES) {
        const hs = headings(src(l, id)!);
        expect(hs[0]?.level, `${l}/${id}: starts with "# Title"`).toBe(1);
        const top = hs.filter((h) => h.level === 2).map((h) => h.anchor);
        const std = top.filter((a) => (STANDARD_ANCHORS as readonly (string | null)[]).includes(a));
        expect(std, `${l}/${id}`).toEqual([...STANDARD_ANCHORS]);
      }
    }
  });

  it("anchors are well formed, unique, and the same in English and Arabic", () => {
    for (const id of HELP_TOPICS) {
      const sets = LOCALES.map((l) => {
        const list = anchors(src(l, id)!);
        for (const a of list) expect(a, `${l}/${id}`).toMatch(/^[a-z0-9]+(-[a-z0-9]+)*$/);
        expect(new Set(list).size, `${l}/${id}: duplicate anchors`).toBe(list.length);
        return [...list].sort();
      });
      expect(sets[1], `${id}: ar anchors differ from en`).toEqual(sets[0]);
    }
  });

  it("every in-content link goes to a topic and anchor that exist, or an app page", () => {
    for (const id of HELP_TOPICS) {
      for (const l of LOCALES) {
        for (const href of links(src(l, id)!)) {
          const what = `${l}/${id}: ${href}`;
          const m = /^\/help\/([^#?]+)(?:#(.+))?$/.exec(
            href.startsWith("#") ? `/help/${id}${href}` : href
          );
          if (m) {
            expectRef({ topic: m[1] as HelpRef["topic"], anchor: m[2] }, what);
          } else if (href.startsWith("/")) {
            const path = href.split(/[?#]/)[0]!;
            expect(pageHelp(path) !== null || path === "/help", `${what}: not an app page`).toBe(
              true
            );
          } else {
            expect(href, what).toMatch(/^(https:\/\/|mailto:)/);
          }
        }
      }
    }
  });
});

describe("help links from the app", () => {
  it("every route of App.tsx and serverRoutes.tsx has a help entry", () => {
    // the server pages' routes live in serverRoutes.tsx (Epic 42.5, lazily loaded)
    const app = read("src/App.tsx") + read("src/serverRoutes.tsx");
    const paths = [...app.matchAll(/<Route\s+path="([^"]+)"/g)].map((m) => m[1]!);
    expect(paths.length).toBeGreaterThan(40);
    const known = new Set(PAGE_HELP.map(([p]) => p));
    for (const p of paths) expect(known.has(p), `route ${p} has no PAGE_HELP entry`).toBe(true);
  });

  it("every page entry points at a topic and anchor that exist", () => {
    for (const [path, ref] of PAGE_HELP) expectRef(ref, `page ${path}`);
  });

  it("every rules section has its entry in the rules topic", () => {
    const schema = JSON.parse(read("../api/internal/handlers/rules/schema.json")) as {
      sections: Array<{ key: string }>;
    };
    expect(schema.sections.length).toBeGreaterThan(20);
    for (const s of schema.sections) expectRef(ruleHelp(s.key), `rules section ${s.key}`);
  });

  it("every failure cause and waiting reason has its entry", () => {
    for (const c of KNOWN_CAUSES) expectRef(causeHelp(c), `cause ${c}`);
    expectRef(causeHelp("ffmpeg:something new"), "generated cause");
    expectRef(causeHelp(null), "unknown cause");
    expectRef(failureClassHelp("anything"), "failure class");
    for (const [cls, ref] of FAILURE_CLASS_HELP) expectRef(ref, `failure class ${cls}`);
    expect(failureClassHelp("dependency:model:tagcheck").anchor).toBe("class-dependency-model");
    expect(causeHelp("model_unavailable: the model did not answer").anchor).toBe(
      "model-unavailable"
    );
    for (const r of WAITING_REASONS) expectRef(waitingHelp(r), `waiting reason ${r}`);
  });

  it("every status, class and action of the Failing items page, and every tile note, has its entry", () => {
    const keys = (prefix: string) =>
      Object.keys(en)
        .map((k) => (k.startsWith(prefix) ? k.slice(prefix.length) : null))
        .filter((k): k is string => !!k && !k.includes("."));
    const statuses = keys("queue.failures.status.").filter((s) => s !== "open" && s !== "all");
    expect(statuses.length).toBeGreaterThan(3);
    for (const s of statuses) {
      expect(failureStatusHelp(s).anchor, `status ${s}`).toBe(`status-${s.replace(/_/g, "-")}`);
      expectRef(failureStatusHelp(s), `status ${s}`);
    }
    const classes = keys("queue.failures.class.").filter((c) => c !== "any");
    expect(classes.length).toBeGreaterThan(3);
    for (const c of classes) {
      expect(FAILURE_CLASS_HELP.has(c), `class ${c}`).toBe(true);
      expectRef(failureClassHelp(c), `class ${c}`);
    }
    const actions = keys("queue.failures.action.");
    expect(actions.length).toBeGreaterThan(5);
    for (const a of actions) expectRef(failureActionHelp(a), `action ${a}`);
    for (const h of ["failing", "waiting", "needs_you"] as const) expectRef(tileHealthHelp(h), h);
  });

  it("every stage named in the catalog has its entry", () => {
    const stages = Object.keys(en)
      .map((k) => /^queue\.stage\.([a-z_]+)\.desc$/.exec(k)?.[1])
      .filter((s): s is string => !!s);
    expect(stages.length).toBeGreaterThan(10);
    for (const s of stages) expectRef(stageHelp(s), `stage ${s}`);
  });

  it("every part of the System health page has its entry, and the API's kinds are all listed", () => {
    for (const k of SYSTEM_HEALTH_KINDS) expectRef(systemHealthHelp(k), `system health ${k}`);
    expect(systemHealthHelp("something_new")).toEqual({ topic: "system-health", anchor: "items" });
    expectRef(systemHealthHelp("something_new"), "system health unknown kind");
    const types = read("../api/internal/handlers/systemstatus/types.go");
    const api = [...types.matchAll(/^\s+Kind\w+\s+=\s+"([a-z_]+)"$/gm)].map((m) => m[1]!);
    expect(api.length).toBeGreaterThan(10);
    expect([...SYSTEM_HEALTH_KINDS].sort()).toEqual([...api].sort());
  });

  it("every reason an update stops with has its entry, listed by name", () => {
    const codes = Object.keys(en)
      .filter((k) => k.startsWith("updates.code."))
      .map((k) => k.slice("updates.code.".length));
    expect(codes.length).toBeGreaterThan(100);
    for (const c of codes) {
      expectRef(updateCodeHelp(c), `update code ${c}`);
      // a new reason gets its entry on purpose, not the general one by default
      const listed = UPDATE_CODE_HELP.has(c) || c.startsWith("verify_");
      expect(listed, `update code ${c} is not in UPDATE_CODE_HELP`).toBe(true);
    }
    for (const [c, anchor] of UPDATE_CODE_HELP)
      expectRef({ topic: "updates", anchor }, `update code ${c}`);
    expect(updateCodeHelp("something_new")).toEqual({ topic: "updates", anchor: "run-failed" });
    expect(updateCodeHelp("verify_something_new")).toEqual({
      topic: "updates",
      anchor: "verify-failed",
    });
  });

  it("the Live TV page's guide links point at entries that exist", () => {
    for (const [k, ref] of Object.entries(DOC_SECTIONS)) expectRef(ref, `Live TV doc ${k}`);
  });

  it("pageHelp resolves the most specific page and skips the help itself", () => {
    expect(pageHelp("/queue/reports")).toEqual({ topic: "processing", anchor: "reports" });
    expect(pageHelp("/queue/transcribe")).toEqual({ topic: "processing", anchor: "queue-page" });
    expect(pageHelp("/settings/rules")).toEqual({ topic: "rules" });
    expect(pageHelp("/settings")).toEqual({ topic: "settings" });
    expect(pageHelp("/help/failures")).toBeNull();
  });
});
