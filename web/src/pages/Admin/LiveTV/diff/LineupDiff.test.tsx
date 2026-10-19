// A draft's Lineup tab lists what it holds, the filler pools too (story 33.12 AC6).
import { describe, it, expect, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { I18nProvider } from "../../../../lib/i18n";
import { LineupDiff } from "./LineupDiff";

const pending = {
  channels: [],
  settings: [],
  filler: [
    { id: "p1", name: "Station ids", op: "added", items_added: 2, items_removed: 0 },
    { id: "p2aaaaaaaa", name: "", op: "removed", items_added: 0, items_removed: 0 },
  ],
};

describe("LineupDiff (draft)", () => {
  beforeEach(() => localStorage.clear());

  it("lists the filler pools a draft touches", () => {
    render(
      <I18nProvider>
        <LineupDiff pending={pending} names={{}} />
      </I18nProvider>
    );
    expect(screen.getByRole("heading", { name: "Filler pools" })).toBeInTheDocument();
    expect(screen.getByText(/Station ids/).closest("li")).toHaveTextContent(
      "added Station ids (2 clips added, 0 removed)"
    );
    expect(screen.getByText(/p2aaaaaa/).closest("li")).toHaveTextContent("removed p2aaaaaa");
  });

  it("renders in Arabic", () => {
    localStorage.setItem("mkt:locale", "ar");
    render(
      <I18nProvider>
        <LineupDiff pending={{ channels: [], settings: [] }} names={{}} />
      </I18nProvider>
    );
    expect(screen.getByRole("heading", { name: "مجموعات الفواصل" })).toBeInTheDocument();
  });
});
