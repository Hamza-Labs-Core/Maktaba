// The dark theme's own surfaces for the design system's light-grey parts: a loading
// placeholder (Skeleton) drew a white box on the dark Home and library pages.
import { describe, it, expect } from "vitest";

const FS = "node:fs";
const { readFileSync } = (await import(/* @vite-ignore */ FS)) as {
  readFileSync: (path: string, encoding: "utf8") => string;
};
const tokens = readFileSync("src/styles/tokens.css", "utf8");
const DARK = ':is([data-theme="dark"], :root:not([data-theme]))';

/** The declarations of the rule whose selector list is exactly `selector`. */
function rule(css: string, selector: string): string {
  const esc = selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const m = css.match(new RegExp(`${esc} \\{([^}]*)\\}`));
  expect(m, selector).not.toBeNull();
  return m![1];
}

describe("dark surfaces", () => {
  it("the loading placeholder shimmers on the dark surfaces, not light greys", () => {
    const decl = rule(tokens, `${DARK} .mk-skeleton`);
    expect(decl).toContain("background-color: var(--color-subtle)");
    expect(decl).toContain("var(--color-border)");
    expect(decl).not.toMatch(/neutral-(100|200)|#e4e4e7|#f4f4f5/);
  });

  it("every dark override names the dark theme as the tokens do", () => {
    const overrides = tokens.slice(tokens.indexOf("/* The design-system surfaces"));
    expect(overrides).not.toMatch(/(^|\n)\[data-theme="dark"\] \./);
  });
});
