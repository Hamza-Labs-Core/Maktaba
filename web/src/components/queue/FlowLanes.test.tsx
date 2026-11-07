// Epic 54 (story 54.13): a plugin's queues are the Queues page's Plugins lane, apart from the
// built-in extensions' lane.
import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { I18nProvider } from "../../lib/i18n";
import { snapshotFixture, stagesFixture } from "../../test/fixtures/queues";
import { FlowLanes } from "./FlowLanes";

describe("FlowLanes", () => {
  it("puts a plugin's stage in the Plugins lane", () => {
    const base = stagesFixture[0];
    const stages = [
      ...stagesFixture,
      {
        ...base,
        stage: "p_3675e5d8_hello",
        label: "dev.example.hello: hello",
        lane: "media" as const,
        extension: "plugin:dev.example.hello",
      },
    ];
    render(
      <I18nProvider>
        <MemoryRouter>
          <FlowLanes
            stages={stages}
            queues={snapshotFixture.queues}
            pools={snapshotFixture.pools}
            flows={snapshotFixture.flows}
          />
        </MemoryRouter>
      </I18nProvider>
    );
    const lane = screen.getByRole("heading", { name: "Plugins" }).closest("section");
    expect(lane?.textContent).toContain("dev.example.hello: hello");
  });
});
