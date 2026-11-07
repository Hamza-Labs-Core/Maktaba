declare module "*.wasm" {
  const module: WebAssembly.Module;
  export default module;
}
declare module "argon2id/lib/setup.js" {
  const setup: (
    simd: (i: WebAssembly.Imports) => Promise<{ instance: WebAssembly.Instance }>,
    nosimd: (i: WebAssembly.Imports) => Promise<{ instance: WebAssembly.Instance }>,
  ) => Promise<unknown>;
  export default setup;
}
// the plugin SDK's manifest module (the flow check's, 54.16 C12) hashes with node:crypto,
// which the Worker has through nodejs_compat
declare module "node:crypto" {
  export function createHash(algorithm: string): { update(data: string): { digest(encoding: "hex"): string } };
}
