// The contract between the loader at /r/ and the receiver builds at /r/<level>/receiver.js.
// A build deployed today is loaded by the loaders of the next 12 months, so this shape only
// ever grows (a new optional field), never changes.

import type { CafContext, CafFramework, CafLoadRequest } from "../receiver/caf";

/** The custom message namespace (docs/developers/contributing/casting.md). Registered by the
 * loader before the context starts. */
export const NAMESPACE = "urn:x-cast:tv.maktaba.cast";

export interface BuildHost {
  framework: CafFramework;
  context: CafContext;
  /** The element the build draws its screens in. */
  root: HTMLElement;
  /** The Maktaba mark's URL, for the idle screen. */
  markURL: string;
  /** Registers the handler of the custom namespace's messages (the loader registered the
   * namespace before the context started). */
  onMessage: (fn: (data: unknown, senderId: string) => void) => void;
}

export interface BuildReceiver {
  onLoad(req: CafLoadRequest): Promise<CafLoadRequest | object>;
}

export interface BuildModule {
  start(host: BuildHost): BuildReceiver;
}
