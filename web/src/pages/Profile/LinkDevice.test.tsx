// Signing in a TV with its code (/link).
import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post } };
});

import { ApiError } from "../../lib/api";
import { renderPage } from "../../test/pageHarness";
import { LinkDevice } from "./LinkDevice";

const TV = {
  code: "ABCD-1234",
  device_kind: "tv",
  device_label: "Living room TV",
  expires_at: "2026-10-26T08:10:00Z",
};

function problem(status: number, title: string) {
  return new ApiError({ type: "about:blank", status, title });
}

function open(path = "/link", opts: { user?: null } = {}) {
  return renderPage(<LinkDevice />, { path, route: "/link", ...opts });
}

describe("LinkDevice", () => {
  beforeEach(() => {
    get.mockReset();
    post.mockReset();
  });

  it("a QR's link goes straight to the TV it names, and approving signs it in", async () => {
    get.mockResolvedValue(TV);
    post.mockResolvedValue({ status: "approved" });
    open("/link?code=ABCD-1234");
    expect(await screen.findByText(/Sign in ⁨?Living room TV⁩? as/)).toBeInTheDocument();
    expect(get).toHaveBeenCalledWith("/api/auth/device?code=ABCD-1234");
    await userEvent.setup().click(screen.getByRole("button", { name: "Sign it in" }));
    expect(await screen.findByRole("status")).toHaveTextContent("is signed in");
    expect(post).toHaveBeenCalledWith("/api/auth/device/approve", { code: "ABCD-1234" });
  });

  it("a typed code is upper-cased and looked up", async () => {
    get.mockResolvedValue(TV);
    open();
    const user = userEvent.setup();
    await user.type(screen.getByLabelText("Code"), "abcd-1234");
    await user.click(screen.getByRole("button", { name: "Continue" }));
    expect(await screen.findByRole("button", { name: "Sign it in" })).toBeInTheDocument();
    expect(get).toHaveBeenCalledWith("/api/auth/device?code=ABCD-1234");
  });

  it("a code no TV shows says so and stays on the form", async () => {
    get.mockRejectedValue(problem(404, "Not Found"));
    open("/link?code=ZZZZ-ZZZZ");
    expect(await screen.findByText(/No TV is showing this code/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Continue" })).toBeInTheDocument();
    expect(post).not.toHaveBeenCalled();
  });

  it("a TV someone else signed in is refused", async () => {
    get.mockResolvedValue(TV);
    post.mockRejectedValue(problem(409, "Conflict"));
    open("/link?code=ABCD-1234");
    await userEvent.setup().click(await screen.findByRole("button", { name: "Sign it in" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Someone else already signed this TV in."
    );
  });

  it("signed out, the page sends you to sign in first", async () => {
    open("/link?code=ABCD-1234", { user: null });
    expect(get).not.toHaveBeenCalled();
  });
});
