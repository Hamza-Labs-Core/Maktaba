// Epic 38 story 38.4: the starting screen says what the server waits on in its
// own language. The server names it by a code from a fixed list and its values
// (`detail_code`, `detail_params` in /processes, beside its English `detail`;
// cmd/maktaba-server/detail_codes.json, which the server embeds); the screen
// has the words (`setup.starting.detail.<code>`), and the programs' own English
// and the server's English go under Details.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import { I18nProvider, bidiIsolate } from "../../lib/i18n";
import en from "../../i18n/en.json";
import ar from "../../i18n/ar.json";
import type { ServerStatus, SetupApi } from "../../lib/desktopSetup";
import { dumpProgress, ServerStarting } from "./ServerStarting";
import { codedText } from "./codedText";
import { APART, startingDetail } from "./startingDetail";

// The server's detail codes and the names of their values, read from the file
// the server embeds (the one list both sides use). Read from disk at run time,
// as localizeRules.test.ts reads the rules schema: the web image builds from
// web/ alone, so a static import of a file outside it fails tsc there.
const FS = "node:fs";
const { readFileSync } = (await import(/* @vite-ignore */ FS)) as {
  readFileSync: (path: string, encoding: "utf8") => string;
};
const DETAIL_CODES = JSON.parse(
  readFileSync("../cmd/maktaba-server/detail_codes.json", "utf8") // vitest runs in web/
) as Record<string, string[]>;

const PREFIX = "setup.starting.detail.";
const names = (s: string) => [...s.matchAll(/\{(\w+)\}/g)].map((m) => m[1]).sort();

function api(status: ServerStatus, platform: "macos" | "windows" = "windows") {
  return {
    appState: vi.fn(async () => ({
      target: "starting",
      server_available: true,
      login_item: "enabled",
      platform,
    })),
    serverStatus: vi.fn(async () => status),
    openLocalServer: vi.fn(async () => {}),
  } as unknown as SetupApi;
}

function show(status: ServerStatus) {
  render(
    <I18nProvider>
      <ServerStarting api={api(status)} />
    </I18nProvider>
  );
}

/** The Details disclosure, once it is there. */
async function details(summary = "Details") {
  return waitFor(() => {
    const box = screen.getByText(summary).closest("details");
    expect(box).not.toBeNull();
    return box as HTMLElement;
  });
}

describe("the detail codes' words", () => {
  const catalogs = { en: en as Record<string, string>, ar: ar as Record<string, string> };

  it("say every code the server sends, in English and Arabic, with only its own values", () => {
    for (const [lang, catalog] of Object.entries(catalogs)) {
      const keys = Object.keys(catalog).filter((k) => k.startsWith(PREFIX));
      expect(keys.sort(), lang).toEqual(
        [...Object.keys(DETAIL_CODES), "db_refused_older.dump"].map((c) => PREFIX + c).sort()
      );
      for (const [code, params] of Object.entries(DETAIL_CODES)) {
        const text = catalog[PREFIX + code];
        for (const n of names(text)) expect(params, `${lang} ${code} {${n}}`).toContain(n);
        // the programs' own English and a log's path stay out of the sentence
        for (const n of APART) expect(text, `${lang} ${code}`).not.toContain(`{${n}}`);
      }
      const kept = catalog[`${PREFIX}db_refused_older.dump`];
      for (const n of names(kept)) expect(DETAIL_CODES.db_refused_older).toContain(n);
      expect(names(kept)).toContain("dump");
    }
  });

  it("are the same in both languages, value for value", () => {
    for (const key of Object.keys(en).filter((k) => k.startsWith("setup.starting."))) {
      const a = (ar as Record<string, string>)[key];
      expect(a, key).toBeTruthy();
      expect(names(a), key).toEqual(names((en as Record<string, string>)[key]));
    }
  });
});

describe("startingDetail", () => {
  const t = (key: string, vars?: Record<string, string | number>) => {
    const hit = (en as Record<string, string>)[key];
    if (hit === undefined) return key;
    return hit.replace(/\{(\w+)\}/g, (m, k: string) => (vars && k in vars ? String(vars[k]) : m));
  };

  it("keeps versions and paths in their own direction", () => {
    expect(startingDetail(t, "db_migrating", { to: "0.5.0" }, {}).text).toBe(
      `Updating the library database for ${bidiIsolate("0.5.0")}.`
    );
  });

  it("says a release the server does not know in words", () => {
    expect(startingDetail(t, "db_migrating", { to: "" }, {}).text).toBe(
      "Updating the library database for a version it does not know."
    );
    // db_restored's: the one the user had before the update
    const d = startingDetail(
      t,
      "db_restored",
      { failed: "0.5.0", error: "migration 0144 failed", dump: "D:\\b\\pre.dump", release: "" },
      {}
    );
    expect(d.text).toContain("install the version you had before again");
    expect(d.apart).toEqual([["Error:", "migration 0144 failed"]]);
  });

  it("names where the database from before the update is only when it is kept", () => {
    const with_ = startingDetail(
      t,
      "db_refused_older",
      { release: "0.4", needed: "0.5", dump: "/b/pre.dump" },
      {}
    );
    expect(with_.text).toContain(`is in ${bidiIsolate("/b/pre.dump")}).`);
    const without = startingDetail(
      t,
      "db_refused_older",
      { release: "0.4", needed: "0.5", dump: "" },
      {}
    );
    expect(without.text).toBe(
      `This Maktaba (${bidiIsolate("0.4")}) is older than the one whose changes the library database holds (${bidiIsolate("0.5")}): install ${bidiIsolate("0.5")} or later.`
    );
  });

  it("puts the programs' own words apart, in order, and none for an unknown code's sentence", () => {
    const d = startingDetail(
      t,
      "db_restore_failed",
      { failed: "0.5", error: "e1", dump: "/d", restore_error: "e2" },
      {}
    );
    expect(d.apart).toEqual([
      ["Error:", "e1"],
      ["Putting it back failed with:", "e2"],
    ]);
    expect(d.text).not.toContain("e1");
    expect(d.text).not.toContain("e2");
    expect(startingDetail(t, "a_code_this_app_lacks", { error: "x" }, {})).toEqual({
      text: "",
      apart: [["Error:", "x"]],
    });
    expect(startingDetail(t, undefined, undefined, {})).toEqual({ text: "", apart: [] });
  });
});

describe("ServerStarting: what the server waits on", () => {
  beforeEach(() => localStorage.clear());
  afterEach(() => localStorage.clear());

  it("says the venv's progress in English, pip's line only under Details", async () => {
    const english = "Installing the transcription tools: Collecting torch==2.4.1 (40%)";
    show({
      kind: "starting",
      detail: english,
      detail_code: "venv_building",
      detail_params: {
        step: "deps",
        done: 12,
        total: 30,
        percent: 40,
        message: "Collecting torch==2.4.1",
      },
    });
    expect(
      await screen.findByText("Installing the transcription tools (40%).")
    ).toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent("The server is starting…");
    const box = await details();
    expect(box).not.toHaveAttribute("open");
    expect(within(box).getByText(english)).toBeInTheDocument();
    expect(screen.queryAllByText(/Collecting torch/)).toHaveLength(1);
  });

  it("says a failed update in Arabic, the error and the log apart under Details", async () => {
    localStorage.setItem("mkt:locale", "ar");
    show({
      kind: "problem",
      roles: ["api"],
      detail:
        "The update to 0.5.0 could not change the library database (migration 0144 failed). …",
      detail_code: "db_restored",
      detail_params: {
        failed: "0.5.0",
        error: "migration 0144 failed",
        dump: "C:\\Users\\me\\AppData\\Local\\Maktaba\\backups\\pre-0.4.2-to-0.5.0.dump",
        release: "",
      },
    });
    expect(
      await screen.findByText(/^لم يتمكن التحديث إلى .*0\.5\.0.* من تعديل قاعدة بيانات المكتبة\./)
    ).toHaveTextContent("ثبّت النسخة التي كانت لديك من قبل مجددًا");
    expect(screen.getByRole("status")).toHaveTextContent("بعض الأجزاء تتوقف مرارًا: api");
    const box = await details("التفاصيل");
    expect(within(box).getByText("الخطأ:")).toHaveTextContent("الخطأ: migration 0144 failed");
    expect(within(box).getByText("migration 0144 failed").tagName).toBe("CODE");
    // the error is in no sentence of the screen's own
    expect(screen.getAllByText(/migration 0144 failed/).every((el) => box.contains(el))).toBe(true);
  });

  it("an unknown code shows the generic line, never the English outside Details", async () => {
    const english = "Doing something new";
    show({ kind: "starting", detail: english, detail_code: "a_code_this_app_lacks" });
    const box = await details();
    expect(within(box).getByText(english)).toBeInTheDocument();
    expect(screen.getAllByText(english)).toHaveLength(1);
    expect(screen.getByRole("status")).toHaveTextContent("The server is starting…");
    expect(screen.queryByText(/a_code_this_app_lacks/)).not.toBeInTheDocument();
  });

  it("an older server's detail with no code shows the generic line, the English under Details", async () => {
    localStorage.setItem("mkt:locale", "ar");
    const english = "Installing the transcription tools (40%)";
    show({ kind: "starting", detail: english });
    expect(await screen.findByText("الخادم قيد التشغيل…")).toBeInTheDocument();
    const box = await details("التفاصيل");
    expect(within(box).getByText(english)).toBeInTheDocument();
    expect(screen.getAllByText(english)).toHaveLength(1);
  });

  it("shows no Details when the server says nothing", async () => {
    show({ kind: "starting" });
    expect(await screen.findByText("The server is starting…")).toBeInTheDocument();
    expect(screen.queryByText("Details")).not.toBeInTheDocument();
  });
});

describe("codedText", () => {
  const catalog: Record<string, string> = {
    "p.known": "{n} of {total_bytes} on {machine}",
  };
  const t = (key: string, vars?: Record<string, string | number>) => {
    const hit = catalog[key];
    if (hit === undefined) return key;
    return hit.replace(/\{(\w+)\}/g, (m, k: string) => (vars && k in vars ? String(vars[k]) : m));
  };

  it("fills a known code's values and the platform's words", () => {
    expect(
      codedText(t, "p", "known", { n: 2, total_bytes: 2048 }, { machine: "this PC" }, "x")
    ).toMatch(/^2 of .+ on this PC$/);
  });

  it("falls back for a code the app has no words for", () => {
    expect(codedText(t, "p", "unknown", {}, {}, "fallback")).toBe("fallback");
    expect(codedText(t, "p", "unknown", undefined, {}, "")).toBe("");
  });
});

// The owner's request: the restart shows its progress, not just a sentence. The backup
// before an update says how much it has written of about how much (the newest finished
// dump's size), on a bar; without an earlier dump it says how much so far.
describe("the backup's progress", () => {
  const dumping = (mb: number, expected_mb: number): ServerStatus => ({
    kind: "starting",
    detail_code: "db_dumping",
    detail_params: { from: "v0.3.23", to: "v0.3.24", mb, expected_mb },
  });

  it("is a bar of what is written against what the last dump came to", async () => {
    show(dumping(10240, 20480));
    expect(await screen.findByText("10.0 GB of about 20.0 GB saved")).toBeInTheDocument();
    const bar = screen.getByRole("progressbar");
    expect(bar).toHaveAttribute("aria-valuenow", "50");
    expect(screen.getByRole("dialog", { name: en["setup.starting.title"] })).toBeInTheDocument();
  });

  it("says how much so far when no dump came before, on a moving bar", async () => {
    show(dumping(512, 0));
    expect(await screen.findByText("512.0 MB saved so far")).toBeInTheDocument();
    expect(screen.getByRole("progressbar")).not.toHaveAttribute("aria-valuenow");
  });

  it("stops short of full when the dump outgrows the last one", () => {
    expect(dumpProgress(dumping(30000, 20000))?.percent).toBe(99);
    expect(dumpProgress({ kind: "running" })).toBeNull();
  });
});
