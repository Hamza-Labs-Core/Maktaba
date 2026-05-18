import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Checkbox } from "./Checkbox";
import { Radio, RadioGroup } from "./Radio";
import { Toggle } from "./Toggle";

describe("Checkbox", () => {
  it("toggles on click via its label and stays keyboard-focusable", async () => {
    render(<Checkbox label="Accept" />);
    const user = userEvent.setup();
    const box = screen.getByRole("checkbox", { name: "Accept" });
    expect(box).not.toBeChecked();
    await user.click(screen.getByText("Accept"));
    expect(box).toBeChecked();
    box.blur();
    await user.tab();
    expect(box).toHaveFocus();
  });
});

describe("Radio / RadioGroup", () => {
  it("exposes a named radiogroup and lets one option be selected", async () => {
    render(
      <RadioGroup legend="Quality" name="q">
        <Radio name="q" value="low" label="Low" />
        <Radio name="q" value="high" label="High" />
      </RadioGroup>
    );
    const user = userEvent.setup();
    expect(screen.getByRole("radiogroup", { name: "Quality" })).toBeInTheDocument();
    await user.click(screen.getByRole("radio", { name: "High" }));
    expect(screen.getByRole("radio", { name: "High" })).toBeChecked();
    expect(screen.getByRole("radio", { name: "Low" })).not.toBeChecked();
  });
});

describe("Toggle", () => {
  it("renders as a switch and flips state", async () => {
    render(<Toggle label="Captions" />);
    const user = userEvent.setup();
    const sw = screen.getByRole("switch", { name: "Captions" });
    expect(sw).not.toBeChecked();
    await user.click(sw);
    expect(sw).toBeChecked();
  });
});
