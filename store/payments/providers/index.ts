// Every payment provider module the store can run. Adding a provider is its folder under
// payments/providers/<id>/ (index.ts and provider.json), one line here and the store
// admin's configuration (a payment_providers row and its Worker secrets): nothing else
// changes. tools/ci/payment_provider_allowlist.py keeps each provider's SDKs, API hosts and
// key prefixes inside its own folder.

import type { ProviderModule } from "../provider.js";
import { adyenModule } from "./adyen/index.js";
import { fakeModule } from "./fake/index.js";

export const PROVIDER_MODULES: readonly ProviderModule<any>[] = [fakeModule, adyenModule];
