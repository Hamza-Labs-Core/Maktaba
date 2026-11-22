package com.hamzalabs.maktaba.core

import com.hamzalabs.maktaba.core.api.DURATION_BANDS
import com.hamzalabs.maktaba.core.api.Facet
import com.hamzalabs.maktaba.core.api.FacetBucket
import com.hamzalabs.maktaba.core.api.FacetChoice
import com.hamzalabs.maktaba.core.api.SearchFacets
import com.hamzalabs.maktaba.core.api.SearchSort
import com.hamzalabs.maktaba.core.api.bandRange
import com.hamzalabs.maktaba.core.api.didYouMean
import com.hamzalabs.maktaba.core.api.languageName
import com.hamzalabs.maktaba.core.api.withLibraries
import com.hamzalabs.maktaba.core.api.yearRange
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.put
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.util.Locale

/**
 * Story 49.11: the rules every app's search shares (the web's `searchShared.tsx`): the filters a
 * facet choice sends, how a second year or band replaces the first, a chosen bucket the narrowed
 * facets lost, the library names kept, did-you-mean worth showing and the names of languages.
 */
class SearchHelpTest {
    private val y2024 = FacetBucket("2024", count = 2, filter = buildJsonObject {
        put("date", buildJsonObject { put("gte", "2024-01-01T00:00:00.000Z"); put("lt", "2025-01-01T00:00:00.000Z") })
    })
    private val y2023 = FacetBucket("2023", count = 1, filter = buildJsonObject {
        put("date", buildJsonObject { put("gte", "2023-01-01T00:00:00.000Z"); put("lt", "2024-01-01T00:00:00.000Z") })
    })

    @Test
    fun nothingChosenSendsNoFilters() {
        val c = FacetChoice()
        assertTrue(c.isEmpty)
        assertNull(c.filters())
        assertTrue(c.chosen.isEmpty())
    }

    @Test
    fun listFacetsCombineAndToggleOff() {
        val c = FacetChoice()
            .toggle(Facet.Library, "L1")
            .toggle(Facet.Library, "L2")
            .toggle(Facet.Language, "ar")
            .toggle(Facet.Speaker, "الشيخ أحمد")
        val f = c.filters()!!
        assertEquals(JsonArray(listOf(JsonPrimitive("L1"), JsonPrimitive("L2"))), f["library_id"])
        assertEquals(JsonArray(listOf(JsonPrimitive("ar"))), f["language"])
        assertEquals(JsonArray(listOf(JsonPrimitive("الشيخ أحمد"))), f["speaker"])
        assertNull(f["date"])
        assertEquals(4, c.count)

        val off = c.toggle(Facet.Library, "L1")
        assertEquals(listOf("L2"), off.library)
        assertFalse(off.has(Facet.Library, "L1"))
        assertEquals("chosen again, it joins the end", listOf("L2", "L1"), off.toggle(Facet.Library, "L1").library)
    }

    @Test
    fun aYearSendsItsBucketsFilterAndASecondReplacesTheFirst() {
        val one = FacetChoice().toggle(Facet.Year, y2024)
        assertEquals(y2024.filter!!["date"], one.filters()!!["date"])
        val two = one.toggle(Facet.Year, y2023)
        assertEquals("2023", two.year)
        assertEquals("one range of each", listOf(Facet.Year to "2023"), two.chosen)
        assertEquals("2023-01-01T00:00:00.000Z", two.filters()!!["date"]!!.jsonObject["gte"]!!.jsonPrimitive.content)
        assertTrue("pressing the chosen year again takes it off", two.toggle(Facet.Year, y2023).isEmpty)
    }

    @Test
    fun aYearOrBandWithoutItsBucketsFilterTakesTheHomesRange() {
        // a home's bucket always carries its filter; one without falls back to the same range
        val y = FacetChoice().toggle(Facet.Year, "2021")
        assertEquals(yearRange("2021"), y.filters()!!["date"])
        assertEquals("2021-01-01T00:00:00.000Z", yearRange("2021")["gte"]!!.jsonPrimitive.content)
        assertEquals("2022-01-01T00:00:00.000Z", yearRange("2021")["lt"]!!.jsonPrimitive.content)

        for (band in DURATION_BANDS) {
            val d = FacetChoice().toggle(Facet.Duration, band)
            assertEquals(band, bandRange(band), d.filters()!!["duration_sec"])
        }
        assertEquals("300", bandRange("5m_20m")!!["gte"]!!.jsonPrimitive.content)
        assertEquals("1200", bandRange("5m_20m")!!["lt"]!!.jsonPrimitive.content)
        assertNull("over an hour has no upper bound", bandRange("over_60m")!!["lt"])
        assertNull(bandRange("forever"))
    }

    @Test
    fun theChosenAreListedFacetByFacetAndRemovable() {
        val c = FacetChoice()
            .toggle(Facet.Duration, "under_5m")
            .toggle(Facet.Speaker, "S")
            .toggle(Facet.Year, y2024)
            .toggle(Facet.Library, "L1")
        assertEquals(
            listOf(Facet.Library to "L1", Facet.Speaker to "S", Facet.Year to "2024", Facet.Duration to "under_5m"),
            c.chosen,
        )
        val less = c.remove(Facet.Year, "2024").remove(Facet.Duration, "under_5m")
        assertNull(less.year)
        assertNull(less.date)
        assertNull(less.filters()!!["date"])
        assertEquals("removing what is not chosen changes nothing", less, less.remove(Facet.Language, "en"))
    }

    @Test
    fun aChosenBucketTheNarrowedFacetsLostStaysFirstWithoutACount() {
        val facets = SearchFacets(language = listOf(FacetBucket("en", count = 4)))
        val c = FacetChoice().toggle(Facet.Language, "ar")
        val shown = c.shown(facets, Facet.Language)
        assertEquals(listOf("ar", "en"), shown.map { it.value })
        assertFalse(shown[0].counted)
        assertTrue(shown[1].counted)
        assertTrue("no facets and nothing chosen: no chips", FacetChoice().shown(null, Facet.Speaker).isEmpty())
    }

    @Test
    fun libraryNamesAreKeptForOnesTheFacetsNoLongerList() {
        val seen = emptyMap<String, String>()
            .withLibraries(SearchFacets(library = listOf(FacetBucket("L1", label = "دروس", count = 2), FacetBucket("L2", label = " ", count = 1))))
            .withLibraries(SearchFacets(library = listOf(FacetBucket("L3", label = "Talks", count = 1))))
            .withLibraries(null)
        assertEquals(mapOf("L1" to "دروس", "L3" to "Talks"), seen)
    }

    @Test
    fun didYouMeanIsShownOnlyWhenItChangesTheQuery() {
        assertEquals("المدرسة", didYouMean("المدرصة", "المدرسة"))
        assertNull(didYouMean("Patience", " patience "))
        assertNull(didYouMean("a  b", "a b"))
        assertNull(didYouMean("x", ""))
        assertNull(didYouMean("x", null))
    }

    @Test
    fun sortsReadTheHomesWord() {
        assertEquals(SearchSort.Newest, SearchSort.of("newest"))
        assertEquals(SearchSort.Matches, SearchSort.of("matches"))
        assertEquals(SearchSort.Relevance, SearchSort.of(""))
        assertEquals(SearchSort.Relevance, SearchSort.of("random"))
    }

    @Test
    fun languagesAreNamedInTheAppsLanguage() {
        assertEquals("Arabic", languageName("ar", Locale.ENGLISH))
        assertEquals("الإنجليزية", languageName("en", Locale.forLanguageTag("ar")))
        assertEquals("a code with no name is shown as it is", "zz", languageName("zz", Locale.ENGLISH))
        assertEquals("", languageName("", Locale.ENGLISH))
    }
}
