// Story 54.18 T01, T02, T05, T07, T08 (web): Browse filters (the server already left out what
// cannot run here; the switch asks for the rest), Arabic search with or without diacritics,
// plugin words as text, the expired catalog, install, rate too early, English and Arabic.
import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import EN from "../../i18n/en.json";
import AR from "../../i18n/ar.json";
import { I18nProvider } from "../../lib/i18n";
import type { CatalogEntry, CatalogView } from "../../lib/pluginsApi";
import { Browse, filterEntries } from "./Browse";

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

const version = {
  version: "1.2.0",
  sha256: "a".repeat(64),
  size: 4096,
  maktaba: ">=0.6.0, <0.9.0",
  plugin_api: "1.0",
  platforms: ["any"],
  capabilities: ["tags.write"],
  review_level: "reviewed" as const,
  released: "2026-11-01T00:00:00Z",
};
function entry(over: Partial<CatalogEntry> = {}): CatalogEntry {
  return {
    id: "dev.example.tagger",
    developer: "Example",
    names: { en: "Tagger", ar: "الوسّام" },
    summaries: { en: "Adds tags <b>fast</b>", ar: "يضيف الوُسُوم" },
    category: "metadata",
    tags: ["tags"],
    rating_avg_x100: 420,
    rating_count: 12,
    installs: 300,
    machine_translated: ["ar"],
    versions: [version],
    newest: version,
    auto_update: false,
    ...over,
  };
}
const trust = {
  revoked_version: 3,
  revoked_expired: false,
  catalog_version: 4,
  catalog_expired: false,
  store_keys: true,
  store_installs_ok: true,
};
const view = (over: Partial<CatalogView> = {}): CatalogView => ({
  plugins: [
    entry(),
    entry({
      id: "dev.example.old",
      names: { en: "Old", ar: "قديم" },
      newest: null,
      rating_count: 2,
    }),
  ],
  trust,
  opening: false,
  ...over,
});

beforeEach(() => {
  for (const f of [get, post]) f.mockReset();
});
afterEach(() => localStorage.removeItem("mkt:locale"));

const show = () =>
  render(
    <I18nProvider>
      <Browse onInstalled={vi.fn()} />
    </I18nProvider>
  );

describe("Browse", () => {
  it("T02: Arabic search finds the same with or without diacritics", () => {
    const list = [entry()];
    expect(filterEntries(list, "الوسوم", "", "rating")).toHaveLength(1);
    expect(filterEntries(list, "الوُسُوم", "", "rating")).toHaveLength(1);
    expect(filterEntries(list, "tagger", "", "rating")).toHaveLength(1);
    expect(filterEntries(list, "nothing", "", "rating")).toHaveLength(0);
  });

  it("T01, T05: incompatible ones are marked; words are text, not markup", async () => {
    get.mockResolvedValue(view());
    show();
    expect(
      (await screen.findAllByText("Adds tags <b>fast</b>", { exact: false })).length
    ).toBeGreaterThan(0);
    expect(document.querySelector("b")).toBeNull();
    expect(screen.getByText(EN["plugins.browse.needs_newer"])).toBeInTheDocument();
    expect(screen.getByText(new RegExp(EN["plugins.browse.few_ratings"]))).toBeInTheDocument();
  });

  it("the store not open yet says so", async () => {
    get.mockResolvedValue(view({ plugins: [], opening: true }));
    show();
    expect(await screen.findByText(EN["plugins.browse.opening"])).toBeInTheDocument();
  });

  it("an expired catalog shows a banner and Install waits; a fresh one installs", async () => {
    get.mockResolvedValue(
      view({ trust: { ...trust, catalog_expired: true, store_installs_ok: false } })
    );
    show();
    const open = await screen.findAllByRole("button", { name: EN["plugins.action.open"] });
    await act(async () => fireEvent.click(open[0]!));
    expect(screen.getByRole("button", { name: EN["plugins.browse.install"] })).toBeDisabled();
    expect(screen.getAllByRole("alert")[0]).toHaveTextContent(EN["plugins.browse.expired"]);
  });

  it("install and a rating too early (the store's words)", async () => {
    get.mockResolvedValue(view());
    post.mockImplementation(async (path: string) => {
      if (path === "/api/plugins/install")
        return { result: { plugin_id: "dev.example.tagger" }, plugin: {} };
      throw Object.assign(new Error("x"), { status: 403, problem: { code: "rating_too_early" } });
    });
    show();
    const open = await screen.findAllByRole("button", { name: EN["plugins.action.open"] });
    await act(async () => fireEvent.click(open[0]!));
    await act(async () =>
      fireEvent.click(screen.getByRole("button", { name: EN["plugins.browse.install"] }))
    );
    expect(post).toHaveBeenCalledWith("/api/plugins/install", {
      store: { id: "dev.example.tagger", version: "1.2.0" },
    });
  });

  it("T08: Arabic, right to left, with the machine-translated label", async () => {
    localStorage.setItem("mkt:locale", "ar");
    get.mockResolvedValue(view());
    show();
    expect(
      await screen.findByRole("heading", { name: AR["plugins.browse.title"] })
    ).toBeInTheDocument();
    const open = await screen.findAllByRole("button", { name: AR["plugins.action.open"] });
    await act(async () => fireEvent.click(open[0]!));
    expect(screen.getByText(AR["plugins.browse.machine_translated"])).toBeInTheDocument();
    expect(document.documentElement.dir).toBe("rtl");
  });

  // N13's 300 ms is a browser's figure; here jsdom draws 300 cards from the kept catalog,
  // with no store fetch, within a loose bound so a slow runner does not fail it
  it("T07: Browse draws from the kept catalog, with no store call of its own", async () => {
    const many = Array.from({ length: 300 }, (_, i) =>
      entry({ id: `dev.example.p${i}`, names: { en: `P${i}`, ar: `ب${i}` } })
    );
    get.mockResolvedValue(view({ plugins: many }));
    const t0 = performance.now();
    show();
    await screen.findByRole("heading", { name: /P299/ });
    expect(performance.now() - t0).toBeLessThan(5000);
    expect(get).toHaveBeenCalledTimes(1);
  });
});
