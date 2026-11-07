// Story 54.15 §5 and T12 (the home's page): link this home, see the code and its QR code,
// polled until linked; unlink. English and Arabic.
import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import EN from "../../i18n/en.json";
import AR from "../../i18n/ar.json";
import { I18nProvider } from "../../lib/i18n";
import { StoreAccount } from "./StoreAccount";

const get = vi.fn();
const post = vi.fn();
const del = vi.fn();
vi.mock("../../lib/api", async (orig) => ({
  ...(await orig<object>()),
  api: {
    get: (...a: unknown[]) => get(...a),
    post: (...a: unknown[]) => post(...a),
    delete: (...a: unknown[]) => del(...a),
    put: vi.fn(),
  },
}));

beforeEach(() => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  for (const f of [get, post, del]) f.mockReset();
});
afterEach(() => {
  vi.useRealTimers();
  localStorage.removeItem("mkt:locale");
});

const pending = {
  user_code: "KQ7M-4TXP",
  verification_uri: "https://plugins.maktaba.tv/link",
  expires_at: "2026-11-06T12:10:00Z",
  interval: 5,
};

describe("Store account", () => {
  it("links: the code and a QR code for /link, then linked after the poll", async () => {
    get.mockResolvedValue({ linked: false, store: "https://plugins.maktaba.tv" });
    post.mockImplementation(async (path: string) =>
      path.endsWith("/link")
        ? pending
        : { linked: true, home_id: "h_1", store: "https://plugins.maktaba.tv" }
    );
    render(
      <I18nProvider>
        <StoreAccount />
      </I18nProvider>
    );
    await act(async () =>
      fireEvent.click(await screen.findByRole("button", { name: EN["plugins.store.link"] }))
    );
    expect(await screen.findByText("KQ7M-4TXP")).toBeInTheDocument();
    expect(screen.getByRole("img", { name: /plugins\.maktaba\.tv\/link/ })).toBeInTheDocument();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5_100);
    });
    expect(await screen.findByRole("status")).toHaveTextContent("h_1");
    expect(post).toHaveBeenCalledWith("/api/plugins/store-account/poll");
  });

  it("Arabic: the page's words, and unlink", async () => {
    localStorage.setItem("mkt:locale", "ar");
    get.mockResolvedValue({ linked: true, home_id: "h_9", store: "https://plugins.maktaba.tv" });
    del.mockResolvedValue({ linked: false });
    render(
      <I18nProvider>
        <StoreAccount />
      </I18nProvider>
    );
    expect(
      await screen.findByRole("heading", { name: AR["plugins.store.title"] })
    ).toBeInTheDocument();
    get.mockResolvedValue({ linked: false, store: "https://plugins.maktaba.tv" });
    await act(async () =>
      fireEvent.click(screen.getByRole("button", { name: AR["plugins.store.unlink"] }))
    );
    expect(del).toHaveBeenCalledWith("/api/plugins/store-account");
    expect(
      await screen.findByRole("button", { name: AR["plugins.store.link"] })
    ).toBeInTheDocument();
  });
});
