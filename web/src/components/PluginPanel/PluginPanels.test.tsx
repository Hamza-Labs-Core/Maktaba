// Story 54.20: while the home serves panels, the section asks GET /api/panels for the
// subject in the person's language and shows what comes back, collapsed; a 404 (an older
// server, or a subject the person cannot see) is no panels.
import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { I18nProvider } from "../../lib/i18n";
import { ApiError, api } from "../../lib/api";
import en from "../../i18n/en.json";

vi.mock("../../lib/capabilities", async () => {
  const actual =
    await vi.importActual<typeof import("../../lib/capabilities")>("../../lib/capabilities");
  return { ...actual, useCapabilities: () => ({ ...actual.ALL_ON, answered: true, panels: true }) };
});

import { PluginPanels } from "./PluginPanel";

const PANELS = {
  panels: [
    {
      plugin_id: "dev.example.loudness",
      plugin: "Loudness",
      title: "Levels",
      blocks: [{ type: "kv", value: [{ label: "Integrated", value: "-14 LUFS" }] }],
    },
  ],
};

function show(kind: "series" | "library", id: string) {
  return render(
    <MemoryRouter>
      <I18nProvider>
        <PluginPanels subject={{ kind, id }} />
      </I18nProvider>
    </MemoryRouter>
  );
}

describe("PluginPanels", () => {
  beforeEach(() => vi.restoreAllMocks());

  it("asks for the subject's panels and shows them under From plugins", async () => {
    const get = vi.spyOn(api, "get").mockResolvedValue(PANELS as never);
    show("series", "s1");
    const summary = await screen.findByText(en["plugins.panels.title"]);
    expect(get).toHaveBeenCalledWith(
      expect.stringMatching(/^\/api\/panels\?subject=series%3As1&lang=(en|ar)$/)
    );
    await userEvent.setup().click(summary);
    expect(await screen.findByText("-14 LUFS")).toBeInTheDocument();
    expect(screen.getByText("Levels")).toBeInTheDocument();
  });

  it("shows nothing for a 404", async () => {
    const get = vi
      .spyOn(api, "get")
      .mockRejectedValue(
        new ApiError({ type: "about:blank", title: "Not found", status: 404 }) as never
      );
    const { container } = show("library", "l1");
    await waitFor(() => expect(get).toHaveBeenCalled());
    expect(container.querySelector("details")).toBeNull();
  });
});
