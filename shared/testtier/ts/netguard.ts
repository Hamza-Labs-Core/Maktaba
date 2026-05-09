// Vitest setup file for the unit tier (Story 20.1 AC1).
//
// Loaded once per vitest worker via `vitest.unit.config.ts ::
// setupFiles`. It replaces `globalThis.fetch` and `node:net.Socket`
// with throwing stubs so any unit test that tries to do I/O fails
// with a clear, AC1-aligned message rather than e.g. a flaky
// connection refused.

const message =
  "unit tests must not do I/O: network access blocked (Story 20.1 AC1)";

const blocked = (): never => {
  throw new Error(message);
};

// Patch the global fetch first — most modern code paths.
(globalThis as unknown as { fetch: unknown }).fetch = blocked;

// Patch raw sockets too, for code that bypasses fetch.
//
// Done lazily so the import doesn't blow up in a non-Node runtime
// (e.g. happy-dom / jsdom) where node:net isn't available.
try {
  // eslint-disable-next-line @typescript-eslint/no-require-imports
  const net = await import("node:net");
  class ForbiddenSocket {
    constructor() {
      throw new Error(message);
    }
  }
  (net as unknown as { Socket: unknown }).Socket = ForbiddenSocket;
} catch {
  // Non-Node runtime, nothing to patch.
}

export {};
