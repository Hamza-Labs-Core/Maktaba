// maktaba.tv/install/synology's text, English and Arabic (the same keys in both;
// synologyStrings.test.ts checks). Story 59.09.

export const SYN = {
  en: {
    title: "Install Maktaba on a Synology NAS",
    intro:
      "On DSM 7.2 or newer, Maktaba runs as one Container Manager project: Synology's own way to run a compose file. It takes about 15 minutes, and Maktaba updates itself after that.",
    private: "Your answers stay in this page: nothing is sent anywhere.",
    before: "Before you start",
    models: "Which models fit",
    "col.model": "Model",
    "col.cpu": "Processor",
    "col.video": "Video",
    "col.memory": "Memory (GB)",
    "col.fits": "Fits?",
    "video.hw": "Hardware (Quick Sync)",
    "video.sw": "Software",
    memory: "{ram}, up to {max}",
    fits: "Fits: {tier}",
    fitsMore: "Fits with more memory: {tier} with {max} GB",
    storage: "Use as storage",
    "tier.light": "Light",
    "tier.standard": "Standard",
    "tier.strong": "Strong",
    modelsNote:
      'Maktaba needs a NAS that reports at least 3.5 GiB of memory (a "4 GB" model). Below that, setup refuses, and the NAS can still hold the library for a Maktaba home on another computer. Maktaba checks the memory and the GPU itself: this table only helps you choose.',
    tiers:
      "Light: speech to text off until you turn it on (base, in the quiet hours), one video conversion at a time up to 720p. Standard: speech to text with small, two conversions. Strong: everything at full size. Heavy work waits for the quiet hours (01:00 to 06:00) on Light and Standard, so the NAS stays quick for its own files.",
    prepare: "What to prepare",
    "prepare.admin":
      "A DSM account with admin rights, and Container Manager installed from Package Center.",
    "prepare.ssd":
      "An SSD volume, if the NAS has one: Maktaba keeps its database there, and browsing is faster.",
    "prepare.shares":
      "The shared folders that hold your videos, and which of them Maktaba may write to (for subtitles, conversions and downloads).",
    noPackage:
      "Maktaba is not in Package Center: a Container Manager project needs nothing signed by Synology, and it is how Jellyfin and Immich run on Synology too.",
    answers: "Your NAS",
    "field.model": "Model",
    "model.otherGpu": "Another Intel model with Quick Sync",
    "model.other": "Another Intel or AMD model, without Quick Sync",
    "model.otherArm": "Another ARM model",
    "field.ram": "Memory installed (GB)",
    "field.uid": "The uid of the DSM user maktaba",
    "field.uid.help": "Step 1 below shows how to read it. The group is always users (100).",
    "field.gpu": "Use Quick Sync (pass /dev/dri)",
    "field.volume": "The volume for Maktaba's data",
    "field.volume.help":
      "The folder is /{volume}/docker/maktaba. Choose an SSD volume if there is one.",
    "field.shares": "Shared folders with videos",
    "field.sharePath": "Folder (such as /volume1/Movies)",
    "field.readOnly": "Read only",
    "share.add": "Add a folder",
    "share.remove": "Remove",
    "field.tz": "Time zone",
    tooSmall:
      "With {ram} GB this NAS has too little memory for Maktaba, and setup will refuse. It can still store the library for a Maktaba home on another computer.",
    result: "The project file ({tier})",
    "result.j4125": "This is the J4125 version: Quick Sync through /dev/dri.",
    "result.plain":
      "This is the version for models without Quick Sync: video is converted in software, so players play files as they are when they can.",
    copy: "Copy",
    copied: "Copied",
    download: "Download compose.yaml",
    fixFirst: "Fix the answers above to get the file.",
    steps: "Step by step",
    "step1.title": "1. Create the DSM user maktaba",
    "step1.body":
      "Control Panel → User & Group → Create: name maktaba, a strong password, no administrator rights, no applications. Then read its uid: Control Panel → Task Scheduler → Create → Scheduled Task → User-defined script, user root, script id maktaba > /volume1/docker/maktaba-id.txt; Run it once, open the file in File Station, and delete the task. Over SSH, id maktaba says the same. The uid is 1026 or higher.",
    "step2.title": "2. Create Maktaba's folder",
    "step2.body":
      "In File Station, create the folder maktaba inside the shared folder docker (Container Manager makes docker; on an SSD volume, make the shared folder there). Properties → Permission → Create: user maktaba, Read/Write.",
    "step3.title": "3. Give maktaba its shared folders",
    "step3.body":
      "Control Panel → Shared Folder → each folder with videos → Edit → Permissions: Read/Write for maktaba where Maktaba may save subtitles, conversions and downloads, Read only everywhere else. Mark the read-only ones Read only in the answers above too: the file mounts them read-only, so Maktaba cannot change them.",
    "step4.title": "4. Create the project",
    "step4.body":
      "Container Manager → Project → Create: name maktaba, path docker/maktaba, source Create docker-compose.yml. Paste the file above, then Next and Done. Container Manager downloads the image and starts it.",
    "step5.title": "5. Finish setup",
    "step5.body":
      "When the project runs: Container Manager → Container → maktaba → Details → Terminal → Create, and run /opt/maktaba/server/bin/maktaba-server container setup there. Answer its questions (the admin account, then your libraries); Maktaba starts by itself when it finishes. Then open http://<your NAS>:8088. Settings → This machine shows the user Maktaba runs as, its tier and whether video uses Quick Sync.",
    after: "After setup",
    "after.firewall":
      "Firewall: if DSM's firewall is on, Control Panel → Security → Firewall → Edit Rules → Create: ports 8088 (the web app), 8080 (apps and the home's other machines) and 8081 (live TV), TCP, from your network only.",
    "after.ports":
      "Ports: DSM keeps 80, 443, 5000 and 5001, and its own database keeps 5432; Maktaba uses none of them.",
    "after.https":
      "HTTPS: Maktaba's own remote access, with its HTTPS name, is the simplest path. A DSM reverse proxy also works (Control Panel → Login Portal → Advanced → Reverse Proxy, to localhost:8088) with the WebSocket headers added under Custom Header.",
    "after.video":
      'Hardware video: on a J4125 model, This machine says "Video: hardware (iHD)". If it names a group to add, stop the project, put that group under group_add in its YAML and build it again.',
    "after.plugins":
      "Plugins: DSM's kernel has no Landlock, so Maktaba isolates plugins inside its container (the Container level). Third-party plugins wait until an admin allows them in Settings → Plugins.",
    "after.inotify":
      "Folder watching: Maktaba scans on a schedule and uses no inotify watches, so you do not need to raise DSM's inotify limit, as some other apps' guides ask.",
    "after.folders":
      "DSM's own folders (@eaDir, #recycle, #snapshot) are never added to your libraries.",
    "after.admin":
      "Running it as your own admin user also works, but a user made for Maktaba can only reach the folders you give it.",
    "after.encrypted":
      "Encrypted shared folders: until you unlock one, its libraries show No access. Nothing in them is deleted or scanned as missing.",
    trouble: "If something goes wrong",
    "trouble.write":
      "The container stops at once: its log (Container → maktaba → Log) names the folder and the uid. Give that user Read/Write on docker/maktaba (step 2) and start the project again.",
    "trouble.noAccess":
      "A library shows No access: give maktaba permission on that shared folder (step 3), then press Check access on the library's page.",
    "trouble.memory":
      "Setup says there is not enough memory: Maktaba needs 3.5 GiB. Add memory if the model takes it, or use the NAS as storage for a Maktaba home on another computer.",
    "trouble.port":
      "Another app on the NAS holds port 8088, 8080 or 8081: when it starts, Maktaba moves that port to a free one between 8780 and 8790. The container's log names the new port (if it was 8088, open http://<your NAS>:8780), and Settings → This machine shows what moved and which program holds the port.",
    "shot.guide-user": "The DSM user maktaba in User & Group, with no administrator rights",
    "shot.guide-uid": "The output of id maktaba, showing uid 1026 and group 100",
    "shot.guide-folder": "The docker/maktaba folder's permission: maktaba with Read/Write",
    "shot.guide-share": "The Movies shared folder's permissions: maktaba with Read only",
    "shot.guide-project": "Container Manager's Create project dialog with the file pasted",
    "shot.guide-firewall": "The firewall rule for ports 8088, 8080 and 8081",
    "field.renderGroup": "The render device's group",
    "field.renderGroup.help": "Over SSH: stat -c %g /dev/dri/renderD128",
    "field.tz.help": "For the quiet hours: heavy work runs at night on Light and Standard.",
    "tier.field": "Memory for Maktaba",
    "tier.hint":
      "Set from the memory installed: Maktaba plans for this much and leaves the rest to DSM.",
    "tier.light.limit": "Light, 4 GB (a 4 to 8 GB NAS)",
    "tier.standard.limit": "Standard, 6 GB (8 to 16 GB)",
    "tier.strong.limit": "Strong, 16 GB (24 GB or more)",
    "problem.uid": "Enter the uid as a number, such as 1026.",
    "problem.volume": "Enter a volume such as volume1.",
    "problem.share-volume": "Each folder is on a volume, such as /volume1/Movies.",
    "problem.media": "Enter each folder as a full path with no colon, quote or # in it.",
    "problem.media-none": "Add at least one folder.",
    "problem.render-group": "Enter the render device's group as a number, such as 937.",
    "problem.tz": "Enter a time zone such as Asia/Riyadh.",
    "problem.data": "Enter a volume such as volume1.",
    "problem.user-root": "The uid is never 0 (root).",
    "problem.host-ip": "Enter this NAS's address.",
    langLink: "العربية",
  },
  ar: {
    title: "تثبيت Maktaba على جهاز Synology NAS",
    intro:
      "على DSM 7.2 أو أحدث، يعمل Maktaba مشروعًا واحدًا في Container Manager، وهي طريقة Synology نفسها لتشغيل ملف compose. يستغرق التثبيت نحو 15 دقيقة، ثم يحدّث Maktaba نفسه بعد ذلك.",
    private: "تبقى إجاباتك في هذه الصفحة: لا يُرسَل شيء إلى أي مكان.",
    before: "قبل أن تبدأ",
    models: "الطرز المناسبة",
    "col.model": "الطراز",
    "col.cpu": "المعالج",
    "col.video": "الفيديو",
    "col.memory": "الذاكرة (غيغابايت)",
    "col.fits": "هل يناسب؟",
    "video.hw": "بالعتاد (Quick Sync)",
    "video.sw": "برمجيًا",
    memory: "{ram}، وحتى {max}",
    fits: "يناسب: {tier}",
    fitsMore: "يناسب بذاكرة أكبر: {tier} مع {max} غيغابايت",
    storage: "للتخزين فقط",
    "tier.light": "خفيف",
    "tier.standard": "قياسي",
    "tier.strong": "قوي",
    modelsNote:
      "يحتاج Maktaba إلى جهاز NAS يُظهر 3.5 غيغابايت من الذاكرة على الأقل (طراز «4 غيغابايت»). وإن قلّت عن ذلك يرفض الإعداد المتابعة، ويبقى الجهاز صالحًا لحفظ المكتبة لمنزل Maktaba على حاسوب آخر. يفحص Maktaba الذاكرة ومعالج الرسوميات بنفسه: هذا الجدول للمساعدة في الاختيار فقط.",
    tiers:
      "خفيف: تحويل الكلام إلى نص متوقف حتى تشغّله (base، في ساعات الهدوء)، وتحويل فيديو واحد في كل مرة حتى 720p. قياسي: تحويل الكلام بنموذج small، وتحويلان في وقت واحد. قوي: كل شيء بكامل حجمه. وينتظر العمل الثقيل ساعات الهدوء (من 01:00 إلى 06:00) في الخفيف والقياسي، فيبقى الجهاز سريعًا في خدمة ملفاته.",
    prepare: "ما تحتاج إلى تجهيزه",
    "prepare.admin": "حساب DSM بصلاحيات المسؤول، وتطبيق Container Manager مثبّتًا من مركز الحزم.",
    "prepare.ssd":
      "وحدة تخزين SSD إن كانت في الجهاز: يحفظ Maktaba قاعدة بياناته فيها، فيصبح التصفح أسرع.",
    "prepare.shares":
      "المجلدات المشتركة التي تحوي مقاطعك، وأيّها يُسمح لـ Maktaba بالكتابة فيه (للترجمات والتحويلات والتنزيلات).",
    noPackage:
      "لا يوجد Maktaba في مركز الحزم: مشروع Container Manager لا يحتاج إلى توقيع من Synology، وهو الطريقة التي يعمل بها Jellyfin وImmich على Synology أيضًا.",
    answers: "جهازك",
    "field.model": "الطراز",
    "model.otherGpu": "طراز Intel آخر يدعم Quick Sync",
    "model.other": "طراز Intel أو AMD آخر، دون Quick Sync",
    "model.otherArm": "طراز ARM آخر",
    "field.ram": "الذاكرة المركّبة (غيغابايت)",
    "field.uid": "معرّف uid لمستخدم DSM باسم maktaba",
    "field.uid.help": "توضح الخطوة 1 أدناه كيف تقرؤه. المجموعة دائمًا users ‏(100).",
    "field.gpu": "استخدام Quick Sync (تمرير ‎/dev/dri)",
    "field.volume": "وحدة التخزين لبيانات Maktaba",
    "field.volume.help": "المجلد هو ‎/{volume}/docker/maktaba. اختر وحدة SSD إن وُجدت.",
    "field.shares": "المجلدات المشتركة التي فيها مقاطع",
    "field.sharePath": "المجلد (مثل ‎/volume1/Movies)",
    "field.readOnly": "للقراءة فقط",
    "share.add": "إضافة مجلد",
    "share.remove": "إزالة",
    "field.tz": "المنطقة الزمنية",
    tooSmall:
      "بذاكرة {ram} غيغابايت لا تكفي ذاكرة هذا الجهاز لـ Maktaba، وسيرفض الإعداد المتابعة. لكنه يبقى صالحًا لحفظ المكتبة لمنزل Maktaba على حاسوب آخر.",
    result: "ملف المشروع ({tier})",
    "result.j4125": "هذه نسخة J4125: ‏Quick Sync عبر ‎/dev/dri.",
    "result.plain":
      "هذه النسخة للطرز التي لا تدعم Quick Sync: يُحوَّل الفيديو برمجيًا، لذا تشغّل المشغلات الملفات كما هي متى استطاعت.",
    copy: "نسخ",
    copied: "نُسخ",
    download: "تنزيل compose.yaml",
    fixFirst: "صحّح الإجابات أعلاه لتحصل على الملف.",
    steps: "خطوة بخطوة",
    "step1.title": "1. أنشئ مستخدم DSM باسم maktaba",
    "step1.body":
      "من لوحة التحكم ← المستخدم والمجموعة ← إنشاء: الاسم maktaba، وكلمة مرور قوية، دون صلاحيات المسؤول، ودون تطبيقات. ثم اقرأ معرّف uid الخاص به: لوحة التحكم ← جدولة المهام ← إنشاء ← مهمة مجدولة ← برنامج نصي معرَّف من المستخدم، المستخدم root، والبرنامج النصي id maktaba > /volume1/docker/maktaba-id.txt؛ شغّلها مرة واحدة، وافتح الملف في File Station، ثم احذف المهمة. ويعطي الأمر id maktaba عبر SSH النتيجة نفسها. المعرّف 1026 أو أكبر.",
    "step2.title": "2. أنشئ مجلد Maktaba",
    "step2.body":
      "في File Station، أنشئ المجلد maktaba داخل المجلد المشترك docker (ينشئ Container Manager المجلد docker؛ وعلى وحدة SSD أنشئ المجلد المشترك هناك). الخصائص ← الإذن ← إنشاء: المستخدم maktaba، القراءة والكتابة.",
    "step3.title": "3. امنح maktaba مجلداته المشتركة",
    "step3.body":
      "من لوحة التحكم ← المجلد المشترك ← كل مجلد فيه مقاطع ← تعديل ← الأذونات: القراءة والكتابة للمستخدم maktaba حيث يُسمح لـ Maktaba بحفظ الترجمات والتحويلات والتنزيلات، والقراءة فقط في غيرها. وحدّد المجلدات المخصصة للقراءة فقط في الإجابات أعلاه أيضًا: يربطها الملف للقراءة فقط، فلا يستطيع Maktaba تغييرها.",
    "step4.title": "4. أنشئ المشروع",
    "step4.body":
      "Container Manager ← المشروع ← إنشاء: الاسم maktaba، والمسار docker/maktaba، والمصدر إنشاء docker-compose.yml. الصق الملف أعلاه، ثم التالي وتم. ينزّل Container Manager الصورة ويشغّلها.",
    "step5.title": "5. أكمل الإعداد",
    "step5.body":
      "حين يعمل المشروع: من Container Manager ← الحاوية ← maktaba ← التفاصيل ← الطرفية ← إنشاء، وشغّل هناك الأمر /opt/maktaba/server/bin/maktaba-server container setup. أجب عن أسئلته (حساب المسؤول، ثم مكتباتك)، ويبدأ Maktaba بنفسه حين ينتهي. ثم افتح http://<جهازك>:8088. وتعرض الإعدادات ← هذا الجهاز المستخدم الذي يعمل به Maktaba، وفئته، وهل يستخدم الفيديو Quick Sync.",
    after: "بعد الإعداد",
    "after.firewall":
      "الجدار الناري: إن كان الجدار الناري في DSM مفعّلًا، فمن لوحة التحكم ← الأمان ← الجدار الناري ← تعديل القواعد ← إنشاء: المنافذ 8088 (تطبيق الويب) و8080 (التطبيقات وأجهزة المنزل الأخرى) و8081 (البث المباشر)، بروتوكول TCP، من شبكتك فقط.",
    "after.ports":
      "المنافذ: يحتفظ DSM بالمنافذ 80 و443 و5000 و5001، وتحتفظ قاعدة بياناته بالمنفذ 5432؛ ولا يستخدم Maktaba أيًّا منها.",
    "after.https":
      "HTTPS: الوصول عن بُعد الخاص بـ Maktaba، باسم HTTPS الخاص به، هو الطريق الأبسط. ويعمل الوكيل العكسي في DSM أيضًا (لوحة التحكم ← بوابة تسجيل الدخول ← متقدم ← الوكيل العكسي، إلى localhost:8088) مع إضافة ترويسات WebSocket في الترويسة المخصصة.",
    "after.video":
      'الفيديو بالعتاد: على طراز J4125 تقول صفحة هذا الجهاز "الفيديو: بالعتاد (iHD)". وإن ذكرت مجموعة لإضافتها، فأوقف المشروع وضع تلك المجموعة تحت group_add في ملف YAML ثم أعد بناءه.',
    "after.plugins":
      "الإضافات: لا تتوفر Landlock في نواة DSM، لذا يعزل Maktaba الإضافات داخل حاويته (مستوى الحاوية). وتنتظر إضافات الجهات الخارجية حتى يسمح بها مسؤول من الإعدادات ← الإضافات.",
    "after.inotify":
      "مراقبة المجلدات: يفحص Maktaba وفق جدول ولا يستخدم مراقبات inotify، فلا تحتاج إلى رفع حد inotify في DSM كما تطلب أدلة بعض التطبيقات الأخرى.",
    "after.folders":
      "مجلدات DSM الخاصة (@eaDir و‎#recycle و‎#snapshot) لا تُضاف إلى مكتباتك أبدًا.",
    "after.admin":
      "تشغيله بحساب المسؤول الخاص بك يعمل أيضًا، لكن المستخدم المخصص لـ Maktaba لا يصل إلا إلى المجلدات التي تمنحه إياها.",
    "after.encrypted":
      "المجلدات المشتركة المشفّرة: إلى أن تفتح قفل أحدها، تظهر مكتباته بلا وصول. ولا يُحذف شيء منها ولا يُعدّ مفقودًا.",
    trouble: "إذا حدث خطأ",
    "trouble.write":
      "تتوقف الحاوية فورًا: يذكر سجلها (الحاوية ← maktaba ← السجل) المجلد ومعرّف uid. امنح ذلك المستخدم القراءة والكتابة على docker/maktaba (الخطوة 2) ثم شغّل المشروع من جديد.",
    "trouble.noAccess":
      "تظهر مكتبة بلا وصول: امنح maktaba الإذن على ذلك المجلد المشترك (الخطوة 3)، ثم اضغط فحص الوصول في صفحة المكتبة.",
    "trouble.memory":
      "يقول الإعداد إن الذاكرة لا تكفي: يحتاج Maktaba إلى 3.5 غيغابايت. أضف ذاكرة إن كان الطراز يقبلها، أو استخدم الجهاز للتخزين لمنزل Maktaba على حاسوب آخر.",
    "trouble.port":
      "يشغل تطبيق آخر على الجهاز المنفذ 8088 أو 8080 أو 8081: عند التشغيل ينقل Maktaba ذلك المنفذ إلى منفذ حر بين 8780 و8790. ويذكر سجل الحاوية المنفذ الجديد (إن كان 8088 فافتح ⁦http://<your NAS>:8780⁩)، وتعرض الإعدادات ← هذا الجهاز ما انتقل وأي برنامج يشغل المنفذ.",
    "shot.guide-user": "مستخدم DSM باسم maktaba في المستخدم والمجموعة، دون صلاحيات المسؤول",
    "shot.guide-uid": "ناتج الأمر id maktaba، ويُظهر المعرّف 1026 والمجموعة 100",
    "shot.guide-folder": "إذن المجلد docker/maktaba: المستخدم maktaba بالقراءة والكتابة",
    "shot.guide-share": "أذونات المجلد المشترك Movies: المستخدم maktaba بالقراءة فقط",
    "shot.guide-project": "نافذة إنشاء مشروع في Container Manager والملف ملصوق فيها",
    "shot.guide-firewall": "قاعدة الجدار الناري للمنافذ 8088 و8080 و8081",
    "field.renderGroup": "مجموعة جهاز العرض",
    "field.renderGroup.help": "عبر SSH: ⁦stat -c %g /dev/dri/renderD128⁩",
    "field.tz.help": "لساعات الهدوء: يعمل العمل الثقيل ليلًا في الفئتين الخفيفة والقياسية.",
    "tier.field": "الذاكرة المخصصة لـ Maktaba",
    "tier.hint": "تُضبط من الذاكرة المركّبة: يخطط Maktaba لهذا القدر ويترك الباقي لـ DSM.",
    "tier.light.limit": "خفيف، 4 غيغابايت (جهاز بذاكرة من 4 إلى 8 غيغابايت)",
    "tier.standard.limit": "قياسي، 6 غيغابايت (من 8 إلى 16 غيغابايت)",
    "tier.strong.limit": "قوي، 16 غيغابايت (‏24 غيغابايت أو أكثر)",
    "problem.uid": "أدخل معرّف uid رقمًا، مثل 1026.",
    "problem.volume": "أدخل وحدة تخزين مثل volume1.",
    "problem.share-volume": "كل مجلد على وحدة تخزين، مثل ‎/volume1/Movies.",
    "problem.media": "أدخل كل مجلد بمساره الكامل دون نقطتين رأسيتين أو علامة اقتباس أو # فيه.",
    "problem.media-none": "أضف مجلدًا واحدًا على الأقل.",
    "problem.render-group": "أدخل مجموعة جهاز العرض رقمًا، مثل 937.",
    "problem.tz": "أدخل منطقة زمنية مثل Asia/Riyadh.",
    "problem.data": "أدخل وحدة تخزين مثل volume1.",
    "problem.user-root": "لا يكون المعرّف 0 (الجذر) أبدًا.",
    "problem.host-ip": "أدخل عنوان هذا الجهاز.",
    langLink: "English",
  },
} as const;

export type SynKey = keyof (typeof SYN)["en"];
export type Lang = keyof typeof SYN;

// A path in Arabic text (/volume1/docker/maktaba, /dev/dri) is isolated left to right, so its
// slashes stay where they are written ("/dev/dri." not "dev/dri/."); a mark before it goes.
const PATH = /[\u200e\u200f]?(?<![\w/:])(\/(?:[\w.-]*[\w-])?(?:\/[\w.-]*[\w-])*)/g;

export function st(lang: Lang, key: SynKey, vars: Record<string, string | number> = {}): string {
  const s = (SYN[lang][key] as string).replace(/\{(\w+)\}/g, (m, k: string) =>
    k in vars ? String(vars[k]) : m
  );
  return lang === "ar" ? s.replace(PATH, "\u2066$1\u2069") : s;
}
