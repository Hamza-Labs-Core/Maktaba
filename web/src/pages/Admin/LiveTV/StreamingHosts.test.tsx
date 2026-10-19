// Streaming hosts (story 32.5): one card per machine; the switch, drain and
// move-now send PUT /api/tv/cluster/hosts/{id}; the home cannot be switched
// off; the cluster settings write PATCH /api/rules/tv_cluster; a two_homes
// notice is a red banner with both names; Arabic renders right to left.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "../../../lib/i18n";

const { put, patch } = vi.hoisted(() => ({ put: vi.fn(), patch: vi.fn() }));
vi.mock("../../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../../lib/api")>("../../../lib/api");
  return { ...actual, api: { ...actual.api, put, patch } };
});

import { StreamingHosts } from "./StreamingHosts";
import { clusterView } from "./fixtures";
import type { TvClusterView } from "../../../lib/tvClusterApi";

function renderHosts(view: TvClusterView = clusterView(), onChanged = vi.fn()) {
  render(
    <I18nProvider>
      <StreamingHosts view={view} onChanged={onChanged} />
    </I18nProvider>
  );
  return onChanged;
}

const card = (name: string) => screen.getByRole("article", { name });

describe("StreamingHosts", () => {
  beforeEach(() => {
    localStorage.clear();
    put.mockReset();
    patch.mockReset();
    put.mockResolvedValue({});
    patch.mockResolvedValue({ section: "tv_cluster", changed: [] });
  });

  it("shows a card per host with its state, load and why it is not taking channels", () => {
    renderHosts();
    expect(screen.getAllByRole("article")).toHaveLength(4);
    const home = card("Mac-Studio");
    expect(within(home).getByText("Home")).toBeInTheDocument();
    expect(within(home).getByText("5 of 16")).toBeInTheDocument();
    expect(within(home).getByText("videotoolbox, software")).toBeInTheDocument();
    expect(within(home).getByText("31%")).toBeInTheDocument();
    expect(within(home).getByText("RTMPS")).toBeInTheDocument();
    const laptop = card("Old-Laptop");
    expect(within(laptop).getByText("Down")).toBeInTheDocument();
    expect(
      within(laptop).getByText("Not taking channels: no heartbeat for 40 s")
    ).toBeInTheDocument();
  });

  it("switches a machine off with PUT {enabled}", async () => {
    const user = userEvent.setup();
    const onChanged = renderHosts();
    const sw = within(card("Gaming-PC")).getByRole("switch", { name: "Stream on this machine" });
    expect(sw).toBeChecked();
    await user.click(sw);
    expect(put).toHaveBeenCalledWith("/api/tv/cluster/hosts/h-pc", { enabled: false });
    expect(
      await within(card("Gaming-PC")).findByText("This machine no longer takes channels.")
    ).toBeInTheDocument();
    expect(onChanged).toHaveBeenCalled();
  });

  it("cannot switch the home off", () => {
    renderHosts();
    const home = card("Mac-Studio");
    const sw = within(home).getByRole("switch", { name: "Stream on this machine" });
    expect(sw).toBeDisabled();
    expect(sw).toBeChecked();
    expect(within(home).getByText("The home always streams.")).toBeInTheDocument();
    // the home has no editable stream address
    expect(within(home).queryByLabelText(/Stream base URL/)).not.toBeInTheDocument();
  });

  it("drains with PUT {drain:true} and offers Move now only while draining", async () => {
    const user = userEvent.setup();
    renderHosts();
    const pc = card("Gaming-PC");
    expect(within(pc).queryByRole("button", { name: "Move now" })).not.toBeInTheDocument();
    await user.click(within(pc).getByRole("button", { name: "Drain" }));
    expect(put).toHaveBeenCalledWith("/api/tv/cluster/hosts/h-pc", { drain: true });

    const nas = card("NAS");
    await user.click(within(nas).getByRole("button", { name: "Move now" }));
    expect(put).toHaveBeenCalledTimes(1); // asks first
    const dialog = await screen.findByRole("dialog");
    expect(dialog).toHaveTextContent("will be interrupted");
    await user.click(within(dialog).getByRole("button", { name: "Move now" }));
    expect(put).toHaveBeenLastCalledWith("/api/tv/cluster/hosts/h-nas", { move_now: true });

    await user.click(within(nas).getByRole("button", { name: "Stop draining" }));
    expect(put).toHaveBeenLastCalledWith("/api/tv/cluster/hosts/h-nas", { drain: false });
  });

  it("saves the capacity, weight and stream address of a machine", async () => {
    const user = userEvent.setup();
    renderHosts();
    const pc = card("Gaming-PC");
    const save = within(pc).getByRole("button", { name: "Save" });
    expect(save).toBeDisabled();
    const cap = within(pc).getByLabelText(/^Capacity/);
    await user.type(cap, "80");
    expect(within(pc).getByText(/A whole number from 0 to 64/)).toBeInTheDocument();
    expect(save).toBeDisabled();
    await user.clear(cap);
    await user.type(cap, "6");
    const weight = within(pc).getByLabelText(/^Weight/);
    await user.clear(weight);
    await user.type(weight, "3");
    await user.type(within(pc).getByLabelText(/^Stream base URL/), "https://pc.lan:8081");
    await user.click(save);
    expect(put).toHaveBeenCalledWith("/api/tv/cluster/hosts/h-pc", {
      capacity_override: 6,
      weight: 3,
      stream_base_url: "https://pc.lan:8081",
    });
  });

  it("shows the refusal when the server says no", async () => {
    const user = userEvent.setup();
    const { ApiError } = await import("../../../lib/api");
    put.mockRejectedValueOnce(
      new ApiError({
        type: "about:blank",
        title: "Conflict",
        status: 409,
        error: "the home cannot be disabled",
      } as never)
    );
    renderHosts();
    await user.click(within(card("Gaming-PC")).getByRole("switch"));
    expect(await screen.findByRole("alert")).toHaveTextContent("the home cannot be disabled");
  });

  it("writes the cluster settings through the rules", async () => {
    const user = userEvent.setup();
    renderHosts();
    await user.click(screen.getByRole("switch", { name: "Other machines may stream" }));
    expect(patch).toHaveBeenCalledWith("/api/rules/tv_cluster", {
      set: { nodes_stream: false },
      reset: [],
    });
    await user.selectOptions(screen.getByLabelText("Where channels go"), "balanced");
    expect(patch).toHaveBeenLastCalledWith("/api/rules/tv_cluster", {
      set: { policy: "balanced" },
      reset: [],
    });
    const spill = screen.getByLabelText(/^Home full at/);
    await user.clear(spill);
    await user.type(spill, "75");
    await user.click(screen.getAllByRole("button", { name: "Save" })[0]);
    expect(patch).toHaveBeenLastCalledWith("/api/rules/tv_cluster", {
      set: { spill_at: 75 },
      reset: [],
    });
  });

  it("shows a red banner with both names when two machines say they are the home", () => {
    renderHosts(
      clusterView({
        notices: [
          { kind: "two_homes", message: "two homes", hosts: ["Mac-Studio", "Mac-Mini"] },
          { kind: "no_room", message: "no host has room for Seerah" },
        ],
      })
    );
    const banner = screen.getByRole("alert");
    expect(banner).toHaveTextContent("Mac-Studio and Mac-Mini both say they are the home");
    expect(screen.getByText("No room")).toBeInTheDocument();
    expect(screen.getByText(/no host has room for Seerah/)).toBeInTheDocument();
  });

  it("renders in Arabic", () => {
    localStorage.setItem("mkt:locale", "ar");
    renderHosts();
    expect(screen.getByRole("heading", { level: 2, name: "أجهزة البث" })).toBeInTheDocument();
    expect(
      within(card("Gaming-PC")).getByRole("switch", { name: "البث على هذا الجهاز" })
    ).toBeInTheDocument();
    expect(within(card("Gaming-PC")).getByRole("button", { name: "تفريغ" })).toBeInTheDocument();
    expect(within(card("Mac-Studio")).getByText("الجهاز الرئيسي يبثّ دائمًا.")).toBeInTheDocument();
    expect(within(card("Old-Laptop")).getByText("متوقف")).toBeInTheDocument();
    expect(document.documentElement.dir).toBe("rtl");
  });
});
