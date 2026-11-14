// Story 50.25 §4: the console's kill switch. The API is the authority (a non-service-admin
// gets 403, tested in the cloud); here: the list, the add form's bodies by kind, a lift that
// needs a note, the warning with no signing key.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { setStaffToken } from "../api";
import { renderAt, staffSession, stubFetch } from "../test/harness";
import { ACTIONS_BY_KIND } from "./Broadcast";

beforeEach(() => setStaffToken(null));
afterEach(() => vi.unstubAllGlobals());

const LIST = "/v1/admin/broadcast/revocations";

function view(over: Record<string, unknown> = {}) {
  return {
    version: 7,
    changed_at: "2026-11-13T10:00:00Z",
    signing: true,
    entries: [
      {
        id: "r-2",
        kind: "home",
        ref: "srv-9",
        action: "suspend",
        reason: "abuse",
        since: "2026-11-13T09:00:00Z",
        note: "spam relay",
        added_by: "ops@example.com",
        added_at: "2026-11-13T09:00:00Z",
      },
      {
        id: "r-1",
        kind: "channel",
        ref: "ch-abc",
        action: "off_air",
        reason: "notice",
        since: "2026-11-12T09:00:00Z",
        note: "DMCA 1234",
        added_by: "ops@example.com",
        added_at: "2026-11-12T09:00:00Z",
      },
      {
        id: "r-0",
        kind: "global",
        action: "off_air",
        reason: "incident",
        since: "2026-11-01T09:00:00Z",
        note: "drill",
        added_by: "ops@example.com",
        added_at: "2026-11-01T09:00:00Z",
        lifted_at: "2026-11-01T09:10:00Z",
      },
    ],
    audit: [
      {
        revocation: "r-2",
        action: "add",
        version: 7,
        actor: "ops@example.com",
        note: "spam relay",
        at: "2026-11-13T09:00:00Z",
      },
    ],
    ...over,
  };
}

describe("broadcast kill switch", () => {
  it("renders Access denied when the API answers 403", async () => {
    stubFetch({
      "POST /v1/staff/refresh": staffSession([]),
      [`GET ${LIST}`]: [403, { error: "admin_access_denied", message: "denied" }],
    });
    renderAt("/broadcast");
    expect(await screen.findByText("Access denied")).toBeInTheDocument();
    expect(screen.queryByRole("table")).not.toBeInTheDocument();
  });

  it("lists the live entries by kind, the lifted ones apart, and the audit", async () => {
    stubFetch({ "POST /v1/staff/refresh": staffSession(), [`GET ${LIST}`]: [200, view()] });
    renderAt("/broadcast");
    const current = (await screen.findByRole("heading", { name: "On the list now" })).closest(
      "section, div"
    )!.parentElement!;
    const rows = within(current).getAllByRole("row").slice(1);
    // channel sorts before home
    expect(rows.map((r) => within(r).getAllByRole("cell")[1]!.textContent)).toEqual([
      "ch-abc",
      "srv-9",
    ]);
    expect(within(rows[1]!).getByText("Suspend")).toBeInTheDocument();
    expect(within(rows[1]!).getByText("spam relay")).toBeInTheDocument();
    const lifted = screen
      .getByRole("heading", { name: "Lifted in the last 30 days" })
      .closest("section, div")!.parentElement!;
    expect(within(lifted).getByText("Every broadcast")).toBeInTheDocument();
    expect(within(lifted).queryByRole("button", { name: /Lift/ })).not.toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Audit" })).toBeInTheDocument();
    expect(screen.queryByText(/No broadcast signing key/)).not.toBeInTheDocument();
  });

  it("warns when no signing key is set", async () => {
    stubFetch({
      "POST /v1/staff/refresh": staffSession(),
      [`GET ${LIST}`]: [200, view({ signing: false, entries: [], audit: [] })],
    });
    renderAt("/broadcast");
    expect(await screen.findByText(/No broadcast signing key/)).toBeInTheDocument();
    expect(screen.getByText("Nothing is on the list.")).toBeInTheDocument();
  });

  it("posts a channel entry with its ref, and a global one without", async () => {
    const f = stubFetch({
      "POST /v1/staff/refresh": staffSession(),
      [`GET ${LIST}`]: [200, view({ entries: [], audit: [] })],
      [`POST ${LIST}`]: [201, { id: "r-3", version: 8 }],
    });
    renderAt("/broadcast");
    const u = userEvent.setup();
    const add = await screen.findByRole("button", { name: "Add to the list" });
    expect(add).toBeDisabled();
    await u.type(screen.getByLabelText(/Identifier/), " ch-xyz ");
    await u.type(screen.getByLabelText(/Note for the audit/), "DMCA 99");
    expect(add).toBeEnabled();
    await u.click(add);
    await waitFor(() => expect(f.find("POST", LIST)).toHaveLength(1));
    expect(f.find("POST", LIST)[0]!.body).toEqual({
      kind: "channel",
      ref: "ch-xyz",
      action: "off_air",
      reason: "notice",
      note: "DMCA 99",
    });

    await u.selectOptions(screen.getByLabelText("Applies to"), "global");
    expect(screen.queryByLabelText(/Identifier/)).not.toBeInTheDocument();
    await u.selectOptions(screen.getByLabelText("Reason"), "incident");
    await u.type(screen.getByLabelText(/Note for the audit/), "edge incident");
    await u.click(screen.getByRole("button", { name: "Add to the list" }));
    await waitFor(() => expect(f.find("POST", LIST)).toHaveLength(2));
    expect(f.find("POST", LIST)[1]!.body).toEqual({
      kind: "global",
      action: "off_air",
      reason: "incident",
      note: "edge incident",
    });
  });

  it("asks a programme entry for its end, and offers only the actions a kind may take", async () => {
    const f = stubFetch({
      "POST /v1/staff/refresh": staffSession(),
      [`GET ${LIST}`]: [200, view({ entries: [], audit: [] })],
      [`POST ${LIST}`]: [201, { id: "r-3", version: 8 }],
    });
    renderAt("/broadcast");
    const u = userEvent.setup();
    await u.selectOptions(await screen.findByLabelText("Applies to"), "programme");
    const actions = within(screen.getByLabelText("Action")).getAllByRole("option");
    expect(actions.map((o) => (o as HTMLOptionElement).value)).toEqual(ACTIONS_BY_KIND.programme);
    await u.type(screen.getByLabelText(/Identifier/), "ch-abc");
    await u.type(screen.getByLabelText(/Note for the audit/), "match rights");
    expect(screen.getByRole("button", { name: "Add to the list" })).toBeDisabled();
    await u.type(screen.getByLabelText(/Slate until/), "2026-11-20T21:00");
    await u.click(screen.getByRole("button", { name: "Add to the list" }));
    await waitFor(() => expect(f.find("POST", LIST)).toHaveLength(1));
    const body = f.find("POST", LIST)[0]!.body as Record<string, string>;
    expect(body).toMatchObject({ kind: "programme", ref: "ch-abc", action: "slate" });
    expect(body.until).toBe(new Date("2026-11-20T21:00").toISOString());
  });

  it("lifts an entry only with a note, and posts it", async () => {
    const f = stubFetch({
      "POST /v1/staff/refresh": staffSession(),
      [`GET ${LIST}`]: [200, view()],
      [`POST ${LIST}/r-1/lift`]: [200, { version: 8 }],
    });
    renderAt("/broadcast");
    const u = userEvent.setup();
    await u.click(await screen.findByRole("button", { name: "Lift r-1" }));
    const dialog = await screen.findByRole("dialog");
    const confirm = within(dialog).getByRole("button", { name: "Lift" });
    expect(confirm).toBeDisabled();
    await u.type(within(dialog).getByLabelText(/Reason/), "notice withdrawn");
    await u.click(confirm);
    await waitFor(() => expect(f.find("POST", `${LIST}/r-1/lift`)).toHaveLength(1));
    expect(f.find("POST", `${LIST}/r-1/lift`)[0]!.body).toEqual({ note: "notice withdrawn" });
  });
});
