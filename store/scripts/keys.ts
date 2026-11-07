// The owner's offline key tool (story 54.14 §1, Rollout): makes the plugin root keys and the
// store's online key, and signs plugins-root.json and store-keys.json. Run it on an offline
// machine; the .key files hold private seeds and never go near a network, a repository or a
// chat. Nothing here is used by the store at run time.
//
//   tsx scripts/keys.ts new <dir> <name>
//       a new Ed25519 key: <dir>/<name>.key (the seed, base64, mode 0600) and <dir>/<name>.pub
//       ({"id","public"}); prints the id
//   tsx scripts/keys.ts root --version N --key R1.key --key R2.key [--previous OLD.key ...]
//       plugins-root.json (threshold 1) listing the --key keys, signed by each of them and,
//       for a rotation, by the previous root's keys (root chaining)
//   tsx scripts/keys.ts store-keys --version N --root R1.key --months 13 \
//       --store S.pub [--entitlement E.pub] [--valid-from ISO] [--valid-months 12]
//       store-keys.json signed by the root key, listing each store and entitlement key

import { chmodSync, readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { fromB64, Signer, signEnvelope, toB64 } from "../signing/sig";

const args = process.argv.slice(2);
const flag = (name: string): string[] => {
  const out: string[] = [];
  for (let i = 0; i < args.length; i++) if (args[i] === `--${name}`) out.push(args[++i] ?? "");
  return out;
};
const one = (name: string, dflt?: string): string => {
  const v = flag(name)[0] ?? dflt;
  if (v === undefined) throw new Error(`--${name} is required`);
  return v;
};
const loadKey = (path: string) => Signer.fromSecret(readFileSync(path, "utf8"));
const loadPub = (path: string): { id: string; public: string } => JSON.parse(readFileSync(path, "utf8"));
const iso = (d: Date) => d.toISOString().replace(/\.\d{3}Z$/, "Z");
const addMonths = (d: Date, m: number) => {
  const x = new Date(d);
  x.setUTCMonth(x.getUTCMonth() + m);
  return x;
};

async function main(): Promise<void> {
  switch (args[0]) {
    case "new": {
      const [dir, name] = [args[1], args[2]];
      if (!dir || !name) throw new Error("usage: new <dir> <name>");
      const seed = crypto.getRandomValues(new Uint8Array(32));
      const s = await Signer.fromSeed(seed.slice());
      const keyPath = join(dir, `${name}.key`);
      writeFileSync(keyPath, toB64(seed) + "\n", { mode: 0o600, flag: "wx" });
      chmodSync(keyPath, 0o600);
      seed.fill(0);
      writeFileSync(join(dir, `${name}.pub`), JSON.stringify({ id: s.id, public: s.public }) + "\n", { flag: "wx" });
      console.log(s.id);
      return;
    }
    case "root": {
      const version = Number(one("version"));
      const keys = await Promise.all(flag("key").map(loadKey));
      const previous = await Promise.all(flag("previous").map(loadKey));
      if (keys.length < 1) throw new Error("at least one --key");
      const signed = { version, threshold: 1, keys: keys.map((k) => ({ id: k.id, public: k.public })) };
      process.stdout.write((await signEnvelope("root", signed, ...previous, ...keys)) + "\n");
      return;
    }
    case "store-keys": {
      const version = Number(one("version"));
      const root = await loadKey(one("root"));
      const from = new Date(one("valid-from", new Date().toISOString()));
      const validMonths = Number(one("valid-months", "12"));
      const list = [
        ...flag("store").map((p) => ({ ...loadPub(p), use: "store" })),
        ...flag("entitlement").map((p) => ({ ...loadPub(p), use: "entitlement" })),
      ].map((k) => ({ id: k.id, public: k.public, use: k.use, not_before: iso(from), not_after: iso(addMonths(from, validMonths)) }));
      if (list.length === 0) throw new Error("at least one --store key");
      for (const k of list) if (fromB64(k.public).length !== 32) throw new Error(`${k.id}: not an Ed25519 public key`);
      const signed = { version, expires: iso(addMonths(new Date(), Number(one("months", "13")))), keys: list };
      process.stdout.write((await signEnvelope("store-keys", signed, root)) + "\n");
      return;
    }
    default:
      console.error("usage: keys.ts new <dir> <name> | root ... | store-keys ... (see the top of this file)");
      process.exit(2);
  }
}

await main();
