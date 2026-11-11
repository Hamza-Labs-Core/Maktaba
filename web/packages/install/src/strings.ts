// The install page's text, English and Arabic (the same keys in both; strings.test.ts checks).

export type Lang = "en" | "ar";

export const STRINGS = {
  en: {
    title: "Install Maktaba with Docker",
    lead: "For QNAP, UGREEN, ASUSTOR, Portainer, Dockge or any Linux machine with Docker. Answer a few questions and copy the compose file. Nothing you type here leaves this page.",
    nas: "Where will it run?",
    "nas.qnap": "QNAP (Container Station)",
    "nas.ugreen": "UGREEN (Docker in App Center)",
    "nas.asustor": "ASUSTOR (Portainer or Docker Compose)",
    "nas.linux": "Linux or another NAS",
    "nas.hint.qnap":
      "Container Station → Applications → Create, paste the file. QTS keeps port 8080 for itself: Maktaba moves its cluster port and setup shows the new one. Not yet tested on a QNAP device.",
    "nas.hint.ugreen":
      "Docker → Project → Create, paste the file, and keep its folder under /volume1/docker.",
    "nas.hint.asustor":
      "Install Portainer from App Central, then Stacks → Add stack and paste the file.",
    "nas.hint.linux":
      "Save it as docker-compose.yml in a folder of its own and run docker compose up -d there. In Portainer or Dockge, paste it as a stack.",
    user: "The user Maktaba runs as (uid:gid)",
    "user.hint":
      "The user that owns your media folders. On the NAS, run: id <user>. uid=1000 gid=100 is 1000:100. Never root (0).",
    ram: "The machine's memory (GB)",
    tier: "Memory for Maktaba",
    "tier.light": "Light, 4 GB (a 4 to 8 GB NAS)",
    "tier.standard": "Standard, 6 GB (8 to 16 GB)",
    "tier.strong": "Strong, 16 GB (24 GB or more)",
    "tier.none": "No limit (Maktaba may use all the memory)",
    "tier.hint":
      "Maktaba plans for this much and leaves the rest to the NAS. Under 3.5 GB it does not run: the NAS then serves best as storage for a Maktaba on another computer. On an ARM NAS, anything under 8 GB is Light.",
    data: "Maktaba's data folder on the NAS",
    "data.hint":
      "Fast local storage (an SSD pool if you have one), never a network share or a USB stick. Empty: a Docker volume.",
    media: "Media folders, one per line",
    "media.hint":
      "Each folder is mounted at the same path inside. Tick read-only for the ones Maktaba must never write to.",
    readOnly: "read-only",
    addFolder: "Add a folder",
    video: "Hardware video",
    "video.none": "None (software)",
    "video.intel-amd": "Intel or AMD (Quick Sync, VA-API)",
    "video.nvidia": "NVIDIA (the NVIDIA container toolkit installed)",
    renderGroup: "The render device's group id",
    "renderGroup.hint": "On the NAS, run: stat -c %g /dev/dri/renderD128",
    tz: "Time zone",
    "tz.hint": "For the quiet hours: heavy work runs at night on smaller machines.",
    network: "Network",
    "network.host": "Host network (recommended: apps and TVs find Maktaba by themselves)",
    "network.bridge": "Bridge network",
    hostIp: "This machine's address on your network",
    "hostIp.hint": "On a bridge, apps connect by this address.",
    result: "Your compose file",
    copy: "Copy",
    copied: "Copied",
    download: "Download docker-compose.yml",
    next: "Then run docker compose up -d, and docker compose run --rm maktaba setup once. Open http://<this machine>:8088.",
    updates:
      "Maktaba updates itself after that, in its update window. The file names the latest release (:latest); your NAS's own app updates, Watchtower included, can stay on too: a newer image goes onto the data folder at the next start, and an older one never replaces a newer Maktaba.",
    "problem.user": "Write the user as two numbers, uid:gid, such as 1000:100.",
    "problem.user-root": "Maktaba never runs as root (uid 0): choose a user of its own.",
    "problem.data":
      "The data folder must be a full path, such as /volume1/docker/maktaba, without a colon.",
    "problem.media":
      "Each media folder must be a full path, such as /volume1/Movies, without a colon.",
    "problem.media-none": "Add at least one media folder.",
    "problem.render-group": "Write the render device's group id, a number from the stat command.",
    "problem.tz": "Write the time zone as Area/City, such as Asia/Riyadh.",
    "problem.host-ip": "Write this machine's IPv4 address, such as 192.168.1.20.",
    other: "Other guides",
    unraid: "Unraid: install Maktaba from Community Applications (search Maktaba).",
    langLink: "العربية",
  },
  ar: {
    title: "تثبيت Maktaba باستخدام Docker",
    lead: "لأجهزة QNAP وUGREEN وASUSTOR، أو Portainer وDockge، أو أي جهاز لينكس فيه Docker. أجب عن بضعة أسئلة وانسخ ملف التركيب. لا يغادر شيء مما تكتبه هنا هذه الصفحة.",
    nas: "أين سيعمل؟",
    "nas.qnap": "QNAP (محطة الحاويات Container Station)",
    "nas.ugreen": "UGREEN (تطبيق Docker في مركز التطبيقات)",
    "nas.asustor": "ASUSTOR (أداة Portainer أو Docker Compose)",
    "nas.linux": "لينكس أو جهاز تخزين آخر",
    "nas.hint.qnap":
      "افتح محطة الحاويات ثم التطبيقات ثم إنشاء، والصق الملف. يحجز نظام QTS المنفذ 8080 لنفسه، فينقل Maktaba منفذ العنقود ويعرض الإعداد المنفذ الجديد. لم يُختبر بعد على جهاز QNAP.",
    "nas.hint.ugreen":
      "افتح Docker ثم المشروع ثم إنشاء، والصق الملف، واجعل مجلده تحت ‎/volume1/docker.",
    "nas.hint.asustor": "ثبّت Portainer من App Central، ثم افتح Stacks ثم Add stack والصق الملف.",
    "nas.hint.linux":
      "احفظه باسم docker-compose.yml في مجلد خاص به وشغّل الأمر docker compose up -d هناك. وفي Portainer أو Dockge الصقه حزمةً (stack).",
    user: "المستخدم الذي يعمل به Maktaba (uid:gid)",
    "user.hint":
      "المستخدم الذي يملك مجلدات الوسائط. شغّل على الجهاز الأمر: id <user>. والقيمتان uid=1000 وgid=100 تُكتبان 1000:100. ولا يكون المستخدم الجذر (0) أبدًا.",
    ram: "ذاكرة الجهاز (غيغابايت)",
    tier: "الذاكرة المخصّصة لـ Maktaba",
    "tier.light": "خفيفة، 4 غيغابايت (جهاز بذاكرة من 4 إلى 8 غيغابايت)",
    "tier.standard": "قياسية، 6 غيغابايت (من 8 إلى 16 غيغابايت)",
    "tier.strong": "قوية، 16 غيغابايت (24 غيغابايت فأكثر)",
    "tier.none": "بلا حدّ (يستطيع Maktaba استخدام الذاكرة كلها)",
    "tier.hint":
      "يخطّط Maktaba لهذا القدر ويترك الباقي لجهاز التخزين. وتحت 3.5 غيغابايت لا يعمل، والأفضل حينها أن يكون الجهاز مخزنًا لـ Maktaba يعمل على حاسوب آخر. وعلى جهاز تخزين بمعالج ARM يكون كل ما دون 8 غيغابايت خفيفًا.",
    data: "مجلد بيانات Maktaba على الجهاز",
    "data.hint":
      "تخزين محلي سريع (مجمّع SSD إن وُجد)، وليس مجلدًا مشتركًا عبر الشبكة ولا ذاكرة USB أبدًا. إن تركته فارغًا يُستخدم مجلد Docker.",
    media: "مجلدات الوسائط، مجلد في كل سطر",
    "media.hint":
      "يُربط كل مجلد بالمسار نفسه في الداخل. اختر «للقراءة فقط» للمجلدات التي يجب ألا يكتب فيها Maktaba أبدًا.",
    readOnly: "للقراءة فقط",
    addFolder: "أضف مجلدًا",
    video: "الفيديو بالعتاد",
    "video.none": "لا شيء (بالبرمجيات)",
    "video.intel-amd": "إنتل أو AMD (Quick Sync وVA-API)",
    "video.nvidia": "إنفيديا (مع تثبيت حزمة NVIDIA container toolkit)",
    renderGroup: "معرّف مجموعة جهاز العرض",
    "renderGroup.hint": "شغّل على الجهاز الأمر: stat -c %g /dev/dri/renderD128",
    tz: "المنطقة الزمنية",
    "tz.hint": "لساعات الهدوء: يجري العمل الثقيل ليلًا على الأجهزة الأصغر.",
    network: "الشبكة",
    "network.host": "شبكة المضيف (مستحسنة: تعثر التطبيقات وأجهزة التلفاز على Maktaba بنفسها)",
    "network.bridge": "شبكة جسرية",
    hostIp: "عنوان هذا الجهاز على شبكتك",
    "hostIp.hint": "على الشبكة الجسرية تتصل التطبيقات بهذا العنوان.",
    result: "ملف التركيب الخاص بك",
    copy: "انسخ",
    copied: "نُسخ",
    download: "نزّل docker-compose.yml",
    next: "ثم شغّل الأمر docker compose up -d، والأمر docker compose run --rm maktaba setup مرة واحدة. وافتح العنوان http://<this machine>:8088.",
    updates:
      "بعد ذلك تحدّث مكتبة نفسها في نافذة التحديث. ويسمّي الملف أحدث إصدار (:latest)؛ ويمكن أن تبقى تحديثات التطبيقات في جهاز NAS، ومنها Watchtower، مفعّلة أيضًا: توضع الصورة الأحدث في مجلد البيانات عند التشغيل التالي، ولا تحلّ الصورة الأقدم أبدًا محلّ نسخة أحدث من مكتبة.",
    "problem.user": "اكتب المستخدم رقمين بهذا الشكل uid:gid، مثل 1000:100.",
    "problem.user-root": "لا يعمل Maktaba أبدًا بالمستخدم الجذر (uid 0)، فاختر مستخدمًا خاصًا به.",
    "problem.data":
      "يجب أن يكون مجلد البيانات مسارًا كاملًا، مثل ‎/volume1/docker/maktaba، بلا نقطتين.",
    "problem.media": "يجب أن يكون كل مجلد وسائط مسارًا كاملًا، مثل ‎/volume1/Movies، بلا نقطتين.",
    "problem.media-none": "أضف مجلد وسائط واحدًا على الأقل.",
    "problem.render-group": "اكتب معرّف مجموعة جهاز العرض، وهو رقم يظهره أمر stat.",
    "problem.tz": "اكتب المنطقة الزمنية بالشكل Area/City، مثل Asia/Riyadh.",
    "problem.host-ip": "اكتب عنوان IPv4 لهذا الجهاز، مثل 192.168.1.20.",
    other: "أدلة أخرى",
    unraid: "Unraid: ثبّت Maktaba من متجر Community Applications (ابحث عن Maktaba).",
    langLink: "English",
  },
} as const;

export type Key = keyof (typeof STRINGS)["en"];

export function t(lang: Lang, key: Key): string {
  return STRINGS[lang][key];
}
