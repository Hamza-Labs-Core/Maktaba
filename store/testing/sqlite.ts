// A D1-shaped database on node:sqlite for tests: the migrations in migrations/commerce run
// on it as they would on D1, and batch() is one transaction.

import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { DatabaseSync } from "node:sqlite";

import type { DB, Stmt } from "../commerce/db.js";

class SqliteStmt implements Stmt {
  constructor(
    private readonly db: DatabaseSync,
    readonly sql: string,
    readonly values: unknown[] = []
  ) {}
  bind(...values: unknown[]): Stmt {
    return new SqliteStmt(this.db, this.sql, values);
  }
  private args() {
    return this.values.map((v) => (v === undefined ? null : typeof v === "boolean" ? Number(v) : v)) as never[];
  }
  async first<T>() {
    return (this.db.prepare(this.sql).get(...this.args()) as T | undefined) ?? null;
  }
  async all<T>() {
    return { results: this.db.prepare(this.sql).all(...this.args()) as T[] };
  }
  async run() {
    const r = this.db.prepare(this.sql).run(...this.args());
    return { meta: { changes: Number(r.changes) } };
  }
  runSync() {
    return this.db.prepare(this.sql).run(...this.args());
  }
}

export function testDB(): DB & { raw: DatabaseSync } {
  const raw = new DatabaseSync(":memory:");
  const dir = join(import.meta.dirname, "..", "migrations", "commerce");
  for (const f of readdirSync(dir).filter((f) => f.endsWith(".sql")).sort()) raw.exec(readFileSync(join(dir, f), "utf8"));
  return {
    raw,
    prepare: (sql) => new SqliteStmt(raw, sql),
    async batch(stmts) {
      raw.exec("BEGIN");
      try {
        const out = stmts.map((s) => (s as SqliteStmt).runSync());
        raw.exec("COMMIT");
        return out;
      } catch (e) {
        raw.exec("ROLLBACK");
        throw e;
      }
    },
  };
}
