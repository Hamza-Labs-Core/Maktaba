import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { Card } from "./Card";

describe("Card", () => {
  it("renders header, body and footer regions", () => {
    render(
      <Card header="H" footer="F">
        Body content
      </Card>
    );
    expect(screen.getByText("H")).toBeInTheDocument();
    expect(screen.getByText("Body content")).toBeInTheDocument();
    expect(screen.getByText("F")).toBeInTheDocument();
  });

  it("makes the body scrollable rather than clipping overflow (EC)", () => {
    render(
      <Card scrollable>
        <div style={{ height: 9999 }}>tall</div>
      </Card>
    );
    const body = screen.getByText("tall").parentElement!;
    expect(body.className).toContain("mk-card__body--scroll");
  });

  it("is keyboard-focusable when interactive + tabIndex supplied", () => {
    render(
      <Card interactive tabIndex={0} role="button" aria-label="Open video">
        clickable
      </Card>
    );
    const card = screen.getByRole("button", { name: "Open video" });
    card.focus();
    expect(card).toHaveFocus();
  });
});
