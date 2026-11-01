// /link-home (Epic 42.7): the cloud's half of linking a home account. Signed in, the
// person confirms; the link assertion goes back to the home in the fragment only.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { setAccessToken } from "../api";
import { renderAt, session, signedOut, stubFetch, user } from "../test/harness";
import { linkReturn } from "./LinkHome";

const { navigateExternal } = vi.hoisted(() => ({ navigateExternal: vi.fn() }));
vi.mock("../nav", () => ({ navigateExternal }));

const SERVER = "0a1b2c3d-0000-4000-8000-000000000000";
const NONCE = "aGVsbG8tdGhpcy1pcy1hLW5vbmNl";
const RETURN = "http://192.168.1.5:8088/account";
const path = (ret = RETURN) =>
  `/link-home?server=${SERVER}&nonce=${NONCE}&return=${encodeURIComponent(ret)}`;

const signedIn = session(user());

beforeEach(() => {
  setAccessToken(null);
  navigateExternal.mockReset();
});
afterEach(() => vi.unstubAllGlobals());

describe("linking a home account", () => {
  it("asks, then goes back to the home with the link assertion in the fragment", async () => {
    const f = stubFetch({
      "POST /v1/auth/refresh": signedIn,
      [`POST /v1/homes/${SERVER}/assertion`]: [200, { assertion: "mkha1.p.s", server_id: SERVER }],
    });
    renderAt(path());
    expect(await screen.findByRole("heading", { name: "Link a home account" })).toBeInTheDocument();
    expect(screen.getByText(/The home at 192\.168\.1\.5:8088 asks to link/)).toBeInTheDocument();
    // nothing is asked of the cloud before the person confirms
    expect(f.find("POST", `/v1/homes/${SERVER}/assertion`)).toHaveLength(0);
    await userEvent.setup().click(screen.getByRole("button", { name: "Link" }));
    await waitFor(() => expect(navigateExternal).toHaveBeenCalledTimes(1));
    const req = f.find("POST", `/v1/homes/${SERVER}/assertion`)[0];
    expect(req.body).toEqual({ purpose: "link", nonce: NONCE });
    expect(req.headers.Authorization).toBe("Bearer tok");
    expect(navigateExternal).toHaveBeenCalledWith(`${RETURN}#cloud-link=mkha1.p.s`);
  });

  it("goes back without one on Cancel", async () => {
    stubFetch({ "POST /v1/auth/refresh": signedIn });
    renderAt(path());
    await userEvent.setup().click(await screen.findByRole("button", { name: "Cancel" }));
    expect(navigateExternal).toHaveBeenCalledWith(`${RETURN}#cloud-link=cancelled`);
  });

  it("signs in first", async () => {
    stubFetch({
      "POST /v1/auth/refresh": signedOut,
      "GET /v1/auth/providers": [200, { google: false, apple: false }],
    });
    renderAt(path());
    expect(await screen.findByRole("button", { name: "Sign in" })).toBeInTheDocument();
  });

  it("says why when the cloud refuses, and stays", async () => {
    stubFetch({
      "POST /v1/auth/refresh": signedIn,
      [`POST /v1/homes/${SERVER}/assertion`]: [
        404,
        { error: "not_found", message: "no such home" },
      ],
    });
    renderAt(path());
    await userEvent.setup().click(await screen.findByRole("button", { name: "Link" }));
    expect(await screen.findByRole("alert")).toBeInTheDocument();
    expect(navigateExternal).not.toHaveBeenCalled();
  });

  it("refuses an incomplete or unsafe link", async () => {
    stubFetch({ "POST /v1/auth/refresh": signedIn });
    renderAt(path("javascript:alert(1)"));
    expect(await screen.findByText(/This link is incomplete/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Link" })).not.toBeInTheDocument();
  });

  it("reads only an http(s) return without credentials", () => {
    expect(linkReturn("https://kitchen.relay.maktaba.tv/maktaba/account#x")?.toString()).toBe(
      "https://kitchen.relay.maktaba.tv/maktaba/account"
    );
    expect(linkReturn("javascript:alert(1)")).toBeNull();
    expect(linkReturn("/account")).toBeNull();
    expect(linkReturn("https://u:p@evil.example/")).toBeNull();
    expect(linkReturn(null)).toBeNull();
  });
});
