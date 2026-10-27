// "Sign in a device" page (Epic 14, Epic 42 story 42.6): the signed-in person sees what
// asks with a code, then approves it with an explicit click.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock("../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../lib/api")>("../lib/api");
  return { ...actual, api: { ...actual.api, get, post } };
});

import { ApiError } from "../lib/api";
import { renderPage, memberUser } from "../test/pageHarness";
import { Pair, normalizePairCode, type PairRequest } from "./Pair";

function problem(status: number, title: string, detail?: string) {
  return new ApiError({ type: "about:blank", status, title, detail });
}

const PHONE: PairRequest = {
  code: "ABCD-1234",
  client_app: "player",
  platform: "ios",
  device: "phone",
  device_label: "Sara's iPhone",
  expires_at: "2026-10-27T12:10:00Z",
};

function open(path = "/pair?code=abcd-1234", opts: { user?: null } = {}) {
  return renderPage(<Pair />, { path, route: "/pair", user: memberUser, ...opts });
}

beforeEach(() => {
  get.mockReset();
  post.mockReset();
});

describe("normalizePairCode", () => {
  it("ignores case, dashes and spaces", () => {
    expect(normalizePairCode(" abcd 1234 ")).toBe("ABCD-1234");
    expect(normalizePairCode("ab-cd-12-34")).toBe("ABCD-1234");
  });
  it("refuses what cannot be a code", () => {
    expect(normalizePairCode("ABCD-123")).toBeNull();
    expect(normalizePairCode("ABCD-123O")).toBeNull(); // O is not Crockford
    expect(normalizePairCode("")).toBeNull();
  });
});

describe("Pair", () => {
  it("a code from the URL (the app's QR code) shows what is asking, and waits for Approve", async () => {
    get.mockResolvedValue(PHONE);
    open();
    expect(
      await screen.findByText(/Maktaba Player.*asks to sign in as .*member/)
    ).toBeInTheDocument();
    expect(get).toHaveBeenCalledWith("/api/pairing/device/ABCD-1234");
    expect(screen.getByText(/Sara's iPhone/)).toBeInTheDocument();
    expect(screen.getByText(/iOS.*phone/)).toBeInTheDocument();
    expect(screen.getByText("ABCD-1234")).toBeInTheDocument();
    // nothing is approved by opening the link
    expect(post).not.toHaveBeenCalled();
  });

  it("Approve posts the code and shows success", async () => {
    get.mockResolvedValue(PHONE);
    post.mockResolvedValue({ status: "approved" });
    open();
    await userEvent.setup().click(await screen.findByRole("button", { name: "Approve" }));
    expect(post).toHaveBeenCalledWith("/api/pairing/approve", { code: "ABCD-1234" });
    expect(
      await screen.findByText("The device is signed in. You can close this page.")
    ).toBeInTheDocument();
  });

  it("Cancel goes back to the code without approving", async () => {
    get.mockResolvedValue(PHONE);
    open();
    await userEvent.setup().click(await screen.findByRole("button", { name: "Cancel" }));
    expect(screen.getByLabelText("Code")).toHaveValue("abcd-1234");
    expect(post).not.toHaveBeenCalled();
  });

  it("a typed code is looked up on Continue, then approved", async () => {
    get.mockResolvedValue({
      ...PHONE,
      code: "WXYZ-5678",
      client_app: "tv",
      platform: "tvos",
      device: "tv",
      device_label: "",
    });
    post.mockResolvedValue({ status: "approved" });
    open("/pair");
    const user = userEvent.setup();
    expect(screen.getByRole("button", { name: "Continue" })).toBeDisabled();
    await user.type(screen.getByLabelText("Code"), "wxyz 5678");
    await user.click(screen.getByRole("button", { name: "Continue" }));
    expect(get).toHaveBeenCalledWith("/api/pairing/device/WXYZ-5678");
    expect(await screen.findByText(/Maktaba TV.*asks to sign in/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Approve" }));
    expect(post).toHaveBeenCalledWith("/api/pairing/approve", { code: "WXYZ-5678" });
  });

  it("an app that says nothing about itself reads as a device", async () => {
    get.mockResolvedValue({ ...PHONE, client_app: "", platform: "", device: "", device_label: "" });
    open();
    expect(await screen.findByText(/A device.*asks to sign in/)).toBeInTheDocument();
  });

  it("404: the code is not waiting", async () => {
    get.mockRejectedValue(problem(404, "not found"));
    open();
    expect(await screen.findByText(/This code is not waiting for a sign-in/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Approve" })).toBeNull();
  });

  it("409 on approve: says the code was already used", async () => {
    get.mockResolvedValue(PHONE);
    post.mockRejectedValue(problem(409, "conflict", "code already approved"));
    open();
    await userEvent.setup().click(await screen.findByRole("button", { name: "Approve" }));
    expect(await screen.findByText(/This code was already used/)).toBeInTheDocument();
    expect(screen.queryByText(/The device is signed in/)).toBeNull();
  });

  it("403 on approve (a cloud sign-in): shows the server's reason", async () => {
    get.mockResolvedValue(PHONE);
    post.mockRejectedValue(
      problem(
        403,
        "forbidden",
        "a sign-in through Maktaba Cloud cannot sign in a device; sign in on the home"
      )
    );
    open();
    await userEvent.setup().click(await screen.findByRole("button", { name: "Approve" }));
    expect(await screen.findByText(/cannot sign in a device/)).toBeInTheDocument();
  });

  it("a malformed code is caught before it is sent", async () => {
    open("/pair?code=nope");
    await userEvent.setup().click(screen.getByRole("button", { name: "Continue" }));
    expect(await screen.findByText(/Check the code/)).toBeInTheDocument();
    expect(get).not.toHaveBeenCalled();
  });

  it("signed out: goes to login and keeps the code to come back to", () => {
    open("/pair?code=ABCD-1234", { user: null });
    expect(screen.getByText("login page (from /pair?code=ABCD-1234)")).toBeInTheDocument();
    expect(get).not.toHaveBeenCalled();
  });

  it("in Arabic the page reads right to left with the code kept left to right", async () => {
    get.mockResolvedValue(PHONE);
    localStorage.setItem("mkt:locale", "ar");
    try {
      open();
      await waitFor(() => expect(get).toHaveBeenCalled());
      expect(await screen.findByText(/يطلب .*مشغّل مكتبة/)).toBeInTheDocument();
      expect(document.documentElement.dir).toBe("rtl");
      const code = screen.getByText("ABCD-1234");
      expect(code.getAttribute("dir")).toBe("ltr");
      expect(screen.getByRole("button", { name: "موافقة" })).toBeInTheDocument();
    } finally {
      localStorage.removeItem("mkt:locale");
    }
  });
});
