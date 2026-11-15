package com.hamzalabs.maktaba.tv

import androidx.activity.ComponentActivity
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.test.ExperimentalTestApi
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.v2.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performSemanticsAction
import androidx.compose.ui.test.performKeyInput
import androidx.compose.ui.test.performTextInput
import androidx.compose.ui.test.printToString
import androidx.compose.ui.test.pressKey
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.github.takahirom.roborazzi.RoborazziOptions
import com.github.takahirom.roborazzi.captureRoboImage
import com.hamzalabs.maktaba.core.MapStore
import com.hamzalabs.maktaba.core.guest.GuestClient
import com.hamzalabs.maktaba.core.guest.GuestPass
import com.hamzalabs.maktaba.core.guest.GuestPassStore
import com.hamzalabs.maktaba.core.guest.GuestSpace
import com.hamzalabs.maktaba.core.session.SecretBox
import com.hamzalabs.maktaba.tv.ui.screens.TvGuestSpace
import com.hamzalabs.maktaba.tv.ui.theme.MaktabaTvTheme
import mockwebserver3.Dispatcher
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.RecordedRequest
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import java.util.Collections
import java.util.concurrent.atomic.AtomicBoolean

/**
 * Story 50.27 on Android TV (T04's screens; T06 runs the same short code on the emulator): Watch a
 * broadcast against a mock edge. The handle and short code typed with the remote open the
 * channels; up and down change the channel; an offline home's slate says so with no error; the QR
 * pairing shows its check code and plays once the phone sends a pass (Arabic); Remove forgets a
 * saved broadcast. Dark screenshots on test data, for maktaba.dev's Apps page.
 */
@OptIn(ExperimentalTestApi::class)
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class TvGuestScreensTest {
    @get:Rule val compose = createAndroidComposeRule<ComponentActivity>()

    private val server = MockWebServer()
    private val plays: MutableList<String> = Collections.synchronizedList(mutableListOf())
    private val phoneSent = AtomicBoolean(false)
    private var closed = 0

    private object Box : SecretBox {
        override fun seal(plain: ByteArray) = plain.reversedArray()
        override fun open(sealed: ByteArray) = sealed.reversedArray()
    }

    private val store = GuestPassStore(MapStore(), Box) { 1_800_000_000L }

    private fun json(code: Int, body: String) = MockResponse.Builder().code(code).setHeader("Content-Type", "application/json").body(body).build()

    @Before
    fun start() {
        java.util.TimeZone.setDefault(java.util.TimeZone.getTimeZone("UTC"))
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                val body = request.body?.utf8().orEmpty()
                return when (request.url.encodedPath) {
                    "/api/v1/redeem" -> if ("MK-UI42" in body && "ui-pub" in body) json(200, PASS) else json(404, """{"error":"code_unknown"}""")
                    "/api/v1/lineup" -> json(200, LINEUP)
                    "/api/v1/play" -> {
                        plays += body
                        val ch = if ("ch2" in body) "ch2" else "ch1"
                        json(200, """{"session_id":"s1","beat_key":"k1","master_url":"https://live.maktaba.tv/vt-1/$ch/master.m3u8","channel_state":"${if (ch == "ch2") "offline_slate" else "live"}"}""")
                    }
                    "/api/v1/pair" -> json(201, """{"nonce":"n-ui","poll_key":"pk-ui","code":"Q7XZ","url":"https://watch.maktaba.tv/pair?tv=n-ui","expires_at":"2030-01-01T00:00:00Z"}""")
                    "/api/v1/pair/n-ui" ->
                        if (request.headers["X-Maktaba-Pair-Key"] != "pk-ui") json(410, """{"error":"pair_gone"}""")
                        else if (phoneSent.get()) json(200, """{"publisher":"ui-pub","credential":"mkbg1.dWktdHY.sig","kind":"pass","channels":["ch1","ch2"],"expires_at":"2030-01-01T00:00:00Z"}""")
                        else MockResponse.Builder().code(204).build()
                    else -> json(404, """{"error":"nope"}""")
                }
            }
        }
        server.start()
    }

    @After
    fun stop() = server.close()

    private fun show() {
        val space = GuestSpace(store, GuestClient(server.url("/")))
        compose.setContent { MaktabaTvTheme { TvGuestSpace(space, onClose = { closed++ }, playerFor = { null }) } }
    }

    private fun shown(tag: String) = compose.onAllNodes(hasTestTag(tag), useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty()

    /** Waits for [check]; when it never holds, fails with what the screen shows. */
    private fun until(timeoutMs: Long = 10_000, check: () -> Boolean) {
        try {
            compose.waitUntil(timeoutMs) { check() }
        } catch (e: Throwable) {
            val tree = runCatching { compose.onRoot(useUnmergedTree = true).printToString() }.getOrElse { it.toString() }
            throw AssertionError("timed out; the screen shows:\n$tree", e)
        }
    }

    /** The text field inside a tagged label-and-field column. */
    private fun field(tag: String) = compose.onNode(androidx.compose.ui.test.hasSetTextAction() and androidx.compose.ui.test.hasAnyAncestor(hasTestTag(tag)))

    private fun capture(name: String) {
        compose.waitForIdle()
        compose.onRoot().captureRoboImage("src/test/screenshots/$name.png", roborazziOptions = options)
    }

    @Test
    @Config(qualifiers = TV)
    fun aShortCodeTypedWithTheRemotePlaysAndUpAndDownChangeTheChannel() {
        show()
        until { shown("tv-guest-no-saved") }
        capture("tv_guest_home_dark_en")
        compose.onNodeWithTag("tv-guest-add-code").performSemanticsAction(androidx.compose.ui.semantics.SemanticsActions.OnClick)
        until { shown("tv-guest-publisher") }
        field("tv-guest-publisher").performTextInput("ui-pub")
        field("tv-guest-code").performTextInput("ui42")
        compose.onNodeWithTag("tv-guest-watch").performSemanticsAction(androidx.compose.ui.semantics.SemanticsActions.OnClick)
        until { shown("tv-guest-channel-ch1") }
        assertTrue(compose.onAllNodes(hasText("Al-Noor on Maktaba")).fetchSemanticsNodes().isNotEmpty())
        capture("tv_guest_channels_dark_en")
        compose.onNodeWithTag("tv-guest-channel-ch1").performSemanticsAction(androidx.compose.ui.semantics.SemanticsActions.OnClick)
        until { shown("tv-guest-playing-ch1") && plays.size == 1 }
        assertTrue("the CC BY credit shows", shown("tv-guest-credit"))
        compose.onNodeWithTag("tv-guest-player").performKeyInput { pressKey(Key.DirectionUp) }
        until { shown("tv-guest-playing-ch2") && plays.size == 2 }
        until { compose.onAllNodes(hasText("This channel's home is offline", substring = true)).fetchSemanticsNodes().isNotEmpty() }
        assertTrue("an offline home is no error", !shown("tv-guest-retry"))
        compose.onNodeWithTag("tv-guest-player").performKeyInput { pressKey(Key.DirectionDown) }
        until { shown("tv-guest-playing-ch1") && plays.size == 3 }
        assertEquals("ui-pub", store.list().single().publisher)
    }

    @Test
    @Config(qualifiers = "ar-$TV")
    fun theQrPairingShowsItsCheckCodeAndOpensTheChannelsOnceThePhoneSends() {
        show()
        until { shown("tv-guest-pair") }
        compose.onNodeWithTag("tv-guest-pair").performSemanticsAction(androidx.compose.ui.semantics.SemanticsActions.OnClick)
        until { shown("tv-guest-pair-code") }
        assertTrue(compose.onAllNodes(hasText("Q7XZ", substring = true)).fetchSemanticsNodes().isNotEmpty())
        assertTrue(shown("tv-guest-qr"))
        capture("tv_guest_pair_dark_ar")
        phoneSent.set(true)
        until(20_000) { shown("tv-guest-channel-ch1") }
        assertTrue(compose.onAllNodes(hasText("Al-Noor على مكتبة")).fetchSemanticsNodes().isNotEmpty())
        assertEquals("mkbg1.dWktdHY.sig", store.pass("ui-pub")!!.credential)
        capture("tv_guest_channels_dark_ar")
    }

    @Test
    @Config(qualifiers = TV)
    fun removeForgetsASavedBroadcastAndCloseLeaves() {
        store.save(GuestPass("ui-pub", "Al-Noor", "mkbg1.a.b", expiresAt = 1_900_000_000L))
        show()
        until { shown("tv-guest-saved-ui-pub") }
        compose.onNodeWithTag("tv-guest-remove-ui-pub").performSemanticsAction(androidx.compose.ui.semantics.SemanticsActions.OnClick)
        until { shown("tv-guest-no-saved") }
        assertTrue(store.list().isEmpty())
        compose.onNodeWithTag("tv-guest-close").performSemanticsAction(androidx.compose.ui.semantics.SemanticsActions.OnClick)
        assertEquals(1, closed)
    }

    @Test
    @Config(qualifiers = TV)
    fun aWrongCodeSaysSo() {
        show()
        until { shown("tv-guest-add-code") }
        compose.onNodeWithTag("tv-guest-add-code").performSemanticsAction(androidx.compose.ui.semantics.SemanticsActions.OnClick)
        until { shown("tv-guest-publisher") }
        field("tv-guest-publisher").performTextInput("ui-pub")
        field("tv-guest-code").performTextInput("ZZ99")
        compose.onNodeWithTag("tv-guest-watch").performSemanticsAction(androidx.compose.ui.semantics.SemanticsActions.OnClick)
        until { shown("tv-guest-problem") }
        assertTrue(compose.onAllNodes(hasText("This invite link isn't valid.")).fetchSemanticsNodes().isNotEmpty())
    }

    private companion object {
        const val TV = "w960dp-h540dp-land-television-xhdpi"
        const val PASS = """{"pass":"mkbg1.dWktcGFzcw.sig","publisher":"ui-pub","channels":["ch1","ch2"],"expires_at":"2030-01-01T00:00:00Z"}"""
        const val LINEUP = """{"publisher":{"id":"ui-pub","name":"Al-Noor"},"channels":[
            {"id":"ch1","name":"Quran","number":1,"state":"live","now":{"start":"2026-11-15T10:00:00Z","end":"2026-11-15T11:00:00Z","title":"Morning recitation","attribution":{"title":"Recitation","by":"Example Reciter","licence":"CC BY 4.0"}},"next":{"start":"2026-11-15T11:00:00Z","end":"2026-11-15T12:00:00Z","title":"Tafsir"}},
            {"id":"ch2","name":"Lectures","number":2,"state":"offline_slate","now":{"start":"2026-11-15T10:00:00Z","end":"2026-11-15T11:00:00Z","title":"Seerah"},"next":null}]}"""
        val options = RoborazziOptions(
            recordOptions = RoborazziOptions.RecordOptions(resizeScale = 0.5),
            compareOptions = RoborazziOptions.CompareOptions(changeThreshold = 0.02f),
        )
    }
}
