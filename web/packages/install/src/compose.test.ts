// Story 59.11 T04-like checks of the install page's generator, on the real published file
// (CI also runs `docker compose config` on every block variant: tests/nas/install_files.py).
import BASE from "../../../../cloud/installers/docker/docker-compose.yml?raw";
import { describe, expect, it } from "vitest";
import { blocks, enable, fillCompose, MEM_LIMIT, problems, tierFor, type Answers } from "./compose";

const answers = (over: Partial<Answers> = {}): Answers => ({
  user: "1000:100",
  data: "/volume1/docker/maktaba",
  media: [
    { path: "/volume1/Movies", readOnly: true },
    { path: "/volume1/Recordings", readOnly: false },
  ],
  tier: "standard",
  video: "none",
  renderGroup: "",
  tz: "Asia/Riyadh",
  network: "host",
  hostIp: "",
  ...over,
});

describe("the published compose's blocks", () => {
  it("has the blocks the page turns on", () => {
    const b = blocks(BASE);
    for (const n of ["memory", "bridge", "dbus", "dri", "nvidia"])
      expect(b.get(n)?.length, n).toBeGreaterThan(0);
  });

  it("keeps explanations as comments", () => {
    const on = enable(BASE, ["dri"]);
    expect(on).toContain("    # [dri] Hardware video");
    expect(on).toContain("    devices:\n      - /dev/dri:/dev/dri");
  });
});

describe("fillCompose", () => {
  it("fills in the user, tier, time zone, data and media at their own paths", () => {
    const out = fillCompose(BASE, answers());
    expect(out).toContain('user: "1000:100"');
    expect(out).toContain("    mem_limit: 6g");
    expect(out).toContain("TZ: Asia/Riyadh");
    expect(out).toContain("      - /volume1/docker/maktaba:/var/lib/maktaba");
    expect(out).toContain(
      "      - /volume1/Movies:/volume1/Movies:ro\n      - /volume1/Recordings:/volume1/Recordings\n"
    );
    expect(out).not.toContain("/srv/media");
    // the named volume goes with a data folder
    expect(out).not.toMatch(/^volumes:/m);
    expect(out).toContain("network_mode: host");
    expect(out).toContain("MAKTABA_PLATFORM: docker");
    expect(out).toContain("    # devices:");
  });

  it("keeps the named volume without a data folder", () => {
    const out = fillCompose(BASE, answers({ data: "" }));
    expect(out).toContain("- maktaba:/var/lib/maktaba");
    expect(out).toMatch(/^volumes:\n {2}maktaba:/m);
  });

  it.each([
    ["light", "4g"],
    ["standard", "6g"],
    ["strong", "16g"],
  ] as const)("sets %s's memory limit", (tier, limit) => {
    expect(MEM_LIMIT[tier]).toBe(limit);
    expect(fillCompose(BASE, answers({ tier }))).toContain(`    mem_limit: ${limit}`);
  });

  it("sets no limit for none", () => {
    expect(fillCompose(BASE, answers({ tier: "none" }))).toContain("    # mem_limit: 6g");
  });

  it("turns the render device on with its group", () => {
    const out = fillCompose(BASE, answers({ video: "intel-amd", renderGroup: "105" }));
    expect(out).toContain('    devices:\n      - /dev/dri:/dev/dri\n    group_add:\n      - "105"');
  });

  it("turns NVIDIA on", () => {
    const out = fillCompose(BASE, answers({ video: "nvidia" }));
    expect(out).toContain("    deploy:\n      resources:");
    expect(out).toContain("            - driver: nvidia");
  });

  it("makes the bridge variant: ports, the host's address, no host network, no Postgres port", () => {
    const out = fillCompose(BASE, answers({ network: "bridge", hostIp: "192.168.1.20" }));
    expect(out).not.toContain("network_mode: host");
    expect(out).toContain('    ports:\n      - "8088:8088"');
    expect(out).toContain("      MAKTABA_HOST_IP: 192.168.1.20");
    expect(out).not.toMatch(/^\s+- "54329/m);
  });

  it("never mounts the D-Bus socket", () => {
    expect(
      fillCompose(
        BASE,
        answers({ video: "intel-amd", renderGroup: "1", network: "bridge", hostIp: "10.0.0.2" })
      )
    ).toContain("      # - /var/run/dbus/system_bus_socket");
  });

  it("refuses answers with problems", () => {
    expect(() => fillCompose(BASE, answers({ user: "0:0" }))).toThrow(/user-root/);
  });
});

describe("problems", () => {
  it("accepts good answers", () => expect(problems(answers())).toEqual([]));
  it.each([
    [{ user: "admin" }, "user"],
    [{ user: "0:100" }, "user-root"],
    [{ data: "volume1/x" }, "data"],
    [{ data: "/a:b" }, "data"],
    [{ media: [{ path: "/", readOnly: false }] }, "media"],
    [{ media: [{ path: "  ", readOnly: false }] }, "media-none"],
    [{ video: "intel-amd" as const, renderGroup: "render" }, "render-group"],
    [{ tz: "Asia Riyadh" }, "tz"],
    [{ network: "bridge" as const, hostIp: "nas.local" }, "host-ip"],
  ])("%j is %s", (over, want) => expect(problems(answers(over))).toContain(want));
});

describe("tierFor", () => {
  it.each([
    [4, "light"],
    [6, "light"],
    [8, "standard"],
    [16, "standard"],
    [32, "strong"],
    [0, "none"],
  ] as const)("%d GB is %s", (gb, tier) => expect(tierFor(gb)).toBe(tier));
});
