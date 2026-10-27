import { describe, it, expect } from "vitest";
import { serverPageModules } from "./editionBundle";

describe("serverPageModules (the player build's check)", () => {
  it("names the server page modules that put code in the bundle", () => {
    expect(
      serverPageModules([
        { id: "/w/web/src/pages/Admin/Users.tsx", size: 120 },
        { id: "/w/web/src/pages/queue/QueueOverview.tsx", size: 80 },
        { id: "/w/web/src/pages/Settings.tsx", size: 1 },
        { id: "/w/web/src/pages/Settings/RulesSection.tsx", size: 10 },
        { id: "/w/web/src/pages/transcription/Transcription.tsx", size: 10 },
        { id: "/w/web/src/pages/Cloud/Devices.tsx", size: 10 },
        { id: "/w/web/src/pages/RemoteHome.tsx", size: 10 },
        { id: "/w/web/src/serverRoutes.tsx", size: 10 },
        { id: "C:\\w\\web\\src\\pages\\Admin\\Logs.tsx", size: 10 },
      ])
    ).toEqual([
      "/w/web/src/pages/Admin/Users.tsx",
      "/w/web/src/pages/queue/QueueOverview.tsx",
      "/w/web/src/pages/Settings.tsx",
      "/w/web/src/pages/Settings/RulesSection.tsx",
      "/w/web/src/pages/transcription/Transcription.tsx",
      "/w/web/src/pages/Cloud/Devices.tsx",
      "/w/web/src/pages/RemoteHome.tsx",
      "/w/web/src/serverRoutes.tsx",
      "C:/w/web/src/pages/Admin/Logs.tsx",
    ]);
  });

  it("ignores viewer pages, shared components and tree-shaken modules", () => {
    expect(
      serverPageModules([
        { id: "/w/web/src/pages/Home.tsx", size: 100 },
        { id: "/w/web/src/pages/Profile/ConnectedDevices.tsx", size: 100 },
        { id: "/w/web/src/components/queue/format.ts", size: 100 },
        { id: "/w/web/src/pages/Admin/Machines.tsx", size: 0 },
      ])
    ).toEqual([]);
  });
});
