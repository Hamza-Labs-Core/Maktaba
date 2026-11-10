// Story 59.09 T04: the install page's answers → the Container Manager project's YAML.
import { describe, expect, it } from "vitest";
import {
  body,
  DEFAULTS,
  fitFor,
  MEM_LIMIT,
  MODELS,
  problems,
  renderCompose,
  type Answers,
} from "./synology";

// read from disk, as posters.test.ts does (vitest runs in web/)
const FS = "node:fs";
const { readFileSync } = (await import(/* @vite-ignore */ FS)) as {
  readFileSync: (path: string, encoding: "utf8") => string;
};
const deploy = (name: string) => readFileSync(`../deploy/nas/synology/${name}`, "utf8");

describe("the Synology compose file", () => {
  it("is, with the defaults, exactly the files deploy/nas/generate.py writes", () => {
    expect(body(renderCompose(DEFAULTS))).toBe(body(deploy("compose.yaml")));
    expect(body(renderCompose({ ...DEFAULTS, gpu: true }))).toBe(
      body(deploy("compose.j4125.yaml"))
    );
  });

  it("fills in the uid, the shares, the tier, the address and the time zone", () => {
    const a: Answers = {
      ...DEFAULTS,
      uid: "1030",
      tier: "light",
      volume: "volume2",
      shares: [
        { path: "/volume1/أفلام", readOnly: true },
        { path: "/volume3/TV Shows", readOnly: false },
      ],
      hostIp: "192.168.1.20",
      tz: "Asia/Riyadh",
      tag: "v0.3.130",
    };
    const y = renderCompose(a, "DS923+, 4 GB");
    expect(y).toContain("image: ghcr.io/hamza-labs-core/maktaba-server:v0.3.130\n");
    expect(y).toContain('user: "1030:100"\n');
    expect(y).toContain("mem_limit: 4g\n");
    expect(y).toContain("      - /volume2/docker/maktaba:/var/lib/maktaba\n");
    expect(y).toContain("      - /volume1/أفلام:/volume1/أفلام:ro\n");
    expect(y).toContain("      - /volume3/TV Shows:/volume3/TV Shows\n");
    expect(y).toContain("      MAKTABA_HOST_IP: 192.168.1.20\n");
    expect(y).toContain("      TZ: Asia/Riyadh\n");
    expect(y).toContain("from your answers: DS923+, 4 GB.");
    expect(y).not.toContain("devices:");
    expect(y).not.toContain("group_add:");
  });

  it("passes /dev/dri and the render group only on the J4125 version", () => {
    const y = renderCompose({ ...DEFAULTS, gpu: true, renderGid: "105" });
    expect(y).toContain('    devices:\n      - /dev/dri:/dev/dri\n    group_add:\n      - "105"\n');
  });

  it("refuses what cannot go in the file", () => {
    const names = (a: Partial<Answers>) => problems({ ...DEFAULTS, ...a }).map((p) => p.problem);
    expect(names({})).toEqual([]);
    expect(names({ uid: "0" })).toEqual(["uid"]);
    expect(names({ uid: "abc" })).toEqual(["uid"]);
    expect(names({ gpu: true, renderGid: "" })).toEqual(["renderGid"]);
    expect(names({ volume: "volumeX" })).toEqual(["volume"]);
    expect(names({ shares: [] })).toEqual(["shares"]);
    for (const bad of ["/srv/media", "/volume1", "/volume1/a:b", "/volume1/#x", "/volume1/ x"]) {
      expect(names({ shares: [{ path: bad, readOnly: true }] })).toEqual(["sharePath"]);
    }
    expect(
      names({
        shares: [
          { path: "/volume1/Movies", readOnly: true },
          { path: "/volume1/Movies", readOnly: false },
        ],
      })
    ).toEqual(["shareTwice"]);
    expect(names({ hostIp: "192.168.1.300" })).toEqual(["hostIp"]);
    expect(names({ tz: "Asia Riyadh" })).toEqual(["tz"]);
    expect(names({ tag: "0.3.1" })).toEqual(["tag"]);
  });
});

describe("the model table", () => {
  it("follows the server's tiers on the memory a NAS reports", () => {
    expect(fitFor(1, "arm64")).toBe("storage"); // DS223j: setup refuses
    expect(fitFor(2, "arm64")).toBe("storage"); // DS223
    expect(fitFor(2, "amd64")).toBe("storage"); // a DS425+ as it ships
    expect(fitFor(4, "amd64")).toBe("light"); // DS923+ (3.6 GiB, over the 3.5 floor)
    expect(fitFor(6, "amd64")).toBe("light"); // DS425+ expanded: under 6 GiB
    expect(fitFor(8, "amd64")).toBe("standard");
    expect(fitFor(8, "arm64")).toBe("light");
    expect(fitFor(16, "amd64")).toBe("standard");
    expect(fitFor(32, "amd64")).toBe("strong");
    expect(MEM_LIMIT).toEqual({ light: "4g", standard: "6g", strong: "16g" });
  });

  it("has the checklist's three models, the J4125s with Quick Sync", () => {
    const by = Object.fromEntries(MODELS.map((m) => [m.name, m]));
    expect(by["DS425+"]).toMatchObject({ gpu: true, arch: "amd64" });
    expect(by["DS923+"]).toMatchObject({ gpu: false, arch: "amd64", ram: 4 });
    expect(by["DS223"]).toMatchObject({ arch: "arm64", maxRam: 2 });
    for (const m of MODELS) expect(m.gpu).toBe(m.cpu.includes("J4125"));
  });
});
