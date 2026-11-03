package com.hamzalabs.maktaba.player.libraries

import androidx.test.ext.junit.runners.AndroidJUnit4
import com.hamzalabs.maktaba.player.harness.FakeHome
import com.hamzalabs.maktaba.player.harness.FakeHome.Companion.json
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith

/**
 * A library's grid read again for the same facets and sort (the page shown again: another
 * section and back, a fold) updates in place: the pages scrolled to stay, a changed video is
 * replaced where it is, a new one comes in at the top. Reading the first page alone cut the grid
 * to it, and the grid's scroll position with it.
 */
@RunWith(AndroidJUnit4::class)
class ShownAgainTest {
    @get:Rule val home = FakeHome()

    private fun v(id: String, title: String = "Video $id") =
        """{"id":"$id","library_id":"l1","filename":"$id.mkv","title":"$title","duration_sec":60}"""

    @Test
    fun theGridReadAgainKeepsItsPagesAndUpdatesInPlace() = runBlocking {
        var first = """{"items":[${v("v1")},${v("v2")},${v("v3")}],"next":"c1"}"""
        home.answer("/api/videos") { r ->
            if (r.url.queryParameter("cursor") == "c1") json("""{"items":[${v("v4")},${v("v5")}],"next":null}""") else json(first)
        }
        val state = LibraryState(home.home.client, "l1") {}
        state.reload()
        state.loadMore()
        assertEquals(listOf("v1", "v2", "v3", "v4", "v5"), state.videos.map { it.id })

        // shown again: v2 renamed, v0 new at the top
        first = """{"items":[${v("v0")},${v("v1")},${v("v2", "Renamed")}],"next":"c1"}"""
        state.reload()
        assertEquals(listOf("v0", "v1", "v2", "v3", "v4", "v5"), state.videos.map { it.id })
        assertEquals("Renamed", state.videos.first { it.id == "v2" }.displayTitle)
    }

    @Test
    fun anotherSortReadsTheGridAnew() = runBlocking {
        home.answer("/api/videos") { r ->
            if (r.url.queryParameter("sort") == "title") json("""{"items":[${v("a")}],"next":null}""")
            else json("""{"items":[${v("v1")},${v("v2")}],"next":null}""")
        }
        val state = LibraryState(home.home.client, "l1") {}
        state.reload()
        state.sort = LibrarySort.Title
        state.reload()
        assertEquals(listOf("a"), state.videos.map { it.id })
    }

    @Test
    fun mergeFirstPageKeepsTheRest() {
        val shown = mutableListOf("a", "b", "c", "d")
        mergeFirstPage(shown, listOf("z", "a", "b")) { it }
        assertEquals(listOf("z", "a", "b", "c", "d"), shown)
    }
}
