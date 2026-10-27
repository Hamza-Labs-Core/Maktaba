// "Connect an app" (Epic 42 story 42.2): the QR code of the app link, the addresses to
// type, and copying them.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { get, copyText } = vi.hoisted(() => ({ get: vi.fn(), copyText: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get } };
});
vi.mock("../../lib/clipboard", () => ({ copyText }));

import { ApiError } from "../../lib/api";
import { ToastProvider } from "@ds/components/Toast/Toast";
import { renderPage, memberUser } from "../../test/pageHarness";
import { ConnectApp, connectLink, type DiscoveryHome } from "./ConnectApp";

const HOME: DiscoveryHome = {
  id: "0a1b2c3d-0000-4000-8000-000000000000",
  name: "Kitchen & Den",
  version: "1.6.0",
  urls: [
    "http://192.168.1.5:8088",
    "http://studio.local:8088",
    "https://kitchen.relay.maktaba.app",
  ],
  sign_in: { password: true, code: true },
};

function open(opts: { user?: null } = {}) {
  return renderPage(
    <ToastProvider>
      <ConnectApp />
    </ToastProvider>,
    { path: "/account/connect", user: memberUser, ...opts }
  );
}

beforeEach(() => {
  get.mockReset();
  copyText.mockReset();
});

describe("connectLink", () => {
  it("is maktaba://connect with the id, the name and every address, LAN first, encoded", () => {
    expect(connectLink(HOME)).toBe(
      "maktaba://connect?id=0a1b2c3d-0000-4000-8000-000000000000&n=Kitchen%20%26%20Den" +
        "&u=http%3A%2F%2F192.168.1.5%3A8088&u=http%3A%2F%2Fstudio.local%3A8088" +
        "&u=https%3A%2F%2Fkitchen.relay.maktaba.app"
    );
  });

  it("round-trips through URLSearchParams, Arabic names too", () => {
    const link = connectLink({ id: "x", name: "بيت سارة", urls: ["http://h:8088"] });
    const q = new URL(link.replace("maktaba://", "https://x/")).searchParams;
    expect(q.get("n")).toBe("بيت سارة");
    expect(q.getAll("u")).toEqual(["http://h:8088"]);
  });
});

describe("ConnectApp", () => {
  it("shows the QR code of the app link and every address", async () => {
    get.mockResolvedValue(HOME);
    open();
    const qr = await screen.findByTestId("connect-qr");
    expect(get).toHaveBeenCalledWith("/api/discovery/home");
    expect(qr.getAttribute("data-link")).toBe(connectLink(HOME));
    const svg = within(qr).getByRole("img", {
      name: /QR code that connects an app to .*Kitchen & Den/,
    });
    expect(Number(svg.getAttribute("data-modules"))).toBeGreaterThan(20);
    for (const u of HOME.urls) expect(screen.getByText(u)).toHaveAttribute("dir", "ltr");
    expect(screen.getByRole("link", { name: "Sign in a device" })).toHaveAttribute("href", "/pair");
  });

  it("Copy copies one address; Copy the app link copies the link", async () => {
    get.mockResolvedValue(HOME);
    copyText.mockResolvedValue(true);
    open();
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: `Copy ${HOME.urls[2]}` }));
    expect(copyText).toHaveBeenLastCalledWith(HOME.urls[2]);
    expect(await screen.findByText("Copied.")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Copy the app link" }));
    expect(copyText).toHaveBeenLastCalledWith(connectLink(HOME));
  });

  it("a failed copy says so", async () => {
    get.mockResolvedValue(HOME);
    copyText.mockResolvedValue(false);
    open();
    await userEvent
      .setup()
      .click(await screen.findByRole("button", { name: `Copy ${HOME.urls[0]}` }));
    expect(await screen.findByText(/Couldn't copy/)).toBeInTheDocument();
  });

  it("a home with no address: no QR code, and says why", async () => {
    get.mockResolvedValue({ ...HOME, urls: [] });
    open();
    expect(await screen.findByText(/no address another device can use/)).toBeInTheDocument();
    expect(screen.queryByTestId("connect-qr")).toBeNull();
  });

  it("a home that cannot sign in by code does not offer it", async () => {
    get.mockResolvedValue({ ...HOME, sign_in: { password: true, code: false } });
    open();
    await screen.findByTestId("connect-qr");
    expect(screen.queryByRole("link", { name: "Sign in a device" })).toBeNull();
  });

  it("an error can be retried", async () => {
    get.mockRejectedValueOnce(new ApiError({ type: "about:blank", status: 500, title: "boom" }));
    get.mockResolvedValueOnce(HOME);
    open();
    await userEvent.setup().click(await screen.findByRole("button", { name: "Try again" }));
    expect(await screen.findByTestId("connect-qr")).toBeInTheDocument();
  });

  it("signed out: goes to login", () => {
    open({ user: null });
    expect(screen.getByText("login page (from /account/connect)")).toBeInTheDocument();
    expect(get).not.toHaveBeenCalled();
  });
});
