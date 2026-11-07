// Human review (story 54.16 §3, §5) on the store's own admin pages: the queue (first
// submissions before updates, oldest first, flagged ones marked), a submission's evidence,
// and the decisions: approve (countersign `reviewed`), reject (reasons from a fixed list and
// notes), ask the developer (the clock pauses). Every route needs a store reviewer signed in
// with a passkey; every decision a passkey confirmed in the last 5 minutes, and an audit row.
import { type Ctx, audit, developer, reauthed, reauthedPasskey, staff } from "./auth";
import { type Env, iso, now } from "./env";
import { body, json, optStr, refuse, str } from "./http";
import { sendMail } from "./mail";
import { countersign } from "./publish";

export const REJECT_REASONS = ["misleading", "does_not_work", "asks_too_much", "harmful_outputs", "malware", "policy", "strings"] as const;

/** First response targets (54.16 §5, N12): 5 business days for a new plugin, 2 for an update. */
export const SLA_DAYS = { first: 5, update: 2 };

interface QueueRow {
  id: string;
  plugin: string;
  version: string;
  status: string;
  first_version: number;
  route_reasons: string;
  flags: string;
  checked_at: string | null;
  created: string;
  paused_ms: number;
}

/** Business days between two times (Saturday and Sunday not counted). */
export function businessDays(from: Date, to: Date): number {
  let days = 0;
  const d = new Date(from);
  while (d < to) {
    d.setUTCDate(d.getUTCDate() + 1);
    if (d > to) break;
    const wd = d.getUTCDay();
    if (wd !== 0 && wd !== 6) days++;
  }
  return days;
}

function waited(r: QueueRow, at: Date): number {
  // the SLA clock starts when the report is ready, not at upload, and pauses while the
  // developer is asked (54.16 edge cases)
  const start = new Date(Date.parse(r.checked_at ?? r.created) + r.paused_ms);
  return businessDays(start, at);
}

/** GET /v1/admin/queue. */
export async function queue(ctx: Ctx): Promise<Response> {
  staff(ctx, "store_reviewer");
  const rows = await ctx.env.DB_CATALOG.prepare(
    "SELECT id, plugin, version, status, first_version, route_reasons, flags, checked_at, created, paused_ms FROM submissions WHERE status IN ('in_review', 'asked')",
  ).all<QueueRow>();
  const at = now();
  const items = rows.results
    .map((r) => {
      const days = waited(r, at);
      const sla = r.first_version ? SLA_DAYS.first : SLA_DAYS.update;
      return {
        id: r.id,
        plugin: r.plugin,
        version: r.version,
        status: r.status,
        first_version: !!r.first_version,
        reasons: JSON.parse(r.route_reasons) as string[],
        flags: JSON.parse(r.flags) as string[],
        waiting_since: r.checked_at ?? r.created,
        business_days: days,
        over_sla: days > sla,
      };
    })
    // the asked ones wait for their developer; first submissions before updates; oldest first
    .sort(
      (a, b) =>
        Number(a.status === "asked") - Number(b.status === "asked") ||
        Number(b.first_version) - Number(a.first_version) ||
        a.waiting_since.localeCompare(b.waiting_since),
    );
  return json({ items, sla_days: SLA_DAYS });
}

/** GET /v1/admin/submissions/{id}: the evidence (manifest, reasons, report, diff, files). */
export async function submission(ctx: Ctx, id: string): Promise<Response> {
  const a = staff(ctx, "store_reviewer");
  const s = await ctx.env.DB_CATALOG.prepare("SELECT * FROM submissions WHERE id = ?")
    .bind(id)
    .first<Record<string, unknown> & { plugin: string; version: string; report_key: string; uploaded_by: string; source_key: string }>();
  if (!s) refuse(404, "not_found");
  const v = await ctx.env.DB_CATALOG.prepare("SELECT notes_en, notes_ar, manifest FROM versions WHERE plugin = ? AND version = ?")
    .bind(s!.plugin, s!.version)
    .first();
  const report = s!.report_key ? JSON.parse((await (await ctx.env.PRIVATE.get(s!.report_key))?.text()) ?? "null") : null;
  const src = await ctx.env.PRIVATE.head(s!.source_key);
  const dev = await ctx.env.DB_ACCOUNTS.prepare("SELECT display_name FROM accounts WHERE id = ?")
    .bind(s!.uploaded_by)
    .first<{ display_name: string }>();
  await audit(ctx, a.id, "review.open", id);
  return json({
    submission: s,
    version: v,
    report,
    developer: dev?.display_name ?? "",
    source: src ? { size: src.size, url: `/v1/admin/submissions/${id}/source` } : null,
  });
}

/** GET /v1/admin/submissions/{id}/source: the source archive, for a reviewer only (never public). */
export async function source(ctx: Ctx, id: string): Promise<Response> {
  const a = staff(ctx, "store_reviewer");
  const s = await ctx.env.DB_CATALOG.prepare("SELECT source_key FROM submissions WHERE id = ?").bind(id).first<{ source_key: string }>();
  const obj = s ? await ctx.env.PRIVATE.get(s.source_key) : null;
  if (!obj) refuse(404, "not_found");
  await audit(ctx, a.id, "review.source", id);
  return new Response(obj!.body, {
    headers: {
      "Content-Type": "application/octet-stream",
      "Content-Disposition": `attachment; filename="${id}-source.tar.zst"`,
      "Cache-Control": "no-store",
    },
  });
}

async function decided(env: Env, s: { plugin: string; version: string; uploaded_by: string }, decision: string): Promise<void> {
  const a = await env.DB_ACCOUNTS.prepare("SELECT email FROM accounts WHERE id = ?").bind(s.uploaded_by).first<{ email: string }>();
  if (a) await sendMail(env, { to: a.email, template: "submission_decided", params: { plugin: s.plugin, version: s.version, decision } });
}

async function reviewable(ctx: Ctx, id: string) {
  const s = await ctx.env.DB_CATALOG.prepare("SELECT id, plugin, version, uploaded_by, status FROM submissions WHERE id = ?")
    .bind(id)
    .first<{ id: string; plugin: string; version: string; uploaded_by: string; status: string }>();
  if (!s) refuse(404, "not_found");
  if (s!.status !== "in_review" && s!.status !== "asked") refuse(409, "submission_state", { status: s!.status });
  return s!;
}

/** POST /v1/admin/submissions/{id}/approve: countersigned `reviewed`, the catalog regenerates. */
export async function approve(ctx: Ctx, id: string): Promise<Response> {
  const a = staff(ctx, "store_reviewer");
  reauthedPasskey(ctx);
  const s = await reviewable(ctx, id);
  if (s.uploaded_by === a.id) refuse(403, "own_submission");
  const out = await countersign(ctx.env, id, "reviewed");
  await ctx.env.DB_CATALOG.prepare(
    "UPDATE submissions SET status = 'approved', reviewer = ?, decision = 'approved', decided_at = ? WHERE id = ?",
  )
    .bind(a.id, iso(now()), id)
    .run();
  const v = await ctx.env.CATALOG_WRITER.get(ctx.env.CATALOG_WRITER.idFromName("main")).regenerate();
  await audit(ctx, a.id, "review.approve", `${id}:${s.plugin}@${s.version}`);
  await decided(ctx.env, s, "approved");
  return json({ status: "approved", sha256: out.sha256, catalog_version: v.catalog });
}

/** POST /v1/admin/submissions/{id}/reject {reasons, notes}. */
export async function reject(ctx: Ctx, id: string): Promise<Response> {
  const a = staff(ctx, "store_reviewer");
  reauthedPasskey(ctx);
  const b = await body(ctx.req);
  const reasons = Array.isArray(b.reasons) ? (b.reasons as string[]) : [];
  if (reasons.length === 0 || reasons.some((r) => !(REJECT_REASONS as readonly string[]).includes(r)))
    refuse(400, "reasons_invalid", { allowed: REJECT_REASONS });
  const s = await reviewable(ctx, id);
  if (s.uploaded_by === a.id) refuse(403, "own_submission");
  const notes = optStr(b.notes, "notes", 4000) ?? "";
  await ctx.env.DB_CATALOG.batch([
    ctx.env.DB_CATALOG.prepare(
      "UPDATE submissions SET status = 'rejected', reviewer = ?, decision = 'rejected', reasons = ?, notes = ?, decided_at = ? WHERE id = ?",
    ).bind(a.id, JSON.stringify(reasons), notes, iso(now()), id),
    ctx.env.DB_CATALOG.prepare("UPDATE versions SET status = 'rejected' WHERE plugin = ? AND version = ?").bind(s.plugin, s.version),
  ]);
  await audit(ctx, a.id, "review.reject", `${id}:${reasons.join(",")}`);
  await decided(ctx.env, s, `rejected (${reasons.join(", ")})`);
  return json({ status: "rejected" });
}

/** POST /v1/admin/submissions/{id}/ask {question}: the clock pauses until the developer answers. */
export async function ask(ctx: Ctx, id: string): Promise<Response> {
  const a = staff(ctx, "store_reviewer");
  reauthedPasskey(ctx);
  const b = await body(ctx.req);
  const q = str(b.question, "question", 4000);
  const s = await reviewable(ctx, id);
  await ctx.env.DB_CATALOG.prepare("UPDATE submissions SET status = 'asked', question = ?, asked_at = ?, reviewer = ? WHERE id = ?")
    .bind(q, iso(now()), a.id, id)
    .run();
  await audit(ctx, a.id, "review.ask", id);
  await decided(ctx.env, s, "a question from the reviewer");
  return json({ status: "asked" });
}

/** POST /v1/submissions/{id}/answer {answer}: the developer's answer; back to the queue. */
export async function answer(ctx: Ctx, id: string): Promise<Response> {
  const a = developer(ctx);
  reauthed(ctx);
  const b = await body(ctx.req);
  const s = await ctx.env.DB_CATALOG.prepare("SELECT uploaded_by, status, asked_at FROM submissions WHERE id = ?")
    .bind(id)
    .first<{ uploaded_by: string; status: string; asked_at: string }>();
  if (!s || s.uploaded_by !== a.id) {
    await audit(ctx, a.id, "submission.answer", id, "forbidden");
    refuse(s ? 403 : 404, s ? "forbidden" : "not_found");
  }
  if (s!.status !== "asked") refuse(409, "submission_state", { status: s!.status });
  const paused = now().getTime() - Date.parse(s!.asked_at);
  await ctx.env.DB_CATALOG.prepare("UPDATE submissions SET status = 'in_review', answer = ?, paused_ms = paused_ms + ? WHERE id = ?")
    .bind(str(b.answer, "answer", 4000), paused, id)
    .run();
  await audit(ctx, a.id, "submission.answer", id);
  return json({ status: "in_review" });
}

/** POST /v1/admin/flagged-words {word}: a word a plugin id is flagged for (squatting). */
export async function flagWord(ctx: Ctx): Promise<Response> {
  const a = staff(ctx, "store_reviewer");
  reauthedPasskey(ctx);
  const w = str((await body(ctx.req)).word, "word", 60).toLowerCase();
  await ctx.env.DB_CATALOG.prepare("INSERT OR IGNORE INTO flagged_words (word, added_by, at) VALUES (?, ?, ?)")
    .bind(w, a.id, iso(now()))
    .run();
  await audit(ctx, a.id, "review.flag_word", w);
  return json({ status: "added" }, 201);
}

/** The daily backlog alert (§5): the store's own email to its admins, no cloud monitoring. */
export async function backlogAlert(env: Env): Promise<number> {
  const rows = await env.DB_CATALOG.prepare(
    "SELECT id, plugin, version, status, first_version, route_reasons, flags, checked_at, created, paused_ms FROM submissions WHERE status = 'in_review'",
  ).all<QueueRow>();
  const at = now();
  const over = rows.results.filter((r) => waited(r, at) > (r.first_version ? SLA_DAYS.first : SLA_DAYS.update));
  if (!over.length) return 0;
  const admins = await env.DB_ACCOUNTS.prepare(
    "SELECT a.email FROM accounts a JOIN roles r ON r.account = a.id WHERE r.role = 'store_admin' AND a.status = 'active'",
  ).all<{ email: string }>();
  const oldest = Math.max(...over.map((r) => waited(r, at)));
  for (const a of admins.results)
    await sendMail(env, {
      to: a.email,
      template: "review_backlog",
      params: { count: String(over.length), age: `${oldest} business days` },
    });
  return over.length;
}
