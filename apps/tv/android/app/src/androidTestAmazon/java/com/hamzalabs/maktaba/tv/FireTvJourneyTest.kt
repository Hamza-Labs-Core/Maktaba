@file:OptIn(UnstableApi::class)

package com.hamzalabs.maktaba.tv

import android.graphics.SurfaceTexture
import android.util.Log
import android.view.Surface
import androidx.annotation.OptIn
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.isRoot
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.test.performSemanticsAction
import androidx.compose.ui.test.printToLog
import androidx.compose.ui.test.hasSetTextAction
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.performTextInput
import androidx.lifecycle.Lifecycle
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.test.espresso.Espresso
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import com.hamzalabs.maktaba.core.api.ClientHeader
import com.hamzalabs.maktaba.core.session.AppSession
import com.hamzalabs.maktaba.tv.playback.TvPlayback
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import mockwebserver3.Dispatcher
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.RecordedRequest
import okio.Buffer
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import java.util.Collections
import java.util.concurrent.atomic.AtomicBoolean

/**
 * Story 58.05 T04: the Fire TV build on Google's Android TV emulator without Google APIs (no Play
 * Services on the image), against a mock home on the device itself. It types the home's address,
 * signs in by the home's code (approved once the code is on screen), plays a real clip through the
 * app's own player (`TvPlayback`: the session asks for the `firetv` profile with this device's
 * `device_caps`, and Media3 decodes the file on the emulator), then Back on Home leaves the app,
 * as Amazon's tests check. What only a Fire TV shows (its codecs, HDR, remote) is the real-device
 * checklist's (docs/stores/amazon.md). Logcat is kept in the CI report.
 */
@RunWith(AndroidJUnit4::class)
class FireTvJourneyTest {
    @get:Rule val rule = createAndroidComposeRule<MainActivity>()

    private val server = MockWebServer()
    private val approved = AtomicBoolean(false)
    private val opens: MutableList<RecordedRequest> = Collections.synchronizedList(mutableListOf())
    private val clientHeaders: MutableSet<String> = Collections.synchronizedSet(mutableSetOf())
    private val clip: ByteArray by lazy {
        InstrumentationRegistry.getInstrumentation().context.assets.open("two-audio.mp4").use { it.readBytes() }
    }
    private val playScope = CoroutineScope(SupervisorJob() + Dispatchers.Main)
    private val background = CoroutineScope(SupervisorJob() + Dispatchers.Default)

    private fun json(code: Int, body: String) =
        MockResponse.Builder().code(code).setHeader("Content-Type", "application/json").body(body).build()

    private fun route(r: RecordedRequest): MockResponse {
        Log.i(TAG, "home asked: ${r.method} ${r.url.encodedPath} (${r.headers[ClientHeader.NAME]})")
        r.headers[ClientHeader.NAME]?.let { clientHeaders += it }
        return when (r.url.encodedPath) {
            "/api/discovery/home" -> json(200, """{"id":"0a1b2c3d-0000-4000-8000-000000000058","name":"Test home","version":"0.3.200","urls":[],"sign_in":{"password":true,"code":true},"api_level":1,"api_level_oldest":1,"client_floor":{}}""")
            "/api/pairing/device" -> json(201, """{"code":"ABCD-0058","device_code":"secret-58","expires_at":"2099-01-01T00:00:00Z","verify_path":"/pair?code=ABCD-0058","interval":1}""")
            "/api/pairing/device/token" ->
                if (approved.get()) json(200, """{"access_token":"${token()}","refresh_token":"r1"}""") else json(202, """{"looked_up":false}""")
            "/api/auth/me" -> json(200, """{"id":"u1","username":"amina","home_name":"Test home"}""")
            "/api/stream/sessions" -> {
                opens += r
                json(201, """{"session_id":"s1","mode":"direct","direct_url":"/media/clip.mp4","expires_at":"2099-01-01T00:00:00Z"}""")
            }
            // the position reports and the close
            "/api/stream/sessions/s1/progress", "/api/stream/sessions/s1" -> json(200, "{}")
            "/media/clip.mp4" -> MockResponse.Builder().code(200).setHeader("Content-Type", "video/mp4").body(Buffer().write(clip)).build()
            else -> json(404, """{"title":"not here"}""")
        }
    }

    private fun token(): String {
        val enc = java.util.Base64.getUrlEncoder().withoutPadding()
        return enc.encodeToString("""{"alg":"RS256"}""".toByteArray()) + "." +
            enc.encodeToString("""{"sub":"u1","exp":${System.currentTimeMillis() / 1000 + 86_400}}""".toByteArray()) + ".sig"
    }

    @After
    fun stop() {
        playScope.cancel()
        background.cancel()
        server.close()
    }

    private fun waitFor(what: String, timeoutMs: Long = 30_000, check: () -> Boolean) {
        val until = System.currentTimeMillis() + timeoutMs
        while (!check()) {
            if (System.currentTimeMillis() > until) throw AssertionError("timed out waiting for $what")
            Thread.sleep(100)
        }
    }

    /** Waits for [check] on screen; when it never holds, the screen's tree goes to logcat first. */
    private fun screen(what: String, check: () -> Boolean) {
        try {
            rule.waitUntil(30_000, check)
        } catch (e: Throwable) {
            val roots = rule.onAllNodes(isRoot(), useUnmergedTree = true)
            for (i in roots.fetchSemanticsNodes().indices) roots[i].printToLog(TAG)
            throw AssertionError("timed out waiting for $what", e)
        }
    }

    private fun shown(tag: String) = rule.onAllNodes(hasTestTag(tag), useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty()

    @Test
    fun signsInByCodePlaysAClipAndBackLeavesTheApp() {
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest) = route(request)
        }
        server.start()
        val app = rule.activity.application as MaktabaTVApp

        // 1. the home picker: its address typed with the remote's keyboard, then Go
        screen("the home picker") { shown("picker-address") }
        val address = server.url("/").toString().trimEnd('/')
        // the field itself, inside the tagged label-and-field column
        val field = rule.onNode(hasSetTextAction() and hasAnyAncestor(hasTestTag("picker-address")))
        field.performTextInput(address)
        rule.onNode(hasText(rule.activity.getString(com.hamzalabs.maktaba.core.R.string.connect)) and hasClickAction()).performSemanticsAction(SemanticsActions.OnClick)

        // 2. the code screen: the code shows, a phone approves it, the TV is signed in
        screen("the code on screen") { rule.onAllNodes(hasTestTag("signin-code") and hasText("ABCD-0058"), useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty() }
        approved.set(true)
        // the code screen's polling runs on Compose's test clock, which moves while the rule waits
        screen("signed in") { app.session.phase.value is AppSession.Phase.SignedIn }
        val client = (app.session.phase.value as AppSession.Phase.SignedIn).client
        assertTrue("every request names the Fire TV build: $clientHeaders", clientHeaders.isNotEmpty() && clientHeaders.all { it.endsWith("(firetv; tv)") })

        // 3. a clip through the app's own player, decoded on the device into a surface
        val texture = SurfaceTexture(0).apply { setDefaultBufferSize(64, 36) }
        val surface = Surface(texture)
        val context = rule.activity
        var playback: TvPlayback? = null
        InstrumentationRegistry.getInstrumentation().runOnMainSync {
            playback = TvPlayback(context, client, "v-clip", 0, playScope, background, signedOut = {}).also { it.start() }
        }
        var position = 0L
        waitFor("the clip playing") {
            var playing = false
            InstrumentationRegistry.getInstrumentation().runOnMainSync {
                val p = playback?.player
                if (p != null) {
                    p.setVideoSurface(surface)
                    playing = p.playbackState == Player.STATE_READY && p.isPlaying
                    position = p.currentPosition
                }
                playback?.problem?.let { throw AssertionError("the player failed: ${it.kind} ${it.details}") }
            }
            playing && position > 500
        }
        InstrumentationRegistry.getInstrumentation().runOnMainSync {
            val p = playback!!.player!!
            Log.i(TAG, "playing at ${p.currentPosition} ms: ${p.videoFormat} ${p.audioFormat}")
            assertEquals("video/avc", p.videoFormat?.sampleMimeType)
            playback!!.stop()
        }
        surface.release()
        texture.release()

        val open = Json.parseToJsonElement(opens.first().body!!.utf8()).jsonObject
        assertEquals("firetv", open.getValue("client_profile").jsonPrimitive.content)
        val caps = open.getValue("device_caps").jsonObject
        assertTrue("the report names the emulator's decoders: $caps", caps.getValue("video").toString().contains("h264"))
        Log.i(TAG, "device_caps on this emulator: $caps")

        // 4. Back on Home leaves the app for the launcher
        // (after the first run's question about crash reports, answered as a person would)
        screen("the crash-report question") { shown("crash-question-no") }
        rule.onNode(hasTestTag("crash-question-no")).performSemanticsAction(SemanticsActions.OnClick)
        screen("the question gone") { !shown("crash-question") }
        Espresso.pressBackUnconditionally()
        // Android 11 and older (Fire OS 7 and 8) finish the activity; Android 12 and later move the
        // task behind the launcher. Either way it is no longer started. (No Compose wait here: once
        // the activity is gone there is no screen to idle.)
        waitFor("the app to leave") {
            rule.activityRule.scenario.state.let { it == Lifecycle.State.DESTROYED || it == Lifecycle.State.CREATED }
        }
    }

    private companion object {
        const val TAG = "FireTvJourney"
    }
}
