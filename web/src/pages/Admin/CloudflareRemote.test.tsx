import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { get, post, put, del } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  put: vi.fn(),
  del: vi.fn(),
}));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post, put, delete: del } };
});

import { AdminExtensions } from "./Extensions";
import { renderAt, routeGet } from "../../test/queueHarness";
import type { Extension } from "../../lib/queueApi";
import type { RemoteIntegration, RemoteTestResult } from "../../lib/tvRemoteApi";

const cfExt: Extension = {
  name: "cloudflare_stream",
  label: "Cloudflare Stream",
  description: "Remote viewing through your own Cloudflare Stream account.",
  version: "1",
  enabled: false,
  configured: true,
  settings_schema: [
    { key: "account_id", label: "Account ID", type: "text", required: true },
    { key: "api_token", label: "API token", type: "secret", required: true },
    { key: "customer_code", label: "Customer subdomain code", type: "text" },
  ],
  settings: { account_id: "acc1", api_token: "********", customer_code: "" },
  secrets_set: ["api_token"],
};

const green: RemoteTestResult = {
  ok: true,
  message: "Cloudflare Stream is ready",
  checked_at: "2026-10-19T12:00:00Z",
  lines: [
    { key: "token", ok: true, detail: "active" },
    { key: "stream", ok: true, detail: "1 live inputs (1 of Maktaba)" },
    { key: "storage", ok: true, detail: "3 of 1000 minutes used" },
    { key: "analytics", ok: false, optional: true, detail: "no Account Analytics read" },
    { key: "signing_key", ok: true, detail: "created key k1" },
    { key: "customer_domain", ok: true, detail: "customer-abc.cloudflarestream.com" },
    { key: "push", ok: true, detail: "the home's push relay can push RTMPS (ffmpeg)" },
  ],
};

const failed: RemoteTestResult = {
  ok: false,
  message: "not ready: token",
  checked_at: "2026-10-19T12:00:00Z",
  lines: [
    { key: "token", ok: false, detail: "cloudflare 401: 1000 Invalid API Token" },
    { key: "stream", ok: false, skipped: true, detail: "not checked" },
  ],
};

function status(over: Partial<RemoteIntegration> = {}): { integration: RemoteIntegration } {
  return {
    integration: {
      configured: true,
      account_id: "acc1",
      token_set: true,
      token_problem: null,
      signing_key_set: false,
      customer_domain: null,
      enabled: false,
      can_enable: false,
      last_test: null,
      ...over,
    },
  };
}

function setup(st: { integration: RemoteIntegration }) {
  routeGet(get, { "/api/extensions": { extensions: [cfExt] }, "/api/tv/remote": st });
  return renderAt(<AdminExtensions />, "/admin/extensions");
}

async function card() {
  return (await screen.findByRole("heading", { name: "Cloudflare Stream" })).closest("section")!;
}

describe("Cloudflare Stream card", () => {
  beforeEach(() => {
    get.mockReset();
    post.mockReset();
    put.mockReset();
    del.mockReset();
  });
  afterEach(() => vi.restoreAllMocks());

  it("keeps the token write-only and says remote needs a passed test", async () => {
    setup(status());
    const c = await card();
    const token = within(c).getByLabelText(/API token/) as HTMLInputElement;
    expect(token.type).toBe("password");
    expect(token.value).toBe("");
    expect(await within(c).findByText(/Not tested yet/)).toBeInTheDocument();
    expect(within(c).getByRole("switch", { name: "Remote viewing" })).toBeDisabled();
    expect(within(c).getByText("Needs a passed test from the last 24 hours.")).toBeInTheDocument();
  });

  it("lists every check of Test connection with its reason", async () => {
    setup(status());
    post.mockResolvedValue(failed);
    const c = await card();
    await userEvent.click(within(c).getByRole("button", { name: "Test connection" }));
    expect(post).toHaveBeenCalledWith("/api/extensions/cloudflare_stream/test");
    expect(await within(c).findByText(/Connection failed: not ready: token/)).toBeInTheDocument();
    // the status panel reloads after a test
    expect(get.mock.calls.filter((a) => a[0] === "/api/tv/remote").length).toBeGreaterThanOrEqual(
      2
    );
  });

  it("shows the last test and turns remote on after a green one", async () => {
    setup(
      status({
        last_test: green,
        can_enable: true,
        customer_domain: "customer-abc.cloudflarestream.com",
      })
    );
    put.mockResolvedValue(status({ last_test: green, can_enable: true, enabled: true }));
    const c = await card();
    const list = await within(c).findByRole("list", { name: "Connection checks" });
    expect(within(list).getAllByRole("listitem")).toHaveLength(7);
    expect(within(list).getByText("Signing key")).toBeInTheDocument();
    expect(within(list).getByText("(missing, optional)")).toBeInTheDocument();
    expect(within(c).getByText(/Last test passed/)).toBeInTheDocument();
    await userEvent.click(within(c).getByRole("switch", { name: "Remote viewing" }));
    expect(put).toHaveBeenCalledWith("/api/tv/remote/enabled", { enabled: true });
    expect(await within(c).findByText("Remote viewing is on")).toBeInTheDocument();
  });

  it("shows a token that cannot be opened any more", async () => {
    setup(
      status({
        token_problem:
          "the saved token cannot be opened (secrets.key was replaced): enter the API token again",
      })
    );
    const c = await card();
    expect(await within(c).findByText(/enter the API token again/)).toBeInTheDocument();
  });

  it("removes the integration only after confirmation", async () => {
    setup(status({ last_test: green }));
    const confirm = vi
      .spyOn(window, "confirm")
      .mockReturnValueOnce(false)
      .mockReturnValueOnce(true);
    del.mockResolvedValue({ deleted_inputs: ["i1"], deleted_signing_key: "k1", errors: [] });
    const c = await card();
    const btn = await within(c).findByRole("button", { name: "Remove integration" });
    await userEvent.click(btn);
    expect(del).not.toHaveBeenCalled();
    await userEvent.click(btn);
    expect(confirm).toHaveBeenCalledTimes(2);
    expect(del).toHaveBeenCalledWith("/api/tv/remote/integration", { confirm: true });
    expect(await within(c).findByText(/1 live inputs deleted at Cloudflare/)).toBeInTheDocument();
  });
});
