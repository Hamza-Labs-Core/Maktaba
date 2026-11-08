// Story 53.12: the Overview names machines by the answer's names (as the Machines page
// does), falling back to the node id; a legend's machine= value is named the same way.
import { describe, it, expect, vi } from "vitest";

const { get } = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock("./api", () => ({ api: { get } }));

import { getBoard, machineName, seriesLabel } from "./dashboards";

describe("dashboards machine names", () => {
  it("names a machine by its name, else its id", () => {
    expect(machineName({ n1: "Attic" }, "n1")).toBe("Attic");
    expect(machineName({ n1: "Attic" }, "n2")).toBe("n2");
    expect(machineName({ n1: "  " }, "n1")).toBe("n1");
    expect(machineName(undefined, "n1")).toBe("n1");
  });

  it("names the machine in a legend and keeps the other labels", () => {
    const s = { name: "x", labels: "id=core.api,machine=n1", kind: "gauge" as const, points: [] };
    expect(seriesLabel(s, { n1: "Attic" })).toBe("core.api · Attic");
    expect(seriesLabel(s)).toBe("core.api · n1");
    expect(seriesLabel({ ...s, labels: undefined })).toBe("");
  });

  it("keeps only text names from the answer, and none from an older home", async () => {
    get.mockResolvedValueOnce({ board: "overview", series: [], names: { a: "A", b: 3, c: null } });
    expect((await getBoard("overview", "1h")).names).toEqual({ a: "A" });
    get.mockResolvedValueOnce({ board: "overview", series: [], names: ["a"] });
    expect((await getBoard("overview", "1h")).names).toEqual({});
    get.mockResolvedValueOnce({ board: "overview", series: [] });
    expect((await getBoard("overview", "1h")).names).toEqual({});
  });
});
