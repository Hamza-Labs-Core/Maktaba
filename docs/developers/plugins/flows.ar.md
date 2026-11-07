# التدفّقات: كيف ترتبط الإضافات بالطابور

> **الحالة:** صيغة التدفّق وحقول الرسم في البيان وفحص التدفّق **في `main`**: يفحص الجهاز
> الرئيسي كل تثبيت وتشغيل وتحديث وتراجع، ويستخدم التحقّق في المتجر الفحص نفسه. وإطلاق الأحداث
> وسلالة المهام وقت التشغيل، والمخطط المرسوم، والإضافات على الأجهزة المنضمّة تأتي في الإصدارات
> التالية من الملحمة 54. والحقول والرموز بدقة في [مرجع الإضافات](reference.ar.md).

لا ترتبط الإضافة بالطابور إلا عبر الرسم البياني الذي تعلنه. ولا طريق غيره: لا يُنشأ أبدًا
رابط أو طلب أو حدث خارج الرسم المُعلَن، وترفض كلٌّ من الواجهة البرمجية للإضافات ومحمِّل
الجهاز الرئيسي وخط المعالجة أيَّ شيء منها.

## التدفّق

يصف **التدفّق** مجموعة من الإضافات تعمل معًا: مراحل كل إضافة، والأحداث الأساسية التي ترتبط بها
كلٌّ منها، والأحداث التي تنشرها، ومن يشترك في ماذا. وهو الملف `flow.toml` في جذر كل حزمة من
المجموعة، بالبايتات نفسها في كلٍّ منها، موقَّعًا مع الحزمة. والإضافة الواحدة تدفّقٌ من عضو واحد.

```toml
schema  = 1
id      = "dev.example.chapters"
version = "1.2.0"

[strings]
name    = "flow.name"

[[plugin]]
id      = "dev.example.silences"
version = ">=1.4.0, <2.0.0"

  [[plugin.stage]]
  name     = "find_silences"
  subject  = "video"
  triggers = ["audio.ready"]
  emits    = ["silences.found"]

  [[plugin.event]]
  name    = "silences.found"
  subject = "video"
  payload = "events/silences.found.schema.json"

[[plugin]]
id      = "dev.example.chapter-writer"
version = ">=1.0.0, <2.0.0"

  [[plugin.stage]]
  name        = "write_chapters"
  subject     = "video"
  triggers    = ["plugin:dev.example.silences/silences.found"]
  may_request = ["tidy"]

  [[plugin.stage]]
  name     = "tidy"
  subject  = "video"
  triggers = ["stage:write_chapters"]
```

وهذا التدفّق مرسومًا:

```
audio.ready ──► silences/find_silences ──► ◆ silences.found ──► chapter-writer/write_chapters ──► chapter-writer/tidy
```

وللمُشغِّل إحدى ثلاث صيغ:

| الصيغة | المعنى |
|---|---|
| `audio.ready` | بعد حدث أساسي ثابت ([القائمة](queue-and-api.ar.md)) |
| `stage:<name>` | بعد مرحلة أخرى من الإضافة نفسها، حين تتغيّر مخرجاتها |
| `plugin:<id>/<event>` | حين تنشر الإضافة `<id>` الحدث `<event>` |

## نصيب البيان

يحمل كل بيان نصيبه من التدفّق، ويجب أن يطابقه تمامًا: المراحل نفسها وموضوعاتها، والمُشغِّلات
والأحداث المُطلَقة والطلبات، والأحداث المنشورة نفسها، ومدخلًا في `[[depends]]` لكل إضافة يشترك
في أحداثها.

```toml
[[stage]]
name    = "find_silences"
subject = "video"
export  = "run-find-silences"
version = 3
emits   = ["silences.found"]
  [[stage.trigger]]
  event = "audio.ready"

[[event]]
name    = "silences.found"
subject = "video"
payload = "events/silences.found.schema.json"   # a JSON Schema in your package
```

ومن جهة المشترك:

```toml
[[stage]]
name    = "write_chapters"
subject = "video"
export  = "run-write-chapters"
version = 1
  [[stage.trigger]]
  event = "plugin:dev.example.silences/silences.found"
  [[stage.trigger]]
  stage = "find_more"                           # or after one of its own stages

[[depends]]
plugin  = "dev.example.silences"
version = ">=1.4.0, <2.0.0"
```

ويطلب المشترك أيضًا الصلاحية `events.subscribe:<publisher id>`، ويوافق عليها المسؤول كأي صلاحية
أخرى.

## القواعد

1. **أوراقٌ بعد الأساس.** لا تعمل مراحلك إلا بعد الأحداث الأساسية. ولا يصل شيء مما تفعله مراحلك
   إلى مرحلة أساسية: لا طلب ولا مُشغِّل ولا حدث يسمّي واحدة منها، ولا تنتظر مرحلة أساسية مخرجاتك.
2. **الإضافات الأخرى عبر الأحداث المنشورة وحدها.** لا تسمّي أبدًا مرحلة إضافة أخرى؛ بل تشترك
   في حدث تنشره. وتُفحص الحمولة بمخطط الناشر حين يُطلَق الحدث.
3. **بلا حلقات.** لا حلقة في الرسم المجمَّع للجهاز الرئيسي (الأحداث الأساسية، ومراحل كل إضافة
   مفعَّلة وأحداثها). ويسمّي الرفض الحلقة، مثلًا:
   `dev.example.alpha/tag → plugin:dev.example.alpha/a.tagged → dev.example.beta/check →
   plugin:dev.example.beta/b.checked → dev.example.alpha/tag`.
4. **16 خطوة على الأكثر.** لا تطول سلسلة مراحل مُعلَنة من حدث أساسي عن 16، وهو حدّ القفزات وقت
   التشغيل.
5. **كل شيء موجود.** كل حدث مسمّى إمّا حدث أساسي وإمّا تنشره إضافة من التدفّق أو إضافة يعتمد
   عليها، بإصدار يقبله، وعن النوع نفسه من الموضوعات.

**أين يُفحص.** يُجري التحقّق في المتجر الفحص على التدفّق المرفوع والإضافات التي يعتمد عليها،
ويرفض التدفّق الذي يخالف قاعدة، مسمّيًا الحلقة أو الرابط الخاطئ. ويُجري الجهاز الرئيسي الفحص
نفسه مرة أخرى عند التثبيت والتشغيل والتحديث والتراجع، على كل إضافة مفعَّلة: فقد يجمع جهاز رئيسي
تدفّقات لم يرها المتجر معًا قط، والتثبيت من ملف لا يمرّ بالمتجر. والإضافة التي تغيب الإضافة التي
تسبقها تُثبَّت مع ذلك؛ وتبقى مراحلها المشتركة خاملة، وتذكر صفحتها أيّ إضافة غائبة.

## الرموز

| الرمز | متى |
|---|---|
| `flow_cycle` | التغيير سيُغلق حلقة؛ ويُسمّى المسار |
| `flow_core_edge` | مرحلة تصل إلى مرحلة أساسية |
| `flow_foreign_stage` | مرحلة تسمّي مرحلة إضافة أخرى |
| `flow_foreign_event` | مرحلة تُطلق حدثًا لا تنشره إضافتها |
| `flow_unknown_event` | مُشغِّل يسمّي حدثًا لا ينشره أحد |
| `flow_subject_mismatch` | حدث عن نوع موضوع غير الذي تعمل عليه المرحلة |
| `flow_missing_plugin` | إضافة سابقة غائبة أو خارج المدى (تحذير على الجهاز الرئيسي) |
| `flow_too_deep` | سلسلة أطول من 16 مرحلة |
| `flow_manifest_mismatch` | البيان يختلف عن مدخله في التدفّق |
| `flow_not_member` | الإضافة ليست في تدفّقها بهذا الإصدار |

وعلى الجهاز الرئيسي يكون الرفض `409 plugin_flow_cycle` (لحلقة) أو `409 plugin_flow_invalid`،
مع كل نتيجة بالإنجليزية والعربية.
