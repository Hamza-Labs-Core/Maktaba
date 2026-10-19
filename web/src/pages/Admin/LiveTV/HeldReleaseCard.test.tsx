// Story 35.3 AC4: a contract release waits for an admin on the Changes page; approving
// is Update now for that version, Skip keeps it from installing automatically.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock("../../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../../lib/api")>("../../../lib/api");
  return { ...actual, api: { ...actual.api, get, post } };
});

import { I18nProvider } from "../../../lib/i18n";
import { HeldReleaseCard } from "./HeldReleaseCard";

const heldOverview = {
  current_version: "1.9.0",
  watermark: "1.9.0",
  policy: {},
  auto_install_available: true,
  plan: {
    target: { version: "2.0.0", tag: "v2.0.0", channel: "stable", verify_state: "verified" },
    held: true,
    stepping_stone: false,
    auto_install: true,
    due: false,
    reason: "needs_approval",
  },
  catalog: [],
  check_stale: false,
  time_zone: "UTC",
};

function renderCard() {
  return render(
    <I18nProvider>
      <HeldReleaseCard />
    </I18nProvider>
  );
}

beforeEach(() => {
  get.mockReset();
  post.mockReset();
});

describe("HeldReleaseCard", () => {
  it("shows a held release and approves it with Update now for that version", async () => {
    get.mockResolvedValue(heldOverview);
    post.mockResolvedValue({ run_id: "r1", version: "2.0.0" });
    const user = userEvent.setup();
    renderCard();
    expect(await screen.findByTestId("held-release")).toHaveTextContent(
      "Version 2.0.0 needs your approval"
    );
    await user.click(screen.getByRole("button", { name: "Approve and update now" }));
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith("/api/cluster/updates/run", {
        version: "2.0.0",
        confirm: true,
      })
    );
  });

  it("skips a held release", async () => {
    get.mockResolvedValue(heldOverview);
    post.mockResolvedValue({});
    const user = userEvent.setup();
    renderCard();
    await user.click(await screen.findByRole("button", { name: "Skip this version" }));
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith("/api/cluster/updates/skip", { version: "2.0.0" })
    );
  });

  it("offers no Update now on a build without the update orchestrator", async () => {
    get.mockResolvedValue({ ...heldOverview, auto_install_available: false });
    renderCard();
    expect(await screen.findByTestId("held-not-built")).toHaveTextContent(
      "Automatic install arrives with the update orchestrator."
    );
    expect(screen.queryByRole("button", { name: "Approve and update now" })).toBeNull();
    expect(screen.getByRole("button", { name: "Skip this version" })).toBeInTheDocument();
    expect(post).not.toHaveBeenCalled();
  });

  it("renders nothing when nothing is held", async () => {
    get.mockResolvedValue({ ...heldOverview, plan: { ...heldOverview.plan, held: false } });
    const { container } = renderCard();
    await waitFor(() => expect(get).toHaveBeenCalled());
    expect(container).toBeEmptyDOMElement();
  });
});
