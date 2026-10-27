// A video's picture is shown whole in its 16:9 box, as a player shows the
// video: a vertical or square one gets black bars at its sides, an ultra-wide
// one above and below. A crop ("cover") would cut a vertical video's frame.
import { describe, it, expect } from "vitest";

// read from disk, as ServerStarting.test.tsx reads its codes (vitest runs in web/)
const FS = "node:fs";
const { readFileSync } = (await import(/* @vite-ignore */ FS)) as {
  readFileSync: (path: string, encoding: "utf8") => string;
};
const app = readFileSync("src/styles/app.css", "utf8");
const browse = readFileSync("src/styles/browse.css", "utf8");
const big = readFileSync("src/bigscreen/bigscreen.css", "utf8");

/** The declarations of the last rule whose selector list is exactly `selector`. */
function rule(css: string, selector: string): string {
  const esc = selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const all = [...css.matchAll(new RegExp(`(?:^|\\n)${esc} \\{([^}]*)\\}`, "g"))];
  expect(all.length, selector).toBeGreaterThan(0);
  return all[all.length - 1][1];
}

describe("video pictures", () => {
  it.each([
    ["img.mkt-card__poster", app],
    [".mkt-won__poster img:not(.mkt-won__logo)", app],
    [".mkt-series-detail__poster", app],
    [".mkt-episode__thumb img", app],
    [".mkt-libcard__mosaic > img", app],
    [".bs-pic", big],
  ])("%s is shown whole over black bars", (selector, css) => {
    const body = rule(css, selector);
    expect(body).toMatch(/object-fit:\s*contain/);
    expect(body).toMatch(/background:\s*#000/);
  });

  it("the Home hero fills its width, and shows a narrow picture whole", () => {
    expect(rule(browse, ".mkt-hero__art--whole")).toMatch(/object-fit:\s*contain/);
    expect(rule(browse, ".mkt-hero__backdrop")).toMatch(/filter:\s*blur/);
  });
});
