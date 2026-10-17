import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, within } from "@testing-library/react";
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
});
