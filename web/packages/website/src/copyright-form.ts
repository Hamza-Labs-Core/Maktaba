// The copyright notice form of /legal/copyright/ (Epic 60 story 60.07, Epic 50 story 50.24 §2).
//
// Shown only when the endpoint is configured: a build-time flag, the two variables
// WEBSITE_NOTICE_ENDPOINT (the cloud's `POST https://api.maktaba.tv/v1/moderation/notices`)
// and WEBSITE_TURNSTILE_SITEKEY (a public key, not a secret). Until 50.24's endpoint is live
// neither is set, and the page shows the agent's email and the list of what a notice must
// contain, with no form (T03).
//
// One file for both sides: the build imports it to render the form's HTML (renderNoticeForm),
// and, with the flag on, publishes it with its types stripped as the page's one script, which
// sends the form as JSON (enhance). It imports nothing, so it runs as it is in both. Without
// JavaScript the form is a plain HTML form posting to the endpoint, which validates it.

export type Lang = "en" | "ar";

export interface NoticeConfig {
  /** where the form posts: on https://api.maktaba.tv, the one origin the page's CSP allows */
  endpoint: string;
  /** Turnstile's site key */
  sitekey: string;
}

/** The only origin the form may post to (src/headers.ts allows it on the copyright page). */
export const API_ORIGIN = "https://api.maktaba.tv";
export const TURNSTILE_SCRIPT = "https://challenges.cloudflare.com/turnstile/v0/api.js";
/** The hidden input Turnstile adds to the form, holding its token. */
export const TURNSTILE_INPUT = "cf-turnstile-response";

/** The flag: both variables set, or neither (no form). One without the other is a mistake. */
export function noticeConfig(env: Record<string, string | undefined>): NoticeConfig | null {
  const endpoint = env.WEBSITE_NOTICE_ENDPOINT?.trim() ?? "";
  const sitekey = env.WEBSITE_TURNSTILE_SITEKEY?.trim() ?? "";
  if (!endpoint && !sitekey) return null;
  if (!endpoint || !sitekey)
    throw new Error(
      "the copyright form needs both WEBSITE_NOTICE_ENDPOINT and WEBSITE_TURNSTILE_SITEKEY, or neither"
    );
  if (new URL(endpoint).origin !== API_ORIGIN)
    throw new Error(
      `WEBSITE_NOTICE_ENDPOINT must be on ${API_ORIGIN} (the page's CSP): ${endpoint}`
    );
  return { endpoint, sitekey };
}

/** A field of the notice: the § 512(c)(3) elements and the DSA Art. 16 fields (50.24 §2). */
interface Field {
  name: string;
  kind: "text" | "email" | "tel" | "textarea" | "check";
  required: boolean;
  autocomplete?: string;
}

export const FIELDS: Field[] = [
  { name: "claimant_name", kind: "text", required: true, autocomplete: "name" },
  { name: "claimant_email", kind: "email", required: true, autocomplete: "email" },
  { name: "claimant_address", kind: "textarea", required: true, autocomplete: "street-address" },
  { name: "claimant_phone", kind: "tel", required: true, autocomplete: "tel" },
  { name: "rights_holder", kind: "text", required: false },
  { name: "work", kind: "textarea", required: true },
  { name: "urls", kind: "textarea", required: true },
  { name: "programme_time", kind: "text", required: false },
  { name: "explanation", kind: "textarea", required: true },
  { name: "good_faith", kind: "check", required: true },
  { name: "accuracy", kind: "check", required: true },
  { name: "signature", kind: "text", required: true, autocomplete: "name" },
];

/** The JSON the form sends: exactly its fields, the Turnstile token and the page's language. */
export const PAYLOAD_KEYS = [...FIELDS.map((f) => f.name), "turnstile_token", "lang"].sort();

const T = {
  en: {
    heading: "Send a notice",
    where: "Your notice goes to Maktaba's moderation team. We reply by email.",
    send: "Send the notice",
    sending: "Sending…",
    sent: "Thank you. Your notice is number {id}. We reply by email.",
    noToken: "The check that you are a person has not finished yet. Wait a moment and send again.",
    failed: "The notice could not be sent. Try again, or email it to {email}.",
    fields: {
      claimant_name: ["Your full name", ""],
      claimant_email: ["Your email address", "We reply here."],
      claimant_address: ["Your postal address", ""],
      claimant_phone: ["Your telephone number", ""],
      rights_holder: ["The copyright owner, if you act for them", ""],
      work: ["The work you say is infringed", "Its title and, if you have one, a link to it."],
      urls: ["Where it is on Maktaba", "The address of each page on watch.maktaba.tv, one a line."],
      programme_time: ["The programme and time, if you know them", ""],
      explanation: ["Why it infringes your copyright", ""],
      good_faith: [
        "I believe in good faith that this use is not authorised by the copyright owner, its agent or the law.",
        "",
      ],
      accuracy: [
        "The information in this notice is accurate and, under penalty of perjury, I am the owner of the copyright or authorised to act for the owner.",
        "",
      ],
      signature: ["Your signature: type your full name", ""],
    } as Record<string, [string, string]>,
  },
  ar: {
    heading: "أرسلوا إشعارًا",
    where: "يصل إشعاركم إلى فريق الإشراف في «مكتبة»، ونردّ عليكم بالبريد الإلكتروني.",
    send: "أرسل الإشعار",
    sending: "جارٍ الإرسال…",
    sent: "شكرًا لكم. رقم إشعاركم {id}، وسنردّ عليكم بالبريد الإلكتروني.",
    noToken: "لم ينتهِ بعدُ التحقّق من أنّ المرسل إنسان. انتظروا لحظة ثم أرسلوا مجددًا.",
    failed: "تعذّر إرسال الإشعار. حاولوا مرة أخرى، أو أرسلوه بالبريد الإلكتروني إلى {email}.",
    fields: {
      claimant_name: ["اسمكم الكامل", ""],
      claimant_email: ["بريدكم الإلكتروني", "نردّ عليكم على هذا العنوان."],
      claimant_address: ["عنوانكم البريدي", ""],
      claimant_phone: ["رقم هاتفكم", ""],
      rights_holder: ["مالك حقوق النشر، إن كنتم تتصرّفون نيابةً عنه", ""],
      work: ["العمل الذي تقولون إنّ حقوقه انتُهكت", "عنوانه، ورابط إليه إن وُجد."],
      urls: ["مكانه في «مكتبة»", "عنوان كل صفحة على watch.maktaba.tv، كلّ عنوان في سطر."],
      programme_time: ["البرنامج ووقت عرضه، إن كنتم تعرفونهما", ""],
      explanation: ["لماذا ينتهك حقوق النشر الخاصة بكم", ""],
      good_faith: [
        "أعتقد بحسن نيّة أنّ هذا الاستخدام غير مأذون به من مالك حقوق النشر أو وكيله أو القانون.",
        "",
      ],
      accuracy: [
        "المعلومات الواردة في هذا الإشعار دقيقة، وأقرّ تحت طائلة عقوبة شهادة الزور بأنّني مالك حقوق النشر أو مخوَّل بالتصرّف نيابةً عنه.",
        "",
      ],
      signature: ["توقيعكم: اكتبوا اسمكم الكامل", ""],
    } as Record<string, [string, string]>,
  },
};

export const AGENT_EMAIL = "copyright@maktaba.tv";

const esc = (s: string) =>
  s.replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[c]!);

/** The form's HTML: a plain form that posts to the endpoint, Turnstile's widget, and the
 * sentence saying where the notice goes, before the button. */
export function renderNoticeForm(lang: Lang, cfg: NoticeConfig, script = ""): string {
  const t = T[lang];
  const rows = FIELDS.map((f) => {
    const [label, hint] = t.fields[f.name];
    const id = `notice-${f.name}`;
    const req = f.required ? " required" : "";
    const auto = f.autocomplete ? ` autocomplete="${f.autocomplete}"` : "";
    const hintId = hint ? `${id}-hint` : "";
    const described = hintId ? ` aria-describedby="${hintId}"` : "";
    const hintHtml = hint ? `\n    <span class="hint" id="${hintId}">${esc(hint)}</span>` : "";
    if (f.kind === "check")
      return `  <p class="field check">
    <input type="checkbox" id="${id}" name="${f.name}" value="yes"${req} />
    <label for="${id}">${esc(label)}</label>
  </p>`;
    const control =
      f.kind === "textarea"
        ? `<textarea id="${id}" name="${f.name}" rows="3"${req}${auto}${described}></textarea>`
        : `<input type="${f.kind}" id="${id}" name="${f.name}"${req}${auto}${described} />`;
    return `  <p class="field">
    <label for="${id}">${esc(label)}</label>${hintHtml}
    ${control}
  </p>`;
  }).join("\n");
  const s = script ? `\n<script type="module" src="${esc(script)}"></script>` : "";
  return `<div class="notice" role="group" aria-labelledby="send">
<h3 id="send">${esc(t.heading)}</h3>
<form id="notice-form" class="notice-form" method="post" action="${esc(cfg.endpoint)}" lang="${lang}"
  data-endpoint="${esc(cfg.endpoint)}" data-email="${AGENT_EMAIL}">
  <input type="hidden" name="lang" value="${lang}" />
${rows}
  <div class="cf-turnstile" data-sitekey="${esc(cfg.sitekey)}" data-language="${lang}"></div>
  <p id="notice-where">${esc(t.where)}</p>
  <p><button class="button" type="submit">${esc(t.send)}</button></p>
  <p id="notice-status" role="status" aria-live="polite"></p>
</form>
</div>
<script src="${TURNSTILE_SCRIPT}" async defer></script>${s}`;
}

/** What the form sends: each field (the URLs as a list, the statements as booleans), the
 * Turnstile token and the language. Nothing else. */
export function noticePayload(get: (name: string) => string | null): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const f of FIELDS) {
    const v = (get(f.name) ?? "").trim();
    if (f.kind === "check") out[f.name] = v === "yes";
    else if (f.name === "urls")
      out[f.name] = v
        .split(/\s+/)
        .map((u) => u.trim())
        .filter(Boolean);
    else out[f.name] = v;
  }
  out.turnstile_token = (get(TURNSTILE_INPUT) ?? "").trim();
  out.lang = get("lang") === "ar" ? "ar" : "en";
  return out;
}

type Fetch = (url: string, init: RequestInit) => Promise<Response>;

/** Sends the form as JSON instead of the plain post, and says what happened. */
export function enhance(form: HTMLFormElement, send: Fetch = (u, i) => fetch(u, i)): void {
  const lang: Lang = form.getAttribute("lang") === "ar" ? "ar" : "en";
  const t = T[lang];
  const status = form.querySelector<HTMLElement>("#notice-status");
  const button = form.querySelector<HTMLButtonElement>('button[type="submit"]');
  const say = (text: string) => {
    if (status) status.textContent = text;
  };
  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    if (!form.reportValidity()) return;
    const data = new FormData(form);
    const body = noticePayload((n) => {
      const v = data.get(n);
      return typeof v === "string" ? v : null;
    });
    if (!body.turnstile_token) return say(t.noToken);
    if (button) button.disabled = true;
    say(t.sending);
    try {
      const res = await send(form.dataset.endpoint ?? form.action, {
        method: "POST",
        mode: "cors",
        credentials: "omit",
        headers: { "Content-Type": "application/json", Accept: "application/json" },
        body: JSON.stringify(body),
      });
      if (res.status !== 202) throw new Error(`status ${res.status}`);
      const answer = (await res.json()) as { notice_id?: string };
      form.reset();
      say(t.sent.replace("{id}", String(answer.notice_id ?? "")));
    } catch {
      say(t.failed.replace("{email}", form.dataset.email ?? AGENT_EMAIL));
    } finally {
      if (button) button.disabled = false;
    }
  });
}

// in the page: the form, when the build put one there
if (typeof document !== "undefined") {
  const form = document.getElementById("notice-form");
  if (form instanceof HTMLFormElement) enhance(form);
}
