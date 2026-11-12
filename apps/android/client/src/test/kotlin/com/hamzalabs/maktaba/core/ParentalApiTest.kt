package com.hamzalabs.maktaba.core

import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.api.AskAnswer
import com.hamzalabs.maktaba.core.api.ChannelNow
import com.hamzalabs.maktaba.core.api.ErrorKind
import com.hamzalabs.maktaba.core.api.MyParental
import com.hamzalabs.maktaba.core.api.ProgressAnswer
import com.hamzalabs.maktaba.core.api.SubjectKind
import com.hamzalabs.maktaba.core.api.TimeReason
import com.hamzalabs.maktaba.core.api.UnlockAnswer
import com.hamzalabs.maktaba.core.api.askForTime
import com.hamzalabs.maktaba.core.api.askParent
import com.hamzalabs.maktaba.core.api.channelAccess
import com.hamzalabs.maktaba.core.api.channelAskAgainMs
import com.hamzalabs.maktaba.core.api.channelNow
import com.hamzalabs.maktaba.core.api.errorKind
import com.hamzalabs.maktaba.core.api.isParentPin
import com.hamzalabs.maktaba.core.api.isRestricted
import com.hamzalabs.maktaba.core.api.moreTimeWithPin
import com.hamzalabs.maktaba.core.api.myParental
import com.hamzalabs.maktaba.core.api.myTime
import com.hamzalabs.maktaba.core.api.parentPinHolders
import com.hamzalabs.maktaba.core.api.timeRefusal
import com.hamzalabs.maktaba.core.api.unlockWithPin
import com.hamzalabs.maktaba.core.parental.AskState
import com.hamzalabs.maktaba.core.parental.ParentalState
import com.hamzalabs.maktaba.core.parental.PinState
import com.hamzalabs.maktaba.core.parental.PinUnlock
import com.hamzalabs.maktaba.core.parental.ask
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import mockwebserver3.Dispatcher
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.RecordedRequest
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Before
import org.junit.Test
import java.time.Instant

/**
 * Epic 57 on the apps' client: the account's limit and time, the home's refusals as the
 * players read them, asking a parent, the parent PIN and live TV's "not now", against a
 * MockWebServer answering with the home's shapes (`shared/api/openapi.yaml`).
 */
class ParentalApiTest {
    private lateinit var server: MockWebServer
    private val routes = mutableMapOf<String, (RecordedRequest) -> MockResponse>()
    private val seen = mutableListOf<RecordedRequest>()
    private val token = jwt(System.currentTimeMillis() / 1000 + 3600)

    private fun json(code: Int, body: String, retryAfter: String? = null) = MockResponse.Builder().code(code)
        .setHeader("Content-Type", if (code >= 400) "application/problem+json" else "application/json")
        .apply { if (retryAfter != null) setHeader("Retry-After", retryAfter) }
        .body(body).build()

    private fun problem(status: Int, slug: String, code: String, more: String = "") =
        """{"type":"https://maktaba.tv/problems/$slug","title":"refused","status":$status,"code":"$code"$more}"""

    private val timeOver = problem(403, "parental-time", "parental_time", ""","reason":"limit_reached","next_allowed_at":"2026-11-12T04:00:00Z"""")

    @Before
    fun start() {
        server = MockWebServer()
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                synchronized(seen) { seen += request }
                return routes["${request.method} ${request.url.encodedPath}"]?.invoke(request) ?: MockResponse.Builder().code(404).build()
            }
        }
        server.start()
    }

    @After
    fun stop() = server.close()

    private fun client() = ApiClient(server.url("/"), MemoryTokens(token, "r"))
    private fun body(r: RecordedRequest) = Json.parseToJsonElement(r.body!!.utf8()).jsonObject
    private fun last() = synchronized(seen) { seen.last() }

    // ---- the limit ----

    @Test
    fun theLimitOfARestrictedAccountAndOfALimitedDevice() = runBlocking {
        routes["GET /api/me/parental"] = { json(200, """{"restricted":true,"max_tier":9,"unrated":"hide","allow_only":false,"version":4}""") }
        val mine = client().myParental()
        assertTrue(mine.restricted && mine.limited)
        assertEquals(9, mine.maxTier)

        routes["GET /api/me/parental"] = { json(200, """{"restricted":false,"max_tier":13,"device_limit":13,"device_pending":true}""") }
        val device = client().myParental()
        assertFalse(device.restricted)
        assertTrue("a device limit is a limit too", device.limited)
        assertTrue(device.devicePending)
    }

    @Test
    fun aHomeOlderThanParentalControlsRestrictsNobody() = runBlocking {
        assertEquals(MyParental.None, client().myParental())
    }

    // ---- the time ----

    @Test
    fun theTimeLeftIsTheHomesAnswer() = runBlocking {
        routes["GET /api/me/parental/time"] = {
            json(200, """{"allowed":true,"limited":true,"remaining_seconds":290,"slot_ends_at":"2026-11-11T17:00:00Z","next_allowed_at":null,"now":"2026-11-11T16:00:00Z"}""")
        }
        val t = client().myTime()
        assertTrue(t.allowed && t.limited)
        assertEquals(290L, t.remainingSeconds)
        assertEquals(Instant.parse("2026-11-11T17:00:00Z"), t.slotEndsAt)
        assertNull(t.nextAllowedAt)
        assertEquals(Instant.parse("2026-11-11T16:00:00Z"), t.now)
    }

    @Test
    fun theTimeOfALibraryNamesIt() = runBlocking {
        routes["GET /api/me/parental/time"] = { json(200, """{"allowed":false,"limited":true,"reason":"outside_slot","remaining_seconds":0,"next_allowed_at":"2026-11-12T07:00:00+00:00"}""") }
        val t = client().myTime("l1")
        assertEquals("l1", last().url.queryParameter("library_id"))
        assertFalse(t.allowed)
        assertEquals(TimeReason.OUTSIDE_SLOT, t.reason)
        assertEquals(Instant.parse("2026-11-12T07:00:00Z"), t.nextAllowedAt)
    }

    @Test
    fun anAccountWithNoRulesIsAllowedAndNotLimited() = runBlocking {
        routes["GET /api/me/parental/time"] = { json(200, """{"allowed":true,"limited":false,"remaining_seconds":null,"slot_ends_at":null,"next_allowed_at":null}""") }
        val t = client().myTime()
        assertTrue(t.allowed)
        assertFalse(t.limited)
        assertNull(t.remainingSeconds)
    }

    // ---- the refusals ----

    @Test
    fun aRefusedSessionOpenCarriesTheReasonAndTheNextTime() = runBlocking {
        routes["POST /api/stream/sessions"] = { json(403, timeOver) }
        try {
            client().openStream("v1")
            fail("the open was refused")
        } catch (e: ApiException.Status) {
            val over = e.timeRefusal()!!
            assertEquals(TimeReason.LIMIT_REACHED, over.reason)
            assertEquals(Instant.parse("2026-11-12T04:00:00Z"), over.nextAllowedAt)
            assertFalse(e.isRestricted())
        }
    }

    @Test
    fun aTimeRefusalWithNoNextTimeAndByCodeAlone() {
        val e = ApiException.Status(403, """{"type":"about:blank","title":"x","code":"parental_time","reason":"no_heartbeat"}""")
        assertEquals(TimeReason.NO_HEARTBEAT, e.timeRefusal()!!.reason)
        assertNull(e.timeRefusal()!!.nextAllowedAt)
        // another 403, and the same problem under another status, are not time refusals
        assertNull(ApiException.Status(403, problem(403, "forbidden", "restricted")).timeRefusal())
        assertNull(ApiException.Status(503, timeOver).timeRefusal())
        assertTrue(ApiException.Status(403, problem(403, "restricted", "restricted")).isRestricted())
    }

    @Test
    fun aProgressPostTellsThePlayerWhatTheHomeSaid() = runBlocking {
        val path = "POST /api/stream/sessions/s1/progress"
        routes[path] = { json(200, """{"position_sec":12,"completed":false}""") }
        assertEquals(ProgressAnswer.Ok, client().reportProgress("s1", 12))
        assertEquals(12, body(last())["position_sec"]!!.jsonPrimitive.content.toInt())

        routes[path] = { json(403, timeOver) }
        val over = client().reportProgress("s1", 13) as ProgressAnswer.TimeOver
        assertEquals(TimeReason.LIMIT_REACHED, over.refusal.reason)
        assertEquals(Instant.parse("2026-11-12T04:00:00Z"), over.refusal.nextAllowedAt)

        routes[path] = { json(404, problem(404, "not-found", "not_found")) }
        assertEquals("a missing session is the stream's end", ProgressAnswer.Ended, client().reportProgress("s1", 14))

        routes[path] = { json(503, problem(503, "unavailable", "unavailable"), retryAfter = "5") }
        assertEquals(ProgressAnswer.CannotCheck(5), client().reportProgress("s1", 15))

        routes[path] = { json(500, "boom") }
        assertTrue(client().reportProgress("s1", 16) is ProgressAnswer.Failed)
    }

    @Test
    fun aProgressPostToAHomeThatDoesNotAnswerIsNotARefusal() = runBlocking {
        val c = client()
        server.close()
        assertTrue(c.reportProgress("s1", 5) is ProgressAnswer.Failed)
    }

    @Test
    fun a503OnOpenKeepsItsRetryAfter() = runBlocking {
        routes["POST /api/stream/sessions"] = { json(503, problem(503, "unavailable", "unavailable"), retryAfter = "5") }
        try {
            client().openStream("v1")
            fail("refused")
        } catch (e: ApiException.Status) {
            assertEquals(5L, e.retryAfterSec)
            assertEquals(ErrorKind.Busy, e.errorKind())
            assertNull(e.timeRefusal())
        }
    }

    // ---- asking a parent ----

    @Test
    fun askingForMoreTimeSendsTheMinutesAsText() = runBlocking {
        routes["POST /api/me/parental/requests"] = {
            json(201, """{"request":{"id":"r1","child_id":"u1","subject_kind":"time","subject_id":"30","minutes":30,"status":"pending","expires_at":"2026-11-12T04:00:00Z"}}""")
        }
        val a = client().askForTime(30) as AskAnswer.Waiting
        assertEquals("""{"subject_kind":"time","subject_id":"30"}""", last().body!!.utf8())
        assertEquals(30, a.request.minutes)
        assertEquals("pending", a.request.status)
    }

    @Test
    fun askingAgainAnswersTheRequestAlreadyWaiting() = runBlocking {
        routes["POST /api/me/parental/requests"] = { json(200, """{"request":{"id":"r1","subject_kind":"video","subject_id":"v9","status":"pending"}}""") }
        val a = client().askParent(SubjectKind.VIDEO, "v9") as AskAnswer.Waiting
        assertEquals("r1", a.request.id)
    }

    @Test
    fun aHiddenItemIsAMissingOneAndNothingIsOffered() = runBlocking {
        routes["POST /api/me/parental/requests"] = { json(404, problem(404, "not-found", "not_found")) }
        assertEquals(AskAnswer.NothingToAsk, client().askParent(SubjectKind.VIDEO, "v9"))
        assertEquals(AskState.NothingToAsk, client().ask(SubjectKind.VIDEO, "v9"))
    }

    @Test
    fun tooManyRequestsAndAnAccountWithNoLimit() = runBlocking {
        routes["POST /api/me/parental/requests"] = { json(429, problem(429, "too-many", "too_many_requests")) }
        assertEquals(AskAnswer.TooMany, client().askParent(SubjectKind.VIDEO, "v9"))
        assertEquals(AskState.TooMany, client().ask(SubjectKind.VIDEO, "v9"))
        routes["POST /api/me/parental/requests"] = { json(403, problem(403, "forbidden", "not_restricted")) }
        assertEquals(AskAnswer.NotRestricted, client().askParent(SubjectKind.VIDEO, "v9"))
        // a plain rate limit is not "too many requests waiting": it is a failure to try again
        routes["POST /api/me/parental/requests"] = { json(429, problem(429, "rate-limited", "rate_limited")) }
        assertEquals(AskState.Failed, client().ask(SubjectKind.VIDEO, "v9"))
    }

    // ---- the parent PIN ----

    @Test
    fun thePinScreenOffersThoseWhoSetAPin() = runBlocking {
        routes["GET /api/me/parental/guardians"] = { json(200, """{"guardians":[{"id":"g1","username":"huda"},{"id":"g2","username":"omar"}]}""") }
        assertEquals(listOf("huda", "omar"), client().parentPinHolders().map { it.username })
        routes["GET /api/me/parental/guardians"] = { json(403, problem(403, "forbidden", "not_restricted")) }
        assertTrue(client().parentPinHolders().isEmpty())
    }

    @Test
    fun aRightPinOpensTheItemAndGivesTime() = runBlocking {
        routes["POST /api/me/parental/unlock"] = { json(200, """{"rule":{"id":"x","effect":"allow","subject_kind":"video","subject_id":"v9","expires_at":"2026-11-12T04:00:00Z"}}""") }
        assertEquals(UnlockAnswer.Unlocked, client().unlockWithPin("g1", "123456", SubjectKind.VIDEO, "v9"))
        assertEquals("""{"guardian_id":"g1","pin":"123456","subject_kind":"video","subject_id":"v9"}""", last().body!!.utf8())

        routes["POST /api/me/parental/unlock"] = { json(200, """{"grant":{"id":"t1","user_id":"u1","day":"2026-11-11","minutes":30,"used_seconds":0}}""") }
        assertEquals(UnlockAnswer.Unlocked, client().moreTimeWithPin("g1", "123456", 30))
        assertEquals("""{"guardian_id":"g1","pin":"123456","subject_kind":"time","subject_id":"30"}""", last().body!!.utf8())
    }

    @Test
    fun aWrongPinALockAndAMissingItem() = runBlocking {
        val c = client()
        routes["POST /api/me/parental/unlock"] = { json(403, problem(403, "forbidden", "pin_wrong")) }
        assertEquals(UnlockAnswer.Wrong, c.unlockWithPin("g1", "000000", SubjectKind.VIDEO, "v9"))
        routes["POST /api/me/parental/unlock"] = { json(429, problem(429, "locked", "pin_locked", ""","retry_after":900"""), retryAfter = "900") }
        assertEquals(UnlockAnswer.Locked(900), c.unlockWithPin("g1", "000000", SubjectKind.VIDEO, "v9"))
        routes["POST /api/me/parental/unlock"] = { json(404, problem(404, "not-found", "not_found")) }
        assertEquals(UnlockAnswer.NothingToUnlock, c.unlockWithPin("g1", "000000", SubjectKind.VIDEO, "v9"))
        routes["POST /api/me/parental/unlock"] = { json(403, problem(403, "forbidden", "not_restricted")) }
        assertEquals(UnlockAnswer.NotRestricted, c.unlockWithPin("g1", "000000", SubjectKind.VIDEO, "v9"))
    }

    @Test
    fun thePinFlowOfAScreen() = runBlocking {
        routes["GET /api/me/parental/guardians"] = { json(200, """{"guardians":[{"id":"g1","username":"huda"}]}""") }
        val flow = PinUnlock(client(), SubjectKind.TIME, "30")
        flow.load()
        assertEquals("g1", flow.holders.value.single().id)

        routes["POST /api/me/parental/unlock"] = { json(403, problem(403, "forbidden", "pin_wrong")) }
        flow.submit("g1", "111111", minutes = 15)
        assertEquals(PinState.Wrong, flow.state.value)
        assertEquals("15", body(last())["subject_id"]!!.jsonPrimitive.content)
        flow.again()
        assertEquals(PinState.Entering, flow.state.value)

        routes["POST /api/me/parental/unlock"] = { json(200, """{"grant":{"id":"t1","minutes":15}}""") }
        flow.submit("g1", "123456", minutes = 15)
        assertEquals(PinState.Unlocked, flow.state.value)

        routes["POST /api/me/parental/unlock"] = { json(500, "boom") }
        flow.submit("g1", "123456", minutes = 15)
        assertEquals(PinState.Failed, flow.state.value)
    }

    @Test
    fun aRevokedSignInAtThePinIsToldToTheAppAndNeverThrown() = runBlocking {
        // the child's sign-in was revoked while the PIN panel was open: 401, and the refresh too
        routes["POST /api/me/parental/unlock"] = { json(401, "{}") }
        routes["POST /api/auth/refresh"] = { json(401, "{}") }
        var signedOut = 0
        val flow = PinUnlock(client(), SubjectKind.VIDEO, "v1", signedOut = { signedOut++ })
        flow.submit("g1", "123456") // thrown here, it would crash the screen's scope
        assertEquals(PinState.Failed, flow.state.value)
        assertEquals("the app signs out", 1, signedOut)
    }

    @Test
    fun aRevokedSignInWhenAskingIsToldToTheAppToo() = runBlocking {
        routes["POST /api/me/parental/requests"] = { json(401, "{}") }
        routes["POST /api/auth/refresh"] = { json(401, "{}") }
        var signedOut = 0
        val asking = com.hamzalabs.maktaba.core.parental.ParentAsk(client(), SubjectKind.TIME, signedOut = { signedOut++ })
        asking.ask(30)
        assertEquals(AskState.Failed, asking.state.value)
        assertEquals("not swallowed as Try again: the app signs out", 1, signedOut)
    }

    @Test
    fun aLockCountsDownAndClearsByItself() = runBlocking {
        routes["POST /api/me/parental/unlock"] = { json(429, problem(429, "parental-pin-locked", "pin_locked"), retryAfter = "3") }
        val flow = PinUnlock(client(), SubjectKind.VIDEO, "v1")
        flow.submit("g1", "000000")
        assertEquals(PinState.Locked(3), flow.state.value)
        val seen = mutableListOf<PinState>()
        var waited = 0L
        flow.waitOutLock(wait = { ms ->
            waited += ms
            seen += flow.state.value
        })
        assertEquals("the seconds left go down", listOf<PinState>(PinState.Locked(3), PinState.Locked(2), PinState.Locked(1)), seen)
        assertEquals(3_000L, waited)
        assertEquals("then the PIN can be typed again", PinState.Entering, flow.state.value)

        // a lock the home named no time for is not counted, and a state that is no lock returns at once
        routes["POST /api/me/parental/unlock"] = { json(429, problem(429, "parental-pin-locked", "pin_locked")) }
        flow.submit("g1", "000000")
        flow.waitOutLock(wait = { fail("nothing to wait for") })
        assertEquals(PinState.Locked(null), flow.state.value)
        flow.again()
        flow.waitOutLock(wait = { fail("not locked") })

        // closed or answered while it counts: the count stops and changes nothing
        routes["POST /api/me/parental/unlock"] = { json(429, problem(429, "parental-pin-locked", "pin_locked"), retryAfter = "60") }
        flow.submit("g1", "000000")
        var ticks = 0
        flow.waitOutLock(wait = { if (++ticks == 2) flow.again() })
        assertEquals(2, ticks)
        assertEquals(PinState.Entering, flow.state.value)
    }

    @Test
    fun onlySixDigitsAreAPin() {
        assertTrue(isParentPin("012345"))
        listOf("", "12345", "1234567", "12345a", "١٢٣٤٥٦").forEach { assertFalse(it, isParentPin(it)) }
    }

    // ---- live TV ----

    @Test
    fun aChannelsAccessSaysWhenItMayChange() = runBlocking {
        routes["GET /api/channels/c1/access"] = { json(200, """{"channel_id":"c1","restricted":true,"allowed":false,"until":"2026-11-11T20:30:00Z"}""") }
        val a = client().channelAccess("c1")
        assertTrue(a.restricted)
        assertFalse(a.allowed)
        assertEquals(Instant.parse("2026-11-11T20:30:00Z"), a.until)
    }

    @Test
    fun playingAChannelGivesItsSignedAddressUnderTheHome() = runBlocking {
        routes["POST /api/channels/c1/play"] = {
            json(200, """{"channel_id":"c1","url":"/stream/channel/c1/index.m3u8?sig=abc&exp=1","expires_at":"2026-11-11T21:00:00Z","until":null}""")
        }
        val now = client().channelNow("c1") as ChannelNow.Playing
        assertEquals(server.url("/stream/channel/c1/index.m3u8?sig=abc&exp=1"), now.url)
        assertNull(now.until)
    }

    @Test
    fun aProgrammeAboveTheLimitIsNotNowUntilTheHomesTime() = runBlocking {
        routes["POST /api/channels/c1/play"] = {
            json(403, problem(403, "restricted", "restricted", ""","reason":"programme","retry_at":"2026-11-11T20:30:00Z""""))
        }
        assertEquals(ChannelNow.NotNow(Instant.parse("2026-11-11T20:30:00Z")), client().channelNow("c1"))
        routes["POST /api/channels/c1/play"] = { json(403, problem(403, "restricted", "restricted", ""","reason":"programme"""")) }
        assertEquals(ChannelNow.NotNow(null), client().channelNow("c1"))
    }

    @Test
    fun aHiddenChannelIsAMissingOneAndAnAddressElsewhereIsRefused() = runBlocking {
        try {
            client().channelNow("c9")
            fail("404")
        } catch (e: ApiException.Status) {
            assertEquals(ErrorKind.NotFound, e.errorKind())
        }
        routes["POST /api/channels/c1/play"] = { json(200, """{"channel_id":"c1","url":"https://elsewhere.example/stream/channel/c1/index.m3u8?sig=x","expires_at":"2026-11-11T21:00:00Z"}""") }
        try {
            client().channelNow("c1")
            fail("another host's address")
        } catch (e: ApiException.Decoding) {
            assertTrue(e.message!!.contains("stream"))
        }
    }

    @Test
    fun whenToAskAboutAChannelAgain() {
        val now = Instant.parse("2026-11-11T20:00:00Z")
        assertEquals(31_000L, channelAskAgainMs(now.plusSeconds(30), allowed = false, nowMs = now.toEpochMilli()))
        assertEquals("never sooner than five seconds", 5_000L, channelAskAgainMs(now.minusSeconds(60), allowed = true, nowMs = now.toEpochMilli()))
        assertEquals(60_000L, channelAskAgainMs(null, allowed = false, nowMs = now.toEpochMilli()))
        assertNull("it plays and nothing is due to change", channelAskAgainMs(null, allowed = true, nowMs = now.toEpochMilli()))
    }

    // ---- the state the screens read ----

    @Test
    fun aFailedLiveStreamIsAskedForAgainOncePerFailure() {
        var now = 1_000_000L
        val reopen = com.hamzalabs.maktaba.core.api.GatedReopen { now }
        assertTrue("the first failure asks the home again", reopen.allowed())
        now += 12_000
        assertFalse("failing again right after: the error shows", reopen.allowed())
        now += 20_000
        assertFalse(reopen.allowed())
        // it played for an hour and then stopped: the programme may have changed
        now += 3_600_000
        assertTrue(reopen.allowed())
        now += 5_000
        assertFalse(reopen.allowed())
        // the person's own Try again starts over
        reopen.reset()
        assertTrue(reopen.allowed())
    }

    private class Remembered(var mark: com.hamzalabs.maktaba.core.parental.KeptLimit.Mark? = null) : com.hamzalabs.maktaba.core.parental.KeptLimit {
        override fun read() = mark
        override fun write(mark: com.hamzalabs.maktaba.core.parental.KeptLimit.Mark) {
            this.mark = mark
        }
    }

    private fun limit(version: Int, tier: Int = 13) = """{"restricted":true,"max_tier":$tier,"allow_only":false,"version":$version}"""

    @Test
    fun aChangedLimitDropsWhatWasKeptAndMovesTheEpoch() = runBlocking {
        var body = limit(3)
        routes["GET /api/me/parental"] = { json(200, body) }
        val state = ParentalState(client(), Remembered())
        val order = mutableListOf<String>()
        state.onChange = { order += "dropped at epoch ${state.epoch.value}" }
        state.refresh()
        assertEquals("the first answer of a limited sign-in: what was kept before is not trusted", 1, state.epoch.value)
        state.refresh()
        state.refresh()
        assertEquals("the same limit: nothing is dropped", 1, state.epoch.value)
        // a guardian tightened it: the version moves
        body = limit(4, tier = 9)
        state.refresh()
        assertEquals(2, state.epoch.value)
        assertEquals("the kept things go before the screens read again", listOf("dropped at epoch 0", "dropped at epoch 1"), order)
        // the same version with another tier in force (the device's limit) is a change too
        body = """{"restricted":true,"max_tier":9,"allow_only":false,"device_limit":0,"version":4}"""
        state.refresh()
        assertEquals(3, state.epoch.value)
        // a home that does not answer changes nothing
        routes["GET /api/me/parental"] = { json(500, "{}") }
        state.refresh()
        assertEquals(3, state.epoch.value)
        assertEquals(9, state.mine.value.maxTier)
    }

    @Test
    fun aLimitChangedWhileTheAppWasClosedIsSeenAtTheFirstAnswer() = runBlocking {
        val dir = java.nio.file.Files.createTempDirectory("kept-limit").toFile()
        try {
            val kept = com.hamzalabs.maktaba.core.parental.KeptLimit.InFile.of(dir, server.url("/"), "u1")
            routes["GET /api/me/parental"] = { json(200, limit(3)) }
            ParentalState(client(), kept).refresh()
            assertTrue(kept.read()!!.limited)

            // the next launch, the same limit: nothing kept on the device shows until the home
            // answers, and its answer drops nothing
            var dropped = 0
            val same = ParentalState(client(), kept).also { it.onChange = { dropped++ } }
            assertTrue("limited when last asked: nothing kept shows yet", same.keepsNothing)
            same.refresh()
            assertEquals(0, dropped)
            assertEquals(0, same.epoch.value)

            // the launch after a guardian changed it
            routes["GET /api/me/parental"] = { json(200, limit(4, tier = 9)) }
            val changed = ParentalState(client(), kept).also { it.onChange = { dropped++ } }
            changed.refresh()
            assertEquals(1, dropped)
            assertEquals(1, changed.epoch.value)

            // the limit taken away while closed: what was kept under it goes once, then it may keep again
            routes["GET /api/me/parental"] = { json(200, """{"restricted":false}""") }
            val lifted = ParentalState(client(), kept).also { it.onChange = { dropped++ } }
            assertTrue(lifted.keepsNothing)
            lifted.refresh()
            assertEquals(2, dropped)
            assertFalse(lifted.keepsNothing)
        } finally {
            dir.deleteRecursively()
        }
    }

    @Test
    fun anAccountNeverLimitedDropsNothing() = runBlocking {
        routes["GET /api/me/parental"] = { json(200, """{"restricted":false}""") }
        var dropped = 0
        val state = ParentalState(client(), Remembered()).also { it.onChange = { dropped++ } }
        assertFalse(state.keepsNothing)
        state.refresh()
        state.refresh()
        assertEquals(0, dropped)
        assertEquals(0, state.epoch.value)
    }

    @Test
    fun theTimeIsAskedOnlyForARestrictedAccount() = runBlocking {
        routes["GET /api/me/parental"] = { json(200, """{"restricted":false}""") }
        routes["GET /api/me/parental/time"] = { json(200, """{"allowed":true,"limited":true,"remaining_seconds":600}""") }
        val state = ParentalState(client())
        state.refresh()
        state.followTime() // returns at once
        assertNull(state.time.value)
        assertFalse(synchronized(seen) { seen.any { it.url.encodedPath.endsWith("/time") } })
    }

    @Test
    fun theTimeIsFollowedSoonerNearTheEnd() = runBlocking {
        routes["GET /api/me/parental"] = { json(200, """{"restricted":true,"max_tier":9}""") }
        var left = 1200
        routes["GET /api/me/parental/time"] = { json(200, """{"allowed":true,"limited":true,"remaining_seconds":$left}""") }
        val state = ParentalState(client())
        state.refresh()
        val waits = mutableListOf<Long>()
        try {
            state.followTime(wait = {
                waits += it
                left = 200
                if (waits.size == 2) throw CancellationException("the screen went")
            })
        } catch (_: CancellationException) {
        }
        assertEquals(listOf(30_000L, 10_000L), waits)
        assertEquals(200L, state.time.value!!.remainingSeconds)
    }

    @Test
    fun aHomeThatDoesNotAnswerKeepsTheLastTime() = runBlocking {
        routes["GET /api/me/parental"] = { json(200, """{"restricted":true}""") }
        routes["GET /api/me/parental/time"] = { json(200, """{"allowed":true,"limited":true,"remaining_seconds":240}""") }
        val state = ParentalState(client())
        state.refresh()
        state.askTime()
        routes["GET /api/me/parental/time"] = { json(503, "busy") }
        assertEquals(240L, state.askTime()!!.remainingSeconds)
        // and the limit stays what it was when the home stops answering
        routes["GET /api/me/parental"] = { json(500, "boom") }
        state.refresh()
        assertTrue(state.mine.value.restricted)
    }
}
