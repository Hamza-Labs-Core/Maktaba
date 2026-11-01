package com.hamzalabs.maktaba.player.home

import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.HomeRails
import com.hamzalabs.maktaba.core.api.NowAnswer
import com.hamzalabs.maktaba.core.api.Recommendations
import com.hamzalabs.maktaba.player.shell.Route
import okhttp3.HttpUrl.Companion.toHttpUrl
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder

/** FR-10's order and hiding, FR-11's card actions, FR-13's empty home and FR-12's cache, without a screen. */
class HomeRowsTest {
    @get:Rule val folder = TemporaryFolder()

    private val rails = ApiClient.json.decodeFromString(HomeRails.serializer(), HomeFixtures.HOME)
    private val recs = ApiClient.json.decodeFromString(Recommendations.serializer(), HomeFixtures.RECS)
    private val now = ApiClient.json.decodeFromString(NowAnswer.serializer(), HomeFixtures.NOW)

    @Test
    fun theRowsAreInFr10sOrderAndAnEmptyRowIsAbsent() {
        val rows = homeRows(rails, recs, now)
        assertEquals(HomeFixtures.ROW_ORDER, rows.map { it.key })
        assertTrue("Favorites is empty", rows.none { it.key == KEY_FAVORITES })
    }

    @Test
    fun withoutRecommendationsOrLiveTvTheirRowsAreAbsent() {
        val rows = homeRows(rails, recs = null, now = null)
        assertEquals(HomeFixtures.ROW_ORDER - setOf(KEY_LIVE, KEY_FOR_YOU, KEY_FROM_LIBRARY), rows.map { it.key })
        val noChannels = ApiClient.json.decodeFromString(NowAnswer.serializer(), """{"channels":[]}""")
        assertTrue(homeRows(rails, null, noChannels).none { it.key == KEY_LIVE })
    }

    @Test
    fun continueWatchingResumesAtThePositionAndRecommendationsOfferNotInterested() {
        val rows = homeRows(rails, recs, now).filterIsInstance<HomeRow.Cards>().associateBy { it.key }
        val cw = rows.getValue(KEY_CONTINUE).cards.single()
        assertEquals(600, cw.resumeAtSec)
        assertEquals(Route.Video(HomeFixtures.CONTINUE), cw.open)
        assertEquals(0.25f, cw.progress!!, 0.001f)
        assertFalse(cw.recommended)

        val forYou = rows.getValue(KEY_FOR_YOU).cards
        assertEquals(listOf(HomeFixtures.REC_1, HomeFixtures.REC_2), forYou.map { it.videoId })
        assertTrue(forYou.all { it.recommended && it.resumeAtSec == null })
        assertNull("not read yet: the screen asks", forYou.first().title)
        // the home's Continue Watching rail is Home's own row already: not shown twice
        assertTrue(rows.keys.none { it == "rec-continue_watching" })
    }

    @Test
    fun recentlyAddedGroupsASeriesThatOpensTheSeries() {
        val added = homeRows(rails, recs, now).filterIsInstance<HomeRow.Cards>().first { it.key == KEY_ADDED }.cards
        val series = added.first()
        assertEquals(Route.Series(HomeFixtures.FEATURED_SERIES), series.open)
        assertEquals(3, series.newLessons)
        assertEquals("series:${HomeFixtures.FEATURED_SERIES}", series.key)
        assertEquals(Route.Video(HomeFixtures.ADDED), added[1].open)
    }

    @Test
    fun aPinnedLibrarysRowPutsWhatIsInProgressFirstAndOpensTheLibrary() {
        val pinned = homeRows(rails, recs, now).filterIsInstance<HomeRow.Cards>().first { it.key.startsWith(KEY_PINNED) }
        assertEquals(RowTitle.Named("Lectures"), pinned.title)
        assertEquals(listOf(HomeFixtures.CONTINUE, HomeFixtures.PINNED_VIDEO), pinned.cards.map { it.videoId })
        assertEquals(Route.Library(HomeFixtures.PINNED_LIB), pinned.seeAll)
    }

    @Test
    fun aHomeWithNothingIsEmpty() {
        val empty = ApiClient.json.decodeFromString(HomeRails.serializer(), HomeFixtures.EMPTY_HOME)
        assertTrue(isEmptyHome(empty, homeRows(empty, null, null)))
        assertFalse(isEmptyHome(rails, homeRows(rails, recs, now)))
    }

    @Test
    fun theCacheKeepsTheLastAnswerPerHomeAndPerson() {
        val a = HomeCache.of(folder.root, "https://home.test/".toHttpUrl(), "u1")
        val b = HomeCache.of(folder.root, "https://home.test/".toHttpUrl(), "u2")
        assertNull(a.read())
        a.write(CachedHome(rails, recs))
        assertEquals(rails, a.read()?.rails)
        assertEquals(recs, a.read()?.recs)
        assertNull("another person on the same home", b.read())
        // a file that cannot be read is no cache
        folder.root.resolve(HomeCache.FOLDER).listFiles()!!.single { it.name.endsWith(".json") }.writeText("{not json")
        assertNull(a.read())
    }
}
