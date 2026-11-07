// Story 54.20 T04/T05: a panel shows hostile values as plain text, cut, with no request,
// and reads right to left in Arabic.
import { describe, expect, it, vi, afterEach } from "vitest";
import { render } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { I18nProvider } from "../../lib/i18n";
import { readCapabilities } from "../../lib/capabilities";
import { PluginPanel, plain, safeImage, type PanelView } from "./PluginPanel";

const hostile = "https://evil.example/x.png";
const panel: PanelView = {
  plugin_id: "dev.example.loudness",
  plugin: "Loudness",
  title: "<b>Report</b>",
  blocks: [
    {
      type: "kv",
      label: "Levels",
      value: [
        { label: "Link", value: hostile },
        { label: "<img src=x onerror=alert(1)>", value: -14 },
      ],
    },
    { type: "badges", value: [{ text: "‮gnol", tone: "warning" }] },
    { type: "chips", value: ["[click](https://evil.example)", "a".repeat(500)] },
    {
      type: "table",
      columns: ["Start", "What"],
      value: [["00:01", `<script>fetch("${hostile}")</script>`]],
    },
    {
      type: "markers",
      label: "Silences",
      value: [
        { t_ms: 61500, label: "pause" },
        { t_ms: "x", label: "bad" },
      ],
    },
    { type: "image", image: hostile },
    { type: "text", value: "**bold** " + hostile },
  ],
};

function show(p: PanelView, kind: "video" | "series" = "video") {
  return render(
    <MemoryRouter>
      <I18nProvider>
        <PluginPanel panel={p} subject={{ kind, id: "11111111-1111-1111-1111-111111111111" }} />
      </I18nProvider>
    </MemoryRouter>
  );
}

afterEach(() => vi.restoreAllMocks());

describe("PluginPanel", () => {
  it("shows hostile values as plain text and asks nothing of the network", () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch");
    const { container } = show(panel);
    // no element a value could have made: no image, script, iframe, or link out of the app
    expect(container.querySelector("img, script, iframe, object, embed")).toBeNull();
    for (const a of container.querySelectorAll("a"))
      expect(a.getAttribute("href")).toMatch(/^\/(search\?|videos\/)/);
    expect(container.querySelector("b")).toBeNull();
    // the words are there, as text
    expect(container.textContent).toContain(hostile);
    expect(container.textContent).toContain("<img src=x onerror=alert(1)>");
    expect(container.textContent).toContain("**bold**");
    // a direction override is removed, a long chip is cut
    expect(container.textContent).not.toContain("‮");
    expect(container.textContent).toContain("a".repeat(199) + "…");
    // the marker seeks in the app; a marker with no time is left out
    const marks = container.querySelectorAll('a[href*="/watch?t="]');
    expect([...marks].map((a) => a.getAttribute("href"))).toEqual([
      "/videos/11111111-1111-1111-1111-111111111111/watch?t=61",
    ]);
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it("shows the home's panel image only", () => {
    expect(safeImage("/api/panels/image?subject=video:1&plugin=dev.example.x")).not.toBeNull();
    for (const s of [
      hostile,
      "//evil.example/x",
      "/api/panels/image?x=<",
      "javascript:alert(1)",
      3,
    ])
      expect(safeImage(s)).toBeNull();
    const { container } = show({
      ...panel,
      blocks: [{ type: "image", image: "/api/panels/image?subject=video:1&plugin=dev.example.x" }],
    });
    expect(container.querySelector("img")?.getAttribute("src")).toBe(
      "/api/panels/image?subject=video:1&plugin=dev.example.x"
    );
  });

  it("keeps markers to videos", () => {
    const { container } = show({ ...panel, blocks: [panel.blocks[4]!] }, "series");
    expect(container.querySelector("a")).toBeNull();
  });

  it("reads right to left in Arabic: every value carries dir=auto", () => {
    const { container } = show({
      ...panel,
      title: "علو الصوت",
      blocks: [{ type: "text", value: "الحلقة الثالثة" }],
    });
    expect(container.querySelector("h3")?.getAttribute("dir")).toBe("auto");
    expect(container.querySelector("p")?.getAttribute("dir")).toBe("auto");
  });

  it("plain keeps strings and numbers only", () => {
    expect(plain({ toString: () => "x" })).toBe("");
    expect(plain(NaN)).toBe("");
    expect(plain(3.5)).toBe("3.5");
    expect(plain("a\u0000b⁦c")).toBe("abc");
  });

  it("reads plugin_panels missing as off", () => {
    expect(readCapabilities({ features: { live_tv: "on" } })?.panels).toBe(false);
    expect(readCapabilities({ features: { plugin_panels: "off" } })?.panels).toBe(false);
    expect(readCapabilities({ features: { plugin_panels: "on" } })?.panels).toBe(true);
  });
});
