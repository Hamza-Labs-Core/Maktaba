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
import en from "../i18n/en.json";
import { renderPage, memberUser } from "../test/pageHarness";
import { Pair, isCloudShaped, normalizePairCode, type PairRequest } from "./Pair";

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

  it("403 on approve (a cloud sign-in): shows the home's plain reason", async () => {
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

  it("a 403 whose detail names a path shows the generic error instead", async () => {
    get.mockResolvedValue(PHONE);
    post.mockRejectedValue(problem(403, "forbidden", "open /Volumes/x/pair.db: permission denied"));
    open();
    await userEvent.setup().click(await screen.findByRole("button", { name: "Approve" }));
    expect(await screen.findByText(en["common.error"])).toBeInTheDocument();
    expect(screen.queryByText(/Volumes/)).toBeNull();
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

// Epic 58 story 58.03 T06: a TV's code from Maktaba Cloud, approved here after the person
// confirms the TV by its platform and country.
describe("Pair: a code from Maktaba Cloud", () => {
  const TV = {
    code: "WEBS-2345",
    platform: "tizen",
    app_version: "1.0.0",
    country: "JO",
    expires_at: "2026-11-14T12:10:00Z",
  };
  function cloudNotLinked() {
    return new ApiError({
      type: "https://maktaba.tv/problems/cloud-not-linked",
      status: 409,
      title: "remote access is off",
      code: "cloud_not_linked",
    });
  }

  it("knows a cloud code's shape: no 0 or 1", () => {
    expect(isCloudShaped("WEBS-2345")).toBe(true);
    expect(isCloudShaped("ABCD-1234")).toBe(false);
    expect(isCloudShaped("WEBS-2340")).toBe(false);
  });

  it("a code the home does not hold is looked up in the cloud, confirmed, then approved", async () => {
    get.mockImplementation((url: string) =>
      url === "/api/pairing/device/WEBS-2345"
        ? Promise.reject(problem(404, "not found"))
        : url === "/api/pairing/cloud-code/WEBS-2345"
          ? Promise.resolve(TV)
          : Promise.reject(new Error(`unmocked GET ${url}`))
    );
    post.mockResolvedValue({ status: "approved", platform: "tizen", country: "JO" });
    open("/pair?code=webs-2345");
    expect(
      await screen.findByText(/Is this your TV\? .*Samsung.*, in .*Jordan.*\./)
    ).toBeInTheDocument();
    // nothing is approved until the person says so
    expect(post).not.toHaveBeenCalled();
    await userEvent.setup().click(screen.getByRole("button", { name: "Approve" }));
    expect(await screen.findByText(en["approveTv.cloud.done"])).toBeInTheDocument();
    expect(post).toHaveBeenCalledWith("/api/pairing/cloud-approve", { code: "WEBS-2345" });
    expect(post).not.toHaveBeenCalledWith("/api/pairing/approve", expect.anything());
  });

  it("a cloud code with no country names the platform alone", async () => {
    get.mockImplementation((url: string) =>
      url.startsWith("/api/pairing/device/")
        ? Promise.reject(problem(404, "not found"))
        : Promise.resolve({ ...TV, country: "" })
    );
    open("/pair?code=webs-2345");
    expect(await screen.findByText(/^Is this your TV\? .*Samsung.*\.$/)).toBeInTheDocument();
  });

  it("a home without remote access says to use the home's code", async () => {
    get.mockImplementation((url: string) =>
      url.startsWith("/api/pairing/device/")
        ? Promise.reject(problem(404, "not found"))
        : Promise.reject(cloudNotLinked())
    );
    open("/pair?code=webs-2345");
    expect(await screen.findByText(en["approveTv.cloud.needsRemote"])).toBeInTheDocument();
    expect(post).not.toHaveBeenCalled();
  });

  it("a code shaped like the home's only (it has a 1) is not looked up in the cloud", async () => {
    get.mockRejectedValue(problem(404, "not found"));
    open("/pair?code=abcd-1234");
    expect(await screen.findByText(/not waiting for a sign-in/)).toBeInTheDocument();
    expect(get).toHaveBeenCalledTimes(1);
  });

  it("a cloud code no longer open is not waiting; a home code still approves the old way", async () => {
    get.mockRejectedValue(problem(404, "not found"));
    open("/pair?code=webs-2345");
    expect(await screen.findByText(/not waiting for a sign-in/)).toBeInTheDocument();
    expect(get).toHaveBeenCalledWith("/api/pairing/cloud-code/WEBS-2345");
  });

  it("in Arabic the question reads right to left, the country in Arabic", async () => {
    get.mockImplementation((url: string) =>
      url.startsWith("/api/pairing/device/")
        ? Promise.reject(problem(404, "not found"))
        : Promise.resolve(TV)
    );
    localStorage.setItem("mkt:locale", "ar");
    try {
      open("/pair?code=webs-2345");
      expect(
        await screen.findByText(/هل هذا تلفازك؟ .*Samsung.*، في .*الأردن/)
      ).toBeInTheDocument();
      expect(document.documentElement.dir).toBe("rtl");
    } finally {
      localStorage.removeItem("mkt:locale");
    }
  });
});
