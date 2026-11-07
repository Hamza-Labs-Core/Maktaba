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
