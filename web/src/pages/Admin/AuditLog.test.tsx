// Admin audit log cases from web/test-cases/admin-audit.md.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderPage, memberUser, quietCrash } from "../../test/pageHarness";
import { routeGet } from "../../test/queueHarness";

const { get } = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return {
    ...actual,
    api: { ...actual.api, get, post: vi.fn(), put: vi.fn(), patch: vi.fn(), delete: vi.fn() },
  };
});

import { ApiError } from "../../lib/api";
import { AdminAuditLog } from "./AuditLog";

const SEC = "/api/security/audit";

interface Row {
  id: number;
  event: unknown;
  actor_user_id?: unknown;
  target_id?: unknown;
  payload?: unknown;
  ts: unknown;
}

/** n rows, newest first, ids from `start` down; the last row's ts is `lastTs`. */
function page(n: number, start: number, lastTs = "2026-10-01T00:00:00.5Z"): Row[] {
  return Array.from({ length: n }, (_, i) => ({
    id: start - i,
    event: "login.failed",
    actor_user_id: "u1",
    target_id: "u1",
    ts: i === n - 1 ? lastTs : `2026-10-18T10:${String(59 - (i % 60)).padStart(2, "0")}:00Z`,
  }));
}

function problem(status: number, title: string, detail?: string) {
  return new ApiError({ type: "about:blank", title, status, detail });
}

const open = () => renderPage(<AdminAuditLog />, { path: "/admin/audit" });

function bodyRows() {
  const table = screen.getByRole("table", { name: "Audit log" });
  return within(table).getAllByRole("row").slice(1);
}

/** Captures the Blob passed to URL.createObjectURL by Export CSV. */
function captureExport() {
  const blobs: Blob[] = [];
  const names: string[] = [];
  const create = vi.fn((b: Blob) => {
    blobs.push(b);
    return "blob:audit";
  });
  const revoke = vi.fn();
  Object.defineProperty(URL, "createObjectURL", { value: create, configurable: true });
  Object.defineProperty(URL, "revokeObjectURL", { value: revoke, configurable: true });
  const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (
    this: HTMLAnchorElement
  ) {
    names.push(this.download);
  });
  return { blobs, names, create, revoke, click };
}

function readBlob(b: Blob): Promise<string> {
  return new Promise((resolve) => {
    const fr = new FileReader();
    fr.onload = () =>
      resolve(new TextDecoder("utf-8", { ignoreBOM: true }).decode(fr.result as ArrayBuffer));
    fr.readAsArrayBuffer(b);
  });
}

describe("AdminAuditLog (test-cases/admin-audit.md)", () => {
  beforeEach(() => {
    get.mockReset();
  });
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("AUD-01: security feed loads and renders rows", async () => {
    routeGet(get, {
      [SEC]: {
        items: [
          {
            id: 2,
            event: "login.failed",
            actor_user_id: "u1",
            target_id: "u1",
            payload: { ip: "10.0.0.2" },
            ts: "2026-10-18T10:00:00.123456789Z",
          },
          {
            id: 1,
            event: "user.created",
            actor_user_id: "u1",
            target_id: "u9",
            payload: "note",
            ts: "2026-10-17T09:00:00Z",
          },
        ],
      },
    });
    open();
    expect(await screen.findByRole("table", { name: "Audit log" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { level: 1, name: "Audit log" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Refresh" })).toBeEnabled();
    expect(screen.getByRole("button", { name: "Export CSV" })).toBeEnabled();
    expect(screen.getByLabelText("Source")).toHaveValue("security");
    expect(screen.queryByLabelText("Library ID")).toBeNull();
    for (const h of ["Timestamp", "Event", "Actor", "Target", "Details"]) {
      expect(screen.getByRole("columnheader", { name: h })).toBeInTheDocument();
    }
    const rows = bodyRows();
    expect(rows).toHaveLength(2);
    const c1 = within(rows[0]).getAllByRole("cell");
    expect(c1[0].textContent).toMatch(/2026/);
    expect(c1.slice(1).map((c) => c.textContent)).toEqual([
      "login.failed",
      "u1",
      "u1",
      '{"ip":"10.0.0.2"}',
    ]);
    expect(within(rows[1]).getAllByRole("cell")[4]).toHaveTextContent("note");
    expect(get).toHaveBeenCalledTimes(1);
    expect(get).toHaveBeenCalledWith(`${SEC}?limit=50`);
    expect(screen.queryByRole("button", { name: "Load more" })).toBeNull();
  });

  it("AUD-03: empty feed shows the empty state", async () => {
    routeGet(get, { [SEC]: { items: [] } });
    open();
    expect(await screen.findByText("No audit events")).toBeInTheDocument();
    expect(screen.getByText("No events match the current source and filters.")).toBeInTheDocument();
    expect(screen.queryByRole("table")).toBeNull();
    expect(screen.queryByRole("button", { name: "Load more" })).toBeNull();
    expect(screen.getByRole("button", { name: "Export CSV" })).toBeDisabled();
    const ev = screen.getByLabelText("Event");
    expect(
      within(ev)
        .getAllByRole("option")
        .map((o) => o.textContent)
    ).toEqual(["All events"]);
  });

  it.each([
    ["items null", { items: null }],
    ["items missing", {}],
  ])("AUD-04: %s shows the empty state (security and library feeds)", async (_, body) => {
    const user = userEvent.setup();
    routeGet(get, { [SEC]: body, "/api/libraries/l1/audit": body });
    open();
    expect(await screen.findByText("No audit events")).toBeInTheDocument();
    await user.selectOptions(screen.getByLabelText("Source"), "library");
    await user.type(screen.getByLabelText("Library ID"), "l1");
    await waitFor(() => expect(get).toHaveBeenCalledWith("/api/libraries/l1/audit?limit=50"));
    expect(await screen.findByText("No audit events")).toBeInTheDocument();
    expect(screen.queryByText("This page hit an error and could not be shown.")).toBeNull();
  });

  it("AUD-04: a null Load more page keeps the rows and hides Load more", async () => {
    const user = userEvent.setup();
    routeGet(get, {
      [SEC]: (url: string) => (url.includes("cursor") ? { items: null } : { items: page(50, 100) }),
    });
    open();
    await user.click(await screen.findByRole("button", { name: "Load more" }));
    await waitFor(() => expect(screen.queryByRole("button", { name: "Load more" })).toBeNull());
    expect(bodyRows()).toHaveLength(50);
  });

  it("AUD-05: null or missing optional fields render without a crash", async () => {
    const user = userEvent.setup();
    routeGet(get, {
      [SEC]: {
        items: [
          {
            id: 3,
            event: "x",
            actor_user_id: null,
            target_id: null,
            payload: null,
            ts: "2026-10-18T10:00:00Z",
          },
          { id: 4, event: "y", ts: "2026-10-18T09:00:00Z" },
          { id: 5, event: "z", payload: {}, ts: "" },
          { id: 7, event: "w", ts: null },
          { id: 6, event: null, ts: "2026-10-18T08:00:00Z" },
        ],
      },
    });
    open();
    await screen.findByRole("table", { name: "Audit log" });
    const cells = bodyRows().map((r) =>
      within(r)
        .getAllByRole("cell")
        .map((c) => c.textContent)
    );
    expect(cells[0].slice(2)).toEqual(["—", "—", ""]);
    expect(cells[1].slice(2)).toEqual(["—", "—", ""]);
    expect(cells[2][4]).toBe("{}");
    // empty and null ts show a dash, not "" or 1 Jan 1970
    expect(cells[2][0]).toBe("—");
    expect(cells[3][0]).toBe("—");
    expect(cells[3][0]).not.toMatch(/1970/);
    // event null adds no blank option
    const opts = within(screen.getByLabelText("Event"))
      .getAllByRole("option")
      .map((o) => o.textContent);
    expect(opts).toEqual(["All events", "w", "x", "y", "z"]);
    await user.type(screen.getByLabelText("Actor"), "u");
    expect(await screen.findByText("No audit events")).toBeInTheDocument();
    expect(screen.queryByText("This page hit an error and could not be shown.")).toBeNull();
  });

  it("AUD-06: Load more pages with the URL-encoded timestamp cursor", async () => {
    const user = userEvent.setup();
    let resolve2: (v: unknown) => void = () => {};
    routeGet(get, {
      [SEC]: (url: string) =>
        url.includes("cursor")
          ? new Promise((r) => {
              resolve2 = r;
            })
          : { items: page(50, 100) },
    });
    open();
    const more = await screen.findByRole("button", { name: "Load more" });
    await user.click(more);
    expect(get).toHaveBeenLastCalledWith(`${SEC}?limit=50&cursor=2026-10-01T00%3A00%3A00.5Z`);
    expect(screen.getByRole("button", { name: "Load more" })).toHaveAttribute("aria-busy", "true");
    resolve2({ items: page(10, 50) });
    await waitFor(() => expect(bodyRows()).toHaveLength(60));
    expect(screen.queryByRole("button", { name: "Load more" })).toBeNull();
    // filters still apply to all 60 rows
    await user.type(screen.getByLabelText("Actor"), "nobody");
    expect(await screen.findByText("No audit events")).toBeInTheDocument();
  });

  it("AUD-07: Load more hidden for a short page, and after an empty page 2", async () => {
    routeGet(get, { [SEC]: { items: page(49, 100) } });
    const { unmount } = open();
    await screen.findByRole("table", { name: "Audit log" });
    expect(screen.queryByRole("button", { name: "Load more" })).toBeNull();
    unmount();

    const user = userEvent.setup();
    routeGet(get, {
      [SEC]: (url: string) => ({ items: url.includes("cursor") ? [] : page(50, 100) }),
    });
    open();
    await user.click(await screen.findByRole("button", { name: "Load more" }));
    await waitFor(() => expect(screen.queryByRole("button", { name: "Load more" })).toBeNull());
    expect(bodyRows()).toHaveLength(50);
  });

  it("AUD-08: a Load more failure keeps the loaded rows and retries the next page", async () => {
    const user = userEvent.setup();
    let fail = true;
    routeGet(get, {
      [SEC]: (url: string) => {
        if (!url.includes("cursor")) return { items: page(50, 100) };
        if (fail) return Promise.reject(problem(500, "internal", "db down"));
        return { items: page(5, 50) };
      },
    });
    open();
    await user.click(await screen.findByRole("button", { name: "Load more" }));
    expect(await screen.findByText("db down")).toBeInTheDocument();
    expect(bodyRows()).toHaveLength(50);
    fail = false;
    await user.click(screen.getByRole("button", { name: "Try again" }));
    await waitFor(() => expect(bodyRows()).toHaveLength(55));
    expect(get).toHaveBeenLastCalledWith(expect.stringContaining("cursor="));
    expect(screen.queryByText("db down")).toBeNull();
  });

  it("AUD-09: Library source needs an id; spaces only count as empty", async () => {
    const user = userEvent.setup();
    routeGet(get, { [SEC]: { items: page(2, 2) } });
    open();
    await screen.findByRole("table", { name: "Audit log" });
    await user.selectOptions(screen.getByLabelText("Source"), "library");
    expect(screen.getByLabelText("Library ID")).toBeInTheDocument();
    expect(screen.getByText("Enter a library ID to load its audit feed.")).toBeInTheDocument();
    await user.type(screen.getByLabelText("Library ID"), "   ");
    expect(screen.getByText("Enter a library ID to load its audit feed.")).toBeInTheDocument();
    expect(screen.queryByRole("table")).toBeNull();
    expect(screen.getByRole("button", { name: "Export CSV" })).toBeDisabled();
    expect(get.mock.calls.some(([u]) => String(u).startsWith("/api/libraries/"))).toBe(false);
  });

  it("AUD-10: library feed URL-encodes the id and pages by the last row id", async () => {
    const user = userEvent.setup();
    const LIB = "/api/libraries/lib%201/audit";
    const first = page(50, 169).map((r, i) => (i === 49 ? { ...r, id: 120 } : r));
    routeGet(get, {
      [SEC]: { items: page(2, 2) },
      [LIB]: (url: string) =>
        url.includes("cursor") ? { items: page(5, 119) } : { items: first, next_cursor: 120 },
    });
    open();
    await screen.findByRole("table", { name: "Audit log" });
    await user.selectOptions(screen.getByLabelText("Source"), "library");
    await user.type(screen.getByLabelText("Library ID"), "lib 1");
    await waitFor(() => expect(get).toHaveBeenCalledWith(`${LIB}?limit=50`));
    await user.click(await screen.findByRole("button", { name: "Load more" }));
    expect(get).toHaveBeenLastCalledWith(`${LIB}?limit=50&cursor=120`);
    await waitFor(() => expect(bodyRows()).toHaveLength(55));
    get.mockClear();
    await user.selectOptions(screen.getByLabelText("Source"), "security");
    await waitFor(() => expect(get).toHaveBeenCalledWith(`${SEC}?limit=50`));
    await waitFor(() => expect(bodyRows()).toHaveLength(2));
  });

  it("AUD-11: a slow response for an old library id does not overwrite the current one", async () => {
    const user = userEvent.setup();
    let slow: (v: unknown) => void = () => {};
    routeGet(get, {
      [SEC]: { items: [] },
      "/api/libraries/l/audit": () =>
        new Promise((r) => {
          slow = r;
        }),
      "/api/libraries/l1/audit": {
        items: [{ id: 2, event: "rows.B", ts: "2026-10-18T10:00:00Z" }],
      },
    });
    open();
    await screen.findByText("No audit events");
    await user.selectOptions(screen.getByLabelText("Source"), "library");
    await user.type(screen.getByLabelText("Library ID"), "l1");
    expect(await screen.findByRole("cell", { name: "rows.B" })).toBeInTheDocument();
    slow({ items: [{ id: 1, event: "rows.A", ts: "2026-10-18T10:00:00Z" }] });
    await new Promise((r) => setTimeout(r, 0));
    expect(screen.getByRole("cell", { name: "rows.B" })).toBeInTheDocument();
    expect(screen.queryByRole("cell", { name: "rows.A" })).toBeNull();
  });

  it("AUD-12: library 404 shows the error, fixing the id loads rows", async () => {
    const user = userEvent.setup();
    routeGet(get, {
      [SEC]: { items: [] },
      "/api/libraries/nope/audit": () =>
        Promise.reject(problem(404, "not found", "library not found")),
      "/api/libraries/ok/audit": { items: page(1, 1) },
    });
    open();
    await screen.findByText("No audit events");
    await user.selectOptions(screen.getByLabelText("Source"), "library");
    await user.type(screen.getByLabelText("Library ID"), "nope");
    expect(await screen.findByText("library not found")).toBeInTheDocument();
    expect(screen.getByText("Something went wrong.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Try again" })).toBeInTheDocument();
    await user.clear(screen.getByLabelText("Library ID"));
    await user.type(screen.getByLabelText("Library ID"), "ok");
    await waitFor(() => expect(bodyRows()).toHaveLength(1));
    expect(screen.queryByText("library not found")).toBeNull();
  });

  it.each([
    ["403 problem", problem(403, "forbidden"), "forbidden"],
    ["500 text body", problem(500, "boom"), "boom"],
    ["fetch rejects", new TypeError("Failed to fetch"), null],
  ])("AUD-13: initial load error (%s) + Try again", async (_, err, desc) => {
    const user = userEvent.setup();
    let fail = true;
    routeGet(get, { [SEC]: () => (fail ? Promise.reject(err) : { items: page(1, 1) }) });
    open();
    expect((await screen.findAllByText("Something went wrong."))[0]).toBeInTheDocument();
    if (desc) expect(screen.getByText(desc)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Export CSV" })).toBeDisabled();
    fail = false;
    await user.click(screen.getByRole("button", { name: "Try again" }));
    await waitFor(() => expect(bodyRows()).toHaveLength(1));
  });

  it.todo("AUD-13: a 401 mid-use redirects to /login (app-wide: lib/api.ts / lib/auth.tsx)");

  it("AUD-14: event filter lists distinct events sorted and resets on source change", async () => {
    const user = userEvent.setup();
    const ts = "2026-10-18T10:00:00Z";
    routeGet(get, {
      [SEC]: {
        items: [
          { id: 4, event: "login.failed", ts },
          { id: 3, event: "user.created", ts },
          { id: 2, event: "login.failed", ts },
          { id: 1, event: "token.revoked", ts },
        ],
      },
      "/api/libraries/l1/audit": { items: [{ id: 9, event: "library.scan", ts }] },
    });
    open();
    await screen.findByRole("table", { name: "Audit log" });
    const ev = screen.getByLabelText("Event");
    expect(
      within(ev)
        .getAllByRole("option")
        .map((o) => o.textContent)
    ).toEqual(["All events", "login.failed", "token.revoked", "user.created"]);
    await user.selectOptions(ev, "login.failed");
    expect(bodyRows()).toHaveLength(2);
    await user.selectOptions(ev, "");
    expect(bodyRows()).toHaveLength(4);
    await user.selectOptions(ev, "user.created");
    expect(bodyRows()).toHaveLength(1);
    await user.selectOptions(screen.getByLabelText("Source"), "library");
    await user.type(screen.getByLabelText("Library ID"), "l1");
    expect(await screen.findByRole("cell", { name: "library.scan" })).toBeInTheDocument();
    expect(screen.getByLabelText("Event")).toHaveValue("");
  });

  it("AUD-15: actor filter is a case-sensitive substring match", async () => {
    const user = userEvent.setup();
    const ts = "2026-10-18T10:00:00Z";
    routeGet(get, {
      [SEC]: {
        items: [
          { id: 3, event: "a", actor_user_id: "u1-alice", ts },
          { id: 2, event: "b", actor_user_id: "u2-bob", ts },
          { id: 1, event: "c", ts },
        ],
      },
    });
    open();
    await screen.findByRole("table", { name: "Audit log" });
    const actor = screen.getByLabelText("Actor");
    await user.type(actor, "u1");
    expect(bodyRows()).toHaveLength(1);
    await user.clear(actor);
    await user.type(actor, "U1");
    expect(screen.getByText("No audit events")).toBeInTheDocument();
    await user.clear(actor);
    expect(bodyRows()).toHaveLength(3);
  });

  it("AUD-16: date range filter in local time; To includes the whole minute", async () => {
    const user = userEvent.setup();
    const local = (d: number, h: number, m: number, s = 0) =>
      new Date(2026, 9, d, h, m, s).toISOString();
    routeGet(get, {
      [SEC]: {
        items: [
          { id: 3, event: "c", ts: local(19, 12, 0) },
          { id: 2, event: "b", ts: local(18, 10, 0, 30) },
          { id: 1, event: "a", ts: local(17, 9, 0) },
        ],
      },
    });
    open();
    await screen.findByRole("table", { name: "Audit log" });
    const from = screen.getByLabelText("From");
    const to = screen.getByLabelText("To");
    expect(from).toHaveAttribute("type", "datetime-local");
    await user.type(from, "2026-10-18T00:00");
    expect(bodyRows().map((r) => within(r).getAllByRole("cell")[1].textContent)).toEqual([
      "c",
      "b",
    ]);
    await user.type(to, "2026-10-18T10:00");
    expect(bodyRows().map((r) => within(r).getAllByRole("cell")[1].textContent)).toEqual(["b"]);
    await user.clear(from);
    await user.type(from, "2026-10-19T00:00");
    expect(screen.getByText("No audit events")).toBeInTheDocument();
  });

  it("AUD-17: filters that hide every row keep Load more reachable", async () => {
    const user = userEvent.setup();
    routeGet(get, { [SEC]: { items: page(50, 100) } });
    open();
    await screen.findByRole("table", { name: "Audit log" });
    await user.type(screen.getByLabelText("Actor"), "zzz");
    expect(screen.getByText("No audit events")).toBeInTheDocument();
    expect(screen.getByText("No events match the current source and filters.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Export CSV" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Load more" })).toBeInTheDocument();
  });

  it("AUD-19: Export CSV content, escaping and file name", async () => {
    const user = userEvent.setup();
    const cap = captureExport();
    routeGet(get, {
      [SEC]: {
        items: [
          {
            id: 2,
            event: "msg",
            target_id: "t2",
            payload: { msg: 'a, "b"\nc' },
            ts: "2026-10-18T10:00:00Z",
          },
          {
            id: 1,
            event: "other",
            actor_user_id: "u1",
            payload: "مرحبا",
            ts: "2026-10-17T10:00:00Z",
          },
          { id: 0, event: "msg", ts: "2026-10-16T10:00:00Z" },
        ],
      },
    });
    open();
    await screen.findByRole("table", { name: "Audit log" });
    await user.selectOptions(screen.getByLabelText("Event"), "msg");
    await user.click(screen.getByRole("button", { name: "Export CSV" }));
    expect(cap.names).toEqual(["audit-security.csv"]);
    expect(cap.blobs[0].type).toBe("text/csv;charset=utf-8");
    expect(cap.revoke).toHaveBeenCalledWith("blob:audit");
    const text = await readBlob(cap.blobs[0]);
    expect(text.startsWith("﻿")).toBe(true);
    expect(text.slice(1).split("\n")[0]).toBe("id,ts,event,actor_user_id,target_id,payload");
    expect(text).toContain('2,2026-10-18T10:00:00Z,msg,,t2,"{""msg"":""a, \\""b\\""\\nc""}"');
    expect(text).toContain("0,2026-10-16T10:00:00Z,msg,,,{}");
    expect(text).not.toContain("other");
    // Arabic payload survives as UTF-8
    await user.selectOptions(screen.getByLabelText("Event"), "other");
    await user.click(screen.getByRole("button", { name: "Export CSV" }));
    expect(await readBlob(cap.blobs[1])).toContain("مرحبا");
  });

  it("AUD-20: CSV export neutralises cells that start with = + - @", async () => {
    const user = userEvent.setup();
    const cap = captureExport();
    routeGet(get, {
      [SEC]: {
        items: [
          {
            id: 1,
            event: "@SUM(A1)",
            actor_user_id: "+cmd",
            target_id: '=HYPERLINK("http://evil")',
            payload: "x",
            ts: "-1",
          },
          { id: 2, event: "ok", actor_user_id: "u1", target_id: "a=b", ts: "2026-10-18T10:00:00Z" },
        ],
      },
    });
    open();
    await screen.findByRole("table", { name: "Audit log" });
    await user.click(screen.getByRole("button", { name: "Export CSV" }));
    const lines = (await readBlob(cap.blobs[0])).replace(/^﻿/, "").split("\n");
    expect(lines[1]).toBe(`1,'-1,'@SUM(A1),'+cmd,"'=HYPERLINK(""http://evil"")","""x"""`);
    // a value with = later in the text is left alone
    expect(lines[2]).toBe("2,2026-10-18T10:00:00Z,ok,u1,a=b,{}");
    for (const line of lines.slice(1)) {
      for (const cell of line.split(",")) expect(cell).not.toMatch(/^"?[=+\-@]/);
    }
  });

  it("AUD-21: a page crash shows the route error fallback; Back works", async () => {
    const user = userEvent.setup();
    quietCrash();
    routeGet(get, {
      [SEC]: { items: [{ id: 1, event: "x", actor_user_id: {}, ts: "2026-10-18T10:00:00Z" }] },
    });
    renderPage(<AdminAuditLog />, { path: "/admin/audit" });
    expect(
      await screen.findByText("This page hit an error and could not be shown.")
    ).toBeInTheDocument();
    expect(screen.getByRole("navigation", { name: "test shell" })).toBeInTheDocument();
    await user.click(screen.getByRole("link", { name: "go elsewhere" }));
    expect(await screen.findByText("elsewhere")).toBeInTheDocument();
    routeGet(get, { [SEC]: { items: page(1, 1) } });
    await user.click(screen.getByRole("button", { name: "go back" }));
    expect(await screen.findByRole("table", { name: "Audit log" })).toBeInTheDocument();
  });

  it("AUD-21: non-array items is treated as empty instead of crashing", async () => {
    routeGet(get, { [SEC]: { items: {} } });
    open();
    expect(await screen.findByText("No audit events")).toBeInTheDocument();
    expect(screen.queryByText("This page hit an error and could not be shown.")).toBeNull();
  });

  it("AUD-22: non-admin sees Admins only and no audit request", async () => {
    routeGet(get, { [SEC]: { items: [] } });
    renderPage(<AdminAuditLog />, { path: "/admin/audit", user: memberUser });
    expect(await screen.findByText("Admins only")).toBeInTheDocument();
    expect(
      screen.getByText("You need administrator access to view this page.")
    ).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Export CSV" })).toBeNull();
    expect(screen.queryByLabelText("Actor")).toBeNull();
    expect(get).not.toHaveBeenCalled();
  });

  it("AUD-23: signed-out user is sent to /login", async () => {
    renderPage(<AdminAuditLog />, { path: "/admin/audit", user: null });
    expect(await screen.findByText("login page (from /admin/audit)")).toBeInTheDocument();
    expect(get).not.toHaveBeenCalled();
  });

  it("AUD-28: every filter has a label and the toolbar is a toolbar", async () => {
    const user = userEvent.setup();
    routeGet(get, { [SEC]: { items: page(1, 1) } });
    open();
    await screen.findByRole("table", { name: "Audit log" });
    expect(screen.getByRole("toolbar")).toBeInTheDocument();
    await user.selectOptions(screen.getByLabelText("Source"), "library");
    for (const l of ["Source", "Library ID", "Event", "Actor", "From", "To"]) {
      expect(screen.getByLabelText(l)).toBeInTheDocument();
    }
  });
});
