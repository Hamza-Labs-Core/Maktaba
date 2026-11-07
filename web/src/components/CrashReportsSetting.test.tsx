// Epic 53 story 53.14: the one-time question has two equal answers and goes once answered;
// the Privacy switch starts off and turns crash reports on and off.
import { describe, it, expect, beforeEach } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { I18nProvider } from "../lib/i18n";
import { crashReportsOn } from "../lib/crashReports";
import { CrashReportsQuestion, CrashReportsSetting } from "./CrashReportsSetting";
import en from "../i18n/en.json";

describe("crash reports consent", () => {
  beforeEach(() => localStorage.clear());

  it("asks once, neither answer chosen for the person", () => {
    render(
      <I18nProvider>
        <CrashReportsQuestion />
        <CrashReportsSetting />
      </I18nProvider>
    );
    const yes = screen.getByRole("button", { name: en["crash.onboarding.yes"] });
    const no = screen.getByRole("button", { name: en["crash.onboarding.no"] });
    expect(yes.className).toBe(no.className);
    expect(screen.getByRole("checkbox")).not.toBeChecked();
    fireEvent.click(no);
    expect(screen.queryByTestId("crash-ask")).toBeNull();
    expect(crashReportsOn()).toBe(false);
    fireEvent.click(screen.getByRole("checkbox"));
    expect(crashReportsOn()).toBe(true);
    expect(screen.getByRole("checkbox")).toBeChecked();
  });
});
