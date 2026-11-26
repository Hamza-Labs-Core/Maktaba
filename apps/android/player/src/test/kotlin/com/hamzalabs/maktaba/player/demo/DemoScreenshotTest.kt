@file:OptIn(UnstableApi::class)

package com.hamzalabs.maktaba.player.demo

import android.content.Context
import android.graphics.BitmapFactory
import android.os.Looper
import androidx.activity.ComponentActivity
import androidx.annotation.OptIn
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.Surface
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.MutableState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.hasSetTextAction
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.v2.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performImeAction
import androidx.compose.ui.test.performScrollToNode
import androidx.compose.ui.test.performTextInput
import androidx.compose.ui.viewinterop.AndroidView
import androidx.media3.common.C
import androidx.media3.common.Format
import androidx.media3.common.MediaItem
import androidx.media3.common.MediaMetadata
import androidx.media3.common.MimeTypes
import androidx.media3.common.Player
import androidx.media3.common.SimpleBasePlayer
import androidx.media3.common.TrackGroup
import androidx.media3.common.Tracks
import androidx.media3.common.text.Cue
import androidx.media3.common.text.CueGroup
import androidx.media3.common.util.UnstableApi
import androidx.media3.ui.PlayerView
import androidx.test.ext.junit.runners.AndroidJUnit4
import coil3.ImageLoader
import coil3.asImage
import coil3.decode.DataSource
import coil3.intercept.Interceptor
import coil3.request.ErrorResult
import coil3.request.ImageResult
import coil3.request.SuccessResult
import com.github.takahirom.roborazzi.RoborazziOptions
import com.github.takahirom.roborazzi.captureRoboImage
import com.google.common.util.concurrent.Futures
import com.google.common.util.concurrent.ListenableFuture
import com.hamzalabs.maktaba.core.DemoLibrary
import com.hamzalabs.maktaba.core.browse.KEY_CONTINUE
import com.hamzalabs.maktaba.core.ui.MaktabaTheme
import com.hamzalabs.maktaba.core.ui.ThemeMode
import com.hamzalabs.maktaba.player.Playback
import com.hamzalabs.maktaba.player.harness.FakeHome
import com.hamzalabs.maktaba.player.harness.adaptiveInfo
import com.hamzalabs.maktaba.player.harness.settled
import com.hamzalabs.maktaba.player.live.FakeLiveEngine
import com.hamzalabs.maktaba.player.live.LiveEnv
import com.hamzalabs.maktaba.player.live.LocalLiveEnv
import com.hamzalabs.maktaba.player.shell.AppScreens
import com.hamzalabs.maktaba.player.shell.LocalPlayerHome
import com.hamzalabs.maktaba.player.shell.PlayerSection
import com.hamzalabs.maktaba.player.shell.PlayerShell
import com.hamzalabs.maktaba.player.shell.Route
import com.hamzalabs.maktaba.player.ui.components.LocalHomeImages
import com.hamzalabs.maktaba.player.ui.components.PictureState
import com.hamzalabs.maktaba.player.video.PosterStateKey
import com.hamzalabs.maktaba.player.video.VideoLink
import com.hamzalabs.maktaba.player.watch.PlayerClock
import com.hamzalabs.maktaba.player.watch.PlayerControls
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.boolean
import kotlinx.serialization.json.int
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import mockwebserver3.MockResponse
import org.junit.After
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RuntimeEnvironment
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import java.io.IOException
import java.util.TimeZone

/**
 * The Player's product screenshots for maktaba.tv and the store galleries (stories 60.09 and
 * 60.14), dark, in English and Arabic, on the demo library (`tests/fixtures/demo-library`):
 * every answer and picture read from its route table, its posters the films' own art, its times
 * from the library's pinned clock (noon UTC). Written to
 * `src/test/screenshots/demo/<surface>-<screen>-<lang>.png`, the names the store gallery's
 * composer reads (`web/packages/website/design/store-gallery/compose.mjs`):
 *
 * - `android-phone` (a 6.3" phone, 1233 × 2673): Home and a film's page (Curiosity), search,
 *   a series (Caminandes), Live TV's On now, the player, and a child's blocked page; and the
 *   player again with the phone turned (`android-phone-player-landscape`, 2673 × 1233), for a
 *   store's landscape canvas;
 * - `android-tablet` (a 10" tablet in landscape, 2560 × 1600) and `android-tablet-7` (a 7"
 *   tablet upright, 1200 × 1920): Home and the same five.
 *
 * Search is the fixture's own (its query, the title and the passages that say it). The blocked
 * page is the child's side of parental controls: a limited sign-in opening a film the home does
 * not show it, with Ask a parent; its limit, its parent (the household's own account) and the
 * film's 404 are this test's, the fixture has none. The player: Media3 cannot decode a stream on
 * the JVM, so the film's backdrop is where the picture would be, at Curiosity's resume point.
 * Over it, on the phone and the 7" tablet, the view Watch draws the video and its subtitles in
 * ([PlayerView]) with the film's Arabic line (the one Apple's demo player shows), its controls
 * hidden as they are while a film plays; on the 10" tablet, the app's own controls
 * ([PlayerControls]) paused, with the subtitles picker open on the film's two tracks (the controls
 * are given a paused player as the playback's own, see [showing]). CI's `verifyRoborazziDebug`
 * fails when one no longer matches.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class DemoScreenshotTest {
    @get:Rule(order = 0) val home = FakeHome { DemoLibrary.load(lang()) }
    @get:Rule(order = 1) val compose = createAndroidComposeRule<ComponentActivity>()

    private val zone: TimeZone = TimeZone.getDefault()
    private val json = Json { ignoreUnknownKeys = true }

    @After
    fun restoreZone() = TimeZone.setDefault(zone)

    private fun lang() = if (RuntimeEnvironment.getQualifiers().split('-').contains("ar")) "ar" else "en"

    private val demo: DemoLibrary get() = requireNotNull(home.demo)

    /** The window the test runs in, from its qualifiers: the phone, the 10" or the 7" tablet. */
    private fun surface(): Form {
        // the full qualifiers ("en-rUS-ldltr-sw411dp-w411dp-h891dp-…-xxhdpi-…"): the window's width and height
        val q = RuntimeEnvironment.getQualifiers().split('-')
        return Form.entries.first { "w${it.widthDp}dp" in q && "h${it.heightDp}dp" in q }
    }

    /** A demo route's JSON in this test's language. */
    private fun read(path: String): JsonObject = json.parseToJsonElement(String(requireNotNull(demo.answer("GET", path)) { path }.body)).jsonObject

    /** The library's Live TV world: its clock, UTC, and a player that plays nothing. */
    private fun liveEnv() = LiveEnv(
        nowMs = { demo.clock.toEpochMilli() },
        zone = { java.time.ZoneOffset.UTC },
        engine = { FakeLiveEngine(fetch = false) },
        device = "demo",
        refreshMs = DAY_MS,
        tickMs = DAY_MS,
    )

    /** The app on the demo home, opened on Home and then on [link]'s screen when there is one. */
    private fun open(link: Route? = null) {
        // times as the library's clock zone shows them: the same on every machine
        TimeZone.setDefault(TimeZone.getTimeZone("UTC"))
        val demo = this.demo
        val activity = compose.activity
        val info = surface().let { adaptiveInfo(it.widthDp, it.heightDp) }
        compose.setContent {
            var pending by remember { mutableStateOf(link) }
            val pictures = remember { demoPictures(activity, demo) }
            MaktabaTheme(ThemeMode.Dark) {
                Surface(Modifier.fillMaxSize()) {
                    CompositionLocalProvider(
                        LocalPlayerHome provides home.home,
                        LocalHomeImages provides pictures,
                        LocalLiveEnv provides remember { liveEnv() },
                    ) {
                        PlayerShell(
                            sections = PlayerSection.enabled,
                            adaptiveInfo = info,
                            screens = { route, nav -> AppScreens(route, nav) },
                            link = pending,
                            onLinkOpened = { pending = null },
                        )
                    }
                }
            }
        }
    }

    private fun count(tag: String) = compose.onAllNodes(hasTestTag(tag), useUnmergedTree = true).fetchSemanticsNodes().size

    private fun shows(text: String) = compose.onAllNodes(hasText(text), useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty()

    /** Every picture on screen has arrived (none still loading), and at least [atLeast] are shown. */
    private fun picturesShown(atLeast: Int): Boolean = count("picture-loading") == 0 && count("picture-shown") >= atLeast

    private fun shoot(screen: String) {
        compose.waitForIdle()
        compose.onRoot().captureRoboImage("src/test/screenshots/demo/${surface().prefix}-$screen-${lang()}.png", roborazziOptions = options)
    }

    private fun homeScreen() {
        open()
        compose.settled(home, "Home's Continue Watching") { count("row-$KEY_CONTINUE") > 0 }
        compose.settled(home, "Home's posters") { picturesShown(atLeast = 3) }
        shoot("home")
    }

    private fun filmScreen() {
        open(VideoLink.route(VideoLink.of(CURIOSITY)))
        val title = read("/api/videos/$CURIOSITY")["title"]!!.jsonPrimitive.content
        compose.settled(home, "Curiosity's page") { shows(title) }
        compose.settled(home, "Curiosity's poster") {
            compose.onAllNodes(hasTestTag("video-poster") and SemanticsMatcher.expectValue(PosterStateKey, PictureState.Shown)).fetchSemanticsNodes().isNotEmpty()
        }
        shoot("film")
    }

    /** Search: the fixture's query typed and sent; its title and the passages that say it. */
    private fun searchScreen() {
        val library = json.parseToJsonElement(java.io.File(demo.dir, "library.json").readText()).jsonObject
        val query = library["search"]!!.jsonObject["query"]!!.jsonObject[lang()]!!.jsonPrimitive.content
        val found = json.parseToJsonElement(demo.text("post-search.json")).jsonObject
        val title = found["videos"]!!.jsonArray.first().jsonObject["video_id"]!!.jsonPrimitive.content
        val inTranscripts = found["transcript_videos"]!!.jsonArray.map { it.jsonObject["video_id"]!!.jsonPrimitive.content }
        open()
        compose.settled(home, "the Search section") { count("section-Search") > 0 }
        compose.onNodeWithTag("section-Search").performClick()
        val field = compose.onNode(hasSetTextAction())
        field.performClick()
        field.performTextInput(query)
        field.performImeAction()
        compose.settled(home, "the title $title and the passages in ${inTranscripts.joinToString()}") {
            count("title-$title") > 0 && inTranscripts.all { count("passage-$it-0") > 0 }
        }
        compose.settled(home, "the results' posters") { picturesShown(atLeast = 1) }
        shoot("search")
    }

    /** Caminandes: the series with its seasons and episodes. */
    private fun seriesScreen() {
        val series = read("/api/series/$CAMINANDES")
        val name = series["name"]!!.jsonPrimitive.content
        val first = read("/api/series/$CAMINANDES/episodes")["seasons"]!!.jsonArray.first().jsonObject["episodes"]!!.jsonArray
            .map { it.jsonObject["video_id"]!!.jsonPrimitive.content }
        open(Route.Series(CAMINANDES))
        compose.settled(home, "the series $name") { count("series-page") > 0 && shows(name) }
        compose.settled(home, "the episodes' list") { count("episodes") > 0 }
        // upright, down to the first season's last episode; a landscape tablet stays at the top,
        // where the picture, Continue, the seasons and the first episode fit
        if (surface() != Form.Tablet) {
            compose.onNodeWithTag("episodes").performScrollToNode(hasTestTag("episode-${first.last()}"))
            compose.settled(home, "its first season's episodes") { first.all { count("episode-$it") > 0 } }
        }
        compose.settled(home, "its pictures") { picturesShown(atLeast = 2) }
        shoot("series")
    }

    /**
     * Live TV's On now: the library's three channels, what plays on each and what comes next, at
     * the library's clock. (The guide is not the shot: the fixture's programmes are a quarter of an
     * hour each, too short for their titles in its blocks.)
     */
    private fun liveScreen() {
        val channels = read("/api/channels")["items"]!!.jsonArray.map { it.jsonObject["id"]!!.jsonPrimitive.content }
        open()
        compose.settled(home, "the Live TV section") { count("section-LiveTv") > 0 }
        compose.onNodeWithTag("section-LiveTv").performClick()
        compose.settled(home, "On now's channels $channels") { channels.all { count("channel-$it") > 0 } }
        shoot("live")
    }

    /**
     * The child's side of parental controls: a sign-in limited to 9+ opens a film the home does
     * not show it (its 404) and sees "This isn't available" with Ask a parent.
     */
    private fun askAParentScreen() {
        home.answer("/api/me/parental") { FakeHome.json("""{"restricted":true,"max_tier":9,"allow_only":false,"version":1}""") }
        home.answer("/api/me/parental/time") { FakeHome.json("""{"allowed":true,"limited":false}""") }
        home.answer("/api/me/parental/requests") { FakeHome.json("""{"requests":[]}""") }
        home.answer("/api/me/parental/guardians") { FakeHome.json("""{"guardians":[{"id":"${demo.userId}","username":"${demo.username}"}]}""") }
        home.answer("/api/videos/$BLOCKED") { MockResponse.Builder().code(404).setHeader("Content-Type", "application/json").body("""{"title":"not found","status":404}""").build() }
        home.answer("/api/me/videos/$BLOCKED") { MockResponse.Builder().code(404).build() }
        runBlocking { home.home.parental.refresh() }
        open(Route.Video(BLOCKED))
        // Enter parent PIN comes once the PIN holders (the guardians) have loaded: on CI's slower
        // runner the shot was once taken before it (#1593, run 45401018003)
        compose.settled(home, "the blocked page with Ask a parent and Enter parent PIN") {
            count("parental-blocked") > 0 && count("parental-ask") > 0 && count("parental-pin") > 0
        }
        shoot("ask-a-parent")
    }

    /**
     * The player on Curiosity at its resume point, over the film's backdrop: its Arabic subtitle
     * line as Watch's own view draws it; on the landscape tablet, paused, the app's controls with
     * the subtitles picker (a side panel there; upright it would cover the film) open on its two
     * tracks (the shot's language chosen).
     */
    private fun playerScreen() {
        TimeZone.setDefault(TimeZone.getTimeZone("UTC"))
        val film = read("/api/videos/$CURIOSITY")
        val mine = read("/api/me/videos/$CURIOSITY")
        val subtitles = read("/api/videos/$CURIOSITY/subtitles")["items"]!!.jsonArray.map { it.jsonObject }
        val backdrop = requireNotNull(demo.answer("GET", "/api/videos/$CURIOSITY/backdrop")).body
        val picture = BitmapFactory.decodeByteArray(backdrop, 0, backdrop.size).asImageBitmap()
        val title = film["title"]!!.jsonPrimitive.content
        val durationMs = film["duration_sec"]!!.jsonPrimitive.int * 1000L
        val positionMs = mine["position_sec"]!!.jsonPrimitive.int * 1000L
        val player = PausedPlayer(
            id = CURIOSITY,
            title = title,
            durationMs = durationMs,
            positionMs = positionMs,
            subtitles = subtitles.map { Triple(it["lang"]!!.jsonPrimitive.content, it["label"]!!.jsonPrimitive.content, it["default"]!!.jsonPrimitive.boolean) },
            line = subtitleLine(),
        )
        val picker = surface() == Form.Tablet
        var view: PlayerView? = null
        val activity = compose.activity
        compose.setContent {
            val scope = rememberCoroutineScope()
            val playback = remember {
                Playback(activity, home.home.client, scope, scope, signedOut = {}, castSender = null, system = false)
                    .also { it.showing(player) }
            }
            val clock = remember { PlayerClock(positionMs, durationMs) }
            // the player is always dark, whatever the theme
            MaktabaTheme(ThemeMode.Dark) {
                Box(Modifier.fillMaxSize().background(Color.Black)) {
                    // where the video would be drawn: the picture fitted, as a 16:9 film plays
                    Image(picture, contentDescription = null, contentScale = ContentScale.Fit, modifier = Modifier.fillMaxSize())
                    if (picker) {
                        PlayerControls(playback, clock, title = title, onClose = {}, hasNext = false, onNext = {})
                    } else {
                        // as Watch sets it up; there is no video on the JVM, so its shutter is clear
                        AndroidView(
                            modifier = Modifier.fillMaxSize(),
                            factory = { ctx ->
                                PlayerView(ctx).apply {
                                    useController = false
                                    setShutterBackgroundColor(android.graphics.Color.TRANSPARENT)
                                    this.player = player
                                    view = this
                                }
                            },
                        )
                    }
                }
            }
        }
        if (!picker) {
            compose.settled(home, "the subtitle's view") { (view?.subtitleView?.width ?: 0) > 0 }
        } else {
            compose.settled(home, "the player's controls") { count("controls") > 0 && count("tracks-button") > 0 }
            compose.onNodeWithTag("tracks-button").performClick()
            compose.settled(home, "the subtitles picker") { count("subtitle-off") > 0 && count("subtitle-1") > 0 }
        }
        // the phone turned: its own name, beside the upright one (a store's landscape canvas)
        shoot(if (surface() == Form.PhoneLandscape) "player-landscape" else "player")
        player.release()
    }

    // ---- the phone (a 6.3" phone: 411 × 891 dp at 3×) ----

    @Test @Config(qualifiers = PHONE)
    fun homeEnglish() = homeScreen()

    @Test @Config(qualifiers = "ar-$PHONE")
    fun homeArabic() = homeScreen()

    @Test @Config(qualifiers = PHONE)
    fun filmEnglish() = filmScreen()

    @Test @Config(qualifiers = "ar-$PHONE")
    fun filmArabic() = filmScreen()

    @Test @Config(qualifiers = PHONE)
    fun searchPhoneEnglish() = searchScreen()

    @Test @Config(qualifiers = "ar-$PHONE")
    fun searchPhoneArabic() = searchScreen()

    @Test @Config(qualifiers = PHONE)
    fun seriesPhoneEnglish() = seriesScreen()

    @Test @Config(qualifiers = "ar-$PHONE")
    fun seriesPhoneArabic() = seriesScreen()

    @Test @Config(qualifiers = PHONE)
    fun livePhoneEnglish() = liveScreen()

    @Test @Config(qualifiers = "ar-$PHONE")
    fun livePhoneArabic() = liveScreen()

    @Test @Config(qualifiers = PHONE)
    fun playerPhoneEnglish() = playerScreen()

    @Test @Config(qualifiers = "ar-$PHONE")
    fun playerPhoneArabic() = playerScreen()

    @Test @Config(qualifiers = PHONE)
    fun askAParentPhoneEnglish() = askAParentScreen()

    @Test @Config(qualifiers = "ar-$PHONE")
    fun askAParentPhoneArabic() = askAParentScreen()

    // ---- the phone turned (891 × 411 dp at 3×: 2673 × 1233), the player only ----

    @Test @Config(qualifiers = PHONE_LANDSCAPE)
    fun playerPhoneLandscapeEnglish() = playerScreen()

    @Test @Config(qualifiers = "ar-$PHONE_LANDSCAPE")
    fun playerPhoneLandscapeArabic() = playerScreen()

    // ---- the 10" tablet in landscape (1280 × 800 dp at 2×: 2560 × 1600) ----

    @Test @Config(qualifiers = TABLET)
    fun homeTabletEnglish() = homeScreen()

    @Test @Config(qualifiers = "ar-$TABLET")
    fun homeTabletArabic() = homeScreen()

    @Test @Config(qualifiers = TABLET)
    fun searchTabletEnglish() = searchScreen()

    @Test @Config(qualifiers = "ar-$TABLET")
    fun searchTabletArabic() = searchScreen()

    @Test @Config(qualifiers = TABLET)
    fun seriesTabletEnglish() = seriesScreen()

    @Test @Config(qualifiers = "ar-$TABLET")
    fun seriesTabletArabic() = seriesScreen()

    @Test @Config(qualifiers = TABLET)
    fun liveTabletEnglish() = liveScreen()

    @Test @Config(qualifiers = "ar-$TABLET")
    fun liveTabletArabic() = liveScreen()

    @Test @Config(qualifiers = TABLET)
    fun playerTabletEnglish() = playerScreen()

    @Test @Config(qualifiers = "ar-$TABLET")
    fun playerTabletArabic() = playerScreen()

    @Test @Config(qualifiers = TABLET)
    fun askAParentTabletEnglish() = askAParentScreen()

    @Test @Config(qualifiers = "ar-$TABLET")
    fun askAParentTabletArabic() = askAParentScreen()

    // ---- the 7" tablet upright (600 × 960 dp at 2×: 1200 × 1920) ----

    @Test @Config(qualifiers = TABLET7)
    fun homeTablet7English() = homeScreen()

    @Test @Config(qualifiers = "ar-$TABLET7")
    fun homeTablet7Arabic() = homeScreen()

    @Test @Config(qualifiers = TABLET7)
    fun searchTablet7English() = searchScreen()

    @Test @Config(qualifiers = "ar-$TABLET7")
    fun searchTablet7Arabic() = searchScreen()

    @Test @Config(qualifiers = TABLET7)
    fun seriesTablet7English() = seriesScreen()

    @Test @Config(qualifiers = "ar-$TABLET7")
    fun seriesTablet7Arabic() = seriesScreen()

    @Test @Config(qualifiers = TABLET7)
    fun liveTablet7English() = liveScreen()

    @Test @Config(qualifiers = "ar-$TABLET7")
    fun liveTablet7Arabic() = liveScreen()

    @Test @Config(qualifiers = TABLET7)
    fun playerTablet7English() = playerScreen()

    @Test @Config(qualifiers = "ar-$TABLET7")
    fun playerTablet7Arabic() = playerScreen()

    @Test @Config(qualifiers = TABLET7)
    fun askAParentTablet7English() = askAParentScreen()

    @Test @Config(qualifiers = "ar-$TABLET7")
    fun askAParentTablet7Arabic() = askAParentScreen()

    /** The windows the shots are taken in, with the name each file starts with. */
    enum class Form(val qualifiers: String, val widthDp: Int, val heightDp: Int, val prefix: String) {
        Phone(PHONE, 411, 891, "android-phone"),
        PhoneLandscape(PHONE_LANDSCAPE, 891, 411, "android-phone"),
        Tablet(TABLET, 1280, 800, "android-tablet"),
        Tablet7(TABLET7, 600, 960, "android-tablet-7"),
    }

    companion object {
        const val PHONE = "w411dp-h891dp-xxhdpi"
        /** The same phone turned, for the player on a store's landscape canvas. */
        const val PHONE_LANDSCAPE = "w891dp-h411dp-land-xxhdpi"
        const val TABLET = "w1280dp-h800dp-xhdpi"
        const val TABLET7 = "w600dp-h960dp-xhdpi"

        private const val CURIOSITY = "v-curiosity"
        private const val CAMINANDES = "s-caminandes"
        /** The film the child's sign-in may not open: Apollo 11's launch, a film for every age (the limit here is made up). */
        private const val BLOCKED = "v-apollo-11-launch"
        private const val DAY_MS = 86_400_000L

        // text is drawn by the host's font rasteriser: a reference recorded on a Mac still matches
        // on CI's Linux within a small share of pixels
        private val options = RoborazziOptions(compareOptions = RoborazziOptions.CompareOptions(changeThreshold = 0.02f))
    }
}

/**
 * The playback shown as playing [player] here: the controls read [Playback.control] and the
 * picker [Playback.tracks], which only an ExoPlayer's own listeners set (and Media3 cannot
 * play a stream on the JVM), so the test sets them, through their state holders, to the paused
 * player and its tracks.
 */
private fun Playback.showing(player: Player) {
    fun <T> set(name: String, value: T) {
        val field = Playback::class.java.getDeclaredField("$name\$delegate").apply { isAccessible = true }
        @Suppress("UNCHECKED_CAST")
        (field.get(this) as MutableState<T>).value = value
    }
    set("control", player)
    set("tracks", player.currentTracks)
}

/**
 * Curiosity's Arabic line, as Apple's demo player shows it (its WebVTT's last line of text). A
 * device gives the line its first letter's direction; the JVM's StaticLayout lays it out left to
 * right, so a right-to-left mark keeps its full stop where a device draws it.
 */
private fun subtitleLine(): String =
    generateSequence(java.io.File("").absoluteFile) { it.parentFile }
        .map { java.io.File(it, "apps/apple/UITests/Fixtures/Stream/demo-subs-ar.vtt") }
        .first { it.exists() }
        .readLines().last { it.isNotBlank() } + "\u200F"

/**
 * A player paused at [positionMs] of a [durationMs] film, with a subtitle track per
 * [subtitles] (language, label, chosen) and [line] on screen: what the controls and the
 * player's view read, with no stream behind it.
 */
private class PausedPlayer(
    id: String,
    title: String,
    durationMs: Long,
    positionMs: Long,
    subtitles: List<Triple<String, String, Boolean>>,
    line: String,
) : SimpleBasePlayer(Looper.getMainLooper()) {
    private val state: State = State.Builder()
        .setAvailableCommands(
            Player.Commands.Builder().addAll(
                Player.COMMAND_PLAY_PAUSE, Player.COMMAND_SEEK_IN_CURRENT_MEDIA_ITEM, Player.COMMAND_SEEK_BACK,
                Player.COMMAND_SEEK_FORWARD, Player.COMMAND_GET_TIMELINE, Player.COMMAND_GET_CURRENT_MEDIA_ITEM,
                Player.COMMAND_GET_METADATA, Player.COMMAND_GET_TRACKS, Player.COMMAND_SET_TRACK_SELECTION_PARAMETERS,
                Player.COMMAND_GET_TEXT,
            ).build(),
        )
        .setCurrentCues(CueGroup(listOf(Cue.Builder().setText(line).build()), positionMs * 1000))
        .setPlaylist(
            listOf(
                MediaItemData.Builder(id)
                    .setMediaItem(MediaItem.Builder().setMediaId(id).build())
                    .setMediaMetadata(MediaMetadata.Builder().setTitle(title).build())
                    .setDurationUs(durationMs * 1000)
                    .setTracks(
                        Tracks(
                            subtitles.map { (lang, label, chosen) ->
                                Tracks.Group(
                                    TrackGroup(Format.Builder().setSampleMimeType(MimeTypes.TEXT_VTT).setLanguage(lang).setLabel(label).build()),
                                    false,
                                    intArrayOf(C.FORMAT_HANDLED),
                                    booleanArrayOf(chosen),
                                )
                            },
                        ),
                    )
                    .build(),
            ),
        )
        .setContentPositionMs(positionMs)
        .setPlaybackState(Player.STATE_READY)
        .setPlayWhenReady(false, Player.PLAY_WHEN_READY_CHANGE_REASON_USER_REQUEST)
        .build()

    override fun getState(): State = state

    override fun handleSetPlayWhenReady(playWhenReady: Boolean): ListenableFuture<*> = Futures.immediateVoidFuture()

    override fun handleRelease(): ListenableFuture<*> = Futures.immediateVoidFuture()
}

/**
 * The demo library's pictures: each URL's path answered from [demo]'s route table (the film's
 * own JPEG), decoded as the app would; a path the table has no picture for is a 404. Nothing is
 * cached, so every shot decodes the same bytes.
 */
fun demoPictures(context: Context, demo: DemoLibrary): ImageLoader = ImageLoader.Builder(context)
    .memoryCache(null)
    .diskCache(null)
    .coroutineContext(Dispatchers.Unconfined)
    .interceptorCoroutineContext(Dispatchers.Unconfined)
    .components {
        add(
            Interceptor { chain ->
                val path = chain.request.data.toString().toHttpUrlOrNull()?.encodedPath
                val answer = path?.let { demo.answer("GET", it) }?.takeIf { it.status == 200 && it.contentType.startsWith("image/") }
                val bitmap = answer?.let { BitmapFactory.decodeByteArray(it.body, 0, it.body.size) }
                val result: ImageResult = if (bitmap != null) {
                    SuccessResult(bitmap.asImage(), chain.request, DataSource.MEMORY)
                } else {
                    ErrorResult(null, chain.request, IOException("404 $path"))
                }
                result
            },
        )
    }
    .build()
