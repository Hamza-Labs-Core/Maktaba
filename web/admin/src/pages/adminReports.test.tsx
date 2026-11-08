// Epic 53 story 53.16: the service admin's problem reports. A home admin's token is refused
// by the API (Access denied); the list filters by app, version, category, status and error
// group; a report shows its merged timeline, and status, issue link and reply are posted.
// Its Apple crashes read as functions and lines where the release's symbols are uploaded
// (53.14), and the Crash symbols page lists the uploaded tables.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { setAccessToken } from "@cloud-shared/api";
import { renderAt, session, stubFetch, user } from "../test/harness";

beforeEach(() => setAccessToken(null));
afterEach(() => vi.unstubAllGlobals());

const REPORT = {
  id: 41,
  server_id: "s1",
  home: "darwish-home",
  home_report_id: 7,
  received_at: "2026-11-07T12:00:00Z",
  happened_at: "2026-11-07T11:50:00Z",
  app: "apple.player",
  version: "0.3.95",
  platform: "ios",
  category: "playback",
  status: "new",
  screenshot: false,
  fingerprints: ["fp-1"],
};

describe("admin problem reports", () => {
  it("renders Access denied for an account without the service admin role", async () => {
    stubFetch({
      "POST /v1/auth/refresh": session(user({ role: "user" })),
      "GET /v1/admin/reports": [403, { error: "admin_access_denied", message: "denied" }],
    });
    renderAt("/reports");
    expect(await screen.findByText("Access denied")).toBeInTheDocument();
  });

  it("lists the reports across homes and filters them", async () => {
    const f = stubFetch({
      "POST /v1/auth/refresh": session(user({ role: "service_admin" })),
      "GET /v1/admin/reports": [200, { reports: [REPORT], total: 1 }],
    });
    renderAt("/reports?app=apple.player");
    const row = await screen.findByTestId("cloud-report-row");
    expect(row).toHaveTextContent("darwish-home");
    expect(row).toHaveTextContent("0.3.95");
    expect(within(row).getByRole("link", { name: "#41" })).toHaveAttribute("href", "/reports/41");
    expect(f.find("GET", "/v1/admin/reports")[0].path).toContain("app=apple.player");
    const u = userEvent.setup();
    await u.selectOptions(screen.getByLabelText("Status"), "fixed");
    await waitFor(() =>
      expect(f.find("GET", "/v1/admin/reports").at(-1)!.path).toContain("status=fixed")
    );
  });

  it("a report's timeline, and its status, issue and reply", async () => {
    const f = stubFetch({
      "POST /v1/auth/refresh": session(user({ role: "service_admin" })),
      "GET /v1/admin/reports/41": [
        200,
        {
          ...REPORT,
          owner_email: "owner@example.org",
          body: {
            text: "the video stopped",
            lines: [{ ts: "2026-11-07T11:50:02Z", msg: "app_side" }],
            server: [{ ts: "2026-11-07T11:50:01Z", msg: "home_side", service: "streaming" }],
          },
        },
      ],
      "POST /v1/admin/reports/41/status": [204, null],
      "POST /v1/admin/reports/41/issue": [204, null],
      "POST /v1/admin/reports/41/reply": [204, null],
    });
    renderAt("/reports/41");
    const tl = await screen.findByTestId("cloud-report-timeline");
    const rows = within(tl).getAllByRole("listitem");
    expect(rows[0]).toHaveTextContent("home_side");
    expect(rows[1]).toHaveTextContent("app_side");
    expect(screen.getByText("the video stopped")).toBeInTheDocument();
    const u = userEvent.setup();
    await u.selectOptions(screen.getByLabelText("Status"), "fixed");
    await u.type(screen.getByLabelText("Fixed in version"), "0.3.96");
    await u.click(screen.getByRole("button", { name: "Save the status" }));
    await waitFor(() =>
      expect(f.find("POST", "/v1/admin/reports/41/status")[0]?.body).toEqual({
        status: "fixed",
        fixed_in: "0.3.96",
      })
    );
    await u.type(screen.getByLabelText("GitHub issue"), "https://github.com/x/y/issues/1");
    await u.click(screen.getByRole("button", { name: "Save the link" }));
    await waitFor(() =>
      expect(f.find("POST", "/v1/admin/reports/41/issue")[0]?.body).toEqual({
        url: "https://github.com/x/y/issues/1",
      })
    );
    await u.type(screen.getByLabelText("Reply to the home"), "Fixed in 0.3.96");
    await u.click(screen.getByRole("button", { name: "Send the reply" }));
    await waitFor(() =>
      expect(f.find("POST", "/v1/admin/reports/41/reply")[0]?.body).toEqual({
        reply: "Fixed in 0.3.96",
      })
    );
  });

  it("links the reports from the admin nav", async () => {
    stubFetch({
      "POST /v1/auth/refresh": session(user({ role: "service_admin" })),
      "GET /v1/admin/reports": [200, { reports: [], total: 0 }],
    });
    renderAt("/reports");
    const nav = await screen.findByRole("navigation", { name: "Admin" });
    expect(within(nav).getByRole("link", { name: "Problem reports" })).toHaveAttribute(
      "href",
      "/reports"
    );
    expect(await screen.findByText("No reports match.")).toBeInTheDocument();
  });

  it("a report's crash, symbolicated where the release's symbols are uploaded", async () => {
    stubFetch({
      "POST /v1/auth/refresh": session(user({ role: "service_admin" })),
      "GET /v1/admin/reports/41": [
        200,
        {
          ...REPORT,
          body: {},
          crashes: [
            {
              type: "EXC_BAD_ACCESS",
              value: "SIGSEGV",
              frames: [
                {
                  raw: "Maktaba + 0x1a2b",
                  in_app: true,
                  symbol: {
                    function: "Maktaba.Player.play",
                    file: "/src/apps/apple/Player.swift",
                    line: 42,
                  },
                },
                { raw: "libsystem_c.dylib + 0x10", in_app: false },
              ],
              missing: [{ code_file: "libsystem_c.dylib", debug_id: "1111" }],
            },
          ],
        },
      ],
    });
    renderAt("/reports/41");
    expect(await screen.findByRole("heading", { name: "Crashes" })).toBeInTheDocument();
    expect(screen.getByText("EXC_BAD_ACCESS · SIGSEGV")).toBeInTheDocument();
    const frames = within(screen.getByTestId("crash-frames")).getAllByRole("listitem");
    expect(frames[0]).toHaveTextContent("Maktaba.Player.play (Player.swift:42) · Maktaba + 0x1a2b");
    expect(frames[1]).toHaveTextContent(/^libsystem_c\.dylib \+ 0x10$/);
    expect(screen.getByTestId("crash-missing")).toHaveTextContent(
      "No symbols for libsystem_c.dylib (1111)."
    );
  });

  it("an Android crash read with the release's R8 mapping, inlined frames marked", async () => {
    stubFetch({
      "POST /v1/auth/refresh": session(user({ role: "service_admin" })),
      "GET /v1/admin/reports/41": [
        200,
        {
          ...REPORT,
          app: "android.player",
          body: {},
          crashes: [
            {
              type: "com.hamzalabs.maktaba.player.PlaybackError",
              value: "com.hamzalabs.maktaba.player.PlaybackError",
              symbols: "mapping",
              frames: [
                {
                  raw: "a.b.c.a(SourceFile:2)",
                  in_app: true,
                  inlined: true,
                  symbol: {
                    function: "com.hamzalabs.maktaba.core.Queue.next",
                    file: "Queue.kt",
                    line: 88,
                  },
                },
                {
                  raw: "",
                  in_app: true,
                  symbol: {
                    function: "com.hamzalabs.maktaba.player.PlaybackController.play",
                    file: "PlaybackController.kt",
                    line: 41,
                  },
                },
                { raw: "android.os.Handler.dispatchMessage(Handler.java:106)", in_app: false },
              ],
            },
            {
              type: "a.b.e",
              frames: [{ raw: "a.b.c.b(SourceFile:5)", in_app: true }],
              missing: [
                { code_file: "android.player", debug_id: "abcd", kind: "r8", version: "0.3.103" },
              ],
            },
          ],
        },
      ],
    });
    renderAt("/reports/41");
    expect(await screen.findByRole("heading", { name: "Crashes" })).toBeInTheDocument();
    expect(screen.getByText("com.hamzalabs.maktaba.player.PlaybackError")).toBeInTheDocument();
    expect(screen.getByTestId("crash-symbols")).toHaveTextContent(
      "Read with the release's R8 mapping"
    );
    const frames = within(screen.getAllByTestId("crash-frames")[0]).getAllByRole("listitem");
    expect(frames[0]).toHaveTextContent(
      "com.hamzalabs.maktaba.core.Queue.next (Queue.kt:88) (inlined) · a.b.c.a(SourceFile:2)"
    );
    // the caller it was inlined into has no obfuscated frame of its own
    expect(frames[1]).toHaveTextContent(
      /^com\.hamzalabs\.maktaba\.player\.PlaybackController\.play \(PlaybackController\.kt:41\)$/
    );
    expect(frames[2]).toHaveTextContent(
      /^android\.os\.Handler\.dispatchMessage\(Handler\.java:106\)$/
    );
    expect(screen.getByTestId("crash-missing-mapping")).toHaveTextContent(
      "No R8 mapping for android.player 0.3.103."
    );
    expect(screen.queryByTestId("crash-missing")).not.toBeInTheDocument();
  });

  it("a desktop crash says the app is not obfuscated", async () => {
    stubFetch({
      "POST /v1/auth/refresh": session(user({ role: "service_admin" })),
      "GET /v1/admin/reports/41": [
        200,
        {
          ...REPORT,
          app: "desktop.player",
          body: {},
          crashes: [
            {
              type: "java.lang.IllegalStateException",
              symbols: "source",
              frames: [
                {
                  raw: "com.hamzalabs.maktaba.desktop.player.Player.open(Player.kt:88)",
                  in_app: true,
                },
              ],
            },
          ],
        },
      ],
    });
    renderAt("/reports/41");
    expect(await screen.findByTestId("crash-symbols")).toHaveTextContent(
      "This app is not obfuscated: its frames are its code's own names and lines."
    );
    expect(within(screen.getByTestId("crash-frames")).getByRole("listitem")).toHaveTextContent(
      "com.hamzalabs.maktaba.desktop.player.Player.open(Player.kt:88)"
    );
    expect(screen.queryByTestId("crash-missing")).not.toBeInTheDocument();
    expect(screen.queryByTestId("crash-missing-mapping")).not.toBeInTheDocument();
  });

  it("an Android native crash read with its libraries' tables, a missing library named with its build ID", async () => {
    stubFetch({
      "POST /v1/auth/refresh": session(user({ role: "service_admin" })),
      "GET /v1/admin/reports/41": [
        200,
        {
          ...REPORT,
          app: "android.player",
          body: {},
          crashes: [
            {
              type: "SIGSEGV",
              value: "SEGV_MAPERR",
              symbols: "native",
              frames: [
                { raw: "libother.so + 0x10", in_app: true },
                {
                  raw: "libmaktaba.so + 0x684",
                  in_app: true,
                  symbol: { function: "mpv_render", file: "/src/native.c", line: 17 },
                },
                { raw: "__libc_init+0x60", in_app: false },
              ],
              missing: [
                {
                  code_file: "libother.so",
                  debug_id: "0102030405060708090a0b0c0d0e0f10",
                  kind: "elf",
                  code_id: "0102030405060708090a0b0c0d0e0f10aabbccdd",
                },
                { code_file: "libnoid.so", debug_id: "", kind: "elf" },
              ],
            },
          ],
        },
      ],
    });
    renderAt("/reports/41");
    expect(await screen.findByTestId("crash-symbols")).toHaveTextContent(
      "Read with the tables of the app's own native libraries: system libraries' frames are as they came."
    );
    const frames = within(screen.getByTestId("crash-frames")).getAllByRole("listitem");
    expect(frames).toHaveLength(3);
    expect(frames[1]).toHaveTextContent("mpv_render (native.c:17) · libmaktaba.so + 0x684");
    expect(frames[2]).toHaveTextContent("__libc_init+0x60");
    expect(screen.getByTestId("crash-missing-native")).toHaveTextContent(
      "No native symbols for libother.so (build ID 0102030405060708090a0b0c0d0e0f10aabbccdd), libnoid.so (no build ID)."
    );
    // not the dSYM or mapping notices
    expect(screen.queryByTestId("crash-missing")).not.toBeInTheDocument();
    expect(screen.queryByTestId("crash-missing-mapping")).not.toBeInTheDocument();
  });

  it("a native crash kept with its reason alone says it has no frames", async () => {
    stubFetch({
      "POST /v1/auth/refresh": session(user({ role: "service_admin" })),
      "GET /v1/admin/reports/41": [
        200,
        {
          ...REPORT,
          app: "android.tv",
          body: {},
          crashes: [{ type: "native", value: "crash (signal 11)", frames: [] }],
        },
      ],
    });
    renderAt("/reports/41");
    expect(await screen.findByTestId("crash-no-frames")).toHaveTextContent(
      "Only the reason was kept, with no frames: a native crash before Android 12, or a tombstone that could not be read."
    );
    expect(screen.getByRole("heading", { name: "native · crash (signal 11)" })).toBeInTheDocument();
    expect(screen.queryByTestId("crash-frames")).not.toBeInTheDocument();
    expect(screen.queryByTestId("crash-symbols")).not.toBeInTheDocument();
    expect(screen.queryByTestId("crash-missing-mapping")).not.toBeInTheDocument();
  });

  it("a report without crashes shows no crash section", async () => {
    stubFetch({
      "POST /v1/auth/refresh": session(user({ role: "service_admin" })),
      "GET /v1/admin/reports/41": [200, { ...REPORT, body: {} }],
    });
    renderAt("/reports/41");
    expect(await screen.findByTestId("cloud-report")).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Crashes" })).not.toBeInTheDocument();
  });

  const TABLES = [
    {
      debug_id: "9e40f33c5e1e37528634a1cd1b5ae44f",
      code_file: "Maktaba",
      arch: "arm64",
      version: "0.3.98",
      uploaded_at: "2026-11-07T12:00:00Z",
      size: 2_500_000,
      kind: "dsym",
    },
    {
      debug_id: "0f1e2d3c4b5a69788796a5b4c3d2e1f0",
      code_file: "android.tv",
      arch: "jvm",
      version: "0.3.102",
      uploaded_at: "2026-11-07T12:00:00Z",
      size: 300_000,
      kind: "r8",
    },
    {
      debug_id: "0102030405060708090a0b0c0d0e0f10",
      code_file: "libmpv.so",
      arch: "arm64",
      version: "0.3.103",
      uploaded_at: "2026-11-07T12:00:00Z",
      size: 900_000,
      kind: "elf",
      code_id: "0102030405060708090a0b0c0d0e0f10aabbccdd",
    },
  ];
  const row = (kind: string) =>
    screen.getAllByTestId("symbol-table").find((r) => r.dataset.kind === kind)!;

  it("lists the uploaded crash symbols by type, each under its own key, linked from the nav", async () => {
    stubFetch({
      "POST /v1/auth/refresh": session(user({ role: "service_admin" })),
      "GET /v1/admin/symbols": [200, { tables: TABLES }],
    });
    renderAt("/symbols");
    const table = await screen.findByTestId("symbol-tables");
    const heads = within(table)
      .getAllByRole("columnheader")
      .map((h) => h.textContent);
    expect(heads).toEqual(["Type", "Binary", "Architecture", "Version", "Key", "Uploaded", "Size"]);
    expect(table).not.toHaveTextContent("UUID");
    // an Apple dSYM's table: its binary's UUID as Apple's tools print it
    const dsym = row("dsym");
    expect(dsym).toHaveTextContent("Apple dSYM");
    expect(dsym).toHaveTextContent("arm64");
    expect(dsym).toHaveTextContent("9E40F33C-5E1E-3752-8634-A1CD1B5AE44F");
    expect(within(dsym).getByTitle("9e40f33c5e1e37528634a1cd1b5ae44f")).toBeInTheDocument();
    expect(dsym).toHaveTextContent("2,442 KB");
    // an Android release's mapping: keyed by its app and version, with no architecture
    const r8 = row("r8");
    expect(r8).toHaveTextContent("R8 mapping");
    expect(within(r8).getByTitle("0f1e2d3c4b5a69788796a5b4c3d2e1f0")).toHaveTextContent(
      "android.tv 0.3.102"
    );
    expect(r8).not.toHaveTextContent("jvm");
    // a native library: its whole build id, shortened, whole on hover and copied whole
    const elf = row("elf");
    expect(elf).toHaveTextContent("Native (build ID)");
    expect(elf).toHaveTextContent("libmpv.so");
    const id = within(elf).getByTestId("build-id");
    expect(id).toHaveTextContent("010203040506…aabbccdd");
    expect(id).toHaveAttribute("title", "0102030405060708090a0b0c0d0e0f10aabbccdd");
    expect(id).toHaveAttribute("dir", "ltr");
    const writeText = vi.fn().mockResolvedValue(undefined);
    // userEvent.setup() puts its own clipboard on navigator: stub it after
    const u = userEvent.setup();
    Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });
    await u.click(within(elf).getByRole("button", { name: "Copy build ID" }));
    expect(writeText).toHaveBeenCalledWith("0102030405060708090a0b0c0d0e0f10aabbccdd");
    expect(await within(elf).findByText("Copied")).toBeInTheDocument();
    const nav = screen.getByRole("navigation", { name: "Admin" });
    expect(within(nav).getByRole("link", { name: "Crash symbols" })).toHaveAttribute(
      "href",
      "/symbols"
    );
  });

  it("names a mapping from an API without kinds by its architecture", async () => {
    stubFetch({
      "POST /v1/auth/refresh": session(user({ role: "service_admin" })),
      "GET /v1/admin/symbols": [
        200,
        { tables: TABLES.slice(0, 2).map(({ kind: _kind, ...t }) => t) },
      ],
    });
    renderAt("/symbols");
    await screen.findByTestId("symbol-tables");
    expect(row("r8")).toHaveTextContent("R8 mapping");
    const unknown = row("unknown");
    expect(unknown).toHaveTextContent("9e40f33c5e1e37528634a1cd1b5ae44f");
    expect(unknown).not.toHaveTextContent("Apple dSYM");
  });

  it("lists the crash symbols in Arabic, right to left, keys left to right", async () => {
    localStorage.setItem("mkt:cloud:locale", "ar");
    try {
      stubFetch({
        "POST /v1/auth/refresh": session(user({ role: "service_admin" })),
        "GET /v1/admin/symbols": [200, { tables: TABLES }],
      });
      renderAt("/symbols");
      const table = await screen.findByTestId("symbol-tables");
      await waitFor(() => expect(document.documentElement.dir).toBe("rtl"));
      const heads = within(table)
        .getAllByRole("columnheader")
        .map((h) => h.textContent);
      expect(heads[0]).toBe("النوع");
      expect(heads[4]).toBe("المفتاح");
      expect(row("dsym")).toHaveTextContent("dSYM من Apple");
      expect(row("r8")).toHaveTextContent("خريطة R8");
      expect(row("elf")).toHaveTextContent("أصلية (بمعرّف البناء)");
      expect(
        within(row("elf")).getByRole("button", { name: "نسخ معرّف البناء" })
      ).toBeInTheDocument();
      expect(within(row("elf")).getByTestId("build-id")).toHaveAttribute("dir", "ltr");
    } finally {
      localStorage.clear();
    }
  });

  it("says when no symbols are uploaded", async () => {
    stubFetch({
      "POST /v1/auth/refresh": session(user({ role: "service_admin" })),
      "GET /v1/admin/symbols": [200, { tables: [] }],
    });
    renderAt("/symbols");
    expect(await screen.findByText("No symbols uploaded yet.")).toBeInTheDocument();
  });

  it("renders Access denied on the symbols page for a normal account", async () => {
    stubFetch({
      "POST /v1/auth/refresh": session(user({ role: "user" })),
      "GET /v1/admin/symbols": [403, { error: "admin_access_denied", message: "denied" }],
    });
    renderAt("/symbols");
    expect(await screen.findByText("Access denied")).toBeInTheDocument();
  });
});
