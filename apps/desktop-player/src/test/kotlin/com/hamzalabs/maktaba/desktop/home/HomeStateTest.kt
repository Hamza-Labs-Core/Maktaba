package com.hamzalabs.maktaba.desktop.home

import com.hamzalabs.maktaba.core.api.NowAnswer
import com.hamzalabs.maktaba.core.browse.KEY_ADDED
import com.hamzalabs.maktaba.core.browse.KEY_CONTINUE
import com.hamzalabs.maktaba.core.browse.KEY_FOR_YOU
import com.hamzalabs.maktaba.desktop.TestHome
import kotlinx.coroutines.async
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import java.util.concurrent.CountDownLatch

/** Story 55.03 T02–T05, against a mock home. */
class HomeStateTest {
    private fun card(id: String, extra: String = "") = """{"id":"$id","title":"Video $id","filename":"$id.mp4","duration_sec":2400$extra}"""

    private val homeBody = """{"continue_watching":[${card("cw", ""","position_sec":600""")}],"recently_added":[${card("new")}]}"""
    private val recsBody = """{"rails":[{"id":"fy","reason_kind":"for_you","items":[{"video_id":"r1"},{"video_id":"r2"}]}]}"""

    private fun state(fake: TestHome, recsOn: Boolean = true, now: Long = 0) = HomeState(
        fake.client, recommendationsOn = { recsOn }, liveNow = { null as NowAnswer? }, signedOut = {}, now = { now },
    )

    @Test
    fun theRowsAreInOrderAndRecommendationsAreNotAskedWhenOff() = TestHome().use { fake ->
        fake.answer("GET /api/home", homeBody)
        fake.answer("GET /api/recommendations", recsBody)
        runBlocking {
            val on = state(fake)
            on.load()
            assertEquals(listOf(KEY_CONTINUE, KEY_ADDED, KEY_FOR_YOU), on.rows.map { it.key })
            assertEquals("desktop-home", fake.asked("GET /api/recommendations").single().url.queryParameter("surface"))
            val off = state(fake, recsOn = false)
            off.load()
            assertEquals(listOf(KEY_CONTINUE, KEY_ADDED), off.rows.map { it.key })
            assertEquals(1, fake.asked("GET /api/recommendations").size)
        }
    }

    @Test
    fun aFailedRefreshKeepsTheLastRows() = TestHome().use { fake ->
        fake.answer("GET /api/home", homeBody)
        runBlocking {
            var clock = 0L
            val s = HomeState(fake.client, { false }, { null }, {}, now = { clock })
            s.open()
            fake.answer("GET /api/home", "{}", 500)
            clock += HOME_FRESH_MS
            s.open()
            assertTrue(s.stale)
            assertEquals(listOf(KEY_CONTINUE, KEY_ADDED), s.rows.map { it.key })
        }
    }

    @Test
    fun shownAgainWithinFiveMinutesItDoesNotReload() = TestHome().use { fake ->
        fake.answer("GET /api/home", homeBody)
        runBlocking {
            var clock = 0L
            val s = HomeState(fake.client, { false }, { null }, {}, now = { clock })
            s.open()
            clock += 60_000
            s.open()
            assertEquals(1, fake.asked("GET /api/home").size)
        }
    }

    @Test
    fun notInterestedStaysGoneWhenAnOlderReadAnswers() = TestHome().use { fake ->
        fake.answer("GET /api/home", homeBody)
        fake.answer("GET /api/recommendations", recsBody)
        fake.answer("DELETE /api/recommendations/items/r1") { TestHome.json("", 204) }
        runBlocking {
            val s = state(fake)
            s.load()
            // a read held open, then Not interested, then the read answers with the card
            val gate = CountDownLatch(1)
            fake.held("GET /api/recommendations", { recsBody }, gate)
            val read = async { s.load() }
            while (fake.asked("GET /api/recommendations").size < 2) kotlinx.coroutines.delay(10)
            assertTrue(s.dismiss("r1"))
            gate.countDown()
            read.await()
            val forYou = s.rows.first { it.key == KEY_FOR_YOU } as com.hamzalabs.maktaba.core.browse.HomeRow.Cards
            assertEquals(listOf("r2"), forYou.cards.map { it.videoId })
        }
    }

    @Test
    fun aFailedNotInterestedPutsTheCardBack() = TestHome().use { fake ->
        fake.answer("GET /api/home", homeBody)
        fake.answer("GET /api/recommendations", recsBody)
        fake.answer("DELETE /api/recommendations/items/r1") { TestHome.json("{}", 500) }
        runBlocking {
            val s = state(fake)
            s.load()
            assertFalse(s.dismiss("r1"))
            assertTrue(s.dismissFailed)
            val forYou = s.rows.first { it.key == KEY_FOR_YOU } as com.hamzalabs.maktaba.core.browse.HomeRow.Cards
            assertEquals(listOf("r1", "r2"), forYou.cards.map { it.videoId })
        }
    }
}
