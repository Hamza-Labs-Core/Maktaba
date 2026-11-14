// Story 50.23 T06: geo rules. A channel's allow or deny list and Maktaba's deny list (merged
// into `deny` by the cloud) reach the edge in the channel's ingest token; a viewer outside gets
// 451 geo_blocked at play and at every playlist.
import { afterEach, describe, expect, it } from "vitest";
import { now, resetClock } from "../src/clock";
import { cleanGeo, forgetGeo, geoBlocked } from "../src/geo";
import {
  call,
  ingestToken,
  LIVE,
  newChannel,
  postState,
  putSegment,
  STATE_3,
  viewerToken,
} from "./helpers";

afterEach(() => {
  resetClock();
  forgetGeo();
});

describe("geoBlocked", () => {
  it("lets an allow list in and keeps everyone else out", () => {
    const g = { allow: ["SA", "EG"] };
    expect(geoBlocked(g, "SA")).toBe(false);
    expect(geoBlocked(g, "eg")).toBe(false);
    expect(geoBlocked(g, "DE")).toBe(true);
    // a country Cloudflare could not tell is outside an allow list
    for (const c of ["", "XX", "T1"]) expect(geoBlocked(g, c)).toBe(true);
  });

  it("keeps a deny list out, and a deny wins over an allow", () => {
    expect(geoBlocked({ deny: ["KP"] }, "KP")).toBe(true);
    expect(geoBlocked({ deny: ["KP"] }, "DE")).toBe(false);
    expect(geoBlocked({ deny: ["KP"] }, "")).toBe(false);
    expect(geoBlocked({ allow: ["SA", "KP"], deny: ["KP"] }, "KP")).toBe(true);
    expect(geoBlocked(null, "KP")).toBe(false);
  });

  it("reads only a well-formed claim", () => {
    expect(cleanGeo({ allow: ["SA"], deny: [] })).toEqual({ allow: ["SA"] });
    expect(cleanGeo({ allow: ["sa"] })).toBeNull();
    expect(cleanGeo({ deny: "KP" })).toBeNull();
    expect(cleanGeo(null)).toBeNull();
  });
});

describe("T06 at the edge", () => {
  async function live(geo: Record<string, string[]>) {
    const ch = newChannel();
    const token = await ingestToken({ ch, geo });
    expect((await putSegment(ch, "src", 1, { token })).status).toBe(201);
    return { ch, viewer: await viewerToken({ ch }) };
  }
  const from = (country: string) => ({ headers: { "CF-IPCountry": country } });

  it("answers 451 geo_blocked to a viewer in DE on a channel limited to SA, EG", async () => {
    const { ch, viewer } = await live({ allow: ["SA", "EG"] });
    const master = await call(`${LIVE}/${viewer}/${ch}/master.m3u8`, from("DE"));
    expect(master.status).toBe(451);
    expect(await master.json()).toEqual({ error: "geo_blocked" });
    expect((await call(`${LIVE}/${viewer}/${ch}/src/index.m3u8`, from("DE"))).status).toBe(451);
    expect((await call(`${LIVE}/${viewer}/${ch}/src/index.m3u8`, from("SA"))).status).toBe(200);
  });

  it("applies Maktaba's deny list to every channel", async () => {
    const { ch, viewer } = await live({ deny: ["KP"] });
    expect((await call(`${LIVE}/${viewer}/${ch}/src/index.m3u8`, from("KP"))).status).toBe(451);
    expect((await call(`${LIVE}/${viewer}/${ch}/src/index.m3u8`, from("DE"))).status).toBe(200);
  });

  it("plays everywhere when the token names no rule", async () => {
    const ch = newChannel();
    expect((await putSegment(ch, "src", 1)).status).toBe(201);
    const viewer = await viewerToken({ ch });
    expect((await call(`${LIVE}/${viewer}/${ch}/src/index.m3u8`, from("DE"))).status).toBe(200);
  });

  it("takes the rule from a call that appends no segment (the state post)", async () => {
    const ch = newChannel();
    const token = await ingestToken({ ch, geo: { deny: ["KP"] } });
    expect((await postState(ch, STATE_3, token)).status).toBe(200);
    const viewer = await viewerToken({ ch });
    expect((await call(`${LIVE}/${viewer}/${ch}/master.m3u8`, from("KP"))).status).toBe(451);
  });

  it("never lets an older token's rule replace a newer one's", async () => {
    const ch = newChannel();
    const t = Math.floor(now() / 1000);
    const newer = await ingestToken({ ch, geo: { deny: ["KP", "IR"] }, iat: t });
    const older = await ingestToken({
      ch,
      geo: { deny: ["KP"] },
      iat: t - 600,
      exp: t - 600 + 24 * 3600,
    });
    expect((await putSegment(ch, "src", 1, { token: newer })).status).toBe(201);
    expect((await putSegment(ch, "src", 2, { token: older })).status).toBe(201);
    const viewer = await viewerToken({ ch });
    expect((await call(`${LIVE}/${viewer}/${ch}/src/index.m3u8`, from("IR"))).status).toBe(451);
  });
});
