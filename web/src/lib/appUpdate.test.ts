// Story 28.5 — the mobile app's own update check (T01–T09): compare, throttle, dismiss
// until a newer version, the server source first, the GitHub fallback, the action per
// platform. Every source is a fake: no network.
import { beforeEach, describe, expect, it, vi, type Mock } from "vitest";
import {
  PLAY_STORE_URL,
  actionUrl,
  appUpdateToOffer,
  checkDue,
  dismissVersion,
  isDismissed,
  readFrequency,
  writeFrequency,
  type AppRelease,
  type CheckDeps,
} from "./appUpdate";
import type { UpdateStatus } from "./updates";

const DAY = 86_400_000;
const T0 = Date.UTC(2026, 9, 25, 9, 0, 0);

const server = (latest: string, extra: Partial<UpdateStatus> = {}): UpdateStatus => ({
  available: true,
  current_version: "1.4.0",
  latest_version: latest,
  channel: "stable",
  release_url: `https://github.com/Hamza-Labs-Core/Maktaba/releases/tag/v${latest}`,
  checked_at: new Date(T0).toISOString(),
  ...extra,
});

const github = (tag: string, init: { status?: number; headers?: Record<string, string> } = {}) =>
  new Response(
    JSON.stringify({
      tag_name: tag,
      html_url: `https://github.com/Hamza-Labs-Core/Maktaba/releases/tag/${tag}`,
      draft: false,
      prerelease: false,
      assets: [
        {
          name: `maktaba-${tag.slice(1)}.apk`,
          browser_download_url: `https://github.com/Hamza-Labs-Core/Maktaba/releases/download/${tag}/maktaba-${tag.slice(1)}.apk`,
        },
      ],
    }),
    { status: init.status ?? 200, headers: init.headers }
  );

const offline = () => Promise.reject(new TypeError("Failed to fetch"));

type Fakes = CheckDeps & { serverStatus: Mock; fetchFn: Mock };

function deps(over: Partial<Fakes> = {}): Fakes {
  return {
    now: () => T0,
    store: localStorage,
    serverStatus: vi.fn(() => Promise.resolve(server("1.4.2"))),
    fetchFn: vi.fn(offline),
    ...over,
  };
}

describe("the app's update check", () => {
  beforeEach(() => localStorage.clear());

  it("T01/T08: offers a newer release from the server", async () => {
    const d = deps();
    const rel = await appUpdateToOffer("1.4.1", d);
    expect(rel?.version).toBe("1.4.2");
    expect(rel?.source).toBe("server");
    expect(rel?.releaseUrl).toBe("https://github.com/Hamza-Labs-Core/Maktaba/releases/tag/v1.4.2");
    expect(d.fetchFn).not.toHaveBeenCalled();
  });

  it("T02: offers nothing on the latest version, or ahead of it (a test build)", async () => {
    expect(await appUpdateToOffer("1.4.2", deps())).toBeNull();
    localStorage.clear();
    expect(await appUpdateToOffer("1.5.0-rc.1", deps())).toBeNull();
    localStorage.clear();
    expect(await appUpdateToOffer("dev", deps())).toBeNull();
  });

  it("T03/T04: a dismissed version stays hidden; a newer one shows again", async () => {
    dismissVersion("v1.4.2");
    expect(isDismissed("1.4.2")).toBe(true);
    expect(await appUpdateToOffer("1.4.1", deps())).toBeNull();
    localStorage.removeItem("mkt:update:lastCheck");
    const d = deps({ serverStatus: vi.fn(() => Promise.resolve(server("1.4.3"))) });
    expect((await appUpdateToOffer("1.4.1", d))?.version).toBe("1.4.3");
  });

  it("T05: checks at most once a day (weekly: once a week), and keeps what it found", async () => {
    let now = T0;
    const d = deps({ now: () => now });
    await appUpdateToOffer("1.4.1", d);
    now = T0 + DAY - 1;
    const again = await appUpdateToOffer("1.4.1", d);
    expect(d.serverStatus).toHaveBeenCalledTimes(1);
    // the banner stays on the later launches of the window
    expect(again?.version).toBe("1.4.2");
    now = T0 + DAY;
    await appUpdateToOffer("1.4.1", d);
    expect(d.serverStatus).toHaveBeenCalledTimes(2);

    writeFrequency("weekly");
    expect(checkDue("weekly", T0 + 2 * DAY, localStorage)).toBe(false);
    expect(checkDue("weekly", T0 + 8 * DAY, localStorage)).toBe(true);
  });

  it("checks nothing and offers nothing when checks are off", async () => {
    const d = deps();
    await appUpdateToOffer("1.4.1", d); // found, kept
    writeFrequency("off");
    localStorage.removeItem("mkt:update:lastCheck");
    expect(readFrequency()).toBe("off");
    expect(await appUpdateToOffer("1.4.1", d)).toBeNull();
    expect(d.serverStatus).toHaveBeenCalledTimes(1);
  });

  it("T09: falls back to GitHub's latest stable release when no server answers", async () => {
    const d = deps({
      serverStatus: vi.fn(offline),
      fetchFn: vi.fn(() => Promise.resolve(github("v1.4.2"))),
    });
    const rel = await appUpdateToOffer("1.4.1", d);
    expect(rel?.source).toBe("github");
    expect(rel?.version).toBe("1.4.2");
    expect(rel?.apkUrl).toBe(
      "https://github.com/Hamza-Labs-Core/Maktaba/releases/download/v1.4.2/maktaba-1.4.2.apk"
    );
    expect(d.fetchFn.mock.calls[0]?.[0]).toBe(
      "https://api.github.com/repos/Hamza-Labs-Core/Maktaba/releases/latest"
    );
  });

  it("falls back to GitHub when the server has no latest version yet, never past checks off", async () => {
    const pending = deps({
      serverStatus: vi.fn(() =>
        Promise.resolve(server("", { latest_version: undefined, available: false }))
      ),
      fetchFn: vi.fn(() => Promise.resolve(github("v1.4.2"))),
    });
    expect((await appUpdateToOffer("1.4.1", pending))?.source).toBe("github");

    localStorage.clear();
    const off = deps({
      serverStatus: vi.fn(() => Promise.resolve(server("", { disabled: true }))),
      fetchFn: vi.fn(() => Promise.resolve(github("v1.4.2"))),
    });
    expect(await appUpdateToOffer("1.4.1", off)).toBeNull();
    expect(off.fetchFn).not.toHaveBeenCalled();
  });

  it("prefers the server when both would answer (it is the deployment's word)", async () => {
    const d = deps({
      serverStatus: vi.fn(() => Promise.resolve(server("1.4.2"))),
      fetchFn: vi.fn(() => Promise.resolve(github("v1.5.0"))),
    });
    expect((await appUpdateToOffer("1.4.1", d))?.version).toBe("1.4.2");
  });

  it("is silent offline and tries again at the next launch", async () => {
    let now = T0;
    const d = deps({ now: () => now, serverStatus: vi.fn(offline), fetchFn: vi.fn(offline) });
    expect(await appUpdateToOffer("1.4.1", d)).toBeNull();
    now = T0 + 60_000;
    d.serverStatus.mockImplementation(() => Promise.resolve(server("1.4.2")));
    expect((await appUpdateToOffer("1.4.1", d))?.version).toBe("1.4.2");
  });

  it("backs off GitHub's rate limit until its reset", async () => {
    let now = T0;
    const reset = Math.floor((T0 + 3 * 3_600_000) / 1000);
    const d = deps({
      now: () => now,
      serverStatus: vi.fn(offline),
      fetchFn: vi.fn(() =>
        Promise.resolve(
          github("v1.4.2", { status: 403, headers: { "x-ratelimit-reset": String(reset) } })
        )
      ),
    });
    expect(await appUpdateToOffer("1.4.1", d)).toBeNull();
    now = T0 + 3_600_000;
    await appUpdateToOffer("1.4.1", d);
    expect(d.fetchFn).toHaveBeenCalledTimes(1);
    now = reset * 1000;
    d.fetchFn.mockImplementation(() => Promise.resolve(github("v1.4.2")));
    expect((await appUpdateToOffer("1.4.1", d))?.version).toBe("1.4.2");
  });

  it("never offers a GitHub prerelease, a draft or a tag it cannot read", async () => {
    for (const body of [
      { tag_name: "v1.5.0-rc.1", prerelease: false },
      { tag_name: "v1.5.0", prerelease: true },
      { tag_name: "v1.5.0", draft: true },
      { tag_name: "nightly" },
    ]) {
      localStorage.clear();
      const d = deps({
        serverStatus: vi.fn(offline),
        fetchFn: vi.fn(() => Promise.resolve(new Response(JSON.stringify(body)))),
      });
      expect(await appUpdateToOffer("1.4.1", d)).toBeNull();
    }
  });
});

describe("the banner's action", () => {
  const rel: AppRelease = {
    version: "1.4.2",
    releaseUrl: "https://github.com/Hamza-Labs-Core/Maktaba/releases/tag/v1.4.2",
    apkUrl: "https://github.com/Hamza-Labs-Core/Maktaba/releases/download/v1.4.2/maktaba-1.4.2.apk",
    source: "server",
  };

  it("T06: opens the App Store on iOS (the release page while the build knows none)", () => {
    expect(actionUrl(rel, "ios", "sideload", "https://apps.apple.com/app/id123")).toBe(
      "https://apps.apple.com/app/id123"
    );
    expect(actionUrl(rel, "ios", "sideload", null)).toBe(rel.releaseUrl);
  });

  it("T07: opens the Play Store for the store build and the .apk for the sideload build", () => {
    expect(actionUrl(rel, "android", "store")).toBe(PLAY_STORE_URL);
    expect(actionUrl(rel, "android", "sideload")).toBe(rel.apkUrl);
    expect(actionUrl(rel, "web", "store")).toBe(rel.releaseUrl);
  });
});
