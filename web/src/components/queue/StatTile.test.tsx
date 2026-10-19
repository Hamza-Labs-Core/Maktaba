import { describe, it, expect } from "vitest";
import { render, screen, within } from "@testing-library/react";
import { StatGrid, StatTile } from "./StatTile";

describe("StatTile", () => {
  it("labels the tile, and shows number, unit and detail lines", () => {
    render(
      <StatGrid label="Health">
        <StatTile label="Backlog" value="523" unit="jobs waiting">
          <p>Oldest waiting 52 min</p>
        </StatTile>
      </StatGrid>
    );
    const tile = screen.getByRole("group", { name: "Backlog" });
    expect(within(tile).getByText("523")).toHaveClass("mkt-q-stat__num");
    expect(within(tile).getByText("jobs waiting")).toHaveClass("mkt-q-stat__unit");
    expect(within(tile).getByText("Oldest waiting 52 min")).toBeInTheDocument();
    expect(screen.getByRole("group", { name: "Health" })).toHaveClass("mkt-q-stats");
  });

  it("marks a tone with a border class and a '!' that is not colour alone", () => {
    render(<StatTile label="Gave up" value="3" tone="error" />);
    const tile = screen.getByRole("group", { name: "Gave up" });
    expect(tile).toHaveClass("mkt-q-stat--error");
    expect(tile.querySelector(".mkt-q-stat__flag")).toHaveTextContent("!");
  });

  it("leaves out the unit, details and sparkline when not given", () => {
    const { container } = render(<StatTile label="Series" value="14" />);
    expect(container.querySelector(".mkt-q-stat__unit")).toBeNull();
    expect(container.querySelector(".mkt-q-stat__detail")).toBeNull();
    expect(container.querySelector("svg")).toBeNull();
  });

  it("draws the label as a heading when asked, and a sparkline with its name", () => {
    render(
      <StatTile
        label="CPU"
        value="47%"
        heading="h2"
        spark={{ values: [1, 2, 3], label: "CPU, last 4 hours" }}
      />
    );
    expect(screen.getByRole("heading", { level: 2, name: "CPU" })).toBeInTheDocument();
    expect(screen.getByRole("img", { name: "CPU, last 4 hours" })).toBeInTheDocument();
  });
});
