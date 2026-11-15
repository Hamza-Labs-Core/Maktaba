package com.hamzalabs.maktaba.player.guest

import androidx.activity.ComponentActivity
import androidx.compose.material3.Surface
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.setValue
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.v2.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollTo
import androidx.compose.ui.test.performTextInput
import androidx.compose.ui.test.performTouchInput
import androidx.compose.ui.test.swipeDown
import androidx.compose.ui.test.swipeUp
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.github.takahirom.roborazzi.RoborazziOptions
import com.github.takahirom.roborazzi.captureRoboImage
import com.hamzalabs.maktaba.core.MapStore
import com.hamzalabs.maktaba.core.MemoryTokens
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.guest.GuestClient
import com.hamzalabs.maktaba.core.guest.GuestPass
import com.hamzalabs.maktaba.core.guest.GuestPassStore
import com.hamzalabs.maktaba.core.guest.GuestSpace
import com.hamzalabs.maktaba.core.guest.LocalWatchBroadcast
import com.hamzalabs.maktaba.core.session.AppSession
import com.hamzalabs.maktaba.core.session.SecretBox
import com.hamzalabs.maktaba.core.ui.HomePicker
import com.hamzalabs.maktaba.core.ui.MaktabaApp
import com.hamzalabs.maktaba.core.ui.MaktabaTheme
import com.hamzalabs.maktaba.core.ui.ThemeMode
import com.hamzalabs.maktaba.player.harness.settledUntil
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
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

/**
 * Story 50.27 on the phone (T04's screens): Watch a broadcast against a mock edge. A handle and a
 * short code open the publisher's channels with now and next, a channel plays, a swipe changes the
 * channel, an offline home's slate says so; a used code says why in Arabic; an invite's link opens
 * straight on the channels; Remove forgets a saved broadcast; the home picker shows the entry only
 * where the app provides it; an invite waits for the home's word on a parental limit, a limit
 * refuses it (in English and Arabic) and closes an open guest space. The screenshots (dark, test data) are the maktaba.dev Apps page's.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class GuestScreensTest {
    @get:Rule val compose = createAndroidComposeRule<ComponentActivity>()

    private val server = MockWebServer()
    private val asked: MutableList<String> = Collections.synchronizedList(mutableListOf())
    private val playBodies: MutableList<String> = Collections.synchronizedList(mutableListOf())
    private var clock = 1_800_000_000L

    private object Box : SecretBox {
        override fun seal(plain: ByteArray) = plain.reversedArray()
        override fun open(sealed: ByteArray) = sealed.reversedArray()
    }

    private val store = GuestPassStore(MapStore(), Box) { clock }

    /** The signed-in home's `GET /api/me/parental` answer (no answer until a test sets one). */
    @Volatile private var parental: MockResponse = MockResponse.Builder().code(503).build()

    private fun json(code: Int, body: String) = MockResponse.Builder().code(code).setHeader("Content-Type", "application/json").body(body).build()

    @Before
    fun start() {
        // the guide's times read the same on a Mac and on CI
        java.util.TimeZone.setDefault(java.util.TimeZone.getTimeZone("UTC"))
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                val path = request.url.encodedPath
                asked += "${request.method} $path"
                val body = request.body?.utf8().orEmpty()
                val bearer = request.headers["Authorization"]?.startsWith("Bearer mkbg1.") == true
                return when (path) {
                    "/api/me/parental" -> parental
                    "/api/v1/redeem" -> when {
                        "MK-UI42" in body || INVITE in body -> json(200, PASS)
                        "MK-USED" in body -> json(410, """{"error":"invite_used"}""")
                        else -> json(404, """{"error":"code_unknown"}""")
                    }
                    "/api/v1/lineup" -> if (bearer) json(200, LINEUP) else json(403, """{"error":"invite_required"}""")
                    "/api/v1/play" -> {
                        playBodies += body
                        val state = if ("ch2" in body) "offline_slate" else "live"
                        json(200, """{"session_id":"s1","beat_key":"k1","master_url":"${server.url("/vt-1/").toString().trimEnd('/')}/${if ("ch2" in body) "ch2" else "ch1"}/master.m3u8","channel_state":"$state"}""")
                    }
                    else -> json(404, """{"error":"nope"}""")
                }
            }
        }
        server.start()
    }

    @After
    fun stop() = server.close()

    private fun space() = GuestSpace(store, GuestClient(server.url("/")))

    private fun show(space: GuestSpace = space(), invite: String? = null) {
        compose.setContent { MaktabaTheme(ThemeMode.Dark) { Surface { GuestSpaceScreen(space, invite, onClose = {}, scanner = { _, _ -> }, playerFor = { null }) } } }
    }

    private fun shown(tag: String) = compose.onAllNodes(hasTestTag(tag), useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty()

    private fun capture(name: String) {
        compose.waitForIdle()
        compose.onRoot().captureRoboImage("src/test/screenshots/$name.png", roborazziOptions = options)
    }

    @Test
    @Config(qualifiers = PHONE)
    fun aShortCodeOpensTheChannelsAndASwipeChangesTheChannel() {
        show()
        compose.settledUntil(5_000) { shown("guest-no-saved") }
        capture("guest_home_phone_dark_en")
        compose.onNodeWithTag("guest-publisher").performTextInput("ui-pub")
        compose.onNodeWithTag("guest-code").performTextInput("mk ui42")
        compose.onNodeWithTag("guest-watch-code").performScrollTo().performClick()
        compose.settledUntil(10_000) { shown("guest-channel-ch1") }
        assertTrue("now and next show", compose.onAllNodes(hasText("Morning recitation", substring = true)).fetchSemanticsNodes().isNotEmpty())
        assertTrue(compose.onAllNodes(hasText("Al-Noor on Maktaba")).fetchSemanticsNodes().isNotEmpty())
        capture("guest_channels_phone_dark_en")
        // the pass was kept, sealed, with the lineup's name
        assertEquals("Al-Noor", store.pass("ui-pub")!!.name)

        compose.onNodeWithTag("guest-channel-ch1").performClick()
        compose.settledUntil(10_000) { shown("guest-playing-ch1") && playBodies.size == 1 }
        assertTrue("the CC BY credit shows", shown("guest-credit"))
        assertTrue(playBodies[0].contains("\"channel\":\"ch1\""))
        // up: the next channel, whose home is offline (its slate plays, the note shows)
        compose.onNodeWithTag("guest-player").performTouchInput { swipeUp() }
        compose.settledUntil(10_000) { shown("guest-playing-ch2") && playBodies.size == 2 }
        compose.settledUntil(10_000) { compose.onAllNodes(hasText("This channel's home is offline", substring = true)).fetchSemanticsNodes().isNotEmpty() }
        assertTrue("an offline home is no error: no Try again", !shown("guest-retry"))
        // down: back to the first
        compose.onNodeWithTag("guest-player").performTouchInput { swipeDown() }
        compose.settledUntil(10_000) { shown("guest-playing-ch1") && playBodies.size == 3 }
    }

    @Test
    @Config(qualifiers = "ar-$PHONE")
    fun aUsedCodeSaysWhyInArabic() {
        show()
        compose.settledUntil(5_000) { shown("guest-home") }
        compose.onNodeWithTag("guest-publisher").performTextInput("ui-pub")
        compose.onNodeWithTag("guest-code").performTextInput("MK-USED")
        compose.onNodeWithTag("guest-watch-code").performScrollTo().performClick()
        compose.settledUntil(10_000) { shown("guest-problem") }
        assertTrue(compose.onAllNodes(hasText("استُخدمت هذه الدعوة", substring = true)).fetchSemanticsNodes().isNotEmpty())
        compose.onNodeWithTag("guest-problem").performScrollTo()
        capture("guest_code_used_phone_dark_ar")
    }

    @Test
    @Config(qualifiers = "ar-$PHONE")
    fun anInvitesLinkOpensStraightOnTheChannels() {
        show(invite = INVITE)
        compose.settledUntil(10_000) { shown("guest-channel-ch1") }
        assertTrue(compose.onAllNodes(hasText("Al-Noor على مكتبة")).fetchSemanticsNodes().isNotEmpty())
        assertTrue(asked.first().startsWith("POST /api/v1/redeem"))
        capture("guest_channels_phone_dark_ar")
    }

    @Test
    @Config(qualifiers = PHONE)
    fun removeForgetsASavedBroadcast() {
        store.save(GuestPass("ui-pub", "Al-Noor", "mkbg1.a.b", expiresAt = clock + 86_400))
        show()
        compose.settledUntil(5_000) { shown("guest-saved-ui-pub") }
        compose.onNodeWithTag("guest-remove-ui-pub").performClick()
        compose.settledUntil(5_000) { shown("guest-no-saved") }
        assertTrue(store.list().isEmpty())
        assertTrue("Remove asks the edge nothing: $asked", asked.isEmpty())
    }

    @Test
    @Config(qualifiers = PHONE)
    fun aBadInviteLinkIsRefusedWithoutAsking() {
        show()
        compose.settledUntil(5_000) { shown("guest-link") }
        compose.onNodeWithTag("guest-link").performScrollTo().performTextInput("https://watch.maktaba.tv:8443/i/$INVITE")
        compose.onNodeWithTag("guest-watch-link").performScrollTo().performClick()
        compose.settledUntil(5_000) { shown("guest-problem") }
        assertTrue(compose.onAllNodes(hasText("That isn't an invite link.")).fetchSemanticsNodes().isNotEmpty())
        assertTrue(asked.isEmpty())
    }

    @Test
    @Config(qualifiers = PHONE)
    fun theHomePickerShowsTheEntryOnlyWhereTheAppProvidesIt() {
        val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
        try {
            val session = AppSession(MapStore(), scope, { MemoryTokens() }, { _, tokens -> ApiClient(server.url("/"), tokens) })
            var opened = 0
            var provided by androidx.compose.runtime.mutableStateOf(false)
            compose.setContent {
                MaktabaTheme(ThemeMode.Dark) {
                    Surface {
                        CompositionLocalProvider(LocalWatchBroadcast provides (if (provided) ({ opened++ }) else null)) {
                            HomePicker(session, MaktabaApp.Player)
                        }
                    }
                }
            }
            compose.waitForIdle()
            assertTrue(!shown("picker-watch-broadcast"))
            provided = true
            compose.settledUntil(5_000) { shown("picker-watch-broadcast") }
            compose.onNodeWithTag("picker-watch-broadcast").performScrollTo().performClick()
            assertEquals(1, opened)
        } finally {
            scope.cancel()
        }
    }

    @Test
    @Config(qualifiers = PHONE)
    fun anInviteWaitsForTheHomesWordAndALimitClosesTheGuestSpace() {
        // signed in at a home that has not said its limit yet: Watch a broadcast fails closed
        val limit = com.hamzalabs.maktaba.core.parental.ParentalState(ApiClient(server.url("/"), MemoryTokens()))
        val gate = com.hamzalabs.maktaba.core.guest.GuestGate { emptyList() }.also { it.follow(limit) }
        val door = com.hamzalabs.maktaba.core.guest.GuestDoor()
        val space = space()
        compose.setContent {
            MaktabaTheme(ThemeMode.Dark) {
                Surface {
                    GuestDoorHost(door, gate, space, open = { invite, close -> GuestSpaceScreen(space, invite, close, scanner = { _, _ -> }, playerFor = { null }) })
                }
            }
        }
        compose.runOnIdle { door.request(INVITE, gate.now()) }
        compose.settledUntil(5_000) { shown("guest-held") }
        assertTrue(compose.onAllNodes(hasText("Your invite opens once your home confirms this account’s limits.")).fetchSemanticsNodes().isNotEmpty())
        assertTrue("nothing is redeemed while held", asked.none { it.endsWith("/api/v1/redeem") })

        // the home says there is no limit: the held invite opens on its channels
        parental = json(200, """{"restricted":false,"allow_only":false,"version":1}""")
        kotlinx.coroutines.runBlocking { limit.refresh() }
        compose.settledUntil(10_000) { shown("guest-channel-ch1") }
        assertEquals(1, asked.count { it.endsWith("/api/v1/redeem") })
        assertTrue(!shown("guest-held"))

        // a guardian puts the sign-in under a limit: the guest space closes and says why
        parental = json(200, """{"restricted":true,"max_tier":9,"allow_only":false,"version":2}""")
        kotlinx.coroutines.runBlocking { limit.refresh() }
        compose.settledUntil(5_000) { shown("guest-refused") && !shown("guest-channel-ch1") }
        assertTrue(compose.onAllNodes(hasText("A parental limit is on for this device, so broadcasts can’t be watched here.")).fetchSemanticsNodes().isNotEmpty())
        compose.onNodeWithTag("guest-refused-close").performClick()
        compose.settledUntil(5_000) { !shown("guest-refused") }
        assertEquals(com.hamzalabs.maktaba.core.guest.GuestDoor.Shown.Shut, door.shown.value)

        // a link opened under the limit is refused at once, never redeemed
        compose.runOnIdle { door.request(INVITE, gate.now()) }
        compose.settledUntil(5_000) { shown("guest-refused") }
        assertEquals(1, asked.count { it.endsWith("/api/v1/redeem") })
    }

    @Test
    @Config(qualifiers = "ar-$PHONE")
    fun theRefusalReadsInArabic() {
        val door = com.hamzalabs.maktaba.core.guest.GuestDoor()
        val gate = com.hamzalabs.maktaba.core.guest.GuestGate { listOf(com.hamzalabs.maktaba.core.parental.KeptLimit.Mark("sig", limited = true)) }
        compose.setContent { MaktabaTheme(ThemeMode.Dark) { Surface { GuestDoorHost(door, gate, space(), open = { _, _ -> }) } } }
        compose.runOnIdle { door.request(INVITE, gate.now()) }
        compose.settledUntil(5_000) { shown("guest-refused") }
        assertTrue(compose.onAllNodes(hasText("تقييد أبوي مفعّل على هذا الجهاز، لذا لا يمكن مشاهدة البثوث هنا.")).fetchSemanticsNodes().isNotEmpty())
        assertTrue(asked.isEmpty())
    }

    private companion object {
        const val PHONE = "w411dp-h891dp-xxhdpi"
        const val INVITE = "mkbn1.dWktaW52aXRl.c2ln"
        const val PASS = """{"pass":"mkbg1.dWktcGFzcw.sig","publisher":"ui-pub","channels":["ch1","ch2"],"expires_at":"2030-01-01T00:00:00Z"}"""
        const val LINEUP = """{"publisher":{"id":"ui-pub","name":"Al-Noor"},"channels":[
            {"id":"ch1","name":"Quran","number":1,"state":"live","now":{"start":"2026-11-15T10:00:00Z","end":"2026-11-15T11:00:00Z","title":"Morning recitation","attribution":{"title":"Recitation","by":"Example Reciter","licence":"CC BY 4.0"}},"next":{"start":"2026-11-15T11:00:00Z","end":"2026-11-15T12:00:00Z","title":"Tafsir"}},
            {"id":"ch2","name":"Lectures","number":2,"state":"offline_slate","now":{"start":"2026-11-15T10:00:00Z","end":"2026-11-15T11:00:00Z","title":"Seerah"},"next":null}]}"""

        val options = RoborazziOptions(compareOptions = RoborazziOptions.CompareOptions(changeThreshold = 0.02f))
    }
}
