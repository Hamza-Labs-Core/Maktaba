@file:OptIn(UnstableApi::class)

package com.hamzalabs.maktaba.tv

import android.graphics.BitmapFactory
import android.os.Looper
import androidx.activity.ComponentActivity
import androidx.annotation.OptIn
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.hasSetTextAction
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.v2.createAndroidComposeRule
import androidx.compose.ui.test.onFirst
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performImeAction
import androidx.compose.ui.test.performScrollToNode
import androidx.compose.ui.test.performSemanticsAction
import androidx.compose.ui.test.performTextInput
import androidx.compose.ui.unit.dp
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
import androidx.media3.common.util.UnstableApi
import androidx.media3.ui.PlayerView
import androidx.test.core.app.ApplicationProvider
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Surface
import androidx.tv.material3.SurfaceDefaults
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.github.takahirom.roborazzi.RoborazziOptions
import com.github.takahirom.roborazzi.captureRoboImage
import com.google.common.util.concurrent.Futures
import com.google.common.util.concurrent.ListenableFuture
import com.hamzalabs.maktaba.core.DemoLibrary
import com.hamzalabs.maktaba.core.MemoryTokens
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.DeviceCode
import com.hamzalabs.maktaba.core.api.User
import com.hamzalabs.maktaba.core.jwt
import com.hamzalabs.maktaba.tv.data.TvApi
import com.hamzalabs.maktaba.tv.ui.SignedInApp
import com.hamzalabs.maktaba.tv.ui.components.LibraryBackdrop
import com.hamzalabs.maktaba.tv.ui.screens.CodeSignInContent
import com.hamzalabs.maktaba.tv.ui.screens.Gutter
import com.hamzalabs.maktaba.tv.ui.screens.library.SeriesScreen
import com.hamzalabs.maktaba.tv.ui.theme.MaktabaTvTheme
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.boolean
import kotlinx.serialization.json.int
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import mockwebserver3.Dispatcher
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.RecordedRequest
import okio.Buffer
import org.junit.After
import org.junit.Assume.assumeTrue
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RuntimeEnvironment
import org.robolectric.Shadows.shadowOf
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import java.util.Collections
import java.util.TimeZone

/**
 * Android TV's product screenshots for maktaba.tv and the store galleries (stories 60.09 and
 * 60.14): Home, a library, the player, the live guide, search, a series (Caminandes, the Google
 * Play build only: Fire TV's gallery shows the library instead) and signing in with a code, on
 * a 1080p TV, in English and Arabic, on the demo library
 * (`tests/fixtures/demo-library`): every answer and picture comes from its route table, its
 * posters are the films' own art, its times follow the library's pinned clock. Written to
 * `src/test/screenshots/demo/<build>-<screen>-<lang>.png`, `<build>` being `androidtv` for the
 * Google Play build and `firetv` for the Amazon one (story 58.05 T02: the same screens from the
 * Fire TV build); CI's `verifyRoborazziGoogleDebug` and `verifyRoborazziAmazonDebug` fail when
 * one no longer matches.
 *
 * Search is the fixture's own query, with its title and the passages that say it. The series is
 * its page as the app opens it (over the tabs, with nothing else on screen). The code is the
 * sign-in screen as SignInScreen lays it out, for the demo home, with the code the Remote's demo
 * shots approve and an approve page at a `.local` name (no address of a real network).
 *
 * The player: Media3 cannot decode a stream on the JVM, so the shot is the TV player's own
 * transport ([PlayerView], set up as `PlayerScreen` sets it up) paused on Curiosity at its resume
 * point, with the film's subtitle tracks, over the film's backdrop where the picture would be.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class DemoScreenshotTest {
    @get:Rule val compose = createAndroidComposeRule<ComponentActivity>()

    private lateinit var server: MockWebServer
    private lateinit var demo: DemoLibrary
    private val paths: MutableList<String> = Collections.synchronizedList(mutableListOf())
    private val zone: TimeZone = TimeZone.getDefault()

    private fun lang() = if (RuntimeEnvironment.getQualifiers().split('-').contains("ar")) "ar" else "en"

    @Before
    fun start() {
        // times as the library's clock zone shows them: the same on every machine
        TimeZone.setDefault(TimeZone.getTimeZone("UTC"))
        demo = DemoLibrary.load(lang())
        server = MockWebServer()
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                val path = request.url.encodedPath
                paths += path
                val a = demo.answer(request.method, path) ?: return MockResponse.Builder().code(404).build()
                return MockResponse.Builder().code(a.status).setHeader("Content-Type", a.contentType).body(Buffer().write(a.body)).build()
            }
        }
        server.start()
    }

    @After
    fun stop() {
        server.close()
        TimeZone.setDefault(zone)
    }

    private fun signedIn() {
        val app = ApplicationProvider.getApplicationContext<MaktabaTVApp>()
        val client = ApiClient(server.url("/"), MemoryTokens(jwt(System.currentTimeMillis() / 1000 + 86_400), "refresh"))
        val user = User(id = demo.userId, username = demo.username, isAdmin = demo.isAdmin)
        compose.setContent {
            // as MainActivity lays it out
            MaktabaTvTheme {
                Surface(Modifier.fillMaxSize(), colors = SurfaceDefaults.colors(containerColor = MaterialTheme.colorScheme.background)) {
                    SignedInApp(app, user, client)
                }
            }
        }
    }

    private fun count(tag: String) = compose.onAllNodes(hasTestTag(tag), useUnmergedTree = true).fetchSemanticsNodes().size

    private fun until(what: String, check: () -> Boolean) {
        runCatching { compose.waitUntil(10_000) { compose.waitForIdle(); check() } }
        assertTrue("$what; asked $paths", check())
    }

    private fun shoot(screen: String) {
        compose.waitForIdle()
        compose.onRoot().captureRoboImage("src/test/screenshots/demo/${BuildConfig.TV_PLATFORM}-$screen-${lang()}.png", roborazziOptions = options)
    }

    private fun libraryScreen() {
        val first = Json.parseToJsonElement(String(demo.answer("GET", "/api/libraries")!!.body)).jsonObject["items"]!!.jsonArray.first().jsonObject
        val name = first["name"]!!.jsonPrimitive.content
        signedIn()
        val libraries = compose.activity.getString(R.string.tab_libraries)
        until("the Libraries tab") { compose.onAllNodes(hasText(libraries), useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty() }
        // a TV tab is chosen when it takes focus (the D-pad)
        compose.onNode(hasText(libraries)).performSemanticsAction(SemanticsActions.RequestFocus)
        until("the library $name") { compose.onAllNodes(hasText(name), useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty() }
        compose.onAllNodes(hasText(name) and hasClickAction()).onFirst().performSemanticsAction(SemanticsActions.OnClick)
        // the library opens on its Recommended shelves (its hub)
        val hub = "/api/libraries/${first["id"]!!.jsonPrimitive.content}/hub"
        until("the library's shelves, every poster drawn") { hub in paths && count("poster-shown") >= 4 && count("poster-loading") == 0 }
        shoot("library")
    }

    private fun homeScreen() {
        signedIn()
        until("Home's posters, every one drawn") { count("poster-shown") >= 4 && count("poster-loading") == 0 }
        shoot("home")
    }

    private fun guideScreen() {
        signedIn()
        val live = compose.activity.getString(R.string.tab_live)
        until("the Live TV tab") { compose.onAllNodes(hasText(live), useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty() }
        // a TV tab is chosen when it takes focus (the D-pad)
        compose.onNode(hasText(live)).performSemanticsAction(SemanticsActions.RequestFocus)
        until("the Live TV views") { count("live-view-Guide") > 0 }
        compose.onNodeWithTag("live-view-Guide").performSemanticsAction(SemanticsActions.OnClick)
        until("the guide with its now-line") { count("live-guide") > 0 && count("guide-now-line") > 0 && count("guide-cell") >= 6 }
        shoot("guide")
    }

    private fun playerScreen() {
        val film = Json.parseToJsonElement(String(demo.answer("GET", "/api/videos/$CURIOSITY")!!.body)).jsonObject
        val mine = Json.parseToJsonElement(String(demo.answer("GET", "/api/me/videos/$CURIOSITY")!!.body)).jsonObject
        val subtitles = Json.parseToJsonElement(String(demo.answer("GET", "/api/videos/$CURIOSITY/subtitles")!!.body)).jsonObject["items"]!!.jsonArray.map { it.jsonObject }
        val backdrop = demo.answer("GET", "/api/videos/$CURIOSITY/backdrop")!!.body
        val picture = BitmapFactory.decodeByteArray(backdrop, 0, backdrop.size).asImageBitmap()
        val player = PausedPlayer(
            id = CURIOSITY,
            title = film["title"]!!.jsonPrimitive.content,
            durationMs = film["duration_sec"]!!.jsonPrimitive.int * 1000L,
            positionMs = mine["position_sec"]!!.jsonPrimitive.int * 1000L,
            subtitles = subtitles.map { Triple(it["lang"]!!.jsonPrimitive.content, it["label"]!!.jsonPrimitive.content, it["default"]!!.jsonPrimitive.boolean) },
        )
        var view: PlayerView? = null
        compose.setContent {
            MaktabaTvTheme {
                Box(Modifier.fillMaxSize().background(Color.Black)) {
                    Image(picture, contentDescription = null, contentScale = ContentScale.Crop, modifier = Modifier.fillMaxSize())
                    AndroidView(
                        modifier = Modifier.fillMaxSize(),
                        factory = { ctx ->
                            // as PlayerScreen sets it up, its controls kept on screen for the shot
                            PlayerView(ctx).apply {
                                useController = true
                                setShowSubtitleButton(true)
                                controllerShowTimeoutMs = 0
                                setShutterBackgroundColor(android.graphics.Color.TRANSPARENT)
                                this.player = player
                                showController()
                                view = this
                            }
                        },
                    )
                }
            }
        }
        until("the player's controls") {
            shadowOf(Looper.getMainLooper()).idle()
            view?.isControllerFullyVisible == true
        }
        shoot("player")
        player.release()
    }

    /** Search: the fixture's query typed on the Search tab; its title and the passages that say it. */
    private fun searchScreen() {
        val library = Json.parseToJsonElement(java.io.File(demo.dir, "library.json").readText()).jsonObject
        val query = library["search"]!!.jsonObject["query"]!!.jsonObject[lang()]!!.jsonPrimitive.content
        val found = Json.parseToJsonElement(demo.text("post-search.json")).jsonObject
        val title = found["videos"]!!.jsonArray.first().jsonObject["video_id"]!!.jsonPrimitive.content
        val inTranscripts = found["transcript_videos"]!!.jsonArray.map { it.jsonObject["video_id"]!!.jsonPrimitive.content }
        signedIn()
        val search = compose.activity.getString(R.string.tab_search)
        until("the Search tab") { compose.onAllNodes(hasText(search), useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty() }
        // a TV tab is chosen when it takes focus (the D-pad)
        compose.onNode(hasText(search)).performSemanticsAction(SemanticsActions.RequestFocus)
        until("the search field") { count("tv-search-field") > 0 }
        val field = compose.onNode(hasSetTextAction())
        field.performTextInput(query)
        field.performImeAction()
        until("the title $title") { count("tv-title-$title") > 0 }
        // the passages' row is under the titles: down to it, as the D-pad goes
        compose.onNodeWithTag("tv-search-list").performScrollToNode(hasTestTag("tv-search-transcripts"))
        until("the passages of $inTranscripts, every poster drawn") {
            inTranscripts.all { count("tv-transcript-$it") > 0 } && count("poster-shown") >= inTranscripts.size && count("poster-loading") == 0
        }
        // the D-pad's focus on the first passage, where OK plays from its moment
        compose.onNodeWithTag("tv-transcript-${inTranscripts.first()}").performSemanticsAction(SemanticsActions.RequestFocus)
        until("the passage focused") { count("poster-loading") == 0 }
        shoot("search")
    }

    /** Caminandes: the series' page with its seasons and the episodes of the one to go on with. */
    private fun seriesScreen() {
        // Fire TV's gallery shows the library in this beat (beats.json): no series shot there
        assumeTrue("the series is Google TV's shot", BuildConfig.TV_PLATFORM == "androidtv")
        val client = ApiClient(server.url("/"), MemoryTokens(jwt(System.currentTimeMillis() / 1000 + 86_400), "refresh"))
        val api = TvApi(client)
        compose.setContent {
            // as SignedInApp shows a pushed screen
            MaktabaTvTheme {
                Surface(Modifier.fillMaxSize(), colors = SurfaceDefaults.colors(containerColor = MaterialTheme.colorScheme.background)) {
                    SeriesScreen(api, CAMINANDES)
                }
            }
        }
        until("the seasons and the episodes, every poster drawn") {
            count("season-1") > 0 && count("season-2") > 0 && count("poster-shown") >= 2 && count("poster-loading") == 0
        }
        shoot("series")
    }

    /** Signing in with a code: the TV's code for the demo home, waiting for a phone to approve it. */
    private fun codeScreen() {
        compose.setContent {
            MaktabaTvTheme {
                // as SignInScreen lays it out
                LibraryBackdrop {
                    Column(
                        Modifier.fillMaxSize().padding(horizontal = Gutter, vertical = 24.dp),
                        verticalArrangement = Arrangement.Center,
                    ) {
                        CodeSignInContent(
                            homeName = demo.homeName,
                            code = DeviceCode(code = CODE, deviceCode = "demo", verifyUrl = APPROVE),
                            approveUrl = APPROVE,
                            expiresInSec = 9 * 60 + 58L,
                            lookedUp = false,
                            renewed = false,
                            problem = null,
                            onNewCode = {},
                            onPassword = {},
                            onOtherHome = {},
                        )
                    }
                }
            }
        }
        until("the code") { count("signin-code") > 0 && count("signin-computer") > 0 }
        shoot("code")
    }

    @Test @Config(qualifiers = TV)
    fun homeEnglish() = homeScreen()

    @Test @Config(qualifiers = "ar-$TV")
    fun homeArabic() = homeScreen()

    @Test @Config(qualifiers = TV)
    fun playerEnglish() = playerScreen()

    @Test @Config(qualifiers = "ar-$TV")
    fun playerArabic() = playerScreen()

    @Test @Config(qualifiers = TV)
    fun libraryEnglish() = libraryScreen()

    @Test @Config(qualifiers = "ar-$TV")
    fun libraryArabic() = libraryScreen()

    @Test @Config(qualifiers = TV)
    fun guideEnglish() = guideScreen()

    @Test @Config(qualifiers = "ar-$TV")
    fun guideArabic() = guideScreen()

    @Test @Config(qualifiers = TV)
    fun searchEnglish() = searchScreen()

    @Test @Config(qualifiers = "ar-$TV")
    fun searchArabic() = searchScreen()

    @Test @Config(qualifiers = TV)
    fun seriesEnglish() = seriesScreen()

    @Test @Config(qualifiers = "ar-$TV")
    fun seriesArabic() = seriesScreen()

    @Test @Config(qualifiers = TV)
    fun codeEnglish() = codeScreen()

    @Test @Config(qualifiers = "ar-$TV")
    fun codeArabic() = codeScreen()

    private companion object {
        const val TV = "w960dp-h540dp-land-television-xhdpi"
        const val CURIOSITY = "v-curiosity"
        const val CAMINANDES = "s-caminandes"

        /** The code the TV shows: the one the Remote's demo shots approve. */
        const val CODE = "KQ7M4TXP"

        /** The demo home's approve page, at a `.local` name. */
        const val APPROVE = "http://maktaba.local:8088/pair?code=KQ7M-4TXP"

        // text is drawn by the host's font rasteriser: a reference recorded on a Mac still matches
        // on CI's Linux within a small share of pixels
        val options = RoborazziOptions(compareOptions = RoborazziOptions.CompareOptions(changeThreshold = 0.02f))
    }
}

/**
 * A player paused at [positionMs] of a [durationMs] film, with a subtitle track per
 * [subtitles] (language, label, chosen): what the TV's transport reads, with no stream behind it.
 */
private class PausedPlayer(
    id: String,
    title: String,
    durationMs: Long,
    positionMs: Long,
    subtitles: List<Triple<String, String, Boolean>>,
) : SimpleBasePlayer(Looper.getMainLooper()) {
    private val state: State = State.Builder()
        .setAvailableCommands(
            Player.Commands.Builder().addAll(
                Player.COMMAND_PLAY_PAUSE, Player.COMMAND_SEEK_IN_CURRENT_MEDIA_ITEM, Player.COMMAND_SEEK_BACK,
                Player.COMMAND_SEEK_FORWARD, Player.COMMAND_GET_TIMELINE, Player.COMMAND_GET_CURRENT_MEDIA_ITEM,
                Player.COMMAND_GET_METADATA, Player.COMMAND_GET_TRACKS, Player.COMMAND_SET_TRACK_SELECTION_PARAMETERS,
            ).build(),
        )
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
