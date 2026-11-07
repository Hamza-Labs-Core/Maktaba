// The providers the store runs, and which one a sale goes to (story 54.19 §2).
//
// The store admin configures providers (the `payment_providers` table of store-commerce):
// several at once, each enabled or not, with an order and, optionally, the buyer countries
// and currencies it is used for. A sale goes to the first enabled provider, in the admin's
// order, that sells to the buyer's country in the price's currency. A live provider is
// used only once its written confirmation that it allows the resale of third-party
// plugins is on record (`resale_confirmed_at`): the known constraint of every candidate.

import {
  type Capabilities,
  type Currency,
  type PaymentProvider,
  type ProviderDeps,
  type ProviderModule,
  ProviderError,
  secretName,
} from "./provider.js";

/** One row of payment_providers. */
export interface ProviderConfig {
  /** the module's id (its folder under payments/providers/); one row per module */
  id: string;
  enabled: boolean;
  /** lower first */
  priority: number;
  /** "test" (the provider's sandbox) or "live" */
  mode: "test" | "live";
  /** buyer countries this store uses it for (empty: every country the provider sells to) */
  regions: string[];
  /** currencies this store uses it for (empty: every currency the provider charges in) */
  currencies: Currency[];
  /** when the provider's written confirmation that it allows plugin resale was filed */
  resaleConfirmedAt: string | null;
  /** the module's own settings (merchant account, URL prefix); never a secret */
  settings: Record<string, unknown>;
}

export interface Choice {
  provider: PaymentProvider;
  config: ProviderConfig;
}

/** Why no provider takes a sale: the buyer page says "not sold in your country" or similar. */
export type NoProvider = "no_provider_for_region" | "no_provider_for_currency" | "no_provider";

export interface SaleNeeds {
  country: string;
  currency: Currency;
  /** a yearly product the provider renews */
  subscription?: boolean;
  /** a developer onboarded with these providers (their account refs); undefined: any */
  developerProviders?: string[];
}

const covers = (list: string[], v: string) => list.length === 0 || list.includes("*") || list.includes(v);

export class ProviderRegistry {
  private readonly built = new Map<string, Choice>();
  private readonly problems: string[] = [];

  constructor(
    modules: readonly ProviderModule<any>[],
    configs: readonly ProviderConfig[],
    env: Record<string, string | undefined>,
    deps: ProviderDeps
  ) {
    const byId = new Map(modules.map((m) => [m.id, m]));
    for (const cfg of configs) {
      const mod = byId.get(cfg.id);
      if (!mod) {
        this.problems.push(`${cfg.id}: no such provider module`);
        continue;
      }
      if (!cfg.enabled) continue;
      if (cfg.mode === "live" && !cfg.resaleConfirmedAt) {
        this.problems.push(`${cfg.id}: live without a written resale confirmation on record`);
        continue;
      }
      const secrets: Record<string, string> = {};
      const missing = mod.secrets.filter((s) => {
        const v = env[secretName(cfg.id, s)];
        if (v) secrets[s] = v;
        return !v;
      });
      if (missing.length) {
        this.problems.push(`${cfg.id}: missing secrets ${missing.map((s) => secretName(cfg.id, s)).join(", ")}`);
        continue;
      }
      try {
        const provider = mod.create(mod.parseConfig(cfg.settings), secrets, deps);
        if (provider.capabilities.id !== cfg.id) throw new ProviderError("config", "module id mismatch");
        this.built.set(cfg.id, { provider, config: cfg });
      } catch (e) {
        this.problems.push(`${cfg.id}: ${(e as Error).message}`);
      }
    }
  }

  /** Why configured providers are not in use (the store admin page lists them). */
  get issues(): readonly string[] {
    return this.problems;
  }

  /** An enabled provider by id: webhooks, refunds and payouts go to the one that took the sale. */
  get(id: string): Choice | undefined {
    return this.built.get(id);
  }

  /** Every enabled provider, in the admin's order. */
  list(): Choice[] {
    return [...this.built.values()].sort(
      (a, b) => a.config.priority - b.config.priority || a.config.id.localeCompare(b.config.id)
    );
  }

  /** The provider a sale goes to, or why none does. */
  choose(needs: SaleNeeds): Choice | NoProvider {
    const all = this.list();
    if (all.length === 0) return "no_provider";
    const forRegion = all.filter(
      (c) => covers(c.config.regions, needs.country) && covers(c.provider.capabilities.regions, needs.country)
    );
    if (forRegion.length === 0) return "no_provider_for_region";
    const fits = forRegion.filter(
      (c) =>
        covers(c.config.currencies, needs.currency) &&
        c.provider.capabilities.currencies.includes(needs.currency) &&
        (!needs.subscription || c.provider.capabilities.subscriptions) &&
        (needs.developerProviders === undefined ||
          c.provider.capabilities.payouts === "store_batches" ||
          needs.developerProviders.includes(c.config.id))
    );
    return fits[0] ?? "no_provider_for_currency";
  }

  /** The capabilities of every enabled provider (the store admin page and the console). */
  capabilities(): Capabilities[] {
    return this.list().map((c) => c.provider.capabilities);
  }
}

/** A payment_providers row as D1 returns it. */
export function configFromRow(row: Record<string, unknown>): ProviderConfig {
  const list = (v: unknown) => (typeof v === "string" && v ? (JSON.parse(v) as string[]) : []);
  return {
    id: String(row.id),
    enabled: Number(row.enabled) === 1,
    priority: Number(row.priority ?? 100),
    mode: row.mode === "live" ? "live" : "test",
    regions: list(row.regions),
    currencies: list(row.currencies),
    resaleConfirmedAt: typeof row.resale_confirmed_at === "string" ? row.resale_confirmed_at : null,
    settings: typeof row.settings === "string" && row.settings ? JSON.parse(row.settings) : {},
  };
}
