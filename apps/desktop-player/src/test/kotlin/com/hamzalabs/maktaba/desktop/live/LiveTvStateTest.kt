package com.hamzalabs.maktaba.desktop.live

import com.hamzalabs.maktaba.core.api.LiveApi
import com.hamzalabs.maktaba.core.api.LiveChannel
import com.hamzalabs.maktaba.core.live.GuideWindow
import com.hamzalabs.maktaba.desktop.TestHome
import kotlinx.coroutines.async
import kotlinx.coroutines.delay
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Test
import java.util.concurrent.CountDownLatch

/** Story 55.07 T02 and T08. */
class LiveTvStateTest {
    private val now = """{"server_time":"2026-11-06T18:20:00Z","channels":[
        {"channel":{"id":"c2","number":2,"name":"Two"},"current":{"start":"2026-11-06T18:00:00Z","stop":"2026-11-06T18:40:00Z","title":"Tafsir"}},
        {"channel":{"id":"c1","number":1,"name":"One"}}]}"""

    @Test
    fun nowAndTheGuideLoadAndAnOlderGuideAnswerIsDropped() = TestHome().use { f ->
        f.answer("GET /api/channels/now", now)
        val gate = CountDownLatch(1)
        f.answer("GET /api/channels/guide") { r ->
            if (r.url.queryParameter("start") == "2026-11-06T16:00:00Z") gate.await()
            TestHome.json("""{"start":"${r.url.queryParameter("start")}","channels":[{"channel":{"id":"c1","number":1}}]}""")
        }
        runBlocking {
            val s = LiveTvState(LiveApi(f.client))
            s.loadNow()
            assertEquals(listOf("c1", "c2"), s.lineup.map { it.id })
            assertEquals("Tafsir", s.entry("c2")?.current?.title)
            val first = GuideWindow(java.time.Instant.parse("2026-11-06T16:00:00Z").toEpochMilli(), java.time.Instant.parse("2026-11-06T22:00:00Z").toEpochMilli())
            val older = async { s.loadGuide(first) }
            while (f.asked("GET /api/channels/guide").isEmpty()) delay(10)
            val later = first.shifted(2)
            s.loadGuide(later)
            gate.countDown()
            older.await()
            assertEquals(later, s.guideWindow)
        }
    }

    @Test
    fun aFailureAndAnEmptyHome() = TestHome().use { f ->
        f.answer("GET /api/channels/now", "{}", 500)
        runBlocking {
            val s = LiveTvState(LiveApi(f.client))
            s.loadNow()
            assertNotNull(s.nowProblem)
            assertNull(s.now)
            f.answer("GET /api/channels/now", """{"channels":[]}""")
            s.loadNow()
            assertEquals(0, s.now!!.channels.size)
        }
    }

    @Test
    fun channelUpAndDownWrap() {
        val lineup = listOf(LiveChannel("c3", 3), LiveChannel("c1", 1), LiveChannel("c2", 2))
        assertEquals("c1", neighbour(lineup, "c3", +1)?.id)
        assertEquals("c3", neighbour(lineup, "c1", -1)?.id)
        assertEquals("c2", neighbour(lineup, "c1", +1)?.id)
        assertEquals("c1", neighbour(lineup, "gone", +1)?.id)
        assertNull(neighbour(emptyList(), "c1", +1))
    }
}
