// Story 59.09: the guide's DSM screenshots, listed by the device checklist.
import { describe, expect, it } from "vitest";
import { SCREENS, screenSrc } from "./synologyGuide";
import { SYN } from "./synologyStrings";

// read from disk, as posters.test.ts does (vitest runs in web/)
const FS = "node:fs";
const { existsSync, readFileSync } = (await import(/* @vite-ignore */ FS)) as {
  existsSync: (path: string) => boolean;
  readFileSync: (path: string, encoding: "utf8") => string;
};

describe("the Synology guide's DSM screenshots", () => {
  it("are each named by the device checklist, in both languages", () => {
    const checklist = readFileSync("../docs/nas/checklists/synology.md", "utf8");
    for (const s of SCREENS) {
      expect(checklist).toContain(`synology/${s.id}.en.png`);
      expect(checklist).toContain(`synology/${s.id}.ar.png`);
    }
  });

  it("have their files once marked ready, and a description in both languages", () => {
    for (const s of SCREENS) {
      for (const lang of ["en", "ar"] as const) {
        expect(screenSrc(s.id, lang)).toBe(`/install/synology/${s.id}.${lang}.png`);
        const file = `packages/install/public${screenSrc(s.id, lang)}`;
        if (s.ready) expect(existsSync(file), file).toBe(true);
        expect(SYN[lang][`shot.${s.id}`]).toBeTruthy();
      }
    }
  });
});
