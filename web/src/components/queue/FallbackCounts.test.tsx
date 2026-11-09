// Story 54.43 T06: a first-party stage's in-process fallbacks on the Queue detail page, with
// and without rows, in English and Arabic.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, within } from "@testing-library/react";
import { I18nProvider, lookupMessage } from "../../lib/i18n";
import { FALLBACK_REASONS } from "../../lib/queueApi";

const { get } = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get } };
});

import { FallbackCounts } from "./FallbackCounts";

const rows = {
  fallbacks: [
    {
      machine: "00000000-0000-0000-0000-0000000000d1",
      machine_name: "Den PC",
      stage: "probe",
      reason: "fingerprint_inproc",
      count: 2,
      first_at: new Date(Date.now() - 2 * 3600_000).toISOString(),
      last_at: new Date(Date.now() - 3600_000).toISOString(),
    },
    {
      machine: "home",
      machine_name: null,
      stage: "probe",
      reason: "runner_not_serving_here",
      count: 1234,
      first_at: new Date(Date.now() - 3 * 3600_000).toISOString(),
      last_at: new Date(Date.now() - 2 * 3600_000).toISOString(),
    },
  ],
  total: 1236,
};

function show() {
  return render(
    <I18nProvider>
      <FallbackCounts stage="probe" />
    </I18nProvider>
  );
}

describe("FallbackCounts", () => {
  beforeEach(() => {
    localStorage.clear();
    get.mockReset();
  });

  it("lists each reason, machine, count and last time, asking for the stage", async () => {
    get.mockResolvedValue(rows);
    show();
    const box = await screen.findByTestId("queue-fallbacks");
    expect(get).toHaveBeenCalledWith("/api/queues/fallbacks?stage=probe");
    expect(within(box).getByRole("heading", { name: "Ran in the worker instead" })).toBeVisible();
    expect(within(box).getByText("The fingerprint runner did not answer")).toBeVisible();
    expect(within(box).getByText("Den PC")).toBeVisible();
    expect(within(box).getByText("This home")).toBeVisible();
    expect(within(box).getByText("Its runner was not serving on this machine")).toBeVisible();
    expect(within(box).getByText("1,234")).toBeVisible();
    expect(within(box).getByText("1 h ago")).toBeVisible();
  });

  it("says when none were counted", async () => {
    get.mockResolvedValue({ fallbacks: [], total: 0 });
    show();
    expect(await screen.findByText("No in-process fallbacks counted.")).toBeVisible();
  });

  it("shows it in Arabic", async () => {
    localStorage.setItem("mkt:locale", "ar");
    get.mockResolvedValue(rows);
    show();
    const box = await screen.findByTestId("queue-fallbacks");
    expect(within(box).getByText("عمل في العامل بدلًا من ذلك")).toBeVisible();
    expect(within(box).getByText("لم يُجب مشغّل البصمات")).toBeVisible();
    expect(within(box).getByText("هذا الجهاز الرئيسي")).toBeVisible();
  });

  it("shows nothing when the read fails", async () => {
    get.mockRejectedValue(new Error("403"));
    const { container } = show();
    await Promise.resolve();
    expect(container.querySelector("[data-testid=queue-fallbacks]")).toBeNull();
  });

  it("has every reason in both languages", () => {
    for (const r of FALLBACK_REASONS) {
      const key = `queue.fallbacks.why.${r}`;
      expect(lookupMessage("en", key), key).toBeTruthy();
      expect(lookupMessage("ar", key), key).toMatch(/[؀-ۿ]/);
    }
  });
});
