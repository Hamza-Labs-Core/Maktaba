package com.hamzalabs.maktaba.desktop.watch

import androidx.compose.ui.test.ExperimentalTestApi
import androidx.compose.ui.test.assertIsEnabled
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTextInput
import androidx.compose.ui.test.runComposeUiTest
import com.hamzalabs.maktaba.core.api.MyTime
import com.hamzalabs.maktaba.desktop.InPlayer
import com.hamzalabs.maktaba.desktop.Lang
import com.hamzalabs.maktaba.desktop.TestHome
import org.junit.Assert.assertEquals
import org.junit.Test
import java.time.Instant

/**
 * Epic 57 story 57.15 (T05) on the desktop Player's screen: the time's-up screen in English and
 * Arabic, asking for more time, a parent's PIN, and the time left and warning while it plays.
 */
@OptIn(ExperimentalTestApi::class)
class TimesUpTest {
    private val up = TimeUp("limit_reached", Instant.parse("2026-11-12T04:00:00Z"), positionMs = 754_000)

    private fun TestHome.withPin() = answer("GET /api/me/parental/guardians", """{"guardians":[{"id":"g1","username":"huda"}]}""")

    @Test
    fun theScreenSaysWhyAndWhereItStopped() = TestHome().use { fake ->
        runComposeUiTest {
            setContent { InPlayer(fake.home()) { TimesUp(up, onCheck = {}) } }
            onNodeWithText("Time's up for today").assertExists()
            onNodeWithText("Your place in this video is saved at 12:34.").assertExists()
            onNodeWithText("Ask a parent for more time").assertExists()
            onNodeWithTag("parental-time-check").assertExists()
            assertEquals("no request button without the model", 0, onAllNodes(hasTestTag("parental-ask")).fetchSemanticsNodes().size)
        }
    }

    @Test
    fun theScreenInArabic() = TestHome().use { fake ->
        runComposeUiTest {
            setContent { InPlayer(fake.home(), Lang.Ar) { TimesUp(up.copy(reason = "outside_slot"), onCheck = {}, more = MoreTime(fake.client)) } }
            onNodeWithText("لم يحن وقت المشاهدة بعد").assertExists()
            onNodeWithText("اطلب من أحد والديك وقتًا إضافيًا").assertExists()
            onNodeWithText("اطلب وقتًا إضافيًا").assertExists()
            onNodeWithText("تحقّق مجددًا").assertExists()
        }
    }

    @Test
    fun askingForMoreTimeSendsTheMinutesAndWaits() = TestHome().use { fake ->
        fake.answer("POST /api/me/parental/requests", """{"request":{"id":"r1","subject_kind":"time","subject_id":"30","minutes":30,"status":"pending"}}""", 201)
        runComposeUiTest {
            setContent { InPlayer(fake.home()) { TimesUp(up, onCheck = {}, more = MoreTime(fake.client)) } }
            onNodeWithTag("parental-ask").performClick()
            onNodeWithText("15 minutes").assertExists()
            onNodeWithText("1 hour").assertExists()
            onNodeWithTag("parental-ask-30").performClick()
            waitUntil(timeoutMillis = 10_000) { onAllNodes(hasTestTag("parental-ask-state")).fetchSemanticsNodes().isNotEmpty() }
            assertEquals("""{"subject_kind":"time","subject_id":"30"}""", fake.asked("POST /api/me/parental/requests").single().body!!.utf8())
            assertEquals("asked: the button goes while it waits", 0, onAllNodes(hasTestTag("parental-ask")).fetchSemanticsNodes().size)
        }
    }

    @Test
    fun aMissingRouteOffersNothingMore() = TestHome().use { fake ->
        // an older home, or nothing to ask for: 404, as a missing thing
        runComposeUiTest {
            setContent { InPlayer(fake.home()) { TimesUp(up, onCheck = {}, more = MoreTime(fake.client)) } }
            onNodeWithTag("parental-ask").performClick()
            onNodeWithTag("parental-ask-15").performClick()
            waitUntil(timeoutMillis = 10_000) { onAllNodes(hasTestTag("parental-ask")).fetchSemanticsNodes().isEmpty() }
            onNodeWithTag("parental-time-check").assertExists()
        }
    }

    @Test
    fun aParentsPinGivesMoreTimeAndChecksAgain() = TestHome().use { fake ->
        fake.withPin()
        fake.answer("POST /api/me/parental/unlock", """{"grant":{"id":"t1","minutes":15}}""")
        var checked = 0
        runComposeUiTest {
            setContent { InPlayer(fake.home()) { TimesUp(up, onCheck = { checked++ }, more = MoreTime(fake.client)) } }
            waitUntil(timeoutMillis = 10_000) { onAllNodes(hasTestTag("parental-pin")).fetchSemanticsNodes().isNotEmpty() }
            onNodeWithTag("parental-pin").performClick()
            onNodeWithTag("parental-pin-minutes-15").performClick()
            onNodeWithTag("parental-pin-field").performTextInput("123456")
            onNodeWithTag("parental-pin-submit").performClick()
            waitUntil(timeoutMillis = 10_000) { checked == 1 }
            assertEquals(
                """{"guardian_id":"g1","pin":"123456","subject_kind":"time","subject_id":"15"}""",
                fake.asked("POST /api/me/parental/unlock").single().body!!.utf8(),
            )
        }
    }

    @Test
    fun aWrongPinIsSaid() = TestHome().use { fake ->
        fake.withPin()
        fake.answer("POST /api/me/parental/unlock", """{"title":"no","status":403,"code":"pin_wrong"}""", 403)
        runComposeUiTest {
            setContent { InPlayer(fake.home()) { TimesUp(up, onCheck = {}, more = MoreTime(fake.client)) } }
            waitUntil(timeoutMillis = 10_000) { onAllNodes(hasTestTag("parental-pin")).fetchSemanticsNodes().isNotEmpty() }
            onNodeWithTag("parental-pin").performClick()
            onNodeWithTag("parental-pin-field").performTextInput("000000")
            onNodeWithTag("parental-pin-submit").performClick()
            waitUntil(timeoutMillis = 10_000) { onAllNodes(hasTestTag("parental-pin-state")).fetchSemanticsNodes().isNotEmpty() }
            onNodeWithText("That PIN isn't right.").assertExists()
        }
    }

    private fun own(status: String) =
        """{"requests":[{"id":"r1","subject_kind":"time","subject_id":"30","minutes":30,"status":"$status","created_at":"2026-11-11T19:00:00Z","expires_at":"2099-01-01T00:00:00Z"}]}"""

    @Test
    fun aRequestAlreadyWaitingIsFollowedAndAParentsYesChecksAgainOnce() = TestHome().use { fake ->
        val status = java.util.concurrent.atomic.AtomicReference("pending")
        fake.answer("GET /api/me/parental/requests") { TestHome.json(own(status.get())) }
        var checked = 0
        runComposeUiTest {
            setContent { InPlayer(fake.home()) { TimesUp(up, onCheck = { checked++ }, more = MoreTime(fake.client)) } }
            // asked earlier: the home says it still waits, and nothing is asked for again
            waitUntil(timeoutMillis = 10_000) { onAllNodes(hasTestTag("parental-ask-state")).fetchSemanticsNodes().isNotEmpty() }
            assertEquals(0, onAllNodes(hasTestTag("parental-ask")).fetchSemanticsNodes().size)
            val read = fake.asked("GET /api/me/parental/requests").size
            mainClock.advanceTimeBy(15_000)
            waitUntil(timeoutMillis = 10_000) { fake.asked("GET /api/me/parental/requests").size > read }
            assertEquals(0, checked)
            // the parent approves from their own device
            status.set("approved")
            mainClock.advanceTimeBy(15_000)
            waitUntil(timeoutMillis = 10_000) { checked == 1 }
            mainClock.advanceTimeBy(60_000)
            waitForIdle()
            assertEquals("once", 1, checked)
            assertEquals("nothing was sent for the child", 0, fake.asked("POST /api/me/parental/requests").size)
        }
    }

    @Test
    fun aDenialIsSaidWithAskAgainInBothLanguages() = TestHome().use { fake ->
        fake.withPin()
        fake.answer("GET /api/me/parental/requests", own("denied"))
        runComposeUiTest {
            setContent { InPlayer(fake.home()) { TimesUp(up, onCheck = {}, more = MoreTime(fake.client)) } }
            waitUntil(timeoutMillis = 10_000) { onAllNodes(hasTestTag("parental-ask-state")).fetchSemanticsNodes().isNotEmpty() }
            onNodeWithText("A parent said no to this request.").assertExists()
            onNodeWithText("Ask again").assertExists()
            waitUntil(timeoutMillis = 10_000) { onAllNodes(hasTestTag("parental-pin")).fetchSemanticsNodes().isNotEmpty() }
        }
        runComposeUiTest {
            setContent { InPlayer(fake.home(), Lang.Ar) { TimesUp(up, onCheck = {}, more = MoreTime(fake.client)) } }
            waitUntil(timeoutMillis = 10_000) { onAllNodes(hasTestTag("parental-ask-state")).fetchSemanticsNodes().isNotEmpty() }
            onNodeWithText("رفض أحد الوالدين هذا الطلب.").assertExists()
            onNodeWithText("اطلب مجددًا").assertExists()
        }
    }

    @Test
    fun anEndedRequestOffersAskingAgain() = TestHome().use { fake ->
        fake.answer("GET /api/me/parental/requests", own("expired"))
        runComposeUiTest {
            setContent { InPlayer(fake.home()) { TimesUp(up, onCheck = {}, more = MoreTime(fake.client)) } }
            waitUntil(timeoutMillis = 10_000) { fake.asked("GET /api/me/parental/requests").isNotEmpty() }
            waitForIdle()
            onNodeWithText("Ask for more time").assertExists()
            assertEquals(0, onAllNodes(hasTestTag("parental-ask-state")).fetchSemanticsNodes().size)
        }
    }

    @Test
    fun aLockCountsDownOnScreenAndThenThePinCanBeTypedAgain() = TestHome().use { fake ->
        fake.withPin()
        fake.answer("POST /api/me/parental/unlock") {
            mockwebserver3.MockResponse.Builder().code(429).setHeader("Content-Type", "application/problem+json").setHeader("Retry-After", "2")
                .body("""{"title":"locked","status":429,"code":"pin_locked"}""").build()
        }
        runComposeUiTest {
            setContent { InPlayer(fake.home()) { TimesUp(up, onCheck = {}, more = MoreTime(fake.client)) } }
            waitUntil(timeoutMillis = 10_000) { onAllNodes(hasTestTag("parental-pin")).fetchSemanticsNodes().isNotEmpty() }
            onNodeWithTag("parental-pin").performClick()
            onNodeWithTag("parental-pin-field").performTextInput("000000")
            onNodeWithTag("parental-pin-submit").performClick()
            waitUntil(timeoutMillis = 10_000) { onAllNodes(hasTestTag("parental-pin-state")).fetchSemanticsNodes().isNotEmpty() }
            onNodeWithText("Too many tries. Try again in 1 minute.").assertExists()
            // the home's two seconds pass: the lock is gone from the screen, and a PIN can be sent again
            waitUntil(timeoutMillis = 20_000) { onAllNodes(hasTestTag("parental-pin-state")).fetchSemanticsNodes().isEmpty() }
            onNodeWithTag("parental-pin-field").performTextInput("111111")
            onNodeWithTag("parental-pin-submit").assertIsEnabled()
        }
    }

    @Test
    fun aWarningNamesTheTimeReallyLeft() = TestHome().use { fake ->
        runComposeUiTest {
            // opened with two minutes left: under the five-minute line, but it says two minutes
            setContent { InPlayer(fake.home()) { TimeNotes(MyTime(allowed = true, limited = true, remainingSeconds = 125), cannotCheck = false) } }
            onNodeWithText("Watching ends in 2 minutes.").assertExists()
        }
    }

    @Test
    fun theTimeLeftTheWarningAndCannotCheckWhileItPlays() = TestHome().use { fake ->
        runComposeUiTest {
            setContent { InPlayer(fake.home()) { TimeNotes(MyTime(allowed = true, limited = true, remainingSeconds = 290), cannotCheck = true) } }
            onNodeWithText("4 minutes left today").assertExists()
            onNodeWithText("Watching ends in 5 minutes.").assertExists()
            onNodeWithText("Your home can't check your watching time right now. Try again in a moment.").assertExists()
        }
    }

    @Test
    fun nothingShowsAboveFifteenMinutes() = TestHome().use { fake ->
        runComposeUiTest {
            setContent { InPlayer(fake.home(), Lang.Ar) { TimeNotes(MyTime(allowed = true, limited = true, remainingSeconds = 2_000), cannotCheck = false) } }
            assertEquals(0, onAllNodes(hasTestTag("parental-time-left")).fetchSemanticsNodes().size)
            assertEquals(0, onAllNodes(hasTestTag("parental-time-warning")).fetchSemanticsNodes().size)
        }
    }
}
