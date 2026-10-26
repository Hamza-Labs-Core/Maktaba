// "Sign in a TV" page: the signed-in person approves the TV's code.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { post } = vi.hoisted(() => ({ post: vi.fn() }));
vi.mock("../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../lib/api")>("../lib/api");
  return { ...actual, api: { ...actual.api, post } };
});

import { ApiError } from "../lib/api";
import { renderPage, memberUser } from "../test/pageHarness";
import { Pair, normalizePairCode } from "./Pair";

function problem(status: number, title: string, detail?: string) {
  return new ApiError({ type: "about:blank", status, title, detail });
}

function open(path = "/pair?code=abcd-1234", opts: { user?: null } = {}) {
  return renderPage(<Pair />, { path, route: "/pair", user: memberUser, ...opts });
}

beforeEach(() => {
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
  it("prefills the code from the URL and names who the TV signs in as", () => {
    open();
    expect(screen.getByLabelText("TV code")).toHaveValue("abcd-1234");
    expect(
      screen.getByText(/Sign in a TV as .*member.*: enter the code the TV shows\./)
    ).toBeTruthy();
  });

  it("approve posts the normalized code and shows success", async () => {
    post.mockResolvedValue({ status: "approved" });
    open();
    await userEvent.setup().click(screen.getByRole("button", { name: "Approve" }));
    expect(post).toHaveBeenCalledWith("/api/pairing/approve", { code: "ABCD-1234" });
    expect(
      await screen.findByText("Your TV is signed in. You can close this page.")
    ).toBeInTheDocument();
  });

  it("a typed code works without one in the URL", async () => {
    post.mockResolvedValue({ status: "approved" });
    open("/pair");
    const user = userEvent.setup();
    expect(screen.getByRole("button", { name: "Approve" })).toBeDisabled();
    await user.type(screen.getByLabelText("TV code"), "wxyz 5678");
    await user.click(screen.getByRole("button", { name: "Approve" }));
    expect(post).toHaveBeenCalledWith("/api/pairing/approve", { code: "WXYZ-5678" });
  });

  it("409: says the code was already used", async () => {
    post.mockRejectedValue(problem(409, "conflict", "code already approved"));
    open();
    await userEvent.setup().click(screen.getByRole("button", { name: "Approve" }));
    expect(await screen.findByText(/This code was already used/)).toBeInTheDocument();
    expect(screen.queryByText(/Your TV is signed in/)).toBeNull();
  });

  it("422: asks to check the code", async () => {
    post.mockRejectedValue(problem(422, "unprocessable"));
    open();
    await userEvent.setup().click(screen.getByRole("button", { name: "Approve" }));
    expect(await screen.findByText(/Check the code/)).toBeInTheDocument();
  });

  it("a malformed code is caught before it is sent", async () => {
    open("/pair?code=nope");
    await userEvent.setup().click(screen.getByRole("button", { name: "Approve" }));
    expect(await screen.findByText(/Check the code/)).toBeInTheDocument();
    expect(post).not.toHaveBeenCalled();
  });

  it("signed out: goes to login and keeps the code to come back to", () => {
    open("/pair?code=ABCD-1234", { user: null });
    expect(screen.getByText("login page (from /pair?code=ABCD-1234)")).toBeInTheDocument();
  });
});
