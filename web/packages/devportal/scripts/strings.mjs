// Every string the portal's own pages show, in English and Arabic. The build renders the
// English into the HTML (it reads without script) and ships both to the page as
// assets/i18n.js; the EN/AR toggle swaps them and sets dir="rtl" for Arabic.
// The test suite checks that every key has both languages.

export const STRINGS = {
  en: {
    "site.name": "Maktaba for developers",
    "nav.home": "Home",
    "nav.wiki": "Wiki",
    "nav.api": "API",
    "nav.plugins": "Plugins",
    "nav.changelog": "Changelog",
    "nav.security": "Security",
    "nav.source": "Source",
    "lang.toggle": "العربية",
    "lang.toggle.label": "Show this page in Arabic",
    skip: "Skip to content",

    "home.title": "Maktaba for developers",
    "home.lead":
      "Maktaba is a self-hosted video library. It transcribes your videos, finds what is said in them, and streams them to the web, phones and TVs, from a computer you own.",
    "home.lead2":
      "This site is for the people who build on it and with it: the source, the wiki, the API, the plugin model and what changed in each release.",
    "home.cta.source": "Source on GitHub",
    "home.cta.wiki": "Open the wiki",
    "home.cards": "Where to start",
    "card.source.title": "Source code",
    "card.source.body":
      "The whole project: the API, the pipeline, the web app and the native apps.",
    "card.wiki.title": "Wiki",
    "card.wiki.body": "The project map: every epic, story, entity, screen and diagram, searchable.",
    "card.specs.title": "Specs and epics",
    "card.specs.body": "What each part of Maktaba must do and why, epic by epic.",
    "card.plugins.title": "Plugin developers",
    "card.plugins.body":
      "Plugins add functions from the queue, with no internet: the manifest, the API, the SDK and the store.",
    "card.api.title": "API reference",
    "card.api.body": "The Maktaba API from its OpenAPI description: every endpoint and type.",
    "card.changelog.title": "Changelog",
    "card.changelog.body": "Every release and what is waiting for the next one.",
    "card.security.title": "Security",
    "card.security.body": "How to report a vulnerability, and our disclosure policy.",
    "home.lang.note": "The wiki and the reference pages are in their source language, English.",
    "footer.text": "Maktaba is built by Hamza Labs.",
    "footer.security": "Report a vulnerability",

    "plugins.title": "Plugin developer docs",
    "plugins.lead":
      "Epic 54: plugins take jobs from the Maktaba queue, read what they are granted through the Maktaba API, and write results back. They can never reach the internet. These pages are the parts of the epic a plugin developer needs.",
    "plugins.status":
      "The plugin system is specified and being built; the details can change until it ships.",
    "doc.source": "View the source of this page on GitHub",
    "changelog.title": "Changelog",
    "api.title": "API reference",
    "api.loading": "Loading the API reference…",
    "api.download": "Download the OpenAPI description (JSON)",

    "security.title": "Security policy",
    "security.lead":
      "We want to hear about any vulnerability in Maktaba: the server, the apps, the cloud services and this site.",
    "security.report.title": "How to report",
    "security.report.body":
      "Email security@maktaba.dev with what you found, how to reproduce it and what it lets an attacker do. Please do not open a public GitHub issue for a vulnerability.",
    "security.after.title": "What happens next",
    "security.after.body":
      "We confirm your report, work on a fix, and tell you when it ships. We publish an advisory with the release that fixes it, and credit you if you want to be credited.",
    "security.rules.title": "Good faith",
    "security.rules.body":
      "Test only against your own Maktaba installation. Do not access other people's data, do not degrade a service for others, and give us reasonable time to fix a problem before you share it.",
    "security.txt": "Machine-readable: /.well-known/security.txt",
    "hof.title": "Thanks",
    "hof.body":
      "People who reported a security problem responsibly are listed here, with their permission.",
    "hof.empty": "No one yet. This could be you.",

    "404.title": "Page not found",
    "404.body": "There is no page at this address. It may have moved.",
    "404.home": "Go to the home page",
  },
  ar: {
    "site.name": "مكتبة للمطوّرين",
    "nav.home": "الرئيسية",
    "nav.wiki": "الويكي",
    "nav.api": "الواجهة البرمجية",
    "nav.plugins": "الإضافات",
    "nav.changelog": "سجل التغييرات",
    "nav.security": "الأمان",
    "nav.source": "الشيفرة المصدرية",
    "lang.toggle": "English",
    "lang.toggle.label": "اعرض هذه الصفحة بالإنجليزية",
    skip: "انتقل إلى المحتوى",

    "home.title": "مكتبة للمطوّرين",
    "home.lead":
      "مكتبة خادمُ فيديو تستضيفه بنفسك: يُفرّغ نصوص مقاطعك، ويجد ما قيل فيها، ويبثّها إلى الويب والهواتف والتلفزيونات، من حاسوب تملكه.",
    "home.lead2":
      "هذا الموقع لمن يبني عليها ومعها: الشيفرة المصدرية، والويكي، والواجهة البرمجية، ونموذج الإضافات، وما تغيّر في كل إصدار.",
    "home.cta.source": "الشيفرة على GitHub",
    "home.cta.wiki": "افتح الويكي",
    "home.cards": "من أين تبدأ",
    "card.source.title": "الشيفرة المصدرية",
    "card.source.body":
      "المشروع كاملاً: الواجهة البرمجية، وخط المعالجة، وتطبيق الويب، والتطبيقات الأصلية.",
    "card.wiki.title": "الويكي",
    "card.wiki.body": "خريطة المشروع: كل ملحمة وقصة وكيان وشاشة ومخطط، مع البحث.",
    "card.specs.title": "المواصفات والملاحم",
    "card.specs.body": "ما يجب أن يفعله كل جزء من مكتبة ولماذا، ملحمةً ملحمة.",
    "card.plugins.title": "لمطوّري الإضافات",
    "card.plugins.body":
      "تضيف الإضافات وظائف من طابور المهام، بلا إنترنت: البيان، والواجهة البرمجية، وحزمة التطوير، والمتجر.",
    "card.api.title": "مرجع الواجهة البرمجية",
    "card.api.body": "واجهة مكتبة البرمجية من وصف OpenAPI: كل نقطة وصول وكل نوع.",
    "card.changelog.title": "سجل التغييرات",
    "card.changelog.body": "كل إصدار، وما ينتظر الإصدار التالي.",
    "card.security.title": "الأمان",
    "card.security.body": "كيف تبلّغ عن ثغرة، وسياستنا في الإفصاح.",
    "home.lang.note": "الويكي والصفحات المرجعية بلغتها الأصلية، الإنجليزية.",
    "footer.text": "تبني مكتبةَ Hamza Labs.",
    "footer.security": "بلّغ عن ثغرة",

    "plugins.title": "وثائق مطوّري الإضافات",
    "plugins.lead":
      "الملحمة 54: تأخذ الإضافات المهام من طابور مكتبة، وتقرأ ما مُنحت من خلال واجهة مكتبة البرمجية، وتكتب النتائج. ولا تصل إلى الإنترنت أبداً. هذه الصفحات هي ما يحتاجه مطوّر الإضافة من الملحمة.",
    "plugins.status": "نظام الإضافات موصوف وقيد البناء، وقد تتغيّر التفاصيل حتى يصدر.",
    "doc.source": "اعرض مصدر هذه الصفحة على GitHub",
    "changelog.title": "سجل التغييرات",
    "api.title": "مرجع الواجهة البرمجية",
    "api.loading": "جارٍ تحميل مرجع الواجهة البرمجية…",
    "api.download": "نزّل وصف OpenAPI ‏(JSON)",

    "security.title": "سياسة الأمان",
    "security.lead":
      "نريد أن نعرف بأي ثغرة في مكتبة: الخادم، والتطبيقات، والخدمات السحابية، وهذا الموقع.",
    "security.report.title": "كيف تبلّغ",
    "security.report.body":
      "راسل security@maktaba.dev بما وجدت، وكيف يُعاد إنتاجه، وما يتيحه للمهاجم. من فضلك لا تفتح بلاغاً عاماً على GitHub عن ثغرة.",
    "security.after.title": "ماذا يحدث بعد ذلك",
    "security.after.body":
      "نؤكد استلام بلاغك، ونعمل على إصلاح، ونخبرك حين يصدر. وننشر تنبيهاً أمنياً مع الإصدار الذي يصلحها، ونذكر اسمك إن أردت.",
    "security.rules.title": "حسن النية",
    "security.rules.body":
      "اختبر على تثبيت مكتبة الخاص بك فقط. لا تصل إلى بيانات الآخرين، ولا تُضعف خدمة لغيرك، وامنحنا وقتاً معقولاً لإصلاح المشكلة قبل أن تنشرها.",
    "security.txt": "للآلات: ‎/.well-known/security.txt",
    "hof.title": "شكراً",
    "hof.body": "نذكر هنا، بإذنهم، من بلّغوا عن مشكلة أمنية بمسؤولية.",
    "hof.empty": "لا أحد بعد. قد تكون الأول.",

    "404.title": "الصفحة غير موجودة",
    "404.body": "لا توجد صفحة بهذا العنوان. ربما نُقلت.",
    "404.home": "اذهب إلى الصفحة الرئيسية",
  },
};
