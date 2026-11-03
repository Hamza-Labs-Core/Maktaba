# الإعداد المحلي

الإعداد المعتمد لليوم الأول بيئة تطوير مبنية على Docker مع إعادة تحميل حية، يقودها `make`.
يشغّل التكامل المستمر أهداف `make` نفسها، فما ينجح محليًا ينجح في التكامل المستمر.

## المتطلبات المسبقة

شغّل `make prereqs` لفحص جهازك. يسرد كل أداة مع الإصدار الذي وجده، ويطبع أوامر التثبيت لكل
ما ينقص.

| الأداة | الإصدار | الاستخدام |
|---|---|---|
| Docker | 24+ | حاويات بيئة التطوير |
| Docker Compose | v2.27+ | فحوص السلامة والملفات الشخصية المسمّاة |
| Git | 2.40+ | فحوص شجرة العمل والخطّافات |
| Go | 1.26+ | `api/` و`streaming/` و`cloud/` و`cmd/` والأدوات |
| Python وuv | Python 3.12+ وuv 0.4+ | `pipeline/` (يدير uv البيئة الافتراضية) |
| Node وpnpm | Node 20+ وpnpm 9+ | `web/` |
| FFmpeg | 6+ | البث واختبارات الوسائط |

يُنصح أيضًا بـ: `golangci-lint`، و`goose` (لعمليات الترحيل)، و`pre-commit`، و`jq`، و`shellcheck`.

تعمل Postgres 16 داخل Docker عبر `make dev`. لا تثبّت Postgres على الجهاز المضيف: فهي تتعارض
على المنفذ 5432 وتتخطى عمليات الترحيل التي تطبّقها البيئة.

تعمل البيئة على Apple Silicon وIntel كليهما. تحتاج واجهة التفريغ النصي MLX إلى Apple Silicon؛
وفي غيره يلجأ خط المعالجة إلى واجهة Whisper أخرى.

للتطبيقات الأصلية تحتاج أيضًا إلى أدوات المنصة: Xcode لـ `apps/apple/` و`apps/tv/tvos/`،
وAndroid SDK وJDK 17 لـ `apps/android/` و`apps/tv/`، وRust لتطبيق Tauri المكتبي.

## التشغيل الأول

```sh
git clone https://github.com/Hamza-Labs-Core/Maktaba.git
cd Maktaba
cp .env.example .env     # change ports here if the defaults clash
make prereqs
make dev                 # builds the dev images and starts the stack
make test                # unit tier
```

يسحب أول تشغيل لـ `make dev` الصور ويبنيها ويستغرق بضع دقائق؛ أما التشغيلات اللاحقة فأسرع.

## بيئة التطوير

يشغّل `make dev` كلًّا من Postgres وChroma والواجهة البرمجية والبث وخط المعالجة وتطبيق الويب:

| الخدمة | العنوان |
|---|---|
| الواجهة البرمجية | http://localhost:8080 |
| البث | http://localhost:8081 |
| الويب | http://localhost:5173 |
| Postgres | `postgres://maktaba:maktaba@localhost:5432/maktaba` (بيانات اعتماد للتطوير فقط) |
| Chroma | http://localhost:8000 |

تُطبَّق التعديلات تلقائيًا: يعيد `air` بناء خدمات Go، ويعيد `watchmedo` تشغيل خط المعالجة،
ويعيد Vite تحميل تطبيق الويب فوريًا.

أهداف مفيدة (يسردها `make help` كلها):

| الهدف | ما يفعله |
|---|---|
| `make dev-logs` | يتابع سجلات كل الخدمات |
| `make dev-ps` | يعرض حالة البيئة |
| `make dev-down` | يوقف البيئة ويُبقي وحدات التخزين |
| `make dev-clean` | يوقف البيئة ويمسح وحدات التخزين (يعيد ضبط قاعدة البيانات) |
| `make dev-build` | يعيد بناء صور التطوير بعد تغيير `Dockerfile.dev` |
| `make migrate` | يطبّق عمليات الترحيل على `$DATABASE_URL` |
| `make lint` | كل أدوات التدقيق، كما في التكامل المستمر |
| `make format` | ينسّق شيفرة كل لغة في مكانها |

لتشغيل حزمة compose الإنتاجية دون إعادة تحميل حية، استخدم `make compose-up`، أو
`docker compose -f deploy/compose/docker-compose.yml up --build`.

## تشغيل الخدمات على الجهاز المضيف

يمكنك أيضًا تشغيل الخدمات عمليات عادية بدلًا من الحاويات، مع قاعدة Postgres التي يوفرها
`make dev`:

- يبني `make server-dev` الملف `maktaba-server` دون واجهة الويب المدمجة، لتسريع الترجمة؛
  ويبنيه `make server` مع الواجهة.
- يشغّل `tools/dev/stack.sh` الواجهة البرمجية والبث وعمال خط المعالجة على جهاز واحد.
  اقرأ ترويسته أولًا: فهو يأخذ رابط قاعدة البيانات ومجلد السجلات وإعدادات أخرى من متغيرات
  البيئة، وقيمه الافتراضية تناسب إعداد مطوّر واحد بعينه، فاضبطها بما يناسب إعدادك.
- في `web/`، شغّل `pnpm install` ثم `pnpm dev` لتقديم تطبيق الويب عبر Vite.

## الخطّافات

ثبّت هذه مرة واحدة لكل نسخة من المستودع:

```sh
pre-commit install                  # fast lint on staged files
tools/affected-tests/install-hook.sh   # pre-push: changed code needs changed tests
```

راجع [تشغيل الاختبارات](tests.ar.md) لمعرفة ما يفعله خطّاف ما قبل الدفع.

## حل المشكلات

- **علقت حالة Postgres بعد تجربة على المخطط:** `make dev-clean && make dev`.
- **صورة تطوير لا تلتقط تغييرًا في Dockerfile:** `make dev-build`.
- **إعادة التحميل الفوري لا تصل إلى المتصفح:** اضبط `VITE_HMR_HOST` في `.env` على اسم
  المضيف الذي يستخدمه متصفحك.
- **خلف وكيل شبكة:** تحترم Go وuv وpnpm وcurl المتغيرات المعتادة `HTTP_PROXY` و`HTTPS_PROXY`
  و`NO_PROXY`؛ ويوجّه `MAKTABA_REGISTRY_MIRROR` في `.env` أداة Docker إلى مرآة.
- **تختلف نتائج التكامل المستمر عن النتائج المحلية:** هذا خلل في التطابق؛ افتح بلاغًا وأرفق
  الفرق.
