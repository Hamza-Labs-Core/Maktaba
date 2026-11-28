// Story 11.12 — i18n: catalog-backed t(), {var} interpolation, missing
// -key returns the key (+dev warn), Western numerals in Arabic via Intl,
// <html dir> stamping, bidi isolate helper.
import { describe, it, expect } from "vitest";
import { render, screen, act } from "@testing-library/react";
import { Modal } from "@ds/components/Modal/Modal";
import { Drawer } from "@ds/components/Drawer/Drawer";
import { I18nProvider, useI18n, bidiIsolate } from "./i18n";

function Probe() {
  const { t, n, formatDate, dir, setLocale } = useI18n();
  return (
    <div>
      <span data-testid="known">{t("nav.library")}</span>
      <span data-testid="missing">{t("totally.absent.key")}</span>
      <span data-testid="interp">{t("settings.signedInAs")}</span>
      <span data-testid="num">{n(1234)}</span>
      <span data-testid="date">
        {formatDate("2026-11-06T21:05:00Z", {
          dateStyle: "medium",
          timeStyle: "short",
          timeZone: "UTC",
        })}
      </span>
      <span data-testid="dir">{dir}</span>
      <button onClick={() => setLocale("ar")}>ar</button>
      <button onClick={() => setLocale("ar", { keep: false })}>ar-visit</button>
    </div>
  );
}

describe("i18n", () => {
  // story 13.21: a language a link asks for is this visit's only
  it("keeps a chosen language, and not one asked for this visit only", () => {
    localStorage.setItem("mkt:locale", "en");
    const { unmount } = render(
      <I18nProvider>
        <Probe />
      </I18nProvider>
    );
    act(() => screen.getByText("ar-visit").click());
    expect(screen.getByTestId("dir")).toHaveTextContent("rtl");
    expect(localStorage.getItem("mkt:locale")).toBe("en");
    act(() => screen.getByText("ar").click());
    expect(localStorage.getItem("mkt:locale")).toBe("ar");
    unmount();
    localStorage.clear();
  });

  it("resolves known keys and returns the key itself on a miss", () => {
    render(
      <I18nProvider>
        <Probe />
      </I18nProvider>
    );
    expect(screen.getByTestId("known")).toHaveTextContent("Library");
    expect(screen.getByTestId("missing")).toHaveTextContent("totally.absent.key");
  });

  it("switches to Arabic: rtl dir + Western digits", () => {
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
    // Arabic keeps Western digits (0-9) across numbers and dates (#1199).
    expect(screen.getByTestId("num").textContent).toBe("1,234");
    expect(screen.getByTestId("num").textContent).not.toMatch(/[٠-٩]/);
    expect(screen.getByTestId("date").textContent).toMatch(/[0-9]/);
    expect(screen.getByTestId("date").textContent).not.toMatch(/[٠-٩]/);
  });

  it("a dialog's and a drawer's close button speak the page's language", () => {
    render(
      <I18nProvider>
        <Probe />
        <Modal open onClose={() => {}} title="m">
          body
        </Modal>
        <Drawer open onClose={() => {}} title="d">
          body
        </Drawer>
      </I18nProvider>
    );
    expect(screen.getAllByRole("button", { name: "Close", hidden: true })).toHaveLength(2);
    act(() => {
      screen.getByText("ar").click();
    });
    expect(screen.getAllByRole("button", { name: "إغلاق", hidden: true })).toHaveLength(2);
    expect(screen.queryAllByRole("button", { name: "Close", hidden: true })).toHaveLength(0);
  });

  it("bidiIsolate wraps a string in FSI…PDI", () => {
    const wrapped = bidiIsolate("علي");
    expect(wrapped.codePointAt(0)).toBe(0x2068);
    expect(wrapped.codePointAt(wrapped.length - 1)).toBe(0x2069);
  });
});
