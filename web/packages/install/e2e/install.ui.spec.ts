// maktaba.tv's install pages in Chromium (Epic 59 story 59.14).
//   T04: the install page fills the compose file in from the answers, the same file the
//        generator makes for them, and no request leaves the page carrying an answer.
//   T05: every guide's Arabic page is right to left and mirrored, in the dark theme; each
//        guide's page is photographed dark in both languages (test-results/guides/).
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { expect, test, type Request } from "@playwright/test";
import { fillCompose, type Answers } from "../src/compose";

const here = path.dirname(fileURLToPath(import.meta.url));
const BASE = fs.readFileSync(
  path.resolve(here, "../../../../cloud/installers/docker/docker-compose.yml"),
  "utf8"
);

const GUIDES = [
  "nas",
  "docker",
  "synology",
  "truenas",
  "unraid",
  "qnap",
  "ugreen",
  "after-setup",
  "troubleshooting",
];

test("T04: the compose file is made in the page, and no answer leaves it", async ({ page }) => {
  const requests: Request[] = [];
  page.on("request", (r) => requests.push(r));
  await page.goto("/install/docker/");
  const answers: Answers = {
    user: "1026:100",
    data: "/share/Container/maktaba-e2e",
    media: [{ path: "/share/Multimedia/Films-e2e", readOnly: true }],
    tier: "standard",
    video: "none",
    renderGroup: "",
    tz: "Asia/Riyadh",
    network: "host",
    hostIp: "",
  };
  await page.selectOption("#nas", "qnap");
  await page.fill("#user", answers.user);
  await page.fill("#ram", "8");
  await page.fill("#data", answers.data);
  await page.locator('[name="media-path"]').first().fill(answers.media[0]!.path);
  await page.fill("#tz", answers.tz);
  await expect(page.locator("#out")).toHaveValue(fillCompose(BASE, answers));

  const before = requests.length;
  await page.getByRole("button").first().click(); // copy: stays in the page
  await page.waitForTimeout(300);
  for (const r of requests) {
    const sent = `${r.url()} ${r.postData() ?? ""}`;
    expect(new URL(r.url()).hostname).toBe("localhost");
    for (const secret of ["1026:100", "maktaba-e2e", "Films-e2e", "Asia/Riyadh"])
      expect(sent).not.toContain(secret);
  }
  expect(requests.slice(before).every((r) => r.method() === "GET")).toBe(true);
});

for (const name of GUIDES) {
  test(`T05: ${name} is mirrored in Arabic, dark, in both languages`, async ({ page }) => {
    for (const lang of ["en", "ar"] as const) {
      await page.goto(lang === "ar" ? `/ar/install/${name}/` : `/install/${name}/`);
      const html = page.locator("html");
      await expect(html).toHaveAttribute("dir", lang === "ar" ? "rtl" : "ltr");
      const dir = await page.evaluate(() => getComputedStyle(document.body).direction);
      expect(dir).toBe(lang === "ar" ? "rtl" : "ltr");
      // dark by default: the page's own background, not white
      const bg = await page.evaluate(() => getComputedStyle(document.body).backgroundColor);
      expect(bg).toBe("rgb(15, 17, 21)");
      // mirrored: the heading starts at the right in Arabic, at the left in English
      const box = (await page.locator("h1").boundingBox())!;
      const width = page.viewportSize()!.width;
      const text = await page.evaluate(() => {
        const h = document.querySelector("h1")!;
        const r = document.createRange();
        r.selectNodeContents(h);
        const b = r.getBoundingClientRect();
        return { left: b.left, right: b.right };
      });
      if (lang === "ar") expect(width - text.right).toBeLessThan(text.left);
      else expect(text.left).toBeLessThan(width - text.right);
      expect(box.width).toBeGreaterThan(0);
      await page.screenshot({
        path: path.join(test.info().project.outputDir, "guides", `${name}.${lang}.png`),
        fullPage: true,
      });
    }
  });
}
