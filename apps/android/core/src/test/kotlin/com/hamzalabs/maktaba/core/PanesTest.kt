package com.hamzalabs.maktaba.core

import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.QueueSnapshot
import com.hamzalabs.maktaba.core.api.StreamSession
import com.hamzalabs.maktaba.core.layout.Fold
import com.hamzalabs.maktaba.core.layout.Panes
import com.hamzalabs.maktaba.core.layout.cut
import com.hamzalabs.maktaba.core.layout.panesFor
import okhttp3.HttpUrl.Companion.toHttpUrl
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class PanesTest {
    @Test
    fun aPhoneOrAFoldedFoldableShowsOnePane() {
        assertEquals(Panes.One, panesFor(411, null))
        assertEquals("open flat but narrow", Panes.One, panesFor(500, Fold(halfOpen = false, vertical = true, start = 0, end = 0)))
    }

    @Test
    fun aTabletOrAnOpenFoldableShowsTheListBesideThePage() {
        assertEquals(Panes.ListDetail, panesFor(600, null))
        assertEquals(Panes.ListDetail, panesFor(1280, null))
        assertEquals(Panes.ListDetail, panesFor(841, Fold(halfOpen = false, vertical = true, start = 1100, end = 1100)))
    }

    @Test
    fun halfFoldedSplitsAtTheHinge() {
        val tabletop = panesFor(841, Fold(halfOpen = true, vertical = false, start = 1100, end = 1140))
        assertEquals(Panes.Tabletop(1100..1140), tabletop)
        assertTrue(tabletop.split)
        val book = panesFor(400, Fold(halfOpen = true, vertical = true, start = 900, end = 900))
        assertEquals("even a narrow window splits when held like a book", Panes.Book(900..900), book)
        assertFalse(Panes.ListDetail.split)
    }

    @Test
    fun cutsAPaneAtTheHinge() {
        assertEquals(1000 to 1040, cut(1100..1140, 100, 2000))
        assertEquals("a hinge outside the pane: halves", 500 to 500, cut(3000..3040, 0, 1000))
        assertEquals("not measured yet: halves", 0 to 0, cut(10..20, 0, 0))
    }

    @Test
    fun aStreamSessionPlaysFromTheHome() {
        val home = "http://192.168.50.177:8088".toHttpUrl()
        val hls = StreamSession("s1", "transcode", manifestUrl = "/stream/s1/master.m3u8?sig=a", expiresAt = "2026-10-25T12:00:00.5Z")
        assertEquals("http://192.168.50.177:8088/stream/s1/master.m3u8?sig=a", hls.playbackUrl(home).toString())
        assertTrue(hls.isHls)
        assertEquals(1792929600500L, hls.expiresMillis)
        val direct = StreamSession("s2", "direct", directUrl = "/stream/s2/file?sig=b", manifestUrl = "/stream/s2/m.m3u8")
        assertEquals("/stream/s2/file", direct.playbackUrl(home)!!.encodedPath)
        assertFalse(direct.isHls)
    }

    @Test
    fun theQueueShowsVisibleStagesWithTheirCounts() {
        val snap = ApiClient.json.decodeFromString(
            QueueSnapshot.serializer(),
            """{"paused_all":false,"stages":[{"stage":"probe","label":"Probe","paused":false,"hidden":false},
               {"stage":"legacy","label":"Old","hidden":true},{"stage":"transcribe","label":"","paused":true}],
               "queues":[{"stage":"probe","counts":{"ready":4,"running":1,"failed_open":2,"done_24h":9},"rate_per_min":1.5}],
               "issues_count":2,"health":{"cpu":{"pct":12}}}""",
        )
        assertEquals(listOf("probe", "transcribe"), snap.rows.map { it.id })
        assertEquals(4, snap.rows[0].counts.ready)
        assertEquals("transcribe", snap.rows[1].label)
        assertTrue(snap.rows[1].stage.paused)
    }
}
