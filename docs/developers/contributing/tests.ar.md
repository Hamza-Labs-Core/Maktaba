# تشغيل الاختبارات

لكل مجموعة اختبارات هدف في `make`، والتكامل المستمر يستدعي الأهداف نفسها. شغّل `make test`
قبل أن تدفع تغييراتك؛ فهو يشغّل مستوى اختبارات الوحدة للغة Go وبايثون وتطبيق الويب.

## المستويات

| المستوى | ما الذي يُشغَّل | الإدخال والإخراج المسموح | هدف `make` |
|---|---|---|---|
| الوحدة | منطق بحت | لا شيء: المقابس محظورة، والملفات في المجلدات المؤقتة فقط | `make test-unit` (أو `make test`) |
| التكامل | PostgreSQL وChroma وFFmpeg حقيقية، ووسائط اختبار جاهزة | الخدمات داخل العنقود | `make test-integration` |
| من طرف إلى طرف | Playwright و`pytest` على حزمة `compose` | الحزمة كاملة | `make test-e2e` |
| الوسائط من طرف إلى طرف | FFmpeg وPostgreSQL حقيقيان | الحزمة كاملة | `make test-media-e2e` |
| الأداء في التكامل المستمر | مجموعة أداء مصغّرة | الحزمة كاملة | `make perf-ci` |

لكل مستوى ميزانية زمنية، ولكل اختبار حدّ مرن (يُصدر تحذيرًا) وحدّ صارم يساوي ثلاثة أضعاف
الحدّ المرن (يُفشل الاختبار). القواعد الكاملة في
[docs/testing.md](https://github.com/Hamza-Labs-Core/Maktaba/blob/main/docs/testing.md).

## Go

```sh
make test-unit-go          # every Go module, -short, under -race, with budgets
cd api && go test -short ./...           # one module, quickly
cd api && go test -short ./internal/handlers/...   # one area
```

- اختبارات الوحدة هي الافتراضية، وتُشغَّل عبر `go test -short`.
- تحمل اختبارات التكامل الوسم `//go:build integration`. الاختبارات التي تحتاج إلى PostgreSQL تقرأ
  `MAKTABA_TEST_PG_URL`، وتنشئ قاعدة بيانات مؤقتة خاصة بها وتُرحّلها ثم تحذفها.
- لمُرحِّل السحابة أهدافه الخاصة: `make relay-test` (و`make relay-ci` للفحص الساكن
  والاختبار والبناء).

## خط المعالجة (بايثون، عبر `uv`)

```sh
make test-unit-py                        # pytest -m unit
cd pipeline && uv run --extra dev pytest -m unit
cd pipeline && uv run --extra dev ruff check . && uv run --extra dev mypy
```

علّم الاختبارات بـ`@pytest.mark.unit` أو `@pytest.mark.integration` أو `@pytest.mark.e2e`.
يشمل `mypy --strict` المجلد `tests/` أيضًا، لذا تحتاج دوال الاختبار إلى تعليقات نوع الإرجاع
(`-> None`). شغّل أوامر `uv` واحدًا تلو الآخر؛ فالتشغيل المتوازي قد يتسابق على البيئة الافتراضية.

## الويب (`vitest`)

```sh
make test-unit-web                       # pnpm -C web run test:unit
cd web && pnpm install && pnpm test:unit
cd web && pnpm exec vitest related src/path/to/file.tsx   # tests for one file
```

يشغّل `make lint-web` كلًّا من `eslint` و`tsc --noEmit` و`prettier`. للحزم الموجودة في
`web/packages/` (مثل `devportal`) اختباراتها الخاصة.

## التطبيقات الأصلية

- **Apple:** يشغّل `swift test` في `apps/apple/MaktabaKit/` اختبارات الحزمة دون محاكٍ.
  ويشغّل `xcodebuild test -scheme "Maktaba Player Mac"` الاختبارات المستضافة لمشغّل Mac.
  تُشغَّل مخططات اختبارات الواجهة لأجهزة iPhone وiPad وMac في التكامل المستمر؛ وتجنّب تشغيل
  اختبارات الواجهة على جهاز مشترك، لأنها تستحوذ على الشاشة.
- **Android:** من داخل `apps/android/`:

```sh
./gradlew :client:test
./gradlew :core:testDebugUnitTest
./gradlew :player:testDebugUnitTest :remote:testDebugUnitTest
./gradlew :player:verifyRoborazziDebug   # screenshot tests against references
```

- **التلفاز:** يبني `make tv-build-ios` و`make tv-build-android` تطبيقات التلفاز.
- **رموز التصميم:** يفشل `make test-tokens` إذا كان `Tokens.swift` أو `Tokens.kt` المولَّدان
  قديمين، أو إذا قلّ تباين نص عن 4.5:1.

## بوابة الاختبارات المتأثرة

أي دفع يغيّر شيفرة تطبيق أو خدمة يجب أن يغيّر أيضًا الاختبارات التي تغطيها.
تفرض الأداة `tools/affected-tests` ذلك في خطّاف ما قبل الدفع وفي التكامل المستمر.

ثبّت الخطّاف مرة واحدة لكل نسخة من المستودع:

```sh
tools/affected-tests/install-hook.sh
```

عند `git push` يقارن الخطّاف فرعك بـ`origin/main`، ويطبع الاختبارات المتأثرة مصنّفة حسب
المنصة، ويمنع الدفع في الحالتين التاليتين:

- ملف مصدري تغيّر دون أن يتغيّر أيٌّ من اختباراته، أو
- اختبار ما زال يستخدم نصًّا أو معرّف إتاحة لم يعد موجودًا في أي مصدر
  (فالاختبار قديم وسيفشل).

يحتاج تغيير الواجهة إلى تغيير في الاختبارات على كل منصة تستخدم اختباراتها المعرّف أو
النص الذي مسّه التغيير. وحين تنجح البوابة، يشغّل الخطّاف الاختبارات المتأثرة الآمنة محليًّا
(Go مع `-short`، وبايثون، و`vitest`، واختبارات Android على JVM، و`MaktabaKit`). أما أهداف اختبارات
واجهة Apple فتُترجَم فقط؛ ولا يُشغَّل أي محاكٍ من الخطّاف.

يجد الخطّاف اختبارات أي ملف من خريطة تغطية ينشرها التكامل المستمر على `main`، ومن بحث في
معرّفات الواجهة ونصوصها (يُحلّ عبر جداول النصوص)، ومن أعراف التسمية
(`foo.go` إلى `foo_test.go`، و`foo.ts` إلى `foo.test.ts`، و`Foo.kt` إلى `FooTest.kt`).

الخيارات:

- إذا كان عدم تغيير الاختبارات صحيحًا: أضف `No-Test-Change: <why>` تذييلًا لأي إيداع (commit)
  ضمن الدفع.
- للبوابة فقط دون تشغيل أي اختبار: `AFFECTED_TESTS_NO_RUN=1 git push`.
- للاستعلام دون دفع: `cd tools/affected-tests && go run . list` (أو `gate` أو `check`؛
  مع `--range a...b` و`--json`).
- يتخطى `git push --no-verify` الخطّاف، لكن مهمة `affected-tests` في التكامل المستمر تطبّق
  البوابة نفسها على طلب الدمج.

الاختبار الذي يفشل بعد تغييرك اختبار قديم يجب إصلاحه في التغيير نفسه، وليس فشلًا عشوائيًّا.
