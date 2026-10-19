import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Route } from "react-router-dom";

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
import { AdminExtensions } from "./Extensions";
import { renderPage, memberUser, quietCrash, adminUser } from "../../test/pageHarness";
import { routeGet } from "../../test/queueHarness";
import { extensionsFixture } from "../../test/fixtures/queues";
import { axeViolations } from "../../test/axe";
import type { Extension } from "../../lib/queueApi";
import type { User } from "../../lib/auth";

const [plexExt, dzExt] = extensionsFixture;

function open(
  table: Record<string, unknown> = { "/api/extensions": { extensions: extensionsFixture } },
  opts: { user?: User | null; path?: string; history?: string[] } = {}
) {
  routeGet(get, table);
  return renderPage(<AdminExtensions />, {
    path: opts.path ?? "/admin/extensions",
    route: "/admin/extensions",
    user: opts.user === undefined ? adminUser : opts.user,
    history: opts.history,
    extraRoutes: <Route path="/queue" element={<p>queue page</p>} />,
  });
}
async function card(name: string) {
  return (await screen.findByRole("heading", { name })).closest("section")!;
}
function problem(status: number, title: string, detail?: string) {
  return new ApiError({ type: "about:blank", title, status, detail });
}

describe("AdminExtensions (test-cases/admin-extensions.md)", () => {
  beforeEach(() => {
    for (const f of [get, post, put, patch, del]) f.mockReset();
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("EXT-04: load failure shows the error banner, not Loading", async () => {
    get.mockRejectedValue(problem(500, "internal"));
    renderPage(<AdminExtensions />, { path: "/admin/extensions" });
    expect(await screen.findByRole("alert")).toHaveTextContent("Could not load the extensions.");
    expect(screen.queryByText("Loading…")).toBeNull();
  });

  it("EXT-05: no extensions installed", async () => {
    open({ "/api/extensions": { extensions: [] } });
    expect(await screen.findByText("No extensions are installed.")).toBeInTheDocument();
    expect(screen.queryByRole("heading", { level: 2 })).toBeNull();
  });

  it.each([
    ["extensions: null", { extensions: null }],
    ["extensions missing", {}],
  ])("EXT-06: %s shows No extensions are installed", async (_n, body) => {
    open({ "/api/extensions": body });
    expect(await screen.findByText("No extensions are installed.")).toBeInTheDocument();
    expect(screen.queryByText("Loading…")).toBeNull();
  });

  it.each([
    ["settings_schema: null", { ...plexExt, settings_schema: null }],
    ["settings_schema missing", { ...plexExt, settings_schema: undefined }],
  ])("EXT-06: %s renders the card with no fields", async (_n, ext) => {
    quietCrash();
    open({ "/api/extensions": { extensions: [ext] } });
    const plex = await card("Plex");
    expect(within(plex).queryByRole("textbox")).toBeNull();
    expect(within(plex).getByRole("button", { name: "Save settings" })).toBeInTheDocument();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it.each([
    ["secrets_set: null", { ...plexExt, secrets_set: null }],
    ["secrets_set missing", { ...plexExt, secrets_set: undefined }],
  ])("EXT-06: %s shows the secret as Not set", async (_n, ext) => {
    quietCrash();
    open({ "/api/extensions": { extensions: [ext] } });
    const plex = await card("Plex");
    expect(within(plex).getByLabelText(/Token/)).toHaveAttribute("placeholder", "Not set");
  });

  it("EXT-06: select options null and settings null render empty fields", async () => {
    const ext = {
      ...dzExt,
      settings: null,
      settings_schema: [
        { key: "mode", label: "Mode", type: "select", options: null },
        { key: "url", label: "Server address", type: "url" },
      ],
    };
    open({ "/api/extensions": { extensions: [ext] } });
    const dz = await card("dizqueTV");
    expect(within(dz).getByLabelText("Mode").querySelectorAll("option")).toHaveLength(0);
    expect(within(dz).getByLabelText("Server address")).toHaveValue("");
  });

  it("EXT-07: toggle success turns the switch on and disables it while pending", async () => {
    let resolve!: (v: unknown) => void;
    put.mockReturnValue(new Promise((r) => (resolve = r)));
    open();
    const dz = await card("dizqueTV");
    const sw = within(dz).getByRole("switch", { name: "Enabled" });
    await userEvent.setup().click(sw);
    expect(put).toHaveBeenCalledWith("/api/extensions/dizquetv", { enabled: true });
    expect(sw).toBeDisabled();
    resolve({ ...dzExt, enabled: true });
    await waitFor(() => expect(sw).toBeChecked());
    expect(sw).toBeEnabled();
  });

  it.each([
    ["403", problem(403, "forbidden", "admin only"), "forbidden: admin only"],
    ["network", new TypeError("Failed to fetch"), "Failed to fetch"],
  ])(
    "EXT-07: toggle failure (%s) keeps the switch off and shows the error",
    async (_n, err, text) => {
      put.mockRejectedValue(err);
      open();
      const dz = await card("dizqueTV");
      const sw = within(dz).getByRole("switch", { name: "Enabled" });
      await userEvent.setup().click(sw);
      expect(await within(dz).findByRole("alert")).toHaveTextContent(text);
      expect(sw).not.toBeChecked();
      expect(within(await card("Plex")).queryByRole("alert")).toBeNull();
    }
  );

  it("EXT-08: toggling keeps unsaved form edits", async () => {
    put.mockResolvedValue({ ...plexExt, enabled: false });
    open();
    const plex = await card("Plex");
    const user = userEvent.setup();
    const pathMap = within(plex).getByLabelText(/Path map/);
    await user.clear(pathMap);
    await user.type(pathMap, "/x=Y:/");
    await user.type(within(plex).getByLabelText(/Token/), "tok");
    await user.click(within(plex).getByRole("switch", { name: "Enabled" }));
    await waitFor(() =>
      expect(within(plex).getByRole("switch", { name: "Enabled" })).not.toBeChecked()
    );
    expect(within(plex).getByLabelText(/Path map/)).toHaveValue("/x=Y:/");
    expect(within(plex).getByLabelText(/Token/)).toHaveValue("tok");
  });

  it.each([
    ["400", problem(400, "bad request", "url: required"), "bad request: url: required"],
    ["403", problem(403, "forbidden"), "forbidden"],
    ["network", new TypeError("Failed to fetch"), "Failed to fetch"],
  ])("EXT-09: save failure (%s) shows the error and keeps edits", async (_n, err, text) => {
    put.mockRejectedValue(err);
    open();
    const plex = await card("Plex");
    const user = userEvent.setup();
    const url = within(plex).getByLabelText(/Server address/);
    await user.clear(url);
    await user.type(within(plex).getByLabelText(/Path map/), "{Enter}");
    expect(put).toHaveBeenCalledWith("/api/extensions/plex", {
      settings: { url: "", path_map: "/data=Z:/" },
    });
    expect(await within(plex).findByRole("alert")).toHaveTextContent(text);
    expect(within(plex).queryByText("Settings saved.")).toBeNull();
    expect(url).toHaveValue("");
    expect(within(plex).getByRole("button", { name: "Save settings" })).toBeEnabled();
  });

  it("EXT-10: number, select, boolean, url fields, help and required", async () => {
    const demo: Extension = {
      name: "demo",
      label: "Demo",
      description: "",
      version: "1",
      enabled: true,
      configured: true,
      settings_schema: [
        { key: "port", label: "Port", type: "number", required: true },
        {
          key: "mode",
          label: "Mode",
          type: "select",
          options: [
            { value: "a", label: "A" },
            { value: "b", label: "B" },
          ],
        },
        { key: "on", label: "On", type: "boolean" },
        { key: "url", label: "URL", type: "url", help: "http://host:port" },
      ],
      settings: { port: 8080, mode: "b", on: true, url: "http://x", extra: "never" },
      secrets_set: [],
    };
    put.mockImplementation((_p: string, body: { settings: Extension["settings"] }) =>
      Promise.resolve({ ...demo, settings: body.settings })
    );
    open({ "/api/extensions": { extensions: [demo] } });
    const c = await card("Demo");
    const port = within(c).getByLabelText(/Port/);
    expect(port).toHaveAttribute("type", "number");
    expect(port).toHaveValue(8080);
    expect(within(c).getByText("(Required)")).toBeInTheDocument();
    expect(within(c).getByLabelText("Mode")).toHaveValue("b");
    expect(within(c).getByRole("checkbox", { name: "On" })).toBeChecked();
    expect(within(c).getByLabelText("URL")).toHaveAttribute("type", "url");
    expect(within(c).getByText("http://host:port")).toBeInTheDocument();

    const user = userEvent.setup();
    await user.clear(port);
    await user.type(port, "9090");
    await user.selectOptions(within(c).getByLabelText("Mode"), "a");
    await user.click(within(c).getByRole("checkbox", { name: "On" }));
    await user.click(within(c).getByRole("button", { name: "Save settings" }));
    expect(put).toHaveBeenLastCalledWith("/api/extensions/demo", {
      settings: { port: 9090, mode: "a", on: false, url: "http://x" },
    });

    await user.clear(port);
    await user.click(within(c).getByRole("button", { name: "Save settings" }));
    expect(put).toHaveBeenLastCalledWith("/api/extensions/demo", {
      settings: { port: null, mode: "a", on: false, url: "http://x" },
    });
  });

  it.each([
    ["ok", { ok: true, message: "Plex 1.40" }, "Connection works: Plex 1.40"],
    ["failed", { ok: false, message: "401 from Plex" }, "Connection failed: 401 from Plex"],
  ])("EXT-11: test connection %s", async (_n, res, text) => {
    let resolve!: (v: unknown) => void;
    post.mockReturnValue(new Promise((r) => (resolve = r)));
    open();
    const plex = await card("Plex");
    const btn = within(plex).getByRole("button", { name: "Test connection" });
    await userEvent.setup().click(btn);
    expect(btn).toHaveAttribute("aria-busy");
    resolve(res);
    expect(await within(plex).findByText(text)).toBeInTheDocument();
    expect(within(plex).getByText(text).closest("[role=status]")).not.toBeNull();
  });

  it("EXT-11: test request error shows an alert, no status line", async () => {
    post.mockRejectedValue(problem(500, "internal", "boom"));
    open();
    const plex = await card("Plex");
    await userEvent.setup().click(within(plex).getByRole("button", { name: "Test connection" }));
    expect(await within(plex).findByRole("alert")).toHaveTextContent("internal: boom");
    expect(within(plex).queryByText(/Connection (works|failed)/)).toBeNull();
  });

  it.each([
    ["with lines", [{ key: "token", ok: true, detail: "valid" }], 1],
    ["lines: null", null, 0],
  ])("EXT-11: test result %s", async (_n, lines, count) => {
    post.mockResolvedValue({ ok: true, message: "ok", lines });
    open();
    const plex = await card("Plex");
    await userEvent.setup().click(within(plex).getByRole("button", { name: "Test connection" }));
    await within(plex).findByText("Connection works: ok");
    const list = within(plex).queryByRole("list", { name: "Connection checks" });
    if (count === 0) expect(list).toBeNull();
    else expect(within(list!).getAllByRole("listitem")).toHaveLength(count);
  });

  describe("EXT-13: Plex sign-in error paths", () => {
    let popup: { location: { href: string }; close: ReturnType<typeof vi.fn> };
    const tick = (ms = 2000) => act(() => vi.advanceTimersByTimeAsync(ms));
    const user = () => userEvent.setup({ advanceTimers: vi.advanceTimersByTime });

    beforeEach(() => {
      vi.useFakeTimers({ shouldAdvanceTime: true });
      popup = { location: { href: "" }, close: vi.fn() };
      vi.spyOn(window, "open").mockReturnValue(popup as unknown as Window);
      post.mockImplementation((url: string) =>
        url === "/api/extensions/plex/signin"
          ? Promise.resolve({ id: "42", code: "C", auth_url: "https://app.plex.tv/auth#?x" })
          : Promise.reject(new Error(`unmocked POST ${url}`))
      );
    });

    async function start(status: (u: string) => unknown) {
      open({
        "/api/extensions": { extensions: extensionsFixture },
        "/api/extensions/plex/signin/42": status,
      });
      const plex = await card("Plex");
      await user().click(within(plex).getByRole("button", { name: "Sign in with Plex" }));
      return plex;
    }

    it("(a) popup blocked: waiting text with the app.plex.tv link, polling continues", async () => {
      vi.mocked(window.open).mockReturnValue(null);
      const plex = await start(() => ({ state: "waiting" }));
      expect(within(plex).getByText(/If no window opened/)).toBeInTheDocument();
      expect(within(plex).getByRole("link", { name: "app.plex.tv" })).toHaveAttribute(
        "href",
        "https://app.plex.tv/auth#?x"
      );
      await tick();
      expect(get).toHaveBeenCalledWith("/api/extensions/plex/signin/42");
    });

    it("(b) sign-in start fails: popup closed, error shown", async () => {
      post.mockRejectedValue(problem(502, "bad gateway", "plex.tv unreachable"));
      const plex = await start(() => ({ state: "waiting" }));
      expect(popup.close).toHaveBeenCalled();
      expect(await within(plex).findByText("bad gateway: plex.tv unreachable")).toBeInTheDocument();
    });

    it("(c) poll error stops polling and shows the error", async () => {
      let n = 0;
      const plex = await start(() => {
        if (++n === 2) throw problem(500, "internal");
        return { state: "waiting" };
      });
      await tick();
      await tick();
      expect(await within(plex).findByText("internal")).toBeInTheDocument();
      const calls = get.mock.calls.length;
      await tick(10000);
      expect(get.mock.calls.length).toBe(calls);
      expect(within(plex).getByRole("button", { name: "Sign in with Plex" })).toBeEnabled();
    });

    it.each([
      ["[]", { state: "done", servers: [] }],
      ["null", { state: "done", servers: null }],
      ["missing", { state: "done" }],
    ])("(d) servers %s: no reachable servers", async (_n, res) => {
      const plex = await start(() => res);
      await tick();
      expect(
        await within(plex).findByText("Signed in, but this Plex account has no reachable servers.")
      ).toBeInTheDocument();
    });

    it("(e) use fails: back to the picker with the error", async () => {
      post.mockImplementation((url: string) =>
        url === "/api/extensions/plex/signin"
          ? Promise.resolve({ id: "42", code: "C", auth_url: "https://app.plex.tv/auth#?x" })
          : Promise.reject(problem(409, "conflict", "server gone"))
      );
      const plex = await start(() => ({
        state: "done",
        servers: [{ name: "Home", uri: "https://h:32400" }],
      }));
      await tick();
      expect(await within(plex).findByText("conflict: server gone")).toBeInTheDocument();
      expect(
        within(plex).getByRole("group", { name: "Choose the Plex server to use" })
      ).toBeVisible();
    });

    it("(f) a second click: only the latest sign-in keeps polling", async () => {
      let id = 0;
      post.mockImplementation(() => {
        id += 1;
        return Promise.resolve({ id: String(id), code: "C", auth_url: "https://app.plex.tv/a" });
      });
      routeGet(get, {
        "/api/extensions": { extensions: extensionsFixture },
        "/api/extensions/plex/signin/1": { state: "waiting" },
        "/api/extensions/plex/signin/2": { state: "waiting" },
      });
      renderPage(<AdminExtensions />, { path: "/admin/extensions" });
      const plex = await card("Plex");
      const btn = within(plex).getByRole("button", { name: "Sign in with Plex" });
      await user().click(btn);
      await user().click(btn);
      await tick();
      await tick();
      const polled = get.mock.calls.map((c) => c[0] as string).filter((u) => u.includes("signin/"));
      expect(polled.length).toBeGreaterThan(0);
      expect(polled.every((u) => u.endsWith("/signin/2"))).toBe(true);
    });
  });

  it.each([
    ["lines: null", { lines: null }],
    ["lines missing", { lines: undefined }],
  ])(
    "EXT-14: Cloudflare last test with %s renders, and remove handles null lists",
    async (_n, over) => {
      quietCrash();
      const cf: Extension = {
        name: "cloudflare_stream",
        label: "Cloudflare Stream",
        description: "",
        version: "1",
        enabled: false,
        configured: true,
        settings_schema: [],
        settings: {},
        secrets_set: [],
      };
      const integration = {
        configured: false,
        account_id: "acc1",
        token_set: true,
        token_problem: null,
        signing_key_set: false,
        customer_domain: null,
        enabled: false,
        can_enable: false,
        last_test: { ok: true, message: "ok", checked_at: "2026-10-19T12:00:00Z", ...over },
      };
      open({ "/api/extensions": { extensions: [cf] }, "/api/tv/remote": { integration } });
      const c = await card("Cloudflare Stream");
      expect(await within(c).findByRole("group")).toBeInTheDocument();
      expect(screen.queryByRole("alert")).toBeNull();

      // Test connection on this card with lines: null
      post.mockResolvedValue({ ok: true, message: "ok", lines: null });
      const user = userEvent.setup();
      await user.click(within(c).getByRole("button", { name: "Test connection" }));
      await within(c).findByText("Connection works: ok");

      vi.spyOn(window, "confirm").mockReturnValue(true);
      del.mockResolvedValueOnce({ deleted_inputs: null, errors: null }).mockResolvedValueOnce({});
      await user.click(within(c).getByRole("button", { name: "Remove integration" }));
      expect(
        await within(c).findByText("Integration removed (0 live inputs deleted at Cloudflare).")
      ).toBeInTheDocument();
      expect(screen.queryByRole("alert")).toBeNull();
    }
  );

  it("EXT-15: non-admin sees Admins only, no forms", async () => {
    open(undefined, { user: memberUser });
    expect(screen.getByText("Admins only")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Save settings" })).toBeNull();
    expect(get).not.toHaveBeenCalled();
  });

  it("EXT-16: signed-out user is redirected to /login", () => {
    open(undefined, { user: null, path: "/admin/extensions#plex" });
    expect(screen.getByText("login page (from /admin/extensions)")).toBeInTheDocument();
    expect(get).not.toHaveBeenCalled();
  });

  it("EXT-17: #dizquetv deep link scrolls to the card once it loads", async () => {
    const scrolled: string[] = [];
    const orig = Element.prototype.scrollIntoView;
    Element.prototype.scrollIntoView = function (this: Element) {
      scrolled.push(this.id);
    };
    try {
      open(undefined, { path: "/admin/extensions#dizquetv" });
      await card("dizqueTV");
      await waitFor(() => expect(scrolled).toEqual(["dizquetv"]));
    } finally {
      Element.prototype.scrollIntoView = orig;
    }
  });

  it("EXT-17: an unknown hash renders the page normally", async () => {
    open(undefined, { path: "/admin/extensions#unknown" });
    expect(await card("Plex")).toBeInTheDocument();
  });

  it("EXT-18: Back to the queues link, then Back refetches the cards", async () => {
    open(undefined, { history: ["/queue"] });
    await card("Plex");
    const user = userEvent.setup();
    await user.click(screen.getByRole("link", { name: "Back to the queues" }));
    expect(screen.getByText("queue page")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "go back" }));
    expect(await card("Plex")).toBeInTheDocument();
    expect(get).toHaveBeenCalledTimes(2);
    await user.click(screen.getByRole("button", { name: "go back" }));
    expect(screen.getByText("queue page")).toBeInTheDocument();
  });

  it("EXT-19: a page crash shows the route error fallback and Back works", async () => {
    quietCrash();
    let exts: unknown[] = [{ ...plexExt, label: { bad: 1 } }];
    get.mockImplementation(() => Promise.resolve({ extensions: exts }));
    renderPage(<AdminExtensions />, { path: "/admin/extensions" });
    expect(await screen.findByRole("alert")).toBeInTheDocument();
    const user = userEvent.setup();
    await user.click(screen.getByRole("link", { name: "go elsewhere" }));
    expect(screen.getByText("elsewhere")).toBeInTheDocument();
    exts = extensionsFixture;
    await user.click(screen.getByRole("button", { name: "go back" }));
    expect(await card("Plex")).toBeInTheDocument();
  });

  it("EXT-23: labelled inputs, status/alert regions and no axe violations", async () => {
    put.mockRejectedValue(problem(400, "bad request"));
    post.mockResolvedValue({
      ok: false,
      message: "nope",
      lines: [{ key: "token", ok: false, detail: "x" }],
    });
    const { container } = open();
    const plex = await card("Plex");
    expect(within(plex).getByLabelText(/Server address/)).toHaveAttribute("id", "ext-plex-url");
    expect(within(plex).getByRole("switch", { name: "Enabled" })).toBeInTheDocument();
    const user = userEvent.setup();
    await user.click(within(plex).getByRole("button", { name: "Test connection" }));
    expect(await within(plex).findByRole("status")).toHaveTextContent("Connection failed: nope");
    await user.click(within(plex).getByRole("button", { name: "Save settings" }));
    expect(await within(plex).findByRole("alert")).toHaveTextContent("bad request");
    expect(await axeViolations(container)).toEqual([]);
  });
});
