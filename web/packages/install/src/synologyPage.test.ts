// Story 59.09: the Synology guide fills the project file in the page and sends nothing.
import BASE from "../../../../cloud/installers/docker/docker-compose.yml?raw";
import { afterEach, describe, expect, it, vi } from "vitest";
import { mount } from "./synologyPage";

function setValue(root: HTMLElement, sel: string, value: string): void {
  const e = root.querySelector<HTMLInputElement | HTMLSelectElement>(sel)!;
  e.value = value;
  e.dispatchEvent(new Event(e.tagName === "SELECT" ? "change" : "input", { bubbles: true }));
}

describe("the Synology guide", () => {
  afterEach(() => vi.restoreAllMocks());

  // Story 60.04: axe's label rule. The file's textarea is named by the heading above it.
  it.each(["en", "ar"] as const)(
    "names the project file's textarea by its heading (%s)",
    (lang) => {
      const root = document.createElement("main");
      document.body.append(root);
      mount(root, BASE, lang);
      const out = root.querySelector<HTMLTextAreaElement>("#out")!;
      const id = out.getAttribute("aria-labelledby")!;
      const heading = document.getElementById(id)!;
      expect(heading.tagName).toBe("H2");
      expect(heading.textContent!.trim()).not.toBe("");
      root.remove();
    }
  );

  it.each(["en", "ar"] as const)("fills the project file in (%s) without a request", (lang) => {
    const fetchSpy = vi
      .spyOn(globalThis, "fetch")
      .mockImplementation(() => Promise.reject(new Error("no")));
    const root = document.createElement("main");
    mount(root, BASE, lang);
    const out = () => root.querySelector<HTMLTextAreaElement>("#out")!.value;
    // the DS425+ (J4125, 6 GB): Quick Sync on, Light
    expect(root.querySelector<HTMLInputElement>("#gpu")!.checked).toBe(true);
    expect(root.querySelector<HTMLSelectElement>("#tier")!.value).toBe("light");
    expect(out()).toContain('user: "1026:100"');
    expect(out()).toContain("mem_limit: 4g");
    expect(out()).toMatch(/^\s+- \/dev\/dri:\/dev\/dri$/m);
    expect(out()).toContain("MAKTABA_PLATFORM: dsm");

    setValue(root, "#model", "DS923+");
    expect(root.querySelector<HTMLInputElement>("#gpu")!.checked).toBe(false);
    expect(out()).not.toMatch(/^\s+- \/dev\/dri:\/dev\/dri$/m);
    setValue(root, "#ram", "32");
    expect(out()).toContain("mem_limit: 16g");

    setValue(root, "#uid", "1030");
    expect(out()).toContain('user: "1030:100"');
    setValue(root, "#volume", "volume2");
    expect(out()).toContain("/volume2/docker/maktaba:/var/lib/maktaba");

    setValue(root, "#uid", "root");
    expect(out()).toBe("");
    expect(root.querySelector(".problems[role=alert]")!.textContent).not.toBe("");

    expect(root.querySelector("#models tbody")!.children.length).toBeGreaterThan(10);
    expect(root.querySelectorAll(".step")).toHaveLength(5);
    expect(root.querySelectorAll("img.shot")).toHaveLength(0);
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it("says a 2 GB model is too small and gives no file", () => {
    const root = document.createElement("main");
    mount(root, BASE, "en");
    setValue(root, "#model", "DS223");
    setValue(root, "#ram", "2");
    expect(root.querySelector("#too-small")!.textContent).toContain("too little memory");
    expect(root.querySelector<HTMLTextAreaElement>("#out")!.value).toBe("");
  });

  it("adds and removes shared folders", () => {
    const root = document.createElement("main");
    mount(root, BASE, "en");
    const rows = () => root.querySelectorAll("#shares .media-row");
    expect(rows()).toHaveLength(2);
    root.querySelector<HTMLButtonElement>("#shares + button")!.click();
    expect(rows()).toHaveLength(3);
    rows()[2]!.querySelector("button")!.click();
    rows()[0]!.querySelector("button")!.click();
    expect(rows()).toHaveLength(1);
    expect(root.querySelector<HTMLTextAreaElement>("#out")!.value).not.toContain("/volume1/Movies");
  });
});
