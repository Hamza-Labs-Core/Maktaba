// Story 54.20 §1: the admin orders the plugins' panels; moving one saves the whole order.
import { act, fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import EN from "../../i18n/en.json";
import { I18nProvider } from "../../lib/i18n";
import { PanelOrder } from "./PanelOrder";

const get = vi.fn();
const put = vi.fn();
vi.mock("../../lib/api", async (orig) => ({
  ...(await orig<object>()),
  api: {
    get: (...a: unknown[]) => get(...a),
    put: (...a: unknown[]) => put(...a),
    post: vi.fn(),
    delete: vi.fn(),
  },
}));

const plugin = (id: string, en: string) => ({
  id,
  names: { name: { en, ar: en }, summary: {} },
  on: ["video"],
});

beforeEach(() => {
  get.mockReset();
  put.mockReset();
});

describe("Panel order", () => {
  it("moves a plugin up and saves the order", async () => {
    get.mockResolvedValue({
      plugins: [plugin("dev.a", "Loudness"), plugin("dev.b", "Scenes")],
      order: [],
    });
    put.mockResolvedValue({ order: ["dev.b", "dev.a"] });
    render(
      <I18nProvider>
        <PanelOrder />
      </I18nProvider>
    );
    const up = await screen.findByRole("button", {
      name: EN["plugins.panels.order.up"].replace("{name}", "Scenes"),
    });
    expect(
      screen.getByRole("button", {
        name: EN["plugins.panels.order.up"].replace("{name}", "Loudness"),
      })
    ).toBeDisabled();
    await act(async () => fireEvent.click(up));
    expect(put).toHaveBeenCalledWith("/api/plugins/panel-order", { order: ["dev.b", "dev.a"] });
    expect(await screen.findByRole("status")).toHaveTextContent(EN["plugins.panels.order.saved"]);
    expect(screen.getAllByRole("listitem").map((li) => li.textContent?.slice(0, 6))).toEqual([
      "Scenes",
      "Loudne",
    ]);
  });

  it("shows nothing with fewer than two plugins with panels", async () => {
    get.mockResolvedValue({ plugins: [plugin("dev.a", "Loudness")], order: [] });
    const { container } = render(
      <I18nProvider>
        <PanelOrder />
      </I18nProvider>
    );
    await act(async () => {});
    expect(container.textContent).toBe("");
  });
});
