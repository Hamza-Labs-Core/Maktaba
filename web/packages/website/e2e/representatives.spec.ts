// Story 60.04: e2e/representatives.json stays true. Each family's representatives are pages it
// has, in both languages, and each claim holds on the rendered page (the longest has the most
// text in <main>, the shortest the least, "table" has a table, "form" a form, "help-link" a link
// to /help/, "code" a code block). A family with pages has representatives.
import fs from "node:fs";
import path from "node:path";
import { expect, test, type Page } from "@playwright/test";
import { PKG, settle, sitePages } from "./pages";

const reps: Record<string, Record<string, string>> = JSON.parse(
  fs.readFileSync(path.join(PKG, "e2e/representatives.json"), "utf8")
);
const families = Object.keys(reps).filter((k) => !k.startsWith("$"));

async function measure(page: Page, url: string) {
  await page.goto(url);
  await settle(page);
  return page.evaluate(() => {
    const main = document.querySelector("main");
    return {
      text: (main?.innerText ?? "").replace(/\s+/g, " ").trim().length,
      table: !!main?.querySelector("table"),
      form: !!main?.querySelector("form, input, select, textarea"),
      helpLink: !!main?.querySelector('a[href*="/help/"]'),
      code: !!main?.querySelector("pre, code"),
    };
  });
}

for (const family of families) {
  test(`${family}: the representatives are its pages and each claim holds`, async ({ page }) => {
    const pages = sitePages().filter((p) => p.kind === family && p.lang === "en");
    const chosen = reps[family];
    if (!pages.length) {
      expect(chosen, `${family} has no pages yet, so no representatives`).toEqual({});
      return;
    }
    expect(
      Object.keys(chosen).length,
      `${family} has pages: name its representatives`
    ).toBeGreaterThan(0);
    const urls = new Set(pages.map((p) => p.url));
    for (const [claim, url] of Object.entries(chosen)) {
      expect(urls, `${family}.${claim}: ${url} is one of its pages`).toContain(url);
      expect(
        sitePages().some((p) => p.url === `/ar${url}`),
        `${family}.${claim}: ${url} has its Arabic twin`
      ).toBe(true);
    }
    const sizes = new Map<string, Awaited<ReturnType<typeof measure>>>();
    for (const p of pages) sizes.set(p.url, await measure(page, p.url));
    const byText = [...sizes].sort((a, b) => a[1].text - b[1].text);
    for (const [claim, url] of Object.entries(chosen)) {
      const m = sizes.get(url)!;
      if (claim === "longest") expect(url, "the longest").toBe(byText.at(-1)![0]);
      else if (claim === "shortest") expect(url, "the shortest").toBe(byText[0][0]);
      else if (claim === "table") expect(m.table, `${url} has a table`).toBe(true);
      else if (claim === "form") expect(m.form, `${url} has a form`).toBe(true);
      else if (claim === "help-link") expect(m.helpLink, `${url} links to help`).toBe(true);
      else if (claim === "code") expect(m.code, `${url} has code`).toBe(true);
      else throw new Error(`${family}.${claim}: an unknown claim`);
    }
  });
}
