// The viewer-facing words of watch.maktaba.tv: the invite page and API answers (story 50.26) and
// the watch pages (50.27), in Arabic (the default, right to left) and English. The keys match the
// apps' and the stories' tables.
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
  "broadcast.ended": { en: "This broadcast has ended.", ar: "انتهى هذا البث." },
  "broadcast.revoked": {
    en: "Access to this broadcast was withdrawn. Ask the publisher for a new invite.",
    ar: "سُحب الوصول إلى هذا البث. اطلب دعوة جديدة من الناشر.",
  },
  "broadcast.homeOffline": {
    en: "This channel's home is offline. It will be back when the home reconnects.",
    ar: "المنزل الذي يبث هذه القناة غير متصل. ستعود القناة عند عودة الاتصال.",
  },
  "broadcast.tuning": { en: "Starting the channel…", ar: "جارٍ تشغيل القناة…" },
  "broadcast.geoBlocked": {
    en: "This channel isn't available in your country.",
    ar: "هذه القناة غير متاحة في بلدك.",
  },
  // the watch pages and the apps (50.27)
  "guest.watchBroadcast": { en: "Watch a broadcast", ar: "شاهد بثًا" },
  "guest.enterCode": {
    en: "Enter the code from the broadcast's page",
    ar: "أدخل الرمز الظاهر في صفحة البث",
  },
  "guest.scanQr": { en: "Or scan this code with your phone", ar: "أو امسح هذا الرمز بهاتفك" },
  "guest.saved": { en: "Saved broadcasts", ar: "البثوث المحفوظة" },
  "guest.remove": { en: "Remove", ar: "إزالة" },
  "guest.onMaktaba": { en: "{publisher} on Maktaba", ar: "{publisher} على مكتبة" },
  "guest.tryAgain": { en: "Try again", ar: "حاول مرة أخرى" },
  "site.channels": { en: "Channels", ar: "القنوات" },
  "site.guide": { en: "Guide", ar: "دليل البرامج" },
  "site.today": { en: "Today", ar: "اليوم" },
  "site.now": { en: "Now", ar: "الآن" },
  "site.next": { en: "Next", ar: "التالي" },
  "site.nothingScheduled": { en: "No programme information", ar: "لا توجد معلومات عن البرامج" },
  "site.attribution": { en: "{title} by {by}, {licence}", ar: "{title} من {by}، {licence}" },
  "site.report": { en: "Report", ar: "إبلاغ" },
  "site.player": { en: "Player", ar: "المشغّل" },
  "site.noChannels": {
    en: "There are no channels for you here. Ask the publisher for an invite.",
    ar: "لا توجد قنوات متاحة لك هنا. اطلب دعوة من الناشر.",
  },
  "site.verify": {
    en: "Confirm you're a person to start watching.",
    ar: "أكّد أنك لست برنامجًا آليًا لبدء المشاهدة.",
  },
  "site.error": { en: "The channel couldn't start.", ar: "تعذّر تشغيل القناة." },
  "site.unavailable": {
    en: "Watching is temporarily unavailable. Please come back soon.",
    ar: "المشاهدة غير متاحة مؤقتًا. يُرجى العودة قريبًا.",
  },
  "site.home": {
    en: "Broadcasts on Maktaba open from the invite link their publisher sends you.",
    ar: "تُفتح البثوث على مكتبة من رابط الدعوة الذي يرسله لك الناشر.",
  },
  "site.legal.copyright": { en: "Copyright", ar: "حقوق النشر" },
  "site.legal.terms": { en: "Terms", ar: "الشروط" },
  "site.legal.privacy": { en: "Privacy", ar: "الخصوصية" },
  "site.legal.copyrightBody": {
    en: "Each channel is published by its own publisher, who is responsible for what it shows. Maktaba only carries the video and never chooses or recommends it. To report a programme that infringes your rights, use the Report button on the channel's page, or write to copyright@maktaba.tv with the channel's address and the time.",
    ar: "ينشر كلَّ قناة ناشرُها، وهو المسؤول عمّا تعرضه. تنقل مكتبة الفيديو فقط ولا تختاره ولا توصي به. للإبلاغ عن برنامج ينتهك حقوقك، استخدم زر الإبلاغ في صفحة القناة، أو اكتب إلى copyright@maktaba.tv مع عنوان القناة والوقت.",
  },
  "site.legal.termsBody": {
    en: "Watching is free and needs no account. Watch for your own viewing only: don't record, rebroadcast or share access beyond what the publisher's invite allows. Access can be withdrawn by the publisher at any time.",
    ar: "المشاهدة مجانية ولا تحتاج إلى حساب. شاهد لاستخدامك الشخصي فقط: لا تسجّل ولا تُعِد البث ولا تشارك الوصول بما يتجاوز ما تسمح به دعوة الناشر. يمكن للناشر سحب الوصول في أي وقت.",
  },
  "site.legal.privacyBody": {
    en: "There is no viewer account and no email. This site sets one functional cookie that holds your invite pass or viewing session. Your IP address is used only as a salted hash, for at most 24 hours, to apply viewing limits. Cloudflare processes requests for us (Workers, Turnstile, Analytics Engine).",
    ar: "لا يوجد حساب للمشاهد ولا بريد إلكتروني. يضع هذا الموقع ملف تعريف ارتباط وظيفيًا واحدًا يحمل تصريح دعوتك أو جلسة مشاهدتك. يُستخدم عنوان IP الخاص بك فقط بصيغة مُجزّأة مملّحة، لمدة 24 ساعة على الأكثر، لتطبيق حدود المشاهدة. تعالج Cloudflare الطلبات نيابةً عنا (Workers وTurnstile وAnalytics Engine).",
  },
  "site.legal.full": { en: "Read the full text", ar: "اقرأ النص الكامل" },
  "site.pair.title": { en: "Watch on your TV", ar: "شاهد على تلفازك" },
  "site.pair.invite": {
    en: "Paste the invite link you were sent",
    ar: "الصق رابط الدعوة الذي أُرسل إليك",
  },
  "site.pair.send": { en: "Send to the TV", ar: "أرسل إلى التلفاز" },
  "site.pair.sendOpen": { en: "Watch this broadcast on the TV", ar: "شاهد هذا البث على التلفاز" },
  "site.pair.sent": {
    en: "Done. The TV will start in a moment.",
    ar: "تم. سيبدأ التلفاز خلال لحظات.",
  },
  "site.pair.gone": {
    en: "This TV code has expired. Show a new one on the TV.",
    ar: "انتهت صلاحية رمز التلفاز هذا. اعرض رمزًا جديدًا على التلفاز.",
  },
  "site.pair.noTv": {
    en: "Open this page by scanning the code your TV shows.",
    ar: "افتح هذه الصفحة بمسح الرمز الذي يعرضه تلفازك.",
  },
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
  geo_blocked: "broadcast.geoBlocked",
  pair_gone: "site.pair.gone",
};

/** fill puts values into a string's {placeholders}. */
export function fill(text: string, values: Record<string, string>): string {
  return text.replace(/\{(\w+)\}/g, (m, k: string) => values[k] ?? m);
}

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
