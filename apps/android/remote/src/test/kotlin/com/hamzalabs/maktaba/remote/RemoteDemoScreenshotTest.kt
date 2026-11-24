package com.hamzalabs.maktaba.remote

import androidx.activity.ComponentActivity
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.Surface
import androidx.compose.ui.Modifier
import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.v2.createAndroidComposeRule
import androidx.compose.ui.test.onFirst
import androidx.compose.ui.test.onLast
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTextInput
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.github.takahirom.roborazzi.RoborazziOptions
import com.github.takahirom.roborazzi.captureRoboImage
import com.hamzalabs.maktaba.core.DemoLibrary
import com.hamzalabs.maktaba.core.ui.ApproveSignIn
import com.hamzalabs.maktaba.core.ui.MaktabaTheme
import com.hamzalabs.maktaba.core.ui.ThemeMode
import org.junit.After
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RuntimeEnvironment
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import java.util.TimeZone

/**
 * The Remote's product screenshots for the store galleries (story 60.14), dark, in English and
 * Arabic, on the demo library's home ([DemoRemoteHome]: the fixture's account, films and clock,
 * with a made-up night's work): the Overview dashboard, Needs attention, the queue, approving a
 * TV's sign-in code, and updates, each on a phone, a 10" tablet in landscape and a 7" tablet
 * upright. On a tablet the screens with a list open a row beside it (the cause Retry cannot fix,
 * a stage, the last update). Written to `src/test/screenshots/demo/<surface>-remote-<screen>-<lang>.png`,
 * the names the store gallery's composer reads; CI's `verifyRoborazziDebug` fails when one no
 * longer matches.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class RemoteDemoScreenshotTest {
    @get:Rule(order = 0) val home = FakeRemoteHome(DemoRemoteHome.user())
    @get:Rule(order = 1) val compose = createAndroidComposeRule<ComponentActivity>()

    private val zone: TimeZone = TimeZone.getDefault()
    private lateinit var demo: DemoLibrary

    private fun lang() = if (RuntimeEnvironment.getQualifiers().split('-').contains("ar")) "ar" else "en"

    /** The window the test runs in, from its qualifiers ("…-w411dp-h891dp-…"). */
    private fun form(): Form {
        val q = RuntimeEnvironment.getQualifiers().split('-')
        return Form.entries.first { "w${it.widthDp}dp" in q && "h${it.heightDp}dp" in q }
    }

    @Before
    fun start() {
        // times as the library's clock zone shows them: the same on every machine
        TimeZone.setDefault(TimeZone.getTimeZone("UTC"))
        demo = DemoLibrary.load(lang())
        DemoRemoteHome.install(home, demo)
        compose.registerIdlingResource(home.idling)
    }

    @After
    fun stop() = TimeZone.setDefault(zone)

    private fun until(what: String, check: () -> Boolean) {
        runCatching { compose.settledUntil(30_000, check) }
        assertTrue("$what; asked ${home.paths}", check())
    }

    private fun shows(text: String) = compose.onAllNodes(hasText(text, substring = true), useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty()

    /** The Remote's frame on the place [place] (or [screen] in it), as MainActivity shows it. */
    private fun frame(place: Place? = null, screen: (@androidx.compose.runtime.Composable () -> Unit)? = null) {
        val f = form()
        compose.setContent {
            MaktabaTheme(ThemeMode.Dark) {
                Surface {
                    val account: @androidx.compose.runtime.Composable () -> Unit = {
                        RemoteAccount(home.session, home.user, home.themes, home.remote.client, home.remote)
                    }
                    val info = adaptiveInfo(f.widthDp, f.heightDp)
                    if (screen != null) {
                        RemoteFrame(home.remote, account = account, adaptiveInfo = info, screens = { screen() })
                    } else {
                        RemoteFrame(home.remote, account = account, adaptiveInfo = info)
                    }
                }
            }
        }
        if (place != null) {
            until("the ${place.name} place") { compose.has("place-${place.name}") }
            compose.onNodeWithTag("place-${place.name}").performClick()
        }
    }

    private fun shoot(screen: String) {
        compose.waitForIdle()
        compose.onRoot().captureRoboImage("src/test/screenshots/demo/${form().prefix}-remote-$screen-${lang()}.png", roborazziOptions = options)
    }

    private fun tablet() = form() != Form.Phone

    /** Diagnostics' Overview dashboard: the home's last 24 hours. */
    private fun dashboards() {
        frame(screen = { DiagnosticsScreen(home.remote, onBack = {}, start = DiagTab.Dashboards) })
        until("the Overview's panels") {
            listOf("http_request_duration_seconds_count", "maktaba_host_load1").all { compose.has("dash-panel-$it") }
        }
        shoot("dashboards")
    }

    /** Needs attention: the night's causes, each with what fixes it; a tablet opens the one Retry cannot fix. */
    private fun attention() {
        frame(Place.Attention)
        val first = "group-fingerprint:nas_timeout"
        until("Needs attention's groups") { compose.has(first) && compose.has("group-chapter_thumbs:disk_full") }
        if (tablet()) {
            compose.onNodeWithTag("group-chapter_thumbs:disk_full").performClick()
        }
        shoot("attention")
    }

    /** The queue: the jobs running now on the library's films, and the stages; a tablet opens one stage. */
    private fun queue() {
        frame(Place.Queue)
        val titles = listOf("v-lake-nasser", "v-ghost-octopus", "v-llamigos").map { DemoRemoteHome.title(demo, it) }
        until("the jobs running on $titles") { titles.all(::shows) && shows("Transcribe") }
        if (tablet()) {
            // the stage's row, under the running jobs (one of which is a transcription too)
            compose.onAllNodes(hasText("Transcribe") and hasClickAction()).onLast().performClick()
            until("the stage's failures read") { "/api/queues/${DemoRemoteHome.STAGE}/failures" in home.paths }
        }
        shoot("queue")
    }

    /** Updates: the version it runs, the next one waiting for the window; a tablet opens the last update. */
    private fun updates() {
        frame(Place.Updates)
        until("the updates and the last run") { shows("0.3.91") && "/api/cluster/updates/runs/${DemoRemoteHome.RUN}" in home.paths }
        if (tablet()) {
            compose.onAllNodes(hasText("0.3.90 · ", substring = true) and hasClickAction()).onFirst().performClick()
            until("the run's steps") { shows("0.3.89") }
        }
        shoot("updates")
    }

    /** Approve a sign-in: the living room's TV's code typed and looked up, waiting for Approve. */
    private fun approveCode() {
        compose.setContent {
            MaktabaTheme(ThemeMode.Dark) {
                Surface(Modifier.fillMaxSize()) {
                    ApproveSignIn(home.remote.client, username = home.user.username, homeName = demo.homeName, onClose = {}, onSignedOut = {})
                }
            }
        }
        until("the code field") { compose.has("approve-code") }
        compose.onNodeWithTag("approve-code").performTextInput(DemoRemoteHome.CODE)
        compose.onNodeWithTag("approve-look").performClick()
        until("the TV asking") { compose.has("approve-card") && compose.has("approve-confirm") }
        shoot("approve-code")
    }

    // ---- the phone ----

    @Test @Config(qualifiers = PHONE) fun dashboardsPhoneEnglish() = dashboards()
    @Test @Config(qualifiers = "ar-$PHONE") fun dashboardsPhoneArabic() = dashboards()
    @Test @Config(qualifiers = PHONE) fun attentionPhoneEnglish() = attention()
    @Test @Config(qualifiers = "ar-$PHONE") fun attentionPhoneArabic() = attention()
    @Test @Config(qualifiers = PHONE) fun queuePhoneEnglish() = queue()
    @Test @Config(qualifiers = "ar-$PHONE") fun queuePhoneArabic() = queue()
    @Test @Config(qualifiers = PHONE) fun approveCodePhoneEnglish() = approveCode()
    @Test @Config(qualifiers = "ar-$PHONE") fun approveCodePhoneArabic() = approveCode()
    @Test @Config(qualifiers = PHONE) fun updatesPhoneEnglish() = updates()
    @Test @Config(qualifiers = "ar-$PHONE") fun updatesPhoneArabic() = updates()

    // ---- the 10" tablet in landscape ----

    @Test @Config(qualifiers = TABLET) fun dashboardsTabletEnglish() = dashboards()
    @Test @Config(qualifiers = "ar-$TABLET") fun dashboardsTabletArabic() = dashboards()
    @Test @Config(qualifiers = TABLET) fun attentionTabletEnglish() = attention()
    @Test @Config(qualifiers = "ar-$TABLET") fun attentionTabletArabic() = attention()
    @Test @Config(qualifiers = TABLET) fun queueTabletEnglish() = queue()
    @Test @Config(qualifiers = "ar-$TABLET") fun queueTabletArabic() = queue()
    @Test @Config(qualifiers = TABLET) fun approveCodeTabletEnglish() = approveCode()
    @Test @Config(qualifiers = "ar-$TABLET") fun approveCodeTabletArabic() = approveCode()
    @Test @Config(qualifiers = TABLET) fun updatesTabletEnglish() = updates()
    @Test @Config(qualifiers = "ar-$TABLET") fun updatesTabletArabic() = updates()

    // ---- the 7" tablet upright ----

    @Test @Config(qualifiers = TABLET7) fun dashboardsTablet7English() = dashboards()
    @Test @Config(qualifiers = "ar-$TABLET7") fun dashboardsTablet7Arabic() = dashboards()
    @Test @Config(qualifiers = TABLET7) fun attentionTablet7English() = attention()
    @Test @Config(qualifiers = "ar-$TABLET7") fun attentionTablet7Arabic() = attention()
    @Test @Config(qualifiers = TABLET7) fun queueTablet7English() = queue()
    @Test @Config(qualifiers = "ar-$TABLET7") fun queueTablet7Arabic() = queue()
    @Test @Config(qualifiers = TABLET7) fun approveCodeTablet7English() = approveCode()
    @Test @Config(qualifiers = "ar-$TABLET7") fun approveCodeTablet7Arabic() = approveCode()
    @Test @Config(qualifiers = TABLET7) fun updatesTablet7English() = updates()
    @Test @Config(qualifiers = "ar-$TABLET7") fun updatesTablet7Arabic() = updates()

    /** The windows the shots are taken in, with the name each file starts with. */
    enum class Form(val widthDp: Int, val heightDp: Int, val prefix: String) {
        Phone(411, 891, "android-phone"),
        Tablet(1280, 800, "android-tablet"),
        Tablet7(600, 960, "android-tablet-7"),
    }

    companion object {
        /** A 6.3" phone (1233 × 2673). */
        const val PHONE = "w411dp-h891dp-xxhdpi"
        /** A 10" tablet in landscape (2560 × 1600). */
        const val TABLET = "w1280dp-h800dp-xhdpi"
        /** A 7" tablet upright (1200 × 1920). */
        const val TABLET7 = "w600dp-h960dp-xhdpi"

        // text is drawn by the host's font rasteriser: a reference recorded on a Mac still matches
        // on CI's Linux within a small share of pixels
        private val options = RoborazziOptions(compareOptions = RoborazziOptions.CompareOptions(changeThreshold = 0.02f))
    }
}
