// Account → Linked accounts (Epic 42.7). The home enforces every rule (its role tests are
// api/internal/handlers/auth/cloud_pg_test.go); here: what the card offers whom, the trip
// to the cloud and back, and that the cloud's answer leaves the address bar.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { get, post, patch, del } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  patch: vi.fn(),
  del: vi.fn(),
}));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post, patch, delete: del } };
});

import { ApiError } from "../../lib/api";
import type { User } from "../../lib/auth";
import { adminUser, memberUser, renderPage } from "../../test/pageHarness";
import en from "../../i18n/en.json";
import ar from "../../i18n/ar.json";
import { CloudLinkCard, leave } from "./CloudLink";

const SERVER = "0a1b2c3d-0000-4000-8000-000000000000";
const LINK = {
  user_id: "u-member",
  cloud_user_id: "c-1",
  cloud_label: "sara@example.com",
  scope: "play" as const,
  created_at: "2026-10-27T10:00:00Z",
};

function open(user: User = memberUser) {
  return renderPage(<CloudLinkCard />, { path: "/account", user });
}

function state(link: typeof LINK | null, available = true) {
  return { available, server_id: SERVER, cloud_api: "https://api.maktaba.app", link };
}

let to: ReturnType<typeof vi.spyOn>;
beforeEach(() => {
  get.mockReset();
  post.mockReset();
  patch.mockReset();
  del.mockReset();
  to = vi.spyOn(leave, "to").mockImplementation(() => {});
  window.history.replaceState(null, "", "/account");
});
afterEach(() => {
  to.mockRestore();
  window.history.replaceState(null, "", "/");
});

describe("Linked accounts", () => {
  it("is not shown on a home that has none", async () => {
    get.mockRejectedValue(new ApiError({ type: "x", status: 404, title: "not linked" }));
    const { container } = open();
    await waitFor(() => expect(get).toHaveBeenCalledWith("/api/me/cloud-link"));
    expect(container.querySelector(".mkt-cloud-link")).toBeNull();
  });

  it("says when the home is not connected to Maktaba Cloud", async () => {
    get.mockResolvedValue(state(null, false));
    open();
    expect(await screen.findByText(en["cloudLink.unavailable"])).toBeVisible();
    expect(screen.queryByRole("button", { name: en["cloudLink.link"] })).not.toBeInTheDocument();
  });

  it("sends the browser to the cloud with this user's nonce and this page to come back to", async () => {
    get.mockResolvedValue(state(null));
    post.mockResolvedValue({
      nonce: "n0nce-aaaaaaaaaaaaaaaa",
      server_id: SERVER,
      cloud_api: "https://api.maktaba.app/",
    });
    open();
    await userEvent.click(await screen.findByRole("button", { name: en["cloudLink.link"] }));
    expect(post).toHaveBeenCalledWith("/api/me/cloud-link/start", {});
    await waitFor(() => expect(to).toHaveBeenCalledTimes(1));
    const u = new URL(to.mock.calls[0][0] as string);
    expect(u.origin + u.pathname).toBe(`https://api.maktaba.app/v1/homes/${SERVER}/link`);
    expect(u.searchParams.get("nonce")).toBe("n0nce-aaaaaaaaaaaaaaaa");
    expect(u.searchParams.get("return")).toBe(`${window.location.origin}/account`);
  });

  it("finishes the link the cloud confirmed, and takes the answer out of the address", async () => {
    window.history.replaceState(null, "", "/account#cloud-link=mkha1.p.s");
    get.mockResolvedValue(state(null));
    post.mockResolvedValue(LINK);
    open();
    expect(await screen.findByText("sara@example.com")).toBeVisible();
    expect(post).toHaveBeenCalledWith("/api/me/cloud-link", { assertion: "mkha1.p.s" });
    expect(post).toHaveBeenCalledTimes(1);
    expect(window.location.hash).toBe("");
    expect(screen.getByText(en["cloudLink.scope.play"])).toBeVisible();
  });

  it("does nothing more when the person cancelled at the cloud", async () => {
    window.history.replaceState(null, "", "/account#cloud-link=cancelled");
    get.mockResolvedValue(state(null));
    open();
    expect(await screen.findByText(en["cloudLink.cancelled"])).toBeVisible();
    expect(post).not.toHaveBeenCalled();
    expect(window.location.hash).toBe("");
  });

  it("says when the cloud account belongs to another user here", async () => {
    window.history.replaceState(null, "", "/account#cloud-link=mkha1.p.s");
    get.mockResolvedValue(state(null));
    post.mockRejectedValue(new ApiError({ type: "x", status: 409, title: "conflict" }));
    open();
    expect(await screen.findByText(en["cloudLink.taken"])).toBeVisible();
  });

  it("offers managing through the cloud to an admin signed in on the home, and no one else", async () => {
    get.mockResolvedValue(state({ ...LINK, user_id: "u-admin" }));
    patch.mockResolvedValue({ ...LINK, scope: "manage" });
    const admin = open(adminUser);
    await userEvent.click(await screen.findByRole("button", { name: en["cloudLink.toManage"] }));
    expect(patch).toHaveBeenCalledWith("/api/me/cloud-link", { scope: "manage" });
    expect(await screen.findByText(en["cloudLink.scope.manage"])).toBeVisible();
    admin.unmount();

    get.mockResolvedValue(state(LINK));
    const viewer = open(memberUser);
    await screen.findByText("sara@example.com");
    expect(
      screen.queryByRole("button", { name: en["cloudLink.toManage"] })
    ).not.toBeInTheDocument();
    viewer.unmount();

    // signed in through the cloud: no link or scope from here (the home refuses them: 403)
    get.mockResolvedValue(state(LINK));
    open({ ...adminUser, cloud_scope: "manage" });
    await screen.findByText("sara@example.com");
    expect(screen.queryByRole("button", { name: en["cloudLink.toPlay"] })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: en["cloudLink.unlink"] })).toBeVisible();
  });

  it("unlinks after asking", async () => {
    get.mockResolvedValue(state(LINK));
    del.mockResolvedValue(undefined);
    open();
    await userEvent.click(await screen.findByRole("button", { name: en["cloudLink.unlink"] }));
    expect(del).not.toHaveBeenCalled();
    const dialog = await screen.findByRole("dialog", { name: en["cloudLink.unlinkTitle"] });
    await userEvent.click(
      Array.from(dialog.querySelectorAll("button")).find(
        (b) => b.textContent === en["cloudLink.unlink"]
      )!
    );
    expect(del).toHaveBeenCalledWith("/api/me/cloud-link");
    expect(await screen.findByRole("button", { name: en["cloudLink.link"] })).toBeVisible();
  });

  it("has every string in Arabic", () => {
    for (const k of Object.keys(en).filter((k) => k.startsWith("cloudLink."))) {
      expect(ar[k as keyof typeof ar], k).toBeTruthy();
    }
  });
});
