// The store's email (story 54.15 §1): every message in English and Arabic, sent from the
// `store-mail` queue so a slow provider never holds a request. The provider is the owner's
// choice (README §14 item 10): MAIL_PROVIDER "postmark" sends through Postmark's HTTP API
// with MAIL_API_KEY; "outbox" writes each message to the private bucket (tests, and a store
// that is not open yet). A message carries no secret but its own link.
import type { Env } from "./env";

export type Template =
  | "verify_email"
  | "email_changed"
  | "signin_locked"
  | "mfa_reset_started"
  | "mfa_reset_done"
  | "prefix_lapsed"
  | "review_backlog"
  | "report_urgent"
  | "submission_decided"
  | "report_resolved";

export interface Mail {
  to: string;
  template: Template;
  params: Record<string, string>;
}

const WORDS: Record<Template, { en: [string, string]; ar: [string, string] }> = {
  verify_email: {
    en: ["Check your email to finish signing up", "Open this link within 24 hours to verify your email for Maktaba Plugins: {link}"],
    ar: ["تحقّق من بريدك الإلكتروني لإكمال التسجيل", "افتح هذا الرابط خلال 24 ساعة لتأكيد بريدك في إضافات مكتبة: {link}"],
  },
  email_changed: {
    en: [
      "Your Maktaba Plugins email is changing",
      "Someone asked to change this account's email to {email}. If it was not you, sign in and secure your account.",
    ],
    ar: ["يتغيّر بريد حسابك في إضافات مكتبة", "طُلب تغيير بريد هذا الحساب إلى {email}. إن لم تكن أنت، فسجّل الدخول وأمّن حسابك."],
  },
  signin_locked: {
    en: [
      "Sign-in to your account is paused",
      "After 5 failed sign-ins, signing in to this account is paused until {until}. If it was not you, change your password.",
    ],
    ar: [
      "أُوقف تسجيل الدخول إلى حسابك مؤقتًا",
      "بعد 5 محاولات فاشلة، أُوقف تسجيل الدخول إلى هذا الحساب حتى {until}. إن لم تكن أنت، فغيّر كلمة المرور.",
    ],
  },
  mfa_reset_started: {
    en: [
      "A reset of your sign-in factors was started",
      "A store admin started resetting this account's sign-in factors. It completes on {date}. If you did not ask for it, reply to this message.",
    ],
    ar: [
      "بدأت إعادة تعيين عوامل تسجيل دخولك",
      "بدأ مسؤول المتجر إعادة تعيين عوامل تسجيل الدخول لهذا الحساب، وتكتمل في {date}. إن لم تطلب ذلك، فردّ على هذه الرسالة.",
    ],
  },
  mfa_reset_done: {
    en: [
      "Your sign-in factors were reset",
      "This account's sign-in factors were removed. Add a new one before you publish: your signing keys stay suspended until you do.",
    ],
    ar: [
      "أُعيد تعيين عوامل تسجيل دخولك",
      "أُزيلت عوامل تسجيل الدخول لهذا الحساب. أضف عاملًا جديدًا قبل النشر: تبقى مفاتيح التوقيع معلّقة حتى تفعل.",
    ],
  },
  prefix_lapsed: {
    en: [
      "Your id prefix could not be verified",
      "The DNS record maktaba-developer for {prefix} is gone. New submissions under it wait until it is back; listed plugins stay.",
    ],
    ar: [
      "تعذّر التحقق من بادئة معرّفك",
      "اختفى سجل DNS ‏maktaba-developer للبادئة {prefix}. تنتظر الطلبات الجديدة تحتها حتى يعود؛ وتبقى الإضافات المنشورة.",
    ],
  },
  review_backlog: {
    en: [
      "The review queue is past its target",
      "{count} submissions have waited longer than the review target. The oldest has waited {age}.",
    ],
    ar: ["تجاوز طابور المراجعة هدفه", "انتظر {count} من الطلبات أكثر من هدف المراجعة. وأقدمها ينتظر منذ {age}."],
  },
  report_urgent: {
    en: ["A plugin was reported: {reason}", "A {reason} report on {plugin} waits for triage (target: 24 hours)."],
    ar: ["أُبلغ عن إضافة: {reason}", "بلاغ {reason} عن {plugin} ينتظر الفرز (الهدف: 24 ساعة)."],
  },
  report_resolved: {
    en: ["Your report was looked at", "Thank you for your report. The store looked at it: {outcome}."],
    ar: ["نُظر في بلاغك", "شكرًا على بلاغك. نظر فيه المتجر: {outcome}."],
  },
  submission_decided: {
    en: ["{plugin} {version}: {decision}", "Your submission was {decision}. See the developer console for details."],
    ar: ["{plugin} {version}: {decision}", "قُرّر في طلبك: {decision}. التفاصيل في لوحة المطوّر."],
  },
};

const fill = (s: string, p: Record<string, string>) => s.replace(/\{(\w+)\}/g, (_, k: string) => p[k] ?? "");

/** The message's subject and text, English then Arabic. */
export function render(m: Mail): { subject: string; text: string } {
  const w = WORDS[m.template];
  return {
    subject: `${fill(w.en[0], m.params)} / ${fill(w.ar[0], m.params)}`,
    text: `${fill(w.en[1], m.params)}\n\n${fill(w.ar[1], m.params)}\n\n— Maktaba Plugins / إضافات مكتبة\n`,
  };
}

export async function sendMail(env: Env, m: Mail): Promise<void> {
  await env.Q_MAIL.send(m);
}

/** The `store-mail` queue's consumer. */
export async function deliver(env: Env, m: Mail): Promise<void> {
  const { subject, text } = render(m);
  if (env.MAIL_PROVIDER === "outbox") {
    await env.PRIVATE.put(
      `mail-outbox/${Date.now()}-${crypto.randomUUID()}.json`,
      JSON.stringify({ to: m.to, template: m.template, params: m.params, subject, text }),
    );
    return;
  }
  if (!env.MAIL_API_KEY) throw new Error("MAIL_API_KEY is not set");
  const r = await fetch("https://api.postmarkapp.com/email", {
    method: "POST",
    headers: { "Content-Type": "application/json", Accept: "application/json", "X-Postmark-Server-Token": env.MAIL_API_KEY },
    body: JSON.stringify({ From: env.MAIL_FROM, To: m.to, Subject: subject, TextBody: text, MessageStream: "outbound" }),
  });
  if (!r.ok) throw new Error(`the mail provider answered ${r.status}`);
}
