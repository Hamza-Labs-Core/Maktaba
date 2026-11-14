// Epic 50 story 50.26: who may watch the home's broadcast channels (owner or admin only).
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderPage, memberUser } from "../../test/pageHarness";
import { routeGet } from "../../test/queueHarness";

const { get, post, put, del } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  put: vi.fn(),
  del: vi.fn(),
}));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post, put, patch: vi.fn(), delete: del } };
});

import { ApiError } from "../../lib/api";
import { BroadcastAccess } from "./BroadcastAccess";

const BASE = "/api/broadcast/access";

const access = (over: Record<string, unknown> = {}) => ({
  channels: [{ id: "c1", slug: "news", access: "invite", max_viewers: 0 }],
  max_sessions_per_ip: 3,
  daily_minutes: 0,
  max_viewers: 100,
  open_access_enabled: true,
  open_in_plan: true,
  verification: {
    verified: false,
    paid_plan: true,
    email_confirmed: true,
    days_since_first_publish: 2,
  },
  ...over,
});

const invite = {
  id: "i1",
  url: "https://watch.maktaba.tv/i/mkbn1.abc.def",
  short_code: "MK-AB12",
  label: "Family in Cairo",
  channels: [],
  max_uses: 10,
  uses: 3,
  expires_at: "2026-12-14T00:00:00Z",
  status: "active",
};

function serve(view = access()) {
  routeGet(get, {
    [BASE]: view,
    [`${BASE}/invites`]: { invites: [invite] },
    [`${BASE}/invites/i1/devices`]: {
      devices: [{ id: "p1", first_use: 1, last_use: 1794528000000, revoked: false }],
    },
  });
  post.mockResolvedValue({ ...invite, id: "i2", short_code: "MK-ZZ99", label: "", uses: 0 });
  put.mockResolvedValue(null);
  del.mockResolvedValue(null);
}

const open = (user?: typeof memberUser) =>
  renderPage(<BroadcastAccess />, {
    path: "/admin/broadcast-access",
    ...(user ? { user } : {}),
  });

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
});
afterEach(() => localStorage.clear());

describe("broadcast access (home)", () => {
  it("lists the invites with their uses, and says viewers never pay", async () => {
    serve();
    open();
    expect(await screen.findByText("Family in Cairo")).toBeInTheDocument();
    expect(screen.getByText(/3 of 10 devices/)).toBeInTheDocument();
    expect(screen.getByText("Free to watch")).toBeInTheDocument();
  });

  it("creates an invite link and shows its TV code", async () => {
    serve();
    open();
    const ue = userEvent.setup();
    await ue.type(await screen.findByLabelText("Name (optional)"), "Grandma");
    await ue.click(screen.getByRole("button", { name: "Create invite link" }));
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith(`${BASE}/invites`, {
        channels: [],
        label: "Grandma",
        max_uses: 10,
        expires_in_days: 30,
      })
    );
    expect(await screen.findByTestId("new-invite")).toHaveTextContent("MK-ZZ99");
  });

  it("withdraws one device and the invite", async () => {
    serve();
    open();
    const ue = userEvent.setup();
    const row = await screen.findByTestId("invite");
    await ue.click(within(row).getByRole("button", { name: "Devices" }));
    await ue.click(await screen.findByRole("button", { name: "Remove device" }));
    await waitFor(() => expect(del).toHaveBeenCalledWith(`${BASE}/invites/i1/devices/p1`));
    await ue.click(within(row).getByRole("button", { name: "Withdraw" }));
    await waitFor(() => expect(del).toHaveBeenCalledWith(`${BASE}/invites/i1`));
  });

  it("keeps open off until the account is verified, item by item", async () => {
    serve();
    open();
    expect(await screen.findByTestId("verification")).toHaveTextContent(
      "✗ 7 days since your first broadcast (2 so far)"
    );
    const option = screen.getByRole("option", {
      name: "Anyone with the link",
    }) as HTMLOptionElement;
    expect(option.disabled).toBe(true);
  });

  it("saves the viewer limits", async () => {
    serve(access({ open_in_plan: false }));
    open();
    const ue = userEvent.setup();
    const perIP = await screen.findByLabelText("Devices per network");
    await ue.clear(perIP);
    await ue.type(perIP, "20");
    await ue.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() =>
      expect(put).toHaveBeenCalledWith(`${BASE}/limits`, {
        max_sessions_per_ip: 20,
        daily_minutes: 0,
      })
    );
  });

  it("an unlinked home says to link it first", async () => {
    get.mockRejectedValue(
      new ApiError({ type: "about:blank", title: "x", status: 409, code: "cloud_not_linked" })
    );
    open();
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Link the home to Maktaba Cloud first"
    );
  });

  it("is not for a member: nothing is asked of the API", async () => {
    serve();
    open(memberUser);
    await waitFor(() => expect(screen.queryByText("Who can watch")).not.toBeInTheDocument());
    expect(get).not.toHaveBeenCalled();
  });

  it("speaks Arabic", async () => {
    localStorage.setItem("mkt:locale", "ar");
    serve();
    open();
    expect(await screen.findByText("من يمكنه المشاهدة")).toBeInTheDocument();
    expect(await screen.findByRole("button", { name: "أنشئ رابط دعوة" })).toBeInTheDocument();
  });
});
