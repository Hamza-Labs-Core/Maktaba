// Epic 42.5: the editions (VITE_MAKTABA_EDITION, lib/edition.ts), each with the real
// App route tree and shell. The edition is read when the modules load, so each test
// stubs the variable, resets the module registry and imports App afresh.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, within } from "@testing-library/react";
import type { Edition } from "./lib/edition";
import en from "./i18n/en.json";

const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock("./lib/api", async () => {
  const actual = await vi.importActual<typeof import("./lib/api")>("./lib/api");
  return { ...actual, api: { ...actual.api, get, post } };
});

const admin = { id: "u1", username: "root", is_admin: true };
const viewer = { id: "u2", username: "amal", is_admin: false };

// The server pages' nav entries, in the sidebar and (admins) the Manage section
const SERVER_NAV = [
  en["nav.queue"],
  en["nav.transcription"],
  en["nav.settings"],
  en["nav.admin.users"],
  en["nav.admin.health"],
  en["nav.billing"],
  en["nav.cloudStatus"],
];

async function open(edition: Edition | undefined, path: string, user: typeof admin) {
  vi.resetModules();
  if (edition) vi.stubEnv("VITE_MAKTABA_EDITION", edition);
  get.mockImplementation((p: string) => {
    if (p === "/api/auth/me") return Promise.resolve({ user });
    if (p === "/api/cluster/self") return Promise.reject(new Error("no cluster"));
    if (p.startsWith("/api/notifications")) return Promise.resolve({ items: [], unread: 0 });
    return Promise.resolve({ items: [], libraries: [], users: [], next_cursor: null });
  });
  const { App } = await import("./App");
  window.history.pushState({}, "", path);
  return render(<App />);
}

async function sidebar() {
  const [side] = await screen.findAllByRole("navigation", { name: en["nav.primary"] });
  await within(side).findByRole("link", { name: en["nav.account"] });
  return side;
}

beforeEach(() => {
  vi.spyOn(window, "scrollTo").mockImplementation(() => {});
  get.mockReset();
  post.mockReset();
  post.mockResolvedValue({});
});

afterEach(() => {
  vi.unstubAllEnvs();
  vi.restoreAllMocks();
  window.history.replaceState({}, "", "/");
});

describe("full edition (the default the server ships)", () => {
  it("is the default: Home at /, the server pages in one Manage section for admins", async () => {
    await open(undefined, "/", admin);
    const side = await sidebar();
    expect(within(side).getAllByRole("link", { name: en["nav.home"] })[0]).toHaveAttribute(
      "href",
      "/"
    );
    expect(within(side).getByText(en["nav.admin"])).toHaveTextContent("Manage");
    for (const name of SERVER_NAV) {
      expect(within(side).getByRole("link", { name })).toBeInTheDocument();
    }
  });

  it("loads a server page on demand", async () => {
    await open("full", "/admin/users", admin);
    expect(
      await screen.findByRole("heading", { level: 1, name: en["admin.users.title"] })
    ).toBeVisible();
  });

  it("hides the Manage section from a viewer", async () => {
    await open("full", "/", viewer);
    const side = await sidebar();
    expect(within(side).queryByText(en["nav.admin"])).not.toBeInTheDocument();
    expect(within(side).queryByRole("link", { name: en["nav.queue"] })).not.toBeInTheDocument();
  });
});

describe("player edition", () => {
  it("shows an admin only the viewer items", async () => {
    await open("player", "/", admin);
    const side = await sidebar();
    for (const key of ["nav.home", "nav.libraries", "nav.guide", "nav.search", "nav.help"]) {
      expect(within(side).getByRole("link", { name: en[key as "nav.home"] })).toBeVisible();
    }
    expect(within(side).queryByText(en["nav.admin"])).not.toBeInTheDocument();
    for (const name of SERVER_NAV) {
      expect(within(side).queryByRole("link", { name })).not.toBeInTheDocument();
    }
  });

  it.each(["/queue", "/transcription", "/settings/privacy", "/admin/users", "/billing"])(
    "has no route %s, even for an admin",
    async (path) => {
      await open("player", path, admin);
      expect(
        await screen.findByRole("heading", { level: 1, name: en["notFound.title"] })
      ).toBeVisible();
    }
  );

  it("keeps the viewer routes", async () => {
    await open("player", "/help", viewer);
    expect(await screen.findByRole("heading", { level: 1, name: en["help.title"] })).toBeVisible();
  });

  // The player has no Settings: a viewer's own choices are Preferences, an account page
  it("has Preferences in the nav, with the viewer's appearance, privacy and account", async () => {
    await open("player", "/", viewer);
    const side = await sidebar();
    expect(within(side).getByRole("link", { name: en["nav.preferences"] })).toHaveAttribute(
      "href",
      "/account/preferences"
    );
  });

  it.each([
    ["/account/preferences", en["settings.theme"]],
    ["/account/preferences/privacy", en["settings.privacy.title"]],
    ["/account/preferences/account", en["settings.signOutEverywhere"]],
  ])("opens %s", async (path, text) => {
    await open("player", path, admin);
    expect(
      await screen.findByRole("heading", { level: 1, name: en["nav.preferences"] })
    ).toBeVisible();
    expect((await screen.findAllByText(text))[0]).toBeVisible();
    // an admin's server parts stay out of the player: the home's name, the server's usage data
    expect(screen.queryByText(en["settings.homeName.title"])).not.toBeInTheDocument();
    expect(screen.queryByText(en["telemetry.server.title"])).not.toBeInTheDocument();
  });
});

describe("remote edition", () => {
  it("opens on the home's status, with the server pages and watching in the nav", async () => {
    await open("remote", "/", admin);
    expect(
      await screen.findByRole("heading", { level: 1, name: en["remote.title"] })
    ).toBeVisible();
    const side = await sidebar();
    expect(within(side).getByRole("link", { name: en["nav.status"] })).toHaveAttribute(
      "aria-current",
      "page"
    );
    expect(within(side).getByRole("link", { name: en["nav.queue"] })).toBeVisible();
    expect(within(side).getByRole("link", { name: en["nav.libraries"] })).toBeVisible();
    expect(screen.getByRole("link", { name: en["remote.attention"] })).toHaveAttribute(
      "href",
      "/queue/failures"
    );
    expect(get).toHaveBeenCalledWith("/api/admin/system/status");
  });

  it("tells a viewer it is for the home's admins, with the way to watch", async () => {
    await open("remote", "/", viewer);
    expect(await screen.findByText(en["remote.notAdmin.title"])).toBeVisible();
    expect(screen.getByRole("link", { name: en["remote.notAdmin.watch"] })).toHaveAttribute(
      "href",
      "/library"
    );
    expect(get).not.toHaveBeenCalledWith("/api/admin/system/status");
  });

  it("has the server routes", async () => {
    await open("remote", "/admin/users", admin);
    expect(
      await screen.findByRole("heading", { level: 1, name: en["admin.users.title"] })
    ).toBeVisible();
  });
});

describe("Preferences in the full edition", () => {
  // In full, Settings is under Manage: a viewer reaches their own choices here instead
  it("is in a viewer's nav, and Settings' Account tab still adds the admin's home name", async () => {
    await open("full", "/", viewer);
    const side = await sidebar();
    expect(within(side).getByRole("link", { name: en["nav.preferences"] })).toBeVisible();
  });

  it("shows an admin the home's name in Settings, not in Preferences", async () => {
    await open("full", "/settings", admin);
    expect(await screen.findByText(en["settings.homeName.title"])).toBeVisible();
  });
});
