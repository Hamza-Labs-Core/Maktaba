package com.hamzalabs.maktaba.core

import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.HomeStatusDoc
import com.hamzalabs.maktaba.core.api.StatusStripRules
import com.hamzalabs.maktaba.core.api.StripKey
import com.hamzalabs.maktaba.core.api.StripLine
import com.hamzalabs.maktaba.core.ui.localWhen
import com.hamzalabs.maktaba.core.ui.stripText
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.File

/** Epic 53 story 53.06 T01-T04: the strip's lines in priority order; off is no outage; Arabic. */
class StatusStripTest {
    private val ok = HomeStatusDoc(
        v = 1,
        components = mapOf("database" to "ok", "api" to "ok", "streaming" to "ok", "processing" to "ok", "search" to "ok", "logs" to "off"),
    )

    private fun with(vararg c: Pair<String, String>, overall: String = "degraded", mode: String = "normal") =
        ok.copy(components = ok.components + c, overall = overall, mode = mode)

    @Test
    fun `nothing when all is well or off`() {
        assertTrue(StatusStripRules.lines(ok, online = true, admin = true).isEmpty())
        assertTrue(StatusStripRules.lines(with("streaming" to "off", "processing" to "off", overall = "ok"), online = true, admin = true).isEmpty())
        assertTrue(StatusStripRules.lines(null, online = true, admin = false).isEmpty())
    }

    @Test
    fun `the priority order`() {
        val s = with("api" to "restarting", "streaming" to "down", overall = "down", mode = "safe")
        assertEquals(
            listOf(StripKey.OFFLINE, StripKey.RESTARTING, StripKey.SAFE, StripKey.STREAMING),
            StatusStripRules.lines(s, online = false, admin = false).map { it.key },
        )
        assertEquals(StripLine(StripKey.DATABASE, "/recovery"), StatusStripRules.lines(with("database" to "down", overall = "down"), online = true, admin = true).first())
    }

    @Test
    fun `admins only lines and details`() {
        val p = with("processing" to "degraded")
        assertTrue(StatusStripRules.lines(p, online = true, admin = false).isEmpty())
        assertEquals(listOf(StripKey.PROCESSING), StatusStripRules.lines(p, online = true, admin = true).map { it.key })
        val tv = with("streaming" to "down")
        assertEquals(null, StatusStripRules.lines(tv, online = true, admin = false).first().details)
        assertEquals("/admin/health", StatusStripRules.lines(tv, online = true, admin = true).first().details)
    }

    @Test
    fun `live tv off is left out of the playback sentence`() {
        val tv = with("streaming" to "down")
        assertEquals(StripKey.STREAMING_NO_TV, StatusStripRules.lines(tv, online = true, admin = false, liveTvOff = true).first().key)
        assertTrue(StatusStripRules.playbackDown(tv))
        assertFalse(StatusStripRules.playbackDown(ok) || StatusStripRules.playbackDown(null))
    }

    @Test
    fun `the document reads`() {
        val d = ApiClient.json.decodeFromString(
            HomeStatusDoc.serializer(),
            """{"v":1,"at":"2026-11-06T10:00:00Z","mode":"safe","overall":"degraded","components":{"streaming":"down"},"features_down":["playback"],"banner":"streaming_down"}""",
        )
        assertEquals("safe", d.mode)
        assertEquals(listOf("playback"), d.featuresDown)
    }

    @Test
    fun `every strip string has its arabic`() {
        val root = generateSequence(File("").absoluteFile) { it.parentFile }.first { File(it, "core/src/main/res/values").exists() }
        val ar = File(root, "core/src/main/res/values-ar/strings.xml").readText()
        for (name in listOf("status_offline", "status_restarting", "status_database", "status_safe", "status_streaming",
            "status_streaming_no_tv", "status_search", "status_processing", "status_logs", "status_details", "status_more",
            "status_close", "status_player")) {
            assertTrue(name, ar.contains("name=\"$name\""))
        }
    }

    /** Epic 50 story 50.11: the cloud's keys have their words; the maintenance window formats. */
    @Test
    fun `every line has its words, the cloud's too`() {
        val ids = StripKey.entries.map { stripText(it) }
        assertEquals("one string per key", StripKey.entries.size, ids.toSet().size)
        assertEquals(R.string.banner_maintenance, stripText(StripKey.MAINTENANCE))
        val (date, time) = localWhen(java.time.Instant.parse("2026-11-04T02:00:00Z").toEpochMilli())
        assertTrue(date.isNotBlank() && time.isNotBlank())
    }
}
