// Story 59.09: the guide's DSM screenshots, listed by the device checklist.
import { describe, expect, it } from "vitest";
import { SCREENS } from "./synologyGuide";
import en from "../i18n/en.json";
import ar from "../i18n/ar.json";

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

  it("have their files once marked ready, and a description in both catalogs", () => {
    for (const s of SCREENS) {
      for (const lang of ["en", "ar"]) {
        const file = `cloud/public/install/synology/${s.id}.${lang}.png`;
        if (s.ready) expect(existsSync(file), file).toBe(true);
      }
      expect((en as Record<string, string>)[`install.syn.shot.${s.id}`]).toBeTruthy();
      expect((ar as Record<string, string>)[`install.syn.shot.${s.id}`]).toBeTruthy();
    }
  });
});
