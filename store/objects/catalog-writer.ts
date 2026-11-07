// CatalogWriter (story 54.15 §1, §8): the one writer of the signed lists. Regenerations are
// run one after another (a promise chain: a Durable Object's awaits on D1, R2 or the network
// let other calls in), so two changes never race to write the same version.
import { DurableObject } from "cloudflare:workers";
import type { Env } from "../web/env";
import { regenerate } from "../web/publish";

export class CatalogWriter extends DurableObject<Env> {
  private chain: Promise<unknown> = Promise.resolve();

  async regenerate(): Promise<{ catalog: number; revoked: number }> {
    const run = this.chain.then(() => regenerate(this.env));
    this.chain = run.catch(() => undefined);
    return run;
  }
}
