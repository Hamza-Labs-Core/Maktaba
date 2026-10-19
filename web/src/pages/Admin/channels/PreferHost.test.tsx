// "Prefer host" on the channel page (story 32.5): Automatic + the cluster's
// host names; a choice writes tv_cluster.pins[slug] and keeps the other pins.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ToastProvider } from "@ds/components/Toast/Toast";
import { I18nProvider } from "../../../lib/i18n";

const { get, patch } = vi.hoisted(() => ({ get: vi.fn(), patch: vi.fn() }));
vi.mock("../../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../../lib/api")>("../../../lib/api");
  return { ...actual, api: { ...actual.api, get, patch } };
});

import { PreferHost } from "./PreferHost";
import { clusterView } from "../LiveTV/fixtures";

const renderPin = (slug = "seerah") =>
  render(
    <I18nProvider>
      <ToastProvider>
        <PreferHost slug={slug} />
      </ToastProvider>
    </I18nProvider>
  );

describe("PreferHost", () => {
  beforeEach(() => {
    localStorage.clear();
    get.mockReset();
    patch.mockReset();
    patch.mockResolvedValue({ section: "tv_cluster", changed: ["pins"] });
  });

  it("pins the channel to a host and back to automatic, keeping the other pins", async () => {
    const user = userEvent.setup();
    const v = clusterView();
    v.settings.pins = { fiqh: ["NAS"] };
    get.mockResolvedValue(v);
    renderPin();
    const select = await screen.findByLabelText("Prefer host");
    expect(select).toHaveValue("");
    await user.selectOptions(select, "Gaming-PC");
    expect(patch).toHaveBeenCalledWith("/api/rules/tv_cluster", {
      set: { pins: { fiqh: ["NAS"], seerah: ["Gaming-PC"] } },
      reset: [],
    });
    await waitFor(() => expect(select).toHaveValue("Gaming-PC"));
    await user.selectOptions(select, "");
    expect(patch).toHaveBeenLastCalledWith("/api/rules/tv_cluster", {
      set: { pins: { fiqh: ["NAS"] } },
      reset: [],
    });
  });

  it("is hidden when the cluster view does not load", async () => {
    get.mockRejectedValue(new Error("forbidden"));
    const { container } = renderPin();
    await waitFor(() => expect(get).toHaveBeenCalled());
    expect(container).toBeEmptyDOMElement();
  });
});
