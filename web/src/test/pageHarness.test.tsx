import { describe, it, expect, afterEach, vi } from "vitest";
import { screen } from "@testing-library/react";
import en from "../i18n/en.json";
import userEvent from "@testing-library/user-event";
import { renderPage, memberUser, quietCrash } from "./pageHarness";

let broken = true;
function Page() {
  if (broken) throw new Error("boom");
  return <p>the page</p>;
}

describe("pageHarness", () => {
  afterEach(() => vi.restoreAllMocks());

  it("redirects a signed-out visitor to /login with the page as `from`", () => {
    renderPage(<Page />, { path: "/x", user: null });
    expect(screen.getByText("login page (from /x)")).toBeInTheDocument();
  });

  it("shows the route error fallback on a crash, and navigation then works", async () => {
    quietCrash();
    broken = true;
    renderPage(<Page />, { path: "/x", user: memberUser });
    expect(screen.getByRole("alert")).toHaveTextContent(en["common.pageError"]);
    // the thrown message stays in the console
    expect(screen.getByRole("alert")).not.toHaveTextContent("boom");
    const user = userEvent.setup();
    await user.click(screen.getByRole("link", { name: "go elsewhere" }));
    expect(screen.getByText("elsewhere")).toBeInTheDocument();
    broken = false;
    await user.click(screen.getByRole("button", { name: "go back" }));
    expect(screen.getByText("the page")).toBeInTheDocument();
  });
});
