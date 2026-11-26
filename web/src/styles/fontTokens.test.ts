import { describe, expect, it } from "vitest";

const FS = "node:fs";
const { readFileSync } = (await import(/* @vite-ignore */ FS)) as {
  readFileSync: (path: string, encoding: "utf8") => string;
};

describe("design-system font tokens", () => {
  it("maps the component font families to the app typography on the root", () => {
    const tokens = readFileSync("src/styles/tokens.css", "utf8").replace(/\/\*[\s\S]*?\*\//g, "");
    const root = tokens.match(/^:root \{([^}]*)\}/m)?.[1];

    expect(root).toMatch(/^\s*--font-family:\s*[^;\s][^;]*;/m);
    expect(root).toMatch(/^\s*--font-heading:\s*[^;\s][^;]*;/m);
    expect(root).toContain("--type-body-family: var(--font-family);");
    expect(root).toContain("--type-display-family: var(--font-heading);");
  });
  it.each([
    ["src/main.tsx", "./styles/tokens.css"],
    ["src/tv/main.tsx", "../styles/tokens.css"],
    ["cloud/src/main.tsx", "../../src/styles/tokens.css"],
    ["admin/src/main.tsx", "../../src/styles/tokens.css"],
  ])("%s loads the shared tokens", (entrypoint, stylesheet) => {
    const source = readFileSync(entrypoint, "utf8").replace(/\/\*[\s\S]*?\*\//g, "");
    const imports = [...source.matchAll(/^\s*import ["']([^"']+)["'];?$/gm)];

    expect(imports.map((match) => match[1])).toContain(stylesheet);
  });
});
