// Story 54.19 §5, T06 (web): a paid plugin's page shows its price; a linked home gets Buy,
// which asks for a purchase intent and posts it to the store's buy page in the tab the click
// opened; an unlinked home gets "Link this home to buy", which opens the linking; a blocked
// pop-up says so; English and Arabic.
import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import EN from "../../i18n/en.json";
import AR from "../../i18n/ar.json";
import { I18nProvider } from "../../lib/i18n";
import type { CatalogEntry } from "../../lib/pluginsApi";
import { BuyPlugin, isPaid, shownPrice } from "./BuyPlugin";

const get = vi.fn();
const post = vi.fn();
vi.mock("../../lib/api", async (orig) => ({
  ...(await orig<object>()),
  api: {
    get: (...a: unknown[]) => get(...a),
    post: (...a: unknown[]) => post(...a),
    put: vi.fn(),
    delete: vi.fn(),
  },
}));

function paid(over: Partial<CatalogEntry> = {}): CatalogEntry {
  return {
    id: "dev.example.pro",
    developer: "Example",
    names: { en: "Pro", ar: "برو" },
    summaries: { en: "s", ar: "s" },
    category: "metadata",
    tags: [],
    rating_avg_x100: 0,
    rating_count: 0,
    installs: 0,
    machine_translated: [],
    versions: [],
    newest: null,
    auto_update: false,
    price: "one_time",
    extra: { prices: { USD: 1000, SAR: 3750 } },
    ...over,
  };
}

const W = EN as Record<string, string>;
const WA = AR as Record<string, string>;

beforeEach(() => {
  for (const f of [get, post]) f.mockReset();
});
afterEach(() => {
  localStorage.removeItem("mkt:locale");
  vi.restoreAllMocks();
});

const show = async (e: CatalogEntry) => {
  render(
    <I18nProvider>
      <BuyPlugin entry={e} />
    </I18nProvider>
  );
  await act(async () => {});
};

describe("Buy on a paid plugin", () => {
  it("knows a paid plugin and shows its price in the usual currency", () => {
    expect(isPaid(paid())).toBe(true);
    expect(isPaid(paid({ price: "free" }))).toBe(false);
    expect(isPaid(paid({ price: undefined }))).toBe(false);
    expect(shownPrice(paid(), "en")?.text).toBe("$10.00");
    expect(shownPrice(paid(), "ar")?.currency).toBe("SAR");
    expect(shownPrice(paid({ extra: {} }), "en")).toBeNull();
  });

  it("a linked home: Buy posts the store's intent to its buy page in the opened tab", async () => {
    get.mockResolvedValue({ linked: true, home_id: "h_1", store: "https://plugins.maktaba.tv" });
    post.mockResolvedValue({
      buy_url: "https://plugins.maktaba.tv/buy/dev.example.pro",
      intent: "I".repeat(43),
      product: "one_time",
      expires_at: "",
    });
    const tab = { opener: {} as unknown, focus: vi.fn(), close: vi.fn() };
    const open = vi.spyOn(window, "open").mockReturnValue(tab as unknown as Window);
    const sent: { action: string; target: string; intent: string }[] = [];
    vi.spyOn(HTMLFormElement.prototype, "submit").mockImplementation(function (
      this: HTMLFormElement
    ) {
      sent.push({
        action: this.action,
        target: this.target,
        intent: (this.elements.namedItem("intent") as HTMLInputElement).value,
      });
    });
    await show(paid());
    expect(
      screen.getByText(W["plugins.buy.price_one_time"]!.replace("{price}", "$10.00"))
    ).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: W["plugins.buy.buy"] }));
    await act(async () => {});
    expect(post).toHaveBeenCalledWith("/api/plugins/store-account/buy", {
      plugin_id: "dev.example.pro",
      lang: "en",
    });
    expect(open).toHaveBeenCalledWith("about:blank", expect.stringMatching(/^mkt-buy-/));
    expect(tab.opener).toBeNull();
    expect(sent).toEqual([
      {
        action: "https://plugins.maktaba.tv/buy/dev.example.pro",
        target: open.mock.calls[0]![1],
        intent: "I".repeat(43),
      },
    ]);
    expect(screen.getByRole("status").textContent).toBe(W["plugins.buy.started"]);
  });

  it("a refusal closes the tab and says why", async () => {
    get.mockResolvedValue({ linked: true, store: "" });
    const { ApiError } = await import("../../lib/api");
    post.mockRejectedValue(
      new ApiError({
        type: "about:blank",
        title: "Not found",
        status: 404,
        code: "not_for_sale",
      } as never)
    );
    const tab = { opener: {}, focus: vi.fn(), close: vi.fn() };
    vi.spyOn(window, "open").mockReturnValue(tab as unknown as Window);
    await show(paid());
    fireEvent.click(screen.getByRole("button", { name: W["plugins.buy.buy"] }));
    await act(async () => {});
    expect(tab.close).toHaveBeenCalled();
    expect(screen.getByRole("alert").textContent).toBe(W["plugins.error.not_for_sale"]);
  });

  it("a blocked pop-up asks nothing of the store", async () => {
    get.mockResolvedValue({ linked: true, store: "" });
    vi.spyOn(window, "open").mockReturnValue(null);
    await show(paid());
    fireEvent.click(screen.getByRole("button", { name: W["plugins.buy.buy"] }));
    await act(async () => {});
    expect(post).not.toHaveBeenCalled();
    expect(screen.getByRole("alert").textContent).toBe(W["plugins.buy.popup_blocked"]);
  });

  it("an unlinked home: Link this home to buy, in Arabic too", async () => {
    localStorage.setItem("mkt:locale", "ar");
    get.mockResolvedValue({ linked: false, store: "https://plugins.maktaba.tv" });
    await show(paid({ price: "yearly" }));
    expect(screen.queryByRole("button", { name: WA["plugins.buy.buy"] })).toBeNull();
    expect(document.querySelector(".mkt-plugins__price")?.textContent).toBe(
      WA["plugins.buy.price_yearly"]!.replace("{price}", shownPrice(paid(), "ar")!.text)
    );
    fireEvent.click(screen.getByRole("button", { name: WA["plugins.buy.link"] }));
    await act(async () => {});
    expect(screen.getByRole("button", { name: WA["plugins.store.link"] })).toBeTruthy();
  });
});
