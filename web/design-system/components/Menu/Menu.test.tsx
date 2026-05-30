import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Menu } from "./Menu";

describe("Menu", () => {
  it("opens, exposes role=menu, selects an item and closes", async () => {
    const onSelect = vi.fn();
    render(
      <Menu
        trigger="Actions"
        items={[
          { id: "edit", label: "Edit", onSelect },
          { id: "del", label: "Delete", onSelect: () => {} },
        ]}
      />
    );
    const user = userEvent.setup();
    const trigger = screen.getByRole("button", { name: "Actions" });
    expect(trigger).toHaveAttribute("aria-haspopup", "menu");
    await user.click(trigger);
    expect(screen.getByRole("menu")).toBeInTheDocument();
    await user.click(screen.getByRole("menuitem", { name: "Edit" }));
    expect(onSelect).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
  });

  it("closes on Escape and restores focus to the trigger", async () => {
    render(<Menu trigger="Actions" items={[{ id: "a", label: "A", onSelect: () => {} }]} />);
    const user = userEvent.setup();
    const trigger = screen.getByRole("button", { name: "Actions" });
    await user.click(trigger);
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
    expect(trigger).toHaveFocus();
  });
});
