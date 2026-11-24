# Store gallery: the design (story 60.14, part 1)

The style of every store listing image Maktaba's apps need, as mockups for review before the
full set is made (part 2). The story is
[`specs/epics/60-website/story-60-14-store-gallery.md`](../../../../../specs/epics/60-website/story-60-14-store-gallery.md).
The direction is part B's ([`../README.md`](../README.md#brand-direction)): the reading room at
night, the screen as the photograph. In the gallery, the frame is a plain panel and not a
device.

## What is here

| Path          | What it is                                                                                                                                                                                                       |
| ------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `stores.json` | the size manifest: every store's assets (size, accepted sizes, count, formats, alpha, bytes, required) with the source page on the store's own site, the stores with no listing and why, and each listing's sets |
| `beats.json`  | each app's beats in order, every caption in English and Arabic, the screen each beat captures per surface, and the test-data stand-in the mockups draw until the new demo library lands                          |
| `gallery.css` | one shot's style, sized by `--u`                                                                                                                                                                                 |
| `make.mjs`    | writes `mockups/*.html` (sheets and full-size shots) from the two files                                                                                                                                          |
| `render.mjs`  | renders `png/` and measures every caption (Playwright, a local server on 127.0.0.1, closed at the end; 256-colour PNGs through `../quantize.py`)                                                                 |
| `sheet.css`   | the review sheets' layout (not part of any image)                                                                                                                                                                |
| `tests/`      | T01–T06, run by `make test-tokens`                                                                                                                                                                               |

```
node web/packages/website/design/store-gallery/make.mjs
node web/packages/website/design/store-gallery/render.mjs   # needs web/packages/website's npm install
make test-tokens
```

## The renders

| PNG (`png/`, `.en` and `.ar`)               | Shows                                                                             |
| ------------------------------------------- | --------------------------------------------------------------------------------- |
| `apple-player-iphone`                       | the Player's 7 shots at 1320 × 2868 (the required 1206 × 2622 is the same layout) |
| `apple-player-ipad`                         | the iPad Player's 7 at 2752 × 2064                                                |
| `apple-player-mac`                          | the Mac Player's 6 at 2880 × 1800                                                 |
| `apple-remote-iphone`                       | the Remote's 6                                                                    |
| `apple-tv`                                  | the TV's 7 at 3840 × 2160, captioned                                              |
| `google-player`                             | the feature graphic, the icon, 7 phone shots at 1080 × 1920, 7 at 2560 × 1440     |
| `tv-stores`                                 | Google TV, Fire TV, Roku, Samsung and LG: tiles, art and the plain screens        |
| `full-iphone-library`, `full-iphone-search` | two iPhone shots at full size                                                     |
| `full-feature`                              | the feature graphic at 1024 × 500                                                 |
| `full-appletv-library`                      | the Apple TV library shot, rendered at half its 3840 × 2160                       |

## The captioned shot

Every length is a share of the unit `u`:

- **Portrait:** `u = min(width, height / 2.17)`, so a 9:16 phone gives the caption the same
  share of the height as a 19.5:9 one.
- **Landscape:** `u = height`.

| Part                       | Portrait                                                                                                                                                | Landscape                             |
| -------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------- |
| field                      | `#0E1624` → `#0B111C` → `#070B12` top to bottom, a brass glow (`rgba(227,178,82,.16)`) behind the top of the screen                                     | the same                              |
| caption top                | 0.085 u                                                                                                                                                 | 0.055 u                               |
| headline                   | Maktaba Display 700, 0.068 u / 1.16 (Arabic 0.078 u / 1.4), `#E8ECF3`, centred, balanced, at most 2 lines                                               | 0.066 u / 1.15 (Arabic 0.074 u / 1.4) |
| line under it              | Maktaba Text 400, 0.034 u / 1.45 (Arabic 0.042 u / 1.55), `#A9B3C4`, at most 2 lines                                                                    | 0.032 u / 1.4 (Arabic 0.04 u / 1.5)   |
| measure                    | 84% of the width                                                                                                                                        | 80%                                   |
| wordmark (first shot only) | the icon at 0.075 u, the names in both scripts at 0.042 u with a brass rule, the shot's script first                                                    | the same                              |
| screen                     | 84% of the width, 0.055 u below the caption, corners 6% of its width, a hairline `rgba(233,236,243,.12)` and a soft shadow; it runs off the bottom edge | 78% of the width, corners 1.4%        |

- Arabic is set larger and looser at each step, as on the site: its letters sit smaller on the
  same body.
- The Arabic caption runs right to left. The screen never mirrors.
- The headline stops at 2 lines (a test holds it to 52 characters as written). The caption's
  text stays under 20% of the image: Google Play's limit, measured on every render.

## The other styles

- **Plain** (Google TV, Fire TV, Roku, Samsung, LG): the 1920 × 1080 capture, unaltered.
- **Tile** (TV banner 1280 × 720, Fire TV icon 1280 × 720, Roku poster 540 × 405, Samsung's
  logo layer):
  - the icon at 0.36 u over the name in Maktaba Display at 0.13 u, centred, with
    `u = min(width, 1.6 × height)`;
  - the listing's language; both scripts with the brass rule where one set serves every
    language.
- **Feature** (Google Play 1024 × 500, Amazon's promotional image): the wordmark and the
  tagline at the inline start, the TV's home screen at the inline end, running off the edge.
  The text stays more than 50 px from every edge (Amazon's rule).
- **Art** (Fire TV background, Samsung detail background, LG launcher background): the apps'
  own library hall at 150%, shifted so the lamp sits right of centre. It has no text, no person
  and no device.
- **Icon:** `tools/icons/source/maktaba-icon.png`, resized.

## Captions

| App · beat         | English                                                                                                                | Arabic                                                                                       |
| ------------------ | ---------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------- |
| Player · library   | Your own library, on every screen at home. / Films, series and lessons, kept on your own computer.                     | مكتبتكم، على كل شاشة في البيت. / أفلامكم ومسلسلاتكم ودروسكم، محفوظة على حاسوبكم أنتم.        |
| Player · search    | Search what was said. / Type a word in Arabic or English. Maktaba finds the moment it is said.                         | ابحثوا فيما قيل. / اكتبوا كلمة بالعربية أو بالإنجليزية، فيجد البرنامج اللحظة التي قيلت فيها. |
| Player · subtitles | Subtitles, made at home. / Maktaba listens to each film and writes its subtitles, in Arabic and English.               | ترجماتٌ تُصنع في البيت. / يستمع البرنامج إلى كل فيلم ويكتب ترجمته، بالعربية والإنجليزية.     |
| Player · series    | Every series in order, season by season. / Each episode joins its season by itself.                                    | كل مسلسلٍ في مواسمه، بترتيبه. / وتنضمّ كل حلقةٍ إلى موسمها وحدها.                            |
| Player · live      | Live channels from your own films. / With a guide. Tune in and you join whatever is on.                                | قنواتٌ مباشرة من أفلامكم. / ولها دليلها، فإذا فتحتم قناة انضممتم إلى ما يُعرض فيها الآن.     |
| Player · arabic    | Arabic, right to left, on every screen. / And English too: titles in both sit side by side, and search finds them all. | بالعربية والإنجليزية، في مكتبة واحدة. / العناوين باللغتين جنبًا إلى جنب، ويجدها البحث كلها.  |
| Player · family    | Each child sees what you allow. / Limits you set on your home apply on every screen.                                   | لا يرى الأطفال إلا ما تسمحون به. / الحدود التي تضبطونها في البيت تسري على كل شاشة.           |
| Remote · glance    | Your home, at a glance. / What it is working on, and anything that needs you.                                          | بيتكم، بنظرة واحدة. / ما يعمل عليه الآن، وما يحتاج إليكم.                                    |
| Remote · attention | When something needs you, it says what to do. / One plain message, and the steps that fix it.                          | إن احتاج شيءٌ إليكم، أخبركم بما تفعلون. / رسالةٌ واضحة، وخطوات الإصلاح.                      |
| Remote · queue     | See the work as it happens. / Subtitles, conversions and films being added, in one queue.                              | تابعوا العمل أولًا بأول. / الترجمات والتحويلات والأفلام المضافة، في قائمة واحدة.             |
| Remote · code      | Sign in a TV with a code. / No password typed with the remote.                                                         | سجّلوا دخول التلفاز برمز. / دون كتابة كلمة مرورٍ بجهاز التحكم.                               |
| Remote · updates   | It updates itself, in the quiet hours you choose. / If an update can't start, the previous version comes back.         | يحدّث نفسه في الساعات الهادئة التي تختارونها. / وإن تعذّر تشغيل التحديث، عاد الإصدار السابق. |
| Remote · arabic    | Arabic, right to left, on every screen. / And English too, for everyone who runs the home.                             | بالعربية والإنجليزية. / لكل من يدير البيت، باللغة التي يختارها.                              |
| TV · library       | Your own library, on every screen at home. / Carry on where you stopped, from the sofa.                                | مكتبتكم، على كل شاشة في البيت. / تابعوا من حيث توقفتم، وأنتم على الأريكة.                    |
| TV · series        | Every series in order, season by season. / Up next plays the following episode.                                        | كل مسلسلٍ في مواسمه، بترتيبه. / وتبدأ الحلقة التالية من تلقاء نفسها.                         |
| TV · subtitles     | Subtitles, made at home. / Arabic drawn right to left, as it should be.                                                | ترجماتٌ تُصنع في البيت. / والعربية تُرسم من اليمين إلى اليسار، كما ينبغي.                    |
| TV · live          | Live channels from your own films. / With a guide, like ordinary TV.                                                   | قنواتٌ مباشرة من أفلامكم. / ولها دليلها، كالتلفاز تمامًا.                                    |
| TV · search        | Search what was said. / Find the moment a word is spoken, and play from there.                                         | ابحثوا فيما قيل. / جدوا اللحظة التي قيلت فيها الكلمة، وشاهدوا منها.                          |
| TV · code          | Sign in with a code from your phone. / No password typed with the remote.                                              | سجّلوا الدخول برمزٍ من هواتفكم. / دون كتابة كلمة مرورٍ بجهاز التحكم.                         |
| TV · arabic        | Arabic, right to left, on every screen. / And English too, in one library.                                             | بالعربية والإنجليزية، في مكتبة واحدة. / العناوين باللغتين جنبًا إلى جنب.                     |

`beats.json` is the source; this table is a copy for reading.

## Stand-ins and the no-women check

The mockups draw test-data screens the repository already has:

- maktaba.dev's Apps screenshots (`web/packages/devportal/static/assets/apps/`);
- the Android apps' Roborazzi references outside `demo/`;
- the Android TV app's sign-in reference.

Their posters are blank or geometric and their titles are lessons, history and places. Each
was looked at before use. Three kinds of screen were left out:

- **The old demo library:** maktaba.dev's `firetv-home` and `firetv-library` and every
  Roborazzi `demo/` reference show its posters, Sintel among them. The test refuses those
  paths.
- **Screens that show a woman's name:** the Player's Profile reference ("layla") and the web
  app's search screen (a signed-in "zara").
- **A beat with no stand-in** (parental controls, the Remote's code and Arabic queue) is drawn
  as an empty, labelled panel.

The full set (part 2) captures the new demo library, and its pull request lists every image
with what it shows.

## Part 2: the full set

`compose.mjs` draws every image of every listing from the apps' captures. The approved style
is used as is (make.mjs's shots, `gallery.css`).

```
node web/packages/website/design/store-gallery/compose.mjs --captures DIR --out DIR [--only id,id] [--allow-missing]
gh workflow run store-gallery.yml --ref main -f run_id=<run>     # in CI, as an artifact
```

- **Captures:** PNGs named `<surface>-<screen>-<lang>.png`, the names the shot tests write;
  the Remote's are `<surface>-remote-<screen>-<lang>.png`. The Android and TV references come
  from the checkout (Roborazzi `demo/`), the Apple ones from the run's `product-shots-*`
  artifacts, and any other source from `store-shots-*`.
- **Output** (`store-gallery-<sha>`, 260 images):
  - `apple/<listing>/screenshots/<locale>/` (fastlane `deliver`);
  - `google/<listing>/metadata/android/<locale>/images/` (fastlane `supply`);
  - `<store>/<listing>/<lang or all>/<asset>/` for Amazon, Roku, Samsung and LG;
  - a contact sheet per listing, `report.json`, and `checklist.md` (one line per image, filled
    in by the person who looks at each).
- **Checks:** each image is the store's exact size, in a format the store takes, without alpha
  where refused and under its byte limit (PNG, else the best JPEG that fits: Samsung's 500 KB),
  with no metadata. Each set's count is inside the store's, and each caption within 20%.
- **Tests:** `tests/compose.test.mjs` runs on synthetic captures in CI's `store gallery
(compose)` job, in the Playwright image.

### Captures

Every screen the 13 listings need is captured on the demo library, in English and Arabic (91
screens, 260 images). Each comes from the app's own shot tests:

| Surface                                    | Source                                                                                                                          | Screens                                                                                                      |
| ------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------ |
| iPhone, iPad (Player)                      | `apps/apple/UITests/PlayerDemoShots.swift`, CI's `product-shots-iphone`, `-ipad`                                                | home, film, player, search, series, live, parental                                                           |
| iPhone, iPad (Remote)                      | `RemoteDemoShotsUITests.swift`, `product-shots-iphone-remote`, `-ipad-remote` (iPad in landscape)                               | remote-dashboards, -attention, -queue, -approve-code, -updates                                               |
| Mac                                        | `PlayerMacDemoShotsUITests.swift`, `product-shots-mac` (1024 × 674: the runner's 1× display)                                    | library, player, search, series, live                                                                        |
| Apple TV                                   | `TVDemoShotsUITests.swift`, `product-shots-appletv`                                                                             | home, series, player, guide, search, code                                                                    |
| Android phone, 7" and 10" tablets (Player) | Roborazzi, `apps/android/player/src/test/screenshots/demo/`                                                                     | home, film (phone), player, search, series, live, ask-a-parent                                               |
| Android phone and tablets (Remote)         | Roborazzi, `apps/android/remote/src/test/screenshots/demo/`                                                                     | remote-dashboards, -attention, -queue, -approve-code, -updates                                               |
| Android TV, Fire TV                        | Roborazzi, `apps/tv/android/app/src/test/screenshots/demo/`                                                                     | home, library or series, player, guide, search, code                                                         |
| Roku                                       | brs-engine, `apps/tv/roku/tests/screenshots/demo/` (no video picture: the player is its overlay and the subtitle line on black) | home, series, subtitles, search, code. The channel has no Live TV screen, so Roku's set leaves that beat out |
| Samsung, LG                                | `scripts/capture-tvweb.mjs` (the TV web UI the launchers open, and the launcher's code screen), `store-shots-tvweb`             | home, series, player with subtitles, guide, code                                                             |

A run of ci.yml on a pull request that touches the Apple apps and the website holds every
artifact `store-gallery.yml` needs: the Apple `product-shots-*` and `store-shots-tvweb`.
