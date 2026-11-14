// Story 54.44 (review): a rule's plugin subject is an installed plugin. The picker asks the
// home for the installed plugins only (no release=true), and drops any entry that says it
// came with Maktaba: a rule on one would do nothing (the home looks a rule's plugin up in
// its installed plugins).
import { describe, it, expect, vi, beforeEach } from "vitest";

const { get } = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get } };
});

import { loadOptions } from "./subjects";

describe("the rule picker's plugins", () => {
  beforeEach(() => get.mockReset());

  it("lists installed plugins only, by name, and never one that came with Maktaba", async () => {
    get.mockResolvedValue({
      plugins: [
        {
          id: "dev.example.tagger",
          names: { name: { en: "Tagger", ar: "الواسم" } },
          source: "file",
        },
        { id: "dev.example.old", names: { name: { en: "Old home's answer" } } },
        {
          id: "tv.maktaba.thumbnails",
          names: { name: { en: "Thumbnails", ar: "الصور المصغّرة" } },
          source: "release",
        },
      ],
      policy: {},
    });
    const en = await loadOptions("plugin", { locale: "en", query: "" } as never);
    expect(get).toHaveBeenCalledWith("/api/plugins");
    expect(en.map((o) => o.id)).toEqual(["dev.example.tagger", "dev.example.old"]);
    expect(en.map((o) => o.name)).toEqual(["Tagger", "Old home's answer"]);
    const ar = await loadOptions("plugin", { locale: "ar", query: "" } as never);
    expect(ar.map((o) => o.name)).toEqual(["الواسم", "Old home's answer"]);
    expect(ar.some((o) => o.id.startsWith("tv.maktaba."))).toBe(false);
  });
});
