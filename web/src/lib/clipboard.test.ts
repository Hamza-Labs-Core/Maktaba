// copyText: the async clipboard in a secure context, else the selection of a
// read-only field through execCommand("copy") (plain-http LAN origins).
import { describe, it, expect, vi, afterEach } from "vitest";
import { copyText } from "./clipboard";

const exec0 = document.execCommand;

function setEnv(secure: boolean, clipboard: unknown, exec: unknown) {
  Object.defineProperty(window, "isSecureContext", { value: secure, configurable: true });
  Object.defineProperty(navigator, "clipboard", { value: clipboard, configurable: true });
  Object.defineProperty(document, "execCommand", { value: exec, configurable: true });
}

describe("copyText", () => {
  afterEach(() => setEnv(true, undefined, exec0));

  it("uses the clipboard API in a secure context", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    const exec = vi.fn();
    setEnv(true, { writeText }, exec);
    expect(await copyText("http://a/xmltv.xml")).toBe(true);
    expect(writeText).toHaveBeenCalledWith("http://a/xmltv.xml");
    expect(exec).not.toHaveBeenCalled();
  });

  it("falls back to selecting the field on plain http", async () => {
    const exec = vi.fn().mockReturnValue(true);
    setEnv(false, undefined, exec);
    const field = document.createElement("input");
    field.value = "http://192.168.1.20:8782/xmltv.xml";
    document.body.appendChild(field);
    expect(await copyText(field.value, field)).toBe(true);
    expect(exec).toHaveBeenCalledWith("copy");
    expect(document.activeElement).toBe(field);
    expect(field.selectionStart).toBe(0);
    expect(field.selectionEnd).toBe(field.value.length);
    field.remove();
  });

  it("falls back when the clipboard API refuses, using a hidden field when none is given", async () => {
    const writeText = vi.fn().mockRejectedValue(new Error("denied"));
    let copied = "";
    const exec = vi.fn(() => {
      copied = (document.activeElement as HTMLTextAreaElement).value;
      return true;
    });
    setEnv(true, { writeText }, exec);
    expect(await copyText("hello")).toBe(true);
    expect(copied).toBe("hello");
    expect(document.querySelectorAll("textarea")).toHaveLength(0); // cleaned up
  });

  it("reports failure when nothing can copy", async () => {
    setEnv(false, undefined, vi.fn().mockReturnValue(false));
    expect(await copyText("x")).toBe(false);
    setEnv(
      false,
      undefined,
      vi.fn(() => {
        throw new Error("no");
      })
    );
    expect(await copyText("x")).toBe(false);
  });
});
