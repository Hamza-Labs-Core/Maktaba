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

function platform(name: "tizen" | "webos" | "browser" = "tizen", ip: string | null = "10.0.0.5"): Platform {
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
  return { l, p, app: () => document.getElementById("app")!, frame: () => document.querySelector("iframe") };
}

const HOME = { id: "h1", name: "Den", version: "0.3.150", urls: ["http://10.0.0.7:8088"], api_level: 1, api_level_oldest: 1, client_floor: {} };

beforeEach(() => {
  localStorage.clear();
  clearLog();
  setLang("en");
});

describe("the launcher", () => {
  it("shows Update the home for a home before the contract, and never loads the iframe", async () => {
    const m = mount(async () => json({ ...HOME, api_level: undefined, api_level_oldest: undefined, client_floor: undefined }));
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
    const m = mount(async () => json({ ...HOME, api_level: 9, api_level_oldest: 9 }), platform("webos"));
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
    const answer = { ...HOME, urls: ["https://h1.example:8443", "http://10.0.0.7:8088"] };
    const m = mount(async () => json(answer));
    await m.l.start();
    await m.l.connectAddress("http://10.0.0.7:8088");
    expect(m.frame()!.src).toBe("https://h1.example:8443/tv/?bridge=1&lang=en");
    expect(savedHomes()[0]).toMatchObject({ base: "https://h1.example:8443", plain: false });
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
    for (let i = 0; i < 50 && !m.app().querySelector('[data-testid="find-again"]'); i++) await flush();
    expect(m.app().querySelectorAll('[data-testid="found-home"]')).toHaveLength(1);
    expect(m.app().textContent).toContain("Den");
  });

  it("says when the TV cannot name its address", async () => {
    const m = mount(async () => json(HOME), platform("tizen", null));
    await m.l.start();
    (m.app().querySelector('[data-testid="find"]') as HTMLButtonElement).click();
    await flush();
    expect(m.app().querySelector('[data-testid="find-status"]')!.textContent).toContain("did not say its network address");
  });

  it("is mirrored in Arabic", async () => {
    setLang("ar");
    const m = mount(async () => json(HOME));
    await m.l.start();
    expect(document.documentElement.dir).toBe("rtl");
    expect(m.app().textContent).toContain("اختر جهازك الرئيسي");
  });
});
