package com.hamzalabs.maktaba.player.parental

import androidx.activity.ComponentActivity
import androidx.compose.material3.Surface
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.platform.LocalLayoutDirection
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertIsEnabled
import androidx.compose.ui.test.assertIsNotEnabled
import androidx.compose.ui.test.assertTextContains
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.junit4.v2.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTextInput
import androidx.compose.ui.unit.LayoutDirection
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.hamzalabs.maktaba.core.api.MyParental
import com.hamzalabs.maktaba.core.api.MyTime
import com.hamzalabs.maktaba.core.api.SubjectKind
import com.hamzalabs.maktaba.core.api.TimeReason
import com.hamzalabs.maktaba.core.api.TimeRefusal
import com.hamzalabs.maktaba.core.ui.BlockedPage
import com.hamzalabs.maktaba.core.ui.HomeTimeLine
import com.hamzalabs.maktaba.core.ui.MaktabaTheme
import com.hamzalabs.maktaba.core.ui.NotNowSlate
import com.hamzalabs.maktaba.core.ui.ParentHelp
import com.hamzalabs.maktaba.core.ui.ThemeMode
import com.hamzalabs.maktaba.core.ui.TimeLeftLine
import com.hamzalabs.maktaba.core.ui.TimeWarning
import com.hamzalabs.maktaba.core.ui.TimesUpScreen
import com.hamzalabs.maktaba.player.harness.FakeHome
import com.hamzalabs.maktaba.player.harness.NoStall
import com.hamzalabs.maktaba.player.harness.settled
import com.hamzalabs.maktaba.player.harness.shown
import mockwebserver3.MockResponse
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import java.time.Instant

private const val PHONE = "w411dp-h891dp-xxhdpi"
private const val OWN = "/api/me/parental/requests"

/**
 * Epic 57 on the phone's screens (stories 57.05, 57.06, 57.10, 57.15; T03's phone root), in
 * English and Arabic: the time left and its warnings, the time's-up screen with asking for more
 * time and the parent PIN, the blocked page, and live TV's slate, against the fake home.
 */
@RunWith(AndroidJUnit4::class)
@Config(qualifiers = PHONE)
class ParentalScreensTest {
    @get:Rule(order = NoStall.FIRST) val noStall = NoStall()
    @get:Rule(order = 0) val home = FakeHome()
    @get:Rule(order = 1) val compose = createAndroidComposeRule<ComponentActivity>()

    private var direction: LayoutDirection? = null
    private val limited = MyParental(restricted = true, maxTier = 9)

    @Before
    fun routes() {
        compose.registerIdlingResource(home.idling)
        home.answer("/api/me/parental/guardians") { FakeHome.json("""{"guardians":[{"id":"g1","username":"huda"}]}""") }
    }

    private fun show(content: @Composable () -> Unit) {
        compose.setContent {
            MaktabaTheme(ThemeMode.Dark) {
                direction = LocalLayoutDirection.current
                Surface { content() }
            }
        }
        compose.waitForIdle()
    }

    private fun problem(status: Int, code: String, more: String = "") = MockResponse.Builder().code(status)
        .setHeader("Content-Type", "application/problem+json")
        .apply { if (status == 429) setHeader("Retry-After", "900") }
        .body("""{"type":"https://maktaba.tv/problems/x","title":"refused","status":$status,"code":"$code"$more}""").build()

    /** What was sent to [path] (the reads of a child's own requests are counted by [ownReads]). */
    private fun requests(path: String) = home.requests.filter { it.url.encodedPath == path && !(it.method == "GET" && path == OWN) }
    private fun ownReads() = home.requests.count { it.url.encodedPath == OWN && it.method == "GET" }

    private fun own(status: String, kind: String = "video", subject: String = "v9") = FakeHome.json(
        """{"requests":[{"id":"r1","subject_kind":"$kind","subject_id":"$subject","status":"$status","created_at":"2026-11-11T19:00:00Z","expires_at":"2099-01-01T00:00:00Z"}]}""",
    )
    private val tomorrow = Instant.now().plusSeconds(3 * 86_400)

    // ---- the time left and the warnings ----

    @Test
    fun theTimeLeftShowsFromFifteenMinutesAndTheWarningAtFive() {
        show {
            androidx.compose.foundation.layout.Column {
                TimeLeftLine(MyTime(allowed = true, limited = true, remainingSeconds = 290))
                TimeWarning(MyTime(allowed = true, limited = true, remainingSeconds = 290))
            }
        }
        compose.onNodeWithTag("parental-time-left").assertTextEquals("4 minutes left today")
        compose.onNodeWithTag("parental-time-warning").assertTextEquals("Watching ends in 5 minutes.")
    }

    @Test
    @Config(qualifiers = "ar-$PHONE")
    fun theOneMinuteWarningInArabic() {
        show {
            androidx.compose.foundation.layout.Column {
                TimeLeftLine(MyTime(allowed = true, limited = true, remainingSeconds = 55))
                TimeWarning(MyTime(allowed = true, limited = true, remainingSeconds = 55))
            }
        }
        assertEquals(LayoutDirection.Rtl, direction)
        compose.onNodeWithTag("parental-time-left").assertTextEquals("بقي أقل من دقيقة اليوم")
        compose.onNodeWithTag("parental-time-warning").assertTextEquals("تنتهي المشاهدة بعد دقيقة واحدة.")
    }

    @Test
    fun nothingShowsAboveFifteenMinutesOrWithNoRule() {
        show {
            androidx.compose.foundation.layout.Column {
                TimeLeftLine(MyTime(allowed = true, limited = true, remainingSeconds = 2000))
                TimeWarning(MyTime(allowed = true, limited = true, remainingSeconds = 2000))
                TimeLeftLine(MyTime(allowed = true, limited = false, remainingSeconds = null))
                HomeTimeLine(MyTime(allowed = true, limited = false))
            }
        }
        assertFalse(compose.shown().toString(), compose.shown().any { it.startsWith("parental-") })
    }

    @Test
    fun homeSaysTheTimeLeftWheneverARuleCoversNowAndWhenItIsOver() {
        val time = androidx.compose.runtime.mutableStateOf(MyTime(allowed = true, limited = true, remainingSeconds = 3900))
        show { HomeTimeLine(time.value) }
        compose.onNodeWithTag("parental-time-left").assertTextEquals("1 hour and 5 minutes left today")
        time.value = MyTime(allowed = false, limited = true, reason = TimeReason.LIMIT_REACHED, nextAllowedAt = null)
        compose.waitForIdle()
        compose.onNodeWithTag("parental-home-over").assertTextEquals("Time's up for today")
    }

    @Test
    fun aWarningNamesTheTimeReallyLeft() {
        // opened with two minutes left: under the five-minute line, but it says two minutes
        show { TimeWarning(MyTime(allowed = true, limited = true, remainingSeconds = 125)) }
        compose.onNodeWithTag("parental-time-warning").assertTextEquals("Watching ends in 2 minutes.")
    }

    // ---- the time poll (only while the app is in front) ----

    @Test
    fun theTimeIsNotAskedWhileTheAppIsInTheBackground() {
        home.answer("/api/me/parental") { FakeHome.json("""{"restricted":true,"max_tier":9,"allow_only":false,"version":1}""") }
        home.answer("/api/me/parental/time") { FakeHome.json("""{"allowed":true,"limited":true,"remaining_seconds":3000}""") }
        val parental = com.hamzalabs.maktaba.core.parental.ParentalState(home.home.client)
        kotlinx.coroutines.runBlocking { parental.refresh() }
        show { com.hamzalabs.maktaba.core.ui.FollowTimeWhileShown(parental, on = true) }
        compose.settled(home, "asked while shown") { requests("/api/me/parental/time").size == 1 }
        // another app in front: two minutes pass and nothing is asked
        compose.activityRule.scenario.moveToState(androidx.lifecycle.Lifecycle.State.CREATED)
        compose.mainClock.advanceTimeBy(120_000)
        org.robolectric.Shadows.shadowOf(android.os.Looper.getMainLooper()).idleFor(java.time.Duration.ofMinutes(2))
        compose.waitForIdle()
        assertEquals("no poll in the background", 1, requests("/api/me/parental/time").size)
        // back in front: asked at once
        compose.activityRule.scenario.moveToState(androidx.lifecycle.Lifecycle.State.RESUMED)
        compose.settled(home, "asked again in front") { requests("/api/me/parental/time").size == 2 }
    }

    // ---- the time's-up screen ----

    @Test
    fun theTimesUpScreenSaysWhyWhenAndWhereItStopped() {
        show {
            TimesUpScreen(TimeRefusal(TimeReason.LIMIT_REACHED, tomorrow), positionMs = 725_000, help = null, onCheck = {})
        }
        compose.onNodeWithTag("parental-times-up-title").assertTextEquals("Time's up for today")
        compose.onNodeWithTag("parental-time-next").assertTextContains("You can watch again at ", substring = true)
        compose.onNodeWithTag("parental-time-saved").assertTextEquals("Your place in this video is saved at 12:05.")
        compose.onNodeWithText("Ask a parent for more time").assertIsDisplayed()
        compose.onNodeWithTag("parental-time-check").assertIsDisplayed()
    }

    @Test
    @Config(qualifiers = "ar-$PHONE")
    fun theTimesUpScreenInArabicIsMirrored() {
        show {
            TimesUpScreen(TimeRefusal(TimeReason.OUTSIDE_SLOT, tomorrow), positionMs = 0, help = remember { ParentHelp(home.home.client, SubjectKind.TIME) }, onCheck = {})
        }
        assertEquals(LayoutDirection.Rtl, direction)
        compose.onNodeWithTag("parental-times-up-title").assertTextEquals("لم يحن وقت المشاهدة بعد")
        compose.onNodeWithTag("parental-time-next").assertTextContains("يمكنك المشاهدة مجددًا عند ", substring = true)
        compose.onNodeWithText("اطلب من أحد والديك وقتًا إضافيًا").assertIsDisplayed()
        compose.onNodeWithTag("parental-ask").assertTextEquals("اطلب وقتًا إضافيًا")
        compose.onNodeWithTag("parental-time-check").assertTextEquals("تحقّق مجددًا")
        assertFalse("no saved place before a second played", "parental-time-saved" in compose.shown())
    }

    @Test
    fun askingForMoreTimeSendsTheMinutesAndWaits() {
        home.answer("/api/me/parental/requests") {
            FakeHome.json("""{"request":{"id":"r1","subject_kind":"time","subject_id":"30","minutes":30,"status":"pending"}}""", code = 201)
        }
        show { TimesUpScreen(TimeRefusal(TimeReason.LIMIT_REACHED), 0, remember { ParentHelp(home.home.client, SubjectKind.TIME) }, onCheck = {}) }
        compose.onNodeWithTag("parental-ask").performClick()
        compose.onNodeWithTag("parental-ask-15").assertTextEquals("15 minutes")
        compose.onNodeWithTag("parental-ask-60").assertTextEquals("1 hour")
        compose.onNodeWithTag("parental-ask-30").performClick()
        compose.settled(home, "the request waits") { "parental-ask-state" in compose.shown() }
        assertEquals("""{"subject_kind":"time","subject_id":"30"}""", requests("/api/me/parental/requests").single().body!!.utf8())
        compose.onNodeWithTag("parental-ask-state").assertTextContains("Waiting for a parent", substring = true)
        assertFalse("asked once: the button is gone while it waits", "parental-ask" in compose.shown())
    }

    @Test
    fun tooManyRequestsWaitingIsSaid() {
        home.answer("/api/me/parental/requests") { problem(429, "too_many_requests") }
        show { TimesUpScreen(TimeRefusal(TimeReason.LIMIT_REACHED), 0, remember { ParentHelp(home.home.client, SubjectKind.TIME) }, onCheck = {}) }
        compose.onNodeWithTag("parental-ask").performClick()
        compose.onNodeWithTag("parental-ask-15").performClick()
        compose.settled(home, "said") { "parental-ask-state" in compose.shown() }
        compose.onNodeWithTag("parental-ask-state").assertTextEquals("You have too many requests waiting.")
    }

    @Test
    fun aParentsPinGivesMoreTimeAndChecksAgain() {
        home.answer("/api/me/parental/unlock") { FakeHome.json("""{"grant":{"id":"t1","minutes":15,"used_seconds":0}}""") }
        var checked = 0
        show { TimesUpScreen(TimeRefusal(TimeReason.LIMIT_REACHED), 0, remember { ParentHelp(home.home.client, SubjectKind.TIME) }, onCheck = { checked++ }) }
        compose.settled(home, "someone has a PIN") { "parental-pin" in compose.shown() }
        compose.onNodeWithTag("parental-pin").performClick()
        compose.onNodeWithTag("parental-pin-submit").assertIsNotEnabled()
        compose.onNodeWithTag("parental-pin-minutes-15").performClick()
        compose.onNodeWithTag("parental-pin-field").performTextInput("12a3456789")
        compose.onNodeWithTag("parental-pin-submit").assertIsEnabled().performClick()
        compose.settled(home, "unlocked") { "parental-pin-done" in compose.shown() }
        assertEquals(
            "six digits only, the minutes as text",
            """{"guardian_id":"g1","pin":"123456","subject_kind":"time","subject_id":"15"}""",
            requests("/api/me/parental/unlock").single().body!!.utf8(),
        )
        compose.onNodeWithTag("parental-pin-state").assertTextEquals("Unlocked for today.")
        compose.onNodeWithTag("parental-pin-done").performClick()
        compose.waitForIdle()
        assertEquals("the video is asked for again", 1, checked)
    }

    @Test
    fun aWrongPinAndALockAreSaid() {
        home.answer("/api/me/parental/unlock") { problem(403, "pin_wrong") }
        show { TimesUpScreen(TimeRefusal(TimeReason.LIMIT_REACHED), 0, remember { ParentHelp(home.home.client, SubjectKind.TIME) }, onCheck = {}) }
        compose.settled(home, "someone has a PIN") { "parental-pin" in compose.shown() }
        compose.onNodeWithTag("parental-pin").performClick()
        compose.onNodeWithTag("parental-pin-field").performTextInput("000000")
        compose.onNodeWithTag("parental-pin-submit").performClick()
        compose.settled(home, "wrong") { "parental-pin-state" in compose.shown() }
        compose.onNodeWithTag("parental-pin-state").assertTextEquals("That PIN isn't right.")

        home.answer("/api/me/parental/unlock") { problem(429, "pin_locked", ""","retry_after":900""") }
        compose.onNodeWithTag("parental-pin-field").performTextInput("111111")
        compose.onNodeWithTag("parental-pin-submit").performClick()
        compose.settled(home, "locked") { compose.shown().any { it.startsWith("Too many tries") } }
        compose.onNodeWithTag("parental-pin-state").assertTextEquals("Too many tries. Try again in 15 minutes.")
        compose.onNodeWithTag("parental-pin-submit").assertIsNotEnabled()
    }

    @Test
    fun withNobodyHoldingAPinTheButtonIsNotOffered() {
        home.answer("/api/me/parental/guardians") { FakeHome.json("""{"guardians":[]}""") }
        show { TimesUpScreen(TimeRefusal(TimeReason.LIMIT_REACHED), 0, remember { ParentHelp(home.home.client, SubjectKind.TIME) }, onCheck = {}) }
        compose.settled(home, "asked who has a PIN") { requests("/api/me/parental/guardians").isNotEmpty() }
        compose.waitForIdle()
        assertFalse("parental-pin" in compose.shown())
        assertTrue("parental-ask" in compose.shown())
    }

    // ---- the blocked page ----

    @Test
    fun theBlockedPageNamesTheLimitAndAsksAParentForTheItem() {
        home.answer("/api/me/parental/requests") {
            FakeHome.json("""{"request":{"id":"r2","subject_kind":"video","subject_id":"v9","status":"pending"}}""", code = 201)
        }
        show { BlockedPage(limited, remember { ParentHelp(home.home.client, SubjectKind.VIDEO, "v9") }, onUnlocked = {}) }
        compose.onNodeWithTag("parental-blocked-title").assertTextEquals("This isn't available")
        compose.onNodeWithTag("parental-blocked-reason").assertTextEquals("Your account is limited to 9+.")
        compose.onNodeWithTag("parental-ask").assertTextEquals("Ask a parent").performClick()
        compose.settled(home, "the request waits") { "parental-ask-state" in compose.shown() }
        assertEquals("""{"subject_kind":"video","subject_id":"v9"}""", requests("/api/me/parental/requests").single().body!!.utf8())
        // nothing about the video itself is on the page: the home sent none
        assertFalse(compose.shown().toString(), compose.shown().any { "v9" in it })
    }

    @Test
    fun theBlockedPageReadsAgainWhenTheAppComesBackToTheFront() {
        // a parent approves from their own phone while the child's app is away (story 57.06)
        var returned = 0
        show { BlockedPage(limited, help = null, onUnlocked = {}, onReturn = { returned++ }) }
        assertEquals("not when it first shows", 0, returned)
        compose.activityRule.scenario.moveToState(androidx.lifecycle.Lifecycle.State.STARTED)
        compose.activityRule.scenario.moveToState(androidx.lifecycle.Lifecycle.State.RESUMED)
        compose.waitForIdle()
        assertEquals(1, returned)
    }

    @Test
    fun aLockCountsDownOnScreenAndThenThePinCanBeTypedAgain() {
        home.answer("/api/me/parental/unlock") {
            MockResponse.Builder().code(429).setHeader("Content-Type", "application/problem+json").setHeader("Retry-After", "125")
                .body("""{"type":"https://maktaba.tv/problems/parental-pin-locked","title":"locked","status":429,"code":"pin_locked"}""").build()
        }
        show { BlockedPage(limited, remember { ParentHelp(home.home.client, SubjectKind.VIDEO, "v9") }, onUnlocked = {}) }
        compose.settled(home, "someone has a PIN") { "parental-pin" in compose.shown() }
        compose.onNodeWithTag("parental-pin").performClick()
        compose.onNodeWithTag("parental-pin-field").performTextInput("000000")
        compose.onNodeWithTag("parental-pin-submit").performClick()
        compose.settled(home, "locked") { "parental-pin-state" in compose.shown() }
        compose.onNodeWithTag("parental-pin-state").assertTextEquals("Too many tries. Try again in 2 minutes.")
        // the minutes shown go down
        compose.mainClock.advanceTimeBy(10_000)
        compose.waitForIdle()
        compose.onNodeWithTag("parental-pin-state").assertTextEquals("Too many tries. Try again in 1 minute.")
        // and at zero the lock is gone from the screen: a PIN can be sent again
        compose.mainClock.advanceTimeBy(120_000)
        compose.waitForIdle()
        assertFalse("the lock's line is gone", "parental-pin-state" in compose.shown())
        compose.onNodeWithTag("parental-pin-field").performTextInput("111111")
        compose.onNodeWithTag("parental-pin-submit").assertIsEnabled()
    }

    @Test
    fun aRequestAlreadyWaitingIsFollowedAndAParentsYesOpensTheItem() {
        // asked earlier (another day, another device): the home says it still waits
        var status = "pending"
        home.answer(OWN) { own(status) }
        var opened = 0
        show { BlockedPage(limited, remember { ParentHelp(home.home.client, SubjectKind.VIDEO, "v9") }, onUnlocked = { opened++ }) }
        compose.settled(home, "the request waits") { "parental-ask-state" in compose.shown() }
        compose.onNodeWithTag("parental-ask-state").assertTextContains("Waiting for a parent", substring = true)
        assertFalse("no Ask a parent while one waits", "parental-ask" in compose.shown())
        val read = ownReads()
        // fifteen seconds on: read again; still waiting
        compose.mainClock.advanceTimeBy(15_000)
        compose.settled(home, "read again") { ownReads() > read }
        assertEquals(0, opened)
        // the parent approves from their own phone
        status = "approved"
        compose.mainClock.advanceTimeBy(15_000)
        compose.settled(home, "opened") { opened == 1 }
        // once: nothing opens again and nothing more is followed
        val after = ownReads()
        compose.mainClock.advanceTimeBy(60_000)
        compose.waitForIdle()
        assertEquals(1, opened)
        assertTrue("not read every 15 s with nothing waiting", ownReads() <= after + 1)
    }

    @Test
    fun aDenialIsSaidWithAskAgainAndThePin() {
        home.answer(OWN) { own("denied") }
        show { BlockedPage(limited, remember { ParentHelp(home.home.client, SubjectKind.VIDEO, "v9") }, onUnlocked = {}) }
        compose.settled(home, "the denial") { "parental-ask-state" in compose.shown() && "parental-pin" in compose.shown() }
        compose.onNodeWithTag("parental-ask-state").assertTextEquals("A parent said no to this request.")
        compose.onNodeWithTag("parental-ask").assertTextEquals("Ask again")
    }

    @Test
    @Config(qualifiers = "ar-$PHONE")
    fun aDenialInArabicAndAnEndedRequestOffersAskingAgain() {
        var status = "denied"
        home.answer(OWN) { own(status, kind = "time", subject = "30") }
        show { TimesUpScreen(TimeRefusal(TimeReason.LIMIT_REACHED, null), 0, remember { ParentHelp(home.home.client, SubjectKind.TIME) }, onCheck = {}) }
        compose.settled(home, "the denial") { "parental-ask-state" in compose.shown() }
        compose.onNodeWithTag("parental-ask-state").assertTextEquals("رفض أحد الوالدين هذا الطلب.")
        compose.onNodeWithTag("parental-ask").assertTextEquals("اطلب مجددًا")
        // the app comes back to the front after the request ended: asking is offered as at first
        status = "expired"
        compose.activityRule.scenario.moveToState(androidx.lifecycle.Lifecycle.State.CREATED)
        compose.activityRule.scenario.moveToState(androidx.lifecycle.Lifecycle.State.RESUMED)
        compose.settled(home, "the ask button again") { "parental-ask-state" !in compose.shown() }
        compose.onNodeWithTag("parental-ask").assertTextEquals("اطلب وقتًا إضافيًا")
    }

    @Test
    fun aHiddenItemIsAMissingOneAndNothingMoreIsOffered() {
        home.answer("/api/me/parental/requests") { problem(404, "not_found") }
        show { BlockedPage(limited, remember { ParentHelp(home.home.client, SubjectKind.VIDEO, "v9") }, onUnlocked = {}) }
        compose.settled(home, "someone has a PIN") { "parental-pin" in compose.shown() }
        compose.onNodeWithTag("parental-ask").performClick()
        compose.settled(home, "the buttons go") { "parental-ask" !in compose.shown() }
        assertFalse("no PIN either", "parental-pin" in compose.shown())
        assertFalse("and no request state", "parental-ask-state" in compose.shown())
        compose.onNodeWithTag("parental-blocked-title").assertIsDisplayed()
    }

    @Test
    @Config(qualifiers = "ar-$PHONE")
    fun theBlockedPageInArabicForALimitedDevice() {
        show {
            BlockedPage(MyParental(restricted = false, maxTier = 13, deviceLimit = 13), help = null, onUnlocked = {}, stopped = true)
        }
        assertEquals(LayoutDirection.Rtl, direction)
        compose.onNodeWithTag("parental-blocked-title").assertTextEquals("هذا غير متاح")
        compose.onNodeWithTag("parental-stopped").assertTextEquals("لم يعد هذا المقطع متاحًا على هذا الحساب.")
        // the plus sign stays on the number's left: a left-to-right mark leads the label
        compose.onNodeWithTag("parental-blocked-reason").assertTextEquals("هذا الجهاز مقيَّد بـ \u200E+١٣.")
        assertFalse("nothing to ask without a help", "parental-ask" in compose.shown())
    }

    @Test
    fun aParentsPinOpensTheItemForToday() {
        home.answer("/api/me/parental/unlock") { FakeHome.json("""{"rule":{"id":"x","effect":"allow","subject_kind":"video","subject_id":"v9"}}""") }
        var unlocked = 0
        show { BlockedPage(limited, remember { ParentHelp(home.home.client, SubjectKind.VIDEO, "v9") }, onUnlocked = { unlocked++ }) }
        compose.settled(home, "someone has a PIN") { "parental-pin" in compose.shown() }
        compose.onNodeWithTag("parental-pin").performClick()
        assertFalse("no minutes to choose for an item", "parental-pin-minutes-15" in compose.shown())
        compose.onNodeWithTag("parental-pin-field").performTextInput("654321")
        compose.onNodeWithTag("parental-pin-submit").performClick()
        compose.settled(home, "unlocked") { "parental-pin-done" in compose.shown() }
        assertEquals(
            """{"guardian_id":"g1","pin":"654321","subject_kind":"video","subject_id":"v9"}""",
            requests("/api/me/parental/unlock").single().body!!.utf8(),
        )
        compose.onNodeWithTag("parental-pin-done").performClick()
        compose.waitForIdle()
        assertEquals(1, unlocked)
    }

    // ---- live TV ----

    @Test
    fun theSlateSaysNotAvailableRightNowAndWhenItComesBack() {
        show { NotNowSlate(tomorrow) }
        compose.onNodeWithText("Not available right now. This channel comes back when the next programme starts.").assertIsDisplayed()
        compose.onNodeWithTag("live-parental-back").assertTextContains("Back at ", substring = true)
    }

    @Test
    @Config(qualifiers = "ar-$PHONE")
    fun theSlateInArabic() {
        show { NotNowSlate(null) }
        compose.onNodeWithText("غير متاح الآن. تعود هذه القناة عند بدء البرنامج التالي.").assertIsDisplayed()
        assertFalse("no time named", "live-parental-back" in compose.shown())
    }
}
