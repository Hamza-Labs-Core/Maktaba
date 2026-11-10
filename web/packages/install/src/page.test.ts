// The page's form fills the file in the page and sends nothing.
import BASE from "../../../../cloud/installers/docker/docker-compose.yml?raw";
import { afterEach, describe, expect, it, vi } from "vitest";
import { mount } from "./page";

function setValue(root: HTMLElement, sel: string, value: string): void {
  const e = root.querySelector<HTMLInputElement | HTMLSelectElement>(sel)!;
  e.value = value;
  e.dispatchEvent(new Event(e.tagName === "SELECT" ? "change" : "input", { bubbles: true }));
}

describe("the install page", () => {
  afterEach(() => vi.restoreAllMocks());

  it.each(["en", "ar"] as const)("fills the compose file in (%s) without a request", (lang) => {
    const fetchSpy = vi
      .spyOn(globalThis, "fetch")
      .mockImplementation(() => Promise.reject(new Error("no")));
    const root = document.createElement("main");
    mount(root, BASE, lang);
    const out = () => root.querySelector<HTMLTextAreaElement>("#out")!.value;
    expect(out()).toContain('user: "1000:100"');
    expect(out()).toContain("/volume1/docker/maktaba:/var/lib/maktaba");

    setValue(root, "#user", "1026:100");
    setValue(root, "#ram", "4");
    expect(root.querySelector<HTMLSelectElement>("#tier")!.value).toBe("light");
    expect(out()).toContain("mem_limit: 4g");
    expect(out()).toContain('user: "1026:100"');

    setValue(root, "#video", "intel-amd");
    expect(out()).toBe("");
    expect(root.querySelector(".problems")!.textContent).not.toBe("");
    setValue(root, "#render-group", "937");
    expect(out()).toContain("- /dev/dri:/dev/dri");

    setValue(root, "#nas", "qnap");
    expect(out()).toContain("/share/Container/maktaba:/var/lib/maktaba");
    expect(root.querySelector("#nas-hint")!.textContent).toMatch(lang === "en" ? /8080/ : /8080/);
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it("refuses root", () => {
    const root = document.createElement("main");
    mount(root, BASE, "en");
    setValue(root, "#user", "0:0");
    expect(root.querySelector<HTMLTextAreaElement>("#out")!.value).toBe("");
    expect(root.querySelector(".problems")!.textContent).toContain("never runs as root");
  });
});
