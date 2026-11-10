// Story 59.09: the household's answers → the Container Manager project's YAML.
import BASE from "../../../../cloud/installers/docker/docker-compose.yml?raw";
import { describe, expect, it } from "vitest";
import {
  DEFAULTS,
  MODELS,
  VERSION,
  detectedTier,
  limitTier,
  problems,
  synologyCompose,
  type SynAnswers,
} from "./synology";

// read from disk, as posters.test.ts does (vitest runs in web/)
const FS = "node:fs";
const { readFileSync } = (await import(/* @vite-ignore */ FS)) as {
  readFileSync: (path: string, encoding: "utf8") => string;
};
const deploy = (name: string) => readFileSync(`../deploy/nas/synology/${name}`, "utf8");

describe("the Synology project file", () => {
  it("is, with the defaults, exactly the files deploy/nas/generate.py writes", () => {
    expect(synologyCompose(BASE, DEFAULTS, VERSION.other)).toBe(deploy("compose.yaml"));
    expect(synologyCompose(BASE, { ...DEFAULTS, gpu: true }, VERSION.j4125)).toBe(
      deploy("compose.j4125.yaml")
    );
  });

  it("fills in the uid, the volume, the shares, the tier and the time zone", () => {
    const a: SynAnswers = {
      ...DEFAULTS,
      uid: "1030",
      tier: "light",
      volume: "volume2",
      shares: [{ path: "/volume1/TV", readOnly: false }],
      tz: "Asia/Riyadh",
    };
    const y = synologyCompose(BASE, a, VERSION.other);
    expect(y).toContain('user: "1030:100"');
    expect(y).toContain("- /volume2/docker/maktaba:/var/lib/maktaba");
    expect(y).toContain("- /volume1/TV:/volume1/TV\n");
    expect(y).not.toContain("/volume1/Movies");
    expect(y).toContain("mem_limit: 4g");
    expect(y).toContain("TZ: Asia/Riyadh");
    expect(y).toContain("MAKTABA_PLATFORM: dsm");
    expect(y).not.toMatch(/^\s+- \/dev\/dri:\/dev\/dri/m);
    expect(y).not.toMatch(/^volumes:/m);
  });

  it("passes /dev/dri and the render group only for Quick Sync", () => {
    const y = synologyCompose(BASE, { ...DEFAULTS, gpu: true, renderGroup: "105" }, VERSION.j4125);
    expect(y).toMatch(/^\s+- \/dev\/dri:\/dev\/dri$/m);
    expect(y).toMatch(/^\s+- "105"/m);
  });

  it("names what is wrong, and makes no file then", () => {
    const bad: SynAnswers = {
      ...DEFAULTS,
      uid: "0",
      volume: "vol",
      shares: [{ path: "/srv/media", readOnly: true }],
      gpu: true,
      renderGroup: "render",
      tz: "Asia Riyadh",
    };
    expect(problems(bad)).toEqual(
      expect.arrayContaining(["uid", "volume", "share-volume", "render-group", "tz"])
    );
    expect(problems({ ...DEFAULTS, shares: [] })).toContain("media-none");
    expect(problems(DEFAULTS)).toEqual([]);
    expect(() => synologyCompose(BASE, bad, VERSION.other)).toThrow(/problems/);
  });

  it("picks the tier the server will detect, and storage under 3.5 GiB", () => {
    expect(detectedTier(2, "amd64")).toBe("storage");
    expect(detectedTier(4, "amd64")).toBe("light");
    expect(detectedTier(6, "arm64")).toBe("light");
    expect(detectedTier(8, "amd64")).toBe("standard");
    expect(detectedTier(32, "amd64")).toBe("strong");
    expect(detectedTier(32, "amd64", 6)).toBe("standard");
    expect(limitTier(2, "amd64")).toBe("storage");
    expect(limitTier(6, "amd64")).toBe("light");
    expect(limitTier(16, "amd64")).toBe("standard");
    expect(limitTier(32, "amd64")).toBe("strong");
    // each tier's limit is never above what that tier detects (the server plans for no more)
    for (const m of MODELS) {
      const t = limitTier(m.maxRam, m.arch);
      const limit = { light: 4, standard: 6, strong: 16, storage: 0 }[t];
      if (t !== "storage") expect(detectedTier(m.maxRam, m.arch, limit)).not.toBe("storage");
    }
  });
});
