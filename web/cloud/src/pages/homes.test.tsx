// /homes (Epic 42.9): a linked home's player and remote, served by the cloud on the home's
// relay address and opened signed in with a one-use assertion (in the fragment only).
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { setAccessToken } from "../api";
import { renderAt, session, stubFetch, user } from "../test/harness";
import { editionUrl, type CloudHome } from "./Homes";
import { forgetSavedHomes, loadHomes, ordered, saveHomes } from "../savedHomes";

const { navigateExternal } = vi.hoisted(() => ({ navigateExternal: vi.fn() }));
vi.mock("../nav", () => ({ navigateExternal }));

const KITCHEN: CloudHome = {
  server_id: "0a1b2c3d-0000-4000-8000-000000000000",
  name: "Kitchen",
  slug: "kitchen",
  relay_url: "https://kitchen.relay.maktaba.tv",
  online: true,
  linked: true,
  owned: true,
};
const CABIN: CloudHome = {
  ...KITCHEN,
  server_id: "s2",
  name: "Cabin",
  slug: "cabin",
  relay_url: "https://cabin.relay.maktaba.tv",
  linked: false,
  online: false,
};

beforeEach(() => {
  setAccessToken(null);
  navigateExternal.mockReset();
});
afterEach(() => vi.unstubAllGlobals());

describe("your homes", () => {
  it("opens a linked home's player, signed in", async () => {
    const f = stubFetch({
      "POST /v1/auth/refresh": session(user()),
      "GET /v1/homes": [200, { homes: [KITCHEN, CABIN] }],
      [`POST /v1/homes/${KITCHEN.server_id}/assertion`]: [200, { assertion: "mkha1.p.s" }],
    });
    renderAt("/homes");
    expect(await screen.findByRole("heading", { name: "Kitchen" })).toBeInTheDocument();
    await userEvent.setup().click(screen.getByRole("button", { name: "Watch" }));
    await waitFor(() => expect(navigateExternal).toHaveBeenCalledTimes(1));
    expect(f.find("POST", `/v1/homes/${KITCHEN.server_id}/assertion`)[0].body).toEqual({
      purpose: "signin",
    });
    expect(navigateExternal).toHaveBeenCalledWith(
      "https://kitchen.relay.maktaba.tv/play/cloud-sign-in#assertion=mkha1.p.s"
    );
  });

  it("opens the remote for Manage", async () => {
    stubFetch({
      "POST /v1/auth/refresh": session(user()),
      "GET /v1/homes": [200, { homes: [KITCHEN] }],
      [`POST /v1/homes/${KITCHEN.server_id}/assertion`]: [200, { assertion: "a2" }],
    });
    renderAt("/homes");
    await userEvent.setup().click(await screen.findByRole("button", { name: "Manage" }));
    await waitFor(() =>
      expect(navigateExternal).toHaveBeenCalledWith(
        "https://kitchen.relay.maktaba.tv/manage/cloud-sign-in#assertion=a2"
      )
    );
  });

  it("offers nothing for a home it is not linked to, only the way to link", async () => {
    const f = stubFetch({
      "POST /v1/auth/refresh": session(user()),
      "GET /v1/homes": [200, { homes: [CABIN] }],
    });
    renderAt("/homes");
    expect(await screen.findByRole("heading", { name: "Cabin" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Watch" })).not.toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Open the home" })).toHaveAttribute(
      "href",
      "https://cabin.relay.maktaba.tv/account"
    );
    expect(f.find("POST", `/v1/homes/s2/assertion`)).toHaveLength(0);
  });

  it("says why when the cloud refuses", async () => {
    stubFetch({
      "POST /v1/auth/refresh": session(user()),
      "GET /v1/homes": [200, { homes: [KITCHEN] }],
      [`POST /v1/homes/${KITCHEN.server_id}/assertion`]: [
        403,
        { error: "not_linked", message: "not linked" },
      ],
    });
    renderAt("/homes");
    await userEvent.setup().click(await screen.findByRole("button", { name: "Watch" }));
    expect(await screen.findByRole("alert")).toBeInTheDocument();
    expect(navigateExternal).not.toHaveBeenCalled();
  });

  it("builds only an http(s) address under the home's own", () => {
    expect(
      editionUrl({ ...KITCHEN, relay_url: "https://k.relay.example/base/" }, "play", "a b")
    ).toBe("https://k.relay.example/base/play/cloud-sign-in#assertion=a%20b");
    expect(() =>
      editionUrl({ ...KITCHEN, relay_url: "javascript:alert(1)" }, "play", "x")
    ).toThrow();
  });
});

// Epic 50 story 50.13: the saved list while Maktaba Cloud can't be reached
describe("your homes while the cloud is down", () => {
  afterEach(() => forgetSavedHomes());
  const WITH_DIRECT: CloudHome = {
    ...KITCHEN,
    direct_url: "http://81.2.3.4:32400",
    connections: [
      { url: "https://kitchen.relay.maktaba.tv", kind: "relay" },
      { url: "http://81.2.3.4:32400", kind: "direct" },
    ],
  };

  it("saves a good list, then shows it from its time when the API fails (T03)", async () => {
    stubFetch({
      "POST /v1/auth/refresh": session(user()),
      "GET /v1/homes": [200, { homes: [WITH_DIRECT] }],
    });
    renderAt("/homes");
    expect(await screen.findByRole("heading", { name: "Kitchen" })).toBeInTheDocument();
    expect(loadHomes("u1")?.homes[0].connections.map((c) => c.kind)).toEqual(["direct", "relay"]);
    vi.unstubAllGlobals();

    stubFetch({
      "POST /v1/auth/refresh": session(user()),
      "GET /v1/homes": [503, { error: "unavailable" }],
    });
    renderAt("/homes");
    expect(
      await screen.findByText(/Maktaba Cloud can't be reached. Showing your homes from/)
    ).toBeInTheDocument();
    expect(screen.getAllByRole("heading", { name: "Kitchen" }).length).toBeGreaterThan(0);
    const direct = screen.getByRole("link", { name: "Open directly" });
    expect(direct).toHaveAttribute("href", "http://81.2.3.4:32400");
    expect(screen.queryByText(/more than 30 days old/)).not.toBeInTheDocument();
  });

  it("a captive portal's 200 page is not the API: the saved list shows", async () => {
    saveHomes("u1", [WITH_DIRECT]);
    stubFetch({
      "POST /v1/auth/refresh": session(user()),
      "GET /v1/homes": () => new Response("<html>Hotel Wi-Fi</html>", { status: 200 }),
    });
    renderAt("/homes");
    expect(await screen.findByText(/Showing your homes from/)).toBeInTheDocument();
  });

  it("marks a list over 30 days old, and shows it still", async () => {
    saveHomes("u1", [WITH_DIRECT], new Date(Date.now() - 31 * 24 * 3600_000));
    stubFetch({
      "POST /v1/auth/refresh": session(user()),
      "GET /v1/homes": () => Promise.reject(new TypeError("Failed to fetch")),
    });
    renderAt("/homes");
    expect(await screen.findByText(/more than 30 days old/)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Open directly" })).toBeInTheDocument();
  });

  it("a refusal (4xx) is not an outage: it says why, no saved list", async () => {
    saveHomes("u1", [WITH_DIRECT]);
    stubFetch({
      "POST /v1/auth/refresh": session(user()),
      "GET /v1/homes": [422, { error: "bad_request", message: "no" }],
    });
    renderAt("/homes");
    expect(await screen.findByRole("button", { name: "Retry" })).toBeInTheDocument();
    expect(screen.queryByText(/Showing your homes from/)).not.toBeInTheDocument();
  });

  it("orders connections local, direct, relay hint, relay (T04)", () => {
    expect(
      ordered([
        { url: "r", kind: "relay" },
        { url: "h", kind: "relay_hint" },
        { url: "d", kind: "direct" },
        { url: "l", kind: "local" },
      ]).map((c) => c.kind)
    ).toEqual(["local", "direct", "relay_hint", "relay"]);
  });

  it("is deleted at sign-out", async () => {
    saveHomes("u1", [WITH_DIRECT]);
    saveHomes("u2", [WITH_DIRECT]);
    stubFetch({
      "POST /v1/auth/refresh": session(user()),
      "POST /v1/auth/logout": [204],
      "GET /v1/homes": [200, { homes: [WITH_DIRECT] }],
    });
    renderAt("/homes");
    await screen.findByRole("heading", { name: "Kitchen" });
    await userEvent.setup().click(await screen.findByRole("button", { name: "Sign out" }));
    await waitFor(() => expect(loadHomes("u1")).toBeNull());
    expect(loadHomes("u2")).toBeNull();
  });
});
