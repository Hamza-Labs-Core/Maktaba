// Epic 37 — switching the home on the Machines tab: the term and the home, the
// machines that could take over (and why one cannot), the confirm step, a switch
// under way, and the last one's machines and outside services.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { I18nProvider } from "../../lib/i18n";
import type { SwitchProgress, SwitchStatus } from "../../lib/clusterApi";
import { ToastProvider } from "@ds/components/Toast/Toast";
import { axeViolations } from "../../test/axe";

const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post } };
});

import { SWITCH_POLL_MS, SwitchHomeCard } from "./SwitchHomeCard";

const term = {
  term: 1,
  home_name: "Living Room PC",
  reason: "initial",
  started_at: "2026-10-01T10:00:00Z",
};

const status = (extra: Partial<SwitchStatus> = {}): SwitchStatus => ({
  term,
  current: null,
  recent: null,
  candidates: [
    { node_id: "n-mac", name: "Studio Mac", ready: true, lag_ms: 800 },
    {
      node_id: "n-nas",
      name: "NAS",
      ready: false,
      reasons: ["copy_not_following", "schema_differs"],
    },
  ],
  ...extra,
});

const progress = (extra: Partial<SwitchProgress> = {}): SwitchProgress => ({
  id: "sw1",
  term: 2,
  kind: "planned",
  to_node: "n-mac",
  to_name: "Studio Mac",
  from_name: "Living Room PC",
  state: "frozen",
  steps: [
    { name: "check", state: "done", at: "2026-10-21T10:00:00Z" },
    { name: "drain", state: "done", at: "2026-10-21T10:00:01Z" },
    { name: "freeze", state: "done", at: "2026-10-21T10:00:02Z" },
    { name: "marker", state: "running", at: "2026-10-21T10:00:03Z" },
  ],
  machines: { "n-nas": { name: "NAS", state: "waiting" } },
  ...extra,
});

const renderCard = () =>
  render(
    <I18nProvider>
      <ToastProvider>
        <SwitchHomeCard />
      </ToastProvider>
    </I18nProvider>
  );

describe("Switching the home", () => {
  beforeEach(() => {
    localStorage.clear();
    get.mockReset();
    post.mockReset();
    vi.useFakeTimers({ shouldAdvanceTime: true });
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it("shows the term and the home, and why a machine cannot take over", async () => {
    get.mockResolvedValue(status());
    const { container } = renderCard();
    expect(await screen.findByText("Term 1")).toBeInTheDocument();
    expect(screen.getByText(/is this cluster's home, since it was set up/)).toBeInTheDocument();
    const mac = screen.getByRole("button", { name: /Make .*Studio Mac.* the home/ });
    expect(mac).toBeEnabled();
    const nas = screen.getByRole("button", { name: /Make .*NAS.* the home/ });
    expect(nas).toBeDisabled();
    expect(
      screen.getByText(
        "its copy of the database isn't following · it runs another version of Maktaba"
      )
    ).toBeInTheDocument();
    expect(get).toHaveBeenCalledWith("/api/cluster/switchover");
    expect(await axeViolations(container)).toEqual([]);
  });

  it("asks first, then starts the switch to that machine", async () => {
    get.mockResolvedValue(status());
    post.mockResolvedValue({ id: "sw1" });
    renderCard();
    fireEvent.click(await screen.findByRole("button", { name: /Make .*Studio Mac.* the home/ }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText(/becomes one of its machines/)).toBeInTheDocument();
    expect(post).not.toHaveBeenCalled();
    fireEvent.click(within(dialog).getByRole("button", { name: "Switch the home" }));
    await act(async () => {});
    expect(post).toHaveBeenCalledWith("/api/cluster/switchover", { to_node: "n-mac" });
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("cancelling the question starts nothing", async () => {
    get.mockResolvedValue(status());
    renderCard();
    fireEvent.click(await screen.findByRole("button", { name: /Make .*Studio Mac.* the home/ }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(post).not.toHaveBeenCalled();
  });

  it("follows a switch under way step by step, and hides the buttons", async () => {
    get.mockResolvedValue(status({ current: progress() }));
    renderCard();
    expect(await screen.findByText("changes paused")).toBeInTheDocument();
    const steps = screen.getAllByRole("listitem").filter((li) => li.closest("ol"));
    expect(steps.map((li) => li.getAttribute("data-state"))).toEqual([
      "done",
      "done",
      "done",
      "running",
      "waiting",
      "waiting",
      "waiting",
      "waiting",
    ]);
    expect(screen.getByText(/Its copy has everything/)).toHaveTextContent("under way");
    expect(screen.getByText(/NAS.*: waiting/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Make .* the home/ })).not.toBeInTheDocument();

    // the next poll: the switch ended; the result replaces the steps
    get.mockResolvedValue(
      status({
        term: { ...term, term: 2, home_name: "Studio Mac", reason: "planned" },
        recent: [
          progress({
            state: "done",
            machines: { "n-nas": { name: "NAS", state: "following" } },
            integrations: {
              secrets: { state: "moved", detail: "4" },
              plex: { state: "moved" },
              youtube: {
                state: "needs_you",
                detail: "sign in to YouTube in firefox on this machine",
              },
              cloudflare_stream: { state: "not_used" },
            },
          }),
        ],
      })
    );
    await act(async () => {
      vi.advanceTimersByTime(SWITCH_POLL_MS);
    });
    expect(await screen.findByText("Term 2")).toBeInTheDocument();
    expect(screen.getByText(/Last switch:/)).toBeInTheDocument();
    expect(screen.getByText(/NAS.*: follows the new home/)).toBeInTheDocument();
    expect(screen.getByText("Secrets")).toBeInTheDocument();
    expect(screen.queryByText("Cloudflare Stream")).not.toBeInTheDocument();
    expect(screen.getByRole("note")).toHaveTextContent(
      "Sign in to YouTube in a browser on this machine (sign in to YouTube in firefox on this machine)."
    );
  });

  it("says when switching isn't available", async () => {
    get.mockRejectedValue(new Error("404"));
    renderCard();
    expect(await screen.findByText("Switching the home isn't available here.")).toBeInTheDocument();
  });

  it("has every string in Arabic too", async () => {
    const en = (await import("../../i18n/en.json")).default as Record<string, string>;
    const ar = (await import("../../i18n/ar.json")).default as Record<string, string>;
    const keys = Object.keys(en).filter((k) => k.startsWith("machines.switch."));
    expect(keys.length).toBeGreaterThan(50);
    expect(keys.filter((k) => !ar[k])).toEqual([]);
  });
});
