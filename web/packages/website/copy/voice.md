# How maktaba.tv sounds

The voice guide for every page on maktaba.tv (story 60.02 part A). English first, then
[the Arabic guide](#كيف-يتكلّم-موقع-maktabatv), written on its own in Arabic: neither half
is a translation of the other. Five minutes for each.

## Who we write for

- A household with films, series and recordings, kept on a computer or a NAS at home.
- One person sets Maktaba up. Everyone else watches: children, parents, grandparents, guests.
- Most of them never see an admin screen. Write for the person on the sofa first, and for the
  person who set it up second.

## How we sound

- **Plain, warm, calm, sure.** Like a friend who knows the machine explaining it at the
  kitchen table. Never a salesperson, never a manual.
- **Say "you" and "your home".** The library is theirs, not ours.
- **What it does for you, then how it works.** "Maktaba makes subtitles for your films", then,
  if it helps, "it listens to the sound on the computer at home".
- **Sure without boasting.** State what Maktaba does. No "best", "powerful", "seamless",
  "revolutionary".

## Words we use, and words we don't

| Use                                       | Not                                    |
| ----------------------------------------- | -------------------------------------- |
| your films and series, your library       | media assets, content                  |
| your home, the computer that runs Maktaba | instance, server node, deployment      |
| watch on your TV                          | client playback                        |
| Maktaba makes subtitles                   | ASR, transcription pipeline            |
| it updates itself                         | auto-update daemon                     |
| the person who runs your home             | administrator (on a page for everyone) |
| a message, what it means                  | error code, exception                  |

A technical word stays only where a person must type it or will see it on another product's
screen: Docker, compose, DSM, a file name, a menu on a NAS. Put it in `code` when it is typed,
in **bold** when it is clicked.

## Sentences

- Short. One idea each. Active voice: "Maktaba updates itself", not "updates are applied".
- No exclamation marks. No "simply", "just" or "easy": what is easy for us may not be for them.
- Numbers as figures, before units, with a space: 4 GB, 15 minutes, 90 days.
- Sentence case for every title and button. A button says what happens: "Choose your install
  guide", "Copy the command".
- An action keeps its name everywhere: the button "Copy" leads to "Copied".
- Dates in full: 11 November 2026.

## Errors and problems

Every message page (60.06) and every message in the apps follows the same three steps:

1. **What happened**, in one sentence, with no blame. "The computer that runs Maktaba didn't
   answer", not "You entered an invalid path".
2. **What you can do**, as steps when there is more than one.
3. **Whom to ask**: the person who runs your home, or Maktaba on GitHub.

No apology, no mood, no "Oops". Say plainly that nothing is lost when that is true.

## Names

- **Maktaba** in Latin text, always with a capital M, never "the Maktaba".
- **Maktaba Cloud** for the optional service. maktaba.tv and maktaba.dev in lower case.
- Other products as their makers write them: Synology DSM, TrueNAS, Unraid, Apple TV.

---

<div dir="rtl" lang="ar">

## كيف يتكلّم موقع maktaba.tv

هذا دليل الأسلوب لكل صفحة عربية على الموقع، وقد كُتب بالعربية من أوله، لا نقلًا عن الدليل
الإنجليزي. يُقرأ في خمس دقائق.

### لمن نكتب

- لبيتٍ عنده أفلام ومسلسلات وتسجيلات، محفوظة على حاسوب أو خادم تخزين في المنزل.
- يُعدّ البرنامجَ شخصٌ واحد، ويشاهد الباقون: الأولاد والوالدان والأجداد والضيوف.
- أكثرهم لن يرى شاشة الإدارة أبدًا. فاكتبوا أولًا لمن يجلس أمام التلفاز، ثم لمن أعدّ الجهاز.

### كيف نبدو

- **واضحون، ودودون، هادئون، واثقون.** كأنّ قريبًا يعرف الجهاز يشرحه لأهل البيت على مائدة
  العشاء. لا بائعًا، ولا كتيّب تعليمات.
- **نخاطب أهل البيت بصيغة الجمع:** «بيتكم»، «مكتبتكم»، «يمكنكم». فالجمع يشمل الجميع، رجالًا
  ونساءً، ويليق بخطاب أسرة كاملة. وحين لا يحتاج الكلام إلى مخاطَب، نستعمل المبني للمجهول
  أو المصدر: «تُثبَّت الإصدارات الجديدة في الساعات الهادئة».
- **الأزرار بالمصدر:** «اختيار دليل التثبيت»، «نسخ»، «تنزيل التطبيقات»، فهي تسمية لما سيحدث،
  لا أمرٌ لشخص بعينه.
- **نقول ما يقدّمه البرنامج قبل أن نقول كيف يعمل:** «يصنع برنامج مكتبة الترجمات لأفلامكم»، ثم
  إن احتاج الأمر: «فهو يستمع إلى الصوت على حاسوب البيت».
- **نثق بلا مبالغة:** لا «الأفضل» ولا «الأقوى» ولا «ثوري».

### الكلمات

| نقول                                        | لا نقول                              |
| ------------------------------------------- | ------------------------------------ |
| أفلامكم ومسلسلاتكم، مكتبتكم                 | الأصول الإعلامية، المحتوى            |
| الجهاز الرئيسي، الحاسوب الذي يشغّل البرنامج | النسخة، العقدة، النشر                |
| المشاهدة على التلفاز                        | التشغيل لدى العميل                   |
| يصنع البرنامج الترجمات                      | التعرّف الآلي على الكلام، خط التفريغ |
| يحدّث نفسه                                  | خدمة التحديث التلقائي                |
| من يدير الجهاز الرئيسي                      | المسؤول التقني                       |
| الرسالة ومعناها                             | رمز الخطأ، الاستثناء                 |

«الجهاز الرئيسي» هو اسم البيت في تطبيقات مكتبة، فنستعمله كما تستعمله. وتبقى الكلمة التقنية
بحروفها اللاتينية حيث يكتبها القارئ أو يراها على شاشة منتج آخر: Docker، وcompose، وDSM،
واسم ملف، واسم قائمة في جهاز التخزين. فإن لم يكن لها اسم عربي مستقر، نصف معناها بالعربية أولًا
ثم نكتبها بين «»: «افتحوا لوحة التحكم «Control Panel»».

### الجمل

- **جمل قصيرة، لكل جملة فكرة واحدة،** وبترتيب الجملة العربية: الفعل أولًا حين يحسن ذلك.
  «يحدّث البرنامج نفسه»، لا «البرنامج يقوم بتحديث نفسه».
- **لا «قم بـ»:** نكتب الفعل نفسه. «احفظوا الملف»، لا «قوموا بحفظ الملف».
- **لا علامات تعجّب،** ولا «ببساطة» ولا «فقط» ولا «سهل».
- **علامات الترقيم العربية:** الفاصلة «،» والفاصلة المنقوطة «؛» وعلامة الاستفهام «؟».
- **الأرقام بالأرقام الغربية كما في التطبيقات:** 4 غيغابايت، 15 دقيقة، 90 يومًا. ويسبق الرقمُ
  الوحدة.
- **التواريخ كاملة:** 11 نوفمبر 2026.

### الأخطاء والرسائل

لكل رسالة ثلاث خطوات، في صفحتها (60.06) وفي التطبيقات:

1. **ما الذي حدث،** في جملة واحدة، بلا لوم: «لم يردّ الحاسوب الذي يشغّل البرنامج»، لا «أدخلتم
   مسارًا غير صالح».
2. **ما الذي يمكنكم فعله،** خطواتٍ إن كانت أكثر من خطوة.
3. **من يمكنه المساعدة:** من يدير الجهاز الرئيسي في البيت، أو فريق مكتبة على GitHub.

لا اعتذار، ولا «عفوًا!». وإن لم يضِع شيء، نقول ذلك صراحةً: «مكتبتكم في أمان».

### الأسماء

- **«مكتبة»** في النص العربي، كما يكتبها تطبيق الويب: «سحابة مكتبة»، «تطبيقات مكتبة».
- وحين تُقرأ الكلمة على معناها العام، نكتب «برنامج مكتبة» في أول ذكرٍ لها في الصفحة:
  «يعمل برنامج مكتبة على حاسوب في بيتكم»، لا «تعمل مكتبة على حاسوب».
- أسماء المنتجات الأخرى كما يكتبها أصحابها: Synology DSM، وTrueNAS، وApple TV.

### كيف نكتب كل نص

- كل نص عربي يُكتب من المعنى، لا جملةً جملة عن الإنجليزية، ولا بالترجمة الآلية أبدًا.
- اقرؤوا النص بصوت مسموع: إن بدا كأنه مترجم، فأعيدوا كتابته.

</div>
