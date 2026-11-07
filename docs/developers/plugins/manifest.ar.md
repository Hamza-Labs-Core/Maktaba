# البيان والحزمة

> **الحالة: في `main`.** بُني مخطط البيان ومدقّقاته الأربعة (Go وRust وPython وTypeScript، وتبقى
> متطابقة بمجموعة حالات اختبار مشتركة واحدة) وأداة فك حزمة `.mkplugin`. وكل مفتاح وقيمته
> الافتراضية وحدوده والخطأ الذي يجيب به موجود في [مرجع الإضافات](reference.ar.md)، المولَّد من
> المخطط نفسه.

## العقد

تلتزم إضافتك أمام مكتبة ببضع حقائق، وتُلزمك مكتبة بها:

- **هويتها:** معرّف بصيغة DNS المعكوسة تحت بادئة تملكها، ورقم إصدار بصيغة semver.
- **ما تعمل عليه:** نطاق من إصدارات خادم مكتبة، وإصدار الواجهة البرمجية للإضافات الذي بُنيت عليه.
- **ما تطلبه:** قدرات، كل منها `required` (مطلوبة) أو `optional` (اختيارية).
- **ما تضيفه:** مراحل، والأحداث والجداول الزمنية التي تغذّيها، والإعدادات، واللوحات.
- **ما يحق لها استهلاكه:** الذاكرة، والوقت، والمساحة المؤقتة، والبيانات، والتزامن.

يتحقق المخطط نفسه من البيان في الخادم المنزلي وفي المتجر وفي حزمة التطوير. والمفاتيح المجهولة
خطأ لا تحذير، فلا يُتجاهَل بصمت أي شيء تكتبه.

## الحزمة

ملف `.mkplugin` أرشيف `tar` مضغوط بخوارزمية zstd:

| المسار | مطلوب | المحتوى |
|---|---|---|
| `manifest.toml` | نعم | البيان |
| `plugin.wasm` | نعم | مكوّن WebAssembly واحد (لا وحدة أساسية) |
| `files.json` | نعم | `[{path, sha256, size}]` لكل ملف، ومنها `manifest.toml`، مرتبة حسب المسار: وهي ما يغطيه التوقيع |
| `i18n/en.json`، `i18n/ar.json` | نعم | كل مفتاح نصي يستخدمه البيان وملفات الواجهة |
| `icon.png` | نعم | صورة PNG بقياس 256 x 256، بحجم 200 KB على الأكثر |
| `LICENSE` | نعم | نص يطابق `licence` |
| `ui/*.json` | لا | مخطط الإعدادات، واللوحات |
| `fixtures/` | لا | مدخلات اختبار لتشغيل المتجر في بيئة معزولة؛ لا تُثبَّت أبدًا |
| `signatures/developer.sig`، `signatures/store.sig` | لا | راجع [النشر](publishing.ar.md) |

الحدود: 50 MB مضغوطة، و200 MB بعد الفك، و500 ملف، ومسارات حتى 200 بايت. الملفات العادية
والمجلدات فقط: لا روابط، ولا أجهزة، ولا مسارات مطلقة، ولا `..`. أسماء الملفات بترميز UTF-8
وبصيغة NFC.

## البيان

يقع `manifest.toml` في جذر الحزمة:

```toml
schema       = 1
id           = "dev.example.silence-chapters"
version      = "1.4.0"
plugin_api   = "1.2"
maktaba      = ">=0.6.0, <0.9.0"
platforms    = ["any"]
languages    = ["ar", "en"]
licence      = "MIT"
price        = "free"
data_version = 2

[strings]
name    = "plugin.name"
summary = "plugin.summary"

[capabilities]
required = ["library.read", "media.audio", "chapters.write"]
optional = ["notify.admin"]

[resources]
memory_mb   = 256
timeout_s   = 300
fuel        = "default"
scratch_mb  = 0
data_mb     = 50
concurrency = 2

[[stage]]
name        = "find_silences"
subject     = "video"
export      = "run-find-silences"
version     = 3
may_request = ["write_chapters"]
  [[stage.trigger]]
  event = "audio.ready"

[[schedule]]
stage = "weekly_report"
every_minutes_setting = "report_interval"

[settings]
schema = "ui/settings.schema.json"
```

## قواعد الحقول

| الحقل | القاعدة |
|---|---|
| `schema` | `1` |
| `id` | بصيغة DNS المعكوسة، من 3 إلى 6 مقاطع من `[a-z0-9-]`، بطول 100 حرف على الأكثر، بأحرف صغيرة فقط |
| `version` | بصيغة semver 2.0، دون بيانات البناء الوصفية |
| `plugin_api` | قيمة `MAJOR.MINOR` من `maktaba:plugin` التي بنيت عليها |
| `maktaba` | نطاق إصدارات الخادم بحد أدنى؛ والحد الأعلى مطلوب للإدراج في المتجر |
| `platforms` | `["any"]`، أو مجموعة جزئية من `darwin-arm64` و`windows-amd64` و`linux-amd64` و`linux-arm64` |
| `languages` | رموز BCP 47 للمحتوى الذي تُحسن التعامل معه (مرشّح في المتجر)؛ يجوز أن تكون فارغة |
| `licence` | معرّف SPDX أو `proprietary` |
| `price` | `free`، أو معرّف منتج في المتجر (المرحلة 3) |
| `data_version` | عدد صحيح يساوي 1 أو أكثر؛ زِده حين يتغير شكل بياناتك المخزّنة |
| `migrate` | `true` حين تتعامل دالة `run` لديك مع المرحلة `__migrate` (انظر [ترحيل بياناتك](queue-and-api.ar.md#ترحيل-بياناتك))؛ وإن غاب أو كان `false` تؤخذ بياناتك كما هي |
| `strings.name`، `strings.summary` | مفاتيح موجودة في ملفي `i18n` كليهما؛ الاسم حتى 40 حرفًا والملخص حتى 140 حرفًا في كل لغة |
| `capabilities` | أسماء من قائمة قدرات مكتبة، دون تكرار بين القائمتين |
| `resources.*` | ضمن الحدود الواردة في [العزل](sandbox.ar.md)؛ وغياب القيمة يعني القيمة الافتراضية |
| `stage[]` | من 1 إلى 20 مرحلة؛ `name` يطابق `[a-z][a-z0-9_]{0,39}`؛ `subject` إحدى القيم `video` أو `series` أو `season` أو `library` أو `catalog`؛ `export` يسمّي دالة يصدّرها المكوّن؛ `version` يساوي 1 أو أكثر؛ `may_request` لا يسمّي إلا مراحلك أنت |
| `stage.trigger[]` | حدث `event` يطابق موضوعه موضوع المرحلة |
| `schedule[]` | مرحلة من نوع `catalog`؛ `every_minutes_setting` يسمّي حقل إعدادات صحيحًا قيمة `minimum` فيه 15 أو أكثر |
| `settings.schema` | ملف في `ui/` (راجع [مراحل الطابور والواجهة البرمجية للإضافات](queue-and-api.ar.md#الإعدادات)) |
| `panel[]` | المرحلة 3؛ يُقبل ويُتجاهَل حتى ذلك الحين |

### المعرّفات المحجوزة

هذه البادئات محجوزة ومرفوضة: `tv.maktaba.` (عدا `tv.maktaba.community.`)، و`dev.maktaba.`،
وأي مقطع قيمته `maktaba` أو `official`. لا تُوحَّد حالة أحرف المعرّفات أبدًا، لذا يُرفض
`Dev.Example` بدل أن يُحوَّل إلى أحرف صغيرة.

### أسماء المراحل في الطابور

تظهر مرحلتك `find_silences` في الطابور باسم `p_<8 hex of sha256(id)>_find_silences`. يبقى الاسم
ثابتًا عبر إصداراتك، ولا يمكن أن تتصادم إضافتان أبدًا فيما بينهما أو مع مراحل مكتبة نفسها.

### إصدارات المراحل

زِد قيمة `version` لمرحلة ما حين تريد أن تعمل من جديد على كل ما أنجزته سابقًا. ولا يعيد إصدار جديد
من الإضافة وحده تنفيذ العمل، ما لم يتغير إصدار المرحلة أو مدخلاتها.

## الأخطاء

أخطاء التحقق رموز مصحوبة بعبارات بالإنجليزية والعربية، منها مثلًا
`manifest_unknown_key` و`manifest_bad_id` و`manifest_reserved_id`
و`manifest_missing_string` و`manifest_bad_capability` و`manifest_limit`
و`manifest_subject_mismatch` و`manifest_schema_newer` و`package_bad_path`.

## النصوص

يجب أن يوجد كل مفتاح يسمّيه البيان وملفات الواجهة في `i18n/en.json` و`i18n/ar.json` كليهما.
تُحسب الأطوال بنقاط ترميز Unicode لا بالبايتات. ويجب ألا تحتوي النصوص المعروضة للمستخدمين على
عناوين URL.
