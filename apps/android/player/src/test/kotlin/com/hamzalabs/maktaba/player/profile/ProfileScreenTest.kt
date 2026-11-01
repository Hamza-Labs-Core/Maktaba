package com.hamzalabs.maktaba.player.profile

import android.app.Application
import android.content.Intent
import android.provider.Settings
import androidx.activity.ComponentActivity
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.SemanticsNodeInteraction
import androidx.compose.ui.test.assertIsNotSelected
import androidx.compose.ui.test.assertIsOff
import androidx.compose.ui.test.assertIsOn
import androidx.compose.ui.test.assertIsSelected
import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollToNode
import androidx.compose.ui.unit.LayoutDirection
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.hamzalabs.maktaba.core.MapStore
import com.hamzalabs.maktaba.core.MemoryTokens
import com.hamzalabs.maktaba.core.jwt
import com.hamzalabs.maktaba.core.session.AppSession
import com.hamzalabs.maktaba.core.session.Home
import com.hamzalabs.maktaba.core.ui.InkAndBrass
import com.hamzalabs.maktaba.core.ui.MaktabaApp
import com.hamzalabs.maktaba.core.ui.MaktabaTheme
import com.hamzalabs.maktaba.core.ui.SessionGate
import com.hamzalabs.maktaba.core.ui.ThemeMode
import com.hamzalabs.maktaba.core.ui.ThemeStore
import com.hamzalabs.maktaba.player.harness.FakeHome
import com.hamzalabs.maktaba.player.harness.FakeHome.Companion.json
import com.hamzalabs.maktaba.player.harness.Window
import com.hamzalabs.maktaba.player.harness.info
import com.hamzalabs.maktaba.player.shell.AppScreens
import com.hamzalabs.maktaba.player.shell.LocalPlayerHome
import com.hamzalabs.maktaba.player.shell.PlayerHome
import com.hamzalabs.maktaba.player.shell.PlayerSection
import com.hamzalabs.maktaba.player.shell.PlayerShell
import com.hamzalabs.maktaba.player.shell.Route
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.Shadows.shadowOf
import org.robolectric.annotation.Config

/**
 * Profile and Settings (48.31, as 48.22) against the fake home: the theme, the language (per-app
 * locales on Android 13+, the in-app fallback before), history, sessions, sign out, playlists,
 * favorites, help, the playback settings, and the first page's touch targets and labels.
 */
@RunWith(AndroidJUnit4::class)
@Config(qualifiers = "w411dp-h891dp")
class ProfileScreenTest {
    @get:Rule(order = 0) val home = FakeHome()
    @get:Rule(order = 1) val compose = createAndroidComposeRule<ComponentActivity>()

    private val app: Application get() = ApplicationProvider.getApplicationContext()
    private val drawn = Drawn()
    private val playback = PlaybackSettings(MapStore())
    private var perApp = FakeLocales()
    private var languages = AppLanguages(35, perApp, MapStore())

    @Before
    fun routes() = ProfileHome.install(home)

    private fun open(languages: AppLanguages = this.languages, screens: com.hamzalabs.maktaba.player.shell.PlayerScreens = AppScreens) {
        compose.setProfileShell(home, languages, playback, drawn, screens = screens)
        compose.onNodeWithTag("section-Profile").performClick()
        compose.waitForIdle()
    }

    private fun waitFor(text: String) =
        compose.waitUntil(5_000) { compose.onAllNodes(hasText(text)).fetchSemanticsNodes().isNotEmpty() }

    private fun waitForTag(tag: String) =
        compose.waitUntil(5_000) { compose.onAllNodes(hasTestTag(tag)).fetchSemanticsNodes().isNotEmpty() }

    private fun gone(tag: String) =
        compose.waitUntil(5_000) { compose.onAllNodes(hasTestTag(tag)).fetchSemanticsNodes().isEmpty() }

    /** The first page's row [tag], scrolled into view. */
    private fun row(tag: String): SemanticsNodeInteraction {
        compose.onNodeWithTag("profile-root").performScrollToNode(hasTestTag(tag))
        return compose.onNodeWithTag(tag)
    }

    // ---------- theme ----------

    @Test
    fun darkIsSelectedOnAFreshInstallAndLightChangesTheAppAtOnce() {
        open()
        row("theme-Dark").assertIsSelected()
        compose.onNodeWithTag("theme-Light").assertIsNotSelected()
        assertEquals(InkAndBrass.Dark.background, drawn.background)

        compose.onNodeWithTag("theme-Light").performClick()
        compose.waitForIdle()
        compose.onNodeWithTag("theme-Light").assertIsSelected()
        assertEquals(ThemeMode.Light, home.home.themes.mode.value)
        assertEquals(InkAndBrass.Light.background, drawn.background)
    }

    // ---------- language ----------

    @Test
    fun onAndroid13ArabicIsSetAsThePerAppLocale() {
        open()
        row("language-System").assertIsSelected()
        compose.onNodeWithTag("language-Arabic").performClick()
        compose.waitForIdle()
        assertEquals("ar", perApp.tags)
        compose.onNodeWithTag("language-Arabic").assertIsSelected()
        // the system applies it (it recreates the activity): the app does not draw it itself
        assertEquals(AppLanguage.System, languages.applied.value)
    }

    @Test
    fun onAndroid13TheSystemsLanguagePageOpens() {
        open()
        row("profile-language-settings").performClick()
        val started = requireNotNull(shadowOf(app).nextStartedActivity)
        assertEquals(Settings.ACTION_APP_LOCALE_SETTINGS, started.action)
        assertEquals("package:${app.packageName}", started.dataString)
    }

    @Test
    fun beforeAndroid13ArabicIsDrawnInTheAppAndMirrors() {
        val store = MapStore()
        val old = AppLanguages(32, null, store)
        open(old)
        assertEquals(LayoutDirection.Ltr, drawn.direction)
        compose.onNodeWithTag("profile-language-settings").assertDoesNotExist()

        row("language-Arabic").performClick()
        compose.waitForIdle()
        assertEquals("ar", store.get(AppLanguages.STORAGE_KEY))
        waitFor("الملف الشخصي")
        assertEquals(LayoutDirection.Rtl, drawn.direction)
        row("profile-history").assertExists()
        compose.onNodeWithText("سجل المشاهدة").assertExists()

        row("language-English").performClick()
        waitFor("Watch history")
        assertEquals(LayoutDirection.Ltr, drawn.direction)
    }

    // ---------- history, sessions, sign out ----------

    @Test
    fun historyRemovesOneItem() {
        open()
        row("profile-history").performClick()
        waitForTag("history-v2")
        compose.onNodeWithText("Omar, Episode 1").assertExists()
        compose.onNodeWithText("Watched 3 times").assertDoesNotExist() // completed: "Watched"
        compose.onNodeWithTag("history-remove-v2").performClick()
        gone("history-v2")
        compose.onNodeWithTag("history-v1").assertExists()
        compose.onNodeWithTag("history-v4").assertExists()
        compose.waitUntil(5_000) { home.requests.any { it.method == "DELETE" && it.url.encodedPath == "/api/me/history/v2" } }
    }

    @Test
    fun aRefusedRemovalPutsTheRowBack() {
        home.answer("/api/me/history/v2") { json("""{"title":"boom","status":500}""", 500) }
        open()
        row("profile-history").performClick()
        waitForTag("history-v2")
        compose.onNodeWithTag("history-remove-v2").performClick()
        compose.waitUntil(5_000) { home.requests.any { it.method == "DELETE" } }
        waitForTag("history-v2")
    }

    @Test
    fun aHistoryRowOpensItsVideo() {
        open(screens = { route, nav -> if (route is Route.Video) Text("video ${route.id}") else AppScreens(route, nav) })
        row("profile-history").performClick()
        waitForTag("history-v4")
        compose.onNodeWithTag("history-v4").performClick()
        waitFor("video v4")
    }

    @Test
    fun sessionsSignsOutOne() {
        open()
        row("profile-sessions").performClick()
        waitForTag("session-s1")
        compose.onNodeWithText("Safari · Mac").assertExists()
        compose.onNodeWithText("Chrome · Windows").assertExists()
        compose.onNodeWithTag("session-sign-out-s1").performClick()
        gone("session-s1")
        compose.onNodeWithTag("session-s2").assertExists()
        assertTrue(home.requests.any { it.method == "DELETE" && it.url.encodedPath == "/api/me/sessions/s1" })
    }

    @Test
    fun signOutReturnsToTheStartScreen() {
        // a session that is signed in to the fake home, as the app's is
        val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
        try {
            home.answer("/api/auth/me") { json("""{"id":"u1","username":"layla","home_name":"Family home"}""") }
            val tokens = MemoryTokens(jwt(System.currentTimeMillis() / 1000 + 86_400), "refresh")
            val session = AppSession(MapStore(), scope, { tokens })
            session.use(Home(home.home.client.home.toString().trimEnd('/'), "Family home"))
            val themes = ThemeStore(MapStore())
            compose.setContent {
                MaktabaTheme(ThemeMode.Dark) {
                    CompositionLocalProvider(LocalAppLanguages provides languages, LocalPlaybackSettings provides playback) {
                        Surface {
                            SessionGate(session, MaktabaApp.Player) { user, client ->
                                val signedIn = remember(user, client) { PlayerHome(session, user, client, themes) }
                                CompositionLocalProvider(LocalPlayerHome provides signedIn) {
                                    PlayerShell(sections = PlayerSection.enabled, adaptiveInfo = Window.Phone.info())
                                }
                            }
                        }
                    }
                }
            }
            waitForTag("section-Profile")
            compose.onNodeWithTag("section-Profile").performClick()
            waitForTag("profile-header")
            compose.onNodeWithText("Family home").assertExists()
            row("profile-sign-out").performClick()
            compose.onNodeWithTag("sign-out-confirm").performClick()
            waitForTag("signin-password-tab")
            assertTrue(session.phase.value is AppSession.Phase.SigningIn)
            assertEquals(null, tokens.access)
        } finally {
            scope.cancel()
        }
    }

    // ---------- playlists, favorites ----------

    @Test
    fun aPlaylistListsItsVideosAndPlaysFromTheFirst() {
        open(screens = { route, nav -> if (route is Route.Watch) Text("watch ${route.videoId} at ${route.startSec}") else AppScreens(route, nav) })
        row("profile-playlists").performClick()
        waitForTag("playlist-p1")
        compose.onNodeWithText("2 videos · 1:30:00").assertExists()
        compose.onNodeWithTag("playlist-p1").performClick()
        waitForTag("playlist-item-v3")
        compose.onNodeWithTag("playlist-item-v5").assertExists()
        compose.onNodeWithTag("playlist-play").performClick()
        waitFor("watch v3 at 0")
    }

    @Test
    fun aPlaylistsPlayButtonPlaysIt() {
        open(screens = { route, nav -> if (route is Route.Watch) Text("watch ${route.videoId}") else AppScreens(route, nav) })
        row("profile-playlists").performClick()
        waitForTag("playlist-play-p1")
        compose.onNodeWithTag("playlist-play-p1").performClick()
        waitFor("watch v3")
    }

    @Test
    fun favoritesAreThePersonsOwn() {
        open()
        row("profile-favorites").performClick()
        waitForTag("favorite-v1")
        compose.onNodeWithTag("favorite-v3").assertExists()
        val asked = home.requests.last { it.url.encodedPath == "/api/me/videos" }
        assertEquals("true", asked.url.queryParameter("favorite"))
    }

    @Test
    fun backAndReselectReturnToTheFirstPage() {
        open()
        row("profile-sessions").performClick()
        waitForTag("session-s1")
        compose.activityRule.scenario.onActivity { it.onBackPressedDispatcher.onBackPressed() }
        compose.waitForIdle()
        waitForTag("profile-root")
        row("profile-history").performClick()
        waitForTag("history-v2")
        compose.onNodeWithTag("section-Profile").performClick()
        waitForTag("profile-root")
    }

    // ---------- help, about, playback ----------

    @Test
    fun helpOpensTheHomesPageForThisApp() {
        open()
        row("profile-help").performClick()
        val started = requireNotNull(shadowOf(app).nextStartedActivity)
        assertEquals(Intent.ACTION_VIEW, started.action)
        assertEquals(home.home.client.home.resolve("/help/android-apps").toString(), started.dataString)
    }

    @Test
    fun aboutShowsTheHomesVersion() {
        open()
        row("profile-about")
        waitFor("The home: 0.3.52")
    }

    @Test
    fun playbackSettingsStartAtTheirDefaultsAndPersist() {
        open()
        row("profile-playback").performClick()
        compose.onNodeWithTag("playback-autoplay").assertIsOn()
        compose.onNodeWithTag("playback-resume").assertIsOn()
        compose.onNodeWithTag("playback-subtitles").assertIsOff()
        compose.onNodeWithTag("subtitle-size-Medium").assertIsSelected()
        compose.onNodeWithTag("playback-autoplay").performClick()
        compose.onNodeWithTag("subtitle-size-Large").performClick()
        compose.onNodeWithTag("playback-settings").performScrollToNode(hasTestTag("quality-mobile-P480"))
        compose.onNodeWithTag("quality-mobile-P480").performClick()
        compose.waitForIdle()
        val p = playback.value.value
        assertEquals(false, p.autoplayNext)
        assertEquals(SubtitleSize.Large, p.subtitleSize)
        assertEquals(1_500, p.qualityMobile.maxBitrateKbps)
    }

    // ---------- accessibility ----------

    @Test
    fun everyControlOnTheFirstPageIsLabelledAnd48dpTall() {
        open()
        waitForTag("profile-root")
        val minPx = 48 * app.resources.displayMetrics.density - 0.5f
        val problems = mutableListOf<String>()
        // scroll through the page, checking each control as it is laid out
        val tags = listOf("profile-history", "profile-sessions", "theme-Dark", "language-Arabic", "profile-playback", "profile-diagnostics")
        for (tag in tags) {
            row(tag)
            compose.onAllNodes(hasClickAction()).fetchSemanticsNodes().forEach { node ->
                val c = node.config
                val label = c.getOrNull(SemanticsProperties.Text)?.joinToString { it.text }
                    ?: c.getOrNull(SemanticsProperties.ContentDescription)?.joinToString()
                if (label.isNullOrBlank()) problems += "unlabelled control ${c.getOrNull(SemanticsProperties.TestTag)}"
                if (node.size.height < minPx && node.boundsInRoot.height > 0) problems += "${label ?: "?"} is ${node.size.height}px tall"
            }
        }
        assertTrue(problems.distinct().joinToString("\n"), problems.isEmpty())
    }

    @After
    fun noLeak() = Unit
}

/** Android 13's per-app locales, in memory. */
class FakeLocales(var tags: String = "") : PerAppLocales {
    override fun get(): String = tags
    override fun set(tags: String) {
        this.tags = tags
    }
}
