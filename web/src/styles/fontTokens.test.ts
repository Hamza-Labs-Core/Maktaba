import { describe, expect, it } from "vitest";

const FS = "node:fs";
const { readFileSync } = (await import(/* @vite-ignore */ FS)) as {
  readFileSync: (path: string, encoding: "utf8") => string;
};

describe("design-system font tokens", () => {
  it("maps the component font families to the app typography on the root", () => {
    const tokens = readFileSync("src/styles/tokens.css", "utf8");
    const root = tokens.match(/^:root \{([^}]*)\}/m)?.[1];

    expect(root).toContain("--type-body-family: var(--font-family);");
    expect(root).toContain("--type-display-family: var(--font-heading);");
  });
});
