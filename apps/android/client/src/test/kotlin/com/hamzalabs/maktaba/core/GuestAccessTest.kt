package com.hamzalabs.maktaba.core

import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.guest.GuestAccess
import com.hamzalabs.maktaba.core.guest.GuestDoor
import com.hamzalabs.maktaba.core.guest.GuestGate
import com.hamzalabs.maktaba.core.guest.guestAccess
import com.hamzalabs.maktaba.core.parental.KeptLimit
import com.hamzalabs.maktaba.core.parental.ParentalState
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import mockwebserver3.Dispatcher
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.RecordedRequest
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test

/**
 * Story 50.27: Watch a broadcast fails closed. It opens only when no parental limit is known to
 * be on: the signed-in home has answered with none, or (nobody signed in) no limit is kept on the
 * device. An invite's link waits for the answer or is refused, and an open guest space closes the
 * moment a limit turns on.
 */
class GuestAccessTest {
    private lateinit var server: MockWebServer

    @Volatile private var parental: MockResponse = answer(limited = false, version = 1)

    private fun answer(limited: Boolean, version: Int) = MockResponse.Builder().code(200).setHeader("Content-Type", "application/json")
        .body(if (limited) """{"restricted":true,"max_tier":9,"allow_only":false,"version":$version}""" else """{"restricted":false,"allow_only":false,"version":$version}""")
        .build()

    @Before
    fun start() {
        server = MockWebServer()
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse =
                if (request.url.encodedPath == "/api/me/parental") parental else MockResponse.Builder().code(404).build()
        }
        server.start()
    }

    @After
    fun stop() = server.close()

    private class Remembered(var mark: KeptLimit.Mark? = null) : KeptLimit {
        override fun read() = mark
        override fun write(mark: KeptLimit.Mark) {
            this.mark = mark
        }
    }

    private fun client() = ApiClient(server.url("/"), MemoryTokens(jwt(System.currentTimeMillis() / 1000 + 3600), "r"))

    private val free = KeptLimit.Mark("sig", limited = false)
    private val limit = KeptLimit.Mark("sig", limited = true)

    @Test
    fun theRuleFailsClosed() {
        // nobody signed in: the limits kept on the device
        assertEquals("a device nobody signed in on", GuestAccess.Allowed, guestAccess(emptyList(), known = null, limited = false))
        assertEquals(GuestAccess.Allowed, guestAccess(listOf(free, free), known = null, limited = false))
        assertEquals("a sign-in kept as limited", GuestAccess.Limited, guestAccess(listOf(free, limit), known = null, limited = false))
        assertEquals("a mark that cannot be read counts as a limit", GuestAccess.Limited, guestAccess(listOf(free, null), known = null, limited = false))
        // signed in: nothing until the home says
        assertEquals(GuestAccess.Unknown, guestAccess(emptyList(), known = false, limited = false))
        assertEquals(GuestAccess.Limited, guestAccess(emptyList(), known = true, limited = true))
        assertEquals(GuestAccess.Allowed, guestAccess(emptyList(), known = true, limited = false))
        assertEquals("a limit kept for another sign-in still counts", GuestAccess.Limited, guestAccess(listOf(limit), known = true, limited = false))
        assertEquals(GuestAccess.Limited, guestAccess(listOf(null), known = true, limited = false))
    }

    @Test
    fun theGateFollowsTheSignInAndItsAnswers() = runBlocking {
        val kept = Remembered()
        val gate = GuestGate { listOfNotNull(kept.mark) }
        assertEquals("signed out on a fresh device", GuestAccess.Allowed, gate.now())

        val state = ParentalState(client(), kept)
        gate.follow(state)
        assertFalse(state.known)
        assertEquals("before the home answers: shut", GuestAccess.Unknown, gate.now())
        assertEquals(GuestAccess.Unknown, gate.access.first())

        state.refresh()
        assertTrue(state.known)
        assertEquals(GuestAccess.Allowed, gate.now())
        assertEquals(GuestAccess.Allowed, gate.access.first())

        // a guardian puts the sign-in under a limit
        parental = answer(limited = true, version = 2)
        state.refresh()
        assertEquals(GuestAccess.Limited, gate.access.first())
        assertEquals(true, kept.mark?.limited)

        // signed out: the kept mark says limited
        gate.follow(null)
        assertEquals(GuestAccess.Limited, gate.now())

        // signed in again, the limit lifted: the mark is rewritten before the gate opens
        val again = ParentalState(client(), kept)
        gate.follow(again)
        assertEquals("until the home answers, the limit kept on the device holds", GuestAccess.Limited, gate.now())
        parental = answer(limited = false, version = 3)
        again.refresh()
        assertEquals(false, kept.mark?.limited)
        assertEquals(GuestAccess.Allowed, gate.access.first())
    }

    @Test
    fun aHomeThatDoesNotAnswerKeepsItShut() = runBlocking {
        parental = MockResponse.Builder().code(503).build()
        val state = ParentalState(client(), Remembered())
        val gate = GuestGate { emptyList() }
        gate.follow(state)
        state.refresh()
        assertFalse(state.known)
        assertEquals(GuestAccess.Unknown, gate.access.first())
    }

    @Test
    fun theDoorOpensHoldsAndRefuses() {
        val door = GuestDoor()
        // the entry
        door.request(null, GuestAccess.Allowed)
        assertEquals(GuestDoor.Shown.Open(null), door.shown.value)
        door.close()
        door.request(null, GuestAccess.Unknown)
        assertEquals("the entry is hidden while unknown: nothing opens", GuestDoor.Shown.Shut, door.shown.value)
        door.request(null, GuestAccess.Limited)
        assertEquals(GuestDoor.Shown.Refused, door.shown.value)
        door.close()

        // an invite's link while the home has not said: held, then opened once it does
        door.request("mkbn1.a.b", GuestAccess.Unknown)
        assertEquals(GuestDoor.Shown.Held("mkbn1.a.b"), door.shown.value)
        door.update(GuestAccess.Unknown)
        assertEquals(GuestDoor.Shown.Held("mkbn1.a.b"), door.shown.value)
        door.update(GuestAccess.Allowed)
        assertEquals(GuestDoor.Shown.Open("mkbn1.a.b"), door.shown.value)

        // a limit turns on while it is open: it closes, and says why
        door.update(GuestAccess.Limited)
        assertEquals(GuestDoor.Shown.Refused, door.shown.value)
        door.update(GuestAccess.Allowed)
        assertEquals("a refusal stays until it is read", GuestDoor.Shown.Refused, door.shown.value)
        door.close()

        // a held link under a limit is refused, never opened later
        door.request("mkbn1.a.b", GuestAccess.Unknown)
        door.update(GuestAccess.Limited)
        assertEquals(GuestDoor.Shown.Refused, door.shown.value)
        door.close()
        door.request("mkbn1.a.b", GuestAccess.Limited)
        assertEquals(GuestDoor.Shown.Refused, door.shown.value)
        door.close()

        // another sign-in whose limit is not known: an open space shuts, its invite is not redeemed again
        door.request("mkbn1.a.b", GuestAccess.Allowed)
        door.update(GuestAccess.Unknown)
        assertEquals(GuestDoor.Shown.Shut, door.shown.value)
        door.update(GuestAccess.Allowed)
        assertEquals(GuestDoor.Shown.Shut, door.shown.value)
    }

    @Test
    fun aPlaysAddressLoadsOnceItArrivesUnderTheSamePlay() {
        val loads = com.hamzalabs.maktaba.core.guest.GuestLoads()
        val live = com.hamzalabs.maktaba.core.guest.GuestLive.of("https://live.maktaba.tv/vt-1/ch1/master.m3u8")!!
        val starting = com.hamzalabs.maktaba.core.guest.GuestWatch.Shown(channel = "ch1", play = 1)
        assertEquals("nothing to load while the edge has not answered", null, loads.take(starting))
        // the edge answered: the same play now has its address, which loads
        val playing = starting.copy(live = live)
        assertEquals(live, loads.take(playing))
        // a heartbeat (a new state, a refreshed token in the same GuestLive) never loads again
        assertEquals(null, loads.take(playing.copy(state = com.hamzalabs.maktaba.core.guest.GuestViewerState.Live)))
        // another channel: a new play loads its own address
        val other = com.hamzalabs.maktaba.core.guest.GuestLive.of("https://live.maktaba.tv/vt-1/ch2/master.m3u8")!!
        assertEquals(null, loads.take(com.hamzalabs.maktaba.core.guest.GuestWatch.Shown(channel = "ch2", play = 2)))
        assertEquals(other, loads.take(com.hamzalabs.maktaba.core.guest.GuestWatch.Shown(channel = "ch2", live = other, play = 2)))
        // Try again on the same channel after a refusal: loads again
        assertEquals(null, loads.take(com.hamzalabs.maktaba.core.guest.GuestWatch.Shown(channel = "ch2", problem = com.hamzalabs.maktaba.core.guest.GuestError.Offline, play = 2)))
        assertEquals(other, loads.take(com.hamzalabs.maktaba.core.guest.GuestWatch.Shown(channel = "ch2", live = other, play = 3)))
    }
}
