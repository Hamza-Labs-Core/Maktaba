// The app's error screens, through the real route tree (errors audit, web):
// a page that throws, a crash outside any page, an unknown address, a failed
// load (with Try again), a sign-in that ended (Sign in again), and no network.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock("./lib/api", async () => {
  const actual = await vi.importActual<typeof import("./lib/api")>("./lib/api");
  return { ...actual, api: { ...actual.api, get, post } };
});
// crash switch: a t() key throws while rendering
const { crash } = vi.hoisted(() => ({ crash: { keys: new Set<string>() } }));
vi.mock("./lib/i18n", async () => {
  const actual = await vi.importActual<typeof import("./lib/i18n")>("./lib/i18n");
  const { useCallback } = await vi.importActual<typeof import("react")>("react");
  return {
    ...actual,
    useI18n: () => {
      const i18n = actual.useI18n();
      // a stable t, as the real one is (pages reload when t changes)
      const base = i18n.t;
      const t = useCallback(
        (key: string, vars?: Record<string, string | number>) => {
          if (crash.keys.has(key)) throw new Error(`boom on ${key} at /Volumes/media`);
          return base(key, vars);
        },
        [base]
      );
      return { ...i18n, t };
    },
  };
});

import { App } from "./App";
import { ApiError } from "./lib/api";
import en from "./i18n/en.json";

const amal = { id: "u1", username: "amal", is_admin: false };
let home: () => Promise<unknown>;

function open(path: string) {
  window.history.pushState({}, "", path);
  return render(<App />);
}

const problem = (status: number, detail?: string) =>
  new ApiError({
    type: "about:blank",
    title: "internal error",
    status,
    ...(detail ? { detail } : {}),
  });

const main = () => screen.getByRole("main");

beforeEach(() => {
  crash.keys.clear();
  vi.spyOn(window, "scrollTo").mockImplementation(() => {});
  get.mockReset();
  post.mockReset();
  home = () => Promise.resolve({});
  get.mockImplementation((path: string) => {
    if (path === "/api/auth/me") return Promise.resolve({ user: amal });
    if (path === "/api/home") return home();
    if (path === "/api/cluster/self") return Promise.reject(new Error("no cluster"));
    if (path.startsWith("/api/notifications")) return Promise.resolve({ items: [], unread: 0 });
    return Promise.resolve({ items: [], libraries: [], next_cursor: null });
  });
  post.mockResolvedValue({});
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  window.history.replaceState({}, "", "/");
});

describe("App error screens", () => {
  it("a page that throws shows the page error in the shell, never the thrown message", async () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    crash.keys.add("home.title");
    open("/");
    const alert = await within(await screen.findByRole("main")).findByRole("alert");
    expect(alert).toHaveTextContent(en["common.pageError"]);
    expect(alert).not.toHaveTextContent("boom");
    expect(alert).not.toHaveTextContent("/Volumes");
    // the shell (menu, other pages) still works
    expect(screen.getAllByRole("link", { name: en["nav.home"] }).length).toBeGreaterThan(0);
  });

  it("a crash outside any page (the shell) shows the app-wide error, not a blank window", async () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    crash.keys.add("nav.skipToContent");
    open("/");
    const alert = await screen.findByTestId("page-error");
    expect(alert).toHaveTextContent(en["common.pageError"]);
    crash.keys.clear();
    await userEvent.setup().click(within(alert).getByRole("button", { name: en["common.retry"] }));
    expect(await screen.findByRole("heading", { name: en["home.title"] })).toBeInTheDocument();
  });

  it("an unknown address shows the translated not-found page with a way home", async () => {
    open("/no/such/page");
    expect(
      await screen.findByRole("heading", { level: 1, name: en["notFound.title"] })
    ).toBeInTheDocument();
    expect(screen.getByText(en["notFound.desc"])).toBeInTheDocument();
    expect(screen.getByRole("link", { name: en["error.goHome"] })).toHaveAttribute("href", "/");
  });

  it("a failed load shows the error state with Try again, which loads again", async () => {
    let fail = true;
    home = () =>
      fail
        ? Promise.reject(problem(500, 'pq: relation "home" does not exist'))
        : Promise.resolve({});
    open("/");
    const alert = await within(await screen.findByRole("main")).findByRole("alert");
    expect(alert).toHaveTextContent(en["error.kind.server"]);
    expect(alert).not.toHaveTextContent("relation");
    expect(alert).not.toHaveTextContent("internal error");
    fail = false;
    await userEvent.setup().click(within(alert).getByRole("button", { name: en["common.retry"] }));
    expect(await screen.findByText(en["home.empty.title"])).toBeInTheDocument();
    expect(within(main()).queryByRole("alert")).toBeNull();
  });

  it("a 401 on a load offers Sign in again", async () => {
    home = () => Promise.reject(problem(401));
    open("/");
    const alert = await within(await screen.findByRole("main")).findByRole("alert");
    expect(alert).toHaveTextContent(en["error.kind.signedOut"]);
    const signIn = within(alert).getByRole("link", { name: en["error.signIn"] });
    expect(signIn).toHaveAttribute("href", "/login");
  });

  it("going offline shows the offline banner; coming back hides it", async () => {
    open("/");
    await screen.findByRole("heading", { name: en["home.title"] });
    expect(screen.queryByTestId("offline-banner")).toBeNull();
    vi.stubGlobal("navigator", { ...navigator, onLine: false });
    act(() => {
      window.dispatchEvent(new Event("offline"));
    });
    // the banner's state is set by the event's listener; on a loaded runner its render can
    // land a moment after act returns, so wait for it rather than look once
    expect(await screen.findByTestId("offline-banner")).toHaveTextContent(
      en["error.offlineBanner"]
    );
    vi.stubGlobal("navigator", { ...navigator, onLine: true });
    act(() => {
      window.dispatchEvent(new Event("online"));
    });
    await waitFor(() => expect(screen.queryByTestId("offline-banner")).toBeNull());
  });
});
