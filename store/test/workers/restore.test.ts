// Story 54.15 §9, T13: the nightly export is encrypted for the owner and restores. The drill
// exports the databases, decrypts with the owner's key, empties a database and replays the
// SQL, and the data is back. (A live restore is the runbook's: docs/runbooks/store-restore.md.)
import { describe, expect, it } from "vitest";
import { decrypt, dump, nightlyExport } from "../../web/exports";
import { E, account, developer } from "./helpers";

describe("backups (T13)", () => {
  it("exports every database encrypted, and a database restores from it", async () => {
    await developer();
    await account();
    const outboxId = `aud_restore_${Date.now() % 1e6}`;
    await E.DB_CATALOG.prepare(
      "INSERT INTO catalog_audit_outbox (id, actor, action, target, at, ip_prefix, result) VALUES (?, 'a_admin', 'maktaba_publisher.move', 'a1->a2:drill', '2026-11-26T00:00:00Z', '10.0.0.0/24', 'ok')",
    )
      .bind(outboxId)
      .run();
    const pair = (await crypto.subtle.generateKey(
      { name: "RSA-OAEP", modulusLength: 3072, publicExponent: new Uint8Array([1, 0, 1]), hash: "SHA-256" },
      true,
      ["wrapKey", "unwrapKey"],
    )) as CryptoKeyPair;
    const spki = btoa(String.fromCharCode(...new Uint8Array((await crypto.subtle.exportKey("spki", pair.publicKey)) as ArrayBuffer)));
    const pkcs8 = new Uint8Array((await crypto.subtle.exportKey("pkcs8", pair.privateKey)) as ArrayBuffer);
    const keys = await nightlyExport({ ...E, EXPORT_PUBLIC_KEY: spki });
    // store-catalog exports before store-accounts so an outbox row flushed between snapshots is never lost.
    expect(keys.map((k) => k.split("/")[1])).toEqual(["store-catalog", "store-accounts", "store-installs"]);
    const file = new Uint8Array(await (await E.PRIVATE.get(keys[0]!))!.arrayBuffer());
    // nothing readable without the owner's key, and no signing key in it
    expect(new TextDecoder().decode(file)).not.toContain("INSERT INTO");
    const sql = await decrypt(pkcs8, file);
    expect(sql).toContain("INSERT INTO developer_keys");
    expect(sql).toContain("INSERT INTO catalog_audit_outbox");
    expect(sql).not.toContain(String(E.STORE_SIGNING_KEY));

    const before = await E.DB_CATALOG.prepare("SELECT count(*) AS n FROM developer_keys").first<{ n: number }>();
    for (const t of [
      "developers",
      "developer_prefixes",
      "developer_keys",
      "plugins",
      "versions",
      "submissions",
      "revocations",
      "lists",
      "flagged_words",
      "catalog_audit_outbox",
    ]) {
      await E.DB_CATALOG.prepare(`DELETE FROM ${t}`).run();
    }
    const stmts = sql.split("\n").filter((l) => l.startsWith("INSERT INTO"));
    for (let i = 0; i < stmts.length; i += 50) await E.DB_CATALOG.batch(stmts.slice(i, i + 50).map((s) => E.DB_CATALOG.prepare(s)));
    const after = await E.DB_CATALOG.prepare("SELECT count(*) AS n FROM developer_keys").first<{ n: number }>();
    expect(after!.n).toBe(before!.n);
    const restoredOutbox = await E.DB_CATALOG.prepare("SELECT id, actor, action, target, result FROM catalog_audit_outbox WHERE id = ?")
      .bind(outboxId)
      .first<{ id: string; actor: string; action: string; target: string; result: string }>();
    expect(restoredOutbox).toEqual({
      id: outboxId,
      actor: "a_admin",
      action: "maktaba_publisher.move",
      target: "a1->a2:drill",
      result: "ok",
    });
    expect(await dump(E, "store-catalog")).toEqual(expect.stringContaining("INSERT INTO developers"));
  });
});
