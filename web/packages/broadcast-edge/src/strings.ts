// The viewer-facing words of watch.maktaba.tv's invite page and API answers (story 50.26), in
// Arabic (the default, right to left) and English. The keys match the apps' (50.27) and the
// story's table.
export type Lang = "ar" | "en";

export const STRINGS: Record<string, Record<Lang, string>> = {
  "broadcast.free": { en: "Free to watch", ar: "المشاهدة مجانية" },
  "broadcast.invite.open": { en: "You're invited to watch", ar: "أنت مدعو للمشاهدة" },
  "broadcast.invite.accept": { en: "Start watching", ar: "ابدأ المشاهدة" },
  "broadcast.invite.ready": {
    en: "You're in. Open the Maktaba Player or keep this page to watch.",
    ar: "تم. افتح مشغّل مكتبة أو أبقِ هذه الصفحة للمشاهدة.",
  },
  "broadcast.invite.used": {
    en: "This invite has already been used on the most devices it allows. Ask for a new one.",
    ar: "استُخدمت هذه الدعوة على الحد الأقصى من الأجهزة. اطلب دعوة جديدة.",
  },
  "broadcast.invite.expired": { en: "This invite has expired.", ar: "انتهت صلاحية هذه الدعوة." },
  "broadcast.invite.revoked": {
    en: "This invite was withdrawn. Ask for a new one.",
    ar: "سُحبت هذه الدعوة. اطلب دعوة جديدة.",
  },
  "broadcast.invite.invalid": {
    en: "This invite link isn't valid.",
    ar: "رابط الدعوة هذا غير صالح.",
  },
  "broadcast.waiting": {
    en: "This channel is busy right now. You'll start watching as soon as there's room.",
    ar: "هذه القناة مزدحمة الآن. ستبدأ المشاهدة فور توفر مكان.",
  },
  "broadcast.full": {
    en: "This channel is full right now. Try again in a few minutes.",
    ar: "هذه القناة ممتلئة الآن. حاول بعد بضع دقائق.",
  },
  "broadcast.ipLimit": {
    en: "Too many devices on this network are watching. Try again later.",
    ar: "عدد كبير من الأجهزة على هذه الشبكة يشاهد الآن. حاول لاحقًا.",
  },
  "broadcast.dailyLimit": {
    en: "You've reached today's viewing time for this channel.",
    ar: "بلغت وقت المشاهدة المسموح اليوم لهذه القناة.",
  },
  "broadcast.ended": { en: "This broadcast has ended for you.", ar: "انتهى هذا البث بالنسبة لك." },
  "broadcast.off": { en: "This channel isn't broadcasting.", ar: "هذه القناة لا تبث الآن." },
  "broadcast.inviteRequired": {
    en: "This channel is invite only. Ask the publisher for an invite.",
    ar: "هذه القناة بالدعوة فقط. اطلب دعوة من الناشر.",
  },
  "broadcast.tooManyTries": {
    en: "Too many tries. Wait a minute and try again.",
    ar: "محاولات كثيرة. انتظر دقيقة وحاول مجددًا.",
  },
};

/** keyFor is the viewer message for an error code (undefined: none to show). */
export const ERROR_KEYS: Record<string, string> = {
  invite_used: "broadcast.invite.used",
  invite_expired: "broadcast.invite.expired",
  invite_revoked: "broadcast.invite.revoked",
  bad_token: "broadcast.invite.invalid",
  bad_signature: "broadcast.invite.invalid",
  unknown_key: "broadcast.invite.invalid",
  code_unknown: "broadcast.invite.invalid",
  broadcast_full: "broadcast.full",
  broadcast_wait: "broadcast.waiting",
  broadcast_ip_limit: "broadcast.ipLimit",
  broadcast_daily_limit: "broadcast.dailyLimit",
  broadcast_off: "broadcast.off",
  session_ended: "broadcast.ended",
  revoked: "broadcast.ended",
  pass_revoked: "broadcast.ended",
  invite_required: "broadcast.inviteRequired",
  not_invited: "broadcast.inviteRequired",
  rate_limited: "broadcast.tooManyTries",
};

/** langOf picks Arabic unless the request prefers English. */
export function langOf(request: Request): Lang {
  const al = (request.headers.get("Accept-Language") ?? "").toLowerCase();
  const en = al.indexOf("en");
  const ar = al.indexOf("ar");
  return en >= 0 && (ar < 0 || en < ar) ? "en" : "ar";
}

export function t(key: string, lang: Lang): string {
  return STRINGS[key]?.[lang] ?? key;
}
