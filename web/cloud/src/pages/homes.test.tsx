// /homes (Epic 42.9): a linked home's player and remote, served by the cloud on the home's
// relay address and opened signed in with a one-use assertion (in the fragment only).
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { setAccessToken } from "../api";
import { renderAt, session, stubFetch, user } from "../test/harness";
import { editionUrl, type CloudHome } from "./Homes";

const { navigateExternal } = vi.hoisted(() => ({ navigateExternal: vi.fn() }));
vi.mock("../nav", () => ({ navigateExternal }));

const KITCHEN: CloudHome = {
  server_id: "0a1b2c3d-0000-4000-8000-000000000000",
  name: "Kitchen",
  slug: "kitchen",
  relay_url: "https://kitchen.relay.maktaba.app",
  online: true,
  linked: true,
  owned: true,
};
const CABIN: CloudHome = {
  ...KITCHEN,
  server_id: "s2",
  name: "Cabin",
  slug: "cabin",
  relay_url: "https://cabin.relay.maktaba.app",
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
      "https://kitchen.relay.maktaba.app/play/cloud-sign-in#assertion=mkha1.p.s"
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
        "https://kitchen.relay.maktaba.app/manage/cloud-sign-in#assertion=a2"
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
      "https://cabin.relay.maktaba.app/account"
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
