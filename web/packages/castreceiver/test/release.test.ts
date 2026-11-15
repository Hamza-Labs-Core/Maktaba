// @vitest-environment node
// The release's first step (scripts/release.mjs fetch, story 58.07): the live site's manifest.
// A first release, before cast.maktaba.tv exists, reads "no builds" and goes on; a live site that
// answers badly fails the release, so a broken site is never overwritten as if it were empty.

import { describe, expect, it } from "vitest";
import { fetchLive } from "../scripts/release.mjs";

const BASE = "https://cast.maktaba.tv";

/** undici's shape for a network failure: TypeError("fetch failed") with the cause's code. */
function netError(code: string): TypeError {
  const cause = Object.assign(new Error(`getaddrinfo ${code} cast.maktaba.tv`), { code });
  return new TypeError("fetch failed", { cause });
}

const throwing = (e: unknown): typeof fetch =>
  (async () => {
    throw e;
  }) as typeof fetch;

const answering = (status: number, body: string): typeof fetch =>
  (async () =>
    new Response(body, {
      status,
      headers: { "content-type": "application/json" },
    })) as typeof fetch;

describe("nothing deployed yet: no previous builds", () => {
  it("the name does not resolve (ENOTFOUND)", async () => {
    const live = await fetchLive(BASE, throwing(netError("ENOTFOUND")));
    expect(live.builds).toEqual([]);
    expect(live.reason).toMatch(/does not resolve \(ENOTFOUND\)/);
  });

  it("the manifest answers 404", async () => {
    const live = await fetchLive(BASE, answering(404, "not found"));
    expect(live.builds).toEqual([]);
    expect(live.reason).toMatch(/404/);
  });
});

describe("a live site that answers badly fails the release", () => {
  it.each(["EAI_AGAIN", "ECONNREFUSED", "ECONNRESET", "ETIMEDOUT", "CERT_HAS_EXPIRED"])(
    "a network error that is not an absent name (%s)",
    async (code) => {
      await expect(fetchLive(BASE, throwing(netError(code)))).rejects.toThrow(code);
    }
  );

  it("a fetch error with no code", async () => {
    await expect(fetchLive(BASE, throwing(new TypeError("fetch failed")))).rejects.toThrow(
      /fetch failed/
    );
  });

  it.each([500, 502, 503, 530])("a %i answer", async (status) => {
    await expect(fetchLive(BASE, answering(status, "oops"))).rejects.toThrow(`answered ${status}`);
  });

  it("a 403 answer", async () => {
    await expect(fetchLive(BASE, answering(403, "denied"))).rejects.toThrow("answered 403");
  });

  it("a manifest that is not JSON", async () => {
    await expect(fetchLive(BASE, answering(200, "<html>"))).rejects.toThrow(/not JSON/);
  });

  it.each([
    ["no builds list", "{}"],
    ["builds not a list", '{"builds": {}}'],
    ["a build without its level", '{"builds": [{"path": "/r/3/"}]}'],
    ["null", "null"],
  ])("a manifest with %s", async (_name, body) => {
    await expect(fetchLive(BASE, answering(200, body))).rejects.toThrow(/not a builds manifest/);
  });
});

describe("a deployed site", () => {
  it("returns its builds", async () => {
    const builds = [{ level: 3, path: "/r/3/", hash: "abc", built: "2026-11-01T00:00:00Z" }];
    const live = await fetchLive(BASE, answering(200, JSON.stringify({ builds })));
    expect(live).toEqual({ builds, reason: "" });
  });

  it("an empty list is a valid deployed manifest", async () => {
    expect(await fetchLive(BASE, answering(200, '{"builds": []}'))).toEqual({
      builds: [],
      reason: "",
    });
  });
});
