# Fire TV (Amazon Appstore)

The Android TV app's `amazon` flavor (story 58.05): the same code, screens and catalogs as the
Google Play build, with its own manifest (`apps/tv/android/app/src/amazon`), no Google Play code
(checked on every CI run) and the platform name `firetv`. It runs on Fire OS 7 (API 28), 8 (API
30), 14 (API 34) and 16 (API 36): Fire TV sticks, the Cube and Fire TV Edition sets. Vega OS
devices do not run Android apps (story 58.18).

What it does not do: nothing is sold in it (no Amazon In-App Purchasing, no purchase links), and
there is no Alexa video skill (a skill needs a catalog feed, and a home's titles stay in the
home). The remote's microphone button reaches the app's Search through the standard `SEARCH`
intent.

## Store listing

The Appstore takes the text per language in the Developer Console (App Submission API keeps
the listing as the console has it; `tools/release/amazon_submit.py` only replaces the APK).

| Field | English | Arabic |
|---|---|---|
| Title | Maktaba | مكتبة |
| Short description | Your family's own video library, on the TV. | مكتبة الفيديو الخاصة بعائلتك، على التلفاز. |
| Category | Video Players & Editors | (the same category) |
| Content rating | General audience; the app shows what the household's own home holds | (the same) |

**Long description (English)**

> Maktaba plays your family's videos from your own Maktaba home: the films, series, lectures
> and recordings you keep, with the places you stopped, on the big screen.
>
> - Find your home on the network, or type its address, and sign in with a code your phone
>   approves.
> - Continue Watching, libraries, collections and search, with the D-pad.
> - Plays 4K, HDR10 and Dolby Vision where your Fire TV and screen do, and Dolby audio to your
>   receiver.
> - English and Arabic, with the whole app mirrored in Arabic.
> - Parental limits set on your home apply here too.
>
> Maktaba needs a Maktaba home (free, self-hosted). Nothing is sold in the app.

**Long description (Arabic)**

> تشغّل مكتبة فيديوهات عائلتك من جهاز مكتبة الرئيسي الخاص بك: الأفلام والمسلسلات والمحاضرات
> والتسجيلات التي تحتفظ بها، مع المواضع التي توقفت عندها، على الشاشة الكبيرة.
>
> - اعثر على جهازك الرئيسي في الشبكة، أو اكتب عنوانه، وسجّل الدخول برمز يوافق عليه هاتفك.
> - تابع المشاهدة، والمكتبات، والمجموعات، والبحث، بأزرار الاتجاهات.
> - يشغّل ‎4K‎ و‎HDR10‎ و‎Dolby Vision‎ حيث يدعمها جهاز Fire TV والشاشة، والصوت المحيطي من Dolby إلى جهاز الاستقبال.
> - بالعربية والإنجليزية، والتطبيق كلّه معكوس الاتجاه بالعربية.
> - حدود الرقابة الأبوية المضبوطة في جهازك الرئيسي تنطبق هنا أيضًا.
>
> يحتاج التطبيق إلى جهاز مكتبة رئيسي (مجاني وتستضيفه بنفسك). لا يُباع شيء داخل التطبيق.

**Screenshots:** `apps/tv/android/app/src/test/screenshots/demo/firetv-{home,library,player,guide}-{en,ar}.png`
(1920×1080, the Fire TV build on the demo library, dark), the same shots CI checks.

**Submission notes for Amazon's reviewer:** the review home of story 58.04 (`review.maktaba.tv`,
a viewer account): the fastest path is **Sign in with a code** and `review.maktaba.tv/approve`,
the fallback the home's address with the username and password. The notes name the release
secret that holds the password (58.04), and the release session fills it in; the password is
never written here. Until 58.04's review home is up, a release is not submitted
for review.

## Build and signing

- A `v*` tag builds `maktaba-tv-<version>-amazon.apk` (`tv-release.yml`, R8 on, the tag's
  versionName and versionCode as the Play bundles have).
- It is signed with the **Amazon upload key**, a separate key from the Play upload key, only when
  the release secrets `AMAZON_KEYSTORE_BASE64`, `AMAZON_KEY_ALIAS` and `AMAZON_KEY_PASSWORD` are
  set. Amazon re-signs the app for its store with the key it keeps for the app. Unsigned, the APK
  stays a workflow artifact (`maktaba-tv-amazon-unsigned`) and goes no further. No key is ever in
  the repository.
- Signed, it is attached to the tag's release and staged in the Appstore's open edit by
  `tools/release/amazon_submit.py stage` when the App Submission API secrets are set:
  `AMAZON_APPSTORE_CLIENT_ID`, `AMAZON_APPSTORE_CLIENT_SECRET` (a security profile with App
  Submission API access) and `AMAZON_APPSTORE_APP_ID`.

To make the upload key once (on the owner's machine, never in the repository):

```bash
keytool -genkeypair -v -keystore maktaba-amazon-upload.jks -alias maktaba-amazon \
  -keyalg RSA -keysize 4096 -validity 10000
base64 -i maktaba-amazon-upload.jks | gh secret set AMAZON_KEYSTORE_BASE64
gh secret set AMAZON_KEY_ALIAS --body maktaba-amazon
gh secret set AMAZON_KEY_PASSWORD
```

## From a tag to the store

1. The tag's run stages the signed APK in the Appstore's open edit (a draft; nothing is sent to
   review). Without the API secrets, the APK is uploaded by hand from the release.
2. **Live App Testing.** In the Developer Console, the same APK goes to Live App Testing for the
   invited testers (Amazon's API cannot make a Live App Testing version). Testers install it from
   the Appstore on their Fire TVs.
3. **The real-device checklist** below runs on the Live App Testing build, and its result line
   goes in `## Results`.
4. **Submit.** `tools/release/amazon_submit.py commit --version <X.Y.Z>` refuses without a passed
   result for that version (`tools/release/tv_checklist_guard.py --platform amazon`); with one,
   it commits the open edit, which sends the version to Amazon's review and then the store. The
   Appstore updates installed apps on its own; the compatibility contract (58.01) covers the days
   between a home's release and the Appstore's.

## Real-device checklist

Run before each release candidate is promoted from Live App Testing to the store (58.05 T06).
The rule and the result line are in [README.md](README.md).

**What CI covers instead:** Robolectric screenshots of the Fire TV build, the instrumented
journey on Google's Android TV emulator (API 30, Play services removed for the run: sign in by
code, a clip played, Back leaves the app), the dependency check (no Google Play code) and the
manifest check. **Only a device shows:** Amazon's codecs, HDR, Dolby, passthrough, Amazon's
remote and launcher behaviour. There is no Fire OS emulator.

### Devices

- Fire TV Stick 4K Max (Fire OS 8)
- one Fire OS 7 device (a Fire TV Stick 3rd generation or Lite)

Install the release candidate from Live App Testing (as a tester would), or `adb install` the
candidate APK over the network (`adb connect <tv-ip>:5555`, ADB debugging on in Developer
options). Capture `adb logcat` for the whole run.

### Checklist

| # | Check | Pass when |
|---|---|---|
| 1 | Find the home on the network (mDNS), then sign in by the home's code, and by a cloud code against the review home | the home is listed, the code screen shows, the phone approves, Home loads |
| 2 | Play a 4K HEVC HDR10 MKV on the 4K Max | plays in HDR at 4K (the TV's info overlay says so), direct play (no transcode on the home) |
| 3 | Play a 1080p H.264 file on the Fire OS 7 device | plays, direct play |
| 4 | Play a Dolby Vision profile 8 file | plays as DV or HDR10, never washed-out SDR |
| 5 | Play a file with EAC3 audio on a receiver or soundbar | passthrough (the receiver shows Dolby Digital Plus) |
| 6 | Switch the app to Arabic | every screen mirrors, text joined, times and episode codes read left to right |
| 7 | Arabic subtitles | joined, right to left, readable on the 10-foot screen |
| 8 | Sleep the device mid-video, wake it | the app resumes at the same place, still signed in |
| 9 | The Amazon remote: Back, Home, play/pause, rewind, fast-forward, menu, the microphone button | each does what its label says; the microphone opens Search; Back from Home leaves the app |
| 10 | Leave and return from Amazon's launcher | the app returns to where it was |
| 11 | The home's log of the playback (Admin, Logs) | the session's profile is `firetv` and its device report names the device's decoders |

If a device fault shows that Amazon's Media3 port (`amzn/media3-external-port`) fixes, the
finding goes in the result line and a follow-up moves the Fire TV build to the port; both
builds stay on stock Media3 until then.

## Results

No result yet (the first release candidate records the first line).
