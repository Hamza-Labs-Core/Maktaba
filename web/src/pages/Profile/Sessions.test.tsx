// Active sessions page cases from web/test-cases/account-sessions.md.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { get, post, put, patch, del } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  put: vi.fn(),
  patch: vi.fn(),
  del: vi.fn(),
}));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post, put, patch, delete: del } };
});

import { ApiError } from "../../lib/api";
import { ToastProvider } from "@ds/components/Toast/Toast";
import { renderPage, quietCrash } from "../../test/pageHarness";
import { routeGet } from "../../test/queueHarness";
import ar from "../../i18n/ar.json";
import { Sessions } from "./Sessions";

const S1 = {
  id: "s1",
  created_at: "2026-10-01T09:00:00Z",
  last_seen_at: "2026-10-19T07:00:00Z",
  expires_at: "2026-11-01T09:00:00Z",
  ip: "192.168.1.20",
  user_agent: "Mozilla/5.0 (Macintosh) Firefox/131",
  current: true,
};
const S2 = {
  id: "s2",
  created_at: "2026-10-10T09:00:00Z",
  last_seen_at: "2026-10-18T07:00:00Z",
  expires_at: "2026-11-10T09:00:00Z",
  ip: "10.0.0.5",
  user_agent: "Maktaba-iOS/1.4",
  current: false,
};
const S3 = { ...S2, id: "s3", ip: "10.0.0.6", user_agent: "Maktaba-Android/2.0" };

function problem(status: number, title: string, detail?: string) {
  return new ApiError({ type: "about:blank", status, title, detail });
}

function deferred<T>() {
  let resolve!: (v: T) => void;
  const promise = new Promise<T>((r) => (resolve = r));
  return { promise, resolve };
}

function sessionsSeq(...replies: unknown[]) {
  let i = 0;
  get.mockImplementation((url: string) => {
    if (url !== "/api/me/sessions") return Promise.reject(new Error(`unmocked GET ${url}`));
    const r = replies[Math.min(i++, replies.length - 1)];
    return r instanceof Error ? Promise.reject(r) : Promise.resolve(r);
  });
}

function open(opts: { user?: null } = {}) {
  return renderPage(
    <ToastProvider>
      <Sessions />
    </ToastProvider>,
    { path: "/account/sessions", ...opts }
  );
}

const row = (text: string) => screen.getByText(text).closest("tr") as HTMLElement;

describe("Sessions (test-cases/account-sessions.md)", () => {
  beforeEach(() => {
    for (const f of [get, post, put, patch, del]) f.mockReset();
    routeGet(get, { "/api/me/sessions": { items: [S1, S2] } });
  });

  it("ASE-01: sessions table renders with the current session badged", async () => {
    open();
    const table = await screen.findByRole("table", { name: "Active sessions" });
    expect(screen.getByRole("heading", { level: 1, name: "Active sessions" })).toBeInTheDocument();
    expect(
      screen.getByText("Devices and browsers currently signed in to your account.")
    ).toBeInTheDocument();
    const headers = within(table)
      .getAllByRole("columnheader")
      .map((h) => h.textContent);
    expect(headers).toEqual(["Device", "IP address", "Signed in", "Last active", "Actions"]);
    const r1 = row("Mozilla/5.0 (Macintosh) Firefox/131");
    expect(within(r1).getByText("This session")).toBeInTheDocument();
    expect(within(r1).getByText("192.168.1.20")).toHaveClass("mkt-mono");
    expect(within(r1).getByText("Current")).toBeInTheDocument();
    const r2 = row("Maktaba-iOS/1.4");
    expect(within(r2).getByRole("button", { name: "Revoke" })).toBeInTheDocument();
    expect(table).not.toHaveTextContent("2026-11");
  });

  it("ASE-02: the current session cannot be revoked", async () => {
    open();
    await screen.findByRole("table");
    expect(within(row("Mozilla/5.0 (Macintosh) Firefox/131")).queryByRole("button")).toBeNull();
    expect(screen.getAllByRole("button", { name: "Revoke" })).toHaveLength(1);
  });

  it("ASE-03: revoke another session", async () => {
    sessionsSeq({ items: [S1, S2] }, { items: [S1] });
    del.mockResolvedValue(undefined);
    open();
    await screen.findByRole("table");
    await userEvent.setup().click(screen.getByRole("button", { name: "Revoke" }));
    expect(await screen.findByRole("status")).toHaveTextContent("Session revoked.");
    expect(del).toHaveBeenCalledTimes(1);
    expect(del).toHaveBeenCalledWith("/api/me/sessions/s2");
    await waitFor(() => expect(screen.queryByText("Maktaba-iOS/1.4")).toBeNull());
    expect(screen.getByText("Mozilla/5.0 (Macintosh) Firefox/131")).toBeInTheDocument();
    expect(screen.queryByText(/login page/)).toBeNull();
  });

  it("ASE-04: revoke goes out on one click (no confirm dialog today)", async () => {
    del.mockResolvedValue(undefined);
    open();
    await screen.findByRole("table");
    await userEvent.setup().click(screen.getByRole("button", { name: "Revoke" }));
    expect(screen.queryByRole("dialog")).toBeNull();
    await waitFor(() => expect(del).toHaveBeenCalledTimes(1));
  });

  it.each([
    ["404 with detail", problem(404, "Not Found", "session not found"), "session not found"],
    ["403 without detail", problem(403, "Forbidden"), "Forbidden"],
    ["500 without detail", problem(500, "Internal Server Error"), "Internal Server Error"],
    ["network failure", new TypeError("Failed to fetch"), "Something went wrong."],
  ])("ASE-05: revoke fails (%s): toast, row stays, button usable", async (_, err, msg) => {
    del.mockRejectedValue(err);
    open();
    await screen.findByRole("table");
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Revoke" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(msg);
    const button = within(row("Maktaba-iOS/1.4")).getByRole("button", { name: "Revoke" });
    expect(button).toBeEnabled();
    expect(button).not.toHaveAttribute("aria-busy");
    expect(get).toHaveBeenCalledTimes(1);
    await user.click(button);
    expect(del).toHaveBeenCalledTimes(2);
  });

  it("ASE-06: revoke 401 never leaves a blank page", async () => {
    del.mockRejectedValue(problem(401, "Unauthorized"));
    open();
    await screen.findByRole("table");
    await userEvent.setup().click(screen.getByRole("button", { name: "Revoke" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Unauthorized");
    expect(screen.getByRole("table")).toBeInTheDocument();
  });

  // Desired: a 401 sends the user to sign in (shared 401 handling in
  // src/lib/api.ts / src/lib/auth.tsx).
  it.fails("ASE-06: revoke 401 redirects to /login", async () => {
    del.mockRejectedValue(problem(401, "Unauthorized"));
    open();
    await screen.findByRole("table");
    await userEvent.setup().click(screen.getByRole("button", { name: "Revoke" }));
    expect(await screen.findByText(/login page/)).toBeInTheDocument();
  });

  it("ASE-07: each row keeps its own busy state; one DELETE per row", async () => {
    sessionsSeq({ items: [S1, S2, S3] }, { items: [S1] });
    const d2 = deferred<undefined>();
    const d3 = deferred<undefined>();
    del.mockImplementation((url: string) => (url.endsWith("s2") ? d2.promise : d3.promise));
    open();
    await screen.findByRole("table");
    const user = userEvent.setup();
    const b2 = within(row("Maktaba-iOS/1.4")).getByRole("button", { name: "Revoke" });
    const b3 = within(row("Maktaba-Android/2.0")).getByRole("button", { name: "Revoke" });
    await user.click(b2);
    await user.click(b2);
    expect(b2).toHaveAttribute("aria-busy", "true");
    expect(b2).toBeDisabled();
    await user.click(b3);
    expect(del).toHaveBeenCalledTimes(2);
    expect(b2).toHaveAttribute("aria-busy", "true");
    expect(b3).toHaveAttribute("aria-busy", "true");

    d2.resolve(undefined);
    await waitFor(() => expect(b3).toHaveAttribute("aria-busy", "true"));
    d3.resolve(undefined);
    await waitFor(() => expect(screen.queryByText("Maktaba-Android/2.0")).toBeNull());
    expect(screen.queryByText("Maktaba-iOS/1.4")).toBeNull();
  });

  it("ASE-08: items [] shows the empty state", async () => {
    routeGet(get, { "/api/me/sessions": { items: [] } });
    open();
    expect(await screen.findByText("No active sessions")).toBeInTheDocument();
    expect(screen.getByText("You have no other active sessions.")).toBeInTheDocument();
    expect(screen.queryByRole("table")).toBeNull();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it.each([
    ["items null", { items: null }],
    ["items missing", {}],
    ["HTML body", "<!doctype html><html></html>"],
  ])("ASE-09: %s shows the empty state", async (_, body) => {
    routeGet(get, { "/api/me/sessions": body });
    open();
    expect(await screen.findByText("No active sessions")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("ASE-09: a null body shows the error state with Retry", async () => {
    routeGet(get, { "/api/me/sessions": null });
    open();
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Something went wrong.");
    expect(within(alert).getByRole("button", { name: "Try again" })).toBeInTheDocument();
  });

  it("ASE-10: null / empty ip and user_agent; null dates", async () => {
    routeGet(get, {
      "/api/me/sessions": {
        items: [
          { ...S2, id: "a", user_agent: null },
          { ...S2, id: "b", user_agent: "" },
          { ...S2, id: "c", user_agent: "c-agent", ip: null },
          { ...S2, id: "d", user_agent: "d-agent", ip: "" },
          { ...S2, id: "e", user_agent: "e-agent", last_seen_at: null },
          { ...S2, id: "f", user_agent: "f-agent", created_at: undefined },
          { ...S2, id: "g", user_agent: "g-agent", current: undefined },
        ],
      },
    });
    open();
    await screen.findByRole("table");
    expect(screen.getAllByRole("row")).toHaveLength(8);
    expect(screen.getAllByText("Unknown device")).toHaveLength(2);
    expect(within(row("c-agent")).getByText("—")).toBeInTheDocument();
    expect(within(row("d-agent")).getByText("—")).toBeInTheDocument();
    expect(within(row("e-agent")).getByText("—")).toBeInTheDocument();
    expect(within(row("f-agent")).getByText("—")).toBeInTheDocument();
    expect(screen.getByRole("table")).not.toHaveTextContent("1970");
    expect(screen.getByRole("table")).not.toHaveTextContent("undefined");
    expect(within(row("g-agent")).getByRole("button", { name: "Revoke" })).toBeInTheDocument();
  });

  it("ASE-11: no row has current: true", async () => {
    routeGet(get, { "/api/me/sessions": { items: [{ ...S1, current: false }, S2] } });
    open();
    await screen.findByRole("table");
    expect(screen.getAllByRole("button", { name: "Revoke" })).toHaveLength(2);
    expect(screen.queryByText("This session")).toBeNull();
  });

  it.each([
    ["500 with detail", problem(500, "Internal Server Error", "db down"), "db down"],
    ["404", problem(404, "Not Found"), "Not Found"],
    ["network failure", new TypeError("Failed to fetch"), "Something went wrong."],
    ["malformed JSON", new SyntaxError("Unexpected token"), "Something went wrong."],
  ])("ASE-13: list load error (%s) and Retry", async (_, err, msg) => {
    sessionsSeq(err, { items: [S1, S2] });
    open();
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Something went wrong.");
    expect(alert).toHaveTextContent(msg);
    await userEvent.setup().click(within(alert).getByRole("button", { name: "Try again" }));
    expect(await screen.findByRole("table")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("ASE-14: 401 on list load never leaves a blank page", async () => {
    sessionsSeq(problem(401, "Unauthorized"));
    open();
    expect(await screen.findByRole("alert")).toHaveTextContent("Unauthorized");
  });

  // Desired: redirect to /login (shared 401 handling, src/lib/api.ts / auth.tsx).
  it.fails("ASE-14: 401 on list load redirects to /login", async () => {
    sessionsSeq(problem(401, "Unauthorized"));
    open();
    expect(await screen.findByText("login page (from /account/sessions)")).toBeInTheDocument();
  });

  it("ASE-15: signed out deep link redirects to /login", async () => {
    open({ user: null });
    expect(await screen.findByText("login page (from /account/sessions)")).toBeInTheDocument();
    expect(get).not.toHaveBeenCalled();
  });

  it("ASE-16: crash during render shows the route fallback; Back recovers", async () => {
    const quiet = quietCrash();
    routeGet(get, { "/api/me/sessions": { items: [{ ...S2, user_agent: { ua: "x" } }] } });
    open();
    const user = userEvent.setup();
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "This page hit an error and could not be shown."
    );
    await user.click(screen.getByRole("link", { name: "go elsewhere" }));
    expect(await screen.findByText("elsewhere")).toBeInTheDocument();

    routeGet(get, { "/api/me/sessions": { items: [S1, S2] } });
    await user.click(screen.getByRole("button", { name: "go back" }));
    expect(await screen.findByRole("table")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).toBeNull();
    quiet.mockRestore();
  });

  it("ASE-17: Back after a revoke re-fetches the current server list", async () => {
    sessionsSeq({ items: [S1, S2] }, { items: [S1] });
    del.mockResolvedValue(undefined);
    open();
    await screen.findByRole("table");
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Revoke" }));
    await waitFor(() => expect(screen.queryByText("Maktaba-iOS/1.4")).toBeNull());
    await user.click(screen.getByRole("link", { name: "go elsewhere" }));
    await user.click(await screen.findByRole("button", { name: "go back" }));
    expect(await screen.findByRole("table")).toBeInTheDocument();
    expect(get).toHaveBeenCalledTimes(3);
    expect(screen.queryByText("Maktaba-iOS/1.4")).toBeNull();
  });

  it("ASE-18: Arabic / RTL", async () => {
    localStorage.setItem("mkt:locale", "ar");
    open();
    const table = await screen.findByRole("table", { name: ar["sessions.title"] });
    expect(document.documentElement).toHaveAttribute("dir", "rtl");
    const headers = within(table)
      .getAllByRole("columnheader")
      .map((h) => h.textContent);
    expect(headers).toEqual([
      ar["sessions.col.device"],
      ar["sessions.col.ip"],
      ar["sessions.col.created"],
      ar["sessions.col.lastSeen"],
      ar["common.actions"],
    ]);
    expect(screen.getByText(ar["sessions.current"])).toBeInTheDocument();
    expect(screen.getByText(ar["sessions.thisDevice"])).toBeInTheDocument();
  });

  // Minor a11y bugs in Sessions.tsx: every "Revoke" has the same name, and
  // focus falls to <body> when the revoked row disappears.
  it.todo("ASE-21: Revoke buttons name their session; focus moves sensibly after a revoke");
});
