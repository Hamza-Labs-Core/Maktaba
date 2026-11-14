// T03 (story 58.07): the home's LAN address times out, the receiver falls back to the relay
// from the discovery list, and playing through it is Remote.

import { describe, expect, it } from "vitest";
import {
  cleanBase,
  HOME_TIMEOUT_MS,
  HomeUnreachable,
  relayOf,
  resolveHome,
} from "../src/receiver/home";
import { fakeHome } from "./fakeCaf";
import { discovery, HOME, RELAY } from "./fixtures";

describe("resolveHome (T03)", () => {
  it("uses the home's address when it answers", async () => {
    const h = fakeHome({ [`GET ${HOME}/api/discovery/home`]: { body: discovery } });
    await expect(resolveHome(HOME, [HOME, RELAY], h.fetch)).resolves.toMatchObject({
      base: HOME,
      remote: false,
    });
  });

  it("falls back to the relay when the home's address times out", async () => {
    const h = fakeHome({
      [`GET ${HOME}/api/discovery/home`]: { hang: true },
      [`GET ${RELAY}/api/discovery/home`]: { body: discovery },
    });
    const started = Date.now();
    const got = await resolveHome(HOME, [HOME, RELAY], h.fetch, 50);
    expect(got).toMatchObject({ base: RELAY, remote: true });
    expect(Date.now() - started).toBeLessThan(2000);
    expect(h.calls.map((c) => c.url)).toEqual([
      `${HOME}/api/discovery/home`,
      `${RELAY}/api/discovery/home`,
    ]);
  });

  it("waits 3 seconds for the home by default", () => {
    expect(HOME_TIMEOUT_MS).toBe(3000);
  });

  it("falls back on an error answer too", async () => {
    const h = fakeHome({
      [`GET ${HOME}/api/discovery/home`]: { status: 502 },
      [`GET ${RELAY}/api/discovery/home`]: { body: discovery },
    });
    await expect(resolveHome(HOME, [HOME, RELAY], h.fetch)).resolves.toMatchObject({
      base: RELAY,
      remote: true,
    });
  });

  it("shows Remote when the sender's address is the relay itself", async () => {
    const h = fakeHome({ [`GET ${RELAY}/api/discovery/home`]: { body: discovery } });
    await expect(resolveHome(RELAY, undefined, h.fetch)).resolves.toMatchObject({
      base: RELAY,
      remote: true,
    });
  });

  it("never tries plain HTTP (mixed content), and says so when nothing answers", async () => {
    const h = fakeHome({});
    const err = await resolveHome(
      "http://192.168.1.20:8080",
      ["http://192.168.1.20:8080"],
      h.fetch
    ).catch((e) => e);
    expect(err).toBeInstanceOf(HomeUnreachable);
    expect(h.calls).toHaveLength(0);
    const both = await resolveHome(HOME, [HOME, RELAY], fakeHome({}).fetch).catch((e) => e);
    expect((both as HomeUnreachable).tried).toEqual([HOME, RELAY]);
  });
});

describe("the addresses", () => {
  it("are HTTPS bases without a trailing slash", () => {
    expect(cleanBase(`${HOME}/`)).toBe(HOME);
    expect(cleanBase("http://h.test")).toBe("");
    expect(cleanBase("not a url")).toBe("");
    expect(cleanBase(42)).toBe("");
  });

  it("name the relay last, when there is more than one", () => {
    expect(relayOf([HOME, RELAY])).toBe(RELAY);
    expect(relayOf([HOME])).toBe("");
    expect(relayOf([HOME, HOME])).toBe("");
    expect(relayOf(undefined)).toBe("");
  });
});
