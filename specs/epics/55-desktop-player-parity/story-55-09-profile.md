# Story 55.09 — Profile: history, favorites, playlists, sessions, settings

> Epic 55 · Phase 2 · Depends on: 55.01, 55.08 · Needed by: 55.10

## Why

The desktop Player has an Account menu (sign out, switch home, approve a sign-in) and a Language
menu. The Android and Apple Players have a Profile section: watch history with Forget,
favorites, playlists, sign-in sessions, approving a TV or device, playback settings, language,
and help and about. This story adds the Profile section; the menus stay as shortcuts.

## Description

### 1. `ProfileState` (`desktop/profile/ProfileState.kt`)

As the Android `ProfileState`: `watchHistory(offset, limit)` paged, `forgetHistory(id)`,
`favorites(cursor)` paged, `setFavorite([id], false)` to remove one, `playlists()`,
`playlist(id)`, `signInSessions()`, `endSignInSession(id)`, `homeVersion()`. Each change runs in
its list's `ReadOrder.saving` and shows at once; a failure puts the row back with "That did not
work".

### 2. The section (`desktop/profile/ProfileScreen.kt`)

A list of groups on the start side and the chosen page beside it (one column on a narrow window):

- **Account**: "{0} on {1}" (user and home), Maktaba Cloud link state ("Linked as {0}", or "Not
  linked. Link one on the home's web page."), **Switch home**, **Sign out** (confirm: "Sign out
  of {0}?" and "You can sign in again with your password or a code.").
- **Your library**: **Watch history** (rows with poster, title, when, a Watched mark, Remove;
  a row opens the video page), **Favorites** (cards; Remove), **Playlists** (list; a playlist's
  page with Play, which plays its videos in order with Up Next between them).
- **Sessions**: browsers signed in as this person, each with its browser name, place and last
  seen, and Sign out.
- **Approve a TV or device**: opens the existing `ApproveDialog`.
- **Playback**: subtitles on by default, subtitle language, audio language, quality (Auto or a
  height). Kept in the Player's settings file; 55.08 reads them.
- **Language**: English or العربية, at once (`AppPrefs.chooseLang`), as the menu does.
- **Help and about**: "Maktaba Player {0}", "The home: {0}", Help (the home's
  `/help/desktop-player`), Check for updates (the existing `UpdateDialog`).

### 3. Words

| Key | English | Arabic |
|---|---|---|
| `profile.account` | Account | الحساب |
| `profile.signedIn` | {0} on {1} | {0} على {1} |
| `profile.cloudLinked` | Linked as {0} | مرتبط باسم {0} |
| `profile.cloudNotLinked` | Not linked. Link one on the home's web page. | غير مرتبط. اربط حسابًا من صفحة الجهاز الرئيسي على الويب. |
| `profile.switchHome` | Switch home | تبديل الجهاز الرئيسي |
| `profile.signOut` | Sign out | تسجيل الخروج |
| `profile.signOutTitle` | Sign out of {0}? | تسجيل الخروج من {0}؟ |
| `profile.signOutBody` | You can sign in again with your password or a code. | يمكنك تسجيل الدخول مجددًا بكلمة المرور أو برمز. |
| `profile.library` | Your library | مكتبتك |
| `profile.history` | Watch history | سجل المشاهدة |
| `profile.historyEmpty` | Nothing watched yet | لم تشاهد شيئًا بعد |
| `profile.historyWatched` | Watched | تمت المشاهدة |
| `profile.historyRemove` | Remove {0} from history | إزالة {0} من السجل |
| `profile.favorites` | Favorites | المفضلة |
| `profile.favoritesEmpty` | No favorites yet | لا مفضلات بعد |
| `profile.favoriteRemove` | Remove {0} from favorites | إزالة {0} من المفضلة |
| `profile.playlists` | Playlists | قوائم التشغيل |
| `profile.playlistsEmpty` | No playlists yet. Make one on the home's web page. | لا قوائم تشغيل بعد. أنشئ واحدة من صفحة الجهاز الرئيسي على الويب. |
| `profile.playlistPlay` | Play | تشغيل |
| `profile.sessions` | Sessions | الجلسات |
| `profile.sessionsAbout` | Browsers signed in to the home as you. Sign out any you do not recognise. | المتصفحات المسجَّل دخولها إلى الجهاز الرئيسي باسمك. سجّل خروج أي متصفح لا تعرفه. |
| `profile.sessionsEmpty` | No browser is signed in as you | لا يوجد متصفح مسجَّل دخوله باسمك |
| `profile.sessionSignOut` | Sign out | تسجيل الخروج |
| `profile.approve` | Approve a TV or device | الموافقة على تلفاز أو جهاز |
| `profile.playback` | Playback | التشغيل |
| `profile.subtitlesOn` | Subtitles on by default | تشغيل الترجمة افتراضيًا |
| `profile.subtitleLanguage` | Subtitle language | لغة الترجمة |
| `profile.audioLanguage` | Audio language | لغة الصوت |
| `profile.quality` | Quality | الجودة |
| `profile.language` | Language | اللغة |
| `profile.help` | Help and about | المساعدة وحول التطبيق |
| `profile.version` | Maktaba Player {0} | مشغل مكتبة {0} |
| `profile.homeVersion` | The home: {0} | الجهاز الرئيسي: {0} |
| `profile.unknown` | unknown | غير معروف |
| `profile.loadMore` | Load more | تحميل المزيد |
| `profile.failed` | That did not work | لم ينجح ذلك |

## Acceptance criteria

- **Given** a history, **when** Remove is chosen on a row, **then** it leaves at once, stays gone
  when an older read answers, and Continue Watching on Home no longer shows it.
- **Given** a favorite, **when** it is removed in Profile, **then** the video page shows
  Favorite off.
- **Given** a playlist, **when** Play is chosen, **then** its first video plays and Up Next offers
  the second.
- **Given** two browser sessions, **when** one is signed out, **then** it leaves the list and the
  home ends it.
- **Given** subtitles on by default in Arabic, **when** a video with Arabic subtitles plays,
  **then** they show without a choice.
- **Given** Language → العربية, **when** chosen, **then** the whole window switches to Arabic at
  once, as the menu does, and stays Arabic after a restart.
- **Given** Sign out, **when** confirmed, **then** the Player signs out and shows the home picker.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit | `ProfileState`, MockWebServer | load each page, Load more | pages appended |
| T02 | latch | a history read held, Forget, read answers with the row | release | stays gone |
| T03 | latch | a favorites read held, remove, read answers with it | release | stays gone |
| T04 | latch | a sessions read held, end one, read answers with it | release | stays gone |
| T05 | latch | Forget fails | — | row back, sentence shown |
| T06 | unit | playback settings | save, read in 55.08's rule | defaults applied |
| T07 | screen | Profile in `en`/`ar`, wide and narrow | compose | groups and labels; one column when narrow |

## Edge cases

- **A cloud-linked play sign-in**: Sessions lists only this home's browser sessions; approving
  a device says a cloud sign-in cannot approve (the existing sentence).
- **An old home** without `/api/me/sessions`: the Sessions group is not shown.
- **History of a removed video**: the row shows its title without a poster and does not open.

## Build plan

| File | Change |
|---|---|
| `apps/desktop-player/src/main/kotlin/com/hamzalabs/maktaba/desktop/profile/ProfileState.kt`, `ProfileScreen.kt`, `ProfilePages.kt`, `PlaybackSettingsPage.kt` (new) | The section. |
| `.../desktop/profile/ProfileStrings.kt` | §3 words. |
| `.../desktop/Main.kt` | Menus keep their shortcuts; Language shares `AppPrefs`. |
| `apps/desktop-player/src/test/.../profile/ProfileStateTest.kt`, `ProfileScreenTest.kt` (new) | T01–T07. |
