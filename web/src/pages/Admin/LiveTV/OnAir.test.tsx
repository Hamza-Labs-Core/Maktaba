// On air (story 32.5): the channels encoding now with their host, encoder and
// viewers; Move opens a host picker (online, not draining, or Automatic) and
// POSTs the move; Stop asks first, then DELETEs the placement; Arabic renders.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "../../../lib/i18n";

const { post, del } = vi.hoisted(() => ({ post: vi.fn(), del: vi.fn() }));
vi.mock("../../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../../lib/api")>("../../../lib/api");
  return { ...actual, api: { ...actual.api, post, delete: del } };
});

import { OnAir } from "./OnAir";
import { clusterView } from "./fixtures";

function renderOnAir(onChanged = vi.fn()) {
  render(
    <I18nProvider>
      <OnAir view={clusterView()} onChanged={onChanged} />
    </I18nProvider>
  );
  return onChanged;
}

const row = (name: string) => screen.getByRole("cell", { name }).closest("tr") as HTMLElement;

describe("OnAir", () => {
  beforeEach(() => {
    localStorage.clear();
    post.mockReset();
    del.mockReset();
    post.mockResolvedValue({ channel_id: "c1", host_id: "h-pc", generation: 43 });
    del.mockResolvedValue(undefined);
  });

  it("lists the channels on air with host, encoder, viewers and stale rows", () => {
    renderOnAir();
    const table = screen.getByRole("table", { name: "On air" });
    expect(within(table).getAllByRole("row")).toHaveLength(3);
    const seerah = row("Seerah");
    expect(within(seerah).getByText("105")).toBeInTheDocument();
    expect(within(seerah).getByText("Mac-Studio")).toBeInTheDocument();
    expect(within(seerah).getByText("videotoolbox")).toBeInTheDocument();
    expect(within(seerah).getByText("Live")).toBeInTheDocument();
    expect(within(seerah).getByText("3")).toBeInTheDocument(); // NVENC fallbacks
    expect(within(seerah).queryByText("Stale")).not.toBeInTheDocument();
    const fiqh = row("Fiqh");
    expect(within(fiqh).getByText("Stale")).toBeInTheDocument();
    expect(within(fiqh).getByText("pushing")).toBeInTheDocument();
  });

  it("moves a channel to a host picked from the online, not draining ones", async () => {
    const user = userEvent.setup();
    const onChanged = renderOnAir();
    await user.click(screen.getByRole("button", { name: "Move Seerah" }));
    const dialog = await screen.findByRole("dialog");
    expect(dialog).toHaveTextContent("will be interrupted");
    const picker = within(dialog).getByLabelText("Move to");
    const options = within(picker)
      .getAllByRole("option")
      .map((o) => o.textContent);
    // Old-Laptop is down and NAS is draining: not offered
    expect(options).toEqual(["Automatic", "Mac-Studio (now)", "Gaming-PC"]);
    expect(post).not.toHaveBeenCalled();
    await user.selectOptions(picker, "h-pc");
    await user.click(within(dialog).getByRole("button", { name: "Move" }));
    expect(post).toHaveBeenCalledWith("/api/tv/cluster/channels/c1/move", { host_id: "h-pc" });
    expect(onChanged).toHaveBeenCalled();
    expect(await screen.findByText("Seerah is moving.")).toBeInTheDocument();
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("moves automatically when no host is picked", async () => {
    const user = userEvent.setup();
    renderOnAir();
    await user.click(screen.getByRole("button", { name: "Move Fiqh" }));
    const dialog = await screen.findByRole("dialog");
    await user.click(within(dialog).getByRole("button", { name: "Move" }));
    expect(post).toHaveBeenCalledWith("/api/tv/cluster/channels/c2/move", {});
  });

  it("stops a channel only after a confirm, then deletes its placement", async () => {
    const user = userEvent.setup();
    const onChanged = renderOnAir();
    await user.click(screen.getByRole("button", { name: "Stop Seerah" }));
    const dialog = await screen.findByRole("dialog");
    expect(dialog).toHaveTextContent("Stop Seerah?");
    expect(dialog).toHaveTextContent("will be interrupted");
    expect(del).not.toHaveBeenCalled();
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    expect(del).not.toHaveBeenCalled();

    await user.click(screen.getByRole("button", { name: "Stop Seerah" }));
    await user.click(
      within(await screen.findByRole("dialog")).getByRole("button", { name: "Stop" })
    );
    expect(del).toHaveBeenCalledWith("/api/tv/cluster/channels/c1/placement");
    expect(onChanged).toHaveBeenCalled();
    expect(await screen.findByText("Seerah stopped.")).toBeInTheDocument();
  });

  it("renders in Arabic", async () => {
    const user = userEvent.setup();
    localStorage.setItem("mkt:locale", "ar");
    renderOnAir();
    expect(screen.getByRole("heading", { level: 2, name: "على الهواء" })).toBeInTheDocument();
    expect(screen.getByRole("columnheader", { name: "مشاهدو HLS" })).toBeInTheDocument();
    expect(screen.getByRole("columnheader", { name: "الموالفات" })).toBeInTheDocument();
    expect(within(row("Fiqh")).getByText("قديم")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "إيقاف Seerah" }));
    expect(await screen.findByRole("dialog")).toHaveTextContent("سينقطع البث");
    expect(document.documentElement.dir).toBe("rtl");
  });
});
