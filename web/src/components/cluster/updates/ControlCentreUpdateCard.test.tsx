// TC4 (story 35.15 AC3): the control centre's update card shows the run's release, where
// it is and how many machines are done, links to the Updates page, and is not there
// without a run (or for someone the updates API refuses).
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { I18nProvider } from "../../../lib/i18n";
import en from "../../../i18n/en.json";

const { get } = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock("../../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../../lib/api")>("../../../lib/api");
  return { ...actual, api: { ...actual.api, get } };
});

import { ControlCentreUpdateCard } from "./ControlCentreUpdateCard";

const run = {
  id: "r1",
  version: "1.5.0",
  from_version: "1.4.0",
  trigger: "auto",
  state: "rolling_out",
  step: "",
  started_at: "2026-10-25T03:00:00Z",
};

const target = (name: string, state: string) => ({
  node_id: name,
  name,
  ord: 0,
  platform: "linux-amd64",
  kind: "layout",
  state,
  from_version: "1.4.0",
  to_version: "1.5.0",
  attempts: 1,
});

const renderCard = () =>
  render(
    <MemoryRouter>
      <I18nProvider>
        <ControlCentreUpdateCard />
      </I18nProvider>
    </MemoryRouter>
  );

describe("ControlCentreUpdateCard", () => {
  beforeEach(() => {
    localStorage.clear();
    get.mockReset();
  });

  it("shows the run: release, state, machines done of all, the Updates page", async () => {
    get.mockImplementation(async (path: string) => {
      if (path === "/api/cluster/updates") return { run };
      if (path === "/api/cluster/updates/runs/r1") {
        return {
          ...run,
          targets: [target("a", "healthy"), target("b", "applying"), target("c", "pending")],
        };
      }
      throw new Error("unexpected " + path);
    });
    renderCard();
    expect(
      await screen.findByText(en["updates.card.title"].replace("{version}", "1.5.0"))
    ).toBeInTheDocument();
    expect(screen.getByText(en["updates.state.rolling_out"])).toBeInTheDocument();
    expect(
      await screen.findByText(
        en["updates.card.machines"].replace("{done}", "1").replace("{total}", "3")
      )
    ).toBeInTheDocument();
    expect(screen.getByRole("link", { name: en["updates.card.open"] })).toHaveAttribute(
      "href",
      "/admin/updates"
    );
  });

  it("is not there without a run, nor when the updates API refuses", async () => {
    get.mockResolvedValueOnce({ run: undefined });
    const { container, unmount } = renderCard();
    await vi.waitFor(() => expect(get).toHaveBeenCalled());
    expect(container).toBeEmptyDOMElement();
    unmount();
    get.mockReset();
    get.mockRejectedValue(Object.assign(new Error("forbidden"), { status: 403 }));
    const second = renderCard();
    await vi.waitFor(() => expect(get).toHaveBeenCalled());
    expect(second.container).toBeEmptyDOMElement();
  });
});
