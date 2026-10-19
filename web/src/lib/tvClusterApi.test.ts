// The cluster view the admin Live TV page renders: lists the API sends as null
// (a home with no streaming host yet) read as empty, so the page never calls
// .filter on null and goes blank.
import { describe, it, expect, vi } from "vitest";

const { get } = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock("./api", async () => {
  const actual = await vi.importActual<typeof import("./api")>("./api");
  return { ...actual, api: { ...actual.api, get } };
});

import { tvClusterApi } from "./tvClusterApi";

describe("tvClusterApi.get", () => {
  it("reads null or missing lists as empty", async () => {
    get.mockResolvedValue({ settings: { policy: "home_first" }, hosts: null, failovers_24h: 0 });
    const v = await tvClusterApi.get();
    expect(get).toHaveBeenCalledWith("/api/tv/cluster");
    expect(v.hosts).toEqual([]);
    expect(v.on_air).toEqual([]);
    expect(v.notices).toEqual([]);
    expect(v.failovers_24h).toBe(0);
  });

  it("keeps lists that are there", async () => {
    get.mockResolvedValue({ settings: {}, hosts: [{ host_id: "h" }], on_air: [], notices: [] });
    expect((await tvClusterApi.get()).hosts).toEqual([{ host_id: "h" }]);
  });
});
