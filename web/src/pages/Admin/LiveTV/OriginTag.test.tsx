// The owner's rule of 2026-10-28, "only manual needs approval": a change says who made it
// (an automatic one goes on by its rule; a manual one waits for an admin).
import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { I18nProvider } from "../../../lib/i18n";
import { OriginTag } from "./changeBits";
import { readyChange } from "./changeFixtures";

describe("OriginTag", () => {
  it("says an automatic change is automatic", () => {
    render(
      <I18nProvider>
        <OriginTag change={{ ...readyChange, origin: "auto" }} />
      </I18nProvider>
    );
    expect(screen.getByTestId("change-origin").textContent).toBe("automatic");
  });

  it("says a manual change (or one from before the column) waits for an admin", () => {
    for (const origin of ["manual", undefined] as const) {
      const { unmount } = render(
        <I18nProvider>
          <OriginTag change={{ ...readyChange, origin }} />
        </I18nProvider>
      );
      expect(screen.getByTestId("change-origin").textContent).toBe("manual: an admin approves");
      unmount();
    }
  });
});
