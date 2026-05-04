# Implementation Plan — Story 14.4 Voice search integration (Siri, Google Assistant)

> Companion to [story-14-04-voice-search.md](story-14-04-voice-search.md).
> The story states *what* and *why*; this plan states *how*.
> Server-side search is owned by Epic 7 Story 7.8 + Epic 5 Story 5.4
> (FTS); this story owns the **voice → server** dispatch on tvOS and
> AndroidTV.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| tvOS files | `apps/tvos/Sources/Features/Search/{SiriIntent.swift,VoiceSearchView.swift,SpeechSession.swift}` plus an `AppIntents` extension `apps/tvos/Intents/MaktabaSearchIntent.swift`. |
| AndroidTV files | `apps/androidtv/src/main/java/io/maktaba/tv/features/search/{VoiceInputController.kt,AssistantSearchActivity.kt,RecognizerLauncher.kt}` and a new `<intent-filter>` for `actions.intent.SEARCH`. |
| Server contract | Calls already exist: `POST /api/search` (results), `GET /api/search/suggest` (did-you-mean). This story does not add server endpoints. |
| Locale routing | Client passes `Accept-Language`; server uses it to pick FTS index (Epic 5 Story 5.4). For Arabic, server applies `unicode61 remove_diacritics 2` — no client change needed. |
| Out of scope | Server-side search (Epic 7 Story 7.8); FTS index (Epic 5 Story 5.4); cross-language semantic ranking (Epic 5 Story 5.3). |

## 1. tvOS — Siri integration

### 1.1 App Intents (preferred for tvOS 17+)

```swift
// apps/tvos/Intents/MaktabaSearchIntent.swift
import AppIntents

struct MaktabaSearchIntent: AppIntent {
    static var title: LocalizedStringResource = "Search Maktaba"
    static var description = IntentDescription("Search the Maktaba library by voice.")

    @Parameter(title: "Query") var query: String

    @MainActor
    func perform() async throws -> some IntentResult & ProvidesDialog & OpensIntent {
        let route = "maktaba://search?q=\(query.addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed) ?? "")"
        return .result(opensIntent: OpenURLIntent(URL(string: route)!))
    }

    static var parameterSummary: some ParameterSummary {
        Summary("Search Maktaba for \(\.$query)")
    }
}
```

Siri routes "Hey Siri, search Maktaba for tafsīr" to `MaktabaSearchIntent` with `query = "tafsīr"`. The intent opens the deep link, which `AppRouter` (Story 14.1) resolves to `SearchView` with the query pre-filled.

### 1.2 In-app voice (Siri Remote → microphone)

```swift
// apps/tvos/Sources/Features/Search/SpeechSession.swift
import Speech
import AVFoundation

final class SpeechSession: NSObject, ObservableObject {
    @Published var transcript: String = ""
    @Published var isRecording = false
    private let recognizer: SFSpeechRecognizer?
    private var task: SFSpeechRecognitionTask?
    private let audioEngine = AVAudioEngine()
    init(locale: Locale) { recognizer = SFSpeechRecognizer(locale: locale) }

    func start() async throws {
        let auth = await SFSpeechRecognizer.requestAuthorization()
        guard auth == .authorized else { throw SpeechError.permission }
        let request = SFSpeechAudioBufferRecognitionRequest()
        request.shouldReportPartialResults = true
        let input = audioEngine.inputNode
        input.installTap(onBus: 0, bufferSize: 1024, format: input.outputFormat(forBus: 0)) { buf, _ in
            request.append(buf)
        }
        try audioEngine.start()
        isRecording = true
        task = recognizer?.recognitionTask(with: request) { [weak self] result, err in
            if let result { self?.transcript = result.bestTranscription.formattedString }
            if err != nil || result?.isFinal == true { self?.stop() }
        }
    }

    func stop() {
        audioEngine.stop()
        audioEngine.inputNode.removeTap(onBus: 0)
        task?.finish()
        isRecording = false
    }
}
```

`VoiceSearchView` shows the live transcript in the search bar so the user can correct mistranscriptions (the EC: "Background noise causes mistranscription: show the recognized text in the search box").

### 1.3 Permission + privacy strings

`Info.plist`:

```xml
<key>NSSpeechRecognitionUsageDescription</key>
<string>Voice search lets you find lectures, sermons, and books hands-free.</string>
<key>NSMicrophoneUsageDescription</key>
<string>Maktaba uses the Siri Remote microphone for voice search.</string>
```

Without these, `SFSpeechRecognizer` and `AVAudioEngine` raise at runtime; the test plan asserts both are present.

## 2. AndroidTV — Assistant + system voice keyboard

### 2.1 `actions.intent.SEARCH` registration

`AndroidManifest.xml`:

```xml
<activity android:name=".features.search.AssistantSearchActivity"
          android:exported="true"
          android:launchMode="singleTask">
    <intent-filter>
        <action android:name="android.intent.action.SEARCH"/>
        <category android:name="android.intent.category.DEFAULT"/>
        <category android:name="android.intent.category.LEANBACK_LAUNCHER"/>
    </intent-filter>
    <intent-filter>
        <!-- Built-in App Action for Google Assistant -->
        <action android:name="actions.intent.SEARCH"/>
    </intent-filter>
    <meta-data android:name="android.app.searchable" android:resource="@xml/searchable"/>
</activity>
```

`res/xml/searchable.xml`:

```xml
<searchable
    xmlns:android="http://schemas.android.com/apk/res/android"
    android:label="@string/app_name"
    android:hint="@string/search_hint"
    android:voiceSearchMode="showVoiceSearchButton|launchRecognizer"/>
```

### 2.2 `AssistantSearchActivity`

```kotlin
class AssistantSearchActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        val q = intent.getStringExtra(SearchManager.QUERY) ?: ""
        // Forward into the main activity's NavHost as a deep link.
        startActivity(Intent(Intent.ACTION_VIEW, Uri.parse("maktaba://search?q=$q"))
            .setPackage(packageName))
        finish()
    }
}
```

### 2.3 In-app voice (mic button → `RecognizerIntent`)

```kotlin
class RecognizerLauncher(activity: ComponentActivity) {
    private val launcher = activity.registerForActivityResult(StartActivityForResult()) { res ->
        val matches = res.data?.getStringArrayListExtra(RecognizerIntent.EXTRA_RESULTS).orEmpty()
        onResult(matches.firstOrNull().orEmpty())
    }
    fun launch(locale: Locale) {
        val intent = Intent(RecognizerIntent.ACTION_RECOGNIZE_SPEECH).apply {
            putExtra(RecognizerIntent.EXTRA_LANGUAGE_MODEL, RecognizerIntent.LANGUAGE_MODEL_FREE_FORM)
            putExtra(RecognizerIntent.EXTRA_LANGUAGE, locale.toLanguageTag())
            putExtra(RecognizerIntent.EXTRA_PARTIAL_RESULTS, true)
        }
        try { launcher.launch(intent) } catch (e: ActivityNotFoundException) { onUnavailable() }
    }
}
```

### 2.4 Permission

`AndroidManifest.xml` declares `android.permission.RECORD_AUDIO`; `RecognizerIntent` itself prompts the user. The EC ("Mic permission denied: surface the OS-level permission flow") is automatic via the system.

## 3. Search dispatch

The voice transcript is rendered into the same `SearchView` that text input fills. There is **one** code path that calls `POST /api/search`:

```swift
// SearchView.swift (tvOS)
.onChange(of: speech.transcript) { newValue in
    queryText = newValue
    Task { results = try await SearchAPI.run(query: newValue, locale: appLocale) }
}
```

```kotlin
// SearchScreen.kt (AndroidTV)
LaunchedEffect(query) {
    val r = api.search(query, appLocale)
    results = r
    if (r.isEmpty()) suggestions = api.suggest(query, appLocale)
}
```

Empty result handling: a 0-hit response triggers `GET /api/search/suggest`. The "did you mean" chips render below the search bar.

## 4. Test plan

### 4.1 tvOS

| Test | What it pins |
|---|---|
| `testSiriIntentDispatchesDeepLink` | Invoke `MaktabaSearchIntent(query: "gratitude")`; assert `OpenURLIntent` URL is `maktaba://search?q=gratitude`. |
| `testSpeechSessionUpdatesTranscript` | Inject a fake `SFSpeechRecognitionResult`; `transcript` updates. |
| `testInfoPlistHasMicAndSpeechStrings` | Build-time check — fails if either string is missing. |
| `testEmptyResultsTriggersSuggest` | Stub `SearchAPI` to return 0 hits; `suggest` is called once with the same query. |
| `testArabicQueryDoesNotMangleDiacritics` | Query "تَفسير" preserves diacritics in the request body (server normalizes). |

### 4.2 AndroidTV

| Test | What it pins |
|---|---|
| `assistantSearchActivityForwardsQuery` | Launch with `SearchManager.QUERY = "ramadan"`; main activity opens the search route with q=ramadan. |
| `recognizerLauncherSurfacesText` | `ActivityResult` with EXTRA_RESULTS = ["test"] → `onResult("test")` invoked. |
| `noVoiceProviderFallsBackSilently` | `ActivityNotFoundException` → text search still works; one-time analytics event. |
| `voiceQueryFiresSearchAndSuggestOnEmpty` | Search returns 0 → suggestions endpoint is called within 200 ms. |

## 5. Edge cases — handling table

| Case | Behaviour | Where pinned |
|---|---|---|
| Mic permission denied | tvOS: surface `SFSpeechRecognizerAuthorizationStatus.denied` UI with "Open Settings" deep link. AndroidTV: `RecognizerIntent` shows OS dialog; on denial we fall back to keyboard. | `testMicPermissionDenied` |
| Background noise / mistranscription | Live transcript visible in the search box; user can edit before the search debounce fires (300 ms). | `testTranscriptEditable` |
| Voice provider returns nothing | tvOS: keep the search box empty; AndroidTV: catch `ActivityNotFoundException` and fall back. | (above) |
| Arabic voice with English UI locale | Speech locale follows the user's preferred input language (Settings); FTS picks Arabic. UI labels stay English. | `testCrossLocaleVoice` |
| Cellular / weak network on the cellular path | Same code path; latency budget allows up to 2 s before a "still working" indicator. | `testSlowNetworkSpinner` |
| User starts typing while voice is recording | Mic stops on first keystroke; transcript-so-far is preserved as the typed query. | `testTypingStopsMic` |
| Query consists only of stopwords | Server returns 0 hits; "did you mean" empty too → empty state with "Try another phrase". | `testStopwordsEmpty` |
| Long query (> 256 chars) | Truncated client-side at 256; toast "Query truncated." | `testLongQueryTruncated` |

## 6. Dependencies

| Dep | Version | Why |
|---|---|---|
| `Speech.framework` | system | tvOS speech recognition. |
| `AppIntents.framework` | system, tvOS 17+ | Siri intent registration. |
| `androidx.activity:activity-compose` | 1.9.0 | `registerForActivityResult` for the recognizer. |

## 7. Acceptance checklist

**tvOS**
- [ ] `MaktabaSearchIntent` dispatches deep links to `SearchView`.
- [ ] In-app voice records, transcribes, and routes to the search API.
- [ ] `Info.plist` contains the privacy usage strings.

**AndroidTV**
- [ ] `actions.intent.SEARCH` and `<searchable>` declared.
- [ ] `RecognizerIntent` voice button renders in the search bar.

**Behaviour**
- [ ] Empty results surface "did you mean" via `/api/search/suggest`.
- [ ] Arabic and English voice both reach their indices.

**Tests**
- [ ] All §4 tests pass.

**Docs**
- [ ] `specs/epics/14-tv-apps/README.md` ticks story 14.4.
