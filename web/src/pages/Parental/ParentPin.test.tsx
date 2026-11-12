// Epic 57 story 57.06 on the web: setting and changing the parent PIN. The home
// asks for the account password again and never sends a PIN back.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { get, put } = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, put } };
});

import { forgetAnswers } from "../../lib/pageCache";
import { renderPage, memberUser } from "../../test/pageHarness";
import type { User } from "../../lib/auth";
import { ParentPinPage } from "./ParentPin";
import { familyGet, problem, type FamilyHome } from "./familyHome";

const guardian: User = { id: "u-guardian", username: "mum", is_admin: false };

function open(h: FamilyHome, user: User = guardian, locale: "en" | "ar" = "en") {
  localStorage.setItem("mkt:locale", locale);
  get.mockImplementation(familyGet(h));
  return renderPage(<ParentPinPage />, { path: "/family/pin", user });
}

async function fill(pin: string, again: string, password: string) {
  await userEvent.type(await screen.findByLabelText(/^(PIN|New PIN)/), pin);
  await userEvent.type(screen.getByLabelText("The PIN again"), again);
  if (password) await userEvent.type(screen.getByLabelText(/Your account password/), password);
}

beforeEach(() => {
  for (const f of [get, put]) f.mockReset();
  localStorage.clear();
  forgetAnswers("parental:");
});

describe("Parent PIN", () => {
  it("sets a first PIN with the account password", async () => {
    open({ pin: false });
    expect(await screen.findByTestId("family-pin-state")).toHaveTextContent(
      "You have no parent PIN yet."
    );
    await fill("246810", "246810", "my password");
    put.mockResolvedValue(undefined);
    await userEvent.click(screen.getByRole("button", { name: "Set a PIN" }));
    expect(put).toHaveBeenCalledWith("/api/me/parental-pin", {
      pin: "246810",
      password: "my password",
    });
    await waitFor(() =>
      expect(screen.getByTestId("family-pin-state")).toHaveTextContent("You have a parent PIN.")
    );
    // nothing typed stays on the page
    expect(screen.getByLabelText(/New PIN/)).toHaveValue("");
    expect(screen.getByLabelText(/Your account password/)).toHaveValue("");
  });

  it("offers Change when one is set", async () => {
    open({ pin: true });
    expect(await screen.findByRole("button", { name: "Change PIN" })).toBeDisabled();
  });

  it("the PIN is a one-time code to the browser: not saved, not taken for the account's password", async () => {
    open({ pin: false });
    await screen.findByRole("button", { name: "Set a PIN" });
    for (const field of [screen.getByLabelText(/^PIN/), screen.getByLabelText("The PIN again")]) {
      expect(field).toHaveAttribute("autocomplete", "one-time-code");
      expect(field).toHaveAttribute("type", "password");
    }
    expect(screen.getByLabelText(/Your account password/)).toHaveAttribute(
      "autocomplete",
      "current-password"
    );
  });

  it("takes digits only, six of them, typed the same twice", async () => {
    open({ pin: false });
    await fill("12ab34", "1234", "pw");
    expect(screen.getByLabelText(/^PIN/)).toHaveValue("1234");
    expect(screen.getByRole("button", { name: "Set a PIN" })).toBeDisabled();
    await userEvent.type(screen.getByLabelText(/^PIN/), "56");
    await userEvent.type(screen.getByLabelText("The PIN again"), "57");
    expect(screen.getByText("The two PINs are not the same.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Set a PIN" })).toBeDisabled();
    expect(put).not.toHaveBeenCalled();
  });

  it("a wrong password says so and keeps the digits", async () => {
    open({ pin: true });
    await fill("246810", "246810", "wrong");
    put.mockRejectedValue(problem(403, { code: "password_wrong" }));
    await userEvent.click(screen.getByRole("button", { name: "Change PIN" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("That password isn't right.");
    expect(screen.getByLabelText(/New PIN/)).toHaveValue("246810");
    expect(screen.getByLabelText(/Your account password/)).toHaveValue("");
  });

  it("someone who has no PIN to set sees the home's refusal and no form", async () => {
    open({ pin: problem(403) }, memberUser);
    expect(await screen.findByRole("alert")).toBeInTheDocument();
    expect(screen.queryByTestId("family-pin-form")).toBeNull();
  });

  it("is in Arabic, with the digits left to right", async () => {
    open({ pin: false }, guardian, "ar");
    const [pin] = await screen.findAllByLabelText(/^الرمز/, { selector: "input" });
    expect(document.documentElement).toHaveAttribute("dir", "rtl");
    expect(pin).toHaveAttribute("dir", "ltr");
    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("رمز الوالدين");
    expect(screen.getByRole("button", { name: "تعيين رمز" })).toBeInTheDocument();
  });
});
