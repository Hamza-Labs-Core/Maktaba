// Epic 38 story 38.4: the starting screen says what the server waits on in its
// own language. The server names it by a code and its values (`detail_code`,
// `detail_params` in /processes, beside its English `detail`); the screen has
// the words (`setup.starting.detail.<code>`), and the English goes under
// Details. The codes here are this test's own: the catalogs below gain them,
// in English and Arabic, for this file only.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import { I18nProvider } from "../../lib/i18n";
import type { ServerStatus, SetupApi } from "../../lib/desktopSetup";
import { ServerStarting } from "./ServerStarting";
import { codedText } from "./codedText";

vi.mock("../../i18n/en.json", async (importOriginal) => {
  const real = (await importOriginal<{ default: Record<string, string> }>()).default;
  return {
    default: {
      ...real,
      "setup.starting.detail.test_tools": "Installing the tools ({pct}%) on {machine}.",
      "setup.starting.detail.test_backup":
        "The backup before the update failed ({free_bytes} free).",
    },
  };
});
vi.mock("../../i18n/ar.json", async (importOriginal) => {
  const real = (await importOriginal<{ default: Record<string, string> }>()).default;
  return {
    default: {
      ...real,
      "setup.starting.detail.test_tools": "جارٍ تثبيت الأدوات ({pct}%) على {machine}.",
      "setup.starting.detail.test_backup": "فشل النسخ الاحتياطي قبل التحديث ({free_bytes} متاحة).",
    },
  };
});

const ENGLISH = "Installing the transcription tools: pip install torch==2.4 (40%)";

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

/** The Details disclosure and the English inside it. */
async function details() {
  const summary = await screen.findByText("Details");
  const box = summary.closest("details");
  expect(box).not.toBeNull();
  return box as HTMLElement;
}

describe("ServerStarting: what the server waits on", () => {
  beforeEach(() => localStorage.clear());
  afterEach(() => localStorage.clear());

  it("says a known code in English with its values, the server's English under Details", async () => {
    show({
      kind: "starting",
      detail: ENGLISH,
      detail_code: "test_tools",
      detail_params: { pct: 40 },
    });
    expect(await screen.findByText("Installing the tools (40%) on this PC.")).toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent("The server is starting…");
    const box = await details();
    expect(within(box).getByText(ENGLISH)).toBeInTheDocument();
    expect(box).not.toHaveAttribute("open");
    // the English is nowhere else
    expect(screen.getAllByText(ENGLISH)).toHaveLength(1);
  });

  it("says it in Arabic on an Arabic screen, sizes formatted, the English still under Details", async () => {
    localStorage.setItem("mkt:locale", "ar");
    show({
      kind: "problem",
      roles: ["api"],
      detail: "the backup before the update failed: disk full",
      detail_code: "test_backup",
      detail_params: { free_bytes: 0 },
    });
    expect(
      await screen.findByText(/^فشل النسخ الاحتياطي قبل التحديث \(.+ متاحة\)\.$/)
    ).toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent("بعض الأجزاء تتوقف مرارًا: api");
    const box = await waitFor(() => {
      const el = screen.getByText("التفاصيل").closest("details");
      expect(el).not.toBeNull();
      return el as HTMLElement;
    });
    expect(
      within(box).getByText("the backup before the update failed: disk full")
    ).toBeInTheDocument();
  });

  it("an unknown code shows the generic line, never the English outside Details", async () => {
    show({ kind: "starting", detail: ENGLISH, detail_code: "a_code_this_app_lacks" });
    const box = await details();
    expect(within(box).getByText(ENGLISH)).toBeInTheDocument();
    expect(screen.getAllByText(ENGLISH)).toHaveLength(1);
    expect(screen.getByRole("status")).toHaveTextContent("The server is starting…");
    expect(screen.queryByText(/a_code_this_app_lacks/)).not.toBeInTheDocument();
  });

  it("an older server's detail with no code shows the generic line, the English under Details", async () => {
    localStorage.setItem("mkt:locale", "ar");
    show({ kind: "starting", detail: ENGLISH });
    expect(await screen.findByText("الخادم قيد التشغيل…")).toBeInTheDocument();
    const box = await waitFor(() => {
      const el = screen.getByText("التفاصيل").closest("details");
      expect(el).not.toBeNull();
      return el as HTMLElement;
    });
    expect(within(box).getByText(ENGLISH)).toBeInTheDocument();
    expect(screen.getAllByText(ENGLISH)).toHaveLength(1);
  });

  it("shows no Details when the server says nothing, or is running", async () => {
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
