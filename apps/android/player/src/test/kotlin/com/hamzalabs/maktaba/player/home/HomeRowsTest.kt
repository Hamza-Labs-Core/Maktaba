package com.hamzalabs.maktaba.player.home

import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.HomeRails
import com.hamzalabs.maktaba.core.api.NowAnswer
import com.hamzalabs.maktaba.core.api.Recommendations
import com.hamzalabs.maktaba.core.browse.HomeRow
import com.hamzalabs.maktaba.core.browse.homeRows
import org.junit.Assert.assertEquals
import org.junit.Test

/**
 * The Android Player's two-page Home (a foldable's book posture). The rows themselves are
 * `:client`'s (`core.browse.HomeRowsTest`).
 */
class HomeRowsTest {
    private val rails = ApiClient.json.decodeFromString(HomeRails.serializer(), HomeFixtures.HOME)
    private val recs = ApiClient.json.decodeFromString(Recommendations.serializer(), HomeFixtures.RECS)
    private val now = ApiClient.json.decodeFromString(NowAnswer.serializer(), HomeFixtures.NOW)

    @Test
    fun likeABookTheRowsKeepTheirOrderAcrossTheTwoPages() {
        val rows = homeRows(rails, recs, now)
        // the hero tops the start page: it takes the smaller half
        val (start, end) = bookPages(rows, hero = true)
        assertEquals(HomeFixtures.ROW_ORDER.take(4), start.map { it.key })
        assertEquals(HomeFixtures.ROW_ORDER.drop(4), end.map { it.key })
        val odd = rows.dropLast(1)
        assertEquals(3, bookPages(odd, hero = true).first.size)
        assertEquals(4, bookPages(odd, hero = false).first.size)
        assertEquals(emptyList<HomeRow>() to emptyList<HomeRow>(), bookPages(emptyList(), hero = true))
    }
}
