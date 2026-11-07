// The owner's side of the nightly export (story 54.15 §9): decrypts an export with the RSA
// private key the owner keeps outside Cloudflare (PKCS #8 PEM or DER).
//
//   tsx scripts/decrypt-export.ts <private-key.pem> <file.sql.gz.enc> > restore.sql
//
// docs/runbooks/store-restore.md has the restore steps.
import { readFileSync } from "node:fs";
import { decrypt } from "../web/exports";

const [keyPath, filePath] = process.argv.slice(2);
if (!keyPath || !filePath) {
  console.error("usage: decrypt-export.ts <private-key.pem> <export file>");
  process.exit(2);
}
const raw = readFileSync(keyPath, "utf8");
const der = raw.includes("BEGIN")
  ? Uint8Array.from(atob(raw.replace(/-----[^-]+-----/g, "").replace(/\s+/g, "")), (c) => c.charCodeAt(0))
  : new Uint8Array(readFileSync(keyPath));
process.stdout.write(await decrypt(der, new Uint8Array(readFileSync(filePath))));
