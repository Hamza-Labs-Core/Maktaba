// Story 58.09 T01 — the subnet probe with a fake fetch: 32 requests at a time, each with a
// 700 ms timeout, one home per address, several homes listed.
import { probeSubnet, subnetHosts, PROBE_CONCURRENCY, PROBE_TIMEOUT_MS } from "./probe";
import type { FetchLike } from "./discovery";

function json(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });
}

describe("subnetHosts", () => {
  it("is the other 253 addresses of the /24", () => {
    const hosts = subnetHosts("192.168.1.42");
    expect(hosts).toHaveLength(253);
    expect(hosts[0]).toBe("192.168.1.1");
    expect(hosts).not.toContain("192.168.1.42");
    expect(hosts[hosts.length - 1]).toBe("192.168.1.254");
  });
  it("is empty for what is not an IPv4 address", () => {
    expect(subnetHosts("fe80::1")).toEqual([]);
    expect(subnetHosts("300.1.1.1")).toEqual([]);
  });
});

describe("probeSubnet (T01)", () => {
  afterEach(() => vi.useRealTimers());

  it("asks 32 at a time and lists every home, each once", async () => {
    let inFlight = 0;
    let most = 0;
    const asked: string[] = [];
    const fetch: FetchLike = async (url) => {
      asked.push(url);
      inFlight++;
      most = Math.max(most, inFlight);
      await new Promise((r) => setTimeout(r, 1));
      inFlight--;
      if (url.startsWith("http://10.0.0.7:8088/"))
        return json({ id: "home-a", name: "Den", urls: [] });
      if (url.startsWith("http://10.0.0.9:8088/"))
        return json({ id: "home-b", name: "Office", urls: [] });
      // a second address of home-a (two network cards on one LAN): listed once
      if (url.startsWith("http://10.0.0.20:8088/"))
        return json({ id: "home-a", name: "Den", urls: [] });
      if (url.startsWith("http://10.0.0.30:8088/"))
        return new Response("not a home", { status: 200 });
      throw new TypeError("connection refused");
    };
    const found: string[] = [];
    const homes = await probeSubnet("10.0.0.5", { fetch, onFound: (h) => found.push(h.answer.id) });
    expect(PROBE_CONCURRENCY).toBe(32);
    expect(most).toBe(32);
    expect(asked).toHaveLength(253);
    expect(asked.every((u) => /^http:\/\/10\.0\.0\.\d+:8088\/api\/discovery\/home$/.test(u))).toBe(
      true
    );
    expect(homes.map((h) => [h.answer.id, h.base])).toEqual([
      ["home-a", "http://10.0.0.7:8088"],
      ["home-b", "http://10.0.0.9:8088"],
    ]);
    expect(found).toEqual(["home-a", "home-b"]);
  });

  it("gives each address 700 ms: a silent address does not hold the scan", async () => {
    vi.useFakeTimers();
    expect(PROBE_TIMEOUT_MS).toBe(700);
    const signals: AbortSignal[] = [];
    const fetch: FetchLike = (url, init) => {
      if (init?.signal) signals.push(init.signal);
      if (url.startsWith("http://10.0.0.2:8088/"))
        return Promise.resolve(json({ id: "h", name: "H", urls: [] }));
      return new Promise<Response>(() => {}); // never answers
    };
    const done = probeSubnet("10.0.0.1", { fetch });
    // 253 addresses / 32 at a time = 8 rounds of 700 ms: 5.6 s, inside the 6 s the story allows
    await vi.advanceTimersByTimeAsync(8 * 700 + 50);
    const homes = await done;
    expect(homes.map((h) => h.answer.id)).toEqual(["h"]);
    expect(signals.length).toBe(253);
    expect(signals.filter((s) => s.aborted).length).toBe(252);
  });

  it("stops at the first port that answers on an address", async () => {
    const asked: string[] = [];
    const fetch: FetchLike = async (url) => {
      asked.push(url);
      if (url.startsWith("http://10.0.0.3:")) return json({ id: "h", name: "H", urls: [] });
      throw new TypeError("refused");
    };
    await probeSubnet("10.0.0.1", { fetch, ports: [8088, 80] });
    expect(asked.filter((u) => u.startsWith("http://10.0.0.3:"))).toEqual([
      "http://10.0.0.3:8088/api/discovery/home",
    ]);
    expect(asked.filter((u) => u.startsWith("http://10.0.0.4:"))).toHaveLength(2);
  });
});
