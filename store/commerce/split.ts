// The 85/15 split (README §6.5): one function, one table of cases (testdata/split.json).
// Maktaba's 15 % is rounded down to the minor unit; the developer gets the rest, so the two
// always sum to the price.

import type { Money } from "../payments/provider.js";

export const MAKTABA_SHARE_BP = 1500;

export function split(price: Money): { maktaba: Money; developer: Money } {
  if (!Number.isSafeInteger(price.minor) || price.minor < 0) throw new RangeError("a price is a whole minor amount");
  const maktaba = Math.floor((price.minor * MAKTABA_SHARE_BP) / 10_000);
  return {
    maktaba: { minor: maktaba, currency: price.currency },
    developer: { minor: price.minor - maktaba, currency: price.currency },
  };
}
