// Settings → Advanced → Experiments: the user's opt-in cohorts, marked
// unstable; a toggle joins or leaves and refreshes the flags at once.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "../../lib/i18n";
import { ToastProvider } from "@ds/components/Toast/Toast";
import { TestAuth, memberUser } from "../../test/pageHarness";

const { get, post, del } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), del: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post, delete: del } };
});

import { FlagsClient, FlagsProvider } from "../../lib/flags";
import { ExperimentsSection } from "./ExperimentsSection";

const early = {
  name: "early_access",
  user_opt_in: true,
  description: "server text",
  joined: false,
};

function renderSection(client: FlagsClient) {
  render(
    <I18nProvider>
      <ToastProvider>
        <TestAuth user={memberUser}>
          <FlagsProvider client={client}>
            <ExperimentsSection />
          </FlagsProvider>
        </TestAuth>
      </ToastProvider>
    </I18nProvider>
  );
}

describe("ExperimentsSection", () => {
  beforeEach(() => {
    localStorage.clear();
    get.mockReset();
    post.mockReset();
    del.mockReset();
  });

  it("says experiments are unstable, joins one and refreshes the flags", async () => {
    const user = userEvent.setup();
    get.mockResolvedValue({ items: [early] });
    post.mockResolvedValue(undefined);
    const load = vi.fn().mockResolvedValue({
      flags: { early_access: true },
      tier: "free",
      kid: "k",
      issued_at: "",
      expires_at: "",
      signature: "s",
    });
    const client = new FlagsClient(load);
    renderSection(client);
    expect(screen.getByRole("note")).toHaveTextContent(/unstable/);
    const toggle = await screen.findByRole("switch", { name: "Early access" });
    expect(toggle).not.toBeChecked();
    const before = load.mock.calls.length;
    await user.click(toggle);
    expect(post).toHaveBeenCalledWith("/api/me/cohorts", { cohort: "early_access" });
    await waitFor(() => expect(toggle).toBeChecked());
    expect(load.mock.calls.length).toBeGreaterThan(before);
    expect(client.isOn("early_access")).toBe(true);
  });

  it("leaves a joined experiment", async () => {
    const user = userEvent.setup();
    get.mockResolvedValue({ items: [{ ...early, joined: true }] });
    del.mockResolvedValue(undefined);
    renderSection(new FlagsClient(vi.fn().mockResolvedValue(null)));
    await user.click(await screen.findByRole("switch", { name: "Early access" }));
    expect(del).toHaveBeenCalledWith("/api/me/cohorts/early_access");
  });

  it("renders in Arabic", async () => {
    localStorage.setItem("mkt:locale", "ar");
    get.mockResolvedValue({ items: [early] });
    renderSection(new FlagsClient(vi.fn().mockResolvedValue(null)));
    expect(await screen.findByRole("switch", { name: "الوصول المبكر" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "التجارب" })).toBeInTheDocument();
  });
});
