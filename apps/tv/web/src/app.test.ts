// Story 58.09 — the launcher's screens in jsdom: a home at a level it cannot use shows its
// own Update page and the iframe is never loaded; a home that fits is saved and loaded in
// the iframe, by its HTTPS name or over plain HTTP; a typed address that is not a home says so.
import { Launcher, READY_TIMEOUT_MS } from "./app";
import { setLang } from "./i18n";
import { clearLog, logText } from "./log";
import type { FetchLike } from "./discovery";
import type { Platform } from "./platform/types";
import { browserPlatform } from "./platform/browser";
import { savedHomes } from "./homes";

function platform(
  name: "tizen" | "webos" | "browser" = "tizen",
  ip: string | null = "10.0.0.5"
): Platform {
  const base = browserPlatform(window);
  return { ...base, name, localIp: async () => ip, exit: vi.fn() };
}

const json = (b: unknown) => new Response(JSON.stringify(b), { status: 200 });
const flush = async () => {
  for (let i = 0; i < 10; i++) await new Promise((r) => setTimeout(r, 0));
};

function mount(fetch: FetchLike, p = platform()) {
  document.body.innerHTML = '<div id="stage"></div><main id="app"></main>';
  const l = new Launcher({ window, platform: p, fetch });
  return {
    l,
    p,
    app: () => document.getElementById("app")!,
    frame: () => document.querySelector("iframe"),
  };
}

const HOME = {
  id: "h1",
  name: "Den",
  version: "0.3.150",
  urls: ["http://10.0.0.7:8088"],
  api_level: 1,
  api_level_oldest: 1,
  client_floor: {},
};

beforeEach(() => {
  localStorage.clear();
  clearLog();
  setLang("en");
});

describe("the launcher", () => {
  it("shows Update the home for a home before the contract, and never loads the iframe", async () => {
    const m = mount(async () =>
      json({ ...HOME, api_level: undefined, api_level_oldest: undefined, client_floor: undefined })
    );
    await m.l.start();
    await m.l.connectAddress("http://10.0.0.7:8088");
    await flush();
    expect(m.app().querySelector('[data-testid="screen-update-home"]')).not.toBeNull();
    expect(m.app().textContent).toContain("This home needs an update");
    expect(m.app().textContent).toContain("Den runs Maktaba 0.3.150.");
    expect(m.frame()).toBeNull();
    expect(savedHomes()).toEqual([]);
  });

  it("shows Update this app in the TV store's words when the home no longer serves this build", async () => {
    const m = mount(
      async () => json({ ...HOME, api_level: 9, api_level_oldest: 9 }),
      platform("webos")
    );
    await m.l.start();
    await m.l.connectAddress("http://10.0.0.7:8088");
    expect(m.app().querySelector('[data-testid="screen-update-app"]')).not.toBeNull();
    expect(m.app().textContent).toContain("LG Content Store");
    expect(m.frame()).toBeNull();
  });

  it("loads a home that fits over plain HTTP, with bridge=1 and plain=1, and saves it", async () => {
    const m = mount(async () => json(HOME));
    await m.l.start();
    await m.l.connectAddress("http://10.0.0.7:8088");
    const f = m.frame()!;
    expect(f.src).toBe("http://10.0.0.7:8088/tv/?bridge=1&lang=en&plain=1");
    expect(f.style.visibility).toBe("hidden");
    expect(savedHomes()[0]).toMatchObject({ id: "h1", plain: true, lan: "http://10.0.0.7:8088" });
    expect(m.l.connection()?.plain).toBe(true);
  });

  it("loads a home by its HTTPS name when it answers", async () => {
    const name = "https://10-0-0-7.h1.direct-a.example:8443";
    const answer = { ...HOME, urls: [name, "http://10.0.0.7:8088"] };
    const m = mount(async () => json(answer));
    await m.l.start();
    await m.l.connectAddress("http://10.0.0.7:8088");
    expect(m.frame()!.src).toBe(`${name}/tv/?bridge=1&lang=en`);
    expect(savedHomes()[0]).toMatchObject({ base: name, plain: false });
  });

  it("keeps a saved HTTPS address when the name fails and it falls back to plain HTTP", async () => {
    const name = "https://10-0-0-7.h1.direct-a.example:8443";
    const answer = { ...HOME, urls: [name, "http://10.0.0.7:8088"] };
    let nameUp = true;
    const m = mount(async (url) =>
      url.startsWith(name) && !nameUp ? Promise.reject(new TypeError("blocked")) : json(answer)
    );
    await m.l.start();
    await m.l.connectAddress("http://10.0.0.7:8088");
    nameUp = false;
    await m.l.connectSaved(savedHomes()[0]!);
    expect(m.l.connection()).toMatchObject({ base: "http://10.0.0.7:8088", plain: true });
    expect(savedHomes()[0]).toMatchObject({
      base: name,
      plain: false,
      lan: "http://10.0.0.7:8088",
    });
  });

  it("opens the last home at once at the next start, and shows the picker on Switch home", async () => {
    const m = mount(async () => json(HOME));
    await m.l.start();
    await m.l.connectAddress("http://10.0.0.7:8088");
    const again = mount(async () => json(HOME));
    await again.l.start();
    await flush();
    expect(again.frame()!.src).toContain("http://10.0.0.7:8088/tv/");
  });

  it("says when no home answers at a typed address", async () => {
    const m = mount(async () => {
      throw new TypeError("refused");
    });
    await m.l.start();
    await m.l.connectAddress("http://10.0.0.99:8088");
    expect(m.app().querySelector('[data-testid="screen-not-home"]')).not.toBeNull();
    expect(m.app().textContent).toContain("10.0.0.99");
  });

  it("calls a home whose /tv/ never answers unreachable", async () => {
    vi.useFakeTimers();
    try {
      const m = mount(async () => json(HOME));
      await m.l.start();
      await m.l.connectAddress("http://10.0.0.7:8088");
      expect(m.frame()).not.toBeNull();
      await vi.advanceTimersByTimeAsync(READY_TIMEOUT_MS + 10);
      expect(m.frame()).toBeNull();
      expect(m.app().querySelector('[data-testid="screen-unreachable"]')).not.toBeNull();
      expect(logText()).toContain("did not answer");
    } finally {
      vi.useRealTimers();
    }
  });

  it("finds homes on the network and lists them", async () => {
    const m = mount(async (url) =>
      url.startsWith("http://10.0.0.7:8088") ? json(HOME) : Promise.reject(new TypeError("refused"))
    );
    await m.l.start();
    (m.app().querySelector('[data-testid="find"]') as HTMLButtonElement).click();
    for (let i = 0; i < 50 && !m.app().querySelector('[data-testid="find-again"]'); i++)
      await flush();
    expect(m.app().querySelectorAll('[data-testid="found-home"]')).toHaveLength(1);
    expect(m.app().textContent).toContain("Den");
  });

  it("says when the TV cannot name its address", async () => {
    const m = mount(async () => json(HOME), platform("tizen", null));
    await m.l.start();
    (m.app().querySelector('[data-testid="find"]') as HTMLButtonElement).click();
    await flush();
    expect(m.app().querySelector('[data-testid="find-status"]')!.textContent).toContain(
      "did not say its network address"
    );
  });

  it("is mirrored in Arabic", async () => {
    setLang("ar");
    const m = mount(async () => json(HOME));
    await m.l.start();
    expect(document.documentElement.dir).toBe("rtl");
    expect(m.app().textContent).toContain("سجّل الدخول برمز");
  });
});

// 58.09's second part: the cloud code (story 58.03) is the first screen.
describe("the cloud code", () => {
  const CODE = {
    code: "ABCD-2345",
    poll_secret: "ps-secret-value-1234567890",
    expires_in: 600,
    interval: 5,
    verify_url: "https://maktaba.tv/tv?c=ABCD2345",
  };
  const GRANT = "Gr4nt_" + "x".repeat(37);
  const NAME = "https://10-0-0-7.h1.direct-a.example:8443";
  const PLAIN = "http://10.0.0.7:8088";
  const APPROVAL = {
    status: "approved",
    // the cloud's server id: not the id the home itself answers with (h1)
    home_id: "7d0c1e9a-5b2f-4c1e-9a7d-3e2f1b0c4d5a",
    home_name: "Den",
    connections: [
      { url: NAME, kind: "local" },
      { url: "https://10.0.0.7:8443", kind: "local_ip", tls_name: "10-0-0-7.h1.direct-a.example" },
      // a plain LAN address: on another network it can be another machine
      { url: PLAIN, kind: "local" },
      { url: "https://h1.relay.maktaba.tv", kind: "relay" },
    ],
    grant: GRANT,
  };
  const status = (code: number, b: unknown) =>
    new Response(JSON.stringify(b), { status: code });

  /** The cloud and the home's connections; `answers` are the bases that answer as h1. */
  function world(
    opts: {
      approved?: () => boolean;
      answers?: string[];
      down?: boolean;
      /** What each answering base lists as the home's own addresses (default: itself). */
      urls?: (base: string) => string[];
    } = {}
  ) {
    const calls: { url: string; init?: Parameters<FetchLike>[1] }[] = [];
    const fetch: FetchLike = async (url, init) => {
      calls.push({ url, init });
      if (url === "https://api.maktaba.tv/v1/tv/codes")
        return opts.down ? Promise.reject(new TypeError("offline")) : status(201, CODE);
      if (url === "https://api.maktaba.tv/v1/tv/codes/poll")
        return opts.approved?.() ? status(200, APPROVAL) : status(202, { status: "pending" });
      const base = url.replace(/\/api\/discovery\/home$/, "");
      if ((opts.answers ?? []).includes(base))
        return json({ ...HOME, urls: opts.urls ? opts.urls(base) : [base] });
      return Promise.reject(new TypeError("refused"));
    };
    return { fetch, calls };
  }

  afterEach(() => vi.useRealTimers());

  it("shows a code from Maktaba Cloud with its QR, and the other ways under it", async () => {
    const w = world();
    const m = mount(w.fetch);
    await m.l.start();
    await flush();
    expect(m.app().querySelector('[data-testid="cloud-code"]')!.textContent).toBe("ABCD-2345");
    expect(m.app().querySelector('[data-testid="cloud-qr"]')).not.toBeNull();
    expect(m.app().textContent).toContain("maktaba.tv/tv");
    expect(m.app().querySelector('[data-testid="find"]')).not.toBeNull();
    expect(m.app().querySelector('[data-testid="more-ways"]')).not.toBeNull();
    const create = w.calls.find((c) => c.url.endsWith("/v1/tv/codes"))!;
    expect(create.init?.method).toBe("POST");
    expect(JSON.parse(create.init!.body!)).toEqual({ platform: "tizen", app_version: "dev" });
    // the poll secret is never in a URL or the log
    expect(w.calls.every((c) => !c.url.includes(CODE.poll_secret))).toBe(true);
    expect(logText()).not.toContain(CODE.poll_secret);
  });

  it("reaches the approving home by its first usable connection and hands its /tv/ the grant", async () => {
    vi.useFakeTimers();
    let approved = false;
    const relay = "https://h1.relay.maktaba.tv";
    const w = world({ approved: () => approved, answers: [relay] });
    const m = mount(w.fetch);
    await m.l.start();
    await vi.advanceTimersByTimeAsync(5_000);
    expect(m.frame()).toBeNull();
    approved = true;
    await vi.advanceTimersByTimeAsync(5_000);
    await vi.advanceTimersByTimeAsync(100);
    const f = m.frame()!;
    expect(f.src).toBe(`${relay}/tv/?bridge=1&lang=en`);
    // the name was tried first; the raw address pinned to a certificate name never
    const tried = w.calls.map((c) => c.url).filter((u) => u.endsWith("/api/discovery/home"));
    expect(tried[0]).toBe(`${NAME}/api/discovery/home`);
    expect(tried.some((u) => u.startsWith("https://10.0.0.7:8443"))).toBe(false);
    expect(savedHomes()[0]).toMatchObject({ id: "h1", base: relay, plain: false });
    expect(JSON.stringify(savedHomes())).not.toContain(GRANT);
    // the UI says hello from the frame: the grant follows, to the home's origin only
    const sent: { msg: Record<string, unknown>; target: string }[] = [];
    const win = f.contentWindow!;
    win.postMessage = ((msg: Record<string, unknown>, target: string) =>
      sent.push({ msg, target })) as typeof win.postMessage;
    window.dispatchEvent(
      new MessageEvent("message", {
        origin: relay,
        source: win,
        data: { type: "maktaba:hello", id: 1, bridge_level: 1, takes: ["signIn"] },
      })
    );
    expect(sent.map((x) => x.msg.type)).toEqual(["maktaba:hello", "maktaba:signIn"]);
    expect(sent[1]).toEqual({ msg: { type: "maktaba:signIn", grant: GRANT }, target: relay });
    expect(logText()).not.toContain(GRANT);
    // polling stopped at the approval
    const polls = w.calls.filter((c) => c.url.endsWith("/poll")).length;
    await vi.advanceTimersByTimeAsync(15_000);
    expect(w.calls.filter((c) => c.url.endsWith("/poll")).length).toBe(polls);
  });

  it("passes over another home answering on a plain LAN address", async () => {
    vi.useFakeTimers();
    const relay = "https://h1.relay.maktaba.tv";
    // another home answers on the same private address (a relative's network): passed over
    const w = world({
      approved: () => true,
      answers: [PLAIN, relay],
      urls: (b) => (b === PLAIN ? [PLAIN, "https://other.relay.maktaba.tv"] : [b]),
    });
    const m = mount(w.fetch);
    await m.l.start();
    await vi.advanceTimersByTimeAsync(5_000);
    await vi.advanceTimersByTimeAsync(100);
    expect(m.frame()!.src).toBe(`${relay}/tv/?bridge=1&lang=en`);
    expect(logText()).toContain("did not answer as the home");
  });

  it("takes the LAN name first when the approving home answers there (it lists its relay)", async () => {
    vi.useFakeTimers();
    const relay = "https://h1.relay.maktaba.tv";
    const w = world({ approved: () => true, answers: [NAME, relay], urls: () => [NAME, relay] });
    const m = mount(w.fetch);
    await m.l.start();
    await vi.advanceTimersByTimeAsync(5_000);
    await vi.advanceTimersByTimeAsync(100);
    expect(m.frame()!.src).toBe(`${NAME}/tv/?bridge=1&lang=en`);
  });

  it("takes a plain LAN address when the home there lists its relay", async () => {
    vi.useFakeTimers();
    const relay = "https://h1.relay.maktaba.tv";
    const w = world({ approved: () => true, answers: [PLAIN, relay], urls: () => [PLAIN, relay] });
    const m = mount(w.fetch);
    await m.l.start();
    await vi.advanceTimersByTimeAsync(5_000);
    await vi.advanceTimersByTimeAsync(100);
    // a plain address: the frame is told it is one
    expect(m.frame()!.src).toBe(`${PLAIN}/tv/?bridge=1&lang=en&plain=1`);
  });

  it("says so when none of the home's connections answers", async () => {
    vi.useFakeTimers();
    const w = world({ approved: () => true, answers: [] });
    const m = mount(w.fetch);
    await m.l.start();
    await vi.advanceTimersByTimeAsync(5_000);
    await vi.advanceTimersByTimeAsync(20_000);
    expect(m.app().querySelector('[data-testid="screen-cloud-unreachable"]')).not.toBeNull();
    expect(m.app().textContent).toContain("Cannot reach Den");
    expect(m.frame()).toBeNull();
  });

  it("offers the network and the address when the cloud gives no code", async () => {
    const w = world({ down: true });
    const m = mount(w.fetch);
    await m.l.start();
    await flush();
    expect(m.app().querySelector('[data-testid="cloud-status"]')!.textContent).toContain(
      "unavailable right now"
    );
    expect(m.app().querySelector('[data-testid="cloud-code"]')).toBeNull();
    expect(m.app().querySelector('[data-focus="find"]')).not.toBeNull();
  });

  it("stops polling once the person leaves the code screen", async () => {
    vi.useFakeTimers();
    const w = world();
    const m = mount(w.fetch);
    await m.l.start();
    await vi.advanceTimersByTimeAsync(5_000);
    (m.app().querySelector('[data-testid="typed"]') as HTMLButtonElement).click();
    const polls = w.calls.filter((c) => c.url.endsWith("/poll")).length;
    await vi.advanceTimersByTimeAsync(30_000);
    expect(w.calls.filter((c) => c.url.endsWith("/poll")).length).toBe(polls);
  });
});
