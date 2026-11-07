// The home's package rules in the Worker (plugind/crates/storewasm, built by
// scripts/build-wasm.sh into build/store.wasm): inspect a .mkplugin, re-pack it with store.sig.
import storeWasm from "../build/store.wasm";

interface Exports {
  memory: WebAssembly.Memory;
  alloc(n: number): number;
  dealloc(p: number, n: number): void;
  answer_len(p: number): number;
  inspect(p: number, n: number): number;
  repack(p: number, n: number, s: number, sn: number): number;
  pack(p: number, n: number): number;
  extract(p: number, n: number, q: number, qn: number): number;
}

let inst: Exports | undefined;
async function wasm(): Promise<Exports> {
  // a fresh instance per package would cost a compile; one per isolate is reused, its
  // buffers freed after each call
  inst ??= (await WebAssembly.instantiate(storeWasm, {})).exports as unknown as Exports;
  return inst;
}

export class PackageRefused extends Error {
  constructor(
    readonly code: string,
    readonly detail: string,
  ) {
    super(`${code}: ${detail}`);
  }
}

async function call(f: (w: Exports, args: number[]) => number, inputs: Uint8Array[]): Promise<Uint8Array> {
  const w = await wasm();
  const ptrs: [number, number][] = [];
  for (const b of inputs) {
    const p = w.alloc(b.length);
    new Uint8Array(w.memory.buffer, p, b.length).set(b);
    ptrs.push([p, b.length]);
  }
  const out = f(w, ptrs.flat());
  const total = w.answer_len(out);
  const view = new Uint8Array(w.memory.buffer, out, total);
  const status = view[0];
  const bodyBytes = view.slice(5, total);
  w.dealloc(out, total);
  for (const [p, n] of ptrs) w.dealloc(p, n);
  if (status !== 0) {
    const r = JSON.parse(new TextDecoder().decode(bodyBytes)) as { code: string; detail: string };
    throw new PackageRefused(r.code, r.detail);
  }
  return bodyBytes;
}

/** One file of a package, after the package's checks. */
export async function extract(pkg: Uint8Array, path: string): Promise<Uint8Array> {
  return call((w, a) => w.extract(a[0]!, a[1]!, a[2]!, a[3]!), [pkg, new TextEncoder().encode(path)]);
}

export interface Inspected {
  manifest: string;
  files_json: string;
  developer_sig: string | null;
  store_sig: string | null;
  files: { path: string; sha256: string; size: number }[];
  unpacked_bytes: number;
  imports: string[];
  exports: string[];
  audit: { code: string; detail: string } | null;
  strings: Record<string, Record<string, unknown>>;
  wasm_size: number;
}

export async function inspect(pkg: Uint8Array): Promise<Inspected> {
  const b = await call((w, a) => w.inspect(a[0]!, a[1]!), [pkg]);
  return JSON.parse(new TextDecoder().decode(b)) as Inspected;
}

export async function repack(pkg: Uint8Array, storeSig: string): Promise<Uint8Array> {
  return call((w, a) => w.repack(a[0]!, a[1]!, a[2]!, a[3]!), [pkg, new TextEncoder().encode(storeSig)]);
}

/** A package from files and signatures, in the home's writer's form (the store's tests). */
export async function pack(files: Record<string, Uint8Array>, sigs: Record<string, string> = {}): Promise<Uint8Array> {
  const b64 = (b: Uint8Array) => btoa(Array.from(b, (x) => String.fromCharCode(x)).join(""));
  const spec = { files: Object.fromEntries(Object.entries(files).map(([k, v]) => [k, b64(v)])), sigs };
  return call((w, a) => w.pack(a[0]!, a[1]!), [new TextEncoder().encode(JSON.stringify(spec))]);
}
