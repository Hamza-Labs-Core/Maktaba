package com.hamzalabs.maktaba.player.search

import androidx.test.ext.junit.runners.AndroidJUnit4
import com.hamzalabs.maktaba.player.harness.FakeHome
import com.hamzalabs.maktaba.player.harness.FakeHome.Companion.json
import kotlinx.coroutines.runBlocking
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
}
