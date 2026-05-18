import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Tabs } from "./Tabs";

const items = [
  { id: "a", label: "Alpha", content: <p>Alpha panel</p> },
  { id: "b", label: "Beta", content: <p>Beta panel</p> },
  { id: "c", label: "Gamma", content: <p>Gamma panel</p> },
];

describe("Tabs", () => {
  it("exposes an ARIA tablist and shows only the active panel", () => {
    render(<Tabs items={items} label="Sections" />);
    expect(screen.getByRole("tablist", { name: "Sections" })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Alpha" })).toHaveAttribute(
      "aria-selected",
      "true"
    );
    expect(screen.getByText("Alpha panel")).toBeVisible();
    expect(screen.queryByText("Beta panel")).not.toBeInTheDocument();
  });

  it("moves selection with Arrow keys (roving tabindex)", async () => {
    render(<Tabs items={items} label="Sections" />);
    const user = userEvent.setup();
    screen.getByRole("tab", { name: "Alpha" }).focus();
    await user.keyboard("{ArrowRight}");
    expect(screen.getByRole("tab", { name: "Beta" })).toHaveAttribute(
      "aria-selected",
      "true"
    );
    expect(screen.getByText("Beta panel")).toBeVisible();
    await user.keyboard("{End}");
    expect(screen.getByRole("tab", { name: "Gamma" })).toHaveAttribute(
      "aria-selected",
      "true"
    );
  });
});
