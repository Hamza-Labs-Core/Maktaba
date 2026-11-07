// Passwords: Argon2id (m = 19 MiB, t = 2, p = 1, the OWASP minimum; story 54.15 §2), in
// WebAssembly (openpgpjs' argon2id). PBKDF2 is not used: Workers cap it at 100,000 rounds.
import setupWasm from "argon2id/lib/setup.js";
import simd from "argon2id/dist/simd.wasm";
import nosimd from "argon2id/dist/no-simd.wasm";
import { b64url, fromB64url, randomBytes, same } from "./crypto";

type Hash = (p: {
  password: Uint8Array;
  salt: Uint8Array;
  parallelism: number;
  passes: number;
  memorySize: number;
  tagLength: number;
}) => Uint8Array;
let loaded: Promise<Hash> | undefined;
const argon = () =>
  (loaded ??= setupWasm(
    async (i: WebAssembly.Imports) => ({ instance: await WebAssembly.instantiate(simd, i) }),
    async (i: WebAssembly.Imports) => ({ instance: await WebAssembly.instantiate(nosimd, i) }),
  ) as Promise<Hash>);

const M = 19456;
const T = 2;
const P = 1;
const enc = new TextEncoder();
const FORMAT = /^\$argon2id\$v=19\$m=(\d+),t=(\d+),p=(\d+)\$([A-Za-z0-9_-]+)\$([A-Za-z0-9_-]+)$/;

export const MIN_PASSWORD = 10;

export async function hashPassword(password: string): Promise<string> {
  const salt = randomBytes(16);
  const tag = (await argon())({
    password: enc.encode(password.normalize("NFC")),
    salt,
    parallelism: P,
    passes: T,
    memorySize: M,
    tagLength: 32,
  });
  return ["", "argon2id", "v=19", `m=${M},t=${T},p=${P}`, b64url(salt), b64url(tag)].join("$");
}

export async function verifyPassword(password: string, stored: string | null): Promise<boolean> {
  const m = FORMAT.exec(stored ?? "");
  if (!m) {
    // spend the same time, so a missing account or password does not answer faster
    (await argon())({ password: enc.encode(password), salt: new Uint8Array(16), parallelism: P, passes: T, memorySize: M, tagLength: 32 });
    return false;
  }
  const tag = (await argon())({
    password: enc.encode(password.normalize("NFC")),
    salt: fromB64url(m[4]!),
    parallelism: Number(m[3]),
    passes: Number(m[2]),
    memorySize: Number(m[1]),
    tagLength: 32,
  });
  return same(tag, fromB64url(m[5]!));
}
