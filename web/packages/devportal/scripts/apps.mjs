// The Apps pages (/apps/ and one page per app), in English and Arabic: who each app is for,
// what it does, its platforms, how to get it and how it signs in, with screenshots.
//
// The list of apps follows the code and the release workflows: apps/apple (Player for iPhone,
// iPad and Mac, Remote; testflight.yml), apps/android (Player and Remote; play-bundles.yml),
// apps/tv (Apple TV, Android TV and Google TV; testflight.yml, play-bundles.yml,
// tv-release.yml), apps/desktop-player (Player for Windows and Linux; desktop-release.yml),
// apps/desktop (Maktaba, the server with the web app; desktop-release.yml) and web/ (the web
// app and its big-screen view).
//
// Every text is {en, ar}; the parity check (parity.mjs, checkApps) fails the build when one
// language is missing, the Arabic is not Arabic or the English carries Arabic script. Inline
// markup: **bold**, `code` and [text](href) (a "/" href is the page in the same language).
//
// The screenshots are in static/assets/apps/, `<name>-<lang>.webp` (one per language where
// the app was shot in both, else a single `<name>-en.webp` or `<name>-ar.webp`). They were
// taken on made-up test data only: the Apple apps' CI UI tests against MockHome, the Android
// Roborazzi screenshot tests, the desktop Player's off-screen Compose tests against its test
// home (TestHome's MockWebServer), and, for the web app and the big screen, a scratch home
// seeded with invented libraries or the web app in a headless browser with its API answered
// by invented data. The scan (scan.mjs) checks every image carries no metadata.

const RELEASES = "https://github.com/Hamza-Labs-Core/Maktaba/releases/latest";
const SRC = "https://github.com/Hamza-Labs-Core/Maktaba/tree/main";

/** One text in both languages. */
const t = (en, ar) => ({ en, ar });

/** A screenshot: `file` is the image's name without `-<lang>.webp` (or with it, when the
 * app was shot in one language only); `kind` is "phone" (tall) or "wide". */
const shot = (file, kind, en, ar) => ({ file, kind, alt: t(en, ar) });

// the sign-in, the same in every app that watches or runs a home
const SIGN_IN = [
  {
    p: t(
      "Every app starts at the **home picker**. **On this network** lists the homes that announce themselves on the local network, **Your homes** the ones this device used before, and **By address** takes the address you open Maktaba at in a browser, or a `maktaba://connect` link the home shared. Phones and tablets can also **scan a QR code**, and **Through Maktaba Cloud** reaches a home linked to a cloud account from anywhere.",
      "يبدأ كل تطبيق من **منتقي الجهاز الرئيسي**. يعرض قسم **على هذه الشبكة** الأجهزة الرئيسية التي تعلن عن نفسها في الشبكة المحلية، ويعرض قسم **أجهزتك الرئيسية** ما استخدمه هذا الجهاز من قبل، ويقبل قسم **بالعنوان** العنوان الذي تفتح به «مكتبة» في المتصفح، أو رابط `maktaba://connect` الذي شاركه الجهاز الرئيسي. ويمكن للهواتف والأجهزة اللوحية أيضًا **مسح رمز QR**، ويصل خيار **عبر سحابة مكتبة** إلى جهاز رئيسي مرتبط بحساب سحابي من أي مكان."
    ),
  },
  {
    p: t(
      "Then sign in with a username and password, or **with a code**: the device shows a code such as `ABCD-1234` and a QR code, and someone already signed in approves it. In Maktaba Remote, **Approve a TV or device** is at the top of every screen; in Maktaba Player it is under **Profile** (on the Mac, in the **Account** menu); in the web app it is the **Sign in a device** page (`/pair`). The new device is signed in as the person who approved it, with that person's role.",
      "ثم سجّل الدخول باسم المستخدم وكلمة المرور، أو **برمز**: يعرض الجهاز رمزًا مثل `ABCD-1234` ورمز QR، فيوافق عليه شخص سجّل دخوله من قبل. في تطبيق الإدارة عن بُعد يوجد زر **الموافقة على تلفاز أو جهاز** أعلى كل شاشة، وفي المشغّل تجده في **الملف الشخصي** (وعلى الماك في قائمة **الحساب**)، وفي تطبيق الويب هو صفحة **تسجيل دخول جهاز** (`/pair`). ويُسجَّل دخول الجهاز الجديد باسم من وافق عليه، وبالدور نفسه."
    ),
  },
  {
    p: t(
      "The home enforces every role itself. An app hiding a screen is a convenience, not the rule: a modified app holding a viewer's token is still a viewer.",
      "يفرض الجهاز الرئيسي كل دور بنفسه. أما إخفاء التطبيق لشاشة ما فهو تيسير لا قاعدة: فالتطبيق المعدَّل الذي يحمل رمز مشاهد يبقى مشاهدًا."
    ),
  },
];

const TESTING = t(
  "The apps are in testing and not in the public stores yet. Testers get the Apple apps from **TestFlight** and the Android apps from **Google Play's internal testing**: the home's owner adds each tester, an Apple ID to the TestFlight testers group or a Google account to the testers list. Every release tag uploads a new build to both, which reaches testers as an update. The Windows, Linux and Mac downloads are on the [latest release](" +
    RELEASES +
    ") page, and they update themselves.",
  "التطبيقات قيد الاختبار، وليست في المتاجر العامة بعد. يحصل المختبِرون على تطبيقات آبل من **TestFlight** وعلى تطبيقات أندرويد من **الاختبار الداخلي في Google Play**: يضيف مالك الجهاز الرئيسي كل مختبِر، بمعرّف آبل في مجموعة المختبِرين على TestFlight أو بحساب جوجل في قائمة المختبِرين. ويرفع كل وسم إصدار نسخة جديدة إلى الاثنين، فتصل إلى المختبِرين تحديثًا. أما تنزيلات ويندوز ولينكس والماك فهي في صفحة [أحدث إصدار](" +
    RELEASES +
    ")، وهي تحدّث نفسها."
);

const SHOTS_NOTE = t(
  "Every screenshot on these pages was taken in the dark theme on made-up test data: the Apple apps' screenshots come from their UI tests in CI, which run against a pretend home; the Android ones from the apps' screenshot tests; the Windows and Linux Player's from its screen tests, which also run against a pretend home; the web app and the big screen from a test home filled with invented libraries. The names, titles and codes in them are test data.",
  "التُقطت كل لقطات الشاشة في هذه الصفحات بالمظهر الداكن وعلى بيانات اختبار مصطنعة: لقطات تطبيقات آبل من اختبارات الواجهة في التكامل المستمر، وهي تعمل على جهاز رئيسي وهمي؛ ولقطات أندرويد من اختبارات لقطات الشاشة في التطبيقين؛ ولقطات المشغّل على ويندوز ولينكس من اختبارات شاشاته، وهي تعمل أيضًا على جهاز رئيسي وهمي؛ ولقطات تطبيق الويب والشاشة الكبيرة من جهاز رئيسي تجريبي مُلئ بمكتبات مختلَقة. فالأسماء والعناوين والرموز فيها كلها بيانات اختبار."
);

/** The pages: the overview (slug "") first, then one page per app. */
export const APPS = [
  {
    slug: "",
    title: t("Maktaba's apps", "تطبيقات مكتبة"),
    card: t("Every app, its platforms and how to get it.", "كل تطبيق ومنصاته وطريقة الحصول عليه."),
    lead: t(
      "Maktaba runs on a computer you own: the home. These apps are how people use it, to watch on a phone, a tablet, a computer or a television, and to run the home. Every app is in English and Arabic, and the Arabic is laid out right to left.",
      "يعمل «مكتبة» على حاسوب تملكه هو الجهاز الرئيسي. وهذه التطبيقات هي ما يستعمله الناس للوصول إليه: للمشاهدة على الهاتف أو الجهاز اللوحي أو الحاسوب أو التلفاز، ولإدارة الجهاز الرئيسي. وكل تطبيق متاح بالعربية والإنجليزية، وتُعرض العربية من اليمين إلى اليسار."
    ),
    sections: [
      {
        h: t("The apps at a glance", "التطبيقات في لمحة"),
        blocks: [
          {
            table: {
              head: [
                t("App", "التطبيق"),
                t("For", "لمن"),
                t("Platforms", "المنصات"),
                t("How to get it", "طريقة الحصول عليه"),
              ],
              rows: [
                [
                  t("[Maktaba Player](/apps/player/)", "[المشغّل](/apps/player/)"),
                  t("anyone who watches", "كل من يشاهد"),
                  t(
                    "iPhone, iPad, Mac, Android phones, foldables and tablets, Windows, Linux",
                    "آيفون، آيباد، ماك، هواتف أندرويد وأجهزته القابلة للطي واللوحية، ويندوز، لينكس"
                  ),
                  t(
                    "TestFlight; Google Play internal testing; the release downloads",
                    "TestFlight؛ الاختبار الداخلي في Google Play؛ تنزيلات الإصدار"
                  ),
                ],
                [
                  t("[Maktaba Remote](/apps/remote/)", "[الإدارة عن بُعد](/apps/remote/)"),
                  t("the home's admins", "مدراء الجهاز الرئيسي"),
                  t(
                    "iPhone, Android phones, foldables and tablets",
                    "آيفون، هواتف أندرويد وأجهزته القابلة للطي واللوحية"
                  ),
                  t(
                    "TestFlight; Google Play internal testing",
                    "TestFlight؛ الاختبار الداخلي في Google Play"
                  ),
                ],
                [
                  t("[Maktaba TV](/apps/tv/)", "[تطبيق التلفاز](/apps/tv/)"),
                  t("watching across the room", "المشاهدة من أنحاء الغرفة"),
                  t("Apple TV, Android TV, Google TV", "آبل تي في، أندرويد تي في، جوجل تي في"),
                  t(
                    "TestFlight on the Apple TV; Google Play internal testing, or the signed `.apk` when a release carries one",
                    "TestFlight على آبل تي في؛ الاختبار الداخلي في Google Play، أو ملف `.apk` الموقَّع حين يتضمنه الإصدار"
                  ),
                ],
                [
                  t("[Maktaba, the server](/apps/server/)", "[مكتبة، الخادم](/apps/server/)"),
                  t("whoever runs the home", "من يشغّل الجهاز الرئيسي"),
                  t("Mac, Windows, Linux", "ماك، ويندوز، لينكس"),
                  t("the release downloads; it updates itself", "تنزيلات الإصدار؛ ويحدّث نفسه"),
                ],
                [
                  t("[The web app](/apps/web/)", "[تطبيق الويب](/apps/web/)"),
                  t("everyone, and the admin pages", "الجميع، ومعه صفحات الإدارة"),
                  t(
                    "any modern browser; the big screen for a TV",
                    "أي متصفح حديث؛ والشاشة الكبيرة للتلفاز"
                  ),
                  t(
                    "the home's address on port `8088`, or through Maktaba Cloud",
                    "عنوان الجهاز الرئيسي على المنفذ `8088`، أو عبر سحابة مكتبة"
                  ),
                ],
              ],
            },
          },
          { p: TESTING },
        ],
      },
      {
        h: t("Finding the home and signing in", "العثور على الجهاز الرئيسي وتسجيل الدخول"),
        blocks: [
          ...SIGN_IN,
          {
            shots: [
              shot(
                "player-iphone-picker",
                "phone",
                "The home picker on an iPhone: on this network, through Maktaba Cloud, by address",
                "منتقي الجهاز الرئيسي على آيفون: على هذه الشبكة، وعبر سحابة مكتبة، وبالعنوان"
              ),
              shot(
                "player-iphone-code",
                "phone",
                "Signing in with a code on an iPhone: the code TEST-2345 and its QR code",
                "تسجيل الدخول برمز على آيفون: الرمز TEST-2345 ورمز QR الخاص به"
              ),
              shot(
                "web-pair",
                "wide",
                "The web app's Sign in a device page, where a code is typed to approve a device",
                "صفحة تسجيل دخول جهاز في تطبيق الويب، حيث يُكتب الرمز للموافقة على جهاز"
              ),
            ],
          },
        ],
      },
      {
        h: t("In every app", "في كل التطبيقات"),
        blocks: [
          {
            ul: [
              t(
                "**The status strip**: when part of the home is down, every app says so in one strip at the top, naming what is unavailable (playback, Live TV, search or processing), and shows nothing while all is well. The apps read the home's `/status.json`, which the home answers even while its API or its database is down.",
                "**شريط الحالة**: حين يتوقف جزء من الجهاز الرئيسي، يقول ذلك كل تطبيق في شريط واحد أعلى الشاشة، ويسمّي ما لا يعمل (التشغيل أو البث المباشر أو البحث أو المعالجة)، ولا يعرض شيئًا ما دام كل شيء يعمل. وتقرأ التطبيقات ملف `/status.json` من الجهاز الرئيسي، وهو يجيب عنه حتى حين تتوقف واجهته البرمجية أو قاعدة بياناته."
              ),
              t(
                "**Maktaba Cloud in the strip**: when an app reaches the home through Maktaba Cloud (away from home), the same strip also says when remote access or signing in with Maktaba Cloud is down or slow, and shows planned maintenance from a day before it starts, with **Details** to [status.maktaba.tv](https://status.maktaba.tv). The home's own trouble comes first. On the home's own network the cloud's line never shows, and if the status page can't be reached the apps show nothing and play as usual.",
                "**سحابة مكتبة في الشريط**: حين يصل التطبيق إلى الجهاز الرئيسي عبر سحابة مكتبة (خارج المنزل)، يقول الشريط نفسه أيضًا متى يتوقف الوصول عن بُعد أو تسجيل الدخول عبر سحابة مكتبة أو يبطؤ، ويعرض الصيانة المجدولة قبل موعدها بيوم، مع **التفاصيل** إلى [status.maktaba.tv](https://status.maktaba.tv). وتأتي مشكلات الجهاز الرئيسي نفسه أولًا. ولا يظهر سطر السحابة أبدًا على شبكة المنزل، وإن تعذّر الوصول إلى صفحة الحالة لا تعرض التطبيقات شيئًا وتشغّل كالمعتاد."
              ),
              t(
                "**The play queue**: **Play** and **Shuffle** on a library, a series, a season, a collection or a playlist start one queue that the home keeps for each person. **Play next** and **Add to queue** add a video to it, Up next plays from it first, and Home offers **Continue your queue** on every device.",
                "**قائمة الانتظار**: يبدأ زرّا **تشغيل** و**تشغيل عشوائي** في مكتبة أو سلسلة أو موسم أو مجموعة أو قائمة تشغيل قائمةَ انتظار واحدة يحفظها الجهاز الرئيسي لكل شخص. ويضيف إليها **شغّل تاليًا** و**أضف إلى قائمة الانتظار** مقطعًا، ويبدأ «التالي» منها أولًا، وتعرض الرئيسية **تابع قائمة الانتظار** على كل جهاز."
              ),
              t(
                "**Report a problem** in the web app and the Apple and Android apps (Settings or Profile, the account menu, the TVs' settings, and **Report this** on an error screen): it shows everything it will send before it does (the app's log of the last 30 minutes, already scrubbed, the last requests, crashes kept on the device, a screenshot only when you tick it and see it), and the home's admin can reply in **My reports**. **Crash reports to the home** are off until each person turns them on: every app asks once after signing in, with two equal buttons, and nothing leaves the device while the answer is no.",
                "**الإبلاغ عن مشكلة** في تطبيق الويب وتطبيقات آبل وأندرويد (الإعدادات أو الملف الشخصي، وقائمة الحساب، وإعدادات التلفاز، و**الإبلاغ عن هذا** في شاشة الخطأ): يعرض كل ما سيُرسله قبل الإرسال (سجل التطبيق لآخر 30 دقيقة منقّحًا، وآخر الطلبات، والأعطال المحفوظة على الجهاز، ولقطة شاشة فقط إن حدّدتها ورأيتها)، ويمكن لمشرف الجهاز الرئيسي الرد في **بلاغاتي**. أما **تقارير الأعطال إلى الجهاز الرئيسي** فمتوقفة حتى يشغّلها كل شخص: يسأل كل تطبيق مرة واحدة بعد تسجيل الدخول بزرّين متساويين، ولا يغادر الجهازَ شيءٌ ما دام الجواب لا."
              ),
            ],
          },
          {
            shots: [
              shot(
                "web-status-strip",
                "wide",
                "The status strip in the web app: playback and Live TV are unavailable right now",
                "شريط الحالة في تطبيق الويب: التشغيل والبث المباشر غير متاحين الآن"
              ),
            ],
          },
        ],
      },
      {
        h: t("How the apps are built", "كيف بُنيت التطبيقات"),
        blocks: [
          {
            p: t(
              "The Player, Remote and TV apps are native and talk to the home's API, the same API the web app uses. Each app's README in the source says how to build and test it.",
              "تطبيقات المشغّل والإدارة عن بُعد والتلفاز تطبيقات أصلية تتصل بواجهة الجهاز الرئيسي البرمجية، وهي الواجهة نفسها التي يستعملها تطبيق الويب. ويشرح ملف README لكل تطبيق في الشيفرة المصدرية طريقة بنائه واختباره."
            ),
          },
          {
            table: {
              head: [t("Apps", "التطبيقات"), t("Built with", "بُني بـ"), t("Source", "المصدر")],
              rows: [
                [
                  t(
                    "Player and Remote for iPhone, iPad and Mac",
                    "المشغّل والإدارة عن بُعد لآيفون وآيباد والماك"
                  ),
                  t("SwiftUI and the shared MaktabaKit", "‏SwiftUI وحزمة MaktabaKit المشتركة"),
                  t(`[\`apps/apple/\`](${SRC}/apps/apple)`, `[\`apps/apple/\`](${SRC}/apps/apple)`),
                ],
                [
                  t("Player and Remote for Android", "المشغّل والإدارة عن بُعد لأندرويد"),
                  t("Jetpack Compose", "‏Jetpack Compose"),
                  t(
                    `[\`apps/android/\`](${SRC}/apps/android)`,
                    `[\`apps/android/\`](${SRC}/apps/android)`
                  ),
                ],
                [
                  t("Player for Windows and Linux", "المشغّل لويندوز ولينكس"),
                  t("Compose for Desktop", "‏Compose for Desktop"),
                  t(
                    `[\`apps/desktop-player/\`](${SRC}/apps/desktop-player)`,
                    `[\`apps/desktop-player/\`](${SRC}/apps/desktop-player)`
                  ),
                ],
                [
                  t("Apple TV", "آبل تي في"),
                  t("SwiftUI and AVKit", "‏SwiftUI وAVKit"),
                  t(
                    `[\`apps/tv/tvos/\`](${SRC}/apps/tv/tvos)`,
                    `[\`apps/tv/tvos/\`](${SRC}/apps/tv/tvos)`
                  ),
                ],
                [
                  t("Android TV and Google TV", "أندرويد تي في وجوجل تي في"),
                  t("Compose for TV and Media3", "‏Compose for TV وMedia3"),
                  t(
                    `[\`apps/tv/android/\`](${SRC}/apps/tv/android)`,
                    `[\`apps/tv/android/\`](${SRC}/apps/tv/android)`
                  ),
                ],
                [
                  t("Maktaba, the server app", "مكتبة، تطبيق الخادم"),
                  t("Tauri 2 around the web app", "‏Tauri 2 حول تطبيق الويب"),
                  t(
                    `[\`apps/desktop/\`](${SRC}/apps/desktop)`,
                    `[\`apps/desktop/\`](${SRC}/apps/desktop)`
                  ),
                ],
                [
                  t("The web app and the big screen", "تطبيق الويب والشاشة الكبيرة"),
                  t("React, TypeScript and Vite", "‏React وTypeScript وVite"),
                  t(`[\`web/\`](${SRC}/web)`, `[\`web/\`](${SRC}/web)`),
                ],
              ],
            },
          },
          { p: SHOTS_NOTE },
        ],
      },
    ],
  },

  {
    slug: "player",
    title: t("Maktaba Player", "المشغّل"),
    card: t(
      "Watch: libraries, series, search, Live TV and your history, on phones, tablets and computers.",
      "للمشاهدة: المكتبات والسلاسل والبحث والبث المباشر وسجلّك، على الهواتف والأجهزة اللوحية والحواسيب."
    ),
    lead: t(
      "Maktaba Player is the app for watching. It browses the home's libraries and series, searches what is said in the videos, plays the Live TV channels, and picks up where you stopped on any device.",
      "المشغّل، واسمه بالإنجليزية Maktaba Player، هو تطبيق المشاهدة. يتصفح مكتبات الجهاز الرئيسي وسلاسله، ويبحث فيما يُقال داخل المقاطع، ويشغّل قنوات البث المباشر، ويكمل من حيث توقفت على أي جهاز."
    ),
    sections: [
      {
        h: t("Who it is for", "لمن هذا التطبيق"),
        blocks: [
          {
            p: t(
              "Anyone with an account on a home, whatever the role. It shows only the libraries and channels that account may see, and **Live TV** appears only when the person may watch a channel.",
              "لكل من له حساب على جهاز رئيسي، أيًّا كان دوره. ولا يعرض إلا المكتبات والقنوات التي يحق لذلك الحساب رؤيتها، ولا يظهر **البث المباشر** إلا حين يُسمح للشخص بمشاهدة قناة."
            ),
          },
        ],
      },
      {
        h: t("What it does", "ماذا يفعل"),
        blocks: [
          {
            ul: [
              t(
                "**Home**: what you were watching, the next lesson of each series you are in, what was added lately, and the rows the home suggests.",
                "**الرئيسية**: ما كنت تشاهده، والدرس التالي من كل سلسلة تتابعها، وما أُضيف حديثًا، والصفوف التي يقترحها الجهاز الرئيسي."
              ),
              t(
                "**Libraries**: each library with its series, collections, playlists and videos, filtered and sorted, with **Play** and **Shuffle**; a series shuffles too. Pin the libraries you use most.",
                "**المكتبات**: كل مكتبة بسلاسلها ومجموعاتها وقوائم تشغيلها ومقاطعها، مع التصفية والترتيب، ومعها **تشغيل** و**تشغيل عشوائي**؛ ويمكن تشغيل السلسلة عشوائيًا أيضًا. وثبّت المكتبات التي تستعملها أكثر."
              ),
              t(
                "**The play queue**: **Play next** and **Add to queue** on a video, Up next from the queue first, and **Continue your queue** on Home. On Android, **Save as smart playlist** on a library's filters.",
                "**قائمة الانتظار**: **شغّل تاليًا** و**أضف إلى قائمة الانتظار** في المقطع، ويبدأ «التالي» من قائمة الانتظار أولًا، و**تابع قائمة الانتظار** في الرئيسية. وعلى أندرويد **الحفظ كقائمة تشغيل ذكية** من مرشّحات المكتبة."
              ),
              t(
                "**Live TV**: **Now**, with what is on each channel and how far it has gone, and the **Guide**; choose a channel to watch it live.",
                "**البث المباشر**: قسم **الآن** بما يُعرض على كل قناة ومقدار ما مضى منه، و**الدليل**؛ اختر قناة لتشاهدها مباشرة."
              ),
              t(
                "**Search**: titles and transcripts; a transcript result plays the video at that moment.",
                "**البحث**: في العناوين والنصوص المفرَّغة؛ ونتيجة النص تشغّل المقطع عند تلك اللحظة."
              ),
              t(
                "**Profile**: watch history, favorites, playlists and sessions, **Approve a TV or device**, switch home, and this device's theme, language and playback settings.",
                "**الملف الشخصي**: سجل المشاهدة والمفضلة وقوائم التشغيل والجلسات، و**الموافقة على تلفاز أو جهاز**، وتبديل الجهاز الرئيسي، وإعدادات المظهر واللغة والتشغيل على هذا الجهاز."
              ),
              t(
                "**Watching**: subtitles and audio tracks, the next lesson, AirPlay and Picture in Picture on Apple devices, picture in picture and sound with the screen off on Android. On the Mac the video fills the window and the **Playback** menu has the keys. On Windows and Linux: the **Audio**, **Subtitles** and **Quality** menus, Up next, and the keys below.",
                "**المشاهدة**: الترجمات ومسارات الصوت، والدرس التالي، وAirPlay وصورة داخل صورة على أجهزة آبل، وصورة داخل صورة واستمرار الصوت مع إطفاء الشاشة على أندرويد. وعلى الماك يملأ المقطع النافذة، وفي قائمة **التشغيل** اختصارات لوحة المفاتيح. وعلى ويندوز ولينكس: قوائم **الصوت** و**الترجمة** و**الجودة**، و«التالي»، والمفاتيح المبيّنة أدناه."
              ),
              t(
                "**The status strip**: when the home's streaming is down, the Player says so before you press play.",
                "**شريط الحالة**: حين يتوقف البث في الجهاز الرئيسي، يقول المشغّل ذلك قبل أن تضغط على التشغيل."
              ),
              t(
                "**Crash reports and Report a problem** (iPhone, iPad, Mac and Android; on Windows and Linux in the **Account** and **Help** menus, without a screenshot): after signing in, the Player asks once whether to **send crash reports** to the home, with two equal buttons; it stays off until you say yes, and **Profile** keeps the switch. **Report a problem** in Profile (and **Report this** on an error screen) shows everything it will send before it does: the app's log of the last 30 minutes, already scrubbed, the last requests, the crashes kept on the device, and a screenshot only when you tick it and see it. It goes to the home's admin, who can reply in **My reports**.",
                "**تقارير الأعطال والإبلاغ عن مشكلة** (آيفون وآيباد والماك وأندرويد؛ وعلى ويندوز ولينكس في قائمتَي **الحساب** و**المساعدة**، دون لقطة شاشة): بعد تسجيل الدخول يسأل المشغّل مرة واحدة هل **يرسل تقارير الأعطال** إلى الجهاز الرئيسي، بزرّين متساويين؛ وتبقى متوقفة حتى توافق، ومفتاحها في **الملف الشخصي**. ويعرض **الإبلاغ عن مشكلة** في الملف الشخصي (و**الإبلاغ عن هذا** في شاشة الخطأ) كل ما سيُرسله قبل الإرسال: سجل التطبيق لآخر 30 دقيقة منقّحًا، وآخر الطلبات، والأعطال المحفوظة على الجهاز، ولقطة شاشة فقط إن حدّدتها ورأيتها. ويصل البلاغ إلى مشرف الجهاز الرئيسي، ويمكنه الرد عليك في **بلاغاتي**."
              ),
            ],
          },
          {
            p: t(
              "The layout follows the window: a tab bar on an iPhone and a sidebar on an iPad and the Mac; on Windows and Linux a rail at the start side (the right in Arabic) with **Home**, **Libraries**, **Live TV**, **Search** and **Profile**, each keeping its own pages; on Android a bar at the bottom of a phone, a rail or a drawer on a wider window, the list beside the page on a tablet, and the screen split at the hinge of a half-folded foldable.",
              "يتبع التصميم النافذة: شريط تبويب على آيفون وشريط جانبي على آيباد والماك؛ وعلى ويندوز ولينكس شريط في جهة البداية (اليمين بالعربية) فيه **الرئيسية** و**المكتبات** و**البث المباشر** و**البحث** و**الملف الشخصي**، ويحتفظ كل قسم بصفحاته؛ وعلى أندرويد شريط أسفل شاشة الهاتف، وشريط أو درج جانبي في النافذة الأعرض، والقائمة بجانب الصفحة على الجهاز اللوحي، والشاشة مقسومة عند مفصل الجهاز القابل للطي حين يكون نصف مطوي."
            ),
          },
        ],
      },
      {
        h: t("Keys on Windows and Linux", "المفاتيح على ويندوز ولينكس"),
        blocks: [
          {
            table: {
              head: [t("While watching", "أثناء المشاهدة"), t("Key", "المفتاح")],
              rows: [
                [t("Play or pause", "تشغيل أو إيقاف مؤقت"), t("`Space`", "`Space`")],
                [
                  t(
                    "Back or forward 10 seconds (30 with Shift)",
                    "رجوع أو تقدّم 10 ثوانٍ (30 مع Shift)"
                  ),
                  t("`←` and `→`, mirrored in Arabic", "`←` و`→`، معكوسان بالعربية"),
                ],
                [t("Volume", "مستوى الصوت"), t("`↑` and `↓`", "`↑` و`↓`")],
                [t("Next episode", "الحلقة التالية"), t("`N`", "`N`")],
                [t("Subtitles", "الترجمة"), t("`S`", "`S`")],
                [t("Mute", "كتم الصوت"), t("`M`", "`M`")],
                [t("Full screen", "ملء الشاشة"), t("`F`", "`F`")],
                [t("Big screen", "الشاشة الكبيرة"), t("`F11`", "`F11`")],
                [
                  t(
                    "Leave full screen, or close the video",
                    "الخروج من ملء الشاشة، أو إغلاق المقطع"
                  ),
                  t("`Esc`", "`Esc`"),
                ],
              ],
            },
          },
          {
            p: t(
              "Anywhere in the app, `Ctrl+F` or `/` opens Search, and `Alt+←` (or the mouse's back button) goes back a page.",
              "وفي أي مكان من التطبيق يفتح `Ctrl+F` أو `/` البحث، ويعود `Alt+←` (أو زر الرجوع في الفأرة) صفحة إلى الوراء."
            ),
          },
        ],
      },
      {
        h: t("Platforms and how to get it", "المنصات وطريقة الحصول عليه"),
        blocks: [
          {
            table: {
              head: [
                t("Platform", "المنصة"),
                t("App", "التطبيق"),
                t("How to get it", "طريقة الحصول عليه"),
              ],
              rows: [
                [
                  t("iPhone", "آيفون"),
                  t("Maktaba Player", "المشغّل"),
                  t("TestFlight", "‏TestFlight"),
                ],
                [
                  t("iPad", "آيباد"),
                  t("Maktaba Player for iPad", "المشغّل لآيباد"),
                  t("TestFlight", "‏TestFlight"),
                ],
                [
                  t("Mac (macOS 15 or later, Apple silicon)", "ماك (macOS 15 أو أحدث، بمعالج آبل)"),
                  t("Maktaba Player for Mac", "المشغّل للماك"),
                  t("TestFlight", "‏TestFlight"),
                ],
                [
                  t(
                    "Android phones, foldables and tablets (Android 8 or later)",
                    "هواتف أندرويد وأجهزته القابلة للطي واللوحية (أندرويد 8 أو أحدث)"
                  ),
                  t("Maktaba Player", "المشغّل"),
                  t("Google Play internal testing", "الاختبار الداخلي في Google Play"),
                ],
                [
                  t("Windows 10 or later (x86-64)", "ويندوز 10 أو أحدث (x86-64)"),
                  t("Maktaba Player", "المشغّل"),
                  t(
                    `\`Maktaba-Player_<v>_x64.msi\` from the [latest release](${RELEASES}); it updates itself`,
                    `\`Maktaba-Player_<v>_x64.msi\` من [أحدث إصدار](${RELEASES})؛ ويحدّث نفسه`
                  ),
                ],
                [
                  t("Linux (x86-64 and ARM64)", "لينكس (x86-64 وARM64)"),
                  t("Maktaba Player", "المشغّل"),
                  t(
                    `the \`.deb\` or \`.rpm\` from the [latest release](${RELEASES}); it updates itself`,
                    `ملف \`.deb\` أو \`.rpm\` من [أحدث إصدار](${RELEASES})؛ ويحدّث نفسه`
                  ),
                ],
              ],
            },
          },
        ],
      },
      { h: t("Signing in", "تسجيل الدخول"), blocks: SIGN_IN.slice(0, 2) },
      {
        h: t("Screenshots", "لقطات الشاشة"),
        blocks: [
          {
            shots: [
              shot(
                "player-iphone-home",
                "phone",
                "The Player's Home on an iPhone: a new series, Continue Watching and On Deck",
                "الرئيسية في المشغّل على آيفون: سلسلة جديدة، وتابع المشاهدة، والدرس التالي"
              ),
              shot(
                "player-iphone-library",
                "phone",
                "A library in the Player on an iPhone",
                "مكتبة في المشغّل على آيفون"
              ),
              shot(
                "player-iphone-live",
                "phone",
                "The Live TV guide in the Player on an iPhone",
                "دليل البث المباشر في المشغّل على آيفون"
              ),
              shot(
                "player-iphone-watch",
                "phone",
                "Watching a lesson on an iPhone, with the player's controls shown",
                "مشاهدة درس على آيفون مع ظهور أزرار التحكم"
              ),
              shot(
                "android-player-home-phone",
                "phone",
                "The Player's Home on an Android phone",
                "الرئيسية في المشغّل على هاتف أندرويد"
              ),
              shot(
                "android-player-libraries-library-phone",
                "phone",
                "A library in the Player on an Android phone",
                "مكتبة في المشغّل على هاتف أندرويد"
              ),
              shot(
                "android-player-picker-phone",
                "phone",
                "Choosing a home in the Player on an Android phone",
                "اختيار الجهاز الرئيسي في المشغّل على هاتف أندرويد"
              ),
              shot(
                "android-player-code-phone",
                "phone",
                "Signing in with a code in the Player on an Android phone: the code and its QR code",
                "تسجيل الدخول برمز في المشغّل على هاتف أندرويد: الرمز ورمز QR الخاص به"
              ),
            ],
          },
          {
            shots: [
              shot(
                "player-ipad-home",
                "wide",
                "The Player's Home on an iPad, with its sidebar",
                "الرئيسية في المشغّل على آيباد مع الشريط الجانبي"
              ),
              shot(
                "android-player-layout-book-foldable",
                "wide",
                "The Player on a foldable held like a book: the libraries beside a library's page",
                "المشغّل على جهاز قابل للطي ممسوك كالكتاب: المكتبات بجانب صفحة مكتبة"
              ),
              shot(
                "android-player-home-tablet",
                "wide",
                "The Player's Home on an Android tablet, with the navigation rail",
                "الرئيسية في المشغّل على جهاز أندرويد لوحي مع شريط التنقل الجانبي"
              ),
              shot(
                "player-mac-profile-en",
                "wide",
                "The Mac Player's Profile, in English: history, favorites, playlists, Approve a TV or device, the theme",
                "الملف الشخصي في مشغّل الماك، بالإنجليزية: السجل والمفضلة وقوائم التشغيل والموافقة على تلفاز أو جهاز والمظهر"
              ),
              shot(
                "player-mac-playback-ar",
                "wide",
                "The Mac Player in Arabic, with its Playback menu open",
                "مشغّل الماك بالعربية وقائمة التشغيل مفتوحة"
              ),
            ],
          },
          {
            shots: [
              shot(
                "desktop-player-home",
                "wide",
                "The Player's Home on Windows and Linux: the rail, a new series, Continue Watching and On Deck",
                "الرئيسية في المشغّل على ويندوز ولينكس: الشريط الجانبي وسلسلة جديدة وتابع المشاهدة والدرس التالي"
              ),
              shot(
                "desktop-player-video",
                "wide",
                "A video's page in the Player on Windows and Linux: resume, play from the start, favorite, chapters",
                "صفحة مقطع في المشغّل على ويندوز ولينكس: المتابعة والتشغيل من البداية والمفضلة والفصول"
              ),
              shot(
                "desktop-player-live",
                "wide",
                "Live TV's Now in the Player on Windows and Linux: three channels and what is on",
                "قسم الآن في البث المباشر في المشغّل على ويندوز ولينكس: ثلاث قنوات وما يُعرض عليها"
              ),
            ],
          },
          { p: SHOTS_NOTE },
        ],
      },
    ],
  },

  {
    slug: "remote",
    title: t("Maktaba Remote", "الإدارة عن بُعد"),
    card: t(
      "Run the home from a phone: what needs attention, the queue, libraries, machines and updates.",
      "أدِر الجهاز الرئيسي من هاتفك: ما يحتاج إلى انتباه، والطابور، والمكتبات، والأجهزة، والتحديثات."
    ),
    lead: t(
      "Maktaba Remote runs the home from a phone: what needs attention, the processing queue, the libraries, the machines, the updates and the home's own state.",
      "تطبيق الإدارة عن بُعد، واسمه بالإنجليزية Maktaba Remote، يدير الجهاز الرئيسي من الهاتف: ما يحتاج إلى انتباه، وطابور المعالجة، والمكتبات، والأجهزة، والتحديثات، وحالة الجهاز الرئيسي نفسه."
    ),
    sections: [
      {
        h: t("Who it is for", "لمن هذا التطبيق"),
        blocks: [
          {
            p: t(
              "The home's admins. The home answers the Remote's calls only for an admin (anyone else gets a 403), and a person who is not an admin sees **For the home's admins** after signing in. To watch, use Maktaba Player.",
              "لمدراء الجهاز الرئيسي. فلا يجيب الجهاز الرئيسي طلبات هذا التطبيق إلا لمدير (ويتلقى غيره الرمز 403)، ومن ليس مديرًا يرى بعد تسجيل الدخول عبارة **لمدراء الجهاز الرئيسي**. وللمشاهدة استعمل المشغّل."
            ),
          },
        ],
      },
      {
        h: t("What it does", "ماذا يفعل"),
        blocks: [
          {
            ul: [
              t(
                "**Needs attention**: the failures, grouped by cause, with **Retry** for a group and **Retry all**.",
                "**ما يحتاج إلى انتباه**: الإخفاقات مجمّعة حسب السبب، مع **إعادة المحاولة** للمجموعة و**إعادة محاولة الكل**."
              ),
              t(
                "**Queue**: every processing stage with the jobs waiting and running, the jobs running now and for how long; pause or resume one stage or all of them.",
                "**الطابور**: كل مرحلة معالجة بما ينتظر فيها وما يعمل، والمهام الجارية الآن ومدة كل منها؛ وإيقاف مرحلة واحدة أو كل المراحل مؤقتًا واستئنافها."
              ),
              t(
                "**Libraries**: scan a library for new files.",
                "**المكتبات**: فحص مكتبة بحثًا عن ملفات جديدة."
              ),
              t(
                "**Machines**: let machines ask to join, approve one with the six-digit code it shows, turn one down or revoke one; on Android, drain a machine so it takes no new work.",
                "**الأجهزة**: السماح للأجهزة بطلب الانضمام، والموافقة على جهاز بالرمز المكوّن من ست خانات الذي يعرضه، أو رفضه أو إلغاء اعتماده؛ وعلى أندرويد إنهاء عمل جهاز دون أن يأخذ عملًا جديدًا."
              ),
              t(
                "**Home**: its name, release, queue state and the machines online, with **Updates** (the plan, what holds it, blocked releases and the run under way) and **Processes** (the supervised processes and their recent events).",
                "**الجهاز الرئيسي**: اسمه وإصداره وحالة الطابور والأجهزة المتصلة، ومعها صفحة **التحديثات** (الخطة وما يوقفها والإصدارات المحظورة والتحديث الجاري) وصفحة **العمليات** (العمليات الخاضعة للمراقبة وأحداثها الأخيرة)."
              ),
              t(
                "**Word review**, from **Needs attention**: the words the home doubts in its transcripts, one at a time. Hear the clip, then accept the word, correct it, mark it as a name, add it to the dictionary or skip it, and undo; the swipes follow the language's direction.",
                "**مراجعة الكلمات**، من **ما يحتاج إلى انتباه**: الكلمات التي يشكّ فيها الجهاز الرئيسي في نصوصه المفرَّغة، كلمةً كلمة. استمع إلى المقطع، ثم اقبل الكلمة أو صحّحها أو علّمها اسمًا أو أضفها إلى القاموس أو تخطَّها، ويمكنك التراجع؛ واتجاه السحب يتبع اتجاه اللغة."
              ),
              t(
                "**Diagnostics** (iPhone, on the **Home** tab; Android, in the account menu), through the same routes as the web's admin pages: **Errors** (what keeps failing, grouped, with its last 14 days; a group's chart and recent lines, **Mark seen**, **Resolve** and **Mute**), **Logs** (search by words, level and time; a reference from someone's error screen opens **Follow a request**, every line of that request in time order), **Dashboards** (Overview and Storage, read only) and **Problem reports** (people's reports: the device, what they wrote and one timeline of the app's and the home's lines; set a status and write back).",
                "**التشخيص** (آيفون، في تبويب **الجهاز الرئيسي**؛ وأندرويد، في قائمة الحساب)، عبر مسارات صفحات الإدارة في الويب نفسها: **الأخطاء** (ما يتكرر فشله، مجمّعًا، بأيامه الأربعة عشر الأخيرة؛ ومخطط المجموعة وأسطرها الأخيرة، و**التعليم كمرئي** و**الحل** و**الكتم**)، و**السجلات** (البحث بالكلمات والمستوى والوقت؛ والمرجع من شاشة خطأ لدى شخص ما يفتح **تتبّع طلب**، أي كل أسطر ذلك الطلب بترتيبها الزمني)، و**لوحات المتابعة** (النظرة العامة والتخزين، للقراءة فقط)، و**بلاغات المشكلات** (بلاغات الناس: الجهاز وما كتبوه وخط زمني واحد لأسطر التطبيق والجهاز الرئيسي؛ وتعيين الحالة والرد)."
              ),
              t(
                "**The status strip**: what is down on the home, at the top of every screen.",
                "**شريط الحالة**: ما توقف في الجهاز الرئيسي، أعلى كل شاشة."
              ),
              t(
                "**Approve a TV or device** on every screen, and a banner with **Enter code** while a TV waits to sign in.",
                "زر **الموافقة على تلفاز أو جهاز** في كل شاشة، وشريط فيه **أدخل الرمز** ما دام تلفاز ينتظر تسجيل الدخول."
              ),
            ],
          },
        ],
      },
      {
        h: t("Platforms and how to get it", "المنصات وطريقة الحصول عليه"),
        blocks: [
          {
            table: {
              head: [t("Platform", "المنصة"), t("How to get it", "طريقة الحصول عليه")],
              rows: [
                [t("iPhone", "آيفون"), t("TestFlight", "‏TestFlight")],
                [
                  t(
                    "Android phones, foldables and tablets (Android 8 or later)",
                    "هواتف أندرويد وأجهزته القابلة للطي واللوحية (أندرويد 8 أو أحدث)"
                  ),
                  t("Google Play internal testing", "الاختبار الداخلي في Google Play"),
                ],
              ],
            },
          },
          {
            p: t(
              "The project also has an iPad build of the Remote, with a sidebar, but it is not uploaded to TestFlight yet. There is no Remote for the Mac: on a computer, the web app does all of this and more.",
              "وفي المشروع أيضًا نسخة من هذا التطبيق لآيباد بشريط جانبي، لكنها لم تُرفع إلى TestFlight بعد. ولا توجد نسخة للماك: فعلى الحاسوب يؤدي تطبيق الويب هذا كله وأكثر."
            ),
          },
        ],
      },
      { h: t("Signing in", "تسجيل الدخول"), blocks: SIGN_IN },
      {
        h: t("Screenshots", "لقطات الشاشة"),
        blocks: [
          {
            shots: [
              shot(
                "remote-iphone-queue-en",
                "phone",
                "The Remote's Queue on an iPhone, in English: three jobs running now, then the stages",
                "الطابور في تطبيق الإدارة عن بُعد على آيفون، بالإنجليزية: ثلاث مهام جارية ثم المراحل"
              ),
              shot(
                "remote-iphone-updates",
                "phone",
                "The Remote's Updates page on an iPhone: a TV held on an older release, a blocked release, this home's release",
                "صفحة التحديثات في تطبيق الإدارة عن بُعد على آيفون: تلفاز باقٍ على إصدار أقدم، وإصدار محظور، وإصدار هذا الجهاز الرئيسي"
              ),
              shot(
                "remote-iphone-errors",
                "phone",
                "The Remote's Errors on an iPhone: two error groups with their counts and last 14 days, made-up data",
                "الأخطاء في تطبيق الإدارة عن بُعد على آيفون: مجموعتا أخطاء بأعدادهما وأيامهما الأربعة عشر الأخيرة، على بيانات مختلَقة"
              ),
              shot(
                "remote-iphone-logs-en",
                "phone",
                "The Remote's Logs on an iPhone: three lines of the last 24 hours, two of one request",
                "السجلات في تطبيق الإدارة عن بُعد على آيفون: ثلاثة أسطر من آخر 24 ساعة، اثنان منها لطلب واحد"
              ),
              shot(
                "remote-iphone-dashboards",
                "phone",
                "The Remote's Overview dashboard on an iPhone: the components up and the errors per hour",
                "لوحة النظرة العامة في تطبيق الإدارة عن بُعد على آيفون: المكوّنات العاملة والأخطاء في الساعة"
              ),
              shot(
                "android-remote-errors",
                "phone",
                "The Remote's Errors on an Android phone: two error groups with their last 14 days, made-up data",
                "الأخطاء في تطبيق الإدارة عن بُعد على هاتف أندرويد: مجموعتا أخطاء بأيامهما الأربعة عشر الأخيرة، على بيانات مختلَقة"
              ),
              shot(
                "android-remote-dashboards",
                "phone",
                "The Remote's Overview dashboard on an Android phone: errors per hour and the components up over 24 hours",
                "لوحة النظرة العامة في تطبيق الإدارة عن بُعد على هاتف أندرويد: الأخطاء في الساعة والمكوّنات العاملة خلال 24 ساعة"
              ),
              shot(
                "android-remote-attention",
                "phone",
                "The Remote's Needs attention on an Android phone, with the banner of a TV waiting to sign in",
                "ما يحتاج إلى انتباه في تطبيق الإدارة عن بُعد على هاتف أندرويد، مع شريط تلفاز ينتظر تسجيل الدخول"
              ),
            ],
          },
          { p: SHOTS_NOTE },
        ],
      },
    ],
  },

  {
    slug: "tv",
    title: t("Maktaba TV", "تطبيق التلفاز"),
    card: t(
      "The home's libraries and Live TV on the television, driven with the remote.",
      "مكتبات الجهاز الرئيسي وبثه المباشر على التلفاز، بالتحكم من جهاز التحكم."
    ),
    lead: t(
      "Maktaba TV is the ten-foot app: the home's libraries and its Live TV channels on the television, driven with the remote's arrows and its centre button.",
      "تطبيق التلفاز، واسمه بالإنجليزية Maktaba TV، مصمَّم للمشاهدة من بعيد: مكتبات الجهاز الرئيسي وقنوات بثه المباشر على شاشة التلفاز، بالتحكم من أسهم جهاز التحكم وزره الأوسط."
    ),
    sections: [
      {
        h: t("Who it is for", "لمن هذا التطبيق"),
        blocks: [
          {
            p: t(
              "Everyone in the room. The TV signs in as one person, approved from a phone or a computer, and shows what that person may see.",
              "لكل من في الغرفة. يُسجَّل دخول التلفاز باسم شخص واحد بموافقة من هاتف أو حاسوب، ويعرض ما يحق لذلك الشخص رؤيته."
            ),
          },
        ],
      },
      {
        h: t("What it does", "ماذا يفعل"),
        blocks: [
          {
            ul: [
              t(
                "**Home**: the home's rows, continue watching first.",
                "**الرئيسية**: صفوف الجهاز الرئيسي، وأولها تابع المشاهدة."
              ),
              t(
                "**Libraries**: each library has five tabs, **Recommended**, **Library** (videos or series, sorted and filtered), **Collections**, **Playlists** and **Categories**, and **Play** and **Shuffle** in its header. The tab chosen last is kept for each library. Series, seasons, collections and playlists each have a page that plays them in order or shuffled.",
                "**المكتبات**: لكل مكتبة خمسة تبويبات، **المقترحات** و**المكتبة** (المقاطع أو السلاسل، مرتبةً ومصفّاة) و**المجموعات** و**قوائم التشغيل** و**الفئات**، وفي رأسها زرّا **تشغيل** و**تشغيل عشوائي**. ويُحفظ آخر تبويب اختير لكل مكتبة. ولكل سلسلة وموسم ومجموعة وقائمة تشغيل صفحة تشغّلها بالترتيب أو عشوائيًا."
              ),
              t(
                "**The play queue**: Home starts with **Continue your queue**; while watching, **Previous**, **Next** and **Shuffle again**, and an Up next card that counts down 10 seconds. On Android TV a card's menu has **Play next**, **Add to queue**, **Add to playlist** and **Go to series**.",
                "**قائمة الانتظار**: تبدأ الرئيسية بـ**تابع قائمة الانتظار**؛ وأثناء المشاهدة **السابق** و**التالي** و**إعادة الخلط**، وبطاقة «التالي» بعدّ تنازلي من 10 ثوانٍ. وعلى أندرويد تي في في قائمة البطاقة **شغّل تاليًا** و**أضف إلى قائمة الانتظار** و**أضف إلى قائمة تشغيل** و**اذهب إلى السلسلة**."
              ),
              t("**Search**.", "**البحث**."),
              t(
                "**Live TV**, when the person may watch a channel: **Now**, with every channel and what is on, and the **Guide**; choose a channel to watch it live.",
                "**البث المباشر** حين يُسمح للشخص بمشاهدة قناة: قسم **الآن** بكل قناة وما يُعرض عليها، و**الدليل**؛ اختر قناة لتشاهدها مباشرة."
              ),
              t(
                "**Settings** on Apple TV, **Account** on Android TV: who is signed in, switch home, sign out.",
                "**الإعدادات** على آبل تي في و**الحساب** على أندرويد تي في: من سجّل الدخول، وتبديل الجهاز الرئيسي، وتسجيل الخروج."
              ),
              t(
                "Videos play in the system's player, with scrubbing and the choice of audio and subtitles.",
                "تُشغَّل المقاطع في مشغّل النظام، مع التنقل في المقطع واختيار الصوت والترجمة."
              ),
              t(
                "**Crash reports and Report a problem** (Apple TV and Android TV): the TV asks once after signing in whether to send crash reports to the home, off until you say yes; **Settings** (Account on Android TV) keeps the switch, **Report a problem** (a category and Send are enough with the remote; the log it sends can be read first) and **My reports**.",
                "**تقارير الأعطال والإبلاغ عن مشكلة** (آبل تي في وأندرويد تي في): يسأل التلفاز مرة واحدة بعد تسجيل الدخول هل يرسل تقارير الأعطال إلى الجهاز الرئيسي، وتبقى متوقفة حتى توافق؛ وفي **الإعدادات** (و**الحساب** على أندرويد تي في) مفتاحها، و**الإبلاغ عن مشكلة** (تكفي الفئة وزر الإرسال بجهاز التحكم؛ ويمكن قراءة السجل الذي سيُرسل أولًا)، و**بلاغاتي**."
              ),
            ],
          },
        ],
      },
      {
        h: t("Platforms and how to get it", "المنصات وطريقة الحصول عليه"),
        blocks: [
          {
            table: {
              head: [t("Platform", "المنصة"), t("How to get it", "طريقة الحصول عليه")],
              rows: [
                [
                  t("Apple TV (tvOS 26)", "آبل تي في (tvOS 26)"),
                  t(
                    "TestFlight, installed from the Apple TV's App Store and signed in with the tester's Apple ID",
                    "تطبيق TestFlight، يُثبَّت من متجر آبل تي في ويُسجَّل الدخول فيه بمعرّف آبل الخاص بالمختبِر"
                  ),
                ],
                [
                  t(
                    "Android TV and Google TV (Android 9 or later)",
                    "أندرويد تي في وجوجل تي في (أندرويد 9 أو أحدث)"
                  ),
                  t(
                    `Google Play internal testing, or install \`maktaba-tv-<version>.apk\` by hand when the [latest release](${RELEASES}) carries it (only signed builds are attached)`,
                    `الاختبار الداخلي في Google Play، أو تثبيت \`maktaba-tv-<version>.apk\` يدويًا حين يتضمنه [أحدث إصدار](${RELEASES}) (ولا يُرفق إلا الإصدار الموقَّع)`
                  ),
                ],
              ],
            },
          },
        ],
      },
      {
        h: t("Signing in", "تسجيل الدخول"),
        blocks: [
          {
            p: t(
              "**Choose your home** lists the homes this TV used and the homes on this network, and takes an address or a `maktaba://connect` link. The TV then shows a code in large letters and a QR code: scan it with a phone, or open **Approve a TV or device** in an app that is signed in and type the code. A username and password, typed with the remote, work too.",
              "تعرض شاشة **اختر جهازك الرئيسي** الأجهزة الرئيسية التي استخدمها هذا التلفاز والأجهزة الموجودة على هذه الشبكة، وتقبل عنوانًا أو رابط `maktaba://connect`. ثم يعرض التلفاز رمزًا بحروف كبيرة ورمز QR: امسحه بالهاتف، أو افتح **الموافقة على تلفاز أو جهاز** في تطبيق مسجَّل الدخول واكتب الرمز. ويمكن أيضًا استعمال اسم المستخدم وكلمة المرور بكتابتهما بجهاز التحكم."
            ),
          },
        ],
      },
      {
        h: t("Screenshots", "لقطات الشاشة"),
        blocks: [
          {
            shots: [
              shot(
                "appletv-home",
                "wide",
                "Maktaba TV's Home on an Apple TV: Continue watching",
                "الرئيسية في تطبيق التلفاز على آبل تي في: تابع المشاهدة"
              ),
              shot(
                "appletv-library-tabs-en",
                "wide",
                "A library on an Apple TV, in English: Play and Shuffle, the five tabs, and Recommended's On Deck",
                "مكتبة على آبل تي في، بالإنجليزية: تشغيل وتشغيل عشوائي والتبويبات الخمسة، والدرس التالي في المقترحات"
              ),
              shot(
                "appletv-library-collections-ar",
                "wide",
                "A library's Collections tab on an Apple TV, in Arabic: a YouTube playlist, a Plex collection and one made by hand",
                "تبويب المجموعات في مكتبة على آبل تي في، بالعربية: قائمة تشغيل من يوتيوب، ومجموعة من Plex، ومجموعة أُنشئت يدويًا"
              ),
              shot(
                "appletv-live-guide",
                "wide",
                "The Live TV guide on an Apple TV: three channels and what is on each",
                "دليل البث المباشر على آبل تي في: ثلاث قنوات وما يُعرض على كل منها"
              ),
              shot(
                "androidtv-code",
                "wide",
                "Signing in on an Android TV: the code, its QR code, and a phone that found the TV",
                "تسجيل الدخول على أندرويد تي في: الرمز ورمز QR الخاص به، وهاتف وجد التلفاز"
              ),
            ],
          },
          {
            p: t(
              "The first four screenshots are from the Apple TV app, the sign-in from the Android TV app. The Android TV app has the same sections as the Apple TV app, laid out for the Android TV remote.",
              "أول أربع لقطات من تطبيق آبل تي في، ولقطة تسجيل الدخول من تطبيق أندرويد تي في. ولتطبيق أندرويد تي في أقسام تطبيق آبل تي في نفسها، مرتبةً لجهاز تحكم أندرويد تي في."
            ),
          },
          { p: SHOTS_NOTE },
        ],
      },
    ],
  },

  {
    slug: "server",
    title: t("Maktaba, the server", "مكتبة، الخادم"),
    card: t(
      "The home itself: the server and the web app in one app for Mac, Windows and Linux.",
      "الجهاز الرئيسي نفسه: الخادم وتطبيق الويب في تطبيق واحد للماك وويندوز ولينكس."
    ),
    lead: t(
      "The app called simply Maktaba is the home itself: the server that keeps the library, transcribes the videos, runs the search and the Live TV channels, and streams to every other app. Its window shows the web app.",
      "التطبيق الذي اسمه «مكتبة» فقط هو الجهاز الرئيسي نفسه: الخادم الذي يحفظ المكتبة، ويفرّغ نصوص المقاطع، ويشغّل البحث وقنوات البث المباشر، ويبث إلى سائر التطبيقات. وتعرض نافذته تطبيق الويب."
    ),
    sections: [
      {
        h: t("Who it is for", "لمن هذا التطبيق"),
        blocks: [
          {
            p: t(
              "Whoever runs the home, on a Mac, a Windows PC or a Linux computer. Everyone else uses the other apps, or the web app in a browser.",
              "لمن يشغّل الجهاز الرئيسي على ماك أو حاسوب بنظام ويندوز أو لينكس. أما الباقون فيستعملون التطبيقات الأخرى، أو تطبيق الويب في المتصفح."
            ),
          },
        ],
      },
      {
        h: t("What it does", "ماذا يفعل"),
        blocks: [
          {
            ul: [
              t(
                "Runs the whole server on this computer: the database, the processing pipeline and the streaming. It starts when you sign in, keeps running when the window is closed, and a menu-bar or tray item shows how it is doing.",
                "يشغّل الخادم كله على هذا الحاسوب: قاعدة البيانات وخط المعالجة والبث. ويبدأ حين تسجّل الدخول إلى حاسوبك، ويستمر في العمل بعد إغلاق النافذة، ويعرض عنصر في شريط القوائم أو في منطقة الإشعارات حالته."
              ),
              t(
                "Or connects to another Maktaba: the window then shows a home that runs elsewhere, and nothing runs on this computer.",
                "أو يتصل بـ«مكتبة» أخرى: فتعرض النافذة حينئذ جهازًا رئيسيًا يعمل في مكان آخر، ولا يعمل شيء على هذا الحاسوب."
              ),
              t(
                "First-run setup: the admin, the libraries, the models and the optional links to other services.",
                "إعداد التشغيل الأول: المدير والمكتبات والنماذج والروابط الاختيارية بخدمات أخرى."
              ),
              t(
                "Updates itself through signed updates; **View → Big Screen** turns the window into the big-screen view.",
                "يحدّث نفسه بتحديثات موقَّعة؛ ويحوّل الأمر **عرض ← الشاشة الكبيرة** النافذة إلى عرض الشاشة الكبيرة."
              ),
              t(
                "Keeps every part's logs for as long as the admin chooses, and groups repeated errors, which the web app's **Logs**, **Errors** and **Dashboards** pages show.",
                "يحتفظ بسجلات كل جزء المدة التي يختارها المدير، ويجمع الأخطاء المتكررة، وتعرضها صفحات **السجلات** و**الأخطاء** و**لوحات المتابعة** في تطبيق الويب."
              ),
              t(
                "**Report a problem…** in the tray and the **Help** menu opens the web app's form. If the app itself crashes, the crash is kept on this computer, scrubbed, and the web app in its window sends it to the home only when crash reports are on there (**Settings → Privacy**).",
                "يفتح **الإبلاغ عن مشكلة…** في منطقة الإشعارات وقائمة **المساعدة** نموذجَ تطبيق الويب. وإذا توقف التطبيق نفسه فجأة، يُحفظ العطل على هذا الحاسوب منقّحًا، ولا يرسله تطبيق الويب في نافذته إلى الجهاز الرئيسي إلا حين تكون تقارير الأعطال مشغّلة هناك (**الإعدادات ← الخصوصية**)."
              ),
              t(
                "Can run with no outbound call at all: `maktaba-server serve --offline` (or `MAKTABA_OFFLINE=1`) turns off the update check, pack and model downloads, usage data and the link to Maktaba Cloud at once.",
                "ويمكن تشغيله دون أي اتصال صادر: يوقف الأمر `maktaba-server serve --offline` (أو `MAKTABA_OFFLINE=1`) التحقق من التحديثات وتنزيل الحزم والنماذج وبيانات الاستخدام والربط بسحابة مكتبة دفعةً واحدة."
              ),
            ],
          },
        ],
      },
      {
        h: t("When something breaks", "حين يتعطل شيء"),
        blocks: [
          {
            p: t(
              "The **recovery page**, at `/recovery` on the home's address, is for admins. The supervisor, the program that starts and watches every other part, serves it itself, so it works while the database or the API is down. It shows each part's state and last error, the warnings and errors of the last 15 minutes read from the log files, and the free disk space. From there an admin can restart a part, **start in safe mode** (the database, the API, updates and log search only, for one start), go back to the previous version, restore a backup or download diagnostics. On the Mac, **Open recovery page** is in the Maktaba menu.",
              "**صفحة الاستعادة**، في `/recovery` على عنوان الجهاز الرئيسي، للمدراء. يقدّمها المشرف بنفسه، وهو البرنامج الذي يشغّل كل الأجزاء الأخرى ويراقبها، فتعمل حتى حين تتوقف قاعدة البيانات أو الواجهة البرمجية. وتعرض حالة كل جزء وآخر خطأ فيه، والتحذيرات والأخطاء في آخر 15 دقيقة مقروءةً من ملفات السجل، والمساحة الحرة على القرص. ومنها يستطيع المدير إعادة تشغيل جزء، أو **البدء في الوضع الآمن** (قاعدة البيانات والواجهة البرمجية والتحديثات والبحث في السجلات فقط، لتشغيل واحد)، أو الرجوع إلى الإصدار السابق، أو استعادة نسخة احتياطية، أو تنزيل بيانات التشخيص. وعلى الماك تجد **فتح صفحة الاستعادة** في قائمة مكتبة."
            ),
          },
          {
            p: t(
              "A part that fails three times in a row is stopped until an admin restarts it. When Maktaba itself cannot start, a page offers **Try again**, safe mode and, when one is kept, the previous version; the Maktaba menu on the Mac offers the same.",
              "والجزء الذي يتعطل ثلاث مرات متتالية يُوقَف حتى يعيد المدير تشغيله. وحين لا تستطيع «مكتبة» نفسها أن تبدأ، تعرض صفحةٌ **المحاولة مجددًا** والوضع الآمن، والإصدار السابق إن كان محفوظًا؛ وتعرض قائمة مكتبة على الماك الخيارات نفسها."
            ),
          },
        ],
      },
      {
        h: t("Platforms and how to get it", "المنصات وطريقة الحصول عليه"),
        blocks: [
          {
            table: {
              head: [
                t("Platform", "المنصة"),
                t("Download from the latest release", "التنزيل من أحدث إصدار"),
              ],
              rows: [
                [
                  t(
                    "Mac with Apple silicon (macOS 15 or later)",
                    "ماك بمعالج آبل (macOS 15 أو أحدث)"
                  ),
                  t(
                    "one `.dmg`, with the whole server inside; there is no Intel build",
                    "ملف `.dmg` واحد، ومعه الخادم كاملًا؛ ولا توجد نسخة لمعالجات إنتل"
                  ),
                ],
                [
                  t("Windows", "ويندوز"),
                  t(
                    "`Maktaba_<ver>_x64-setup.exe`, with the server inside, installed for the current user with no administrator",
                    "ملف `Maktaba_<ver>_x64-setup.exe` ومعه الخادم، ويُثبَّت للمستخدم الحالي دون صلاحيات المدير"
                  ),
                ],
                [
                  t("Linux (x86-64 and aarch64)", "لينكس (x86-64 وaarch64)"),
                  t(
                    "the `.AppImage`, `.deb` or `.rpm`; on a computer without a desktop, `install.sh --app` installs the server alone",
                    "ملف `.AppImage` أو `.deb` أو `.rpm`؛ وعلى حاسوب بلا واجهة رسومية يثبّت الأمر `install.sh --app` الخادم وحده"
                  ),
                ],
              ],
            },
          },
          {
            p: t(
              `The project's [README](https://github.com/Hamza-Labs-Core/Maktaba#install-the-home-server) has the requirements and every way to install the home.`,
              `وفي [ملف README](https://github.com/Hamza-Labs-Core/Maktaba#install-the-home-server) للمشروع المتطلبات وكل طرق تثبيت الجهاز الرئيسي.`
            ),
          },
        ],
      },
      {
        h: t("Screenshots", "لقطات الشاشة"),
        blocks: [
          {
            shots: [
              shot(
                "web-home",
                "wide",
                "The web app's Home, which the Maktaba window shows: a new series and Continue Watching",
                "الرئيسية في تطبيق الويب، وهي ما تعرضه نافذة مكتبة: سلسلة جديدة وتابع المشاهدة"
              ),
            ],
          },
          {
            p: t(
              "The window is the web app: see [the web app](/apps/web/) for its pages and the big screen.",
              "النافذة هي تطبيق الويب: انظر صفحة [تطبيق الويب](/apps/web/) لصفحاته وللشاشة الكبيرة."
            ),
          },
          { p: SHOTS_NOTE },
        ],
      },
    ],
  },

  {
    slug: "web",
    title: t("The web app and the big screen", "تطبيق الويب والشاشة الكبيرة"),
    card: t(
      "Maktaba in a browser: watching, and every page that runs the home; the big screen for a TV.",
      "مكتبة في المتصفح: للمشاهدة ولكل صفحات إدارة الجهاز الرئيسي؛ والشاشة الكبيرة للتلفاز."
    ),
    lead: t(
      "The web app is Maktaba in a browser. It is for watching, and for everything that runs the home: the libraries, the processing, the Live TV channels, the users and the settings. Its big-screen view is made for a TV or a monitor across the room.",
      "تطبيق الويب هو «مكتبة» في المتصفح. وهو للمشاهدة، ولكل ما يدير الجهاز الرئيسي: المكتبات والمعالجة وقنوات البث المباشر والمستخدمون والإعدادات. أما عرض الشاشة الكبيرة فيه فمصمَّم لتلفاز أو شاشة في الطرف الآخر من الغرفة."
    ),
    sections: [
      {
        h: t("Who it is for", "لمن هذا التطبيق"),
        blocks: [
          {
            p: t(
              "Everyone with an account on the home. Admins also see the pages that run it; the home checks each person's role on every request.",
              "لكل من له حساب على الجهاز الرئيسي. ويرى المدراء أيضًا صفحات إدارته، ويتحقق الجهاز الرئيسي من دور كل شخص في كل طلب."
            ),
          },
        ],
      },
      {
        h: t("Opening it", "فتحه"),
        blocks: [
          {
            ul: [
              t(
                "On the home's network: the home's address on port `8088`, in any modern browser.",
                "على شبكة الجهاز الرئيسي: عنوان الجهاز الرئيسي على المنفذ `8088`، في أي متصفح حديث."
              ),
              t(
                "From anywhere: through Maktaba Cloud, when the home is linked to it.",
                "من أي مكان: عبر سحابة مكتبة، حين يكون الجهاز الرئيسي مرتبطًا بها."
              ),
              t(
                "It installs as an app from the browser, and the Maktaba app on a Mac, Windows or Linux shows it in its window.",
                "ويمكن تثبيته تطبيقًا من المتصفح، ويعرضه تطبيق مكتبة في نافذته على الماك وويندوز ولينكس."
              ),
              t(
                "The user guide for every app is inside it, at `/help`, in English and Arabic.",
                "ودليل المستخدم لكل التطبيقات داخله، في `/help`، بالعربية والإنجليزية."
              ),
            ],
          },
        ],
      },
      {
        h: t("What it does", "ماذا يفعل"),
        blocks: [
          {
            ul: [
              t(
                "**Home**, **Libraries**, **Series**, **Playlists**, the **TV Guide** and **Search**, which finds what is said in the videos.",
                "**الرئيسية** و**المكتبات** و**السلاسل** و**قوائم التشغيل** و**دليل التلفاز** و**البحث**، الذي يجد ما يُقال داخل المقاطع."
              ),
              t(
                "A video's page: resume, mark watched, favorite, add to a playlist, **Play next** and **Add to queue**, its chapters and its transcript.",
                "صفحة المقطع: المتابعة، ووضع علامة المشاهدة، والإضافة إلى المفضلة أو إلى قائمة تشغيل، و**شغّل تاليًا** و**أضف إلى قائمة الانتظار**، وفصوله، ونصه المفرَّغ."
              ),
              t(
                "A library's **Playlists** tab, **Save as smart playlist** on its filters, **Shuffle** on a series and on each season, and **Continue your queue** on Home. Admins choose how each collection shows: its name, whether it is hidden, and its rows on Recommended and Home.",
                "تبويب **قوائم التشغيل** في المكتبة، و**الحفظ كقائمة تشغيل ذكية** من مرشّحاتها، و**تشغيل عشوائي** في السلسلة وفي كل موسم، و**تابع قائمة الانتظار** في الرئيسية. ويختار المدراء طريقة عرض كل مجموعة: اسمها، وهل هي مخفية، وصفوفها في المقترحات والرئيسية."
              ),
              t(
                "**Account**: devices, **Connect an app** (the link and QR code the apps take), sessions, API tokens, history and preferences; and **Sign in a device**, where a code is approved.",
                "**الحساب**: الأجهزة، و**ربط تطبيق** (الرابط ورمز QR اللذان تقبلهما التطبيقات)، والجلسات، ورموز الواجهة البرمجية، والسجل، والتفضيلات؛ وصفحة **تسجيل دخول جهاز** للموافقة على الرموز."
              ),
              t(
                "For admins: the libraries and their processing, the queue and its failures, the Live TV channels, the machines, the users, the updates and the settings.",
                "للمدراء: المكتبات ومعالجتها، والطابور وإخفاقاته، وقنوات البث المباشر، والأجهزة، والمستخدمون، والتحديثات، والإعدادات."
              ),
              t(
                "**Word review** (`/review`), for admins: the words the home doubts in its transcripts, each with its sentence, its clip, why it was flagged and the spellings suggested. Accept, correct, mark as a name, add to the dictionary or skip, for one place or every place with the same word, and undo. Beside it: **Fixes**, the **Vocabulary**, the **Language packs**, and each library's word review settings.",
                "**مراجعة الكلمات** (`/review`)، للمدراء: الكلمات التي يشكّ فيها الجهاز الرئيسي في نصوصه المفرَّغة، ومع كل منها جملتها ومقطعها الصوتي وسبب تعليمها والتهجئات المقترحة. اقبل أو صحّح أو علّم اسمًا أو أضف إلى القاموس أو تخطَّ، في موضع واحد أو في كل موضع فيه الكلمة نفسها، ويمكنك التراجع. وبجانبها: **التصحيحات** و**المفردات** و**حزم اللغات**، وإعدادات مراجعة الكلمات لكل مكتبة."
              ),
              t(
                "**Logs**, **Errors** and **Dashboards**, for admins. Logs searches every part's lines, follows one request from app to server and back, and follows new lines live. Errors groups repeated failures, with how often and since which version, and tells admins of a new one. **Diagnostics settings** choose how long logs are kept and turn on detailed logs for an hour. Dashboards draw this machine's metrics over a chosen range.",
                "**السجلات** و**الأخطاء** و**لوحات المتابعة**، للمدراء. تبحث صفحة السجلات في أسطر كل جزء، وتتتبّع طلبًا واحدًا من التطبيق إلى الخادم وبالعكس، وتعرض الأسطر الجديدة مباشرةً. وتجمع صفحة الأخطاء الإخفاقات المتكررة، مع عدد مراتها ومنذ أي إصدار، وتنبّه المدراء إلى كل خطأ جديد. وتحدد **إعدادات التشخيص** مدة الاحتفاظ بالسجلات، وتشغّل السجلات المفصّلة لمدة ساعة. وترسم لوحات المتابعة مقاييس هذا الجهاز في مدة تختارها."
              ),
            ],
          },
        ],
      },
      {
        h: t("The big screen", "الشاشة الكبيرة"),
        blocks: [
          {
            p: t(
              "The big screen is a full-screen view for a TV or a big monitor: **Home**, **Libraries**, **Series**, **Search**, **Live TV** and **Settings**, in large type, always in the dark library theme. It shows the same videos, watch progress and permissions as the normal view. On a library, a series and a season, **Play** and **Shuffle** start the play queue. Open it with **Big screen** at the top of any page, or `g` then `b`; in the desktop apps, **View → Big Screen**, and **Start in Big Screen** opens it at launch. In Arabic the screens run right to left and the arrows follow them.",
              "الشاشة الكبيرة عرض بملء الشاشة لتلفاز أو شاشة كبيرة: **الرئيسية** و**المكتبات** و**السلاسل** و**البحث** و**البث المباشر** و**الإعدادات**، بخط كبير وبمظهر المكتبة الداكن دائمًا. وتعرض المقاطع نفسها وتقدّم المشاهدة نفسه والصلاحيات نفسها التي في العرض العادي. وفي المكتبة والسلسلة والموسم يبدأ زرّا **تشغيل** و**تشغيل عشوائي** قائمة الانتظار. افتحها بزر **الشاشة الكبيرة** أعلى أي صفحة، أو بالضغط على `g` ثم `b`؛ وفي تطبيقات سطح المكتب بالأمر **عرض ← الشاشة الكبيرة**، ويفتحها خيار **البدء بالشاشة الكبيرة** عند التشغيل. وبالعربية تسير الشاشات من اليمين إلى اليسار، وتتبعها الأسهم."
            ),
          },
          {
            table: {
              head: [
                t("To", "لـ"),
                t("Keyboard", "لوحة المفاتيح"),
                t("Game controller", "يد التحكم"),
                t("TV remote", "جهاز تحكم التلفاز"),
              ],
              rows: [
                [
                  t("Move", "التنقل"),
                  t("the arrow keys", "مفاتيح الأسهم"),
                  t("the D-pad or the left stick", "أزرار الاتجاهات أو العصا اليسرى"),
                  t("the arrows", "الأسهم"),
                ],
                [
                  t("Choose", "الاختيار"),
                  t("`Enter`", "`Enter`"),
                  t("A or ✕", "‏A أو ✕"),
                  t("OK", "‏OK"),
                ],
                [
                  t("Go back", "الرجوع"),
                  t("`Esc` or `Backspace`", "`Esc` أو `Backspace`"),
                  t("B or ○", "‏B أو ○"),
                  t("Back", "زر الرجوع"),
                ],
                [t("Search", "البحث"), t("`/`", "`/`"), t("Y or △", "‏Y أو △"), t("—", "—")],
              ],
            },
          },
        ],
      },
      {
        h: t("Screenshots", "لقطات الشاشة"),
        blocks: [
          {
            shots: [
              shot(
                "web-home",
                "wide",
                "The web app's Home: the sidebar, a new series and Continue Watching",
                "الرئيسية في تطبيق الويب: الشريط الجانبي وسلسلة جديدة وتابع المشاهدة"
              ),
              shot(
                "web-library",
                "wide",
                "A library's page in the web app, with its tabs and rows",
                "صفحة مكتبة في تطبيق الويب بتبويباتها وصفوفها"
              ),
              shot(
                "web-video",
                "wide",
                "A video's page in the web app: resume, mark watched, favorite, add to a playlist",
                "صفحة مقطع في تطبيق الويب: المتابعة ووضع علامة المشاهدة والمفضلة والإضافة إلى قائمة تشغيل"
              ),
              shot(
                "web-review",
                "wide",
                "Word review in the web app: the flagged words, the sentence, why each was flagged and the spellings suggested",
                "مراجعة الكلمات في تطبيق الويب: الكلمات المعلَّمة والجملة وسبب تعليم كل كلمة والتهجئات المقترحة"
              ),
              shot(
                "web-errors",
                "wide",
                "The Errors page: three error groups, with their counts, their last 14 days and their state",
                "صفحة الأخطاء: ثلاث مجموعات أخطاء مع أعدادها وآخر 14 يومًا منها وحالتها"
              ),
              shot(
                "web-logs",
                "wide",
                "The Logs page: the filters, and the lines of every part with their request ids",
                "صفحة السجلات: المرشّحات، وأسطر كل جزء مع معرّفات طلباتها"
              ),
              shot(
                "big-home",
                "wide",
                "The big screen's Home: the video to resume, then the rows",
                "الرئيسية في الشاشة الكبيرة: المقطع الذي تتابعه ثم الصفوف"
              ),
              shot(
                "big-libraries",
                "wide",
                "The big screen's Libraries, the first library in focus",
                "المكتبات في الشاشة الكبيرة، والمكتبة الأولى محددة"
              ),
            ],
          },
          {
            shots: [
              shot(
                "web-phone",
                "phone",
                "The web app on a phone: Home with the tab bar at the bottom",
                "تطبيق الويب على الهاتف: الرئيسية وشريط التبويب في الأسفل"
              ),
            ],
          },
          { p: SHOTS_NOTE },
        ],
      },
    ],
  },
];

/** Every text node of the Apps pages, with where it is (for the parity check).
 * @param {any[]} [pages] */
export function appTexts(pages = APPS) {
  const out = [];
  const add = (where, x) => out.push({ where, ...x });
  for (const pg of pages) {
    const at = `apps/${pg.slug || "index"}`;
    add(`${at} title`, pg.title);
    add(`${at} card`, pg.card);
    add(`${at} lead`, pg.lead);
    pg.sections.forEach((s, i) => {
      add(`${at} §${i + 1} heading`, s.h);
      for (const b of s.blocks) {
        if (b.p) add(`${at} §${i + 1} paragraph`, b.p);
        for (const li of b.ul ?? []) add(`${at} §${i + 1} item`, li);
        if (b.table) {
          b.table.head.forEach((c) => add(`${at} §${i + 1} table head`, c));
          b.table.rows.flat().forEach((c) => add(`${at} §${i + 1} table cell`, c));
        }
        for (const sh of b.shots ?? []) add(`${at} §${i + 1} alt of ${sh.file}`, sh.alt);
      }
    });
  }
  return out;
}

/** Every screenshot the pages show: [{page, file, kind, alt}].
 * @param {any[]} [pages] */
export function appShots(pages = APPS) {
  return pages.flatMap((pg) =>
    pg.sections.flatMap((s) =>
      s.blocks.flatMap((b) => (b.shots ?? []).map((sh) => ({ page: pg.slug, ...sh })))
    )
  );
}
