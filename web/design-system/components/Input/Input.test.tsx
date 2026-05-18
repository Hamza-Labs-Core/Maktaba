import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Input } from "./Input";

describe("Input", () => {
  it("associates the label with the control", () => {
    render(<Input label="Email" />);
    expect(screen.getByLabelText("Email")).toBeInTheDocument();
  });

  it("wires aria-describedby to the description", () => {
    render(<Input label="Email" description="We never share it." />);
    const input = screen.getByLabelText("Email");
    const descId = input.getAttribute("aria-describedby");
    expect(descId).toBeTruthy();
    expect(document.getElementById(descId!)).toHaveTextContent("We never share it.");
  });

  it("marks the control invalid and exposes the error via role=alert", () => {
    render(<Input label="Email" error="Required" />);
    const input = screen.getByLabelText("Email");
    expect(input).toHaveAttribute("aria-invalid", "true");
    expect(screen.getByRole("alert")).toHaveTextContent("Required");
  });

  it("accepts typed input", async () => {
    render(<Input label="Email" />);
    const user = userEvent.setup();
    await user.type(screen.getByLabelText("Email"), "a@b.co");
    expect(screen.getByLabelText("Email")).toHaveValue("a@b.co");
  });
});
