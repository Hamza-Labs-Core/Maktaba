package com.hamzalabs.maktaba.tv

import com.hamzalabs.maktaba.core.api.GuideRow
import com.hamzalabs.maktaba.core.api.LiveChannel
import com.hamzalabs.maktaba.core.api.Programme
import com.hamzalabs.maktaba.tv.ui.screens.GUIDE_SPAN_MS
import com.hamzalabs.maktaba.tv.ui.screens.GuideFocus
import com.hamzalabs.maktaba.tv.ui.screens.GuideKey
import com.hamzalabs.maktaba.tv.ui.screens.GuideNav
import com.hamzalabs.maktaba.tv.ui.screens.floorHalfHour
import com.hamzalabs.maktaba.tv.ui.screens.guideRows
import com.hamzalabs.maktaba.tv.ui.screens.stepChannel
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Test
import java.time.Instant

/** The Android TV guide's D-pad focus moves (48.35), English and Arabic, without a TV. */
class LiveGuideTest {
    private val t0 = Instant.parse("2026-11-01T18:00:00Z").toEpochMilli()
    private val min = 60_000L
    private val end = t0 + 6 * 60 * min

    private fun at(m: Long) = Instant.ofEpochMilli(t0 + m * min).toString()
    private fun p(ch: String, from: Long, to: Long, title: String, kind: String = "program") =
        Programme(channelId = ch, kind = kind, start = at(from), stop = at(to), title = title)

    // channel 1: half-hour shows; channel 2: one long film then a show; channel 3: nothing
    private val rows = guideRows(
        listOf(
            GuideRow(LiveChannel("c2", number = 2, name = "Two"), listOf(p("c2", -20, 100, "Film"), p("c2", 100, 400, "Late"))),
            GuideRow(LiveChannel("c3", number = 3, name = "Three")),
            GuideRow(
                LiveChannel("c1", number = 1, name = "One"),
                (0 until 12).map { p("c1", it * 30L, it * 30L + 28, "Show $it") } +
                    (0 until 12).map { p("c1", it * 30L + 28, it * 30L + 30, "bumper", kind = "bumper") },
            ),
        ),
        t0,
        end,
    )
    private val nav = GuideNav(rows, t0)

    private fun title(f: GuideFocus?) = nav.cell(f!!)?.programme?.title

    @Test
    fun `rows come in channel order and cover the window without gaps`() {
        assertEquals(listOf("c1", "c2", "c3"), rows.map { it.channel.id })
        for (r in rows) {
            assertEquals(t0, r.cells.first().startMs)
            assertEquals(end, r.cells.last().stopMs)
            r.cells.zipWithNext().forEach { (a, b) -> assertEquals(a.stopMs, b.startMs) }
        }
        // the bumpers fold into the show before them; the film is clipped to the window
        assertEquals(12, rows[0].cells.size)
        assertEquals(t0 + 30 * min, rows[0].cells[0].stopMs)
        assertEquals("Film", rows[1].cells[0].programme?.title)
        assertEquals(t0, rows[1].cells[0].startMs)
        // a channel with nothing scheduled is one empty cell, so it can still be focused and tuned
        assertEquals(1, rows[2].cells.size)
        assertNull(rows[2].cells[0].programme)
    }

    @Test
    fun `focus starts on the first channel's programme on air now`() {
        val f = nav.start(t0 + 45 * min)!!
        assertEquals(0, f.row)
        assertEquals("Show 1", title(f))
        assertEquals(t0, f.viewStartMs)
    }

    @Test
    fun `right moves later in English, left earlier, and the first cell is an edge`() {
        val f = nav.start(t0)!!
        val later = nav.move(f, GuideKey.Right, rtl = false)
        assertEquals("Show 1", title(later))
        assertEquals("Show 0", title(nav.move(later!!, GuideKey.Left, rtl = false)))
        assertNull(nav.move(f, GuideKey.Left, rtl = false))
    }

    @Test
    fun `in Arabic the timeline runs right to left, so left is later`() {
        val f = nav.start(t0)!!
        val later = nav.move(f, GuideKey.Left, rtl = true)
        assertEquals("Show 1", title(later))
        assertEquals("Show 0", title(nav.move(later!!, GuideKey.Right, rtl = true)))
        assertNull(nav.move(f, GuideKey.Right, rtl = true))
    }

    @Test
    fun `down and up keep the moment, not the cell number`() {
        // on channel 1's 19:30 show, down lands on the film that is on then, and up comes back
        val f = nav.move(nav.start(t0)!!, GuideKey.Right, rtl = false)!!
        val down = nav.move(f, GuideKey.Down, rtl = false)!!
        assertEquals(1, down.row)
        assertEquals("Film", title(down))
        assertEquals("Show 1", title(nav.move(down, GuideKey.Up, rtl = false)))
        // through the empty channel and back, the moment holds
        val empty = nav.move(down, GuideKey.Down, rtl = false)!!
        assertEquals(2, empty.row)
        assertNull(nav.cell(empty)!!.programme)
        assertEquals("Film", title(nav.move(empty, GuideKey.Up, rtl = false)))
    }

    @Test
    fun `up from the first channel and down from the last are edges`() {
        val f = nav.start(t0)!!
        assertNull(nav.move(f, GuideKey.Up, rtl = false))
        val last = nav.start(t0, row = 2)!!
        assertNull(nav.move(last, GuideKey.Down, rtl = false))
    }

    @Test
    fun `moving past the view's end scrolls it by half hours, and back again`() {
        var f = nav.start(t0)!!
        repeat(4) { f = nav.move(f, GuideKey.Right, rtl = false)!! }
        // 20:00 is the end of the two-hour view: it now starts at 20:00
        assertEquals("Show 4", title(f))
        assertEquals(t0 + 120 * min, f.viewStartMs)
        assertEquals(t0 + 120 * min, f.anchorMs)
        // down keeps the moment: the film ended at 19:40, so the late show
        assertEquals("Late", title(nav.move(f, GuideKey.Down, rtl = false)))
        repeat(4) { f = nav.move(f, GuideKey.Left, rtl = false)!! }
        assertEquals("Show 0", title(f))
        assertEquals(t0, f.viewStartMs)
    }

    @Test
    fun `an earlier programme off the view scrolls back, and down onto a long one keeps the view`() {
        // from the late show (20:20 to 24:00) in 21:00's view, left is the film, off the view
        val f = GuideFocus(row = 1, cell = 1, anchorMs = t0 + 210 * min, viewStartMs = t0 + 180 * min)
        val back = nav.move(f, GuideKey.Left, rtl = false)!!
        assertEquals("Film", title(back))
        assertEquals(floorHalfHour(t0), back.viewStartMs)
        val f2 = GuideFocus(row = 0, cell = 6, anchorMs = t0 + 180 * min, viewStartMs = t0 + 180 * min)
        val down = nav.move(f2, GuideKey.Down, rtl = false)!!
        assertEquals("Late", title(down))
        assertEquals(t0 + 180 * min, down.viewStartMs)
        assertEquals(GUIDE_SPAN_MS, nav.spanMs)
    }

    @Test
    fun `no channels, no focus`() {
        assertNull(GuideNav(emptyList(), t0).start(t0))
        assertNotNull(nav.start(t0, row = 99))
    }

    @Test
    fun `channel up and down in the player wrap round`() {
        val ids = listOf("c1", "c2", "c3")
        assertEquals("c2", stepChannel(ids, "c1", 1))
        assertEquals("c1", stepChannel(ids, "c3", 1))
        assertEquals("c3", stepChannel(ids, "c1", -1))
        assertEquals("c1", stepChannel(ids, "gone", 1))
        assertNull(stepChannel(emptyList(), "c1", 1))
    }
}
