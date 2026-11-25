import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { I18nProvider } from "../../lib/i18n";
import type { JobRef } from "../../lib/queueApi";
import { RunningJobs } from "./RunningJobs";

const job = {
  id: 1,
  stage: "dedup_scan",
  subject_kind: "library",
  subject_id: "l1",
  subject_label: "Lessons",
  attempts: 1,
  max_attempts: 3,
  progress: {
    step: "full_hash",
    steps: ["fan_out", "full_hash", "warp_drive"],
    done_units: 1,
    total_units: 2,
  },
} as unknown as JobRef;

function steps(locale: "en" | "ar") {
  localStorage.setItem("mkt:locale", locale);
  render(
    <I18nProvider>
      <MemoryRouter>
        <RunningJobs jobs={[job]} />
      </MemoryRouter>
    </I18nProvider>
  );
  return screen
    .getAllByRole("listitem")
    .filter((li) => li.className.startsWith("mkt-q-step"))
    .map((li) => li.textContent!.replace(/^[✓▶·]\s*/, ""));
}

describe("RunningJobs", () => {
  beforeEach(() => localStorage.clear());
  afterEach(() => localStorage.clear());

  it("says a running job's steps in words, an unknown one as the home sent it", () => {
    expect(steps("en")).toEqual(["Share out the work", "Compare whole files", "warp drive"]);
  });

  it("says them in Arabic", () => {
    expect(steps("ar")).toEqual(["توزيع العمل", "مقارنة الملفات كاملة", "warp drive"]);
  });
});
