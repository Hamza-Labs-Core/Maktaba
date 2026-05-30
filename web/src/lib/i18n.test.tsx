// Story 11.12 — i18n: catalog-backed t(), {var} interpolation, missing
// -key returns the key (+dev warn), Arabic-Indic numerals via Intl,
// <html dir> stamping, bidi isolate helper.
import { describe, it, expect } from "vitest";
import { render, screen, act } from "@testing-library/react";
import { I18nProvider, useI18n, bidiIsolate } from "./i18n";

function Probe() {
  const { t, n, dir, setLocale } = useI18n();
  return (
    <div>
      <span data-testid="known">{t("nav.library")}</span>
      <span data-testid="missing">{t("totally.absent.key")}</span>
      <span data-testid="interp">{t("settings.signedInAs")}</span>
      <span data-testid="num">{n(1234)}</span>
      <span data-testid="dir">{dir}</span>
      <button onClick={() => setLocale("ar")}>ar</button>
    </div>
  );
}

describe("i18n", () => {
  it("resolves known keys and returns the key itself on a miss", () => {
    render(
      <I18nProvider>
        <Probe />
      </I18nProvider>
    );
    expect(screen.getByTestId("known")).toHaveTextContent("Library");
    expect(screen.getByTestId("missing")).toHaveTextContent("totally.absent.key");
  });

  it("switches to Arabic: rtl dir + Arabic-Indic digits", () => {
    render(
      <I18nProvider>
        <Probe />
      </I18nProvider>
    );
    expect(screen.getByTestId("dir")).toHaveTextContent("ltr");
    expect(screen.getByTestId("num")).toHaveTextContent("1,234");
    act(() => {
      screen.getByText("ar").click();
    });
    expect(screen.getByTestId("dir")).toHaveTextContent("rtl");
    expect(document.documentElement.dir).toBe("rtl");
    // ar-EG formats with Arabic-Indic digits.
    expect(screen.getByTestId("num").textContent).toMatch(/[٠-٩]/);
  });

  it("bidiIsolate wraps a string in FSI…PDI", () => {
    const wrapped = bidiIsolate("علي");
    expect(wrapped.codePointAt(0)).toBe(0x2068);
    expect(wrapped.codePointAt(wrapped.length - 1)).toBe(0x2069);
  });
});
