// The part of Cloudflare D1's API the commerce code uses, so tests can run the same SQL on
// node:sqlite (testing/sqlite.ts). D1's batch() is one transaction: all or nothing.

export interface Stmt {
  bind(...values: unknown[]): Stmt;
  first<T = Record<string, unknown>>(): Promise<T | null>;
  all<T = Record<string, unknown>>(): Promise<{ results: T[] }>;
  run(): Promise<{ meta: { changes: number } }>;
}

export interface DB {
  prepare(sql: string): Stmt;
  batch(stmts: Stmt[]): Promise<unknown[]>;
}
