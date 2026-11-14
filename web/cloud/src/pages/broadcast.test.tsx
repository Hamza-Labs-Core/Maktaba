import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { setAccessToken } from "../shared/api";
import { renderAt, session, stubFetch, user } from "../test/harness";

beforeEach(() => setAccessToken(null));
afterEach(() => {
  localStorage.removeItem("mkt:cloud:locale");
});

const base = "/v1/broadcast/homes/h1";

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
  viewers_pay: false,
  ...over,
});

const invite = (over: Record<string, unknown> = {}) => ({
  id: "i1",
  url: "https://watch.maktaba.tv/i/mkbn1.abc.def",
  short_code: "MK-AB12",
  label: "Family in Cairo",
  channels: [],
  max_uses: 10,
  uses: 3,
  expires_at: "2026-12-14T00:00:00Z",
  status: "active",
  ...over,
});

function routes(view = access(), invites = [invite()]) {
  return {
    "POST /v1/auth/refresh": session(user()),
    "GET /v1/homes": [
      200,
      {
        homes: [
          { server_id: "h1", name: "Living room", owned: true },
          { server_id: "h2", name: "A friend's", owned: false },
        ],
      },
    ] as [number, unknown],
    [`GET ${base}/access`]: [200, view] as [number, unknown],
    [`GET ${base}/invites`]: [200, { invites }] as [number, unknown],
    [`POST ${base}/invites`]: [
      201,
      invite({ id: "i2", short_code: "MK-ZZ99", label: "", uses: 0 }),
    ] as [number, unknown],
    [`DELETE ${base}/invites/i1`]: [204, null] as [number, unknown],
    [`PUT ${base}/viewer-limits`]: [204, null] as [number, unknown],
    [`PUT ${base}/channels/c1/access`]: [204, null] as [number, unknown],
    [`GET ${base}/invites/i1/devices`]: [
      200,
      { devices: [{ id: "p1", first_use: 1, last_use: 1794528000000, revoked: false }] },
    ] as [number, unknown],
    [`DELETE ${base}/invites/i1/devices/p1`]: [204, null] as [number, unknown],
  };
}

// Epic 50 story 50.26: who may watch each home's channels.
describe("broadcast: viewer access", () => {
  it("lists the owned home's invites with their uses, never a friend's home", async () => {
    stubFetch(routes());
    renderAt("/broadcast");
    expect(await screen.findByText("Living room")).toBeInTheDocument();
    expect(screen.queryByText("A friend's")).not.toBeInTheDocument();
    expect(await screen.findByText("Family in Cairo")).toBeInTheDocument();
    expect(screen.getByText(/3 of 10 devices/)).toBeInTheDocument();
    expect(screen.getByText("Free to watch")).toBeInTheDocument();
  });

  it("creates an invite link and shows its TV code", async () => {
    const f = stubFetch(routes());
    renderAt("/broadcast");
    const ue = userEvent.setup();
    await ue.type(await screen.findByLabelText("Name (optional)"), "Grandma");
    await ue.click(screen.getByRole("button", { name: "Create invite link" }));
    await waitFor(() =>
      expect(f.find("POST", `${base}/invites`)[0].body).toEqual({
        channels: [],
        label: "Grandma",
        max_uses: 10,
        expires_in_days: 30,
      })
    );
    expect(await screen.findByTestId("new-invite")).toHaveTextContent("MK-ZZ99");
  });

  it("withdraws an invite and one device", async () => {
    const f = stubFetch(routes());
    renderAt("/broadcast");
    const ue = userEvent.setup();
    const row = await screen.findByTestId("invite");
    await ue.click(within(row).getByRole("button", { name: "Devices" }));
    await ue.click(await screen.findByRole("button", { name: "Remove device" }));
    await waitFor(() => expect(f.find("DELETE", `${base}/invites/i1/devices/p1`)).toHaveLength(1));
    await ue.click(within(row).getByRole("button", { name: "Withdraw" }));
    await waitFor(() => expect(f.find("DELETE", `${base}/invites/i1`)).toHaveLength(1));
  });

  it("says what an open channel still needs, item by item", async () => {
    stubFetch(routes());
    renderAt("/broadcast");
    expect(
      await screen.findByText(/Open channels are available once your account is verified/)
    ).toBeInTheDocument();
    expect(screen.getByTestId("verification")).toHaveTextContent(
      "✗ 7 days since your first broadcast (2 so far)"
    );
    const option = screen.getByRole("option", {
      name: "Anyone with the link",
    }) as HTMLOptionElement;
    expect(option.disabled).toBe(true);
  });

  it("saves the viewer limits", async () => {
    const f = stubFetch(routes(access({ open_in_plan: false })));
    renderAt("/broadcast");
    const ue = userEvent.setup();
    const perIP = await screen.findByLabelText("Devices per network");
    await ue.clear(perIP);
    await ue.type(perIP, "20");
    await ue.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() =>
      expect(f.find("PUT", `${base}/viewer-limits`)[0].body).toEqual({
        max_sessions_per_ip: 20,
        daily_minutes: 0,
      })
    );
  });

  it("speaks Arabic", async () => {
    localStorage.setItem("mkt:cloud:locale", "ar");
    stubFetch(routes());
    renderAt("/broadcast");
    expect(await screen.findByText("من يمكنه المشاهدة")).toBeInTheDocument();
    expect(await screen.findByText(/من .* أجهزة/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "أنشئ رابط دعوة" })).toBeInTheDocument();
  });
});
