// The review pages (story 54.16 §3) at plugins.maktaba.tv/admin: the queue, and a
// submission's evidence with the decisions. English and Arabic. Every decision asks for a
// passkey confirmed in the last 5 minutes (the server checks; this page asks when told).
import { useEffect, useState } from "react";
import { ApiError, api, getPasskey } from "../api";
import { type Lang, translate } from "../i18n";

export const REVIEW_WORDS = {
  en: {
    "review.title": "Review queue",
    "review.empty": "Nothing waits for review.",
    "review.first": "First submission",
    "review.update": "Update",
    "review.over_sla": "Past the target",
    "review.asked": "Waiting for the developer",
    "review.days": "{n} business days",
    "review.open": "Open",
    "review.back": "Back to the queue",
    "review.capabilities": "Capabilities and the developer's reasons",
    "review.checks": "Automated checks",
    "review.diff": "Changes since the last version",
    "review.flow": "The flow",
    "review.flow_events": "Published events",
    "review.flow_subscriptions": "Subscriptions",
    "review.flow_chart": "The flow's chart: its plugins' stages, the Maktaba events they start from and the events between them",
    "review.source": "Download the source archive",
    "review.approve": "Approve",
    "review.reject": "Reject",
    "review.ask": "Ask the developer",
    "review.notes": "Notes",
    "review.question": "Question",
    "review.reasons": "Reasons",
    "review.reauth": "Confirm with your passkey to decide",
    "review.done": "Decision saved.",
    "reason.misleading": "Misleading",
    "reason.does_not_work": "Does not work",
    "reason.asks_too_much": "Asks for more than it needs",
    "reason.harmful_outputs": "Harmful outputs",
    "reason.malware": "Malware",
    "reason.policy": "Policy",
    "reason.strings": "Strings",
    "lang.switch": "العربية",
  },
  ar: {
    "review.title": "طابور المراجعة",
    "review.empty": "لا شيء ينتظر المراجعة.",
    "review.first": "أول طلب",
    "review.update": "تحديث",
    "review.over_sla": "تجاوز الهدف",
    "review.asked": "بانتظار المطوّر",
    "review.days": "{n} أيام عمل",
    "review.open": "فتح",
    "review.back": "العودة إلى الطابور",
    "review.capabilities": "القدرات وأسباب المطوّر",
    "review.checks": "الفحوص الآلية",
    "review.diff": "التغييرات منذ الإصدار السابق",
    "review.flow": "التدفق",
    "review.flow_events": "الأحداث المنشورة",
    "review.flow_subscriptions": "الاشتراكات",
    "review.flow_chart": "مخطط التدفق: مراحل إضافاته، وأحداث مكتبة التي تبدأ منها، والأحداث بينها",
    "review.source": "نزّل أرشيف المصدر",
    "review.approve": "قبول",
    "review.reject": "رفض",
    "review.ask": "اسأل المطوّر",
    "review.notes": "ملاحظات",
    "review.question": "السؤال",
    "review.reasons": "الأسباب",
    "review.reauth": "أكّد بمفتاح المرور لتقرّر",
    "review.done": "حُفظ القرار.",
    "reason.misleading": "مضلِّلة",
    "reason.does_not_work": "لا تعمل",
    "reason.asks_too_much": "تطلب أكثر مما تحتاج",
    "reason.harmful_outputs": "مخرجات ضارة",
    "reason.malware": "برمجية خبيثة",
    "reason.policy": "السياسة",
    "reason.strings": "النصوص",
    "lang.switch": "English",
  },
} as const;

const REASONS = ["misleading", "does_not_work", "asks_too_much", "harmful_outputs", "malware", "policy", "strings"];

function t(lang: Lang, k: string, p: Record<string, string | number> = {}): string {
  const w = (REVIEW_WORDS[lang] as Record<string, string>)[k];
  return w ? w.replace(/\{(\w+)\}/g, (_, x: string) => String(p[x] ?? "")) : translate(lang, k, p);
}

interface Item {
  id: string;
  plugin: string;
  version: string;
  status: string;
  first_version: boolean;
  reasons: string[];
  flags: string[];
  business_days: number;
  over_sla: boolean;
}

async function withPasskey<T>(f: () => Promise<T>): Promise<T> {
  try {
    return await f();
  } catch (e) {
    if (!(e instanceof ApiError) || e.code !== "reauth_required") throw e;
    const o = await api<{ challenge_id: string; publicKey: { challenge: string } }>("POST", "/v1/reauth/passkey/options");
    await api("POST", "/v1/reauth", { challenge_id: o.challenge_id, credential: await getPasskey(o.publicKey) });
    return f();
  }
}

export function Review({ initialLang = "en" }: { initialLang?: Lang }) {
  const [lang, setLang] = useState<Lang>(initialLang);
  const [items, setItems] = useState<Item[] | null>(null);
  const [open, setOpen] = useState<string | null>(null);
  const [error, setError] = useState("");
  const load = () =>
    api<{ items: Item[] }>("GET", "/v1/admin/queue").then(
      (r) => setItems(r.items),
      (e) => setError(String((e as ApiError).code ?? e)),
    );
  useEffect(() => {
    void load();
  }, []);
  useEffect(() => {
    document.documentElement.lang = lang;
    document.documentElement.dir = lang === "ar" ? "rtl" : "ltr";
  }, [lang]);
  return (
    <main>
      <header className="bar">
        <strong>{t(lang, "review.title")}</strong>
        <span className="spacer" />
        <button type="button" onClick={() => setLang(lang === "en" ? "ar" : "en")}>
          {t(lang, "lang.switch")}
        </button>
      </header>
      {error && <p role="alert">{translate(lang, `error.${error}`)}</p>}
      {open ? (
        <Submission id={open} lang={lang} onBack={() => (setOpen(null), void load())} />
      ) : items === null ? null : items.length === 0 ? (
        <p>{t(lang, "review.empty")}</p>
      ) : (
        <table>
          <tbody>
            {items.map((i) => (
              <tr key={i.id}>
                <td dir="ltr">
                  <code>
                    {i.plugin} {i.version}
                  </code>
                </td>
                <td>{t(lang, i.first_version ? "review.first" : "review.update")}</td>
                <td>{i.status === "asked" ? t(lang, "review.asked") : t(lang, "review.days", { n: i.business_days })}</td>
                <td>{i.over_sla && <strong className="warn">{t(lang, "review.over_sla")}</strong>}</td>
                <td dir="ltr">{[...i.reasons, ...i.flags].join(", ")}</td>
                <td>
                  <button type="button" onClick={() => setOpen(i.id)}>
                    {t(lang, "review.open")}
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </main>
  );
}

interface Detail {
  submission: { id: string; plugin: string; version: string; status: string };
  version: { notes_en: string; notes_ar: string; manifest: string } | null;
  report: {
    checks: { id: string; result: string; code?: string; detail?: string; detail_ar?: string }[];
    capabilities: { required: string[]; optional: string[] };
    diff?: Record<string, unknown>;
    flow?: { id: string; version: string; members: string[]; events: string[]; subscriptions: string[] };
  } | null;
  flow_chart: string;
  developer: string;
  source: { size: number; url: string } | null;
}

function Submission({ id, lang, onBack }: { id: string; lang: Lang; onBack: () => void }) {
  const [d, setD] = useState<Detail | null>(null);
  const [reasons, setReasons] = useState<string[]>([]);
  const [notes, setNotes] = useState("");
  const [msg, setMsg] = useState("");
  useEffect(() => {
    void api<Detail>("GET", `/v1/admin/submissions/${id}`).then(setD);
  }, [id]);
  if (!d) return null;
  const decide = (path: string, body: unknown) =>
    withPasskey(() => api("POST", `/v1/admin/submissions/${id}/${path}`, body)).then(
      () => setMsg(t(lang, "review.done")),
      (e) => setMsg(translate(lang, `error.${(e as ApiError).code}`)),
    );
  return (
    <section>
      <button type="button" onClick={onBack}>
        {t(lang, "review.back")}
      </button>
      <h1 dir="ltr">
        {d.submission.plugin} {d.submission.version}
      </h1>
      <p>
        <bdi>{d.developer}</bdi>
      </p>
      <h2>{t(lang, "review.capabilities")}</h2>
      <ul dir="ltr">
        {[...(d.report?.capabilities.required ?? []), ...(d.report?.capabilities.optional ?? [])].map((c) => (
          <li key={c}>
            <code>{c}</code>
          </li>
        ))}
      </ul>
      <p dir="ltr">{d.version?.notes_en}</p>
      <p dir="rtl" lang="ar">
        {d.version?.notes_ar}
      </p>
      <h2>{t(lang, "review.checks")}</h2>
      <ul dir="ltr">
        {d.report?.checks.map((c, n) => (
          <li key={n}>
            {c.id} {c.result} {c.code} {lang === "ar" && c.detail_ar ? <bdi dir="rtl">{c.detail_ar}</bdi> : c.detail}
          </li>
        ))}
      </ul>
      {d.report?.flow && (
        <>
          <h2>
            {t(lang, "review.flow")} <bdi dir="ltr">{d.report.flow.id} {d.report.flow.version}</bdi>
          </h2>
          {d.flow_chart && (
            <img
              className="flow-chart"
              alt={t(lang, "review.flow_chart")}
              src={`data:image/svg+xml;charset=utf-8,${encodeURIComponent(d.flow_chart)}`}
            />
          )}
          <h3>{t(lang, "review.flow_events")}</h3>
          <ul dir="ltr">
            {d.report.flow.events.map((e) => (
              <li key={e}>
                <code>{e}</code>
              </li>
            ))}
          </ul>
          <h3>{t(lang, "review.flow_subscriptions")}</h3>
          <ul dir="ltr">
            {d.report.flow.subscriptions.map((e) => (
              <li key={e}>
                <code>{e}</code>
              </li>
            ))}
          </ul>
        </>
      )}
      {d.report?.diff && (
        <>
          <h2>{t(lang, "review.diff")}</h2>
          <pre dir="ltr">{JSON.stringify(d.report.diff, null, 2)}</pre>
        </>
      )}
      {d.source && (
        <p>
          <a href={d.source.url}>{t(lang, "review.source")}</a>
        </p>
      )}
      <fieldset>
        <legend>{t(lang, "review.reasons")}</legend>
        {REASONS.map((r) => (
          <label key={r}>
            <input
              type="checkbox"
              checked={reasons.includes(r)}
              onChange={(e) => setReasons(e.target.checked ? [...reasons, r] : reasons.filter((x) => x !== r))}
            />
            {t(lang, `reason.${r}`)}
          </label>
        ))}
      </fieldset>
      <label>
        {t(lang, "review.notes")}
        <textarea value={notes} onChange={(e) => setNotes(e.target.value)} rows={3} />
      </label>
      <p className="note">{t(lang, "review.reauth")}</p>
      <button type="button" onClick={() => void decide("approve", {})}>
        {t(lang, "review.approve")}
      </button>
      <button type="button" disabled={reasons.length === 0} onClick={() => void decide("reject", { reasons, notes })}>
        {t(lang, "review.reject")}
      </button>
      <button type="button" disabled={!notes} onClick={() => void decide("ask", { question: notes })}>
        {t(lang, "review.ask")}
      </button>
      {msg && <p role="status">{msg}</p>}
    </section>
  );
}
