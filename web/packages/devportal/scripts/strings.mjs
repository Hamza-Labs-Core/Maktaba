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
    "nav.contributing": "Contributing",
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
      "This site is for developers who extend Maktaba or work on it: the wiki of every epic and story, how to build a plugin, how to contribute, the API reference, security and what changed in each release.",
    "home.cta.source": "Source on GitHub",
    "home.cta.wiki": "Open the wiki",
    "home.cta.plugins": "Build a plugin",
    "home.cta.contributing": "Contribute",
    "home.cards": "Where to start",
    "card.source.title": "Source code",
    "card.source.body":
      "The whole project: the API, the pipeline, the web app and the native apps.",
    "card.wiki.title": "Wiki",
    "card.wiki.body":
      "The project map: every epic, story and plan, with the API endpoints and database tables they touch, searchable.",
    "wiki.title": "Maktaba Wiki",
    "wiki.description": "The Maktaba project map: every epic, story and plan, searchable.",
    "wiki.noscript":
      "The wiki needs JavaScript. The specs are on GitHub: https://github.com/Hamza-Labs-Core/Maktaba/tree/main/specs/epics",
    "card.specs.title": "Specs and epics",
    "card.specs.body": "What each part of Maktaba must do and why, epic by epic, on GitHub.",
    "card.plugins.title": "Build a plugin",
    "card.plugins.body":
      "Plugins add functions from the queue, with no internet: the manifest, the sandbox, the API, testing, packaging and the store.",
    "card.contributing.title": "Contribute to Maktaba",
    "card.contributing.body":
      "The architecture, the repo, a local setup, the tests, the conventions, CI and how to send a pull request.",
    "card.api.title": "API reference",
    "card.api.body": "The Maktaba API from its OpenAPI description: every endpoint and type.",
    "card.changelog.title": "Changelog",
    "card.changelog.body": "Every release and what is waiting for the next one.",
    "card.security.title": "Security",
    "card.security.body": "How to report a vulnerability, and our disclosure policy.",
    "home.lang.note":
      "Every page is in English and Arabic: the language switch at the top opens this page in the other one.",
    "footer.text": "Maktaba is built by Hamza Labs.",
    "footer.security": "Report a vulnerability",

    "doc.source": "View the source of this page on GitHub",
    "changelog.title": "Changelog",
    "api.title": "API reference",
    "api.loading": "Loading the API reference…",
    "api.lang":
      "The API reference in English. Paths, fields and code are the same in both languages.",
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
    "security.conform.title": "Rules for plugins and contributors",
    "security.conform.body":
      "Plugins run sandboxed with no internet and only the access they are granted. Code sent to Maktaba carries no secrets and checks every role on the server.",
    "security.conform.link": "Read the security rules for plugins and contributors",
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
    "nav.contributing": "المساهمة",
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
      "هذا الموقع للمطوّرين الذين يوسّعون مكتبة أو يعملون عليها: ويكي كل ملحمة وقصة، وكيف تبني إضافة، وكيف تساهم، ومرجع الواجهة البرمجية، والأمان، وما تغيّر في كل إصدار.",
    "home.cta.source": "الشيفرة على GitHub",
    "home.cta.wiki": "افتح الويكي",
    "home.cta.plugins": "ابنِ إضافة",
    "home.cta.contributing": "ساهم",
    "home.cards": "من أين تبدأ",
    "card.source.title": "الشيفرة المصدرية",
    "card.source.body":
      "المشروع كاملاً: الواجهة البرمجية، وخط المعالجة، وتطبيق الويب، والتطبيقات الأصلية.",
    "card.wiki.title": "الويكي",
    "card.wiki.body":
      "خريطة المشروع: كل ملحمة وقصة وخطة، مع نقاط وصول الواجهة البرمجية وجداول قاعدة البيانات التي تمسّها، مع البحث.",
    "wiki.title": "ويكي مكتبة",
    "wiki.description": "خريطة مشروع مكتبة: كل ملحمة وقصة وخطة، مع البحث.",
    "wiki.noscript":
      "لا يعمل الويكي دون جافاسكربت. وتجد المواصفات في مستودع المشروع: https://github.com/Hamza-Labs-Core/Maktaba/tree/main/specs/epics",
    "card.specs.title": "المواصفات والملاحم",
    "card.specs.body": "ما يجب أن يفعله كل جزء من مكتبة ولماذا، ملحمةً ملحمة، على GitHub.",
    "card.plugins.title": "ابنِ إضافة",
    "card.plugins.body":
      "تضيف الإضافات وظائف من طابور المهام، بلا إنترنت: البيان، والعزل، والواجهة البرمجية، والاختبار، والتحزيم، والمتجر.",
    "card.contributing.title": "ساهم في مكتبة",
    "card.contributing.body":
      "البنية، والمستودع، والإعداد المحلي، والاختبارات، والأعراف، والتكامل المستمر، وكيف ترسل طلب دمج.",
    "card.api.title": "مرجع الواجهة البرمجية",
    "card.api.body": "واجهة مكتبة البرمجية من وصف OpenAPI: كل نقطة وصول وكل نوع.",
    "card.changelog.title": "سجل التغييرات",
    "card.changelog.body": "كل إصدار، وما ينتظر الإصدار التالي.",
    "card.security.title": "الأمان",
    "card.security.body": "كيف تبلّغ عن ثغرة، وسياستنا في الإفصاح.",
    "home.lang.note":
      "كل صفحة بالعربية والإنجليزية: يفتح مبدّل اللغة في أعلى الصفحة الصفحةَ نفسها باللغة الأخرى.",
    "footer.text": "تبني مكتبةَ Hamza Labs.",
    "footer.security": "بلّغ عن ثغرة",

    "doc.source": "اعرض مصدر هذه الصفحة على GitHub",
    "changelog.title": "سجل التغييرات",
    "api.title": "مرجع الواجهة البرمجية",
    "api.loading": "جارٍ تحميل مرجع الواجهة البرمجية…",
    "api.lang":
      "مرجع الواجهة البرمجية بالعربية: الأوصاف مترجمة، أما المسارات والحقول والشيفرة فكما هي في اللغتين.",
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
    "security.conform.title": "قواعد للإضافات والمساهمين",
    "security.conform.body":
      "تعمل الإضافات معزولةً بلا إنترنت، ولا تصل إلا إلى ما مُنحت. والشيفرة المرسلة إلى مكتبة لا تحمل أسراراً، وتتحقّق من كل دور على الخادم.",
    "security.conform.link": "اقرأ قواعد الأمان للإضافات والمساهمين",
    "security.txt": "للآلات: ‎/.well-known/security.txt",
    "hof.title": "شكراً",
    "hof.body": "نذكر هنا، بإذنهم، من بلّغوا عن مشكلة أمنية بمسؤولية.",
    "hof.empty": "لا أحد بعد. قد تكون الأول.",

    "404.title": "الصفحة غير موجودة",
    "404.body": "لا توجد صفحة بهذا العنوان. ربما نُقلت.",
    "404.home": "اذهب إلى الصفحة الرئيسية",
  },
};
