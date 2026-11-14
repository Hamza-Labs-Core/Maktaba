// Story 58.09 — the launcher's log (500 lines, redacted), discovery (the HTTPS name, the
// plain-HTTP fallback, a typed address), the saved homes and the catalogs.
import { LOG_LINES, clearLog, log, logLines, redact } from "./log";
import {
  chooseConnection,
  directNameOf,
  normalizeBase,
  readHome,
  type FetchLike,
} from "./discovery";
import { forgetHome, rememberHome, savedHomes } from "./homes";
import { catalogKeys, setLang, t, dir, langOf } from "./i18n";
import { step } from "./ui/focus";
import { chromiumMajor, yearFromEngine, commonKeyAction } from "./platform/types";
import { ipFromStatus } from "./platform/webos";

const json = (b: unknown) => new Response(JSON.stringify(b), { status: 200 });

describe("the log", () => {
  beforeEach(() => clearLog());
  it("keeps the last 500 lines", () => {
    for (let i = 0; i < LOG_LINES + 20; i++) log("launcher", `line ${i}`);
    const lines = logLines();
    expect(lines).toHaveLength(500);
    expect(lines[0]!.text).toBe("line 20");
  });
  it("redacts signatures, grants, codes and tokens", () => {
    expect(redact("GET https://h/stream/a.m3u8?sig=abc.def&x=1")).toBe(
      "GET https://h/stream/a.m3u8?sig=[REDACTED]&x=1"
    );
    expect(
      redact(
        '{"grant":"g123","device_code":"d","access_token":"a","refresh_token":"r","poll_secret":"p"}'
      )
    ).toBe(
      '{"grant":"[REDACTED]","device_code":"[REDACTED]","access_token":"[REDACTED]","refresh_token":"[REDACTED]","poll_secret":"[REDACTED]"}'
    );
    expect(redact("/pair?code=ABCD-2345")).toBe("/pair?code=[REDACTED]");
    log("bridge-in", "play https://h/m?sig=zzz");
    expect(logLines()[0]!.text).not.toContain("zzz");
  });
});

describe("discovery", () => {
  it("reads a typed address", () => {
    expect(normalizeBase("192.168.1.20")).toBe("http://192.168.1.20:8088");
    expect(normalizeBase("192.168.1.20:9000")).toBe("http://192.168.1.20:9000");
    expect(normalizeBase("https://home.example")).toBe("https://home.example");
    expect(normalizeBase("http://home.local/")).toBe("http://home.local");
    expect(normalizeBase("  ")).toBeNull();
    expect(normalizeBase("http://user:pw@h")).toBeNull();
    expect(normalizeBase("a b")).toBeNull();
  });

  it("is not a home without an id and urls", async () => {
    const f: FetchLike = async () => json({ hello: 1 });
    expect(await readHome("http://x", f, 100)).toBeNull();
  });

  const answer = {
    id: "h1",
    name: "Den",
    urls: [
      "https://192-168-1-7.h1.direct-a.example:8443",
      "http://192.168.1.7:8088",
      "https://h1.relay.maktaba.tv",
    ],
  };

  it("uses the home's direct HTTPS name for the found address when it answers", async () => {
    const f: FetchLike = async (url) =>
      url.startsWith("https://192-168-1-7.") ? json(answer) : Promise.reject(new Error("no"));
    const c = await chooseConnection("http://192.168.1.7:8088", answer, f);
    expect(c).toMatchObject({ base: "https://192-168-1-7.h1.direct-a.example:8443", plain: false });
  });

  it("takes only a name for the address the home answered from (no other site)", async () => {
    const at = "http://192.168.1.7:8088";
    expect(directNameOf("https://192-168-1-7.h1.direct-a.example", at, "h1")).toBe(true);
    expect(directNameOf("https://192-168-1-7.H1.direct-a.example", at, "H1")).toBe(true);
    expect(directNameOf("https://192-168-1-8.h1.direct-a.example", at, "h1")).toBe(false);
    // another home's name, or a look-alike, for the same address
    expect(directNameOf("https://192-168-1-7.h2.direct-a.example", at, "h1")).toBe(false);
    expect(directNameOf("https://192-168-1-7.evil.example", at, "h1")).toBe(false);
    expect(directNameOf("https://192-168-1-7.h1.direct-a.example", at, "")).toBe(false);
    expect(directNameOf("https://evil.example", at, "h1")).toBe(false);
    expect(directNameOf("https://h1.relay.maktaba.tv", at, "h1")).toBe(false);
    const f: FetchLike = vi.fn(async () => json({ ...answer, id: "h1" }));
    const c = await chooseConnection(
      "http://192.168.1.7:8088",
      { ...answer, urls: ["https://evil.example"] },
      f,
      50
    );
    expect(c.plain).toBe(true);
    expect(f).not.toHaveBeenCalled();
  });

  it("falls back to plain HTTP on the LAN when no HTTPS name resolves (DNS rebinding blocked)", async () => {
    const f: FetchLike = async () => {
      throw new TypeError("ERR_NAME_NOT_RESOLVED");
    };
    const c = await chooseConnection("http://192.168.1.7:8088", answer, f, 50);
    expect(c).toMatchObject({ base: "http://192.168.1.7:8088", plain: true });
  });

  it("does not take another home answering at a name", async () => {
    const f: FetchLike = async () => json({ ...answer, id: "someone-else" });
    const c = await chooseConnection("http://192.168.1.7:8088", answer, f, 50);
    expect(c.plain).toBe(true);
  });

  it("keeps a typed HTTPS address", async () => {
    const f: FetchLike = vi.fn();
    const c = await chooseConnection("https://h1.example", answer, f);
    expect(c).toMatchObject({ base: "https://h1.example", plain: false });
    expect(f).not.toHaveBeenCalled();
  });
});

describe("the saved homes", () => {
  beforeEach(() => localStorage.clear());
  it("lists the last used first and forgets one", () => {
    rememberHome({ id: "a", name: "A", base: "https://a", plain: false }, 1);
    rememberHome({ id: "b", name: "B", base: "http://10.0.0.2:8088", plain: true }, 2);
    rememberHome({ id: "a", name: "A", base: "https://a", plain: false }, 3);
    expect(savedHomes().map((h) => h.id)).toEqual(["a", "b"]);
    forgetHome("a");
    expect(savedHomes().map((h) => h.id)).toEqual(["b"]);
  });
  it("ignores what is not a saved home", () => {
    localStorage.setItem(
      "maktaba.launcher.homes",
      '[{"id":1},{"id":"x","name":"X","base":"javascript:1","lastUsed":1}]'
    );
    expect(savedHomes()).toEqual([]);
    localStorage.setItem("maktaba.launcher.homes", "{not json");
    expect(savedHomes()).toEqual([]);
  });
});

describe("the catalogs and the language", () => {
  afterEach(() => setLang("en"));
  it("has the same keys in English and Arabic", () => {
    expect(catalogKeys("ar").sort()).toEqual(catalogKeys("en").sort());
  });
  it("fills positional placeholders and mirrors Arabic", () => {
    setLang("en");
    expect(t("compat.homeVersion", "Den", "0.3.1")).toBe("Den runs Maktaba 0.3.1.");
    setLang("ar");
    expect(dir()).toBe("rtl");
    expect(document.documentElement.dir).toBe("rtl");
    expect(t("launcher.plainHttp")).toBe("متصل دون تشفير على شبكتك");
  });
  it("takes the TV's language", () => {
    expect(langOf("ar-SA")).toBe("ar");
    expect(langOf("en-US")).toBe("en");
    expect(langOf(undefined)).toBe("en");
  });
});

describe("the remote and the TV", () => {
  it("mirrors Left and Right in Arabic, and leaves them to a text field", () => {
    expect(step("right", false, false)).toBe(1);
    expect(step("right", true, false)).toBe(-1);
    expect(step("left", false, true)).toBe(0);
    expect(step("down", true, true)).toBe(1);
  });
  it("maps the Samsung and LG keys", () => {
    const k = (keyCode: number, key = "Unidentified") =>
      commonKeyAction({ keyCode, key } as KeyboardEvent);
    expect(k(10009)).toBe("back");
    expect(k(461)).toBe("back");
    expect(k(403)).toBe("menu");
    expect(k(10252)).toBe("playPause");
    expect(k(0, "ArrowLeft")).toBe("left");
    expect(k(0, "a")).toBeNull();
  });
  it("reads the engine and the model year", () => {
    const ua =
      "Mozilla/5.0 (SMART-TV; Linux; Tizen 5.5) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/69.0.3497.106 TV Safari/537.36";
    expect(chromiumMajor(ua)).toBe(69);
    // Samsung's own runtimes: the engine without "Chrome/" (2020 Tizen 5.5, 2022 Tizen 6.5)
    expect(
      chromiumMajor(
        "Mozilla/5.0 (SMART-TV; LINUX; Tizen 5.5) AppleWebKit/537.36 (KHTML, like Gecko) 69.0.3497.106.1/5.5 TV Safari/537.36"
      )
    ).toBe(69);
    expect(
      chromiumMajor(
        "Mozilla/5.0 (SMART-TV; LINUX; Tizen 6.5) AppleWebKit/537.36 (KHTML, like Gecko) 85.0.4183.93/6.5 TV Safari/537.36"
      )
    ).toBe(85);
    // LG webOS 5 (2020)
    expect(
      chromiumMajor(
        "Mozilla/5.0 (Web0S; Linux/SmartTV) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/68.0.3440.106 Safari/537.36 WebAppManager"
      )
    ).toBe(68);
    expect(chromiumMajor("Mozilla/5.0 (X11; Linux) AppleWebKit/537.36 Safari/537.36")).toBe(0);
    expect(yearFromEngine(69, "tizen")).toBe(2020);
    expect(yearFromEngine(87, "webos")).toBe(2022);
    expect(yearFromEngine(53, "webos")).toBe(0);
  });
  it("reads the webOS address, wired first", () => {
    expect(
      ipFromStatus({
        wired: { state: "connected", ipAddress: "10.0.0.4" },
        wifi: { state: "connected", ipAddress: "10.0.0.5" },
      })
    ).toBe("10.0.0.4");
    expect(
      ipFromStatus({
        wired: { state: "disconnected" },
        wifi: { state: "connected", ipAddress: "10.0.0.5" },
      })
    ).toBe("10.0.0.5");
    expect(ipFromStatus({})).toBeNull();
  });
});
