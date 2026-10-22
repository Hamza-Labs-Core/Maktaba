// Epic 37 — a standby home taking over when its home is gone for good: shown only on a
// standby, offered only when it can (why not, otherwise), and only after the home's
// name is typed to confirm.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { I18nProvider } from "../../lib/i18n";
import type { EmergencyState } from "../../lib/clusterApi";
import { ToastProvider } from "@ds/components/Toast/Toast";
import { axeViolations } from "../../test/axe";

const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post } };
});

import { EmergencyCard } from "./EmergencyCard";

const state = (extra: Partial<EmergencyState> = {}): EmergencyState => ({
  standby: true,
  kit_term: 1,
  kit_made_at: "2026-10-21T09:00:00Z",
  home: "Living Room PC",
  home_answers: false,
  copy: { state: "behind", applied_at: "2026-10-21T08:59:00Z" },
  ready: true,
  ...extra,
});

const renderCard = () =>
  render(
    <I18nProvider>
      <ToastProvider>
        <EmergencyCard />
      </ToastProvider>
    </I18nProvider>
  );

describe("Taking over as the home", () => {
  beforeEach(() => {
    localStorage.clear();
    get.mockReset();
    post.mockReset();
  });

  it("is not shown on a machine that is not a standby home (or on a home)", async () => {
    get.mockRejectedValue(new Error("404"));
    const { container } = renderCard();
    await act(async () => {});
    expect(get).toHaveBeenCalledWith("/api/cluster/emergency");
    expect(container).toBeEmptyDOMElement();
  });

  it("says why it cannot take over while the home answers", async () => {
    get.mockResolvedValue(state({ ready: false, home_answers: true, reasons: ["home_answers"] }));
    renderCard();
    expect(await screen.findByText(/is answering, so switch the home/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Make this machine the home…" })).toBeDisabled();
  });

  it("takes over only once the home's name is typed", async () => {
    get.mockResolvedValue(state());
    post.mockResolvedValue({ accepted: true, id: "sw-e" });
    const { container } = renderCard();
    expect(await screen.findByText(/This machine is a standby home/)).toBeInTheDocument();
    expect(screen.getByText(/keys is from/)).toBeInTheDocument();
    expect(await axeViolations(container)).toEqual([]);
    fireEvent.click(screen.getByRole("button", { name: "Make this machine the home…" }));
    const dialog = await screen.findByRole("dialog");
    const go = within(dialog).getByRole("button", { name: "Take over" });
    expect(go).toBeDisabled();
    fireEvent.change(within(dialog).getByRole("textbox"), { target: { value: "living room pc" } });
    expect(go).toBeEnabled();
    fireEvent.click(go);
    await act(async () => {});
    expect(post).toHaveBeenCalledWith("/api/cluster/emergency", { confirm: "living room pc" });
    expect(await screen.findByText(/is taking over as the home/)).toBeInTheDocument();
  });
});
