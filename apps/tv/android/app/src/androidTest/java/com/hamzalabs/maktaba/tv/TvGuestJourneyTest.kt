package com.hamzalabs.maktaba.tv

import android.util.Log
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasScrollToNodeAction
import androidx.compose.ui.test.hasSetTextAction
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.isRoot
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.performScrollToNode
import androidx.compose.ui.test.performSemanticsAction
import androidx.compose.ui.test.performTextInput
import androidx.compose.ui.test.printToLog
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import com.hamzalabs.maktaba.core.guest.Guests
import com.hamzalabs.maktaba.core.session.AppSession
import com.hamzalabs.maktaba.core.session.CloudSession
import com.hamzalabs.maktaba.core.session.PreferencesStore
import mockwebserver3.Dispatcher
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.RecordedRequest
import okio.Buffer
import org.junit.AfterClass
import org.junit.Assert.assertTrue
import org.junit.BeforeClass
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import java.util.Collections

/**
 * Story 50.27 T06: Watch a broadcast on the Android TV emulator, against a mock edge on the device
 * itself (the debug build's `guest` preferences name it). From the home picker the guest types the
 * publisher's handle and an MK- code, picks the first channel, and the app's own player plays its
 * HLS from the edge: the playlists and segments are asked for under the viewer token, and no error
 * shows. Logcat for the run is kept in the CI report.
 */
@RunWith(AndroidJUnit4::class)
class TvGuestJourneyTest {
    @get:Rule val rule = createAndroidComposeRule<MainActivity>()

    companion object {
        private const val TAG = "TvGuestJourney"
        private val server = MockWebServer()
        private val asked: MutableList<String> = Collections.synchronizedList(mutableListOf())

        private fun asset(name: String): ByteArray =
            InstrumentationRegistry.getInstrumentation().context.assets.open(name).use { it.readBytes() }

        private fun json(code: Int, body: String) =
            MockResponse.Builder().code(code).setHeader("Content-Type", "application/json").body(body).build()

        private fun file(name: String, type: String) =
            MockResponse.Builder().code(200).setHeader("Content-Type", type).body(Buffer().write(asset(name))).build()

        /** The edge's guest API and the channel's HLS under its viewer token (the player test's clip). */
        private fun route(r: RecordedRequest): MockResponse {
            val path = r.url.encodedPath
            asked += "${r.method} $path"
            Log.i(TAG, "edge asked: ${r.method} $path")
            val bearer = r.headers["Authorization"]?.startsWith("Bearer mkbg1.") == true
            val body = r.body?.utf8().orEmpty()
            val base = server.url("/").toString().trimEnd('/')
            return when {
                path == "/api/v1/redeem" ->
                    if ("\"MK-TV06\"" in body && "\"tv-pub\"" in body) json(200, """{"pass":"mkbg1.dHYtcGFzcw.sig","publisher":"tv-pub","channels":["ch1"],"expires_at":"2030-01-01T00:00:00Z"}""")
                    else json(404, """{"error":"code_unknown"}""")
                path == "/api/v1/lineup" && bearer -> json(200, """{"publisher":{"id":"tv-pub","name":"Emulator broadcast"},"channels":[{"id":"ch1","name":"Clip","number":1,"state":"live","now":{"title":"Test clip"}}]}""")
                path == "/api/v1/play" && bearer -> json(200, """{"session_id":"s1","beat_key":"k1","master_url":"$base/vt-1/ch1/master.m3u8","refresh_after":300,"channel_state":"live"}""")
                path.endsWith("/beat") -> json(200, """{"ok":true,"channel_state":"live"}""")
                path.startsWith("/vt-1/ch1/") -> {
                    val rest = path.removePrefix("/vt-1/ch1/")
                    val type = if (rest.endsWith(".ts")) "video/mp2t" else if (rest.endsWith(".m3u8")) "application/vnd.apple.mpegurl" else "text/vtt"
                    runCatching { file("hls/$rest", type) }.getOrElse { json(404, "{}") }
                }
                else -> json(404, """{"error":"nope"}""")
            }
        }

        @JvmStatic
        @BeforeClass
        fun edge() {
            server.dispatcher = object : Dispatcher() {
                override fun dispatch(request: RecordedRequest) = route(request)
            }
            server.start()
            val context = InstrumentationRegistry.getInstrumentation().targetContext
            PreferencesStore(context, Guests.PREFS).put(Guests.KEY_EDGE, server.url("/").toString())
            // the picker never asks the hosted cloud for a code (as FireTvJourneyTest)
            PreferencesStore(context, "session").put(CloudSession.KEY_API, "http://127.0.0.1:9")
        }

        @JvmStatic
        @AfterClass
        fun stop() {
            server.close()
        }
    }

    private fun shown(tag: String) = rule.onAllNodes(hasTestTag(tag), useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty()

    private fun screen(what: String, timeoutMs: Long = 30_000, check: () -> Boolean) {
        try {
            rule.waitUntil(timeoutMs, check)
        } catch (e: Throwable) {
            val roots = rule.onAllNodes(isRoot(), useUnmergedTree = true)
            for (i in roots.fetchSemanticsNodes().indices) roots[i].printToLog(TAG)
            Log.i(TAG, "the edge was asked: $asked")
            throw AssertionError("timed out waiting for $what (asked: $asked)", e)
        }
    }

    private fun click(tag: String) = rule.onNode(hasTestTag(tag)).performSemanticsAction(SemanticsActions.OnClick)

    private fun type(tag: String, text: String) = rule.onNode(hasSetTextAction() and hasAnyAncestor(hasTestTag(tag))).performTextInput(text)

    @Test
    fun aShortCodeTypedWithTheRemotePlaysTheChannel() {
        val app = rule.activity.application as MaktabaTVApp
        // a run after another journey may start signed in: the guest's way in is the home picker
        if (app.session.phase.value !is AppSession.Phase.ChoosingHome) {
            InstrumentationRegistry.getInstrumentation().runOnMainSync { app.session.switchHome() }
        }
        screen("the home picker") { shown("picker-other-ways") }
        rule.onNode(hasScrollToNodeAction() and hasAnyAncestor(hasTestTag("picker-other-ways"))).performScrollToNode(hasTestTag("picker-watch-broadcast"))
        click("picker-watch-broadcast")

        screen("the guest space") { shown("tv-guest-add-code") }
        click("tv-guest-add-code")
        screen("the code screen") { shown("tv-guest-publisher") }
        type("tv-guest-publisher", "tv-pub")
        type("tv-guest-code", "mk tv06")
        click("tv-guest-watch")

        screen("the channels") { shown("tv-guest-channel-ch1") }
        click("tv-guest-channel-ch1")
        screen("the player") { shown("tv-guest-playing-ch1") }
        // the app's player reads the master, the rendition and its segments under the viewer token
        screen("the segments played", 60_000) { asked.contains("GET /vt-1/ch1/seg0.ts") && asked.contains("GET /vt-1/ch1/seg1.ts") }
        // a moment of playing: a picture the player could not decode would show its error by now
        Thread.sleep(3_000)
        rule.waitForIdle()
        assertTrue("the play went with the pass: $asked", asked.any { it == "POST /api/v1/play" })
        assertTrue("no refusal or error shows", !shown("tv-guest-state"))
        Log.i(TAG, "played: $asked")
    }
}
