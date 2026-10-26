// The public site panel (Epic 34): settings saved, a channel made public only
// through the rights dialog (the server's 422 opens it; the confirmed PUT
// carries rights_confirmed), private without a dialog, secrets shown once,
// hidden for a non-admin (403). Arabic renders.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "../../../lib/i18n";
import { ApiError } from "../../../lib/api";

const { get, post, put } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn() }));
vi.mock("../../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../../lib/api")>("../../../lib/api");
  return { ...actual, api: { ...actual.api, get, post, put } };
});

import { PublicPanel } from "./PublicPanel";
import type { PublicView } from "../../../lib/tvPublicApi";

const view = (over: Partial<PublicView> = {}): PublicView => ({
  settings: { enabled: false, site_url: "" },
  secrets_set: false,
  publisher: { last_ok_at: null, last_at: null },
  channels: [
    {
      channel_id: "c1",
      number: 101,
      name: "Seerah",
      enabled: true,
      public: false,
      slug: "seerah",
      rights_confirmed_by: null,
      rights_confirmed_at: null,
      rights_note: "",
      on_air: false,
    },
  ],
  ...over,
});

function problem(status: number, code: string) {
  return new ApiError({ type: "about:blank", title: "x", status, detail: code, code } as never);
}

function renderPanel() {
  return render(
    <I18nProvider>
      <PublicPanel />
    </I18nProvider>
  );
}

beforeEach(() => {
  localStorage.clear();
  get.mockReset();
  post.mockReset();
  put.mockReset();
});

describe("PublicPanel", () => {
  it("makes a channel public only after the rights confirmation", async () => {
    get.mockResolvedValue(view());
    put.mockRejectedValueOnce(problem(422, "rights_confirmation_required")).mockResolvedValue({});
    renderPanel();
    const toggle = await screen.findByRole("switch", { name: "Public: Seerah" });
    await userEvent.click(toggle);
    expect(put).toHaveBeenLastCalledWith(
      "/api/tv/public/channels/c1",
      expect.objectContaining({ rights_confirmed: false })
    );
    expect(await screen.findByText("Confirm your rights")).toBeInTheDocument();
    await userEvent.click(
      screen.getByRole("button", { name: "I hold the rights: make it public" })
    );
    await waitFor(() =>
      expect(put).toHaveBeenLastCalledWith(
        "/api/tv/public/channels/c1",
        expect.objectContaining({ public: true, slug: "seerah", rights_confirmed: true })
      )
    );
  });

  it("makes a channel private with no dialog", async () => {
    const v = view();
    v.channels![0] = {
      ...v.channels![0],
      public: true,
      rights_confirmed_at: "2026-10-26T10:00:00Z",
    };
    get.mockResolvedValue(v);
    put.mockResolvedValue({});
    renderPanel();
    await userEvent.click(await screen.findByRole("switch", { name: "Public: Seerah" }));
    expect(put).toHaveBeenCalledWith("/api/tv/public/channels/c1", { public: false });
    expect(screen.queryByText("Confirm your rights")).not.toBeInTheDocument();
  });

  it("saves the settings and shows the server's refusal", async () => {
    get.mockResolvedValue(view());
    put.mockRejectedValueOnce(problem(400, "site_url: use https"));
    renderPanel();
    await userEvent.click(await screen.findByRole("switch", { name: "Publish public channels" }));
    await userEvent.type(screen.getByLabelText(/Site address/), "http://tv.example.com");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(put).toHaveBeenCalledWith("/api/tv/public", {
      enabled: true,
      site_url: "http://tv.example.com",
    });
    expect(await screen.findByRole("alert")).toHaveTextContent("site_url: use https");
  });

  it("shows the site's secrets once", async () => {
    get.mockResolvedValue(view());
    post.mockResolvedValue({
      home_hmac: "h".repeat(64),
      signing_key_id: "kid-1",
      signing_key_jwk: "eyJqd2siOjF9",
    });
    renderPanel();
    await userEvent.click(await screen.findByRole("button", { name: "Create the site's secrets" }));
    await userEvent.click(
      screen.getAllByRole("button", { name: "Create the site's secrets" }).at(-1)!
    );
    expect(post).toHaveBeenCalledWith("/api/tv/public/site-secrets");
    expect(await screen.findByDisplayValue("kid-1")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "I copied them" }));
    expect(screen.queryByDisplayValue("kid-1")).not.toBeInTheDocument();
  });

  it("is hidden for a non-admin", async () => {
    get.mockRejectedValue(problem(403, "admin-only"));
    const { container } = renderPanel();
    await waitFor(() => expect(container).toBeEmptyDOMElement());
  });

  it("renders in Arabic", async () => {
    localStorage.setItem("mkt:locale", "ar");
    get.mockResolvedValue(view());
    renderPanel();
    expect(await screen.findByText("موقع التلفاز العام")).toBeInTheDocument();
  });
});
