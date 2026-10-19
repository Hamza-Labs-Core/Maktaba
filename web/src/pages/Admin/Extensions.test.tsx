import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { get, post, put } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post, put } };
});

import { AdminExtensions } from "./Extensions";
import { renderAt, routeGet } from "../../test/queueHarness";
import { extensionsFixture } from "../../test/fixtures/queues";

function setup() {
  routeGet(get, { "/api/extensions": { extensions: extensionsFixture } });
  return renderAt(<AdminExtensions />, "/admin/extensions");
}

describe("AdminExtensions", () => {
  beforeEach(() => {
    get.mockReset();
    post.mockReset();
    put.mockReset();
  });

  it("lists extensions with version, status and a form generated from the schema", async () => {
    setup();
    const plex = (await screen.findByRole("heading", { name: "Plex" })).closest("section")!;
    expect(within(plex).getByText("version 1.2.0")).toBeInTheDocument();
    expect(within(plex).getByText("Configured")).toBeInTheDocument();
    expect(within(plex).getByLabelText(/Server address/)).toHaveValue("http://192.168.1.10:32400");
    const dz = screen.getByRole("heading", { name: "dizqueTV" }).closest("section")!;
    expect(within(dz).getByText("Not configured")).toBeInTheDocument();
    expect(within(dz).getByRole("switch", { name: "Enabled" })).not.toBeChecked();
    expect(within(dz).getByRole("checkbox", { name: "Also mirror channels" })).toBeInTheDocument();
  });

  it("shows secrets masked and write-only", async () => {
    setup();
    const plex = (await screen.findByRole("heading", { name: "Plex" })).closest("section")!;
    const token = within(plex).getByLabelText(/Token/) as HTMLInputElement;
    expect(token.type).toBe("password");
    expect(token.value).toBe("");
    expect(token.placeholder).toContain("••••••••");
    // saving without typing a secret does not send it
    put.mockResolvedValue(extensionsFixture[0]);
    await userEvent.click(within(plex).getByRole("button", { name: "Save settings" }));
    expect(put).toHaveBeenCalledWith("/api/extensions/plex", {
      settings: { url: "http://192.168.1.10:32400", path_map: "/data=Z:/" },
    });
    // typing one sends it
    await userEvent.type(token, "new-token");
    await userEvent.click(within(plex).getByRole("button", { name: "Save settings" }));
    expect(put).toHaveBeenLastCalledWith("/api/extensions/plex", {
      settings: { url: "http://192.168.1.10:32400", path_map: "/data=Z:/", token: "new-token" },
    });
    expect(await within(plex).findAllByText("Settings saved.")).not.toHaveLength(0);
  });

  it("toggles an extension on", async () => {
    setup();
    const dz = (await screen.findByRole("heading", { name: "dizqueTV" })).closest("section")!;
    put.mockResolvedValue({ ...extensionsFixture[1], enabled: true });
    await userEvent.click(within(dz).getByRole("switch", { name: "Enabled" }));
    expect(put).toHaveBeenCalledWith("/api/extensions/dizquetv", { enabled: true });
  });

  it("tests the connection and reports the result", async () => {
    setup();
    const plex = (await screen.findByRole("heading", { name: "Plex" })).closest("section")!;
    post.mockResolvedValue({ ok: false, message: "401 from Plex" });
    await userEvent.click(within(plex).getByRole("button", { name: "Test connection" }));
    expect(post).toHaveBeenCalledWith("/api/extensions/plex/test");
    expect(await within(plex).findByText(/Connection failed: 401 from Plex/)).toBeInTheDocument();
  });

  it("lists each check of a Plex or dizqueTV test inline", async () => {
    setup();
    const plex = (await screen.findByRole("heading", { name: "Plex" })).closest("section")!;
    post.mockResolvedValue({
      ok: false,
      message: "not ready: token",
      checked_at: "2026-10-19T10:00:00Z",
      lines: [
        { key: "reachable", ok: true, detail: "nas:32400 answered in 12 ms", latency_ms: 12 },
        {
          key: "token",
          ok: false,
          detail: "token rejected — Sign in with Plex again",
          code: "token_rejected",
        },
        { key: "livetv", ok: false, optional: true, skipped: true, detail: "not checked" },
      ],
    });
    await userEvent.click(within(plex).getByRole("button", { name: "Test connection" }));
    const list = await within(plex).findByRole("list", { name: "Connection checks" });
    const items = within(list).getAllByRole("listitem");
    expect(items).toHaveLength(3);
    expect(items[0]).toHaveTextContent("Server reachable");
    expect(items[0]).toHaveTextContent("nas:32400 answered in 12 ms");
    expect(items[1]).toHaveTextContent("Plex token");
    expect(items[1]).toHaveTextContent("token rejected — Sign in with Plex again");
    expect(items[1]).toHaveTextContent("token_rejected");
    expect(items[2]).toHaveTextContent("Live TV");
    expect(items[2]).toHaveTextContent("not checked");

    const dz = screen.getByRole("heading", { name: "dizqueTV" }).closest("section")!;
    post.mockResolvedValue({
      ok: true,
      message: "dizqueTV is ready",
      checked_at: "2026-10-19T10:00:00Z",
      lines: [{ key: "channels", ok: true, detail: "4 channels" }],
    });
    await userEvent.click(within(dz).getByRole("button", { name: "Test connection" }));
    expect(post).toHaveBeenLastCalledWith("/api/extensions/dizquetv/test");
    const dzList = await within(dz).findByRole("list", { name: "Connection checks" });
    expect(within(dzList).getByRole("listitem")).toHaveTextContent("Channels");
  });
});

describe("Sign in with Plex", () => {
  const plexSaved = { ...extensionsFixture[0], settings: { url: "https://lan.plex.direct:32400" } };
  let popup: { location: { href: string }; close: ReturnType<typeof vi.fn> };

  beforeEach(() => {
    get.mockReset();
    post.mockReset();
    put.mockReset();
    vi.useFakeTimers({ shouldAdvanceTime: true });
    popup = { location: { href: "" }, close: vi.fn() };
    vi.spyOn(window, "open").mockReturnValue(popup as unknown as Window);
    post.mockImplementation((url: string) => {
      if (url === "/api/extensions/plex/signin")
        return Promise.resolve({ id: "42", code: "ABCD", auth_url: "https://app.plex.tv/auth#?x" });
      if (url === "/api/extensions/plex/signin/42/use") return Promise.resolve(plexSaved);
      return Promise.reject(new Error(`unmocked POST ${url}`));
    });
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  const user = () => userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
  const tick = (ms = 2000) => act(() => vi.advanceTimersByTimeAsync(ms));

  async function startSignin(status: (u: string) => unknown) {
    routeGet(get, {
      "/api/extensions": { extensions: extensionsFixture },
      "/api/extensions/plex/signin/42": status,
    });
    const view = renderAt(<AdminExtensions />, "/admin/extensions");
    const plex = (await screen.findByRole("heading", { name: "Plex" })).closest("section")!;
    await user().click(within(plex).getByRole("button", { name: "Sign in with Plex" }));
    return { plex, view };
  }

  it("opens plex.tv, polls, and auto-picks the only server", async () => {
    let n = 0;
    const { plex } = await startSignin(() =>
      ++n < 2
        ? { state: "waiting" }
        : { state: "done", servers: [{ name: "Home", uri: "https://lan.plex.direct:32400" }] }
    );
    expect(post).toHaveBeenCalledWith("/api/extensions/plex/signin");
    expect(popup.location.href).toBe("https://app.plex.tv/auth#?x");
    expect(within(plex).getByText(/Waiting for you to sign in/)).toBeInTheDocument();
    await tick();
    expect(get).toHaveBeenCalledWith("/api/extensions/plex/signin/42");
    await tick();
    expect(post).toHaveBeenCalledWith("/api/extensions/plex/signin/42/use", { name: "Home" });
    expect(await within(plex).findByText("Connected to Plex server Home.")).toBeInTheDocument();
    expect(within(plex).getByLabelText(/Server address/)).toHaveValue(
      "https://lan.plex.direct:32400"
    );
  });

  it("lets the user choose when the account has several servers", async () => {
    const { plex } = await startSignin(() => ({
      state: "done",
      servers: [
        { name: "Home", uri: "https://lan.plex.direct:32400" },
        { name: "Cabin", uri: "http://cabin:32400" },
      ],
    }));
    await tick();
    const cabin = await within(plex).findByRole("radio", { name: /Cabin/ });
    await user().click(cabin);
    await user().click(within(plex).getByRole("button", { name: "Use this server" }));
    expect(post).toHaveBeenCalledWith("/api/extensions/plex/signin/42/use", { name: "Cabin" });
    expect(await within(plex).findByText("Connected to Plex server Cabin.")).toBeInTheDocument();
  });

  it("gives up after 15 minutes", async () => {
    const { plex } = await startSignin(() => ({ state: "waiting" }));
    await tick(15 * 60 * 1000 + 2000);
    expect(await within(plex).findByText(/Plex sign-in timed out/)).toBeInTheDocument();
    const calls = get.mock.calls.length;
    await tick(10000);
    expect(get.mock.calls.length).toBe(calls);
  });

  it("stops polling on unmount", async () => {
    const { view } = await startSignin(() => ({ state: "waiting" }));
    await tick();
    view.unmount();
    const calls = get.mock.calls.length;
    await tick(10000);
    expect(get.mock.calls.length).toBe(calls);
  });
});
