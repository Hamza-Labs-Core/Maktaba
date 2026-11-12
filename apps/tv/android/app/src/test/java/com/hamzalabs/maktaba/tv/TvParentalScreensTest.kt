package com.hamzalabs.maktaba.tv

import androidx.activity.ComponentActivity
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.platform.LocalLayoutDirection
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assertTextContains
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.junit4.v2.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performSemanticsAction
import androidx.compose.ui.test.performTextInput
import androidx.compose.ui.unit.LayoutDirection
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.hamzalabs.maktaba.core.MemoryTokens
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.MyParental
import com.hamzalabs.maktaba.core.api.MyTime
import com.hamzalabs.maktaba.core.api.SubjectKind
import com.hamzalabs.maktaba.core.api.TimeReason
import com.hamzalabs.maktaba.core.api.TimeRefusal
import com.hamzalabs.maktaba.core.jwt
import com.hamzalabs.maktaba.core.ui.ParentHelp
import com.hamzalabs.maktaba.tv.ui.screens.TvBlocked
import com.hamzalabs.maktaba.tv.ui.screens.TvNotNow
import com.hamzalabs.maktaba.tv.ui.screens.TvTimeOverlay
import com.hamzalabs.maktaba.tv.ui.screens.TvTimesUp
import com.hamzalabs.maktaba.tv.ui.theme.MaktabaTvTheme
import mockwebserver3.Dispatcher
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.RecordedRequest
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import java.time.Instant
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.CopyOnWriteArrayList

private const val TV = "w960dp-h540dp-xhdpi"

/**
 * Epic 57 on the TV's screens (stories 57.05, 57.06, 57.10, 57.15; T03's TV root), in English
 * and Arabic: the time left and its warning over the player, the time's-up screen with asking
 * for more time and a parent's PIN by remote, the blocked page and live TV's slate.
 */
@RunWith(AndroidJUnit4::class)
@Config(qualifiers = TV)
class TvParentalScreensTest {
    @get:Rule val compose = createAndroidComposeRule<ComponentActivity>()

    private lateinit var server: MockWebServer
    private val routes = ConcurrentHashMap<String, () -> MockResponse>()
    private val bodies = CopyOnWriteArrayList<Pair<String, String>>()
    private var direction: LayoutDirection? = null
    private val tomorrow = Instant.now().plusSeconds(3 * 86_400)

    private fun json(body: String, code: Int = 200) = MockResponse.Builder().code(code)
        .setHeader("Content-Type", if (code >= 400) "application/problem+json" else "application/json").body(body).build()

    @Before
    fun start() {
        server = MockWebServer()
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                val p = request.url.encodedPath
                bodies += p to (request.body?.utf8() ?: "")
                return routes[p]?.invoke() ?: MockResponse.Builder().code(404).build()
            }
        }
        server.start()
        routes["/api/me/parental/guardians"] = { json("""{"guardians":[{"id":"g1","username":"huda"}]}""") }
    }

    @After
    fun stop() = server.close()

    private fun client() = ApiClient(server.url("/"), MemoryTokens(jwt(System.currentTimeMillis() / 1000 + 86_400), "r"))

    private fun show(content: @Composable () -> Unit) {
        compose.setContent {
            MaktabaTvTheme {
                direction = LocalLayoutDirection.current
                content()
            }
        }
        compose.waitForIdle()
    }

    private fun shown(): List<String> =
        compose.onAllNodes(SemanticsMatcher("any") { true }, useUnmergedTree = true).fetchSemanticsNodes().flatMap { n ->
            listOfNotNull(n.config.getOrNull(SemanticsProperties.TestTag)) + n.config.getOrNull(SemanticsProperties.Text).orEmpty().map { it.text }
        }

    private fun until(what: String, check: () -> Boolean) {
        runCatching { compose.waitUntil(10_000) { compose.waitForIdle(); check() } }
        assertTrue("$what; asked ${bodies.map { it.first }}; showing ${shown()}", check())
    }

    private fun press(tag: String) = compose.onNodeWithTag(tag, useUnmergedTree = true).performSemanticsAction(SemanticsActions.OnClick)
    /** The bodies sent to [path] (a read has none). */
    private fun sent(path: String) = bodies.filter { it.first == path && it.second.isNotEmpty() }.map { it.second }
    private fun ownReads() = bodies.count { it.first == "/api/me/parental/requests" && it.second.isEmpty() }

    private fun own(status: String, kind: String = "video", subject: String = "v9") = json(
        """{"requests":[{"id":"r1","subject_kind":"$kind","subject_id":"$subject","status":"$status","created_at":"2026-11-11T19:00:00Z","expires_at":"2099-01-01T00:00:00Z"}]}""",
    )

    @Test
    fun theTimesUpScreenSaysWhyWhenAndWhereItStopped() {
        show { TvTimesUp(TimeRefusal(TimeReason.LIMIT_REACHED, tomorrow), positionMs = 725_000, help = null, onCheck = {}, onBack = {}) }
        compose.onNodeWithTag("parental-times-up-title").assertTextEquals("Time's up for today")
        compose.onNodeWithTag("parental-time-next").assertTextContains("You can watch again at ", substring = true)
        compose.onNodeWithTag("parental-time-saved").assertTextEquals("Your place in this video is saved at 12:05.")
        assertTrue(shown().toString(), "Ask a parent for more time" in shown())
        assertTrue("Check again" in shown())
    }

    @Test
    @Config(qualifiers = "ar-$TV")
    fun theTimesUpScreenInArabicIsMirrored() {
        show { TvTimesUp(TimeRefusal(TimeReason.OUTSIDE_SLOT, tomorrow), positionMs = 0, help = remember { ParentHelp(client(), SubjectKind.TIME) }, onCheck = {}) }
        assertEquals(LayoutDirection.Rtl, direction)
        compose.onNodeWithTag("parental-times-up-title").assertTextEquals("لم يحن وقت المشاهدة بعد")
        compose.onNodeWithTag("parental-time-next").assertTextContains("يمكنك المشاهدة مجددًا عند ", substring = true)
        val words = shown()
        assertTrue(words.toString(), "اطلب من أحد والديك وقتًا إضافيًا" in words)
        assertTrue(words.toString(), "اطلب وقتًا إضافيًا" in words)
        assertTrue(words.toString(), "تحقّق مجددًا" in words)
        assertFalse("parental-time-saved" in words)
    }

    @Test
    fun askingForMoreTimeByRemoteSendsTheMinutesAndWaits() {
        routes["/api/me/parental/requests"] = { json("""{"request":{"id":"r1","subject_kind":"time","subject_id":"60","minutes":60,"status":"pending"}}""", 201) }
        show { TvTimesUp(TimeRefusal(TimeReason.LIMIT_REACHED), 0, remember { ParentHelp(client(), SubjectKind.TIME) }, onCheck = {}) }
        press("parental-ask")
        until("the minutes are offered") { "parental-ask-60" in shown() }
        assertTrue(shown().toString(), shown().containsAll(listOf("15 minutes", "30 minutes", "1 hour")))
        press("parental-ask-60")
        until("the request waits") { "parental-ask-state" in shown() }
        assertEquals(listOf("""{"subject_kind":"time","subject_id":"60"}"""), sent("/api/me/parental/requests"))
        assertTrue(shown().any { it.startsWith("Waiting for a parent") })
        assertFalse("parental-ask" in shown())
    }

    @Test
    fun aParentsPinGivesMoreTimeAndChecksAgain() {
        routes["/api/me/parental/unlock"] = { json("""{"grant":{"id":"t1","minutes":15}}""") }
        var checked = 0
        show { TvTimesUp(TimeRefusal(TimeReason.LIMIT_REACHED), 0, remember { ParentHelp(client(), SubjectKind.TIME) }, onCheck = { checked++ }) }
        until("someone has a PIN") { "parental-pin" in shown() }
        press("parental-pin")
        until("the PIN panel") { "parental-pin-field" in shown() }
        press("parental-pin-minutes-15")
        compose.onNode(androidx.compose.ui.test.hasSetTextAction()).performTextInput("123456")
        compose.waitForIdle()
        press("parental-pin-submit")
        until("checked again") { checked == 1 }
        assertEquals(listOf("""{"guardian_id":"g1","pin":"123456","subject_kind":"time","subject_id":"15"}"""), sent("/api/me/parental/unlock"))
    }

    @Test
    fun aWrongPinIsSaid() {
        routes["/api/me/parental/unlock"] = { json("""{"type":"https://maktaba.tv/problems/forbidden","title":"no","status":403,"code":"pin_wrong"}""", 403) }
        show { TvTimesUp(TimeRefusal(TimeReason.LIMIT_REACHED), 0, remember { ParentHelp(client(), SubjectKind.TIME) }, onCheck = {}) }
        until("someone has a PIN") { "parental-pin" in shown() }
        press("parental-pin")
        until("the PIN panel") { "parental-pin-field" in shown() }
        compose.onNode(androidx.compose.ui.test.hasSetTextAction()).performTextInput("000000")
        compose.waitForIdle()
        press("parental-pin-submit")
        until("wrong") { "That PIN isn't right." in shown() }
    }

    @Test
    fun theBlockedPageAsksAParentAndOffersNothingForAMissingItem() {
        routes["/api/me/parental/requests"] = { json("""{"type":"https://maktaba.tv/problems/not-found","title":"Not found","status":404}""", 404) }
        show { TvBlocked(MyParental(restricted = true, maxTier = 9), remember { ParentHelp(client(), SubjectKind.VIDEO, "v9") }, onUnlocked = {}, onBack = {}) }
        compose.onNodeWithTag("parental-blocked-title").assertTextEquals("This isn't available")
        compose.onNodeWithTag("parental-blocked-reason").assertTextEquals("Your account is limited to 9+.")
        until("someone has a PIN") { "parental-pin" in shown() }
        press("parental-ask")
        // a hidden item is a missing one: nothing more is offered
        until("the buttons go") { "parental-ask" !in shown() && "parental-pin" !in shown() }
        assertEquals(listOf("""{"subject_kind":"video","subject_id":"v9"}"""), sent("/api/me/parental/requests"))
        assertFalse(shown().toString(), shown().any { "v9" in it })
    }

    @Test
    fun aRequestAlreadyWaitingIsFollowedAndAParentsYesOpensTheItem() {
        var status = "pending"
        routes["/api/me/parental/requests"] = { own(status) }
        var opened = 0
        show { TvBlocked(MyParental(restricted = true, maxTier = 9), remember { ParentHelp(client(), SubjectKind.VIDEO, "v9") }, onUnlocked = { opened++ }, onBack = {}) }
        until("the request waits") { "parental-ask-state" in shown() && "parental-ask" !in shown() }
        val read = ownReads()
        compose.mainClock.advanceTimeBy(15_000)
        until("read again after 15 s") { ownReads() > read }
        assertEquals(0, opened)
        // the parent approves from their own phone
        status = "approved"
        compose.mainClock.advanceTimeBy(15_000)
        until("opened") { opened == 1 }
        compose.mainClock.advanceTimeBy(60_000)
        compose.waitForIdle()
        assertEquals("once", 1, opened)
    }

    @Test
    fun aDenialIsSaidWithAskAgainAndAnEndedRequestOffersAskingAgain() {
        var status = "denied"
        routes["/api/me/parental/requests"] = { own(status, kind = "time", subject = "30") }
        show { TvTimesUp(TimeRefusal(TimeReason.LIMIT_REACHED), 0, remember { ParentHelp(client(), SubjectKind.TIME) }, onCheck = {}) }
        until("the denial, Ask again and the PIN") { "A parent said no to this request." in shown() && "Ask again" in shown() && "parental-pin" in shown() }
        // the TV comes back to the app after the request ended
        status = "expired"
        compose.activityRule.scenario.moveToState(androidx.lifecycle.Lifecycle.State.CREATED)
        compose.activityRule.scenario.moveToState(androidx.lifecycle.Lifecycle.State.RESUMED)
        until("asking is offered as at first") { "Ask for more time" in shown() && "parental-ask-state" !in shown() }
    }

    @Test
    fun theBlockedPageReadsAgainWhenTheTvComesBackToTheApp() {
        // a parent approves from their own phone while the TV shows another app (story 57.06)
        var returned = 0
        show { TvBlocked(MyParental(restricted = true, maxTier = 9), help = null, onUnlocked = {}, onBack = {}, onReturn = { returned++ }) }
        assertEquals("not when it first shows", 0, returned)
        compose.activityRule.scenario.moveToState(androidx.lifecycle.Lifecycle.State.STARTED)
        compose.activityRule.scenario.moveToState(androidx.lifecycle.Lifecycle.State.RESUMED)
        compose.waitForIdle()
        assertEquals(1, returned)
    }

    @Test
    fun aLockCountsDownOnScreenAndThenThePinCanBeTypedAgain() {
        routes["/api/me/parental/unlock"] = {
            MockResponse.Builder().code(429).setHeader("Content-Type", "application/problem+json").setHeader("Retry-After", "125")
                .body("""{"type":"https://maktaba.tv/problems/parental-pin-locked","title":"locked","status":429,"code":"pin_locked"}""").build()
        }
        show { TvBlocked(MyParental(restricted = true, maxTier = 9), remember { ParentHelp(client(), SubjectKind.VIDEO, "v9") }, onUnlocked = {}, onBack = {}) }
        until("someone has a PIN") { "parental-pin" in shown() }
        press("parental-pin")
        until("the PIN panel") { "parental-pin-field" in shown() }
        compose.onNode(androidx.compose.ui.test.hasSetTextAction()).performTextInput("000000")
        compose.waitForIdle()
        press("parental-pin-submit")
        until("locked") { "Too many tries. Try again in 2 minutes." in shown() }
        compose.mainClock.advanceTimeBy(10_000)
        until("the minutes go down") { "Too many tries. Try again in 1 minute." in shown() }
        compose.mainClock.advanceTimeBy(120_000)
        until("the lock clears by itself") { "parental-pin-state" !in shown() }
    }

    @Test
    fun theTimeIsNotAskedWhileTheTvShowsAnotherApp() {
        routes["/api/me/parental"] = { json("""{"restricted":true,"max_tier":9,"allow_only":false,"version":1}""") }
        routes["/api/me/parental/time"] = { json("""{"allowed":true,"limited":true,"remaining_seconds":3000}""") }
        val parental = com.hamzalabs.maktaba.core.parental.ParentalState(client())
        kotlinx.coroutines.runBlocking { parental.refresh() }
        fun asks() = bodies.count { it.first == "/api/me/parental/time" }
        show { com.hamzalabs.maktaba.tv.ui.screens.TvHomeTime(parental) }
        until("asked while Home shows") { asks() == 1 && "50 minutes left today" in shown() }
        compose.activityRule.scenario.moveToState(androidx.lifecycle.Lifecycle.State.CREATED)
        compose.mainClock.advanceTimeBy(120_000)
        org.robolectric.Shadows.shadowOf(android.os.Looper.getMainLooper()).idleFor(java.time.Duration.ofMinutes(2))
        compose.waitForIdle()
        assertEquals("no poll in standby", 1, asks())
        compose.activityRule.scenario.moveToState(androidx.lifecycle.Lifecycle.State.RESUMED)
        until("asked again in front") { asks() == 2 }
    }

    @Test
    fun aWarningNamesTheTimeReallyLeft() {
        show { TvTimeOverlay(MyTime(allowed = true, limited = true, remainingSeconds = 125), cannotCheck = false) }
        compose.onNodeWithTag("parental-time-warning").assertTextEquals("Watching ends in 2 minutes.")
    }

    @Test
    @Config(qualifiers = "ar-$TV")
    fun theBlockedPageAndTheSlateInArabic() {
        show { TvBlocked(MyParental(restricted = true, maxTier = 13), help = null, onUnlocked = {}, stopped = true, onBack = {}) }
        assertEquals(LayoutDirection.Rtl, direction)
        compose.onNodeWithTag("parental-blocked-title").assertTextEquals("هذا غير متاح")
        compose.onNodeWithTag("parental-stopped").assertTextEquals("لم يعد هذا المقطع متاحًا على هذا الحساب.")
        compose.onNodeWithTag("parental-blocked-reason").assertTextEquals("حسابك مقيَّد بـ \u200E+١٣.")
    }

    @Test
    fun theSlateSaysNotAvailableRightNowAndWhenItComesBack() {
        show { TvNotNow(tomorrow, onBack = {}) }
        assertTrue(shown().toString(), "Not available right now. This channel comes back when the next programme starts." in shown())
        compose.onNodeWithTag("live-parental-back").assertTextContains("Back at ", substring = true)
    }

    @Test
    fun thePlayerShowsTheTimeLeftTheWarningAndCannotCheck() {
        show { TvTimeOverlay(MyTime(allowed = true, limited = true, remainingSeconds = 290), cannotCheck = true) }
        compose.onNodeWithTag("parental-time-left").assertTextEquals("4 minutes left today")
        compose.onNodeWithTag("parental-time-warning").assertTextEquals("Watching ends in 5 minutes.")
        compose.onNodeWithTag("parental-cannot-check").assertTextEquals("Your home can't check your watching time right now. Try again in a moment.")
    }

    @Test
    @Config(qualifiers = "ar-$TV")
    fun theOneMinuteWarningInArabic() {
        show { TvTimeOverlay(MyTime(allowed = true, limited = true, remainingSeconds = 50), cannotCheck = false) }
        compose.onNodeWithTag("parental-time-warning").assertTextEquals("تنتهي المشاهدة بعد دقيقة واحدة.")
        compose.onNodeWithTag("parental-time-left").assertTextEquals("بقي أقل من دقيقة اليوم")
    }

    @Test
    fun nothingShowsAboveFifteenMinutes() {
        show { TvTimeOverlay(MyTime(allowed = true, limited = true, remainingSeconds = 2000), cannotCheck = false) }
        assertFalse(shown().toString(), shown().any { it.startsWith("parental-") })
    }
}
