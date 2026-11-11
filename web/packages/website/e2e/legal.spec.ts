// Story 60.07 in a real browser: the copyright page with its form's endpoint off (T03) and
// faked (T04), and every legal page setting no cookie and reaching no other origin (T06).
import fs from "node:fs";
import path from "node:path";
import { stripTypeScriptTypes } from "node:module";
import { expect, test, type Request } from "@playwright/test";
import { PKG, settle } from "./pages";
// @ts-ignore: plain JavaScript build scripts
import { legalDoc, renderLegal } from "../scripts/legal.mjs";
// @ts-ignore
import { page as shell } from "../scripts/shell.mjs";
import { PAYLOAD_KEYS, TURNSTILE_SCRIPT } from "../src/copyright-form";

const LEGAL = ["privacy", "terms", "copyright"];
const ENDPOINT = "https://api.maktaba.tv/v1/moderation/notices";

test.describe("T03: the copyright page before the endpoint is live", () => {
  for (const url of ["/legal/copyright/", "/ar/legal/copyright/"]) {
    test(`${url}: the agent's email and the notice's contents, and no form`, async ({ page }) => {
      await page.goto(url);
      await settle(page);
      await expect(
        page.locator('main a[href="mailto:copyright@maktaba.tv"]').first()
      ).toBeVisible();
      await expect(page.locator("section[aria-labelledby=notice] ol > li")).toHaveCount(7);
      await expect(page.locator("form")).toHaveCount(0);
      await expect(page.locator("script")).toHaveCount(0);
    });
  }
});

test.describe("T06: the legal pages set no cookie and reach no other origin", () => {
  for (const name of LEGAL)
    for (const lang of ["en", "ar"]) {
      const url = `${lang === "ar" ? "/ar" : ""}/legal/${name}/`;
      test(url, async ({ page, context, baseURL }) => {
        const origin = new URL(baseURL!).origin;
        const seen: string[] = [];
        const cookies: string[] = [];
        page.on("request", (r) => seen.push(r.url()));
        page.on("response", async (r) => {
          const c = await r.headerValue("set-cookie");
          if (c) cookies.push(`${r.url()}: ${c}`);
        });
        await page.goto(url);
        await settle(page);
        // what the page does after load (a link hovered is not a request)
        await page.waitForLoadState("networkidle");
        const other = seen.filter((u) => !u.startsWith("data:") && new URL(u).origin !== origin);
        expect(other, `${url} reached another origin`).toEqual([]);
        expect(cookies).toEqual([]);
        expect(await context.cookies()).toEqual([]);
        expect(await page.evaluate(() => document.cookie)).toBe("");
      });
    }
});

test.describe("T04: the copyright form with its endpoint faked", () => {
  // the page as the build writes it with WEBSITE_NOTICE_ENDPOINT and WEBSITE_TURNSTILE_SITEKEY
  // set, served at a path of its own; Turnstile and the endpoint are faked in the browser
  const notice = { endpoint: ENDPOINT, sitekey: "1x00000000000000000000AA" };
  const SCRIPT = "/t04/assets/copyright-form.js";

  for (const lang of ["en", "ar"] as const) {
    test(`${lang}: one POST with exactly the form's fields and a Turnstile token`, async ({
      page,
      baseURL,
    }) => {
      const origin = new URL(baseURL!).origin;
      const doc = legalDoc("copyright", lang, {
        // the copyright page reads no source (the privacy and terms pages do)
        sources: {},
        notice,
        noticeScript: SCRIPT,
      });
      const html = shell({
        lang,
        path: "/legal/copyright/",
        title: doc.title,
        description: doc.description,
        body: renderLegal(doc),
      });
      const js = stripTypeScriptTypes(
        fs.readFileSync(path.join(PKG, "src/copyright-form.ts"), "utf8")
      );
      await page.route(`${origin}/t04/legal/copyright/`, (r) =>
        r.fulfill({ status: 200, contentType: "text/html; charset=utf-8", body: html })
      );
      await page.route(`${origin}${SCRIPT}`, (r) =>
        r.fulfill({ status: 200, contentType: "text/javascript", body: js })
      );
      // Turnstile, faked: it puts its token in the form, as the real widget does
      await page.route(TURNSTILE_SCRIPT, (r) =>
        r.fulfill({
          status: 200,
          contentType: "text/javascript",
          body: `for (const d of document.querySelectorAll(".cf-turnstile")) {
  const i = document.createElement("input");
  i.type = "hidden"; i.name = "cf-turnstile-response"; i.value = "fake-token";
  d.appendChild(i);
}`,
        })
      );
      const posts: Request[] = [];
      const cors = {
        "Access-Control-Allow-Origin": origin,
        "Access-Control-Allow-Methods": "POST",
        "Access-Control-Allow-Headers": "Content-Type, Accept",
      };
      await page.route(ENDPOINT, (r) => {
        if (r.request().method() === "OPTIONS") return r.fulfill({ status: 204, headers: cors });
        posts.push(r.request());
        return r.fulfill({
          status: 202,
          headers: cors,
          contentType: "application/json",
          body: JSON.stringify({ notice_id: "N-42" }),
        });
      });

      await page.goto("/t04/legal/copyright/");
      await settle(page);
      const form = page.locator("#notice-form");
      await expect(form).toBeVisible();
      await expect(page.locator("#notice-where")).toBeVisible();
      await expect(form.locator('input[name="cf-turnstile-response"]')).toHaveValue("fake-token");

      await form.locator("#notice-claimant_name").fill("A Person");
      await form.locator("#notice-claimant_email").fill("a@example.com");
      await form.locator("#notice-claimant_address").fill("1 Street, Town");
      await form.locator("#notice-claimant_phone").fill("+1 555 0100");
      await form.locator("#notice-work").fill("A film");
      await form
        .locator("#notice-urls")
        .fill("https://watch.maktaba.tv/c/1\nhttps://watch.maktaba.tv/c/2");
      await form.locator("#notice-explanation").fill("It is mine.");
      await form.locator("#notice-good_faith").check();
      await form.locator("#notice-accuracy").check();
      await form.locator("#notice-signature").fill("A Person");
      await form.locator('button[type="submit"]').click();

      await expect(page.locator("#notice-status")).toContainText("N-42");
      expect(posts).toHaveLength(1);
      const post = posts[0];
      expect(post.method()).toBe("POST");
      expect(post.headers()["content-type"]).toBe("application/json");
      const body = post.postDataJSON();
      expect(Object.keys(body).sort()).toEqual(PAYLOAD_KEYS);
      expect(body).toMatchObject({
        claimant_name: "A Person",
        claimant_email: "a@example.com",
        urls: ["https://watch.maktaba.tv/c/1", "https://watch.maktaba.tv/c/2"],
        good_faith: true,
        accuracy: true,
        signature: "A Person",
        turnstile_token: "fake-token",
        lang,
      });
    });
  }
});
