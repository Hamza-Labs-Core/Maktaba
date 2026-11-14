// Story 58.09's second part — the cloud code's calls and their loop, with a fake fetch and
// fake timers: the code asked for once, polled at the cloud's interval (slower on slow_down),
// renewed when it runs out or is gone, stopped at the approval; the connections a web TV can
// use; an approval without a well-formed grant is not one.
import {
  CLOUD_BASE,
  CodeFlow,
  createCode,
  pollCode,
  RETRY_DOWN_MS,
  usableConnections,
  type CloudApproval,
  type CodeState,
} from "./cloud";
import type { FetchLike } from "./discovery";
import { clearLog, logText } from "./log";

const CODE = {
  code: "ABCD-2345",
  poll_secret: "ps-0123456789abcdef",
  expires_in: 600,
  interval: 5,
  verify_url: "https://maktaba.tv/tv?c=ABCD2345",
};
const GRANT = "Gr4nt_" + "z".repeat(37);
const res = (status: number, body: unknown) => new Response(JSON.stringify(body), { status });

beforeEach(() => clearLog());
afterEach(() => vi.useRealTimers());

describe("the cloud's calls", () => {
  it("asks Maktaba Cloud for a code with the platform and version, by POST with JSON", async () => {
    const calls: { url: string; init?: Parameters<FetchLike>[1] }[] = [];
    const code = await createCode(async (url, init) => {
      calls.push({ url, init });
      return res(201, CODE);
    }, "webos", "1.2.3");
    expect(code).toEqual(CODE);
    expect(calls[0]!.url).toBe(`${CLOUD_BASE}/v1/tv/codes`);
    expect(CLOUD_BASE).toBe("https://api.maktaba.tv");
    expect(calls[0]!.init?.method).toBe("POST");
    expect(calls[0]!.init?.headers?.["Content-Type"]).toBe("application/json");
    expect(JSON.parse(calls[0]!.init!.body!)).toEqual({ platform: "webos", app_version: "1.2.3" });
  });

  it("gives no code when the cloud refuses, is too busy, or does not answer", async () => {
    expect(await createCode(async () => res(429, { error: "rate_limited" }), "tizen", "1")).toBeNull();
    expect(await createCode(async () => res(201, { code: "X" }), "tizen", "1")).toBeNull();
    expect(
      await createCode(async () => Promise.reject(new TypeError("offline")), "tizen", "1")
    ).toBeNull();
  });

  it("reads each poll answer", async () => {
    const poll = (status: number, body: unknown) => pollCode(async () => res(status, body), CODE);
    expect(await poll(202, { status: "pending" })).toEqual({ kind: "pending" });
    expect(await poll(429, { error: "slow_down" })).toEqual({ kind: "slow_down" });
    expect(await poll(410, { error: "expired_token" })).toEqual({ kind: "gone" });
    expect(await poll(404, { error: "not_found" })).toEqual({ kind: "gone" });
    const approved = {
      status: "approved",
      home_id: "h1",
      home_name: "Den",
      connections: [{ url: "https://h1.relay.maktaba.tv", kind: "relay" }],
      grant: GRANT,
    };
    expect(await poll(200, approved)).toEqual({
      kind: "approved",
      approval: {
        home_id: "h1",
        home_name: "Den",
        connections: approved.connections,
        grant: GRANT,
      },
    });
    // an approval whose grant is not the home's pairing secret's shape is not one
    expect(await poll(200, { ...approved, grant: "x y" })).toEqual({ kind: "error" });
    expect(await poll(200, { ...approved, home_id: "" })).toEqual({ kind: "error" });
  });

  it("sends the poll secret in the body only", async () => {
    let seen = "";
    let body = "";
    await pollCode(async (url, init) => {
      seen = url;
      body = init?.body ?? "";
      return res(202, {});
    }, CODE);
    expect(seen).toBe(`${CLOUD_BASE}/v1/tv/codes/poll`);
    expect(seen).not.toContain(CODE.poll_secret);
    expect(JSON.parse(body)).toEqual({ code: "ABCD-2345", poll_secret: CODE.poll_secret });
  });
});

describe("the connections a web TV can use", () => {
  it("keeps the cloud's order, leaves out a raw address pinned to a certificate name", () => {
    expect(
      usableConnections([
        { url: "https://10-0-0-7.h1.direct.example:8443/", kind: "local" },
        { url: "https://10.0.0.7:8443", kind: "local_ip", tls_name: "10-0-0-7.h1.direct.example" },
        { url: "https://h1.direct.example", kind: "direct", tls_name: "h1.direct.example" },
        { url: "https://h1.relay.maktaba.tv", kind: "relay" },
        { url: "https://h1.relay.maktaba.tv", kind: "relay" },
      ])
    ).toEqual([
      "https://10-0-0-7.h1.direct.example:8443",
      "https://h1.direct.example",
      "https://h1.relay.maktaba.tv",
    ]);
  });

  it("takes only http(s) addresses without credentials", () => {
    expect(
      usableConnections([
        { url: "javascript:alert(1)", kind: "relay" },
        { url: "https://user:pw@h1.example", kind: "relay" },
        { url: "not a url", kind: "relay" },
        { kind: "relay" },
        null,
        { url: "http://10.0.0.7:8088", kind: "local" },
      ])
    ).toEqual(["http://10.0.0.7:8088"]);
    expect(usableConnections("nope")).toEqual([]);
  });
});

describe("the code screen's loop", () => {
  function flow(answers: ((n: number) => Response | Promise<Response>)[]) {
    vi.useFakeTimers();
    const states: CodeState[] = [];
    const approved: CloudApproval[] = [];
    const urls: string[] = [];
    let creates = 0;
    let polls = 0;
    const fetch: FetchLike = async (url) => {
      urls.push(url);
      if (url.endsWith("/v1/tv/codes")) {
        creates++;
        return res(201, { ...CODE, code: `CODE-000${creates}` });
      }
      const a = answers[Math.min(polls, answers.length - 1)]!;
      return a(polls++);
    };
    const f = new CodeFlow({
      fetch,
      platform: "tizen",
      appVersion: "1.0.0",
      onState: (s) => states.push(s),
      onApproved: (a) => approved.push(a),
    });
    return { f, states, approved, urls, creates: () => creates, polls: () => polls };
  }

  it("polls at the interval, slower on slow_down, and stops at the approval", async () => {
    const t = flow([
      () => res(202, {}),
      () => res(429, {}),
      () => res(202, {}),
      () =>
        res(200, {
          status: "approved",
          home_id: "h1",
          home_name: "Den",
          connections: [],
          grant: GRANT,
        }),
    ]);
    await t.f.start();
    expect(t.states.map((s) => s.phase)).toEqual(["asking", "showing"]);
    await vi.advanceTimersByTimeAsync(4_999);
    expect(t.polls()).toBe(0);
    await vi.advanceTimersByTimeAsync(1);
    expect(t.polls()).toBe(1);
    await vi.advanceTimersByTimeAsync(5_000); // slow_down
    expect(t.polls()).toBe(2);
    await vi.advanceTimersByTimeAsync(5_000);
    expect(t.polls()).toBe(2); // now every 10 s
    await vi.advanceTimersByTimeAsync(5_000);
    expect(t.polls()).toBe(3);
    await vi.advanceTimersByTimeAsync(10_000);
    expect(t.approved).toHaveLength(1);
    expect(t.approved[0]!.grant).toBe(GRANT);
    await vi.advanceTimersByTimeAsync(60_000);
    expect(t.polls()).toBe(4);
    expect(logText()).not.toContain(GRANT);
    expect(logText()).not.toContain(CODE.poll_secret);
  });

  it("asks for a new code when the cloud says it is gone, and when it runs out", async () => {
    const t = flow([() => res(410, {}), () => res(202, {})]);
    await t.f.start();
    await vi.advanceTimersByTimeAsync(5_000);
    expect(t.creates()).toBe(2);
    // the second code runs out after 10 minutes of pending polls
    await vi.advanceTimersByTimeAsync(600_000 + 5_000);
    expect(t.creates()).toBe(3);
  });

  it("asks again later when the cloud gives no code, and stops when told", async () => {
    vi.useFakeTimers();
    const states: CodeState[] = [];
    let creates = 0;
    const f = new CodeFlow({
      fetch: async () => {
        creates++;
        return Promise.reject(new TypeError("offline"));
      },
      platform: "webos",
      appVersion: "1",
      onState: (s) => states.push(s),
      onApproved: () => {},
    });
    await f.start();
    expect(states.at(-1)).toEqual({ phase: "down" });
    await vi.advanceTimersByTimeAsync(RETRY_DOWN_MS);
    expect(creates).toBe(2);
    f.stop();
    await vi.advanceTimersByTimeAsync(RETRY_DOWN_MS * 3);
    expect(creates).toBe(2);
  });
});
