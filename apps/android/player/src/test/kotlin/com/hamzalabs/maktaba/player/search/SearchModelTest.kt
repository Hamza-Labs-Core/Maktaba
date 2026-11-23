package com.hamzalabs.maktaba.player.search

import com.hamzalabs.maktaba.core.search.SearchModel
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.hamzalabs.maktaba.player.harness.FakeHome
import com.hamzalabs.maktaba.player.harness.FakeHome.Companion.json
import com.hamzalabs.maktaba.core.api.Facet
import com.hamzalabs.maktaba.core.api.SearchSort
import com.hamzalabs.maktaba.core.api.SuggestKind
import kotlinx.coroutines.Dispatchers
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withContext
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith

/** Search's view model (NFR-21) against the fake home's `/api/search*` answers. */
@RunWith(AndroidJUnit4::class)
class SearchModelTest {
    @get:Rule val home = FakeHome()

    private fun model(): SearchModel {
        SearchFixtures.install(home)
        return SearchModel(home.home.client)
    }

    @Test
    fun submitKeepsBothGroupsAndRefreshesTheRecentSearches() = runBlocking {
        val m = model()
        m.submit("  patience ")
        val a = m.answer!!
        assertEquals(listOf("v1", "v2"), a.titles.map { it.videoId })
        assertEquals(listOf("v4"), a.transcripts.map { it.videoId })
        assertEquals(1322.5, a.transcripts[0].passages[1].startSec, 0.0)
        assertFalse(a.moreTitles)
        assertFalse(m.searching)
        assertNull(m.problem)
        assertEquals(2, m.recent.size)
        assertTrue("recent asked after the search", home.paths.indexOf("/api/search/recent") > home.paths.indexOf("/api/search"))
    }

    @Test
    fun thePageBesideTheResultsShowsTheFirstUntilOneIsChosenAndANewSearchForgetsIt() = runBlocking {
        val m = model()
        assertNull("nothing before a search", m.chosen)
        m.submit("patience")
        assertNull(m.chosen)
        assertEquals("the first title", "v1", m.answer!!.first)
        m.chosen = "v2"
        assertEquals("v2", m.chosen)
        m.submit("patience again")
        assertNull("a new search forgets the chosen one", m.chosen)
    }

    @Test
    fun withoutTitlesTheFirstIsTheFirstTranscriptsVideo() = runBlocking {
        SearchFixtures.install(home) { SearchFixtures.results(SearchFixtures.EN.copy(titles = emptyList())) }
        val m = SearchModel(home.home.client)
        m.submit("patience")
        assertEquals("v4", m.answer!!.first)
        home.answer("/api/search") { json(SearchFixtures.EMPTY) }
        m.submit("nothing")
        assertNull("nothing found, nothing to show", m.answer!!.first)
    }

    @Test
    fun blankAsksNothing() = runBlocking {
        val m = model()
        m.submit("   ")
        m.suggest(" ")
        assertNull(m.answer)
        assertTrue(m.suggestions.isEmpty())
        assertTrue(home.paths.isEmpty())
    }

    @Test
    fun moreTitlesAppendsTheNextPage() = runBlocking {
        SearchFixtures.install(home) { SearchFixtures.results(SearchFixtures.EN, titlesTotal = 3) }
        val m = SearchModel(home.home.client)
        m.submit("patience")
        assertTrue(m.answer!!.moreTitles)
        m.moreTitles()
        assertEquals(listOf("v1", "v2", "v3"), m.answer!!.titles.map { it.videoId })
        assertFalse(m.answer!!.moreTitles)
    }

    @Test
    fun aFailureIsKeptInWordsAndRetrySendsTheSameQuery() = runBlocking {
        val m = model()
        home.answer("/api/search") { json("""{"error":"boom"}""", 500) }
        m.submit("patience")
        assertNotNull(m.problem)
        assertNull(m.answer)
        SearchFixtures.install(home)
        m.retry()
        assertNull(m.problem)
        assertEquals(2, m.answer!!.titles.size)
    }

    @Test
    fun forgetAndClearDeleteOnTheHomeThenHere() = runBlocking {
        val m = model()
        m.loadRecent()
        m.forget(m.recent[1])
        assertEquals(listOf("patience"), m.recent.map { it.q })
        m.clearRecent()
        assertTrue(m.recent.isEmpty())
        val deletes = home.requests.filter { it.method == "DELETE" }.map { it.url.encodedPath }
        assertEquals(listOf("/api/search/recent/r1", "/api/search/recent"), deletes)
    }

    /**
     * The recent searches read again (held: answered as the home had them when asked) while
     * [change] is saved on the home: the read's older answer must not put back what it removed.
     */
    private fun whileRecentIsRead(change: suspend (SearchModel) -> Unit): SearchModel = runBlocking {
        val m = model()
        m.loadRecent()
        assertEquals(2, m.recent.size)
        val recent = SearchFixtures.recent(SearchFixtures.EN)
        val held = home.hold("/api/search/recent") { json(if (it.method == "DELETE") "" else recent, if (it.method == "DELETE") 204 else 200) }
        val read = launch { m.loadRecent() }
        withContext(Dispatchers.IO) { held.awaitAsked() }
        change(m)
        held.release()
        read.join()
        m
    }

    @Test
    fun aRecentSearchForgottenWhileTheyAreReadAgainStaysForgotten() {
        val m = whileRecentIsRead { it.forget(it.recent[1]) }
        assertTrue(home.requests.any { it.method == "DELETE" && it.url.encodedPath == "/api/search/recent/r1" })
        assertEquals(listOf("patience"), m.recent.map { it.q })
    }

    @Test
    fun clearAllWhileTheRecentSearchesAreReadAgainStaysCleared() {
        val m = whileRecentIsRead { it.clearRecent() }
        assertTrue(home.requests.any { it.method == "DELETE" && it.url.encodedPath == "/api/search/recent" })
        assertTrue(m.recent.isEmpty())
    }

    @Test
    fun aSignedOutAnswerSignsOut() = runBlocking {
        var out = false
        SearchFixtures.install(home)
        home.answer("/api/search") { json("""{"error":"unauthorized"}""", 401) }
        // and the home refuses the refresh token too
        home.answer("/api/auth/refresh") { json("""{"error":"unauthorized"}""", 401) }
        val m = SearchModel(home.home.client) { out = true }
        m.submit("patience")
        assertTrue(out)
    }

    // ---------- story 49.11: suggestions' kinds, did-you-mean, facets, sort ----------

    private fun bodies(): List<JsonObject> =
        home.requests.filter { it.method == "POST" && it.url.encodedPath == "/api/search" }.map { SearchFixtures.body(it) }

    @Test
    fun suggestionsCarryTheirKindsAndATitleItsVideo() = runBlocking {
        val m = model()
        m.suggest("pa")
        assertEquals(listOf(SuggestKind.History, SuggestKind.Term, SuggestKind.Title, SuggestKind.Speaker), m.suggestions.map { it.kind })
        assertEquals(listOf(null, null, "v1", null), m.suggestions.map { it.opensVideo })
    }

    @Test
    fun anOlderHomesSuggestionsHaveNoKind() = runBlocking {
        SearchFixtures.install(home, kinds = false)
        val m = SearchModel(home.home.client)
        m.suggest("pa")
        assertEquals(listOf("patience in hardship"), m.suggestions.map { it.text })
        assertNull(m.suggestions.single().kind)
    }

    @Test
    fun theAnswerKeepsItsFacetsAndOrderAndAnOlderHomesHasNone() = runBlocking {
        val m = model()
        m.submit("patience")
        val a = m.answer!!
        assertEquals(SearchSort.Relevance, a.sort)
        assertNull(a.didYouMean)
        assertEquals(listOf("l1", "l2"), a.facets!!.of(Facet.Library).map { it.value })
        assertEquals(mapOf("l1" to "Lessons", "l2" to "Talks"), m.libraryNames)
        assertNull("nothing chosen, no filters sent", bodies().single()["filters"])
        assertNull("relevance is not sent", bodies().single()["sort"])

        SearchFixtures.install(home) { SearchFixtures.results(SearchFixtures.EN, help = false) }
        m.submit("patience again")
        assertNull(m.answer!!.facets)
        assertNull(m.answer!!.didYouMean)
        assertEquals(SearchSort.Relevance, m.answer!!.sort)
    }

    @Test
    fun facetChoicesBuildTheFiltersAndRunTheSearchAgain() = runBlocking {
        val m = model()
        m.submit("patience")
        val f = m.answer!!.facets!!
        m.toggle(Facet.Library, f.of(Facet.Library)[1])
        m.toggle(Facet.Speaker, f.of(Facet.Speaker)[0])
        m.toggle(Facet.Year, f.of(Facet.Year)[0])
        m.toggle(Facet.Duration, f.of(Facet.Duration)[1])
        val sent = bodies().last()["filters"]!!.jsonObject
        assertEquals("patience", bodies().last()["q"]!!.jsonPrimitive.content)
        assertEquals(JsonArray(listOf(JsonPrimitive("l2"))), sent["library_id"])
        assertEquals(JsonArray(listOf(JsonPrimitive("Shaykh Ahmad"))), sent["speaker"])
        assertEquals("the year's own filter", "2024-01-01T00:00:00.000Z", sent["date"]!!.jsonObject["gte"]!!.jsonPrimitive.content)
        assertEquals("3600", sent["duration_sec"]!!.jsonObject["gte"]!!.jsonPrimitive.content)
        assertEquals(5, bodies().size)

        // a second year replaces the first; a filter in use comes off; Clear all takes the rest
        m.toggle(Facet.Year, f.of(Facet.Year)[1])
        assertEquals("2023-01-01T00:00:00.000Z", bodies().last()["filters"]!!.jsonObject["date"]!!.jsonObject["gte"]!!.jsonPrimitive.content)
        m.remove(Facet.Library, "l2")
        assertNull(bodies().last()["filters"]!!.jsonObject["library_id"])
        m.clearFilters()
        assertNull(bodies().last()["filters"])
        assertTrue(m.choice.isEmpty)
    }

    @Test
    fun newWordsKeepTheOrderAndDropTheFiltersTheSameWordsKeepBoth() = runBlocking {
        val m = model()
        m.submit("patience")
        m.sortBy(SearchSort.Newest)
        assertEquals("newest", bodies().last()["sort"]!!.jsonPrimitive.content)
        assertEquals("the answer's order is shown", SearchSort.Newest, m.answer!!.sort)
        m.toggle(Facet.Language, m.answer!!.facets!!.of(Facet.Language)[0])

        m.submit("patience")
        assertEquals("the same words keep the filters", JsonArray(listOf(JsonPrimitive("en"))), bodies().last()["filters"]!!.jsonObject["language"])

        m.submit("journey")
        assertNull("new words drop the filters", bodies().last()["filters"])
        assertEquals("and keep the order", "newest", bodies().last()["sort"]!!.jsonPrimitive.content)
        assertTrue(m.choice.isEmpty)
    }

    @Test
    fun didYouMeanRunsTheCorrectionWithTheFiltersKept() = runBlocking {
        val m = model()
        m.submit("patiance")
        assertEquals("patience", m.answer!!.didYouMean)
        m.toggle(Facet.Library, m.answer!!.facets!!.of(Facet.Library)[0])
        m.acceptDidYouMean()
        assertEquals("patience", bodies().last()["q"]!!.jsonPrimitive.content)
        assertEquals(JsonArray(listOf(JsonPrimitive("l1"))), bodies().last()["filters"]!!.jsonObject["library_id"])
        assertNull("the correction is not offered for itself", m.answer!!.didYouMean)
    }

    @Test
    fun aChosenLibraryTheNarrowedFacetsLostKeepsItsName() = runBlocking {
        val m = model()
        m.submit("patience")
        m.toggle(Facet.Library, m.answer!!.facets!!.of(Facet.Library)[1])
        // the narrowed answer has no facets (an older home's, or nothing found)
        SearchFixtures.install(home) { SearchFixtures.results(SearchFixtures.EN, help = false) }
        m.toggle(Facet.Speaker, com.hamzalabs.maktaba.core.api.FacetBucket("Shaykh Ahmad", count = 2))
        assertEquals("Talks", m.libraryNames["l2"])
        val shown = m.choice.shown(m.answer!!.facets, Facet.Library)
        assertEquals(listOf("l2"), shown.map { it.value })
        assertFalse(shown.single().counted)
    }

    @Test
    fun anOlderWordsSuggestionsAnsweringLateAreDropped() = runBlocking {
        val m = model()
        val held = home.hold("/api/search/suggest") { r -> json("""{"suggestions":["${r.url.queryParameter("q")}-x"]}""") }
        val older = launch(Dispatchers.IO) { m.suggest("pa") }
        withContext(Dispatchers.IO) { held.awaitAsked() }
        m.suggest("pat")
        assertEquals(listOf("pat-x"), m.suggestions.map { it.text })
        held.release()
        older.join()
        assertEquals("the older word's answer is dropped", listOf("pat-x"), m.suggestions.map { it.text })
    }
}
