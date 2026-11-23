package com.hamzalabs.maktaba.desktop.search

import com.hamzalabs.maktaba.core.api.Facet
import com.hamzalabs.maktaba.core.api.SearchSort
import com.hamzalabs.maktaba.core.api.SuggestKind
import com.hamzalabs.maktaba.desktop.TestHome
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.coroutines.async
import kotlinx.coroutines.delay
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
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
            assertEquals(listOf("fasting", "fasting rules"), m.suggestions.map { it.text })
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
            assertEquals(listOf("abc-x"), m.suggestions.map { it.text })
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

    // ---------- story 49.11: suggestions' kinds, did-you-mean, facets, sort ----------

    private val helped = """{"search_id":"s1","q":"fasting","sort":"relevance","videos":[{"video_id":"v1","title":"Fasting 1","library_id":"l1"}],"videos_total":1,
        "transcript_videos":[],"transcript_total":0,
        "facets":{"library":[{"value":"l1","label":"Lessons","count":1},{"value":"l2","label":"Talks","count":1}],"language":[{"value":"en","count":1}],
          "speaker":[{"value":"Shaykh Ahmad","count":1}],
          "year":[{"value":"2024","count":1,"filter":{"date":{"gte":"2024-01-01T00:00:00.000Z","lt":"2025-01-01T00:00:00.000Z"}}}],
          "duration":[{"value":"under_5m","count":1,"filter":{"duration_sec":{"gte":0,"lt":300}}}],"videos":1}}"""

    /** A 49.8 home: the order asked comes back, and did-you-mean for "fastign". */
    private fun helping(fake: TestHome) = fake.answer("POST /api/search") { r ->
        val body = Json.parseToJsonElement(r.body!!.utf8()).jsonObject
        val q = body["q"]!!.jsonPrimitive.content
        val sort = body["sort"]?.jsonPrimitive?.content ?: "relevance"
        TestHome.json(helped.replace("\"q\":\"fasting\"", "\"q\":\"$q\"").replace("\"sort\":\"relevance\"", "\"sort\":\"$sort\"" + if (q == "fastign") ",\"did_you_mean\":\"fasting\"" else ""))
    }

    private fun bodies(fake: TestHome): List<JsonObject> = fake.asked("POST /api/search").map { Json.parseToJsonElement(it.body!!.utf8()).jsonObject }

    @Test
    fun suggestionsCarryTheirKindsAndATitleOpensItsVideoOnEnter() = TestHome().use { fake ->
        fake.answer("GET /api/search/suggest", """{"suggestions":["fasting","Fasting 1","Shaykh Ahmad"],"items":[{"text":"fasting","kind":"history"},
            {"text":"Fasting 1","kind":"title","video_id":"v1"},{"text":"Shaykh Ahmad","kind":"speaker"}]}""")
        runBlocking {
            val m = SearchModel(fake.client, rest = {})
            m.typing("fa")
            assertEquals(listOf(SuggestKind.History, SuggestKind.Title, SuggestKind.Speaker), m.suggestions.map { it.kind })
            val opened = mutableListOf<String>()
            m.move(1)
            m.move(1)
            m.enter { opened += it }
            assertEquals("a title opens its video", listOf("v1"), opened)
            assertTrue("and is not searched", fake.asked("POST /api/search").isEmpty())
            assertTrue(m.suggestions.isEmpty())
        }
    }

    @Test
    fun facetsSortAndDidYouMeanRunTheSearchAgain() = TestHome().use { fake ->
        helping(fake)
        runBlocking {
            val m = SearchModel(fake.client, rest = {})
            m.submit("fastign")
            assertEquals("fasting", m.answer!!.didYouMean)
            val f = m.answer!!.facets!!
            m.toggle(Facet.Library, f.of(Facet.Library)[1])
            m.toggle(Facet.Duration, f.of(Facet.Duration)[0])
            m.sortBy(SearchSort.Matches)
            val last = bodies(fake).last()
            assertEquals("matches", last["sort"]!!.jsonPrimitive.content)
            assertEquals("[\"l2\"]", last["filters"]!!.jsonObject["library_id"].toString())
            assertEquals("300", last["filters"]!!.jsonObject["duration_sec"]!!.jsonObject["lt"]!!.jsonPrimitive.content)
            assertEquals(SearchSort.Matches, m.answer!!.sort)

            m.acceptDidYouMean()
            assertEquals("fasting", bodies(fake).last()["q"]!!.jsonPrimitive.content)
            assertEquals("did-you-mean keeps the filters", "[\"l2\"]", bodies(fake).last()["filters"]!!.jsonObject["library_id"].toString())
            assertNull(m.answer!!.didYouMean)

            m.remove(Facet.Library, "l2")
            m.clearFilters()
            assertNull(bodies(fake).last()["filters"])

            m.toggle(Facet.Language, f.of(Facet.Language)[0])
            m.submit("prayer")
            assertNull("new words drop the filters", bodies(fake).last()["filters"])
            assertEquals("and keep the order", "matches", bodies(fake).last()["sort"]!!.jsonPrimitive.content)
        }
    }

    @Test
    fun anOlderHomesAnswerHasNoHelp() = TestHome().use { fake ->
        fake.answer("POST /api/search", results)
        runBlocking {
            val m = SearchModel(fake.client, rest = {})
            m.submit("fasting")
            assertNull(m.answer!!.facets)
            assertNull(m.answer!!.didYouMean)
            assertEquals(SearchSort.Relevance, m.answer!!.sort)
            assertNull("relevance is not sent", bodies(fake).single()["sort"])
        }
    }

    @Test
    fun resultsIsTheTabAndASearchNeverAsksAiMode() = TestHome().use { fake ->
        // story 49.14: AI mode asks only when its tab is opened or asked from
        fake.answer("POST /api/search", """{"search_id":"s1","q":"fasting","videos":[],"videos_total":0,"transcript_videos":[],"transcript_total":0}""")
        val m = SearchModel(fake.client, rest = {})
        runBlocking { m.submit("fasting") }
        assertEquals(SearchTab.Results, m.tab)
        assertTrue(fake.asked("POST /api/search/ai").isEmpty())
        assertTrue(m.ai.state.value.isEmpty)
    }
}
