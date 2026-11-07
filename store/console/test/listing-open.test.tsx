// #757: the listing page's "open at my Maktaba's address" script, in a browser (jsdom): a bad
// address shows the error and stores nothing; a good one is remembered (its origin only, in
// this browser) and the page goes to the home's Settings → Plugins → Browse on the plugin.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { OPEN_SCRIPT } from "../../web/open-script";

function mount(plugin: string): { input: HTMLInputElement; form: HTMLFormElement; bad: HTMLElement } {
  document.body.innerHTML = `<form id="home-open" data-plugin="${plugin}"><input id="home-addr"><button type="submit">Open</button><span id="home-bad" hidden></span></form>`;
  // eslint-disable-next-line no-new-func
  new Function(OPEN_SCRIPT)();
  return {
    input: document.getElementById("home-addr") as HTMLInputElement,
    form: document.getElementById("home-open") as HTMLFormElement,
    bad: document.getElementById("home-bad") as HTMLElement,
  };
}

// an in-memory localStorage (Node's own global shadows jsdom's here)
beforeEach(() => {
  const m = new Map<string, string>();
  vi.stubGlobal("localStorage", {
    getItem: (k: string) => m.get(k) ?? null,
    setItem: (k: string, v: string) => void m.set(k, String(v)),
    removeItem: (k: string) => void m.delete(k),
    clear: () => m.clear(),
  });
});
afterEach(() => {
  vi.unstubAllGlobals();
  document.body.innerHTML = "";
});

describe("open at my Maktaba's address (#757)", () => {
  it("refuses an address that is not http(s) and remembers nothing", () => {
    const { input, form, bad } = mount("dev.example.tagger");
    for (const v of ["javascript:alert(1)", "file:///etc", "not a url"]) {
      input.value = v;
      form.dispatchEvent(new Event("submit", { cancelable: true }));
      expect(bad.hidden).toBe(false);
    }
    expect(localStorage.getItem("maktaba.home")).toBeNull();
  });

  it("remembers the home's origin only, and fills it in next time", () => {
    const { input, form } = mount("dev.example.tagger");
    input.value = "http://192.168.1.10:8096/some/page?x=1";
    form.dispatchEvent(new Event("submit", { cancelable: true }));
    expect(localStorage.getItem("maktaba.home")).toBe("http://192.168.1.10:8096");
    const again = mount("dev.example.other");
    expect(again.input.value).toBe("http://192.168.1.10:8096");
  });

  it("goes to the home's Browse on the plugin, the id encoded", () => {
    expect(OPEN_SCRIPT).toContain('u.origin+"/settings/plugins?store="+encodeURIComponent(f.getAttribute("data-plugin"))');
  });
});
