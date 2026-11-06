package com.hamzalabs.maktaba.desktop.search

import com.hamzalabs.maktaba.desktop.TestHome
import kotlinx.coroutines.async
import kotlinx.coroutines.delay
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import java.util.concurrent.CountDownLatch

/** Story 55.04 T01–T05. */
class SearchModelTest {
    private val results = """{"search_id":"s1","q":"fasting","videos":[{"video_id":"v1","title":"Fasting 1"}],"videos_total":2,
        "transcript_videos":[{"video_id":"v2","title":"Talk","passages":[{"start_sec":724,"snippet":"the <mark>fasting</mark> day"}]}],"transcript_total":1}"""
    private val recent = """{"items":[{"search_id":"a","q":"fasting","title_count":2},{"search_id":"b","q":"prayer"}]}"""

    @Test
    fun typeEnterAndShowMore() = TestHome().use { fake ->
        fake.answer("GET /api/search/suggest", """{"suggestions":["fasting","fasting rules"]}""")
        fake.answer("POST /api/search", results)
        fake.answer("GET /api/search/results/s1", """{"search_id":"s1","list":"titles","offset":1,"total":2,"items":[{"video_id":"v3","title":"Fasting 2"}]}""")
        fake.answer("GET /api/search/recent", recent)
        runBlocking {
            val m = SearchModel(fake.client, rest = {})
            m.typing("fas")
            assertEquals(listOf("fasting", "fasting rules"), m.suggestions)
            m.move(1)
            m.enter()
            assertEquals("fasting", m.answer!!.q)
            assertEquals("hybrid", fake.asked("POST /api/search").single().body!!.utf8().let { Regex("\"mode\":\"(\\w+)\"").find(it)!!.groupValues[1] })
            assertTrue(m.answer!!.moreTitles)
            m.moreTitles()
            assertEquals(listOf("v1", "v3"), m.answer!!.titles.map { it.videoId })
        }
    }

    @Test
    fun anOlderWordsSuggestionsAreDropped() = TestHome().use { fake ->
        val gate = CountDownLatch(1)
        fake.answer("GET /api/search/suggest") { r ->
            if (r.url.queryParameter("q") == "ab") gate.await()
            TestHome.json("""{"suggestions":["${r.url.queryParameter("q")}-x"]}""")
        }
        runBlocking {
            val m = SearchModel(fake.client, rest = {})
            val older = async { m.typing("ab") }
            while (fake.asked("GET /api/search/suggest").isEmpty()) delay(10)
            m.typing("abc")
            gate.countDown()
            older.await()
            assertEquals(listOf("abc-x"), m.suggestions)
        }
    }

    @Test
    fun aRemovedRecentSearchStaysGoneWhenAnOlderReadAnswers() = TestHome().use { fake ->
        fake.answer("GET /api/search/recent", recent)
        fake.answer("DELETE /api/search/recent/a") { TestHome.json("", 204) }
        runBlocking {
            val m = SearchModel(fake.client, rest = {})
            m.loadRecent()
            val gate = CountDownLatch(1)
            fake.held("GET /api/search/recent", { recent }, gate)
            val read = async { m.loadRecent() }
            while (fake.asked("GET /api/search/recent").size < 2) delay(10)
            m.forget(m.recent.first())
            assertEquals(listOf("b"), m.recent.map { it.searchId })
            gate.countDown()
            read.await()
            assertEquals(listOf("b"), m.recent.map { it.searchId })
        }
    }

    @Test
    fun clearAllStaysEmpty() = TestHome().use { fake ->
        fake.answer("GET /api/search/recent", recent)
        fake.answer("DELETE /api/search/recent") { TestHome.json("", 204) }
        runBlocking {
            val m = SearchModel(fake.client, rest = {})
            m.loadRecent()
            val gate = CountDownLatch(1)
            fake.held("GET /api/search/recent", { recent }, gate)
            val read = async { m.loadRecent() }
            while (fake.asked("GET /api/search/recent").size < 2) delay(10)
            m.clearRecent()
            gate.countDown()
            read.await()
            assertTrue(m.recent.isEmpty())
        }
    }

    @Test
    fun withSearchByMeaningOffOnlyWordsAreAskedFor() = TestHome().use { fake ->
        fake.answer("POST /api/search", results)
        runBlocking {
            val m = SearchModel(fake.client, meaningOn = { false }, rest = {})
            m.submit("fasting")
            // the home's words-only mode
            assertTrue(fake.asked("POST /api/search").single().body!!.utf8().contains("\"mode\":\"fts\""))
            // an empty query searches nothing
            m.submit("  ")
            assertEquals(1, fake.asked("POST /api/search").size)
        }
    }
}
